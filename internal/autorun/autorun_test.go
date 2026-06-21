package autorun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/szatmary/agentbox/internal/supervisor"
)

// scriptRunner returns a queued sequence of results, then repeats the last one.
type scriptRunner struct {
	results []supervisor.Result
	err     error
	calls   int
}

func (r *scriptRunner) RunOnce(ctx context.Context) (supervisor.Result, error) {
	if r.err != nil {
		return supervisor.Result{}, r.err
	}
	i := r.calls
	r.calls++
	if i < len(r.results) {
		return r.results[i], nil
	}
	if len(r.results) > 0 {
		return r.results[len(r.results)-1], nil
	}
	return supervisor.Result{Status: supervisor.StatusGuardIters}, nil
}

// scriptProbe returns a queued sequence of heads/errors.
type scriptProbe struct {
	heads []string
	errs  []error
	calls int
}

func (p *scriptProbe) RemoteHead(ctx context.Context) (string, error) {
	i := p.calls
	p.calls++
	var h string
	var e error
	if i < len(p.heads) {
		h = p.heads[i]
	}
	if i < len(p.errs) {
		e = p.errs[i]
	}
	return h, e
}

func noSleep() func(ctx context.Context, d time.Duration) error {
	return func(ctx context.Context, d time.Duration) error { return ctx.Err() }
}

func guard() supervisor.Result { return supervisor.Result{Status: supervisor.StatusGuardIters} }

func TestAutorunStopsOnDone(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{
		guard(),
		{Status: supervisor.StatusDone},
	}}
	a := &Autorun{
		Runner:  r,
		Probe:   &scriptProbe{heads: []string{"h1", "h2"}},
		Options: Options{MaxNoProgress: 5},
		Sleep:   noSleep(),
	}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone || res.Runs != 2 {
		t.Fatalf("res = %+v, want done/2", res)
	}
}

func TestAutorunStopsOnFailed(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{
		{Status: supervisor.StatusFailed, Reason: "deps unreachable"},
	}}
	a := &Autorun{Runner: r, Options: Options{MaxNoProgress: 5}, Sleep: noSleep()}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed || res.Reason != "deps unreachable" || res.Runs != 1 {
		t.Fatalf("res = %+v, want failed/'deps unreachable'/1", res)
	}
}

func TestAutorunNoProgressStops(t *testing.T) {
	// HEAD never changes after the baseline => consecutive no-progress.
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	a := &Autorun{
		Runner:  r,
		Probe:   &scriptProbe{heads: []string{"same", "same", "same", "same"}},
		Options: Options{MaxNoProgress: 2},
		Sleep:   noSleep(),
	}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusNoProgress {
		t.Fatalf("Status = %v, want no_progress", res.Status)
	}
	// run1 baseline (unknown), run2 no-progress(1), run3 no-progress(2)=>stop.
	if res.Runs != 3 {
		t.Fatalf("Runs = %d, want 3", res.Runs)
	}
}

func TestAutorunProgressResetsCounter(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	// baseline a, then a (no-progress 1), then b (progress resets to 0),
	// then b (1), then b (2)=>stop. Runs: 1..5.
	a := &Autorun{
		Runner:  r,
		Probe:   &scriptProbe{heads: []string{"a", "a", "b", "b", "b"}},
		Options: Options{MaxNoProgress: 2},
		Sleep:   noSleep(),
	}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusNoProgress || res.Runs != 5 {
		t.Fatalf("res = %+v, want no_progress/5", res)
	}
}

func TestAutorunNoProbeCountsEveryRun(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	a := &Autorun{Runner: r, Probe: nil, Options: Options{MaxNoProgress: 3}, Sleep: noSleep()}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// No probe: every run is no-progress; stops at run 3.
	if res.Status != StatusNoProgress || res.Runs != 3 {
		t.Fatalf("res = %+v, want no_progress/3", res)
	}
}

func TestAutorunProbeErrorIsUnknown(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	// Probe always errors => progress unknown => counter never increments.
	// With MaxRuns cap the loop still terminates.
	a := &Autorun{
		Runner:  r,
		Probe:   &scriptProbe{errs: []error{errors.New("net"), errors.New("net"), errors.New("net"), errors.New("net")}},
		Options: Options{MaxNoProgress: 2, MaxRuns: 3},
		Sleep:   noSleep(),
	}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusMaxRuns || res.Runs != 3 {
		t.Fatalf("res = %+v, want max_runs/3 (probe errors must not trip no-progress)", res)
	}
}

func TestAutorunMaxRuns(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	a := &Autorun{
		Runner:  r,
		Probe:   &scriptProbe{heads: []string{"a", "b", "c", "d"}}, // always progressing
		Options: Options{MaxNoProgress: 100, MaxRuns: 3},
		Sleep:   noSleep(),
	}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusMaxRuns || res.Runs != 3 {
		t.Fatalf("res = %+v, want max_runs/3", res)
	}
}

func TestAutorunStopRequested(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	stopAfter := 0
	a := &Autorun{
		Runner:        r,
		Options:       Options{MaxNoProgress: 100},
		Sleep:         noSleep(),
		StopRequested: func() bool { stopAfter++; return stopAfter > 2 },
	}
	res, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusStopped {
		t.Fatalf("Status = %v, want stopped", res.Status)
	}
	if res.Runs != 2 {
		t.Fatalf("Runs = %d, want 2", res.Runs)
	}
}

func TestAutorunRunnerError(t *testing.T) {
	r := &scriptRunner{err: errors.New("vm start failed")}
	a := &Autorun{Runner: r, Options: Options{MaxNoProgress: 5}, Sleep: noSleep()}
	res, err := a.Run(context.Background())
	if err == nil || err.Error() != "vm start failed" {
		t.Fatalf("err = %v, want vm start failed", err)
	}
	if res.Status != StatusStopped {
		t.Fatalf("Status = %v, want stopped", res.Status)
	}
}

func TestAutorunContextCancelled(t *testing.T) {
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &Autorun{Runner: r, Options: Options{MaxNoProgress: 5}, Sleep: noSleep()}
	_, err := a.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAutorunCooldownCancelled(t *testing.T) {
	// Sleep returns a cancellation error, ending the loop after one run.
	r := &scriptRunner{results: []supervisor.Result{guard()}}
	a := &Autorun{
		Runner:  r,
		Options: Options{MaxNoProgress: 100, Cooldown: time.Hour},
		Sleep:   func(ctx context.Context, d time.Duration) error { return context.Canceled },
	}
	_, err := a.Run(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled from cooldown", err)
	}
}

func TestDefaultSleepReturnsImmediatelyForZero(t *testing.T) {
	a := &Autorun{}
	if err := a.sleep(context.Background(), 0); err != nil {
		t.Fatalf("sleep(0) = %v, want nil", err)
	}
}

func TestDefaultSleepHonorsTimer(t *testing.T) {
	a := &Autorun{}
	start := time.Now()
	if err := a.sleep(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 5*time.Millisecond {
		t.Error("sleep returned too quickly")
	}
}
