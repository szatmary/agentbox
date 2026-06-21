package container

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type recorder struct {
	calls   [][]string // each: name + args
	stdout  string
	exit    int
	err     error
	gotName string
}

func (rec *recorder) fn() commandFunc {
	return func(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (int, error) {
		rec.gotName = name
		rec.calls = append(rec.calls, append([]string{name}, args...))
		if rec.stdout != "" {
			_, _ = stdout.Write([]byte(rec.stdout))
		}
		return rec.exit, rec.err
	}
}

func lastArgs(rec *recorder) []string {
	if len(rec.calls) == 0 {
		return nil
	}
	return rec.calls[len(rec.calls)-1]
}

func hasSeq(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		if equal(args[i:i+len(seq)], seq) {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCLIBuildArgs(t *testing.T) {
	rec := &recorder{}
	r := &CLIRuntime{Bin: "container", run: rec.fn()}
	err := r.Build(context.Background(), BuildOptions{
		Tag:        "agentbox:latest",
		ContextDir: "/ctx",
		Dockerfile: "/ctx/Dockerfile",
		BuildArgs:  map[string]string{"UID": "501", "GID": "20"},
		NoCache:    true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	a := lastArgs(rec)
	if a[0] != "container" || a[1] != "build" {
		t.Fatalf("args = %v", a)
	}
	if !hasSeq(a, "--no-cache") || !hasSeq(a, "-t", "agentbox:latest") || !hasSeq(a, "-f", "/ctx/Dockerfile") {
		t.Errorf("missing core build args: %v", a)
	}
	// Build args are emitted sorted (GID before UID).
	if !hasSeq(a, "--build-arg", "GID=20") || !hasSeq(a, "--build-arg", "UID=501") {
		t.Errorf("missing build-args: %v", a)
	}
	if a[len(a)-1] != "/ctx" {
		t.Errorf("context dir should be last: %v", a)
	}
}

func TestCLIBuildPropagatesFailure(t *testing.T) {
	rec := &recorder{exit: 2, err: nil}
	r := &CLIRuntime{run: rec.fn()}
	if err := r.Build(context.Background(), BuildOptions{Tag: "x"}); err == nil {
		t.Fatal("expected error on non-zero build exit")
	}
}

func TestCLIImageExists(t *testing.T) {
	r0 := &CLIRuntime{run: (&recorder{exit: 0}).fn()}
	ok, err := r0.ImageExists(context.Background(), "img")
	if err != nil || !ok {
		t.Fatalf("exit 0 => exists: got %v,%v", ok, err)
	}
	r1 := &CLIRuntime{run: (&recorder{exit: 1}).fn()}
	ok, err = r1.ImageExists(context.Background(), "img")
	if err != nil || ok {
		t.Fatalf("exit 1 => absent: got %v,%v", ok, err)
	}
}

// TestCLIImageExistsArgvAndErrors pins the real `container image inspect`
// subcommand (the old code shelled the nonexistent `container images inspect`,
// which always failed → always "absent") and that a non-"absent" failure is
// surfaced as an error rather than reported as a missing image.
func TestCLIImageExistsArgvAndErrors(t *testing.T) {
	rec := &recorder{exit: 0}
	r := &CLIRuntime{Bin: "container", run: rec.fn()}
	ok, err := r.ImageExists(context.Background(), "agentbox:latest")
	if err != nil || !ok {
		t.Fatalf("present image: got %v,%v", ok, err)
	}
	a := lastArgs(rec)
	if !hasSeq(a, "container", "image", "inspect", "agentbox:latest") {
		t.Errorf("argv must be `container image inspect <ref>`, got %v", a)
	}
	if hasSeq(a, "images") {
		t.Errorf("must not use the fabricated `images` subcommand: %v", a)
	}

	// exit 1 => absent, no error.
	r1 := &CLIRuntime{run: (&recorder{exit: 1}).fn()}
	if ok, err := r1.ImageExists(context.Background(), "x"); err != nil || ok {
		t.Fatalf("exit 1 => absent: got %v,%v", ok, err)
	}

	// Any other non-zero exit must be surfaced as an error (not "absent").
	r2 := &CLIRuntime{run: (&recorder{exit: 125}).fn()}
	if ok, err := r2.ImageExists(context.Background(), "x"); err == nil || ok {
		t.Fatalf("exit 125 must surface an error, got ok=%v err=%v", ok, err)
	}
}

func TestCLIRunArgsAndID(t *testing.T) {
	rec := &recorder{stdout: "  abc123\n", exit: 0}
	r := &CLIRuntime{run: rec.fn()}
	id, err := r.Run(context.Background(), RunOptions{
		Image:      "agentbox:latest",
		Name:       "go2110",
		Workdir:    "/work/workspace",
		Env:        map[string]string{"B": "2", "A": "1"},
		Mounts:     []Mount{{Source: "/h/control", Target: "/work/control"}, {Source: "/h/ro", Target: "/ro", ReadOnly: true}},
		Entrypoint: []string{"sleep"},
		Cmd:        []string{"infinity"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if id != "abc123" {
		t.Fatalf("id = %q, want abc123", id)
	}
	a := lastArgs(rec)
	if !hasSeq(a, "run", "-d") || !hasSeq(a, "--name", "go2110") || !hasSeq(a, "-w", "/work/workspace") {
		t.Errorf("missing run flags: %v", a)
	}
	if !hasSeq(a, "-e", "A=1") || !hasSeq(a, "-e", "B=2") {
		t.Errorf("env not sorted/present: %v", a)
	}
	if !hasSeq(a, "-v", "/h/control:/work/control") || !hasSeq(a, "-v", "/h/ro:/ro:ro") {
		t.Errorf("mounts wrong: %v", a)
	}
	if !hasSeq(a, "--entrypoint", "sleep") {
		t.Errorf("entrypoint wrong: %v", a)
	}
	// image precedes the command args.
	if !hasSeq(a, "agentbox:latest", "infinity") {
		t.Errorf("image/cmd order wrong: %v", a)
	}
}

func TestCLIRunEmptyIDError(t *testing.T) {
	rec := &recorder{stdout: "\n", exit: 0}
	r := &CLIRuntime{run: rec.fn()}
	if _, err := r.Run(context.Background(), RunOptions{Image: "x"}); err == nil {
		t.Fatal("expected error for empty container id")
	}
}

func TestCLIExecCapturesAndStreams(t *testing.T) {
	rec := &recorder{stdout: "hello", exit: 5}
	r := &CLIRuntime{run: rec.fn()}
	var streamed bytes.Buffer
	res, err := r.Exec(context.Background(), "cid", ExecOptions{
		Cmd:     []string{"cat", "/work/control/STATUS"},
		Workdir: "/w",
		Env:     map[string]string{"K": "v"},
		Stdout:  &streamed,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 5 || res.Stdout != "hello" {
		t.Fatalf("res = %+v", res)
	}
	if streamed.String() != "hello" {
		t.Errorf("streamed = %q, want hello", streamed.String())
	}
	a := lastArgs(rec)
	if !hasSeq(a, "exec", "-w", "/w", "-e", "K=v", "cid", "cat", "/work/control/STATUS") {
		t.Errorf("exec args wrong: %v", a)
	}
}

func TestCLIExecLaunchError(t *testing.T) {
	rec := &recorder{err: errors.New("not found")}
	r := &CLIRuntime{run: rec.fn()}
	if _, err := r.Exec(context.Background(), "cid", ExecOptions{Cmd: []string{"x"}}); err == nil {
		t.Fatal("expected error when command cannot launch")
	}
}

func TestCLIStopRemove(t *testing.T) {
	rec := &recorder{exit: 0}
	r := &CLIRuntime{run: rec.fn()}
	if err := r.Stop(context.Background(), "cid"); err != nil {
		t.Fatal(err)
	}
	if !hasSeq(lastArgs(rec), "stop", "cid") {
		t.Errorf("stop args: %v", lastArgs(rec))
	}
	if err := r.Remove(context.Background(), "cid"); err != nil {
		t.Fatal(err)
	}
	if !hasSeq(lastArgs(rec), "delete", "cid") {
		t.Errorf("delete args: %v", lastArgs(rec))
	}

	recFail := &recorder{exit: 1}
	rf := &CLIRuntime{run: recFail.fn()}
	if err := rf.Stop(context.Background(), "cid"); err == nil {
		t.Error("expected error on non-zero stop")
	}
}

// Exercise the default execCommand runner without needing the `container`
// binary, using /bin/sh.
func TestExecCommandReal(t *testing.T) {
	var out bytes.Buffer
	code, err := execCommand(context.Background(), &out, &out, "sh", "-c", "printf hi; exit 3")
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Errorf("output = %q", out.String())
	}

	if _, err := execCommand(context.Background(), io.Discard, io.Discard, "definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("expected launch error for missing binary")
	}
}

func TestCLIInspect(t *testing.T) {
	const js = `[{"status":"running","configuration":{"id":"job-123","image":{"reference":"agentbox:latest"}}}]`
	rec := &recorder{stdout: js, exit: 0}
	r := &CLIRuntime{Bin: "container", run: rec.fn()}
	c, err := r.Inspect(context.Background(), "job-123")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !c.Running || c.Name != "job-123" || c.Image != "agentbox:latest" {
		t.Errorf("inspect = %+v", c)
	}
	if !hasSeq(lastArgs(rec), "container", "inspect", "job-123") {
		t.Errorf("argv must be `container inspect <id>`, got %v", lastArgs(rec))
	}

	// Stopped container: parsed, Running=false, no error.
	rec2 := &recorder{stdout: `[{"status":"stopped","configuration":{"id":"x"}}]`, exit: 0}
	r2 := &CLIRuntime{run: rec2.fn()}
	if c, err := r2.Inspect(context.Background(), "x"); err != nil || c.Running {
		t.Errorf("stopped: c=%+v err=%v", c, err)
	}

	// Non-zero exit (absent/service down) surfaces an error.
	r3 := &CLIRuntime{run: (&recorder{exit: 1}).fn()}
	if _, err := r3.Inspect(context.Background(), "gone"); err == nil {
		t.Error("expected error for absent container")
	}

	// Malformed JSON surfaces an error.
	r4 := &CLIRuntime{run: (&recorder{stdout: "not json", exit: 0}).fn()}
	if _, err := r4.Inspect(context.Background(), "x"); err == nil {
		t.Error("expected error for malformed inspect output")
	}
}

// streamRecorder captures ExecStream argv (and confirms stdio is wired).
type streamRecorder struct {
	args     []string
	exit     int
	err      error
	sawStdin bool
}

func (s *streamRecorder) fn() streamFunc {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) (int, error) {
		s.args = append([]string{name}, args...)
		s.sawStdin = stdin != nil
		if stdout != nil && s.exit == 0 {
			_, _ = stdout.Write([]byte("streamed"))
		}
		return s.exit, s.err
	}
}

func TestCLIExecStream(t *testing.T) {
	// Non-TTY (SSH tunnel): -i present, -t absent, stdin wired.
	rec := &streamRecorder{exit: 7}
	r := &CLIRuntime{Bin: "container", stream: rec.fn()}
	var out bytes.Buffer
	code, err := r.ExecStream(context.Background(), "vm1", StreamOptions{
		Cmd:    []string{"/usr/sbin/sshd", "-i"},
		Stdin:  strings.NewReader("data"),
		Stdout: &out,
	})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if code != 7 {
		t.Errorf("code = %d, want 7", code)
	}
	if !hasSeq(rec.args, "exec", "-i", "vm1", "/usr/sbin/sshd", "-i") {
		t.Errorf("argv = %v", rec.args)
	}
	if hasSeq(rec.args, "-t") {
		t.Errorf("non-TTY exec must not pass -t: %v", rec.args)
	}
	if !rec.sawStdin {
		t.Error("stdin not wired through")
	}

	// TTY (interactive shell): -t present.
	recT := &streamRecorder{}
	rt := &CLIRuntime{stream: recT.fn()}
	if _, err := rt.ExecStream(context.Background(), "vm2", StreamOptions{Cmd: []string{"bash"}, TTY: true}); err != nil {
		t.Fatal(err)
	}
	if !hasSeq(recT.args, "exec", "-i", "-t", "vm2", "bash") {
		t.Errorf("tty argv = %v", recT.args)
	}
}

func TestExecStreamCommandReal(t *testing.T) {
	var out bytes.Buffer
	code, err := execStreamCommand(context.Background(), strings.NewReader("hello"), &out, &out, "sh", "-c", "cat; exit 4")
	if err != nil {
		t.Fatalf("execStreamCommand: %v", err)
	}
	if code != 4 || !strings.Contains(out.String(), "hello") {
		t.Errorf("code=%d out=%q", code, out.String())
	}
}

func TestNewCLIRuntimeDefaults(t *testing.T) {
	r := NewCLIRuntime()
	if r.bin() != "container" || r.runner() == nil {
		t.Fatalf("defaults wrong: bin=%q", r.bin())
	}
	empty := &CLIRuntime{}
	if empty.bin() != "container" || empty.runner() == nil {
		t.Fatal("zero-value defaults wrong")
	}
}
