package container

import (
	"context"
	"fmt"
	"sync"
)

// Call records a single invocation of a Fake method, for assertions in tests.
type Call struct {
	Method  string // "Build", "ImageExists", "Run", "Exec", "Stop", "Remove"
	ID      string // container id, for Exec/Stop/Remove
	Image   string // for Run/ImageExists
	Tag     string // for Build
	Cmd     []string
	Env     map[string]string
	Mounts  []Mount // for Run
	Workdir string  // for Run/Exec
}

// Fake is an in-memory [Runtime] for tests. It records every call and lets a
// test override any method via the *Func hooks. The default behavior is
// permissive (success, auto-generated ids) so tests only override what matters.
//
// The zero value is ready to use. Fake is safe for concurrent use.
type Fake struct {
	mu    sync.Mutex
	calls []Call

	images  map[string]bool
	running map[string]bool
	stopped []string
	removed []string
	nextID  int

	// Hooks. When set, the hook fully replaces the default behavior for that
	// method (the call is still recorded first).
	BuildFunc       func(ctx context.Context, opts BuildOptions) error
	ImageExistsFunc func(ctx context.Context, image string) (bool, error)
	RunFunc         func(ctx context.Context, opts RunOptions) (string, error)
	ExecFunc        func(ctx context.Context, id string, opts ExecOptions) (ExecResult, error)
	StopFunc        func(ctx context.Context, id string) error
	RemoveFunc      func(ctx context.Context, id string) error
}

var _ Runtime = (*Fake)(nil)

func (f *Fake) record(c Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

// Calls returns a copy of the recorded calls in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallsOf returns the recorded calls for a single method, in order.
func (f *Fake) CallsOf(method string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// SetImage marks an image present (or absent) for ImageExists.
func (f *Fake) SetImage(image string, present bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.images == nil {
		f.images = map[string]bool{}
	}
	f.images[image] = present
}

// Running reports whether the given container id is currently marked running.
func (f *Fake) Running(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[id]
}

// Stopped returns the ids passed to Stop, in order.
func (f *Fake) Stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

// Removed returns the ids passed to Remove, in order.
func (f *Fake) Removed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

func (f *Fake) Build(ctx context.Context, opts BuildOptions) error {
	f.record(Call{Method: "Build", Tag: opts.Tag})
	if f.BuildFunc != nil {
		return f.BuildFunc(ctx, opts)
	}
	f.SetImage(opts.Tag, true)
	return nil
}

func (f *Fake) ImageExists(ctx context.Context, image string) (bool, error) {
	f.record(Call{Method: "ImageExists", Image: image})
	if f.ImageExistsFunc != nil {
		return f.ImageExistsFunc(ctx, image)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[image], nil
}

func (f *Fake) Run(ctx context.Context, opts RunOptions) (string, error) {
	f.record(Call{Method: "Run", Image: opts.Image, Cmd: opts.Cmd, Env: opts.Env, Mounts: opts.Mounts, Workdir: opts.Workdir})
	if f.RunFunc != nil {
		id, err := f.RunFunc(ctx, opts)
		if err == nil && id != "" {
			f.mu.Lock()
			if f.running == nil {
				f.running = map[string]bool{}
			}
			f.running[id] = true
			f.mu.Unlock()
		}
		return id, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("fake-%d", f.nextID)
	if f.running == nil {
		f.running = map[string]bool{}
	}
	f.running[id] = true
	return id, nil
}

func (f *Fake) Exec(ctx context.Context, id string, opts ExecOptions) (ExecResult, error) {
	f.record(Call{Method: "Exec", ID: id, Cmd: opts.Cmd, Env: opts.Env, Workdir: opts.Workdir})
	if f.ExecFunc != nil {
		return f.ExecFunc(ctx, id, opts)
	}
	return ExecResult{ExitCode: 0}, nil
}

func (f *Fake) Stop(ctx context.Context, id string) error {
	f.record(Call{Method: "Stop", ID: id})
	if f.StopFunc != nil {
		return f.StopFunc(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, id)
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *Fake) Remove(ctx context.Context, id string) error {
	f.record(Call{Method: "Remove", ID: id})
	if f.RemoveFunc != nil {
		return f.RemoveFunc(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, id)
	f.removed = append(f.removed, id)
	return nil
}
