package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// packageNameRE matches a safe Debian package name (optionally with an
// apt version qualifier handled elsewhere). It deliberately forbids whitespace,
// shell metacharacters, and newlines so an extra_packages entry cannot inject
// extra Dockerfile/RUN commands. See S3.
var packageNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)

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
	if c.Repo != "" {
		// Reject argument- and transport-injection vectors before the URL ever
		// reaches `git clone`/`git ls-remote`. A leading '-' would be parsed as a
		// flag; the `ext::` remote-helper transport runs an arbitrary command. See S4.
		if strings.HasPrefix(c.Repo, "-") {
			errs = append(errs, fmt.Sprintf("repo %q must not begin with '-' (would be parsed as a git flag)", c.Repo))
		}
		if strings.HasPrefix(strings.ToLower(c.Repo), "ext::") {
			errs = append(errs, fmt.Sprintf("repo %q must not use the ext:: transport (arbitrary command execution)", c.Repo))
		}
		if !strings.Contains(c.Repo, "://") && !strings.Contains(c.Repo, "@") {
			errs = append(errs, fmt.Sprintf("repo %q does not look like a git URL", c.Repo))
		}
	}

	// extra_packages are interpolated into the image Dockerfile's apt-get install
	// line; an unconstrained value injects arbitrary build commands. See S3.
	for _, pkg := range c.Image.ExtraPackages {
		if !packageNameRE.MatchString(pkg) {
			errs = append(errs, fmt.Sprintf("image.extra_packages entry %q is not a valid package name "+
				"(allowed: letters, digits, and . + _ -)", pkg))
		}
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

	if len(errs) > 0 {
		return errors.New("invalid config:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}
