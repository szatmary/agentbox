package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/run"
)

// testServer builds an MCP server over a temp runs dir with one run, and marks
// that run's container running in the fake.
func testServer(t *testing.T) (*Server, *container.Fake, string) {
	t.Helper()
	runsDir := filepath.Join(t.TempDir(), "runs")
	r, err := run.Create(runsDir, "job", "20260101T000000Z")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	name := filepath.Base(r.Root)
	fake := &container.Fake{}
	fake.SetRunning(name, true)
	return NewServer(runsDir, fake, "test"), fake, name
}

// call invokes a tool and returns the text content + isError.
func call(t *testing.T, s *Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + string(params) + `}`)
	raw := s.HandleRaw(context.Background(), req)
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("protocol error for %s: %+v", name, resp.Error)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("no content for %s:\n%s", name, raw)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func TestInitializeAndToolsList(t *testing.T) {
	s, _, _ := testServer(t)

	init := s.HandleRaw(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if !strings.Contains(string(init), ProtocolVersion) || !strings.Contains(string(init), "agentbox") {
		t.Errorf("initialize result wrong:\n%s", init)
	}

	list := s.HandleRaw(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	for _, want := range []string{
		"list_runs", "get_status", "tail_log", "list_files", "read_file",
		"git_status", "git_diff", "exec", "stop",
	} {
		if !strings.Contains(string(list), want) {
			t.Errorf("tools/list missing %q:\n%s", want, list)
		}
	}
}

func TestNotificationNoResponse(t *testing.T) {
	s, _, _ := testServer(t)
	// A notification (no id) must produce no response.
	if got := s.HandleRaw(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); got != nil {
		t.Errorf("notification produced a response: %s", got)
	}
}

func TestUnknownMethodAndTool(t *testing.T) {
	s, _, _ := testServer(t)
	resp := s.HandleRaw(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"bogus"}`))
	if !strings.Contains(string(resp), "method not found") {
		t.Errorf("expected method-not-found:\n%s", resp)
	}
	// Unknown tool => invalid params error.
	_, _ = resp, resp
	params := `{"name":"nope","arguments":{}}`
	r2 := s.HandleRaw(context.Background(), []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":`+params+`}`))
	if !strings.Contains(string(r2), "unknown tool") {
		t.Errorf("expected unknown-tool:\n%s", r2)
	}
}

func TestParseError(t *testing.T) {
	s, _, _ := testServer(t)
	resp := s.HandleRaw(context.Background(), []byte(`{not json`))
	if !strings.Contains(string(resp), "parse error") {
		t.Errorf("expected parse error:\n%s", resp)
	}
}

func TestToolListRuns(t *testing.T) {
	s, _, name := testServer(t)
	text, isErr := call(t, s, "list_runs", map[string]any{})
	if isErr {
		t.Fatalf("list_runs error: %s", text)
	}
	if !strings.Contains(text, name) || !strings.Contains(text, `"running": true`) {
		t.Errorf("list_runs text:\n%s", text)
	}
}

func TestToolGetStatus(t *testing.T) {
	s, _, name := testServer(t)
	text, isErr := call(t, s, "get_status", map[string]any{"run": "job"})
	if isErr {
		t.Fatalf("get_status error: %s", text)
	}
	if !strings.Contains(text, name) || !strings.Contains(text, `"running": true`) {
		t.Errorf("get_status:\n%s", text)
	}
	// Missing run arg => error result.
	if text, isErr := call(t, s, "get_status", map[string]any{}); !isErr {
		t.Errorf("expected error for missing run, got: %s", text)
	}
}

func TestExecToolArgvAndOutput(t *testing.T) {
	s, fake, _ := testServer(t)
	fake.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		return container.ExecResult{ExitCode: 0, Stdout: "argv:" + strings.Join(opts.Cmd, " ")}, nil
	}

	// Array command passed through verbatim.
	text, isErr := call(t, s, "exec", map[string]any{"run": "job", "command": []any{"ls", "-la"}})
	if isErr {
		t.Fatalf("exec error: %s", text)
	}
	if !strings.Contains(text, "argv:ls -la") || !strings.Contains(text, "exit: 0") {
		t.Errorf("exec output:\n%s", text)
	}

	// String command is wrapped in sh -c.
	text, _ = call(t, s, "exec", map[string]any{"run": "job", "command": "echo hi"})
	if !strings.Contains(text, "argv:sh -c echo hi") {
		t.Errorf("string command not sh-wrapped:\n%s", text)
	}
}

