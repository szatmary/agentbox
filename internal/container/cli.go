package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Apple `container` subcommand names. They are isolated here so they can be
// adjusted for a given `container` version without touching call sites. See
// DECISIONS D10.
const (
	subBuild        = "build"
	subRun          = "run"
	subExec         = "exec"
	subStop         = "stop"
	subDelete       = "delete"
	subImageInspect = "images" // used as: container images inspect <ref>
)

// commandFunc launches a command, streaming stdout/stderr to the given writers,
// and reports the process exit code. A non-nil error means the command could
// not be launched (or another non-exit failure); a command that runs and exits
// non-zero returns that code with a nil error.
type commandFunc func(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (exitCode int, err error)

// CLIRuntime is the production [Runtime] backed by Apple's `container` CLI. It
// only functions on macOS with `container` installed and its service running;
// on other platforms the binary simply will not exist and calls fail with a
// clear error. Argument construction is unit-tested via an injected command
// runner, so no real `container` binary is invoked during `go test`.
type CLIRuntime struct {
	// Bin is the CLI binary name; defaults to "container".
	Bin string
	// run launches commands; defaults to execCommand. Injectable for tests.
	run commandFunc
}

var _ Runtime = (*CLIRuntime)(nil)

// NewCLIRuntime returns a CLIRuntime using the real `container` binary.
func NewCLIRuntime() *CLIRuntime { return &CLIRuntime{Bin: "container", run: execCommand} }

func (r *CLIRuntime) bin() string {
	if r.Bin != "" {
		return r.Bin
	}
	return "container"
}

func (r *CLIRuntime) runner() commandFunc {
	if r.run != nil {
		return r.run
	}
	return execCommand
}

// execCommand is the default commandFunc, shelling out via os/exec.
func execCommand(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, err
}

// capture runs args, capturing stdout/stderr into strings while also teeing to
// the optional extra writers.
func (r *CLIRuntime) capture(ctx context.Context, extraOut, extraErr io.Writer, args ...string) (ExecResult, error) {
	var outBuf, errBuf bytes.Buffer
	out := io.Writer(&outBuf)
	errw := io.Writer(&errBuf)
	if extraOut != nil {
		out = io.MultiWriter(&outBuf, extraOut)
	}
	if extraErr != nil {
		errw = io.MultiWriter(&errBuf, extraErr)
	}
	start := time.Now()
	code, err := r.runner()(ctx, out, errw, r.bin(), args...)
	return ExecResult{
		ExitCode: code,
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		Duration: time.Since(start),
	}, err
}

// requireSuccess runs args and returns an error unless the command exits 0.
func (r *CLIRuntime) requireSuccess(ctx context.Context, what string, args ...string) (ExecResult, error) {
	res, err := r.capture(ctx, nil, nil, args...)
	if err != nil {
		return res, fmt.Errorf("%s: %w", what, err)
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s: %s exited %d: %s", what, r.bin(), res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}

func (r *CLIRuntime) Build(ctx context.Context, opts BuildOptions) error {
	args := []string{subBuild}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if opts.Tag != "" {
		args = append(args, "-t", opts.Tag)
	}
	if opts.Dockerfile != "" {
		args = append(args, "-f", opts.Dockerfile)
	}
	for _, k := range sortedKeys(opts.BuildArgs) {
		args = append(args, "--build-arg", k+"="+opts.BuildArgs[k])
	}
	ctxDir := opts.ContextDir
	if ctxDir == "" {
		ctxDir = "."
	}
	args = append(args, ctxDir)
	_, err := r.requireSuccess(ctx, "container build", args...)
	return err
}

func (r *CLIRuntime) ImageExists(ctx context.Context, image string) (bool, error) {
	res, err := r.capture(ctx, nil, nil, subImageInspect, "inspect", image)
	if err != nil {
		return false, fmt.Errorf("container images inspect: %w", err)
	}
	return res.ExitCode == 0, nil
}

func (r *CLIRuntime) Run(ctx context.Context, opts RunOptions) (string, error) {
	args := []string{subRun, "-d"}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if opts.Workdir != "" {
		args = append(args, "-w", opts.Workdir)
	}
	for _, k := range sortedKeys(opts.Env) {
		args = append(args, "-e", k+"="+opts.Env[k])
	}
	for _, m := range opts.Mounts {
		spec := m.Source + ":" + m.Target
		if m.ReadOnly {
			spec += ":ro"
		}
		args = append(args, "-v", spec)
	}
	var cmdArgs []string
	if len(opts.Entrypoint) > 0 {
		args = append(args, "--entrypoint", opts.Entrypoint[0])
		cmdArgs = append(cmdArgs, opts.Entrypoint[1:]...)
	}
	cmdArgs = append(cmdArgs, opts.Cmd...)
	args = append(args, opts.Image)
	args = append(args, cmdArgs...)

	res, err := r.requireSuccess(ctx, "container run", args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(res.Stdout)
	if id == "" {
		return "", fmt.Errorf("container run: empty container id in output")
	}
	return id, nil
}

func (r *CLIRuntime) Exec(ctx context.Context, id string, opts ExecOptions) (ExecResult, error) {
	args := []string{subExec}
	if opts.Workdir != "" {
		args = append(args, "-w", opts.Workdir)
	}
	for _, k := range sortedKeys(opts.Env) {
		args = append(args, "-e", k+"="+opts.Env[k])
	}
	args = append(args, id)
	args = append(args, opts.Cmd...)

	var extraOut, extraErr io.Writer
	if opts.Stdout != nil {
		extraOut = opts.Stdout
	}
	if opts.Stderr != nil {
		extraErr = opts.Stderr
	}
	res, err := r.capture(ctx, extraOut, extraErr, args...)
	if err != nil {
		return res, fmt.Errorf("container exec: %w", err)
	}
	return res, nil
}

func (r *CLIRuntime) Stop(ctx context.Context, id string) error {
	_, err := r.requireSuccess(ctx, "container stop", subStop, id)
	return err
}

func (r *CLIRuntime) Remove(ctx context.Context, id string) error {
	_, err := r.requireSuccess(ctx, "container delete", subDelete, id)
	return err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
