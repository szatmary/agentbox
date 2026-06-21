package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/attach"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/observe"
)

// codeRunner launches the VSCode CLI; injected for tests.
type codeRunner func(args ...string) error

func realCodeRunner(args ...string) error {
	c := exec.Command("code", args...)
	c.Stdout, c.Stderr, c.Stdin = os.Stdout, os.Stderr, os.Stdin
	return c.Run()
}

// --- agentbox ssh-proxy <run> (hidden ProxyCommand) ---------------------------

func newSSHProxyCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "ssh-proxy <run>",
		Short:  "SSH ProxyCommand: pipe sshd -i in a live run's VM over container exec",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Use the raw OS stdio: ssh speaks its binary protocol over these.
			_, err := runSSHProxy(cmd.Context(), g.runsDir, args[0], container.NewCLIRuntime(),
				os.Stdin, os.Stdout, os.Stderr)
			return err
		},
	}
	return cmd
}

// runSSHProxy resolves the run and pipes sshd -i. It returns the sshd exit code;
// a non-zero exit is a normal connection close, not an error.
func runSSHProxy(ctx context.Context, runsDir, ref string, rt container.Runtime,
	stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	runDir, err := resolveRun(runsDir, ref)
	if err != nil {
		return 1, err
	}
	return attach.RunSSHProxy(ctx, rt, filepath.Base(runDir), stdin, stdout, stderr)
}

// --- agentbox ssh <run> -------------------------------------------------------

func newSSHCmd(g *globalFlags) *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "ssh <run>",
		Short: "Install (or print) an ~/.ssh/config Host block for a live run",
		Long: "Generates an `~/.ssh/config` Host block that reaches a run's VM through a\n" +
			"ProxyCommand (`agentbox ssh-proxy`) over `container exec` — no ports are\n" +
			"opened. After this you can `ssh agentbox-<run>` or point any SSH client at it.\n\n" +
			"The run must have been started with `[attach] ssh = true` (which installs the\n" +
			"matching key into the VM and generates the per-run keypair).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := defaultSSHConfigPath()
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			alias, err := installSSHForRun(cmd.OutOrStdout(), g.runsDir, args[0], cfgPath, exe, printOnly)
			if err != nil {
				return err
			}
			if !printOnly {
				fmt.Fprintf(cmd.OutOrStdout(), "connect with:  ssh %s\n", alias)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the Host block instead of installing it")
	return cmd
}

// --- agentbox attach <run> --vscode ------------------------------------------

func newAttachCmd(g *globalFlags) *cobra.Command {
	var vscode bool
	cmd := &cobra.Command{
		Use:   "attach <run>",
		Short: "Attach an editor to a live run (VSCode Remote-SSH over container exec)",
		Long: "Installs the run's `~/.ssh/config` Host block and, with --vscode, opens the\n" +
			"run's workspace in VSCode via Remote-SSH. The SSH connection is tunneled\n" +
			"through `container exec` (no open ports). Requires `[attach] ssh = true`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := defaultSSHConfigPath()
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return runAttach(cmd.OutOrStdout(), g.runsDir, args[0], cfgPath, exe, vscode, realCodeRunner)
		},
	}
	cmd.Flags().BoolVar(&vscode, "vscode", false, "open the run's workspace in VSCode Remote-SSH")
	return cmd
}

func runAttach(out io.Writer, runsDir, ref, cfgPath, exe string, vscode bool, runCode codeRunner) error {
	alias, err := installSSHForRun(out, runsDir, ref, cfgPath, exe, false)
	if err != nil {
		return err
	}
	if !vscode {
		fmt.Fprintf(out, "configured %s; open VSCode with --vscode or `ssh %s`\n", alias, alias)
		return nil
	}
	remote := "ssh-remote+" + alias
	fmt.Fprintf(out, "opening VSCode: code --remote %s %s\n", remote, observe.DefaultWorkdir)
	return runCode("--remote", remote, observe.DefaultWorkdir)
}

// installSSHForRun ensures the run's keypair exists, renders the Host block, and
// installs it into cfgPath (or, when printOnly, writes it to out). It returns the
// Host alias. The ProxyCommand embeds an ABSOLUTE runs-dir so it works no matter
// the cwd ssh is invoked from.
func installSSHForRun(out io.Writer, runsDir, ref, cfgPath, exe string, printOnly bool) (string, error) {
	runDir, err := resolveRun(runsDir, ref)
	if err != nil {
		return "", err
	}
	runName := filepath.Base(runDir)

	privPath := attach.PrivateKeyPath(runDir)
	if !fileExists(privPath) {
		return "", fmt.Errorf("run %q has no SSH key (start it with [attach] ssh = true)", runName)
	}

	absRunsDir, err := filepath.Abs(runsDir)
	if err != nil {
		return "", err
	}
	proxy := fmt.Sprintf("%s ssh-proxy %s --runs-dir %s",
		shellQuote(exe), shellQuote(runName), shellQuote(absRunsDir))
	alias := attach.Alias(runName)
	block := attach.HostBlock(attach.HostOptions{
		Alias:        alias,
		IdentityFile: privPath,
		ProxyCommand: proxy,
	})

	if printOnly {
		fmt.Fprint(out, block)
		return alias, nil
	}
	if err := attach.InstallHostBlock(cfgPath, alias, block); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "installed Host %s into %s\n", alias, cfgPath)
	return alias, nil
}

// defaultSSHConfigPath returns ~/.ssh/config.
func defaultSSHConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}
