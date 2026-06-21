// Package container abstracts the sandbox runtime that agentbox drives.
//
// The [Runtime] interface is the single seam between agentbox's pure logic
// (supervisor, autorun, CLI) and the outside world. The production
// implementation ([CLIRuntime]) shells out to Apple's `container` CLI on macOS;
// tests use [Fake]. The interface is deliberately backend-agnostic so a Docker
// or Podman backend can be added later without touching callers.
package container

import (
	"context"
	"time"
)

// BuildOptions describes an image build.
type BuildOptions struct {
	// Tag is the image name/tag to produce, e.g. "agentbox:latest".
	Tag string
	// ContextDir is the build context directory on the host.
	ContextDir string
	// Dockerfile is the path to the Dockerfile (may be outside ContextDir).
	Dockerfile string
	// BuildArgs are --build-arg key/value pairs (e.g. host UID/GID).
	BuildArgs map[string]string
	// NoCache forces a rebuild without layer cache.
	NoCache bool
}

// Mount is a host directory bound into the sandbox.
type Mount struct {
	Source   string // host path
	Target   string // path inside the sandbox
	ReadOnly bool
}

// RunOptions describes a long-lived sandbox to start.
type RunOptions struct {
	// Image is the image to run.
	Image string
	// Name is an optional stable container name.
	Name string
	// Env are environment variables injected into the sandbox. Values may be
	// secret and must never be logged by callers.
	Env map[string]string
	// Mounts are host directories bound into the sandbox.
	Mounts []Mount
	// Workdir is the working directory inside the sandbox.
	Workdir string
	// Entrypoint overrides the image entrypoint. agentbox keeps the VM alive
	// with a long sleep so it can be driven via Exec.
	Entrypoint []string
	// Cmd is the command (and args) passed to the entrypoint.
	Cmd []string
}

// ExecOptions describes a single command executed inside a running sandbox.
type ExecOptions struct {
	// Cmd is the command and its arguments.
	Cmd []string
	// Env are extra environment variables for this exec only.
	Env map[string]string
	// Workdir overrides the working directory for this exec.
	Workdir string
	// Stdout/Stderr, when non-nil, receive streamed output in addition to the
	// captured buffers in ExecResult. Useful for live logging.
	Stdout, Stderr interface{ Write([]byte) (int, error) }
}

// ExecResult is the outcome of an Exec call.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	// Duration is how long the exec took.
	Duration time.Duration
}

// StreamOptions describes an interactive command whose stdio is attached
// directly to the caller's streams (no buffering), used for an interactive
// shell and the SSH-over-exec stdio tunnel. Unlike [ExecOptions] there is no
// captured result body — only the process exit code is returned.
type StreamOptions struct {
	// Cmd is the command and its arguments to run inside the sandbox.
	Cmd []string
	// Env are extra environment variables for this exec only.
	Env map[string]string
	// Workdir overrides the working directory for this exec.
	Workdir string
	// Stdin/Stdout/Stderr are wired straight to the in-VM process. Any may be
	// nil. For the SSH tunnel these carry the raw SSH wire protocol.
	Stdin          interface{ Read([]byte) (int, error) }
	Stdout, Stderr interface{ Write([]byte) (int, error) }
	// TTY requests a pseudo-terminal (interactive shell). It must be false for
	// the SSH tunnel, which speaks a binary protocol over a plain pipe.
	TTY bool
}

// Container describes a sandbox known to the runtime, as reported by [Runtime.Inspect].
type Container struct {
	// ID is the runtime's container id.
	ID string
	// Name is the stable name the sandbox was started with (agentbox sets this
	// to the run-dir base name, so a run resolves to its container by name).
	Name string
	// Image is the image reference the sandbox was started from.
	Image string
	// Running reports whether the sandbox is currently running.
	Running bool
}

// Runtime is the sandbox backend agentbox drives. Implementations must be safe
// for sequential use by a single supervisor; concurrent use is not required.
type Runtime interface {
	// Build builds (or rebuilds) an image.
	Build(ctx context.Context, opts BuildOptions) error
	// ImageExists reports whether an image is present locally.
	ImageExists(ctx context.Context, image string) (bool, error)
	// Run starts a long-lived sandbox and returns its container ID.
	Run(ctx context.Context, opts RunOptions) (id string, err error)
	// Exec runs a command inside a running sandbox and returns its result.
	// A non-nil error means the command could not be launched; a command that
	// runs but exits non-zero returns a nil error and a non-zero ExitCode.
	Exec(ctx context.Context, id string, opts ExecOptions) (ExecResult, error)
	// ExecStream runs a command with stdio attached directly to the provided
	// streams (no buffering), for interactive shells and the SSH-over-exec
	// tunnel. It returns the command's exit code; a non-nil error means the
	// command could not be launched or stdio could not be wired.
	ExecStream(ctx context.Context, id string, opts StreamOptions) (exitCode int, err error)
	// Inspect returns details about a sandbox by id or name. It returns an error
	// if the sandbox does not exist or cannot be inspected; a stopped sandbox
	// that still exists is returned with Running=false.
	Inspect(ctx context.Context, id string) (Container, error)
	// Stop stops a running sandbox.
	Stop(ctx context.Context, id string) error
	// Remove removes a (stopped) sandbox.
	Remove(ctx context.Context, id string) error
}
