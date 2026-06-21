package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/auth"
	"github.com/szatmary/agentbox/internal/config"
)

// check status levels.
const (
	statusOK   = "OK"
	statusWarn = "WARN"
	statusFail = "FAIL"
)

type checkResult struct {
	name   string
	status string
	detail string
}

// prober abstracts host probing so doctor's logic is testable.
type prober interface {
	lookPath(name string) (string, error)
	runOK(ctx context.Context, name string, args ...string) (ok bool, output string)
}

type osProber struct{}

func (osProber) lookPath(name string) (string, error) { return exec.LookPath(name) }

func (osProber) runOK(ctx context.Context, name string, args ...string) (bool, string) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return err == nil, strings.TrimSpace(string(out))
}

func newDoctorCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [job.toml]",
		Short: "Check prerequisites (container, gh, Claude credential)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			cfg.Name, cfg.Task = "doctor", "task.md"
			if p := configArg(args); fileExists(p) {
				if loaded, err := config.Load(p); err == nil {
					cfg = loaded
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "note: could not load %s (%v); using defaults\n", p, err)
				}
			}
			results := runDoctor(cmd.Context(), osProber{}, auth.NewResolver(), cfg)
			ok := printChecks(cmd, results)
			if !ok {
				return fmt.Errorf("doctor found problems")
			}
			return nil
		},
	}
	return cmd
}

// runDoctor performs the prerequisite checks and returns the results.
func runDoctor(ctx context.Context, p prober, resolver auth.Resolver, cfg config.Config) []checkResult {
	var out []checkResult

	if _, err := p.lookPath("container"); err != nil {
		out = append(out, checkResult{"container CLI", statusFail,
			"not found in PATH — install Apple `container` (macOS 26+ only)"})
	} else {
		out = append(out, checkResult{"container CLI", statusOK, "found"})
		if ok, msg := p.runOK(ctx, "container", "system", "status"); ok {
			out = append(out, checkResult{"container service", statusOK, "running"})
		} else {
			out = append(out, checkResult{"container service", statusWarn,
				"not confirmed running (`container system start`?): " + truncate(msg)})
		}
		if ok, msg := p.runOK(ctx, "container", "ls"); ok {
			out = append(out, checkResult{"container ready", statusOK, "can list containers"})
		} else {
			out = append(out, checkResult{"container ready", statusWarn, truncate(msg)})
		}
	}

	if cfg.Auth.GitHub == config.GitHubGH {
		if _, err := p.lookPath("gh"); err != nil {
			out = append(out, checkResult{"gh CLI", statusFail, "auth.github=gh but `gh` not found in PATH"})
		} else if ok, msg := p.runOK(ctx, "gh", "auth", "status"); ok {
			out = append(out, checkResult{"gh CLI", statusOK, "authenticated"})
		} else {
			out = append(out, checkResult{"gh CLI", statusWarn, "found but not authenticated (`gh auth login`): " + truncate(msg)})
		}
	} else {
		out = append(out, checkResult{"gh CLI", statusOK, "not required (auth.github=" + cfg.Auth.GitHub + ")"})
	}

	if inj, err := resolver.Resolve(ctx, cfg.Auth); err != nil {
		out = append(out, checkResult{"Claude credential", statusFail, err.Error()})
	} else {
		out = append(out, checkResult{"Claude credential", statusOK, "source=" + inj.ClaudeSource})
	}

	return out
}

func printChecks(cmd *cobra.Command, results []checkResult) bool {
	w := cmd.OutOrStdout()
	allOK := true
	for _, r := range results {
		if r.status == statusFail {
			allOK = false
		}
		fmt.Fprintf(w, "[%-4s] %-20s %s\n", r.status, r.name, r.detail)
	}
	if allOK {
		fmt.Fprintln(w, "\nAll required checks passed.")
	} else {
		fmt.Fprintln(w, "\nSome checks FAILED — see above.")
	}
	return allOK
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func truncate(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
