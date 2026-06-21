package supervisor

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// vm is a virtual sandbox backing a container.Fake's ExecFunc. It models the
// control files and a claude binary whose effect is supplied by onClaude.
type vm struct {
	files     map[string]string // absolute path -> contents
	calls     int               // claude invocations
	onClaude  func(call int, files map[string]string)
	clock     time.Time
	clockStep time.Duration
}

func newVM() *vm {
	return &vm{files: map[string]string{}, clock: time.Unix(1_700_000_000, 0)}
}

// fake wires the vm into a container.Fake and a Supervisor.
func (v *vm) fake() *container.Fake {
	f := &container.Fake{}
	f.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		if len(opts.Cmd) == 0 {
			return container.ExecResult{}, errors.New("empty cmd")
		}
		if opts.Cmd[0] == "cat" {
			p := opts.Cmd[1]
			if c, ok := v.files[p]; ok {
				return container.ExecResult{ExitCode: 0, Stdout: c}, nil
			}
			return container.ExecResult{ExitCode: 1, Stderr: "No such file"}, nil
		}
		// claude
		v.calls++
		if v.onClaude != nil {
			v.onClaude(v.calls, v.files)
		}
		v.clock = v.clock.Add(v.clockStep)
		return container.ExecResult{ExitCode: 0}, nil
	}
	return f
}

func (v *vm) sup(f *container.Fake, opts Options) *Supervisor {
	s := New(f, opts)
	s.Clock = func() time.Time { return v.clock }
	return s
}

func statusPath(name string) string { return path.Join(DefaultControlDir, name) }

func TestRunDoneAfterIterations(t *testing.T) {
	v := newVM()
	v.onClaude = func(call int, files map[string]string) {
		if call == 3 {
			files[statusPath(run.StatusFile)] = "DONE\nfinished\n"
		}
	}
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "do the thing", MaxIters: 100, MaxTurns: 50})

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusDone {
		t.Fatalf("Status = %v, want done", res.Status)
	}
	if res.Iterations != 3 {
		t.Fatalf("Iterations = %d, want 3", res.Iterations)
	}
	if v.calls != 3 {
		t.Fatalf("claude calls = %d, want 3", v.calls)
	}
	// VM was torn down.
	if len(f.Stopped()) != 1 || len(f.Removed()) != 1 {
		t.Fatalf("teardown not run: stopped=%v removed=%v", f.Stopped(), f.Removed())
	}
}

func TestRunFailedSentinel(t *testing.T) {
	v := newVM()
	v.onClaude = func(call int, files map[string]string) {
		if call == 1 {
			files[statusPath(run.StatusFile)] = "FAILED: cannot reach deps\n"
		}
	}
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "x", MaxIters: 10})

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusFailed || res.Reason != "cannot reach deps" {
		t.Fatalf("res = %+v, want failed/'cannot reach deps'", res)
	}
	if res.Status.Terminal() != true {
		t.Fatal("failed should be terminal")
	}
}

func TestRunGuardIters(t *testing.T) {
	v := newVM() // STATUS never written
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "x", MaxIters: 4})

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusGuardIters {
		t.Fatalf("Status = %v, want guard_iters", res.Status)
	}
	if res.Iterations != 4 || v.calls != 4 {
		t.Fatalf("Iterations=%d calls=%d, want 4/4", res.Iterations, v.calls)
	}
	if res.Status.Terminal() {
		t.Fatal("guard_iters must not be terminal (job can be relaunched)")
	}
}

func TestRunGuardWall(t *testing.T) {
	v := newVM()
	v.clockStep = 4 * time.Second // each claude call advances 4s
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "x", MaxIters: 1000, MaxWall: 10 * time.Second})

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusGuardWall {
		t.Fatalf("Status = %v, want guard_wall", res.Status)
	}
	// iter1 @0s, iter2 @4s, iter3 @8s run; iter4 @12s >= 10s trips the guard.
	if res.Iterations != 3 || v.calls != 3 {
		t.Fatalf("Iterations=%d calls=%d, want 3/3", res.Iterations, v.calls)
	}
}

func TestRunStopFile(t *testing.T) {
	v := newVM()
	// STOP present from the start: should halt before any claude call.
	v.files[statusPath(run.StopFile)] = "stop\n"
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "x", MaxIters: 10})

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusStopped {
		t.Fatalf("Status = %v, want stopped", res.Status)
	}
	if v.calls != 0 {
		t.Fatalf("claude calls = %d, want 0 (stopped before first iteration)", v.calls)
	}
}

