package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/szatmary/agentbox/internal/auth"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// credentialsFileInVM is where the keychain OAuth blob is copied inside the VM.
const credentialsFileInVM = "/work/control/.claude-credentials.json"

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
		setup = append(setup, []string{"sh", "-c",
			"cd /work/workspace && if [ -z \"$(ls -A)\" ]; then git clone " + shellQuote(repo) + " .; fi"})
	}
	return setup
}

// writeCredentials writes the keychain OAuth blob into the run's control dir so
// the in-VM setup can copy it into place. It returns the host path written, or
// "" when there is nothing to write. The file is 0600.
func writeCredentials(r *run.Run, inj auth.Injection) (string, error) {
	if inj.ClaudeCredentialsJSON == "" {
		return "", nil
	}
	p := r.Control(".claude-credentials.json")
	if err := os.WriteFile(p, []byte(inj.ClaudeCredentialsJSON), 0o600); err != nil {
		return "", fmt.Errorf("writing claude credentials: %w", err)
	}
	return p, nil
}

// envFor merges the injected secret env vars (copy to avoid mutation).
func envFor(inj auth.Injection) map[string]string {
	env := make(map[string]string, len(inj.Env))
	for k, v := range inj.Env {
		env[k] = v
	}
	return env
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
