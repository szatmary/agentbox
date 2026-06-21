package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateLayout(t *testing.T) {
	base := t.TempDir()
	r, err := Create(base, "go2110", "20260621120000")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	for _, sub := range []string{ControlDir, OutputDir, LogsDir, WorkspaceDir} {
		if fi, err := os.Stat(filepath.Join(r.Root, sub)); err != nil || !fi.IsDir() {
			t.Errorf("missing subdir %s: %v", sub, err)
		}
	}
	if filepath.Base(r.Root) != "go2110-20260621120000" {
		t.Errorf("root base = %s", filepath.Base(r.Root))
	}
	if r.Logger() == nil {
		t.Error("Logger() is nil after Create")
	}
}

func TestCreateRequiresNameAndID(t *testing.T) {
	if _, err := Create(t.TempDir(), "", "id"); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := Create(t.TempDir(), "n", ""); err == nil {
		t.Error("expected error for empty id")
	}
}

func TestCreateRejectsExisting(t *testing.T) {
	base := t.TempDir()
	if _, err := Create(base, "n", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(base, "n", "1"); err == nil {
		t.Error("expected error creating existing run dir")
	}
}

func TestLoggerWritesToFile(t *testing.T) {
	r, err := Create(t.TempDir(), "n", "1")
	if err != nil {
		t.Fatal(err)
	}
	r.Logger().Info("hello", "key", "value")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.Path(LogsDir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || !filepath.IsAbs(r.Path(LogsDir, LogFile)) {
		t.Errorf("log not written: %d bytes", len(b))
	}
}

func TestStatusAndStop(t *testing.T) {
	r, err := Create(t.TempDir(), "n", "1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	_, exists, err := r.ReadStatus()
	if err != nil || exists {
		t.Fatalf("ReadStatus before write = exists %v err %v", exists, err)
	}
	if err := os.WriteFile(r.Control(StatusFile), []byte("DONE\nall good\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, exists, err := r.ReadStatus()
	if err != nil || !exists {
		t.Fatalf("ReadStatus after write = exists %v err %v", exists, err)
	}
	if got := ParseStatus(content); !got.Done {
		t.Errorf("ParseStatus = %+v, want Done", got)
	}

	if err := r.WriteStop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.Control(StopFile)); err != nil {
		t.Errorf("STOP not written: %v", err)
	}
}

func TestOpen(t *testing.T) {
	base := t.TempDir()
	created, err := Create(base, "job", "20260101000000")
	if err != nil {
		t.Fatal(err)
	}
	created.Close()

	opened, err := Open(created.Root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Name != "job" || opened.ID != "20260101000000" {
		t.Errorf("Open parsed name/id = %q/%q", opened.Name, opened.ID)
	}

	if _, err := Open(filepath.Join(base, "does-not-exist")); err == nil {
		t.Error("expected error opening non-run dir")
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		in       string
		done     bool
		failed   bool
		reason   string
		terminal bool
	}{
		{"DONE", true, false, "", true},
		{"DONE\nextra", true, false, "", true},
		{"FAILED: out of disk", false, true, "out of disk", true},
		{"FAILED", false, true, "", true},
		{"FAILED no colon", false, true, "no colon", true},
		{"working...", false, false, "", false},
		{"", false, false, "", false},
		{"  DONE  \n", true, false, "", true},
	}
	for _, tt := range tests {
		got := ParseStatus(tt.in)
		if got.Done != tt.done || got.Failed != tt.failed || got.Reason != tt.reason || got.Terminal() != tt.terminal {
			t.Errorf("ParseStatus(%q) = %+v terminal=%v, want done=%v failed=%v reason=%q terminal=%v",
				tt.in, got, got.Terminal(), tt.done, tt.failed, tt.reason, tt.terminal)
		}
	}
}
