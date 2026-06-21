package config

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks the config for actionable errors. It does not touch the
// filesystem or network (the task file's existence is checked by the caller
// against the config's directory).
func (c Config) Validate() error {
	var errs []string

	if strings.TrimSpace(c.Name) == "" {
		errs = append(errs, `name is required (e.g. name = "go2110")`)
	} else if strings.ContainsAny(c.Name, " \t/\\:") {
		errs = append(errs, fmt.Sprintf("name %q must not contain spaces or path separators", c.Name))
	}

	if strings.TrimSpace(c.Task) == "" {
		errs = append(errs, `task is required (path to the task prompt file, e.g. task = "task.md")`)
	}

	if c.Guards.MaxWall.D() <= 0 {
		errs = append(errs, `guards.max_wall must be a positive duration (e.g. "3h")`)
	}
	if c.Guards.MaxIters <= 0 {
		errs = append(errs, "guards.max_iters must be > 0")
	}
	if c.Guards.MaxTurns <= 0 {
		errs = append(errs, "guards.max_turns must be > 0")
	}

	switch c.Auth.Claude {
	case ClaudeKeychain, ClaudeAPIKey, ClaudeToken:
	default:
		errs = append(errs, fmt.Sprintf("auth.claude %q must be one of keychain|api_key|token", c.Auth.Claude))
	}
	switch c.Auth.GitHub {
	case GitHubGH, GitHubPAT, GitHubNone:
	default:
		errs = append(errs, fmt.Sprintf("auth.github %q must be one of gh|pat|none", c.Auth.GitHub))
	}

	// A GitHub credential without a repo is harmless but a repo with github=none
	// means the agent cannot push; warn via error only when autorun relies on it.
	if c.Repo != "" && !strings.Contains(c.Repo, "://") && !strings.Contains(c.Repo, "@") {
		errs = append(errs, fmt.Sprintf("repo %q does not look like a git URL", c.Repo))
	}

	if c.Autorun.PerRunWall.D() <= 0 {
		errs = append(errs, `autorun.per_run_wall must be a positive duration (e.g. "3h")`)
	}
	if c.Autorun.MaxNoProgress <= 0 {
		errs = append(errs, "autorun.max_noprogress must be > 0")
	}
	if c.Autorun.Cooldown.D() < 0 {
		errs = append(errs, "autorun.cooldown must not be negative")
	}
	if c.Autorun.MaxRuns < 0 {
		errs = append(errs, "autorun.max_runs must not be negative (0 = unlimited)")
	}
	if c.Repo == "" && c.Autorun.MaxNoProgress > 0 && c.Autorun.MaxRuns == 0 {
		// Not fatal: documented in DECISIONS (D6). Autorun without a repo has no
		// HEAD-based progress signal; it then relies on DONE/FAILED or max_runs.
	}

	if len(errs) > 0 {
		return errors.New("invalid config:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}
