package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/auth"
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

			// O2: when running as the detached child, remove our pidfile on exit
			// so `stop` can never signal a dead/reused PID via a stale pidfile.
			defer detachPidfileCleanup(runsBase(g.runsDir), cfg.Name)()

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

// executeRun performs one bounded supervised run with the production runtime and
// credential resolver. It is shared by `run` and `autorun`.
func executeRun(ctx context.Context, out io.Writer, runsDir string, cfg config.Config, taskText, image string, wall time.Duration) (supervisor.Result, error) {
	return executeRunWith(ctx, out, runsDir, cfg, taskText, image, wall, container.NewCLIRuntime(), auth.NewResolver())
}

// executeRunWith is the testable core: the sandbox Runtime and credential
// Resolver are injected so the config→auth→supervisor wiring (mounts, model,
// secret routing) can be exercised with fakes. See H3.
func executeRunWith(ctx context.Context, out io.Writer, runsDir string, cfg config.Config, taskText, image string, wall time.Duration, rt container.Runtime, resolver auth.Resolver) (supervisor.Result, error) {
	r, err := run.Create(runsDir, cfg.Name, newRunID())
	if err != nil {
		return supervisor.Result{}, err
	}
	defer r.Close()

	inj, err := resolver.Resolve(ctx, cfg.Auth)
	if err != nil {
		return supervisor.Result{}, err
	}

	// S1/S2: stage secrets in a 0600 env/cred file outside the mounted control
	// dir, mount it read-only, and remove it after teardown.
	secretsMount, cleanup, err := stageSecrets(r, inj)
	if err != nil {
		return supervisor.Result{}, err
	}
	defer cleanup()

	sup := supervisor.New(rt, supervisor.Options{
		Image:       image,
		Name:        cfg.Name + "-" + r.ID,
		Task:        taskText,
		Model:       cfg.Model.Name,
		MaxWall:     wall,
		MaxIters:    cfg.Guards.MaxIters,
		MaxTurns:    cfg.Guards.MaxTurns,
		Mounts:      append(mountsFor(r), secretsMount),
		Setup:       buildSetup(inj, cfg.Repo),
		SecretsFile: secretsEnvFileInVM,
		LogOut:      r.LogWriter(),
	})
	sup.Log = r.Logger()

	fmt.Fprintf(out, "run %s (claude=%s github=%s)\n  dir: %s\n",
		r.Name+"-"+r.ID, inj.ClaudeSource, inj.GitHubSource, r.Root)
	return sup.Run(ctx)
}

// detachPidfileCleanup returns a func that removes this run's detached pidfile
// when invoked, but only when running as the detached child (the parent that
// spawned us wrote the pidfile). Removing it on clean exit means `stop` never
// finds a stale PID to signal. See O2.
func detachPidfileCleanup(base, name string) func() {
	if os.Getenv(detachedEnv) != "1" {
		return func() {}
	}
	return func() { _ = os.Remove(filepath.Join(base, name+".pid")) }
}
