package main

import (
	"fmt"
	"os"
	"path/filepath"

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
			fmt.Fprintln(cmd.OutOrStdout(), "Edit agentbox.toml and task.md, then run `agentbox doctor`.")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "job name (default: current directory name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return cmd
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
