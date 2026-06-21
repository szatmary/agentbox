// Command agentbox runs autonomous, fully-sandboxed Claude coding agents in
// Apple `container` microVMs. See the README for the macOS-only runtime caveat.
package main

import (
	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// globalFlags holds flags shared across subcommands.
type globalFlags struct {
	runsDir string
}

func newRootCmd() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:   "agentbox",
		Short: "Run autonomous, sandboxed Claude coding agents in Apple container microVMs",
		Long: "agentbox starts a disposable sandbox VM, runs a bounded Claude Code session\n" +
			"inside it, resumes that session until the agent declares the task done, and can\n" +
			"relaunch fresh sessions until the work converges — all unattended.\n\n" +
			"The runtime is macOS-only (it drives Apple `container` and the macOS keychain).",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.runsDir, "runs-dir", ".agentbox/runs",
		"directory under which run directories are created")

	root.AddCommand(
		newInitCmd(),
		newDoctorCmd(g),
		newBuildCmd(g),
		newRunCmd(g),
		newAutorunCmd(g),
		newStatusCmd(g),
		newLogsCmd(g),
		newStopCmd(g),
		newShellCmd(g),
		newSSHCmd(g),
		newSSHProxyCmd(g),
		newAttachCmd(g),
		newMCPCmd(g),
	)
	return root
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}
