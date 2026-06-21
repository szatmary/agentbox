package autorun

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitHeadProbe reports the remote HEAD of a git repository via `git ls-remote`.
// It is never invoked during tests (a fake HeadProbe is used instead).
type GitHeadProbe struct {
	// Repo is the git remote URL (or path).
	Repo string
	// Ref is the ref to inspect; empty means "HEAD".
	Ref string
}

// lsRemoteArgs builds the `git ls-remote` argument vector. The `--` separator
// stops git from interpreting a crafted repo value as a flag; config.Validate
// also rejects '-'-leading and ext:: repos. See S4. Kept pure so the argv is
// unit-tested without invoking real git.
func lsRemoteArgs(repo, ref string) []string {
	if ref == "" {
		ref = "HEAD"
	}
	return []string{"ls-remote", "--", repo, ref}
}

// RemoteHead implements [HeadProbe].
func (p GitHeadProbe) RemoteHead(ctx context.Context) (string, error) {
	args := lsRemoteArgs(p.Repo, p.Ref)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", p.Repo, args[len(args)-1], err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}
