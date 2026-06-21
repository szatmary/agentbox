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
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
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
	// StatusClaudeError — claude exited non-zero on consecutive iterations
	// without writing STATUS (no progress). Surfaced alongside an error so the
	// run is not misreported as a benign guard trip. See C2.
	StatusClaudeError Status = "claude_error"
)

// DefaultMaxClaudeErrors is how many consecutive non-zero claude exits without
// progress (no STATUS) are tolerated before the run aborts. See C2.
const DefaultMaxClaudeErrors = 3

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
	// Setup are commands run once inside the VM after it starts and before the
	// first iteration (e.g. install credentials, configure git, clone the repo).
	// Each must exit 0; a non-zero exit or launch error aborts the run.
	Setup [][]string

	// SecretsFile, when non-empty, is the in-VM path of a 0600 file holding
	// secret env assignments. Every setup and claude command is wrapped to source
	// it (set -a) so secrets reach the process environment without ever appearing
	// in `container run`/`container exec` argv (visible via ps/inspect). See S1.
	SecretsFile string
	// MaxClaudeErrors caps consecutive non-zero claude exits without progress
	// before the run aborts; <=0 means DefaultMaxClaudeErrors. See C2.
	MaxClaudeErrors int
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
	// Sleep waits for d or until ctx is cancelled, returning ctx.Err() if
	// cancelled. Defaults to a real timer. Injectable for tests (claude-error
	// backoff). See C2.
	Sleep func(ctx context.Context, d time.Duration) error
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

// sleep waits for d, returning early with ctx.Err() if the context is cancelled.
func (s *Supervisor) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (s *Supervisor) maxClaudeErrors() int {
	if s.opts.MaxClaudeErrors > 0 {
		return s.opts.MaxClaudeErrors
	}
	return DefaultMaxClaudeErrors
}

// claudeBackoff returns the backoff before retrying after the nth consecutive
// failing claude exit (n starts at 1): 1s, 2s, 4s, … capped at 60s.
func claudeBackoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	d := time.Second << (n - 1)
	if d <= 0 || d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}

// wrapCmd wraps a command so it sources the secrets file (exporting its
// assignments) before exec-ing the original argv. When no secrets file is
// configured the command is returned unchanged. Keeping secrets in a sourced
// file (not argv) is the whole point of S1.
func (s *Supervisor) wrapCmd(cmd []string) []string {
	if s.opts.SecretsFile == "" || len(cmd) == 0 {
		return cmd
	}
	f := shellSingleQuote(s.opts.SecretsFile)
	// set -a so sourced assignments are exported; guard on existence; exec the
	// original argv via "$@" so no re-quoting of the original command is needed.
	script := "set -a; [ -f " + f + " ] && . " + f + "; set +a; exec \"$@\""
	return append([]string{"sh", "-c", script, "sh"}, cmd...)
}

// shellSingleQuote single-quotes s for safe inclusion in a /bin/sh script.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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

	if err := s.runSetup(ctx, id); err != nil {
		return Result{}, err
	}

	start := s.now()
	claudeErrors := 0
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

		// O3: bound the in-flight exec by the remaining wall budget so a hung
		// claude is interrupted (not left running until the next guard check,
		// which only fires between iterations).
		execCtx := ctx
		var cancel context.CancelFunc
		if s.opts.MaxWall > 0 {
			if remaining := s.opts.MaxWall - s.elapsed(start); remaining > 0 {
				execCtx, cancel = context.WithTimeout(ctx, remaining)
			}
		}
		res, err := s.rt.Exec(execCtx, id, container.ExecOptions{
			Cmd:     s.wrapCmd(args),
			Workdir: s.opts.Workdir,
			Stdout:  s.opts.LogOut,
			Stderr:  s.opts.LogOut,
		})
		if cancel != nil {
			cancel()
		}
		if err != nil {
			// A per-exec deadline (wall budget) firing while the parent context
			// is still live is a wall-clock guard trip, not an infra failure.
			if ctx.Err() == nil && errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				s.logf("guard: wall-clock budget exhausted mid-exec", "iter", iter, "max_wall", s.opts.MaxWall)
				return Result{Status: StatusGuardWall, Iterations: iter, Elapsed: s.elapsed(start)}, nil
			}
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

		// C2: a claude that exits non-zero without writing STATUS made no
		// progress. Without inspecting the exit code this hot-loops through
		// MaxIters with no backoff and is then misreported as a benign guard
		// trip. Log it, back off, and abort after too many consecutive failures.
		if res.ExitCode != 0 {
			claudeErrors++
			s.logf("claude exited non-zero without progress",
				"iter", iter, "exit_code", res.ExitCode, "consecutive", claudeErrors)
			if claudeErrors >= s.maxClaudeErrors() {
				return Result{Status: StatusClaudeError, Iterations: iter, Elapsed: s.elapsed(start)},
					fmt.Errorf("claude exited non-zero on %d consecutive iterations without progress (last exit %d)",
						claudeErrors, res.ExitCode)
			}
			if err := s.sleep(ctx, claudeBackoff(claudeErrors)); err != nil {
				return Result{Status: StatusStopped, Iterations: iter, Elapsed: s.elapsed(start)}, err
			}
		} else {
			claudeErrors = 0
		}
	}
}

// runSetup executes the one-time setup commands inside the VM.
func (s *Supervisor) runSetup(ctx context.Context, id string) error {
	for i, cmd := range s.opts.Setup {
		if len(cmd) == 0 {
			continue
		}
		s.logf("setup command", "index", i)
		res, err := s.rt.Exec(ctx, id, container.ExecOptions{
			Cmd:     s.wrapCmd(cmd),
			Workdir: s.opts.Workdir,
			Stdout:  s.opts.LogOut,
			Stderr:  s.opts.LogOut,
		})
		if err != nil {
			return fmt.Errorf("setup command %d: %w", i, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("setup command %d exited %d: %s", i, res.ExitCode, res.Stderr)
		}
	}
	return nil
}

func (s *Supervisor) elapsed(start time.Time) time.Duration { return s.now().Sub(start) }

// readFile reads a control file from the VM via `cat`. It distinguishes a
// genuinely-absent file (cat exits 1) from an exec failure such as a dead VM or
// permission error (any other non-zero exit), which it surfaces as an error
// rather than silently reporting "file absent". See C3.
func (s *Supervisor) readFile(ctx context.Context, id, name string) (content string, exists bool, err error) {
	p := path.Join(s.opts.ControlDir, name)
	res, err := s.rt.Exec(ctx, id, container.ExecOptions{Cmd: []string{"cat", p}})
	if err != nil {
		return "", false, err
	}
	switch res.ExitCode {
	case 0:
		return res.Stdout, true, nil
	case 1:
		// cat's exit status for "No such file or directory": genuinely absent.
		return "", false, nil
	default:
		return "", false, fmt.Errorf("reading control file %s: cat exited %d (VM unreachable?): %s",
			name, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
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
