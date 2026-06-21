package attach

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostOptions parameterizes a generated `~/.ssh/config` Host block.
type HostOptions struct {
	// Alias is the Host alias users `ssh` to, e.g. "agentbox-job-<id>".
	Alias string
	// IdentityFile is the absolute path to the run's private key.
	IdentityFile string
	// ProxyCommand is the full command ssh runs to reach the VM, e.g.
	// "/usr/local/bin/agentbox ssh-proxy job-<id>".
	ProxyCommand string
}

// Alias returns the conventional SSH Host alias for a run name.
func Alias(runName string) string { return "agentbox-" + runName }

// HostBlock renders the managed `~/.ssh/config` Host block for a run. The block
// is wrapped in BEGIN/END markers so InstallHostBlock can replace it idempotently.
//
// Host-key checking is disabled because each run gets ephemeral, throwaway host
// keys reached only through the local exec tunnel — there is no stable host
// identity to pin, and nothing is exposed on the network. See the README trust
// model.
func HostBlock(o HostOptions) string {
	begin := beginMarker(o.Alias)
	end := endMarker(o.Alias)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", begin)
	fmt.Fprintf(&b, "Host %s\n", o.Alias)
	fmt.Fprintf(&b, "    User %s\n", VMUser)
	fmt.Fprintf(&b, "    IdentityFile %s\n", o.IdentityFile)
	fmt.Fprintf(&b, "    IdentitiesOnly yes\n")
	fmt.Fprintf(&b, "    StrictHostKeyChecking no\n")
	fmt.Fprintf(&b, "    UserKnownHostsFile /dev/null\n")
	fmt.Fprintf(&b, "    LogLevel ERROR\n")
	fmt.Fprintf(&b, "    ProxyCommand %s\n", o.ProxyCommand)
	fmt.Fprintf(&b, "%s\n", end)
	return b.String()
}

func beginMarker(alias string) string { return "# >>> agentbox " + alias + " >>>" }
func endMarker(alias string) string   { return "# <<< agentbox " + alias + " <<<" }

// InstallHostBlock writes block into the ssh config at path, replacing any
// existing managed block for the same alias (matched by markers) and otherwise
// appending. Unmanaged content is preserved verbatim. The config file is created
// 0600 with its parent dir 0700 if absent.
func InstallHostBlock(path, alias, block string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("attach: mkdir %s: %w", dir, err)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("attach: reading ssh config: %w", err)
	}
	updated := replaceBlock(string(existing), alias, block)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		return fmt.Errorf("attach: writing ssh config: %w", err)
	}
	return nil
}

// replaceBlock returns content with the managed block for alias replaced by
// block, or block appended if no managed block is present. It is pure and tested.
func replaceBlock(content, alias, block string) string {
	begin := beginMarker(alias)
	end := endMarker(alias)
	lines := strings.Split(content, "\n")
	var out []string
	inBlock := false
	replaced := false
	for _, line := range lines {
		switch {
		case strings.TrimSpace(line) == begin:
			inBlock = true
		case strings.TrimSpace(line) == end:
			inBlock = false
			// Splice the new block in place of the old one (once).
			if !replaced {
				out = append(out, strings.TrimRight(block, "\n"))
				replaced = true
			}
		case !inBlock:
			out = append(out, line)
		}
	}
	result := strings.Join(out, "\n")
	if !replaced {
		// Append, ensuring a blank line of separation.
		result = strings.TrimRight(result, "\n")
		if result != "" {
			result += "\n\n"
		}
		result += strings.TrimRight(block, "\n") + "\n"
	} else {
		result = strings.TrimRight(result, "\n") + "\n"
	}
	return result
}
