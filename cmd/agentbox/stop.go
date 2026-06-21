package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/run"
)

func newStopCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <job|run>",
		Short: "Request a graceful stop of a run or autorun job",
		Long: "Writes the STOP control file for a run (and a job-level stop marker), and\n" +
			"signals any detached process. The supervisor halts at the next safe point.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			out := cmd.OutOrStdout()
			acted := false

			// 1. If ref is a run directory or run name, write its STOP file.
			if runDir, ok := resolveRunDir(g.runsDir, ref); ok {
				r, err := run.Open(runDir)
				if err != nil {
					return err
				}
				if err := r.WriteStop(); err != nil {
					return err
				}
				fmt.Fprintf(out, "wrote STOP in %s\n", runDir)
				acted = true
			}

			// 2. Job-level stop marker (consumed by autorun) + STOP in latest run.
			base := runsBase(g.runsDir)
			for _, marker := range []string{ref, ref + "-autorun"} {
				stopPath := filepath.Join(base, marker+".stop")
				if err := os.WriteFile(stopPath, []byte("stop\n"), 0o644); err == nil {
					if signalPidfile(out, filepath.Join(base, marker+".pid")) {
						acted = true
					}
				}
			}
			if latest, ok := latestRunFor(g.runsDir, ref); ok {
				if r, err := run.Open(latest); err == nil {
					_ = r.WriteStop()
					fmt.Fprintf(out, "wrote STOP in latest run %s\n", filepath.Base(latest))
					acted = true
				}
			}

			if !acted {
				return fmt.Errorf("no run or job matching %q found under %s", ref, g.runsDir)
			}
			return nil
		},
	}
	return cmd
}

// resolveRunDir returns the run directory for ref if ref is itself a run dir or
// a run name directly under runsDir.
func resolveRunDir(runsDir, ref string) (string, bool) {
	for _, c := range []string{ref, filepath.Join(runsDir, ref)} {
		if fileExists(filepath.Join(c, run.ControlDir)) {
			return c, true
		}
	}
	return "", false
}

// latestRunFor returns the most recent run directory whose name starts with the
// job name (run dirs are "<name>-<sortable-id>", so lexical max is newest).
func latestRunFor(runsDir, name string) (string, bool) {
	runs, err := listRuns(runsDir)
	if err != nil {
		return "", false
	}
	var best string
	for _, r := range runs {
		b := filepath.Base(r)
		if strings.HasPrefix(b, name+"-") && r > best {
			best = r
		}
	}
	return best, best != ""
}

// signalPidfile sends SIGTERM to the PID in pidPath, if present.
func signalPidfile(out interface{ Write([]byte) (int, error) }, pidPath string) bool {
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return false
	}
	fmt.Fprintf(out, "sent SIGTERM to pid %d\n", pid)
	return true
}
