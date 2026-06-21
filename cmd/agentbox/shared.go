package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/szatmary/agentbox/internal/auth"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// secretsMountTarget is a dedicated, read-only mount inside the VM holding the
// staged secrets. It is deliberately NOT the bind-mounted control/output dirs
// (which persist on the host and hold run artifacts): the staging dir is created
// outside them and removed after teardown. See S1/S2.
const secretsMountTarget = "/work/.agentbox"

// credentialsFileInVM is the in-VM path of the keychain OAuth blob (copied into
// place by setup). It lives on the ephemeral secrets mount, never the control dir.
const credentialsFileInVM = secretsMountTarget + "/.claude-credentials.json"

// secretsEnvFileInVM is the in-VM path of the 0600 env file holding secret
// environment assignments, sourced before every command so secrets never appear
// in `container run`/`container exec` argv. See S1.
const secretsEnvFileInVM = secretsMountTarget + "/env"

// secretsDirName is the host-side staging subdirectory under the run root. It is
// a sibling of (not under) the bind-mounted control/output/workspace dirs.
const secretsDirName = "secrets"

// defaultImageTag is the sandbox image agentbox builds and runs.
const defaultImageTag = "agentbox:latest"

// loadJob reads the job config at path, applies overrides, and returns the
// merged config plus the directory containing the config (used to resolve the
// task file).
func loadJob(path string, ov config.Overrides) (config.Config, string, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err = cfg.Apply(ov)
	if err != nil {
		return config.Config{}, "", err
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return config.Config{}, "", err
	}
	return cfg, dir, nil
}

// readTask reads the task prompt referenced by cfg.Task, resolved relative to
// configDir when it is not absolute.
func readTask(cfg config.Config, configDir string) (string, error) {
	p := cfg.Task
	if !filepath.IsAbs(p) {
		p = filepath.Join(configDir, p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading task file %s: %w", p, err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("task file %s is empty", p)
	}
	return string(b), nil
}

// newRunID returns a sortable, hyphen-free run id derived from the current UTC
// time (the run package forbids '-' in ids).
func newRunID() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

// mountsFor binds the run's control/output/workspace dirs into the VM.
func mountsFor(r *run.Run) []container.Mount {
	return []container.Mount{
		{Source: r.Path(run.ControlDir), Target: "/work/control"},
		{Source: r.Output(), Target: "/work/output"},
		{Source: r.Workspace(), Target: "/work/workspace"},
	}
}

// buildSetup builds the one-time in-VM setup commands from a resolved
// Injection. It is pure: the caller is responsible for having written the
// credentials file (when inj.ClaudeCredentialsJSON is non-empty) to the host
// path that maps to credentialsFileInVM.
func buildSetup(inj auth.Injection, repo string) [][]string {
	var setup [][]string
	if inj.ClaudeCredentialsJSON != "" {
		setup = append(setup, []string{"sh", "-c",
			"mkdir -p \"$HOME/.claude\" && cp " + credentialsFileInVM +
				" \"$HOME/.claude/.credentials.json\" && chmod 600 \"$HOME/.claude/.credentials.json\""})
	}
	if inj.GitName != "" {
		setup = append(setup, []string{"git", "config", "--global", "user.name", inj.GitName})
	}
	if inj.GitEmail != "" {
		setup = append(setup, []string{"git", "config", "--global", "user.email", inj.GitEmail})
	}
	if inj.GitHubSource != config.GitHubNone {
		// Let git use the injected token for HTTPS GitHub operations.
		setup = append(setup, []string{"sh", "-c", "gh auth setup-git >/dev/null 2>&1 || true"})
	}
	if repo != "" {
		// The `--` separator stops git from interpreting a crafted repo value as
		// a flag; config.Validate also rejects '-'-leading and ext:: repos. See S4.
		setup = append(setup, []string{"sh", "-c",
			"cd /work/workspace && if [ -z \"$(ls -A)\" ]; then git clone -- " + shellQuote(repo) + " .; fi"})
	}
	return setup
}

// stageSecrets writes the injected secrets (env assignments + optional keychain
// OAuth blob) into a 0700 staging dir under the run root, OUTSIDE the
// bind-mounted control/output/workspace dirs, and returns a read-only mount of
// that dir plus a cleanup func that removes it. Routing secrets through a
// sourced 0600 file (not argv) is S1; staging outside the control dir and
// removing it after teardown is S2.
func stageSecrets(r *run.Run, inj auth.Injection) (container.Mount, func(), error) {
	dir := r.Path(secretsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return container.Mount{}, func() {}, fmt.Errorf("staging secrets: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	// env file: KEY='value' per line; sourced with `set -a` inside the VM.
	var b strings.Builder
	for _, k := range sortedStringKeys(inj.Env) {
		b.WriteString(k + "=" + shellQuote(inj.Env[k]) + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "env"), []byte(b.String()), 0o600); err != nil {
		cleanup()
		return container.Mount{}, func() {}, fmt.Errorf("writing secrets env file: %w", err)
	}

	if inj.ClaudeCredentialsJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, ".claude-credentials.json"),
			[]byte(inj.ClaudeCredentialsJSON), 0o600); err != nil {
			cleanup()
			return container.Mount{}, func() {}, fmt.Errorf("writing claude credentials: %w", err)
		}
	}

	return container.Mount{Source: dir, Target: secretsMountTarget, ReadOnly: true}, cleanup, nil
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveAuth resolves credentials for the job, with a clear error on failure.
func resolveAuth(ctx context.Context, cfg config.Config) (auth.Injection, error) {
	return auth.NewResolver().Resolve(ctx, cfg.Auth)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runsBase returns the directory holding detached pid/log files (the parent of
// the runs directory).
func runsBase(runsDir string) string { return filepath.Dir(runsDir) }
