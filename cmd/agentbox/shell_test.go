package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/observe"
	"github.com/szatmary/agentbox/internal/run"
)

func TestRunShellLive(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, _ := run.Create(runsDir, "job", "20260101T000000Z")
	r.Close()
	name := filepath.Base(r.Root)

	fake := &container.Fake{}
	fake.SetRunning(name, true)
	var sawStream container.StreamOptions
	fake.ExecStreamFunc = func(ctx context.Context, id string, opts container.StreamOptions) (int, error) {
		sawStream = opts
		return 0, nil
	}

	var in, out, errb bytes.Buffer
	// Default command is an interactive bash with a TTY.
	if err := runShell(context.Background(), runsDir, "job", nil, fake, &in, &out, &errb); err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if !sawStream.TTY {
		t.Error("shell must request a TTY")
	}
	if strings.Join(sawStream.Cmd, " ") != "bash" {
		t.Errorf("default cmd = %v, want bash", sawStream.Cmd)
	}
	if sawStream.Workdir != observe.DefaultWorkdir {
		t.Errorf("workdir = %q", sawStream.Workdir)
	}

	// Explicit command is passed through.
	if err := runShell(context.Background(), runsDir, "job", []string{"ls", "-la"}, fake, &in, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if strings.Join(sawStream.Cmd, " ") != "ls -la" {
		t.Errorf("cmd = %v, want ls -la", sawStream.Cmd)
	}
}

func TestRunShellNotRunning(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, _ := run.Create(runsDir, "job", "20260101T000000Z")
	r.Close()

	fake := &container.Fake{} // not marked running
	var in, out, errb bytes.Buffer
	err := runShell(context.Background(), runsDir, "job", nil, fake, &in, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want not running", err)
	}
	if len(fake.CallsOf("ExecStream")) != 0 {
		t.Error("must not stream when the container is down")
	}
}

func TestRunShellUnknownRun(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := runShell(context.Background(), runsDir, "nope", nil, &container.Fake{},
		&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("expected error for unknown run")
	}
}

func TestRunShellExitCode(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, _ := run.Create(runsDir, "job", "20260101T000000Z")
	r.Close()
	name := filepath.Base(r.Root)
	fake := &container.Fake{}
	fake.SetRunning(name, true)
	fake.ExecStreamFunc = func(ctx context.Context, id string, opts container.StreamOptions) (int, error) {
		return 3, nil
	}
	err := runShell(context.Background(), runsDir, "job", []string{"false"}, fake,
		&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exited 3") {
		t.Errorf("err = %v, want exited 3", err)
	}
}

func TestGatherStatusLive(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	live, _ := run.Create(runsDir, "job", "20260101T000000Z")
	live.Close()
	dead, _ := run.Create(runsDir, "job", "20260102T000000Z")
	dead.Close()

	fake := &container.Fake{}
	fake.SetRunning(filepath.Base(live.Root), true)
	// dead left unmarked => Inspect reports Running=false.

	rows, err := gatherStatus(context.Background(), runsDir, fake)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]statusRow{}
	for _, r := range rows {
		byName[r.name] = r
	}
	if got := byName[filepath.Base(live.Root)].live; got != "running" {
		t.Errorf("live run LIVE = %q, want running", got)
	}
	if got := byName[filepath.Base(dead.Root)].live; got != "stopped" {
		t.Errorf("dead run LIVE = %q, want stopped", got)
	}

	// nil runtime => no probe, LIVE is "-".
	rows2, _ := gatherStatus(context.Background(), runsDir, nil)
	for _, r := range rows2 {
		if r.live != "-" {
			t.Errorf("nil runtime LIVE = %q, want -", r.live)
		}
	}
}