func TestReadFileAndGitTools(t *testing.T) {
	s, fake, _ := testServer(t)
	fake.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) (container.ExecResult, error) {
		joined := strings.Join(opts.Cmd, " ")
		switch {
		case strings.HasPrefix(joined, "cat -- "):
			return container.ExecResult{ExitCode: 0, Stdout: "file-body"}, nil
		case strings.Contains(joined, "status --porcelain"):
			return container.ExecResult{ExitCode: 0, Stdout: "## main\n M f.go\n"}, nil
		case strings.Contains(joined, "diff"):
			return container.ExecResult{ExitCode: 0, Stdout: "diff --git a b"}, nil
		case strings.HasPrefix(joined, "ls -la"):
			return container.ExecResult{ExitCode: 0, Stdout: "total 0"}, nil
		}
		return container.ExecResult{ExitCode: 0}, nil
	}

	if text, isErr := call(t, s, "read_file", map[string]any{"run": "job", "path": "/work/output/x"}); isErr || !strings.Contains(text, "file-body") {
		t.Errorf("read_file: %q err=%v", text, isErr)
	}
	if text, _ := call(t, s, "git_status", map[string]any{"run": "job"}); !strings.Contains(text, "## main") {
		t.Errorf("git_status: %q", text)
	}
	if text, _ := call(t, s, "git_diff", map[string]any{"run": "job"}); !strings.Contains(text, "diff --git") {
		t.Errorf("git_diff: %q", text)
	}
	if text, _ := call(t, s, "list_files", map[string]any{"run": "job"}); !strings.Contains(text, "total 0") {
		t.Errorf("list_files: %q", text)
	}
}

func TestExecToolNotRunning(t *testing.T) {
	s, fake, name := testServer(t)
	fake.SetRunning(name, false) // bring the VM down
	text, isErr := call(t, s, "exec", map[string]any{"run": "job", "command": []any{"ls"}})
	if !isErr || !strings.Contains(text, "not running") {
		t.Errorf("expected not-running error, got isErr=%v text=%q", isErr, text)
	}
}

func TestStopToolWritesStopFile(t *testing.T) {
	s, _, name := testServer(t)
	text, isErr := call(t, s, "stop", map[string]any{"run": "job"})
	if isErr {
		t.Fatalf("stop error: %s", text)
	}
	stopPath := filepath.Join(s.runsDir, name, run.ControlDir, run.StopFile)
	if _, err := os.Stat(stopPath); err != nil {
		t.Errorf("STOP file not written: %v", err)
	}
}

func TestTailLogTool(t *testing.T) {
	s, _, name := testServer(t)
	logPath := filepath.Join(s.runsDir, name, run.LogsDir, run.LogFile)
	if err := os.WriteFile(logPath, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, _ := call(t, s, "tail_log", map[string]any{"run": "job", "lines": 2})
	if text != "b\nc\n" {
		t.Errorf("tail_log = %q", text)
	}
}

func TestHTTPTransport(t *testing.T) {
	s, _, _ := testServer(t)
	srv := httptest.NewServer(s.HTTPHandler())
	defer srv.Close()

	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := srv.Client().Post(srv.URL, "application/json", strings.NewReader(req))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Result.Tools) != 9 {
		t.Errorf("expected 9 tools over HTTP, got %d", len(body.Result.Tools))
	}
}

func TestStdioTransport(t *testing.T) {
	s, _, _ := testServer(t)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out strings.Builder
	if err := s.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Only the ping gets a response; the notification is silent.
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":1`) {
		t.Errorf("stdio responses = %q", out.String())
	}
}
