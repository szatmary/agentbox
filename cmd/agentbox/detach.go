package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// detachedEnv marks a re-exec'd child so it does not detach again.
const detachedEnv = "AGENTBOX_DETACHED"

// maybeDetach backgrounds the current command when running as the parent of a
// --detach invocation. It returns started=true when it spawned a child (the
// caller should then return immediately). When already running as the detached
// child, or when detaching is not requested, it returns started=false and the
// caller proceeds in the foreground.
func maybeDetach(cmd *cobra.Command, base, name string) (started bool, err error) {
	if os.Getenv(detachedEnv) == "1" {
		return false, nil // we are the child
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	logPath := filepath.Join(base, name+".log")
	pidPath := filepath.Join(base, name+".pid")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer logf.Close()

	child := exec.Command(exe, stripDetach(os.Args[1:])...)
	child.Env = append(os.Environ(), detachedEnv+"=1")
	child.Stdout = logf
	child.Stderr = logf
	child.Stdin = nil
	if err := child.Start(); err != nil {
		return false, err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)+"\n"), 0o644); err != nil {
		return false, err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "detached: pid %d\n  logs: %s\n  stop: agentbox stop %s\n",
		child.Process.Pid, logPath, name)
	return true, child.Process.Release()
}

// stripDetach removes the --detach/-d flag tokens from an argument list.
func stripDetach(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--detach" || a == "-d" || strings.HasPrefix(a, "--detach=") {
			continue
		}
		out = append(out, a)
	}
	return out
}
