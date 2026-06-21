package container

import (
	"context"
	"errors"
	"testing"
)

func TestFakeRunExecLifecycle(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()

	id, err := f.Run(ctx, RunOptions{Image: "img:1", Cmd: []string{"sleep", "infinity"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if id == "" {
		t.Fatal("Run returned empty id")
	}
	if !f.Running(id) {
		t.Fatalf("container %s should be running", id)
	}

	res, err := f.Exec(ctx, id, ExecOptions{Cmd: []string{"echo", "hi"}})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("default exit code = %d, want 0", res.ExitCode)
	}

	if err := f.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if f.Running(id) {
		t.Fatal("container should not be running after Stop")
	}
	if err := f.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if got := f.Stopped(); len(got) != 1 || got[0] != id {
		t.Fatalf("Stopped() = %v, want [%s]", got, id)
	}
	if got := f.Removed(); len(got) != 1 || got[0] != id {
		t.Fatalf("Removed() = %v, want [%s]", got, id)
	}

	// Call recording in order.
	var methods []string
	for _, c := range f.Calls() {
		methods = append(methods, c.Method)
	}
	want := []string{"Run", "Exec", "Stop", "Remove"}
	if len(methods) != len(want) {
		t.Fatalf("calls = %v, want %v", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Fatalf("calls = %v, want %v", methods, want)
		}
	}
}

func TestFakeImageExistsAndBuild(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()

	ok, err := f.ImageExists(ctx, "missing")
	if err != nil || ok {
		t.Fatalf("ImageExists(missing) = %v,%v want false,nil", ok, err)
	}
	if err := f.Build(ctx, BuildOptions{Tag: "built:1"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ok, err = f.ImageExists(ctx, "built:1")
	if err != nil || !ok {
		t.Fatalf("ImageExists(built:1) = %v,%v want true,nil", ok, err)
	}
}

func TestFakeHooksOverride(t *testing.T) {
	sentinel := errors.New("boom")
	f := &Fake{
		ExecFunc: func(ctx context.Context, id string, opts ExecOptions) (ExecResult, error) {
			return ExecResult{ExitCode: 7, Stdout: "out"}, nil
		},
		RunFunc: func(ctx context.Context, opts RunOptions) (string, error) {
			return "", sentinel
		},
	}
	ctx := context.Background()

	if _, err := f.Run(ctx, RunOptions{}); !errors.Is(err, sentinel) {
		t.Fatalf("RunFunc error = %v, want %v", err, sentinel)
	}
	res, _ := f.Exec(ctx, "x", ExecOptions{Cmd: []string{"foo"}})
	if res.ExitCode != 7 || res.Stdout != "out" {
		t.Fatalf("ExecFunc result = %+v", res)
	}
	if got := f.CallsOf("Exec"); len(got) != 1 || got[0].Cmd[0] != "foo" {
		t.Fatalf("CallsOf(Exec) = %+v", got)
	}
}
