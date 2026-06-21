// Package mcp serves agentbox's observe layer to AI agents over the Model
// Context Protocol. It implements the JSON-RPC 2.0 subset MCP needs
// (initialize / tools/list / tools/call) directly — no third-party MCP library —
// so the server is small, dependency-free, and table-testable.
//
// Tools (list_runs, get_status, tail_log, list_files, read_file, git_status,
// git_diff, exec, stop) are thin wrappers over [observe.Observer], so an
// external Claude can watch and steer a live run the same way `agentbox status`
// and `agentbox shell` do. The transport is stdio or HTTP; see transport.go.
//
// TRUST MODEL: the `exec` tool runs arbitrary commands inside the caller's own
// sandbox VM (the same blast radius as `agentbox shell`). The HTTP transport
// therefore defaults to a localhost bind — see the README.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/observe"
	"github.com/szatmary/agentbox/internal/run"
)

// ProtocolVersion is the MCP revision this server speaks.
const ProtocolVersion = "2024-11-05"

// Server exposes the observe layer as MCP tools over one or more runs.
type Server struct {
	runsDir string
	rt      container.Runtime
	version string
	tools   []tool
}

// NewServer returns a Server reading runs under runsDir and execing into their
// sandboxes via rt. version is reported in the initialize handshake.
func NewServer(runsDir string, rt container.Runtime, version string) *Server {
	s := &Server{runsDir: runsDir, rt: rt, version: version}
	s.tools = s.buildTools()
	return s
}

// --- JSON-RPC 2.0 wire types -------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// HandleRaw parses one JSON-RPC message and returns the marshaled response, or
// nil for a notification (no id). A parse error yields a JSON-RPC error response
// with a null id.
func (s *Server) HandleRaw(ctx context.Context, raw []byte) []byte {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return mustMarshal(errorResponse(nil, codeParseError, "parse error: "+err.Error()))
	}
	resp := s.handle(ctx, &req)
	if resp == nil {
		return nil // notification
	}
	return mustMarshal(resp)
}

// handle dispatches a parsed request. It returns nil for notifications.
func (s *Server) handle(ctx context.Context, req *rpcRequest) *rpcResponse {
	isNotification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		return s.ok(req, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agentbox", "version": s.version},
		})
	case "notifications/initialized", "notifications/cancelled":
		return nil // notifications: no response
	case "ping":
		return s.ok(req, map[string]any{})
	case "tools/list":
		return s.ok(req, map[string]any{"tools": s.toolDescriptors()})
	case "tools/call":
		if isNotification {
			return nil
		}
		return s.handleToolCall(ctx, req)
	default:
		if isNotification {
			return nil
		}
		return errorResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) ok(req *rpcRequest, result any) *rpcResponse {
	if len(req.ID) == 0 {
		return nil
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// --- tools/call --------------------------------------------------------------

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *Server) handleToolCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var p toolCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorResponse(req.ID, codeInvalidParams, "invalid params: "+err.Error())
		}
	}
	t := s.lookupTool(p.Name)
	if t == nil {
		return errorResponse(req.ID, codeInvalidParams, "unknown tool: "+p.Name)
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	text, err := t.handler(ctx, p.Arguments)
	if err != nil {
		// Per MCP, tool failures are returned as a result with isError=true (not
		// a protocol-level error), so the model can read and react to them.
		return s.ok(req, toolResult(err.Error(), true))
	}
	return s.ok(req, toolResult(text, false))
}

// toolResult builds an MCP tool result with a single text content block.
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (s *Server) lookupTool(name string) *tool {
	for i := range s.tools {
		if s.tools[i].name == name {
			return &s.tools[i]
		}
	}
	return nil
}

func (s *Server) toolDescriptors() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, map[string]any{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.inputSchema,
		})
	}
	return out
}

// --- run resolution + observer construction ----------------------------------

// observerFor resolves a run ref (run-dir base name or job name → latest run)
// and returns an Observer over it.
func (s *Server) observerFor(ref string) (*observe.Observer, error) {
	if ref == "" {
		return nil, fmt.Errorf("missing required argument: run")
	}
	dir, ok := s.resolveRunDir(ref)
	if !ok {
		return nil, fmt.Errorf("no run matching %q under %s", ref, s.runsDir)
	}
	return observe.New(s.rt, dir)
}

// resolveRunDir maps a run ref to its run directory: an exact run-dir base name,
// else the most recent run whose name starts with the job name.
func (s *Server) resolveRunDir(ref string) (string, bool) {
	dirs := s.listRunDirs()
	for _, d := range dirs {
		if filepath.Base(d) == ref {
			return d, true
		}
	}
	var best string
	for _, d := range dirs {
		if strings.HasPrefix(filepath.Base(d), ref+"-") && d > best {
			best = d
		}
	}
	return best, best != ""
}

// listRunDirs returns the run directories under runsDir, sorted.
func (s *Server) listRunDirs() []string {
	dirs, err := run.ListDirs(s.runsDir)
	if err != nil {
		return nil
	}
	sort.Strings(dirs)
	return dirs
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Should never happen for our concrete result types; surface as a
		// JSON-RPC internal error with a null id rather than panicking.
		b, _ = json.Marshal(errorResponse(nil, codeInternalError, "marshal error"))
	}
	return b
}
