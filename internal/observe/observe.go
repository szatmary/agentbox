// Package observe is a read/exec layer over a single live agentbox run.
//
// It is the foundation of the "attach + observe" feature: an [Observer] resolves
// a run to its live sandbox container and reads or runs commands inside it via
// the existing [container.Runtime.Exec] transport, plus reads the host-side run
// directory (the STATUS sentinel and logs/run.log) directly. The same layer
// backs `agentbox status` detail, the MCP tools, and ad-hoc inspection.
//
// A run's container is started with the run-dir base name as its `--name`
// (see cmd/agentbox), so resolution is deterministic: the container name *is*
// the run-dir base; Observe only confirms the container is actually running.
//
// Every method that touches the VM resolves run→live-container first and returns
// [ErrNotRunning] if the sandbox is not up. The package is pure logic over the
// Runtime interface and the host filesystem, so it is table-tested against the
// fake Runtime with no real container or VM.
package observe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// DefaultWorkdir is the in-VM working tree the agent operates in (mirrors the
// supervisor's default). ls/cat/git default to this directory.
const DefaultWorkdir = "/work/workspace"

// ErrNotRunning means the run's sandbox container is not currently running, so
// no in-VM operation can be performed.
var ErrNotRunning = errors.New("run is not running")

// Observer reads and execs inside one live run.
type Observer struct {
	rt      container.Runtime
	runDir  string // absolute host path of the run directory
	name    string // container name == run-dir base name
	workdir string // in-VM working directory for ls/cat/git
}

// New returns an Observer for the run rooted at runDir (a host path produced by
// run.Create). The sandbox container name is taken to be the run-dir base name.
func New(rt container.Runtime, runDir string) (*Observer, error) {
	if rt == nil {
		return nil, fmt.Errorf("observe: nil runtime")
	}
	abs, err := filepath.Abs(runDir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, run.ControlDir)); err != nil {
		return nil, fmt.Errorf("observe: %s is not a run directory: %w", abs, err)
	}
	return &Observer{rt: rt, runDir: abs, name: filepath.Base(abs), workdir: DefaultWorkdir}, nil
}

// Name returns the run/container name.
func (o *Observer) Name() string { return o.name }

// resolve confirms the run's container is running and returns the exec target id.
func (o *Observer) resolve(ctx context.Context) (string, error) {
	c, err := o.rt.Inspect(ctx, o.name)
	if err != nil {
		return "", fmt.Errorf("resolve run %q: %w", o.name, err)
	}
	if !c.Running {
		return "", fmt.Errorf("%w: %s", ErrNotRunning, o.name)
	}
	id := c.ID
	if id == "" {
		id = o.name
	}
	return id, nil
}

// Status reports the run's liveness and its STATUS sentinel (read host-side).
type Status struct {
	Name string
	// Running is whether the sandbox container is currently up.
	Running bool
	// StatusText is the raw STATUS control file content, if present.
	StatusText string
	// HasStatus is whether a STATUS file exists yet.
	HasStatus bool
	// Sentinel is the parsed STATUS first line (Done/Failed/Reason).
	Sentinel run.Sentinel
}

// Status reports the run's status: container liveness (via Inspect) plus the
// host STATUS sentinel. Reading STATUS host-side (not via exec) means status
// still works after the VM has been torn down.
func (o *Observer) Status(ctx context.Context) (Status, error) {
	st := Status{Name: o.name}
	if c, err := o.rt.Inspect(ctx, o.name); err == nil {
		st.Running = c.Running
	}
	b, err := os.ReadFile(filepath.Join(o.runDir, run.ControlDir, run.StatusFile))
	switch {
	case err == nil:
		st.HasStatus = true
		st.StatusText = string(b)
		st.Sentinel = run.ParseStatus(string(b))
	case os.IsNotExist(err):
		// no STATUS yet (in progress or interrupted)
	default:
		return st, fmt.Errorf("reading STATUS: %w", err)
	}
	return st, nil
}

// TailLog returns the last n lines of the run's host log (logs/run.log). n<=0
// returns the whole file. It reads the host file directly (no VM needed).
func (o *Observer) TailLog(ctx context.Context, n int) (string, error) {
	b, err := os.ReadFile(filepath.Join(o.runDir, run.LogsDir, run.LogFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading log: %w", err)
	}
	return lastLines(string(b), n), nil
}

// lastLines returns the last n lines of s (the whole string if n<=0).
func lastLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n") + "\n"
}

// ListFiles lists a directory inside the VM (`ls -la`). An empty path lists the
// working tree. The `--` separator stops a crafted path from being read as a flag.
func (o *Observer) ListFiles(ctx context.Context, path string) (string, error) {
	if path == "" {
		path = o.workdir
	}
	res, err := o.execIn(ctx, "ls", "-la", "--", path)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("list %q: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

// ReadFile returns the contents of a file inside the VM (`cat`). The `--`
// separator stops a crafted path from being read as a flag.
func (o *Observer) ReadFile(ctx context.Context, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("read: empty path")
	}
	res, err := o.execIn(ctx, "cat", "--", path)
	if err != nil {
		return "", err
	}
	switch res.ExitCode {
	case 0:
		return res.Stdout, nil
	case 1:
		return "", fmt.Errorf("read %q: no such file", path)
	default:
		return "", fmt.Errorf("read %q: exit %d: %s", path, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
}

// GitStatus runs `git status --porcelain=v1 -b` in the working tree.
func (o *Observer) GitStatus(ctx context.Context) (string, error) {
	res, err := o.execIn(ctx, "git", "-C", o.workdir, "status", "--porcelain=v1", "-b")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git status: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

// GitDiff runs `git diff` in the working tree (optionally staged with --cached).
func (o *Observer) GitDiff(ctx context.Context, staged bool) (string, error) {
	args := []string{"-C", o.workdir, "diff"}
	if staged {
		args = append(args, "--cached")
	}
	res, err := o.execIn(ctx, "git", args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git diff: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

// Exec runs an arbitrary command inside the VM and returns its result. It is the
// general-purpose escape hatch (and the MCP `exec` tool). The command runs in
// the run's own sandbox VM — see the README trust model.
func (o *Observer) Exec(ctx context.Context, cmd ...string) (container.ExecResult, error) {
	if len(cmd) == 0 {
		return container.ExecResult{}, fmt.Errorf("exec: empty command")
	}
	return o.execIn(ctx, cmd[0], cmd[1:]...)
}

// execIn resolves the live container and runs name+args inside it (buffered).
func (o *Observer) execIn(ctx context.Context, name string, args ...string) (container.ExecResult, error) {
	id, err := o.resolve(ctx)
	if err != nil {
		return container.ExecResult{}, err
	}
	cmd := append([]string{name}, args...)
	res, err := o.rt.Exec(ctx, id, container.ExecOptions{Cmd: cmd, Workdir: o.workdir})
	if err != nil {
		return container.ExecResult{}, fmt.Errorf("exec in %s: %w", o.name, err)
	}
	return res, nil
}
