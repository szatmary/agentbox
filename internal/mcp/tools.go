package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/szatmary/agentbox/internal/observe"
	"github.com/szatmary/agentbox/internal/run"
)

// tool is one MCP tool: a JSON Schema for its arguments plus a handler that
// returns the text payload (or an error, surfaced to the model as isError).
type tool struct {
	name        string
	description string
	inputSchema map[string]any
	handler     func(ctx context.Context, args map[string]any) (string, error)
}

// schema helpers ---------------------------------------------------------------

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// argument extraction ----------------------------------------------------------

func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func argInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64: // JSON numbers decode to float64
		return int(v)
	case int:
		return v
	default:
		return def
	}
}

func argBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// argCommand extracts a command as []string from either a string ("sh -c"-wrapped)
// or an array of strings.
func argCommand(args map[string]any, key string) ([]string, error) {
	switch v := args[key].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("empty command")
		}
		return []string{"sh", "-c", v}, nil
	case []any:
		cmd := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("command array must contain only strings")
			}
			cmd = append(cmd, s)
		}
		if len(cmd) == 0 {
			return nil, fmt.Errorf("empty command")
		}
		return cmd, nil
	default:
		return nil, fmt.Errorf("missing required argument: %s (string or string array)", key)
	}
}

// buildTools wires the observe layer into the MCP tool set.
func (s *Server) buildTools() []tool {
	runArg := map[string]any{"run": strProp("run name (job name => latest run, or a full <job>-<id>)")}

	return []tool{
		{
			name:        "list_runs",
			description: "List all agentbox runs with their STATUS sentinel and live state.",
			inputSchema: objSchema(map[string]any{}),
			handler:     s.toolListRuns,
		},
		{
			name:        "get_status",
			description: "Get a run's status: sandbox liveness plus its STATUS sentinel (DONE/FAILED/in-progress).",
			inputSchema: objSchema(runArg, "run"),
			handler:     s.toolGetStatus,
		},
		{
			name:        "tail_log",
			description: "Return the tail of a run's host log (logs/run.log).",
			inputSchema: objSchema(mergeProps(runArg, map[string]any{"lines": intProp("number of trailing lines (default 200)")}), "run"),
			handler:     s.toolTailLog,
		},
		{
			name:        "list_files",
			description: "List a directory inside a live run's VM (ls -la). Defaults to the working tree.",
			inputSchema: objSchema(mergeProps(runArg, map[string]any{"path": strProp("directory path (default /work/workspace)")}), "run"),
			handler:     s.toolListFiles,
		},
		{
			name:        "read_file",
			description: "Read a file inside a live run's VM.",
			inputSchema: objSchema(mergeProps(runArg, map[string]any{"path": strProp("file path")}), "run", "path"),
			handler:     s.toolReadFile,
		},
		{
			name:        "git_status",
			description: "Run `git status` in a live run's working tree.",
			inputSchema: objSchema(runArg, "run"),
			handler:     s.toolGitStatus,
		},
		{
			name:        "git_diff",
			description: "Run `git diff` (optionally --cached) in a live run's working tree.",
			inputSchema: objSchema(mergeProps(runArg, map[string]any{"staged": boolProp("show staged changes (--cached)")}), "run"),
			handler:     s.toolGitDiff,
		},
		{
			name:        "exec",
			description: "Run a command inside a live run's VM. WARNING: runs in your own sandbox VM (same blast radius as `agentbox shell`).",
			inputSchema: objSchema(mergeProps(runArg, map[string]any{"command": map[string]any{"description": "command string or array of args", "type": []any{"string", "array"}}}), "run", "command"),
			handler:     s.toolExec,
		},
		{
			name:        "stop",
			description: "Request a graceful stop of a run (writes its STOP control file).",
			inputSchema: objSchema(runArg, "run"),
			handler:     s.toolStop,
		},
	}
}

func mergeProps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// tool handlers ----------------------------------------------------------------

func (s *Server) toolListRuns(ctx context.Context, args map[string]any) (string, error) {
	type runInfo struct {
		Run     string `json:"run"`
		Status  string `json:"status"`
		Running bool   `json:"running"`
	}
	var infos []runInfo
	for _, dir := range s.listRunDirs() {
		o, err := observe.New(s.rt, dir)
		if err != nil {
			continue
		}
		st, err := o.Status(ctx)
		if err != nil {
			continue
		}
		infos = append(infos, runInfo{Run: filepath.Base(dir), Status: statusWord(st), Running: st.Running})
	}
	return jsonText(infos)
}

func statusWord(st observe.Status) string {
	switch {
	case st.Sentinel.Done:
		return "DONE"
	case st.Sentinel.Failed:
		return "FAILED"
	case st.HasStatus:
		return "in-progress"
	default:
		return "running?"
	}
}

func (s *Server) toolGetStatus(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	st, err := o.Status(ctx)
	if err != nil {
		return "", err
	}
	return jsonText(map[string]any{
		"run":        st.Name,
		"running":    st.Running,
		"hasStatus":  st.HasStatus,
		"status":     statusWord(st),
		"statusText": strings.TrimRight(st.StatusText, "\n"),
		"reason":     st.Sentinel.Reason,
	})
}

func (s *Server) toolTailLog(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	return o.TailLog(ctx, argInt(args, "lines", 200))
}

func (s *Server) toolListFiles(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	return o.ListFiles(ctx, argString(args, "path"))
}

func (s *Server) toolReadFile(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	return o.ReadFile(ctx, argString(args, "path"))
}

func (s *Server) toolGitStatus(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	return o.GitStatus(ctx)
}

func (s *Server) toolGitDiff(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	return o.GitDiff(ctx, argBool(args, "staged"))
}

func (s *Server) toolExec(ctx context.Context, args map[string]any) (string, error) {
	o, err := s.observerFor(argString(args, "run"))
	if err != nil {
		return "", err
	}
	cmd, err := argCommand(args, "command")
	if err != nil {
		return "", err
	}
	res, err := o.Exec(ctx, cmd...)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "exit: %d\n", res.ExitCode)
	if res.Stdout != "" {
		fmt.Fprintf(&b, "--- stdout ---\n%s", res.Stdout)
		if !strings.HasSuffix(res.Stdout, "\n") {
			b.WriteByte('\n')
		}
	}
	if res.Stderr != "" {
		fmt.Fprintf(&b, "--- stderr ---\n%s", res.Stderr)
		if !strings.HasSuffix(res.Stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

func (s *Server) toolStop(ctx context.Context, args map[string]any) (string, error) {
	ref := argString(args, "run")
	dir, ok := s.resolveRunDir(ref)
	if !ok {
		return "", fmt.Errorf("no run matching %q under %s", ref, s.runsDir)
	}
	r, err := run.Open(dir)
	if err != nil {
		return "", err
	}
	if err := r.WriteStop(); err != nil {
		return "", err
	}
	return fmt.Sprintf("requested stop of %s (wrote STOP)", filepath.Base(dir)), nil
}

func jsonText(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
