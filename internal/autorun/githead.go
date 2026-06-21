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

// RemoteHead implements [HeadProbe].
func (p GitHeadProbe) RemoteHead(ctx context.Context) (string, error) {
	ref := p.Ref
	if ref == "" {
		ref = "HEAD"
	}
	out, err := exec.CommandContext(ctx, "git", "ls-remote", p.Repo, ref).Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", p.Repo, ref, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}
