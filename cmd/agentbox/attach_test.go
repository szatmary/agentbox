package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/attach"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// makeRunWithKey creates a run dir and (optionally) its SSH keypair.
func makeRunWithKey(t *testing.T, runsDir, name, id string, withKey bool) string {
	t.Helper()
	r, err := run.Create(runsDir, name, id)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if withKey {
		if _, err := attach.EnsureKeyPair(r.Root); err != nil {
			t.Fatal(err)
		}
	}
	return r.Root
}

func TestRunSSHProxyCmd(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := makeRunWithKey(t, runsDir, "job", "20260101T000000Z", true)
	name := filepath.Base(runDir)

	fake := &container.Fake{}
	fake.SetRunning(name, true)
	var ran []string
	fake.ExecStreamFunc = func(ctx context.Context, id string, opts container.StreamOptions) (int, error) {
		ran = opts.Cmd
		return 0, nil
	}
	code, err := runSSHProxy(context.Background(), runsDir, "job", fake,
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || code != 0 {
		t.Fatalf("runSSHProxy: code=%d err=%v", code, err)
	}
	if strings.Join(ran, " ") != "sudo /usr/sbin/sshd -i -e" {
		t.Errorf("proxy command = %v", ran)
	}
}

func TestInstallSSHForRun(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := makeRunWithKey(t, runsDir, "job", "20260101T000000Z", true)
	runName := filepath.Base(runDir)
	cfgPath := filepath.Join(t.TempDir(), ".ssh", "config")

	var out bytes.Buffer
	alias, err := installSSHForRun(&out, runsDir, "job", cfgPath, "/usr/local/bin/agentbox", false)
	if err != nil {
		t.Fatalf("installSSHForRun: %v", err)
	}
	if alias != "agentbox-"+runName {
		t.Errorf("alias = %q", alias)
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	for _, want := range []string{
		"Host agentbox-" + runName,
		"ssh-proxy '" + runName + "'",
		"IdentityFile " + attach.PrivateKeyPath(runDir),
		"User agent",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("ssh config missing %q:\n%s", want, cfg)
		}
	}
	// ProxyCommand must carry an ABSOLUTE runs-dir (cwd-independent).
	absRuns, _ := filepath.Abs(runsDir)
	if !strings.Contains(cfg, "--runs-dir '"+absRuns+"'") {
		t.Errorf("ssh config missing absolute --runs-dir:\n%s", cfg)
	}
}

func TestInstallSSHForRunPrintOnly(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	makeRunWithKey(t, runsDir, "job", "20260101T000000Z", true)
	cfgPath := filepath.Join(t.TempDir(), ".ssh", "config")

	var out bytes.Buffer
	if _, err := installSSHForRun(&out, runsDir, "job", cfgPath, "/bin/agentbox", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Host agentbox-job-") {
		t.Errorf("print did not emit the host block:\n%s", out.String())
	}
	if fileExists(cfgPath) {
		t.Error("print-only must not write the ssh config")
	}
}

func TestInstallSSHForRunNoKey(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	makeRunWithKey(t, runsDir, "job", "20260101T000000Z", false) // no key
	cfgPath := filepath.Join(t.TempDir(), ".ssh", "config")
	if _, err := installSSHForRun(&bytes.Buffer{}, runsDir, "job", cfgPath, "/bin/agentbox", false); err == nil ||
		!strings.Contains(err.Error(), "[attach] ssh") {
		t.Errorf("expected a missing-key error, got %v", err)
	}
}

func TestRunAttachVSCode(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	runDir := makeRunWithKey(t, runsDir, "job", "20260101T000000Z", true)
	runName := filepath.Base(runDir)
	cfgPath := filepath.Join(t.TempDir(), ".ssh", "config")

	var codeArgs []string
	runCode := func(args ...string) error { codeArgs = args; return nil }

	var out bytes.Buffer
	if err := runAttach(&out, runsDir, "job", cfgPath, "/bin/agentbox", true, runCode); err != nil {
		t.Fatalf("runAttach: %v", err)
	}
	want := []string{"--remote", "ssh-remote+agentbox-" + runName, "/work/workspace"}
	if strings.Join(codeArgs, " ") != strings.Join(want, " ") {
		t.Errorf("code args = %v, want %v", codeArgs, want)
	}

	// Without --vscode, code is not invoked.
	codeArgs = nil
	if err := runAttach(&out, runsDir, "job", cfgPath, "/bin/agentbox", false, runCode); err != nil {
		t.Fatal(err)
	}
	if codeArgs != nil {
		t.Errorf("code should not run without --vscode, got %v", codeArgs)
	}
}
