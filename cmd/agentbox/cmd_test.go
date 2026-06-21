package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szatmary/agentbox/internal/auth"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
	"github.com/szatmary/agentbox/internal/supervisor"
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
	// S4: clone must use the `--` separator so a crafted repo can't be a flag.
	if !strings.Contains(joined, "git clone -- ") {
		t.Errorf("clone must use the -- separator:\n%s", joined)
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

// fakeKeychain is an auth.Keychain returning a fixed blob (for the wiring test).
type fakeKeychain struct{ blob string }

func (f fakeKeychain) Find(service string) (string, error) { return f.blob, nil }

// TestExecuteRunWiringNoSecretsInArgv covers S1, S2 and H3 together: with an
// injected fake Runtime and credential resolver, executeRunWith must (1) never
// place secret values in `container run`/`exec` argv or env, (2) route secrets
// through a sourced env file, (3) wire mounts/model correctly, (4) stage the
// cred blob OUTSIDE the control dir and remove it after teardown.
func TestExecuteRunWiringNoSecretsInArgv(t *testing.T) {
	const (
		ghSecret   = "ghp_SECRET_TOKEN_VALUE"
		credSecret = `{"oauth":"CLAUDE_SECRET_BLOB"}`
		model      = "claude-opus-4-8"
	)
	runsDir := filepath.Join(t.TempDir(), "runs")

	// Fake runtime: drive the supervisor to DONE on the first iteration.
	fake := &container.Fake{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
			if len(opts.Cmd) > 0 && opts.Cmd[0] == "cat" {
				if strings.HasSuffix(opts.Cmd[1], run.StatusFile) {
					return container.ExecResult{ExitCode: 0, Stdout: "DONE\n"}, nil
				}
				return container.ExecResult{ExitCode: 1}, nil // STOP absent
			}
			return container.ExecResult{ExitCode: 0}, nil
		},
	}

	// keychain claude (=> cred blob) + pat github (=> GITHUB_TOKEN/GH_TOKEN env).
	resolver := auth.Resolver{
		Env:      auth.MapEnv{"GITHUB_TOKEN": ghSecret},
		Keychain: fakeKeychain{blob: credSecret},
	}
	cfg := config.Default()
	cfg.Name = "wiretest"
	cfg.Repo = "https://github.com/szatmary/go2110.git"
	cfg.Model.Name = model
	cfg.Auth = config.Auth{Claude: config.ClaudeKeychain, GitHub: config.GitHubPAT}

	var out bytes.Buffer
	res, err := executeRunWith(context.Background(), &out, runsDir, cfg, "TASKPROMPT", "agentbox:latest",
		cfg.Guards.MaxWall.D(), fake, resolver)
	if err != nil {
		t.Fatalf("executeRunWith: %v", err)
	}
	if res.Status != supervisor.StatusDone {
		t.Fatalf("status = %v, want done", res.Status)
	}

	secrets := []string{ghSecret, credSecret}

	// S1: no secret value anywhere in Run argv/env.
	runCalls := fake.CallsOf("Run")
	if len(runCalls) != 1 {
		t.Fatalf("Run calls = %d, want 1", len(runCalls))
	}
	rc := runCalls[0]
	for _, v := range rc.Env {
		for _, sec := range secrets {
			if strings.Contains(v, sec) {
				t.Errorf("secret leaked into container run env: %q", v)
			}
		}
	}
	for _, arg := range rc.Cmd {
		for _, sec := range secrets {
			if strings.Contains(arg, sec) {
				t.Errorf("secret leaked into container run cmd: %q", arg)
			}
		}
	}

	// S1: no secret value in ANY exec argv/env either.
	var sawSecretsSourcing bool
	for _, c := range fake.CallsOf("Exec") {
		joined := strings.Join(c.Cmd, " ")
		for _, sec := range secrets {
			if strings.Contains(joined, sec) {
				t.Errorf("secret leaked into exec argv: %q", joined)
			}
			for _, v := range c.Env {
				if strings.Contains(v, sec) {
					t.Errorf("secret leaked into exec env: %q", v)
				}
			}
		}
		if strings.Contains(joined, secretsEnvFileInVM) {
			sawSecretsSourcing = true
		}
	}
	if !sawSecretsSourcing {
		t.Error("S1: no command sourced the secrets env file")
	}

	// H3: model is wired into the claude invocation.
	var sawModel bool
	for _, c := range fake.CallsOf("Exec") {
		if strings.Contains(strings.Join(c.Cmd, " "), model) {
			sawModel = true
		}
	}
	if !sawModel {
		t.Error("H3: model not wired into claude args")
	}

	// H3: the run's bind mounts (control/output/workspace) plus the secrets
	// mount are present; the secrets mount target is NOT the control dir.
	var controlSrc, secretsSrc string
	for _, m := range rc.Mounts {
		switch m.Target {
		case "/work/control":
			controlSrc = m.Source
		case secretsMountTarget:
			secretsSrc = m.Source
		}
	}
	if controlSrc == "" {
		t.Fatal("control mount missing")
	}
	if secretsSrc == "" {
		t.Fatal("secrets mount missing")
	}
	// S2: secrets staged OUTSIDE the bind-mounted control dir...
	if strings.HasPrefix(secretsSrc, controlSrc+string(os.PathSeparator)) {
		t.Errorf("secrets staged under the control dir: %s under %s", secretsSrc, controlSrc)
	}
	// ...and removed after teardown.
	if _, err := os.Stat(secretsSrc); !os.IsNotExist(err) {
		t.Errorf("secrets staging dir not removed after run: stat err = %v", err)
	}
	// S2: no leftover credentials file anywhere under the run dir.
	walkAssertNoCredFile(t, runsDir)
}

