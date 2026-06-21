// Package supervisor implements agentbox's per-run resume loop.
//
// A Supervisor starts one long-lived sandbox VM, then drives a Claude agent
// inside it via repeated `container exec` calls: it runs `claude -p <task>` on
// the first iteration and `claude --continue <resume>` afterwards, checking the
// STATUS sentinel file after each iteration and the guards (wall clock,
// iteration count, per-call turn cap, STOP file) before each. When the agent
// declares completion, a guard trips, or a stop is requested, it stops and
// removes the VM.
//
// The loop is pure logic over the [container.Runtime] interface and an
// injectable clock, so its stop/guard/sentinel decisions are table-tested
// against a fake runtime with no real container, `claude`, or VM.
package supervisor

import (
	"context"
	"io"
	"path"
	"time"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// Status is the terminal status of a supervised run.
type Status string

const (
	// StatusDone — the agent wrote STATUS with first line "DONE".
	StatusDone Status = "done"
	// StatusFailed — the agent wrote STATUS with first line "FAILED".
	StatusFailed Status = "failed"
	// StatusGuardWall — the wall-clock budget was exhausted.
	StatusGuardWall Status = "guard_wall"
	// StatusGuardIters — the iteration cap was reached.
	StatusGuardIters Status = "guard_iters"
	// StatusStopped — a STOP file requested a graceful halt.
	StatusStopped Status = "stopped"
)

// Terminal reports whether a status means the overall job is finished and
// should not be relaunched (DONE or FAILED).
func (s Status) Terminal() bool { return s == StatusDone || s == StatusFailed }

// Default control directory inside the sandbox and resume prompt.
const (
	DefaultControlDir   = "/work/control"
	DefaultWorkdir      = "/work/workspace"
	DefaultResumePrompt = "Continue the task. Read ROADMAP.md and PROGRESS.md, keep " +
		"making and committing progress, and write the STATUS file (first line DONE " +
		"or FAILED) only when the whole task is genuinely complete."
)

// Options configures a supervised run.
type Options struct {
	// Image is the sandbox image to run.
	Image string
	// Name is an optional stable container name.
	Name string
	// Task is the prompt passed to `claude -p` on the first iteration.
	Task string
	// ResumePrompt is passed to `claude --continue` on later iterations.
	ResumePrompt string
	// Model is the optional Claude model id.
	Model string
	// MaxWall bounds total wall-clock time.
	MaxWall time.Duration
	// MaxIters caps the number of iterations.
	MaxIters int
	// MaxTurns caps agent turns per claude invocation.
	MaxTurns int
	// Env are environment variables for the VM (may be secret; never logged).
	Env map[string]string
	// Mounts are host directories bound into the VM.
	Mounts []container.Mount
	// Workdir is the working directory inside the VM.
	Workdir string
	// ControlDir is the control directory inside the VM (holds STATUS/STOP).
	ControlDir string

	// ClaudeBin overrides the claude binary name (default "claude").
	ClaudeBin string
	// LogOut, if non-nil, receives streamed claude stdout/stderr per iteration.
	LogOut io.Writer
}

func (o *Options) setDefaults() {
	if o.ResumePrompt == "" {
		o.ResumePrompt = DefaultResumePrompt
	}
	if o.ControlDir == "" {
		o.ControlDir = DefaultControlDir
	}
	if o.Workdir == "" {
		o.Workdir = DefaultWorkdir
	}
}

// Result reports the outcome of a supervised run.
type Result struct {
	Status     Status
	Iterations int
	Reason     string        // for StatusFailed, the reason from the sentinel
	Elapsed    time.Duration // wall-clock time spent in the loop
}

// Logger is the minimal logging surface the supervisor needs.
type Logger interface {
	Info(msg string, args ...any)
}

// Supervisor drives a single run.
type Supervisor struct {
	rt   container.Runtime
	opts Options

	// Clock returns the current time; defaults to time.Now. Injectable for tests.
	Clock func() time.Time
	// Log receives non-secret progress messages; may be nil.
	Log Logger
}

// New constructs a Supervisor.
func New(rt container.Runtime, opts Options) *Supervisor {
	opts.setDefaults()
	return &Supervisor{rt: rt, opts: opts, Clock: time.Now}
}

func (s *Supervisor) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *Supervisor) logf(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Info(msg, args...)
	}
}

