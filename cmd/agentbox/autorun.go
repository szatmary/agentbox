package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/autorun"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/supervisor"
)

// runnerFunc adapts a function to autorun.Runner.
type runnerFunc func(ctx context.Context) (supervisor.Result, error)

func (f runnerFunc) RunOnce(ctx context.Context) (supervisor.Result, error) { return f(ctx) }

func newAutorunCmd(g *globalFlags) *cobra.Command {
	var ov overrideVals
	var image string
	var detach bool
	cmd := &cobra.Command{
		Use:   "autorun [job.toml]",
		Short: "Continuously relaunch bounded sessions until the job converges",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, dir, err := loadJob(configArg(args), ov.overrides(cmd))
			if err != nil {
				return err
			}
			taskText, err := readTask(cfg, dir)
			if err != nil {
				return err
			}
			if detach {
				if started, err := maybeDetach(cmd, runsBase(g.runsDir), cfg.Name+"-autorun"); err != nil || started {
					return err
				}
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// O2: consume any stale job-level stop marker on startup so a previous
			// `stop` does not permanently poison future autoruns of this job.
			clearJobStop(g.runsDir, cfg.Name)

			// O2: remove our pidfile on exit (detached child only).
			defer detachPidfileCleanup(runsBase(g.runsDir), cfg.Name+"-autorun")()

			wall := autorunWall(cfg)
			loop := &autorun.Autorun{
				Runner: runnerFunc(func(ctx context.Context) (supervisor.Result, error) {
					return executeRun(ctx, cmd.OutOrStdout(), g.runsDir, cfg, taskText, image, wall)
				}),
				Options: autorun.Options{
					MaxNoProgress: cfg.Autorun.MaxNoProgress,
					Cooldown:      cfg.Autorun.Cooldown.D(),
					MaxRuns:       cfg.Autorun.MaxRuns,
				},
				StopRequested: jobStopRequested(g.runsDir, cfg.Name),
			}
			if cfg.Repo != "" {
				loop.Probe = autorun.GitHeadProbe{Repo: cfg.Repo}
			}

			res, err := loop.Run(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "autorun finished: status=%s runs=%d\n", res.Status, res.Runs)
			if res.Status == autorun.StatusFailed {
				return fmt.Errorf("agent reported FAILED: %s", res.Reason)
			}
			return nil
		},
	}
	ov.register(cmd)
	ov.registerAutorun(cmd)
	cmd.Flags().StringVar(&image, "image", defaultImageTag, "sandbox image to run")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "run in the background (pidfile + logfile)")
	return cmd
}

// jobStopRequested returns a poll function that reports true once a job-level
// STOP file appears at <runsBase>/<name>.stop (written by `agentbox stop`).
func jobStopRequested(runsDir, name string) func() bool {
	stopPath := filepath.Join(runsBase(runsDir), name+".stop")
	return func() bool {
		_, err := os.Stat(stopPath)
		return err == nil
	}
}

// clearJobStop removes a stale job-level stop marker at startup. Without this a
// `.stop` written by a previous `stop` is never cleared and would immediately
// halt every future autorun of the same job. See O2.
func clearJobStop(runsDir, name string) {
	_ = os.Remove(filepath.Join(runsBase(runsDir), name+".stop"))
}

// autorunWall is the per-run wall budget: the more-restrictive (smaller
// positive) of autorun.per_run_wall and guards.max_wall, so a --max-wall
// override (which sets guards.max_wall) is honored for autorun too. See O1.
func autorunWall(cfg config.Config) time.Duration {
	wall := cfg.Autorun.PerRunWall.D()
	if mw := cfg.Guards.MaxWall.D(); mw > 0 && (wall <= 0 || mw < wall) {
		wall = mw
	}
	return wall
}
