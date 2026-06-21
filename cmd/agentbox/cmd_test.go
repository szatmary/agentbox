package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/auth"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/run"
)

func execRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestInitWritesScaffold(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out, err := execRoot(t, "init", "--name", "go2110")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	cfg := filepath.Join(dir, "agentbox.toml")
	task := filepath.Join(dir, "task.md")
	if !fileExists(cfg) || !fileExists(task) {
		t.Fatal("init did not write both files")
	}
	// The written config must be valid and carry the chosen name.
	loaded, err := config.Load(cfg)
	if err != nil {
		t.Fatalf("written config invalid: %v", err)
	}
	if loaded.Name != "go2110" {
		t.Errorf("name = %q", loaded.Name)
	}
	// Second init without --force should skip, not overwrite.
	out, err = execRoot(t, "init", "--name", "other")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "skip (exists)") {
		t.Errorf("expected skip message, got %q", out)
	}
}

func TestStatusListsRuns(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	done, _ := run.Create(runsDir, "job", "20260101T000000Z")
	os.WriteFile(done.Control(run.StatusFile), []byte("DONE\nall good\n"), 0o644)
	done.Close()
	failed, _ := run.Create(runsDir, "job", "20260102T000000Z")
	os.WriteFile(failed.Control(run.StatusFile), []byte("FAILED: boom\n"), 0o644)
	failed.Close()
	running, _ := run.Create(runsDir, "job", "20260103T000000Z")
	running.Close()

	out, err := execRoot(t, "status", "--runs-dir", runsDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"DONE", "FAILED", "boom", "running?"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusEmpty(t *testing.T) {
	out, err := execRoot(t, "status", "--runs-dir", filepath.Join(t.TempDir(), "none"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no runs") {
		t.Errorf("expected 'no runs', got %q", out)
	}
}

func TestLogsReadsRunLog(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, _ := run.Create(runsDir, "job", "20260101T000000Z")
	r.Logger().Info("hello-from-test")
	r.Close()

	out, err := execRoot(t, "logs", "job-20260101T000000Z", "--runs-dir", runsDir)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out, "hello-from-test") {
		t.Errorf("logs output missing entry:\n%s", out)
	}

	if _, err := execRoot(t, "logs", "nope", "--runs-dir", runsDir); err == nil {
		t.Error("expected error for missing run")
	}
}

func TestStopWritesStopFile(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, _ := run.Create(runsDir, "job", "20260101T000000Z")
	r.Close()

	if _, err := execRoot(t, "stop", "job-20260101T000000Z", "--runs-dir", runsDir); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !fileExists(filepath.Join(r.Root, run.ControlDir, run.StopFile)) {
		t.Error("STOP file not written")
	}
}

func TestBuildSetup(t *testing.T) {
	// Keychain creds + git identity + github + repo => all setup commands.
	inj := auth.Injection{
		ClaudeCredentialsJSON: `{"x":1}`,
		GitName:               "Matthew Szatmary",
		GitEmail:              "matt@szatmary.org",
		GitHubSource:          config.GitHubGH,
	}
	setup := buildSetup(inj, "https://github.com/szatmary/go2110.git")
	joined := flatten(setup)
	for _, want := range []string{credentialsFileInVM, "user.name", "Matthew Szatmary", "user.email", "gh auth setup-git", "git clone"} {
		if !strings.Contains(joined, want) {
			t.Errorf("setup missing %q in:\n%s", want, joined)
		}
	}

	// No creds, github=none, no repo => only the (absent) bits omitted.
	inj2 := auth.Injection{GitName: "n", GitEmail: "e", GitHubSource: config.GitHubNone}
	setup2 := buildSetup(inj2, "")
	j2 := flatten(setup2)
	if strings.Contains(j2, credentialsFileInVM) || strings.Contains(j2, "git clone") || strings.Contains(j2, "setup-git") {
		t.Errorf("setup2 should omit creds/clone/setup-git:\n%s", j2)
	}
	if !strings.Contains(j2, "user.name") {
		t.Errorf("setup2 should configure git identity:\n%s", j2)
	}
}

func flatten(cmds [][]string) string {
	var parts []string
	for _, c := range cmds {
		parts = append(parts, strings.Join(c, " "))
	}
	return strings.Join(parts, "\n")
}

type okTokener struct{}

func (okTokener) Token(ctx context.Context) (string, error) { return "tok", nil }

// fakeProber drives runDoctor without touching the host.
type fakeProber struct {
	present map[string]bool
	ok      map[string]bool
}

func (f fakeProber) lookPath(name string) (string, error) {
	if f.present[name] {
		return "/usr/bin/" + name, nil
	}
	return "", os.ErrNotExist
}

func (f fakeProber) runOK(ctx context.Context, name string, args ...string) (bool, string) {
	key := name + " " + strings.Join(args, " ")
	return f.ok[key], "output"
}

func TestRunDoctorAllGood(t *testing.T) {
	p := fakeProber{
		present: map[string]bool{"container": true, "gh": true},
		ok: map[string]bool{
			"container system status": true,
			"container ls":            true,
			"gh auth status":          true,
		},
	}
	// api_key resolver succeeds with env set; github=gh needs a tokener.
	resolver := auth.Resolver{Env: auth.MapEnv{"ANTHROPIC_API_KEY": "sk"}, GitHub: okTokener{}}
	cfg := config.Default()
	cfg.Auth = config.Auth{Claude: config.ClaudeAPIKey, GitHub: config.GitHubGH}

	results := runDoctor(context.Background(), p, resolver, cfg)
	for _, r := range results {
		if r.status == statusFail {
			t.Errorf("unexpected FAIL: %s — %s", r.name, r.detail)
		}
	}
}

func TestRunDoctorMissingContainerAndCred(t *testing.T) {
	p := fakeProber{present: map[string]bool{}, ok: map[string]bool{}}
	resolver := auth.Resolver{Env: auth.MapEnv{}} // api_key missing => fail
	cfg := config.Default()
	cfg.Auth = config.Auth{Claude: config.ClaudeAPIKey, GitHub: config.GitHubNone}

	results := runDoctor(context.Background(), p, resolver, cfg)
	var fails int
	for _, r := range results {
		if r.status == statusFail {
			fails++
		}
	}
	if fails < 2 {
		t.Errorf("expected >=2 FAILs (container, credential), got %d:\n%+v", fails, results)
	}
}

func TestHelpers(t *testing.T) {
	if got := stripDetach([]string{"run", "--detach", "job.toml", "-d"}); len(got) != 2 {
		t.Errorf("stripDetach = %v", got)
	}
	if sanitizeName("My Job!") != "My-Job-" {
		t.Errorf("sanitizeName = %q", sanitizeName("My Job!"))
	}
	if firstLine("a\nb") != "a" {
		t.Errorf("firstLine = %q", firstLine("a\nb"))
	}
	if shellQuote("a'b") != `'a'\''b'` {
		t.Errorf("shellQuote = %q", shellQuote("a'b"))
	}
}