func TestRunStopFileMidway(t *testing.T) {
	v := newVM()
	v.onClaude = func(call int, files map[string]string) {
		if call == 2 {
			files[statusPath(run.StopFile)] = "stop\n"
		}
	}
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "x", MaxIters: 10})

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusStopped {
		t.Fatalf("Status = %v, want stopped", res.Status)
	}
	// claude ran on iter1 and iter2; STOP written during iter2 trips at iter3.
	if v.calls != 2 {
		t.Fatalf("claude calls = %d, want 2", v.calls)
	}
}

func TestRunContextCancelled(t *testing.T) {
	v := newVM()
	f := v.fake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the loop body
	s := v.sup(f, Options{Image: "img", Task: "x", MaxIters: 10})

	res, err := s.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res.Status != StatusStopped {
		t.Fatalf("Status = %v, want stopped", res.Status)
	}
	// Teardown must still have run despite cancellation.
	if len(f.Removed()) != 1 {
		t.Fatalf("teardown not run on cancel: removed=%v", f.Removed())
	}
}

func TestRunStartError(t *testing.T) {
	f := &container.Fake{RunFunc: func(ctx context.Context, opts container.RunOptions) (string, error) {
		return "", errors.New("no kernel")
	}}
	s := New(f, Options{Image: "img", Task: "x"})
	_, err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when VM cannot start")
	}
	// Nothing to tear down.
	if len(f.Stopped()) != 0 {
		t.Fatalf("Stop called despite failed Run: %v", f.Stopped())
	}
}

func TestRunExecError(t *testing.T) {
	f := &container.Fake{ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		return container.ExecResult{}, errors.New("exec failed")
	}}
	s := New(f, Options{Image: "img", Task: "x", MaxIters: 10})
	_, err := s.Run(context.Background())
	if err == nil || err.Error() != "exec failed" {
		t.Fatalf("err = %v, want exec failed", err)
	}
	// Even on exec error the VM should be torn down.
	if len(f.Removed()) != 1 {
		t.Fatalf("teardown not run on exec error: %v", f.Removed())
	}
}

