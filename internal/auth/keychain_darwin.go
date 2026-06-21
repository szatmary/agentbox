//go:build darwin

package auth

import (
	"fmt"
	"os/exec"
	"strings"
)

// SystemKeychain reads generic-password items from the macOS keychain via the
// `security` CLI. This file compiles only on darwin.
type SystemKeychain struct{}

// Find returns the password value (`-w`) for the generic-password item with the
// given service name.
func (SystemKeychain) Find(service string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("security find-generic-password -s %q: %w", service, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
