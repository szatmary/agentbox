package main

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
	"github.com/szatmary/agentbox/internal/supervisor"
)

func newRunCmd(g *globalFlags) *cobra.Command {
	var ov overrideVals
	var image string
	var detach bool
	cmd := &cobra.Command{
		Use:   "run [job.toml]",
		Short: "Run one bounded supervised session",
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
				if started, err := maybeDetach(cmd, runsBase(g.runsDir), cfg.Name); err != nil || started {
					return err
				}
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			res, err := executeRun(ctx, cmd.OutOrStdout(), g.runsDir, cfg, taskText, image, cfg.Guards.MaxWall.D())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "run finished: status=%s iterations=%d elapsed=%s\n",
				res.Status, res.Iterations, res.Elapsed.Round(time.Second))
			if res.Status == supervisor.StatusFailed {
				return fmt.Errorf("agent reported FAILED: %s", res.Reason)
			}
			return nil
		},
	}
	ov.register(cmd)
	cmd.Flags().StringVar(&image, "image", defaultImageTag, "sandbox image to run")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "run in the background (pidfile + logfile)")
	return cmd
}

// executeRun performs one bounded supervised run and returns its result. It is
// shared by `run` and `autorun`.
func executeRun(ctx context.Context, out io.Writer, runsDir string, cfg config.Config, taskText, image string, wall time.Duration) (supervisor.Result, error) {
	r, err := run.Create(runsDir, cfg.Name, newRunID())
	if err != nil {
		return supervisor.Result{}, err
	}
	defer r.Close()

	inj, err := resolveAuth(ctx, cfg)
	if err != nil {
		return supervisor.Result{}, err
	}
	if _, err := writeCredentials(r, inj); err != nil {
		return supervisor.Result{}, err
	}

	rt := container.NewCLIRuntime()
	sup := supervisor.New(rt, supervisor.Options{
		Image:    image,
		Name:     cfg.Name + "-" + r.ID,
		Task:     taskText,
		Model:    cfg.Model.Name,
		MaxWall:  wall,
		MaxIters: cfg.Guards.MaxIters,
		MaxTurns: cfg.Guards.MaxTurns,
		Env:      envFor(inj),
		Mounts:   mountsFor(r),
		Setup:    buildSetup(inj, cfg.Repo),
		LogOut:   r.LogWriter(),
	})
	sup.Log = r.Logger()

	fmt.Fprintf(out, "run %s (claude=%s github=%s)\n  dir: %s\n",
		r.Name+"-"+r.ID, inj.ClaudeSource, inj.GitHubSource, r.Root)
	return sup.Run(ctx)
}