func TestFirstThenContinue(t *testing.T) {
	v := newVM()
	v.onClaude = func(call int, files map[string]string) {
		if call == 2 {
			files[statusPath(run.StatusFile)] = "DONE\n"
		}
	}
	f := v.fake()
	s := v.sup(f, Options{Image: "img", Task: "TASKPROMPT", ResumePrompt: "RESUMEPROMPT", Model: "claude-opus-4-8", MaxTurns: 7, MaxIters: 10})
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	var claude [][]string
	for _, c := range f.CallsOf("Exec") {
		if len(c.Cmd) > 0 && c.Cmd[0] == "claude" {
			claude = append(claude, c.Cmd)
		}
	}
	if len(claude) != 2 {
		t.Fatalf("claude invocations = %d, want 2", len(claude))
	}
	if !contains(claude[0], "-p") || !contains(claude[0], "TASKPROMPT") || contains(claude[0], "--continue") {
		t.Errorf("iter1 args wrong: %v", claude[0])
	}
	if !contains(claude[1], "--continue") || !contains(claude[1], "RESUMEPROMPT") {
		t.Errorf("iter2 args wrong: %v", claude[1])
	}
	for _, c := range claude {
		if !contains(c, "--max-turns") || !contains(c, "7") || !contains(c, "--model") || !contains(c, "claude-opus-4-8") {
			t.Errorf("missing model/max-turns: %v", c)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestRunSetupCommands(t *testing.T) {
	var setupCmds [][]string
	claudeCalls := 0
	f := &container.Fake{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
			switch opts.Cmd[0] {
			case "cat":
				if opts.Cmd[1] == statusPath(run.StatusFile) {
					return container.ExecResult{ExitCode: 0, Stdout: "DONE\n"}, nil
				}
				return container.ExecResult{ExitCode: 1}, nil
			case "claude":
				claudeCalls++
				return container.ExecResult{ExitCode: 0}, nil
			default:
				setupCmds = append(setupCmds, opts.Cmd)
				return container.ExecResult{ExitCode: 0}, nil
			}
		},
	}
	s := New(f, Options{
		Image:    "img",
		Task:     "x",
		MaxIters: 10,
		Setup: [][]string{
			{"git", "config", "--global", "user.name", "Matthew Szatmary"},
			{"sh", "-c", "git clone repo ."},
		},
	})
	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusDone {
		t.Fatalf("Status = %v", res.Status)
	}
	if len(setupCmds) != 2 {
		t.Fatalf("setup commands run = %d, want 2", len(setupCmds))
	}
	if setupCmds[0][0] != "git" || setupCmds[1][0] != "sh" {
		t.Errorf("setup commands wrong: %v", setupCmds)
	}
}

func TestRunSetupFailureAborts(t *testing.T) {
	f := &container.Fake{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
			if opts.Cmd[0] == "setup-fail" {
				return container.ExecResult{ExitCode: 1, Stderr: "boom"}, nil
			}
			return container.ExecResult{ExitCode: 0}, nil
		},
	}
	s := New(f, Options{Image: "img", Task: "x", MaxIters: 10, Setup: [][]string{{"setup-fail"}}})
	if _, err := s.Run(context.Background()); err == nil {
		t.Fatal("expected setup failure to abort the run")
	}
	// VM still torn down.
	if len(f.Removed()) != 1 {
		t.Errorf("teardown not run after setup failure: %v", f.Removed())
	}
}

// TestClaudeNonZeroExitBacksOffAndAborts covers C2: a claude that keeps exiting
// non-zero without writing STATUS must not hot-loop to a benign guard trip. The
// supervisor must back off between attempts and surface a distinct, non-success
// error after MaxClaudeErrors consecutive failures.
func TestClaudeNonZeroExitBacksOffAndAborts(t *testing.T) {
	claudeCalls := 0
	f := &container.Fake{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
			if opts.Cmd[0] == "cat" {
				return container.ExecResult{ExitCode: 1}, nil // STATUS/STOP absent
			}
			claudeCalls++
			return container.ExecResult{ExitCode: 7}, nil // claude fails every time
		},
	}
	var sleeps []time.Duration
	s := New(f, Options{Image: "img", Task: "x", MaxIters: 100, MaxClaudeErrors: 3})
	s.Sleep = func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }

	res, err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error for persistent non-zero claude exit")
	}
	if res.Status != StatusClaudeError {
		t.Fatalf("Status = %v, want claude_error", res.Status)
	}
	if res.Status.Terminal() {
		t.Fatal("claude_error must not be terminal-as-success/DONE")
	}
	if res.Status == StatusGuardIters {
		t.Fatal("must not be misreported as a benign guard_iters trip")
	}
	// 3 consecutive failures: backoff before attempts 2 and 3, then abort.
	if claudeCalls != 3 {
		t.Fatalf("claude calls = %d, want 3", claudeCalls)
	}
	if len(sleeps) != 2 {
		t.Fatalf("backoff sleeps = %d (%v), want 2", len(sleeps), sleeps)
	}
	if !(sleeps[1] > sleeps[0]) {
		t.Fatalf("backoff must increase: %v", sleeps)
	}
	// Teardown still ran.
	if len(f.Removed()) != 1 {
		t.Fatalf("teardown not run: %v", f.Removed())
	}
}

// TestClaudeNonZeroThenRecovers verifies a transient non-zero exit does not
// abort: the counter resets on a clean exit (C2).
func TestClaudeNonZeroThenRecovers(t *testing.T) {
	v := newVM()
	v.onClaude = func(call int, files map[string]string) {
		if call == 3 {
			files[statusPath(run.StatusFile)] = "DONE\n"
		}
	}
	// Make the first claude exit non-zero, the rest zero.
	f := &container.Fake{}
	calls := 0
	f.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		if opts.Cmd[0] == "cat" {
			if c, ok := v.files[opts.Cmd[1]]; ok {
				return container.ExecResult{ExitCode: 0, Stdout: c}, nil
			}
			return container.ExecResult{ExitCode: 1}, nil
		}
		calls++
		v.calls = calls
		if v.onClaude != nil {
			v.onClaude(calls, v.files)
		}
		if calls == 1 {
			return container.ExecResult{ExitCode: 1}, nil
		}
		return container.ExecResult{ExitCode: 0}, nil
	}
	s := New(f, Options{Image: "img", Task: "x", MaxIters: 100, MaxClaudeErrors: 3})
	s.Sleep = func(ctx context.Context, d time.Duration) error { return nil }
	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusDone {
		t.Fatalf("Status = %v, want done (transient failure should recover)", res.Status)
	}
}

