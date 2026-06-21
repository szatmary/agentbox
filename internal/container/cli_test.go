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