// Run starts the VM, drives the resume loop to a terminal condition, then stops
// and removes the VM (best-effort). It returns the Result, or an error for an
// infrastructure failure (VM could not start, exec could not be launched, or
// the context was cancelled).
func (s *Supervisor) Run(ctx context.Context) (Result, error) {
	id, err := s.rt.Run(ctx, container.RunOptions{
		Image:      s.opts.Image,
		Name:       s.opts.Name,
		Env:        s.opts.Env,
		Mounts:     s.opts.Mounts,
		Workdir:    s.opts.Workdir,
		Entrypoint: []string{"sleep"},
		Cmd:        []string{"infinity"},
	})
	if err != nil {
		return Result{}, err
	}
	defer s.teardown(id)

	start := s.now()
	for iter := 1; ; iter++ {
		if err := ctx.Err(); err != nil {
			return Result{Status: StatusStopped, Iterations: iter - 1, Elapsed: s.elapsed(start)}, err
		}
		if s.opts.MaxIters > 0 && iter > s.opts.MaxIters {
			s.logf("guard: max iterations reached", "max_iters", s.opts.MaxIters)
			return Result{Status: StatusGuardIters, Iterations: iter - 1, Elapsed: s.elapsed(start)}, nil
		}
		if s.opts.MaxWall > 0 && s.elapsed(start) >= s.opts.MaxWall {
			s.logf("guard: wall-clock budget exhausted", "max_wall", s.opts.MaxWall)
			return Result{Status: StatusGuardWall, Iterations: iter - 1, Elapsed: s.elapsed(start)}, nil
		}
		if stop, err := s.fileExists(ctx, id, run.StopFile); err != nil {
			return Result{}, err
		} else if stop {
			s.logf("stop file present; halting")
			return Result{Status: StatusStopped, Iterations: iter - 1, Elapsed: s.elapsed(start)}, nil
		}

		args := claudeArgs(s.opts.ClaudeBin, iter, s.opts.Task, s.opts.ResumePrompt, s.opts.Model, s.opts.MaxTurns)
		s.logf("iteration start", "iter", iter)
		if _, err := s.rt.Exec(ctx, id, container.ExecOptions{
			Cmd:     args,
			Workdir: s.opts.Workdir,
			Stdout:  s.opts.LogOut,
			Stderr:  s.opts.LogOut,
		}); err != nil {
			return Result{}, err
		}

		content, exists, err := s.readFile(ctx, id, run.StatusFile)
		if err != nil {
			return Result{}, err
		}
		if exists {
			if sent := run.ParseStatus(content); sent.Terminal() {
				if sent.Done {
					s.logf("agent reported DONE", "iter", iter)
					return Result{Status: StatusDone, Iterations: iter, Elapsed: s.elapsed(start)}, nil
				}
				s.logf("agent reported FAILED", "iter", iter, "reason", sent.Reason)
				return Result{Status: StatusFailed, Iterations: iter, Reason: sent.Reason, Elapsed: s.elapsed(start)}, nil
			}
		}
	}
}

func (s *Supervisor) elapsed(start time.Time) time.Duration { return s.now().Sub(start) }

// readFile reads a control file from the VM via `cat`. A non-zero exit code is
// interpreted as "file absent" (exists=false).
func (s *Supervisor) readFile(ctx context.Context, id, name string) (content string, exists bool, err error) {
	p := path.Join(s.opts.ControlDir, name)
	res, err := s.rt.Exec(ctx, id, container.ExecOptions{Cmd: []string{"cat", p}})
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	return res.Stdout, true, nil
}

func (s *Supervisor) fileExists(ctx context.Context, id, name string) (bool, error) {
	_, exists, err := s.readFile(ctx, id, name)
	return exists, err
}

// teardown stops and removes the VM with a fresh, bounded context so cleanup
// still runs even if the caller's context was cancelled.
func (s *Supervisor) teardown(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.rt.Stop(ctx, id); err != nil {
		s.logf("teardown: stop failed", "id", id, "err", err.Error())
	}
	if err := s.rt.Remove(ctx, id); err != nil {
		s.logf("teardown: remove failed", "id", id, "err", err.Error())
	}
}
