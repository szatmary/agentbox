package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/autorun"
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

			loop := &autorun.Autorun{
				Runner: runnerFunc(func(ctx context.Context) (supervisor.Result, error) {
					return executeRun(ctx, cmd.OutOrStdout(), g.runsDir, cfg, taskText, image, cfg.Autorun.PerRunWall.D())
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
