package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/run"
)

func newLogsCmd(g *globalFlags) *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs <run>",
		Short: "Print a run's log (logs/run.log)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logPath, err := resolveLogPath(g.runsDir, args[0])
			if err != nil {
				return err
			}
			if !follow {
				b, err := os.ReadFile(logPath)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(b)
				return err
			}
			return followFile(cmd.Context(), cmd.OutOrStdout(), logPath)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow the log (like tail -f)")
	return cmd
}

// resolveLogPath maps a run name or directory to its log file path.
func resolveLogPath(runsDir, ref string) (string, error) {
	candidates := []string{
		filepath.Join(ref, run.LogsDir, run.LogFile),               // ref is a run dir
		filepath.Join(runsDir, ref, run.LogsDir, run.LogFile),      // ref is a run name
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("no log found for run %q (looked under %s)", ref, runsDir)
}

// followFile streams a file's contents, then polls for appended data until the
// context is cancelled.
func followFile(ctx context.Context, w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}
		if err != nil {
			return err
		}
	}
}
