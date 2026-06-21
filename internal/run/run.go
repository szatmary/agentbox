// Package run manages the on-disk layout and logging for a single agentbox run.
//
// Each run gets a directory:
//
//	<base>/<name>-<id>/
//	  control/    # STATUS, STOP, pidfile — the host↔agent control channel
//	  output/     # deliverables the agent writes
//	  logs/       # run.log (structured) + per-iteration claude output
//	  workspace/  # the agent's working tree (cloned repo, etc.)
//
// The control/ and output/ subdirectories are bind-mounted into the sandbox so
// the supervisor can read the completion sentinel and the agent's deliverables.
package run

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Subdirectory names within a run directory.
const (
	ControlDir   = "control"
	OutputDir    = "output"
	LogsDir      = "logs"
	WorkspaceDir = "workspace"
)

// Control-channel file names within ControlDir.
const (
	StatusFile = "STATUS"
	StopFile   = "STOP"
	PidFile    = "agentbox.pid"
	LogFile    = "run.log"
)

// Run is a created run directory.
type Run struct {
	// Root is the absolute path of the run directory.
	Root string
	// Name and ID identify the run.
	Name string
	ID   string

	logFile *os.File
	logger  *slog.Logger
}

// Create makes the run directory tree under base, named "<name>-<id>", and
// returns a Run. id should be a sortable, filesystem-safe token (e.g. a
// timestamp). It is an error if the run directory already exists.
func Create(base, name, id string) (*Run, error) {
	if name == "" || id == "" {
		return nil, fmt.Errorf("run: name and id are required")
	}
	if strings.Contains(id, "-") {
		// The run directory is "<name>-<id>" and Open splits on the last '-',
		// so the id must be hyphen-free for the round-trip to be unambiguous
		// (names may contain hyphens).
		return nil, fmt.Errorf("run: id %q must not contain '-'", id)
	}
	root, err := filepath.Abs(filepath.Join(base, name+"-"+id))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); err == nil {
		return nil, fmt.Errorf("run: directory already exists: %s", root)
	}
	for _, sub := range []string{ControlDir, OutputDir, LogsDir, WorkspaceDir} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, fmt.Errorf("run: mkdir %s: %w", sub, err)
		}
	}
	r := &Run{Root: root, Name: name, ID: id}
	if err := r.openLog(); err != nil {
		return nil, err
	}
	return r, nil
}

// Open returns a Run for an existing run directory (e.g. for `agentbox logs`).
// It does not create or truncate anything and does not open the logger for
// writing.
func Open(root string) (*Run, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, ControlDir)); err != nil {
		return nil, fmt.Errorf("run: %s is not a run directory: %w", abs, err)
	}
	base := filepath.Base(abs)
	name, id := splitNameID(base)
	return &Run{Root: abs, Name: name, ID: id}, nil
}

func splitNameID(base string) (name, id string) {
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '-' {
			return base[:i], base[i+1:]
		}
	}
	return base, ""
}

func (r *Run) openLog() error {
	f, err := os.OpenFile(r.Path(LogsDir, LogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("run: open log: %w", err)
	}
	r.logFile = f
	r.logger = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return nil
}

// Path joins parts onto the run root.
func (r *Run) Path(parts ...string) string {
	return filepath.Join(append([]string{r.Root}, parts...)...)
}

// Control returns the path of a file within control/.
func (r *Run) Control(name string) string { return r.Path(ControlDir, name) }

// Output returns the path of the output/ directory.
func (r *Run) Output() string { return r.Path(OutputDir) }

// Workspace returns the path of the workspace/ directory.
func (r *Run) Workspace() string { return r.Path(WorkspaceDir) }

// Logger returns the run's structured logger, writing JSON lines to logs/run.log.
// It is nil for Runs opened with Open.
func (r *Run) Logger() *slog.Logger { return r.logger }

// LogWriter returns an io.Writer for the log file (e.g. to tee claude output),
// or nil if the log is not open.
func (r *Run) LogWriter() io.Writer {
	if r.logFile == nil {
		return nil
	}
	return r.logFile
}

// WriteStop writes the STOP control file, requesting a graceful halt.
func (r *Run) WriteStop() error {
	return os.WriteFile(r.Control(StopFile), []byte("stop\n"), 0o644)
}

// ReadStatus returns the contents of the STATUS file and whether it exists.
func (r *Run) ReadStatus() (content string, exists bool, err error) {
	b, err := os.ReadFile(r.Control(StatusFile))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// Close closes the run's log file.
func (r *Run) Close() error {
	if r.logFile != nil {
		err := r.logFile.Close()
		r.logFile = nil
		return err
	}
	return nil
}
