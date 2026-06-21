package auth

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GHCLI resolves a GitHub token by running `gh auth token`. It is never invoked
// during tests (a fake GitHubTokener is used instead).
type GHCLI struct{}

// Token implements [GitHubTokener].
func (GHCLI) Token(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
