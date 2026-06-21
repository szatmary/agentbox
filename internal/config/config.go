// Package config loads, validates, and merges agentbox job configuration.
//
// A job is described by a TOML file (see [Config]) and may be further overridden
// by command-line flags. Durations are written as Go duration strings ("3h").
package config

import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// Claude credential source kinds (the `[auth] claude` field).
const (
	ClaudeKeychain = "keychain" // macOS keychain item "Claude Code-credentials"
	ClaudeAPIKey   = "api_key"  // ANTHROPIC_API_KEY env var
	ClaudeToken    = "token"    // CLAUDE_CODE_OAUTH_TOKEN env var
)

// GitHub credential source kinds (the `[auth] github` field).
const (
	GitHubGH   = "gh"   // `gh auth token`
	GitHubPAT  = "pat"  // GITHUB_TOKEN / GH_TOKEN env var
	GitHubNone = "none" // no GitHub credential injected
)

// Config is a parsed agentbox job. It mirrors the agentbox.toml schema.
type Config struct {
	// Name identifies the job; used for run directories and container names.
	Name string `toml:"name"`
	// Repo is an optional git remote the agent clones and pushes to. Empty
	// means the job runs without git (progress detection is then disabled).
	Repo string `toml:"repo"`
	// Task is a path (relative to the config file) to the task prompt file.
	Task string `toml:"task"`

	Guards  Guards  `toml:"guards"`
	Model   Model   `toml:"model"`
	Auth    Auth    `toml:"auth"`
	Image   Image   `toml:"image"`
	Autorun Autorun `toml:"autorun"`
}

// Guards bound a single supervised run.
type Guards struct {
	// MaxWall is the wall-clock budget for one run.
	MaxWall Duration `toml:"max_wall"`
	// MaxIters caps the number of resume iterations.
	MaxIters int `toml:"max_iters"`
	// MaxTurns caps agent turns per individual claude invocation.
	MaxTurns int `toml:"max_turns"`
}

// Model selects the Claude model.
type Model struct {
	// Name is the model id; empty means the account default.
	Name string `toml:"name"`
}

// Auth selects credential sources.
type Auth struct {
	// Claude is one of keychain|api_key|token.
	Claude string `toml:"claude"`
	// GitHub is one of gh|pat|none.
	GitHub string `toml:"github"`
}

// Image configures the sandbox image build.
type Image struct {
	// ExtraPackages are additional OS packages installed into the image.
	ExtraPackages []string `toml:"extra_packages"`
}

// Autorun configures the continuous relaunch loop.
type Autorun struct {
	// PerRunWall is the wall-clock budget for each bounded run.
	PerRunWall Duration `toml:"per_run_wall"`
	// MaxNoProgress stops the loop after this many consecutive runs without a
	// remote git HEAD change.
	MaxNoProgress int `toml:"max_noprogress"`
	// Cooldown is the pause between runs.
	Cooldown Duration `toml:"cooldown"`
	// MaxRuns optionally caps the total number of runs (0 = unlimited).
	MaxRuns int `toml:"max_runs"`
}

// Default returns a Config populated with sensible defaults. Load starts from
// these before decoding the file, so omitted fields keep their defaults.
func Default() Config {
	return Config{
		Task: "task.md",
		Guards: Guards{
			MaxWall:  Duration(3 * time.Hour),
			MaxIters: 500,
			MaxTurns: 200,
		},
		Auth: Auth{
			Claude: ClaudeKeychain,
			GitHub: GitHubGH,
		},
		Autorun: Autorun{
			PerRunWall:    Duration(3 * time.Hour),
			MaxNoProgress: 3,
			Cooldown:      Duration(30 * time.Second),
		},
	}
}

// Load reads and parses a TOML job file, applying defaults for omitted fields,
// then validates the result. It returns an error with actionable context on
// parse or validation failure.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("config %s: unknown key %q", path, undecoded[0].String())
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes a TOML job from a string (used by tests and `--config -`).
// Like Load it applies defaults and validates.
func Parse(data string) (Config, error) {
	cfg := Default()
	md, err := toml.Decode(data, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("config: unknown key %q", undecoded[0].String())
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
