package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/embedfs"
)

func newInitCmd() *cobra.Command {
	var name string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold an agentbox.toml and task.md in the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if name == "" {
				name = sanitizeName(filepath.Base(cwd))
			}
			cfg, err := embedfs.ConfigTemplate(name)
			if err != nil {
				return err
			}
			task, err := embedfs.TaskTemplate()
			if err != nil {
				return err
			}
			if err := writeScaffold(cmd, filepath.Join(cwd, "agentbox.toml"), cfg, force); err != nil {
				return err
			}
			if err := writeScaffold(cmd, filepath.Join(cwd, "task.md"), task, force); err != nil {
				return err
			}
			// Ensure the run dir (which transiently holds staged credentials) is
			// never committed. See H2.
			if err := ensureGitignore(cmd, filepath.Join(cwd, ".gitignore"), gitignoreEntry); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Edit agentbox.toml and task.md, then run `agentbox doctor`.")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "job name (default: current directory name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return cmd
}

// gitignoreEntry is the run-directory pattern init ensures is ignored. The run
// dir transiently holds the staged 0600 credential/env files.
const gitignoreEntry = ".agentbox/"

// ensureGitignore makes sure path contains entry, creating the file if missing
// and appending the entry (preserving existing content) if absent. It is a no-op
// when the entry is already present.
func ensureGitignore(cmd *cobra.Command, path, entry string) error {
	out := cmd.OutOrStdout()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	existing := string(b)
	for _, line := range strings.Split(existing, "\n") {
		if strings.TrimSpace(line) == entry {
			fmt.Fprintf(out, "skip (already ignored): %s in %s\n", entry, path)
			return nil
		}
	}
	var sb strings.Builder
	sb.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString(entry + "\n")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	if existing == "" {
		fmt.Fprintf(out, "wrote %s\n", path)
	} else {
		fmt.Fprintf(out, "added %s to %s\n", entry, path)
	}
	return nil
}

func writeScaffold(cmd *cobra.Command, path, content string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Fprintf(cmd.OutOrStdout(), "skip (exists): %s\n", path)
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "myjob"
	}
	return string(out)
}
