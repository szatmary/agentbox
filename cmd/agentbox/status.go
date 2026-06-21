package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/observe"
	"github.com/szatmary/agentbox/internal/run"
)

func newStatusCmd(g *globalFlags) *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List runs and their status",
		Long: "Lists runs and their STATUS sentinel. With --live, also probes each run's\n" +
			"sandbox via `container inspect` and shows whether the VM is currently up.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var rt container.Runtime
			if live {
				rt = container.NewCLIRuntime()
			}
			rows, err := gatherStatus(cmd.Context(), g.runsDir, rt)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no runs under %s\n", g.runsDir)
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "RUN\tLIVE\tSTATUS\tDETAIL")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.name, r.live, r.status, r.detail)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "probe each run's sandbox for liveness (container inspect)")
	return cmd
}

// statusRow is one run's rendered status.
type statusRow struct {
	name, live, status, detail string
}

// gatherStatus collects status rows for all runs under runsDir. When rt is
// non-nil it also probes each run's sandbox liveness via the observe layer; a
// nil rt (or a probe error) leaves the LIVE column as "-". Pure logic over the
// injected runtime, so the live path is testable against the fake.
func gatherStatus(ctx context.Context, runsDir string, rt container.Runtime) ([]statusRow, error) {
	runs, err := listRuns(runsDir)
	if err != nil {
		return nil, err
	}
	var rows []statusRow
	for _, r := range runs {
		status, detail := runStatus(r)
		row := statusRow{name: filepath.Base(r), live: "-", status: status, detail: detail}
		if rt != nil {
			if o, err := observe.New(rt, r); err == nil {
				if st, err := o.Status(ctx); err == nil {
					if st.Running {
						row.live = "running"
					} else {
						row.live = "stopped"
					}
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
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
