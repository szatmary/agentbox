package attach

import (
	"context"
	"fmt"
	"io"

	"github.com/szatmary/agentbox/internal/container"
)

// RunSSHProxy is the body of `agentbox ssh-proxy <run>`: it resolves the run's
// live sandbox container by name and runs `sshd -i` inside it with the caller's
// stdio piped straight through, so the local ssh client speaks the SSH protocol
// to the in-VM sshd over `container exec`. No TTY is requested — SSH is a binary
// protocol over a plain pipe.
//
// The runtime is injected so the resolution and exec argv are tested against the
// fake without a real VM. It returns the sshd exit code.
func RunSSHProxy(ctx context.Context, rt container.Runtime, name string,
	stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	c, err := rt.Inspect(ctx, name)
	if err != nil {
		return 1, fmt.Errorf("ssh-proxy: resolve run %q: %w", name, err)
	}
	if !c.Running {
		return 1, fmt.Errorf("ssh-proxy: run %q is not running", name)
	}
	id := c.ID
	if id == "" {
		id = name
	}
	return rt.ExecStream(ctx, id, container.StreamOptions{
		Cmd:    SSHDCommand(),
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		TTY:    false,
	})
}