// TestReadControlExecFailureAborts covers C3: a non-"absent" cat exit (e.g. the
// VM is gone) must abort the run rather than be treated as "file absent" and
// looping forever.
func TestReadControlExecFailureAborts(t *testing.T) {
	claudeCalls := 0
	f := &container.Fake{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
			if opts.Cmd[0] == "cat" {
				// exit 2 is not cat's "file not found" (1): treat as VM unreachable.
				return container.ExecResult{ExitCode: 2, Stderr: "exec: container not running"}, nil
			}
			claudeCalls++
			return container.ExecResult{ExitCode: 0}, nil
		},
	}
	s := New(f, Options{Image: "img", Task: "x", MaxIters: 100})
	_, err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected abort on control-read exec failure, got nil")
	}
	if !strings.Contains(err.Error(), "VM unreachable") {
		t.Fatalf("error should explain the VM read failure, got %v", err)
	}
	// Must not have looped: the STOP read fails before the first claude call.
	if claudeCalls != 0 {
		t.Fatalf("claude calls = %d, want 0 (aborted on first control read)", claudeCalls)
	}
	if len(f.Removed()) != 1 {
		t.Fatalf("teardown not run: %v", f.Removed())
	}
}

// TestExecBoundedByWallDeadline covers O3: the claude exec must be given a ctx
// with a deadline derived from the remaining wall budget so a hung claude is
// interrupted; without MaxWall there is no deadline.
func TestExecBoundedByWallDeadline(t *testing.T) {
	check := func(maxWall time.Duration, wantDeadline bool) {
		var sawClaudeDeadline bool
		f := &container.Fake{
			ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
				if opts.Cmd[0] == "cat" {
					if opts.Cmd[1] == statusPath(run.StatusFile) {
						return container.ExecResult{ExitCode: 0, Stdout: "DONE\n"}, nil
					}
					return container.ExecResult{ExitCode: 1}, nil
				}
				_, ok := ctx.Deadline()
				sawClaudeDeadline = ok
				return container.ExecResult{ExitCode: 0}, nil
			},
		}
		s := New(f, Options{Image: "img", Task: "x", MaxIters: 10, MaxWall: maxWall})
		if _, err := s.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if sawClaudeDeadline != wantDeadline {
			t.Fatalf("MaxWall=%v: claude exec deadline present = %v, want %v", maxWall, sawClaudeDeadline, wantDeadline)
		}
	}
	check(time.Hour, true) // bounded by remaining wall budget
	check(0, false)        // no wall budget => no per-exec deadline
}

// TestSecretsFileSourcedNotInArgv covers S1 at the supervisor seam: when a
// SecretsFile is configured, setup and claude commands are wrapped to source it
// (so secrets never appear directly in argv); control reads stay unwrapped.
func TestSecretsFileSourcedNotInArgv(t *testing.T) {
	const secretsFile = "/work/.agentbox/env"
	var claudeWrapped, setupWrapped bool
	f := &container.Fake{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
			joined := strings.Join(opts.Cmd, " ")
			switch {
			case opts.Cmd[0] == "cat":
				// Control reads must NOT be wrapped (no secrets needed).
				if strings.Contains(joined, secretsFile) {
					t.Errorf("control read should not source secrets: %v", opts.Cmd)
				}
				if opts.Cmd[1] == statusPath(run.StatusFile) {
					return container.ExecResult{ExitCode: 0, Stdout: "DONE\n"}, nil
				}
				return container.ExecResult{ExitCode: 1}, nil
			case strings.Contains(joined, "claude"):
				if opts.Cmd[0] == "sh" && strings.Contains(joined, secretsFile) {
					claudeWrapped = true
				}
			case strings.Contains(joined, "setup-marker"):
				if opts.Cmd[0] == "sh" && strings.Contains(joined, secretsFile) {
					setupWrapped = true
				}
			}
			return container.ExecResult{ExitCode: 0}, nil
		},
	}
	s := New(f, Options{
		Image:       "img",
		Task:        "x",
		MaxIters:    10,
		SecretsFile: secretsFile,
		Setup:       [][]string{{"setup-marker"}},
	})
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !claudeWrapped {
		t.Error("claude exec was not wrapped to source the secrets file")
	}
	if !setupWrapped {
		t.Error("setup command was not wrapped to source the secrets file")
	}
}

func TestClaudeArgs(t *testing.T) {
	first := claudeArgs("claude", 1, "TASK", "RESUME", "", 0)
	if first[0] != "claude" || first[1] != "-p" || first[2] != "TASK" {
		t.Errorf("first = %v", first)
	}
	if contains(first, "--max-turns") || contains(first, "--model") {
		t.Errorf("first should omit unset flags: %v", first)
	}
	if !contains(first, "--dangerously-skip-permissions") {
		t.Errorf("first should skip permissions: %v", first)
	}
	cont := claudeArgs("", 2, "TASK", "RESUME", "m", 9)
	if cont[1] != "-p" || cont[2] != "--continue" || cont[3] != "RESUME" {
		t.Errorf("continue = %v", cont)
	}
}