func walkAssertNoCredFile(t *testing.T, root string) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && strings.Contains(info.Name(), "credentials") {
			t.Errorf("leftover credential file: %s", p)
		}
		return nil
	})
}

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

// fakeSignaler records terminate calls and reports liveness from a set.
type fakeSignaler struct {
	live       map[int]bool
	terminated []int
}

func (f *fakeSignaler) alive(pid int) bool { return f.live[pid] }
func (f *fakeSignaler) terminate(pid int) error {
	f.terminated = append(f.terminated, pid)
	return nil
}

// TestSignalPidfileLiveness covers O2: a dead/reused PID must NOT be signalled
// (and its stale pidfile is removed); a live PID is signalled.
func TestSignalPidfileLiveness(t *testing.T) {
	base := t.TempDir()
	var buf bytes.Buffer

	// Dead PID: must not terminate; pidfile removed.
	deadPid := filepath.Join(base, "dead.pid")
	if err := os.WriteFile(deadPid, []byte("4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sigDead := &fakeSignaler{live: map[int]bool{}}
	if signalPidfile(&buf, deadPid, sigDead) {
		t.Error("signalPidfile reported action for a dead PID")
	}
	if len(sigDead.terminated) != 0 {
		t.Errorf("dead PID must not be signalled, got %v", sigDead.terminated)
	}
	if fileExists(deadPid) {
		t.Error("stale pidfile should be removed")
	}

	// Live PID: terminate is called.
	livePid := filepath.Join(base, "live.pid")
	if err := os.WriteFile(livePid, []byte("777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sigLive := &fakeSignaler{live: map[int]bool{777: true}}
	if !signalPidfile(&buf, livePid, sigLive) {
		t.Error("signalPidfile should act on a live PID")
	}
	if len(sigLive.terminated) != 1 || sigLive.terminated[0] != 777 {
		t.Errorf("live PID not signalled: %v", sigLive.terminated)
	}
}

// TestDetachPidfileCleanup covers O2: the detached child removes its pidfile on
// exit; a non-detached process leaves it alone.
func TestDetachPidfileCleanup(t *testing.T) {
	base := t.TempDir()
	pidPath := filepath.Join(base, "job.pid")
	if err := os.WriteFile(pidPath, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Not the detached child: no-op.
	t.Setenv(detachedEnv, "")
	detachPidfileCleanup(base, "job")()
	if !fileExists(pidPath) {
		t.Error("non-detached process must not remove the pidfile")
	}

	// Detached child: removes its pidfile.
	t.Setenv(detachedEnv, "1")
	detachPidfileCleanup(base, "job")()
	if fileExists(pidPath) {
		t.Error("detached child should remove its pidfile on exit")
	}
}

// TestClearJobStop covers O2: a stale job-level stop marker is consumed on
// startup so it does not poison future autoruns.
func TestClearJobStop(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	stopPath := filepath.Join(runsBase(runsDir), "job.stop")
	if err := os.MkdirAll(runsBase(runsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stopPath, []byte("stop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !jobStopRequested(runsDir, "job")() {
		t.Fatal("precondition: stop marker should be detected")
	}
	clearJobStop(runsDir, "job")
	if jobStopRequested(runsDir, "job")() {
		t.Error("stale stop marker not cleared on startup")
	}
}

// TestAutorunWall covers O1: the per-run wall is the more-restrictive of
// guards.max_wall (set by --max-wall) and autorun.per_run_wall.
func TestAutorunWall(t *testing.T) {
	cfg := config.Default()
	cfg.Autorun.PerRunWall = config.Duration(3 * time.Hour)

	cfg.Guards.MaxWall = config.Duration(1 * time.Hour)
	if got := autorunWall(cfg); got != time.Hour {
		t.Errorf("guards.max_wall more restrictive: got %v, want 1h", got)
	}

	cfg.Guards.MaxWall = config.Duration(5 * time.Hour)
	if got := autorunWall(cfg); got != 3*time.Hour {
		t.Errorf("per_run_wall more restrictive: got %v, want 3h", got)
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
