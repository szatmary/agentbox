// Package attach implements SSH-over-exec attachment to a live agentbox run.
//
// Everything rides on `container exec` (the only reliable transport on this
// Apple `container` setup): no ports are published and nothing listens on the VM
// IP. SSH reaches the VM through a ProxyCommand — `agentbox ssh-proxy <run>` —
// which runs `sshd -i` (inetd mode) inside the VM over a piped exec, decoupling
// `~/.ssh/config` from the per-run container name.
//
// This package is split into pure, testable pieces:
//   - keys.go     — ephemeral per-run ed25519 keypair (private key 0600) and the
//     in-VM setup command that installs host keys + authorized_keys.
//   - sshconfig.go — render and idempotently install the `~/.ssh/config` Host block.
//   - proxy.go    — the ProxyCommand body: resolve the live VM and pipe `sshd -i`.
package attach

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SSHDir is the per-run subdirectory holding the ephemeral SSH keypair.
const SSHDir = "ssh"

// PrivateKeyName / PublicKeyName are the key filenames within SSHDir.
const (
	PrivateKeyName = "id_ed25519"
	PublicKeyName  = "id_ed25519.pub"
)

// VMUser is the non-root user inside the sandbox that SSH authenticates as.
const VMUser = "agent"

// SSHDPath is the sshd binary path inside the sandbox image.
const SSHDPath = "/usr/sbin/sshd"

// PrivateKeyPath returns the host path of a run's private key.
func PrivateKeyPath(runDir string) string {
	return filepath.Join(runDir, SSHDir, PrivateKeyName)
}

// PublicKeyPath returns the host path of a run's public key.
func PublicKeyPath(runDir string) string {
	return filepath.Join(runDir, SSHDir, PublicKeyName)
}

// EnsureKeyPair generates an ephemeral ed25519 keypair for the run (if not
// already present) under <runDir>/ssh/, writing the OpenSSH-format private key
// 0600 and the public key 0644. It returns the trimmed public-key line (the
// authorized_keys entry). Regeneration is skipped when both files already exist,
// so it is safe to call repeatedly across resumes.
func EnsureKeyPair(runDir string) (pubLine string, err error) {
	dir := filepath.Join(runDir, SSHDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("attach: mkdir %s: %w", dir, err)
	}
	privPath := PrivateKeyPath(runDir)
	pubPath := PublicKeyPath(runDir)

	if fileExists(privPath) && fileExists(pubPath) {
		b, err := os.ReadFile(pubPath)
		if err != nil {
			return "", fmt.Errorf("attach: reading existing public key: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}

	comment := "agentbox-" + filepath.Base(runDir)
	privPEM, pubLine, err := generateEd25519(comment)
	if err != nil {
		return "", err
	}
	// Write the private key 0600 BEFORE the public key so a crash never leaves a
	// world-readable private key. Use O_EXCL semantics via 0600 create.
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return "", fmt.Errorf("attach: writing private key: %w", err)
	}
	if err := os.Chmod(privPath, 0o600); err != nil {
		return "", fmt.Errorf("attach: chmod private key: %w", err)
	}
	if err := os.WriteFile(pubPath, []byte(pubLine+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("attach: writing public key: %w", err)
	}
	return pubLine, nil
}

// generateEd25519 returns the OpenSSH-format private key PEM bytes and the
// single-line authorized_keys entry for a fresh ed25519 keypair.
func generateEd25519(comment string) (privPEM []byte, pubLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("attach: generating ed25519 key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, "", fmt.Errorf("attach: marshaling private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("attach: marshaling public key: %w", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	return pem.EncodeToMemory(block), line, nil
}

// AuthorizedKeysSetup returns the one-time in-VM command that prepares the VM
// for SSH-over-exec: generate host keys, create the privsep dir, and install
// pubLine into the agent user's authorized_keys (idempotently). It runs as the
// agent user and uses passwordless sudo for the root-only steps (host keys,
// /run/sshd). The public key is not secret, so embedding it in argv is fine.
func AuthorizedKeysSetup(pubLine string) []string {
	q := shellSingleQuote(pubLine)
	script := strings.Join([]string{
		"set -e",
		"sudo ssh-keygen -A",         // generate /etc/ssh/ssh_host_* if absent
		"sudo mkdir -p /run/sshd",    // sshd privilege-separation dir
		`mkdir -p "$HOME/.ssh"`,
		`chmod 700 "$HOME/.ssh"`,
		// Append the key only if it is not already present (idempotent on resume).
		`touch "$HOME/.ssh/authorized_keys"`,
		`grep -qxF ` + q + ` "$HOME/.ssh/authorized_keys" || printf '%s\n' ` + q + ` >> "$HOME/.ssh/authorized_keys"`,
		`chmod 600 "$HOME/.ssh/authorized_keys"`,
	}, "\n")
	return []string{"sh", "-c", script}
}

// SSHDCommand is the in-VM command the ProxyCommand runs: sshd in inetd mode.
// It runs under sudo because sshd needs root for host keys and privilege
// separation; the agent user has NOPASSWD sudo in the image.
func SSHDCommand() []string {
	return []string{"sudo", SSHDPath, "-i", "-e"}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// shellSingleQuote single-quotes s for safe inclusion in a /bin/sh script.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
