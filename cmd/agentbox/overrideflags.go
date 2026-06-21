package main

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/config"
)

// overrideVals backs the CLI flags that override job config fields.
type overrideVals struct {
	name   string
	repo   string
	task   string
	model  string
	claude string
	github string

	maxWall  time.Duration
	maxIters int
	maxTurns int

	perRunWall    time.Duration
	cooldown      time.Duration
	maxNoProgress int
	maxRuns       int
}

// register adds the common override flags to a command.
func (v *overrideVals) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&v.name, "name", "", "override job name")
	f.StringVar(&v.repo, "repo", "", "override git repo URL (empty disables git)")
	f.StringVar(&v.task, "task", "", "override path to the task file")
	f.StringVar(&v.model, "model", "", "override Claude model id")
	f.StringVar(&v.claude, "claude", "", "override claude credential source (keychain|api_key|token)")
	f.StringVar(&v.github, "github", "", "override github credential source (gh|pat|none)")
	f.DurationVar(&v.maxWall, "max-wall", 0, "override guards.max_wall")
	f.IntVar(&v.maxIters, "max-iters", 0, "override guards.max_iters")
	f.IntVar(&v.maxTurns, "max-turns", 0, "override guards.max_turns")
}

// registerAutorun adds the autorun-specific override flags.
func (v *overrideVals) registerAutorun(cmd *cobra.Command) {
	f := cmd.Flags()
	f.DurationVar(&v.perRunWall, "per-run-wall", 0, "override autorun.per_run_wall")
	f.DurationVar(&v.cooldown, "cooldown", 0, "override autorun.cooldown")
	f.IntVar(&v.maxNoProgress, "max-noprogress", 0, "override autorun.max_noprogress")
	f.IntVar(&v.maxRuns, "max-runs", 0, "override autorun.max_runs (0 = unlimited)")
}

// overrides builds a config.Overrides from the flags that were explicitly set.
func (v *overrideVals) overrides(cmd *cobra.Command) config.Overrides {
	f := cmd.Flags()
	o := config.Overrides{}
	if f.Changed("name") {
		o.Name = &v.name
	}
	if f.Changed("repo") {
		o.Repo = &v.repo
	}
	if f.Changed("task") {
		o.Task = &v.task
	}
	if f.Changed("model") {
		o.Model = &v.model
	}
	if f.Changed("claude") {
		o.Claude = &v.claude
	}
	if f.Changed("github") {
		o.GitHub = &v.github
	}
	if f.Changed("max-wall") {
		o.MaxWall = &v.maxWall
	}
	if f.Changed("max-iters") {
		o.MaxIters = &v.maxIters
	}
	if f.Changed("max-turns") {
		o.MaxTurns = &v.maxTurns
	}
	if f.Changed("per-run-wall") {
		o.PerRunWall = &v.perRunWall
	}
	if f.Changed("cooldown") {
		o.Cooldown = &v.cooldown
	}
	if f.Changed("max-noprogress") {
		o.MaxNoProgress = &v.maxNoProgress
	}
	if f.Changed("max-runs") {
		o.MaxRuns = &v.maxRuns
	}
	return o
}

func configArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "agentbox.toml"
}
