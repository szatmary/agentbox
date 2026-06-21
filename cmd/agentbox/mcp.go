package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/mcp"
)

// defaultMCPAddr binds the HTTP transport to localhost by default: the `exec`
// tool runs commands in the user's sandbox, so the server must not be exposed
// to the network without an explicit, deliberate address. See the README.
const defaultMCPAddr = "127.0.0.1:7337"

func newMCPCmd(g *globalFlags) *cobra.Command {
	var httpAddr string
	var stdio bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the observe layer to AI agents over MCP (stdio or HTTP)",
		Long: "Runs an MCP server exposing agentbox runs as tools (list_runs, get_status,\n" +
			"tail_log, list_files, read_file, git_status, git_diff, exec, stop) so another\n" +
			"Claude can watch and steer live runs. Default transport is stdio; use\n" +
			"--http <addr> for the HTTP transport (localhost by default).\n\n" +
			"TRUST: the `exec` tool runs commands in your own sandbox VM. Do not expose\n" +
			"the HTTP transport beyond localhost without understanding the blast radius.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := mcp.NewServer(g.runsDir, container.NewCLIRuntime(), version)
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			if httpAddr != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agentbox mcp: HTTP transport on %s\n", httpAddr)
				return srv.ListenAndServeHTTP(ctx, httpAddr)
			}
			_ = stdio // stdio is the default; the flag is just for explicitness
			return srv.ServeStdio(ctx, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", true, "serve over stdio (default)")
	cmd.Flags().StringVar(&httpAddr, "http", "", "serve over HTTP on this address (e.g. "+defaultMCPAddr+")")
	cmd.Flags().Lookup("http").NoOptDefVal = defaultMCPAddr
	return cmd
}
