package observe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// makeRun creates a run directory and returns its host path; its base name is
// the container name the Observer resolves to.
func makeRun(t *testing.T, name, id string) string {
	t.Helper()
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, err := run.Create(runsDir, name, id)
	if err != nil {
		t.Fatalf("run.Create: %v", err)
	}
	r.Close()
	return r.Root
}

func TestNewRejectsNonRunDir(t *testing.T) {
	if _, err := New(&container.Fake{}, t.TempDir()); err == nil {
		t.Error("expected error for a non-run directory")
	}
	if _, err := New(nil, t.TempDir()); err == nil {
		t.Error("expected error for nil runtime")
	}
}

func TestResolveRequiresRunning(t *testing.T) {
	runDir := makeRun(t, "job", "20260101T000000Z")
	name := filepath.Base(runDir)

	// Not running: in-VM ops fail with ErrNotRunning; no Exec is attempted.
	fake := &container.Fake{}
	o, err := New(fake, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.ReadFile(context.Background(), "/x"); err == nil {
		t.Fatal("expected ErrNotRunning")
	} else if !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want not running", err)
	}
	if len(fake.CallsOf("Exec")) != 0 {
		t.Error("must not Exec when the container is not running")
	}

	// Running: ops go through.
	fake.SetRunning(name, true)
	if _, err := o.GitStatus(context.Background()); err != nil {
		t.Fatalf("GitStatus when running: %v", err)
	}
}

func TestExecMethodsArgv(t *testing.T) {
	runDir := makeRun(t, "job", "20260101T000000Z")
	name := filepath.Base(runDir)
	fake := &container.Fake{}
	fake.SetRunning(name, true)
	fake.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		return container.ExecResult{ExitCode: 0, Stdout: "OK:" + strings.Join(opts.Cmd, " ")}, nil
	}
	o, err := New(fake, runDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	cases := []struct {
		desc string
		call func() (string, error)
		want []string // argv substrings expected, in order
	}{
		{"ListFiles default", func() (string, error) { return o.ListFiles(ctx, "") },
			[]string{"ls", "-la", "--", DefaultWorkdir}},
		{"ListFiles path", func() (string, error) { return o.ListFiles(ctx, "/etc") },
			[]string{"ls", "-la", "--", "/etc"}},
		{"ReadFile", func() (string, error) { return o.ReadFile(ctx, "/work/output/x") },
			[]string{"cat", "--", "/work/output/x"}},
		{"GitStatus", func() (string, error) { return o.GitStatus(ctx) },
			[]string{"git", "-C", DefaultWorkdir, "status", "--porcelain=v1", "-b"}},
		{"GitDiff", func() (string, error) { return o.GitDiff(ctx, false) },
			[]string{"git", "-C", DefaultWorkdir, "diff"}},
		{"GitDiff staged", func() (string, error) { return o.GitDiff(ctx, true) },
			[]string{"git", "-C", DefaultWorkdir, "diff", "--cached"}},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			before := len(fake.CallsOf("Exec"))
			if _, err := tc.call(); err != nil {
				t.Fatalf("%s: %v", tc.desc, err)
			}
			calls := fake.CallsOf("Exec")
			if len(calls) != before+1 {
				t.Fatalf("%s: expected one Exec, got %d new", tc.desc, len(calls)-before)
			}
			got := calls[len(calls)-1]
			if got.Workdir != DefaultWorkdir {
				t.Errorf("%s: workdir = %q", tc.desc, got.Workdir)
			}
			if strings.Join(got.Cmd, " ") != strings.Join(tc.want, " ") {
				t.Errorf("%s: argv = %v, want %v", tc.desc, got.Cmd, tc.want)
			}
		})
	}
}

func TestReadFileNotFound(t *testing.T) {
	runDir := makeRun(t, "job", "20260101T000000Z")
	name := filepath.Base(runDir)
	fake := &container.Fake{}
	fake.SetRunning(name, true)
	fake.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		return container.ExecResult{ExitCode: 1, Stderr: "cat: no such file"}, nil
	}
	o, _ := New(fake, runDir)
	if _, err := o.ReadFile(context.Background(), "/nope"); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("err = %v, want no such file", err)
	}
}

func TestExecErrorSurfacing(t *testing.T) {
	runDir := makeRun(t, "job", "20260101T000000Z")
	name := filepath.Base(runDir)
	fake := &container.Fake{}
	fake.SetRunning(name, true)
	fake.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		return container.ExecResult{ExitCode: 2, Stderr: "boom"}, nil
	}
	o, _ := New(fake, runDir)
	if _, err := o.ListFiles(context.Background(), "/x"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("ListFiles err = %v, want boom surfaced", err)
	}
	if _, err := o.GitStatus(context.Background()); err == nil {
		t.Error("GitStatus should surface a non-zero exit")
	}
	// Exec returns the raw result, not an error, on non-zero exit.
	res, err := o.Exec(context.Background(), "false")
	if err != nil || res.ExitCode != 2 {
		t.Errorf("Exec passthrough: res=%+v err=%v", res, err)
	}
	if _, err := o.Exec(context.Background()); err == nil {
		t.Error("Exec with no command should error")
	}
}

func TestStatusHostSide(t *testing.T) {
	runDir := makeRun(t, "job", "20260101T000000Z")
	name := filepath.Base(runDir)
	fake := &container.Fake{}
	o, _ := New(fake, runDir)

	// No STATUS yet, not running.
	st, err := o.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Running || st.HasStatus {
		t.Errorf("fresh run: running=%v hasStatus=%v", st.Running, st.HasStatus)
	}

	// Write a DONE STATUS and mark running.
	statusPath := filepath.Join(runDir, run.ControlDir, run.StatusFile)
	if err := os.WriteFile(statusPath, []byte("DONE\nall good\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.SetRunning(name, true)
	st, err = o.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || !st.HasStatus || !st.Sentinel.Done {
		t.Errorf("status = %+v, want running+done", st)
	}
}

func TestTailLog(t *testing.T) {
	runDir := makeRun(t, "job", "20260101T000000Z")
	o, _ := New(&container.Fake{}, runDir)

	// Missing log file -> empty, no error.
	if got, err := o.TailLog(context.Background(), 5); err != nil || got != "" {
		t.Fatalf("missing log: got %q err %v", got, err)
	}

	logPath := filepath.Join(runDir, run.LogsDir, run.LogFile)
	if err := os.WriteFile(logPath, []byte("l1\nl2\nl3\nl4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := o.TailLog(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "l3\nl4\n" {
		t.Errorf("tail 2 = %q, want l3\\nl4\\n", got)
	}
	// n<=0 returns whole file.
	if got, _ := o.TailLog(context.Background(), 0); got != "l1\nl2\nl3\nl4\n" {
		t.Errorf("tail all = %q", got)
	}
	// n larger than line count returns whole file.
	if got, _ := o.TailLog(context.Background(), 99); got != "l1\nl2\nl3\nl4\n" {
		t.Errorf("tail 99 = %q", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("", 3); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := lastLines("a\nb\nc\n", 2); got != "b\nc\n" {
		t.Errorf("got %q", got)
	}
	if got := lastLines("only\n", 0); got != "only\n" {
		t.Errorf("got %q", got)
	}
}
