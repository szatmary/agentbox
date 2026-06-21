package container

import (
	"bytes"
	"context"
	"encoding/json"
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
	subBuild  = "build"
	subRun    = "run"
	subExec   = "exec"
	subStop   = "stop"
	subDelete  = "delete"
	subImage   = "image"   // used as: container image inspect <ref>
	subInspect = "inspect" // used as: container inspect <id>

)

// commandFunc launches a command, streaming stdout/stderr to the given writers,
// and reports the process exit code. A non-nil error means the command could
// not be launched (or another non-exit failure); a command that runs and exits
// non-zero returns that code with a nil error.
type commandFunc func(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (exitCode int, err error)

// streamFunc launches a command with stdin wired in addition to stdout/stderr,
// for interactive/attached execs. Same exit-code/error contract as commandFunc.
type streamFunc func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) (exitCode int, err error)

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
	// stream launches stdio-attached commands; defaults to execStreamCommand.
	// Injectable for tests (so ExecStream argv is asserted without a real VM).
	stream streamFunc
}

var _ Runtime = (*CLIRuntime)(nil)

// NewCLIRuntime returns a CLIRuntime using the real `container` binary.
func NewCLIRuntime() *CLIRuntime {
	return &CLIRuntime{Bin: "container", run: execCommand, stream: execStreamCommand}
}

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

func (r *CLIRuntime) streamer() streamFunc {
	if r.stream != nil {
		return r.stream
	}
	return execStreamCommand
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

// execStreamCommand is the default streamFunc, wiring stdin/stdout/stderr of an
// os/exec process straight through. nil streams are left unset.
func execStreamCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
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
	// `container image inspect <ref>` exits 0 when the image is present and 1
	// when it is absent. Any other non-zero exit (e.g. the container service is
	// down, or a malformed reference) is a real failure we must surface rather
	// than silently report as "absent".
	res, err := r.capture(ctx, nil, nil, subImage, "inspect", image)
	if err != nil {
		return false, fmt.Errorf("container image inspect: %w", err)
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("container image inspect: %s exited %d: %s",
			r.bin(), res.ExitCode, strings.TrimSpace(res.Stderr))
	}
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

// ExecStream runs an interactive/attached command. It adds `-i` (always, so
// stdin is wired) and `-t` (when a TTY is requested) to `container exec`, then
// pipes the caller's streams straight through. Used by `shell` and the
// SSH-over-exec ProxyCommand.
func (r *CLIRuntime) ExecStream(ctx context.Context, id string, opts StreamOptions) (int, error) {
	args := []string{subExec, "-i"}
	if opts.TTY {
		args = append(args, "-t")
	}
	if opts.Workdir != "" {
		args = append(args, "-w", opts.Workdir)
	}
	for _, k := range sortedKeys(opts.Env) {
		args = append(args, "-e", k+"="+opts.Env[k])
	}
	args = append(args, id)
	args = append(args, opts.Cmd...)

	var stdin io.Reader
	if opts.Stdin != nil {
		stdin = opts.Stdin
	}
	var stdout, stderr io.Writer
	if opts.Stdout != nil {
		stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		stderr = opts.Stderr
	}
	code, err := r.streamer()(ctx, stdin, stdout, stderr, r.bin(), args...)
	if err != nil {
		return code, fmt.Errorf("container exec (stream): %w", err)
	}
	return code, nil
}

// containerInspect mirrors the subset of `container inspect` JSON we rely on.
// The exact schema is version-sensitive (like the subcommand names, D10): it is
// isolated here so a `container` release change is a one-place fix.
type containerInspect struct {
	Status        string `json:"status"`
	Configuration struct {
		ID    string `json:"id"`
		Image struct {
			Reference string `json:"reference"`
		} `json:"image"`
	} `json:"configuration"`
}

// Inspect shells `container inspect <id>` and parses the JSON. A non-zero exit
// (absent container, service down, bad reference) is surfaced as an error.
func (r *CLIRuntime) Inspect(ctx context.Context, id string) (Container, error) {
	res, err := r.capture(ctx, nil, nil, subInspect, id)
	if err != nil {
		return Container{}, fmt.Errorf("container inspect: %w", err)
	}
	if res.ExitCode != 0 {
		return Container{}, fmt.Errorf("container inspect %q: exited %d: %s",
			id, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var docs []containerInspect
	if err := json.Unmarshal([]byte(res.Stdout), &docs); err != nil {
		return Container{}, fmt.Errorf("container inspect %q: parsing output: %w", id, err)
	}
	if len(docs) == 0 {
		return Container{}, fmt.Errorf("container inspect %q: no container in output", id)
	}
	d := docs[0]
	name := d.Configuration.ID
	if name == "" {
		name = id
	}
	return Container{
		ID:      name,
		Name:    name,
		Image:   d.Configuration.Image.Reference,
		Running: strings.EqualFold(d.Status, "running"),
	}, nil
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
