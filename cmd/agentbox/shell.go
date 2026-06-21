package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/observe"
)

func newShellCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell <run> [-- command...]",
		Short: "Open an interactive shell (or run a command) in a live run's VM",
		Long: "Attaches an interactive shell to a running run's sandbox via\n" +
			"`container exec -it` — the quick-poke path that needs no SSH. With a\n" +
			"trailing `-- command...` it runs that command instead of bash.\n\n" +
			"Everything rides on `container exec`; no ports are opened.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd.Context(), g.runsDir, args[0], args[1:],
				container.NewCLIRuntime(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

// runShell resolves a live run and attaches stdio to a TTY exec inside its VM.
// command is the command to run (default bash). The runtime is injected so the
// resolution/argv path is testable against the fake without a real VM.
func runShell(ctx context.Context, runsDir, ref string, command []string, rt container.Runtime,
	stdin io.Reader, stdout, stderr io.Writer) error {
	runDir, err := resolveRun(runsDir, ref)
	if err != nil {
		return err
	}
	name := filepath.Base(runDir)
	c, err := rt.Inspect(ctx, name)
	if err != nil {
		return fmt.Errorf("resolve run %q: %w", name, err)
	}
	if !c.Running {
		return fmt.Errorf("run %q is not running", name)
	}
	id := c.ID
	if id == "" {
		id = name
	}
	if len(command) == 0 {
		command = []string{"bash"}
	}
	code, err := rt.ExecStream(ctx, id, container.StreamOptions{
		Cmd:     command,
		Workdir: observe.DefaultWorkdir,
		TTY:     true,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("shell exited %d", code)
	}
	return nil
}
