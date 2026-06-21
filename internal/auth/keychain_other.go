//go:build !darwin

package auth

import "errors"

// SystemKeychain is the non-darwin stub. The macOS keychain is unavailable on
// other platforms, so Find always fails with a clear message. This keeps the
// whole module compilable and testable on Linux.
type SystemKeychain struct{}

// Find always returns an error on non-darwin platforms.
func (SystemKeychain) Find(service string) (string, error) {
	return "", errors.New("keychain access is only available on macOS; use auth.claude = \"api_key\" or \"token\" instead")
}
