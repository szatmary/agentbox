// Package autorun implements agentbox's continuous relaunch loop.
//
// It runs bounded supervised sessions back-to-back until the job is genuinely
// finished. Between runs it detects progress by comparing the remote git HEAD
// (the agent pushes its work). The loop ends when the agent reports DONE/FAILED,
// when N consecutive runs make no progress, when an optional run cap is reached,
// or when a stop is requested. The decision logic is pure over the [Runner] and
// [HeadProbe] interfaces and an injectable sleep, so it is table-tested with
// fakes.
package autorun

import (
	"context"
	"time"

	"github.com/szatmary/agentbox/internal/supervisor"
)

// Status is the terminal status of an autorun loop.
type Status string

const (
	// StatusDone — a run reported DONE.
	StatusDone Status = "done"
	// StatusFailed — a run reported FAILED.
	StatusFailed Status = "failed"
	// StatusNoProgress — MaxNoProgress consecutive runs changed nothing.
	StatusNoProgress Status = "no_progress"
	// StatusMaxRuns — the optional run cap was reached.
	StatusMaxRuns Status = "max_runs"
	// StatusStopped — a stop was requested (signal/STOP file/cancelled context).
	StatusStopped Status = "stopped"
)

// Runner performs a single bounded supervised run.
type Runner interface {
	RunOnce(ctx context.Context) (supervisor.Result, error)
}

// HeadProbe returns the current remote git HEAD commit. Implementations should
// return a stable empty string and a nil error when HEAD cannot be determined
// but that is not an error worth aborting on; return a non-nil error only for
// genuine failures (which the loop treats as "progress unknown").
type HeadProbe interface {
	RemoteHead(ctx context.Context) (string, error)
}

// Options configures the autorun loop.
type Options struct {
	// MaxNoProgress stops the loop after this many consecutive no-progress runs.
	// Must be > 0 when a HeadProbe is set.
	MaxNoProgress int
	// Cooldown is the pause between runs.
	Cooldown time.Duration
	// MaxRuns optionally caps total runs (0 = unlimited).
	MaxRuns int
}

// Logger is the minimal logging surface autorun needs.
type Logger interface {
	Info(msg string, args ...any)
}

// Autorun is the relaunch loop.
type Autorun struct {
	Runner Runner
	// Probe detects progress via remote HEAD. Nil disables HEAD-based progress
	// detection: each non-terminal run then counts as no-progress (so the loop
	// still terminates via MaxNoProgress), per DECISIONS D6.
	Probe   HeadProbe
	Options Options

	// Sleep waits for d or until ctx is cancelled, returning ctx.Err() if
	// cancelled. Defaults to a real timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// StopRequested, if set, is polled before each run; true ends the loop.
	StopRequested func() bool
	// Log receives non-secret progress messages; may be nil.
	Log Logger
}

// Result reports the outcome of the loop.
type Result struct {
	Status Status
	Runs   int
	// Last is the result of the final supervised run (zero if none ran).
	Last supervisor.Result
	// Reason carries extra detail (e.g. the FAILED sentinel reason).
	Reason string
}

func (a *Autorun) logf(msg string, args ...any) {
	if a.Log != nil {
		a.Log.Info(msg, args...)
	}
}

func (a *Autorun) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	if a.Sleep != nil {
		return a.Sleep(ctx, d)
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

// Run drives the loop to a terminal condition. It returns an error only for an
// infrastructure failure from a run; all expected outcomes are reported via the
// Result's Status.
func (a *Autorun) Run(ctx context.Context) (Result, error) {
	var prevHead string
	var haveHead bool
	noProgress := 0
	var last supervisor.Result

	for run := 1; ; run++ {
		if err := ctx.Err(); err != nil {
			return Result{Status: StatusStopped, Runs: run - 1, Last: last}, err
		}
		if a.StopRequested != nil && a.StopRequested() {
			a.logf("stop requested; ending autorun")
			return Result{Status: StatusStopped, Runs: run - 1, Last: last}, nil
		}
		if a.Options.MaxRuns > 0 && run > a.Options.MaxRuns {
			a.logf("run cap reached", "max_runs", a.Options.MaxRuns)
			return Result{Status: StatusMaxRuns, Runs: run - 1, Last: last}, nil
		}

		a.logf("run start", "run", run)
		res, err := a.Runner.RunOnce(ctx)
		if err != nil {
			return Result{Status: StatusStopped, Runs: run, Last: last}, err
		}
		last = res

		if res.Status.Terminal() {
			st := StatusDone
			if res.Status == supervisor.StatusFailed {
				st = StatusFailed
			}
			a.logf("job finished", "status", string(st), "run", run)
			return Result{Status: st, Runs: run, Last: res, Reason: res.Reason}, nil
		}

		switch progress := a.progressed(ctx, &prevHead, &haveHead); progress {
		case progressYes:
			noProgress = 0
		case progressNo:
			noProgress++
		case progressUnknown:
			// Leave the counter unchanged; rely on terminal/MaxRuns to stop.
		}
		a.logf("run complete", "run", run, "status", string(res.Status), "no_progress", noProgress)

		if a.Options.MaxNoProgress > 0 && noProgress >= a.Options.MaxNoProgress {
			a.logf("no progress; ending autorun", "consecutive", noProgress)
			return Result{Status: StatusNoProgress, Runs: run, Last: res}, nil
		}

		if err := a.sleep(ctx, a.Options.Cooldown); err != nil {
			return Result{Status: StatusStopped, Runs: run, Last: res}, err
		}
	}
}

type progressKind int

const (
	progressUnknown progressKind = iota
	progressYes
	progressNo
)

// progressed determines whether the latest run advanced the remote HEAD. With
// no probe, every run counts as no-progress (HEAD detection disabled).
func (a *Autorun) progressed(ctx context.Context, prevHead *string, haveHead *bool) progressKind {
	if a.Probe == nil {
		return progressNo
	}
	head, err := a.Probe.RemoteHead(ctx)
	if err != nil {
		a.logf("head probe failed; progress unknown", "err", err.Error())
		return progressUnknown
	}
	defer func() { *prevHead = head; *haveHead = true }()
	if !*haveHead {
		// First observation establishes a baseline; not counted either way.
		return progressUnknown
	}
	if head == *prevHead {
		return progressNo
	}
	return progressYes
}
