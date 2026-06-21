package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/run"
)

func newStatusCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List runs and their status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runs, err := listRuns(g.runsDir)
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no runs under %s\n", g.runsDir)
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "RUN\tSTATUS\tDETAIL")
			for _, r := range runs {
				status, detail := runStatus(r)
				fmt.Fprintf(w, "%s\t%s\t%s\n", filepath.Base(r), status, detail)
			}
			return w.Flush()
		},
	}
	return cmd
}

// listRuns returns the run directories under runsDir, sorted by name.
func listRuns(runsDir string) ([]string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(runsDir, e.Name())
		if _, err := os.Stat(filepath.Join(p, run.ControlDir)); err == nil {
			dirs = append(dirs, p)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// runStatus reports a run's status from its STATUS file (or "running"/"unknown").
func runStatus(runDir string) (status, detail string) {
	b, err := os.ReadFile(filepath.Join(runDir, run.ControlDir, run.StatusFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "running?", "no STATUS file (in progress or interrupted)"
		}
		return "unknown", err.Error()
	}
	sent := run.ParseStatus(string(b))
	switch {
	case sent.Done:
		return "DONE", firstLine(string(b))
	case sent.Failed:
		return "FAILED", sent.Reason
	default:
		return "running?", firstLine(string(b))
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
