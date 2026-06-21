package attach

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/container"
	"golang.org/x/crypto/ssh"
)

func TestEnsureKeyPair(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "job-20260101T000000Z")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	pubLine, err := EnsureKeyPair(runDir)
	if err != nil {
		t.Fatalf("EnsureKeyPair: %v", err)
	}

	// Private key: exists, 0600, parses as a valid OpenSSH ed25519 key.
	privPath := PrivateKeyPath(runDir)
	info, err := os.Stat(privPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key perm = %o, want 600", perm)
	}
	privBytes, _ := os.ReadFile(privPath)
	signer, err := ssh.ParsePrivateKey(privBytes)
	if err != nil {
		t.Fatalf("private key does not parse: %v", err)
	}
	if got := signer.PublicKey().Type(); got != ssh.KeyAlgoED25519 {
		t.Errorf("key type = %q, want ed25519", got)
	}

	// Public line parses and matches the private key.
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine))
	if err != nil {
		t.Fatalf("public line does not parse: %v", err)
	}
	if !bytes.Equal(pub.Marshal(), signer.PublicKey().Marshal()) {
		t.Error("public key does not match private key")
	}
	if !strings.Contains(pubLine, "agentbox-job-20260101T000000Z") {
		t.Errorf("pub line missing run comment: %q", pubLine)
	}

	// Idempotent: a second call returns the same line and does not regenerate.
	pubLine2, err := EnsureKeyPair(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if pubLine2 != pubLine {
		t.Error("EnsureKeyPair regenerated keys on the second call")
	}
	privBytes2, _ := os.ReadFile(privPath)
	if !bytes.Equal(privBytes, privBytes2) {
		t.Error("private key changed on the second call")
	}
}

func TestAuthorizedKeysSetup(t *testing.T) {
	cmd := AuthorizedKeysSetup("ssh-ed25519 AAAAC3xyz agentbox-job")
	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("setup cmd shape = %v", cmd)
	}
	script := cmd[2]
	for _, want := range []string{
		"ssh-keygen -A", "/run/sshd", "authorized_keys",
		"chmod 600", "ssh-ed25519 AAAAC3xyz", "grep -qxF",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("setup script missing %q:\n%s", want, script)
		}
	}
}

func TestSSHDCommand(t *testing.T) {
	if got := strings.Join(SSHDCommand(), " "); got != "sudo /usr/sbin/sshd -i -e" {
		t.Errorf("sshd command = %q", got)
	}
}

func TestHostBlock(t *testing.T) {
	block := HostBlock(HostOptions{
		Alias:        "agentbox-job-1",
		IdentityFile: "/runs/job-1/ssh/id_ed25519",
		ProxyCommand: "/usr/local/bin/agentbox ssh-proxy job-1",
	})
	for _, want := range []string{
		"Host agentbox-job-1",
		"User agent",
		"IdentityFile /runs/job-1/ssh/id_ed25519",
		"IdentitiesOnly yes",
		"StrictHostKeyChecking no",
		"UserKnownHostsFile /dev/null",
		"ProxyCommand /usr/local/bin/agentbox ssh-proxy job-1",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("host block missing %q:\n%s", want, block)
		}
	}
}

func TestReplaceBlockAppendAndReplace(t *testing.T) {
	alias := "agentbox-job-1"
	b1 := HostBlock(HostOptions{Alias: alias, IdentityFile: "/k1", ProxyCommand: "p1"})

	// Append into existing unmanaged content.
	existing := "Host other\n    User someone\n"
	out := replaceBlock(existing, alias, b1)
	if !strings.Contains(out, "Host other") || !strings.Contains(out, "/k1") {
		t.Fatalf("append lost content:\n%s", out)
	}

	// Replace: a second install with a new identity file replaces, not duplicates.
	b2 := HostBlock(HostOptions{Alias: alias, IdentityFile: "/k2", ProxyCommand: "p2"})
	out2 := replaceBlock(out, alias, b2)
	if strings.Count(out2, "Host "+alias) != 1 {
		t.Errorf("alias duplicated:\n%s", out2)
	}
	if strings.Contains(out2, "/k1") || !strings.Contains(out2, "/k2") {
		t.Errorf("old identity not replaced:\n%s", out2)
	}
	if !strings.Contains(out2, "Host other") {
		t.Errorf("unmanaged content lost on replace:\n%s", out2)
	}
}

func TestInstallHostBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	alias := "agentbox-job-1"
	block := HostBlock(HostOptions{Alias: alias, IdentityFile: "/k", ProxyCommand: "p"})

	if err := InstallHostBlock(path, alias, block); err != nil {
		t.Fatalf("InstallHostBlock: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("ssh config perm = %o, want 600", perm)
	}
	// Idempotent: a second install does not duplicate the block.
	if err := InstallHostBlock(path, alias, block); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if n := strings.Count(string(b), "Host "+alias); n != 1 {
		t.Errorf("block appears %d times, want 1:\n%s", n, string(b))
	}
}

func TestRunSSHProxy(t *testing.T) {
	fake := &container.Fake{}
	fake.SetRunning("job-1", true)
	var sawCmd []string
	var sawStdin bool
	var sawTTY bool
	fake.ExecStreamFunc = func(ctx context.Context, id string, opts container.StreamOptions) (int, error) {
		sawCmd = opts.Cmd
		sawStdin = opts.Stdin != nil
		sawTTY = opts.TTY
		return 0, nil
	}
	in := strings.NewReader("ssh-bytes")
	code, err := RunSSHProxy(context.Background(), fake, "job-1", in, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || code != 0 {
		t.Fatalf("RunSSHProxy: code=%d err=%v", code, err)
	}
	if strings.Join(sawCmd, " ") != "sudo /usr/sbin/sshd -i -e" {
		t.Errorf("proxy ran %v", sawCmd)
	}
	if !sawStdin {
		t.Error("proxy must wire stdin (the SSH wire protocol)")
	}
	if sawTTY {
		t.Error("proxy must NOT allocate a TTY")
	}
}

func TestRunSSHProxyNotRunning(t *testing.T) {
	fake := &container.Fake{} // not running
	if _, err := RunSSHProxy(context.Background(), fake, "job-1", strings.NewReader(""),
		&bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("expected error when the run is not running")
	}
	if len(fake.CallsOf("ExecStream")) != 0 {
		t.Error("must not stream when the run is down")
	}
}
