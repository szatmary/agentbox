package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// shutdownGrace bounds graceful HTTP shutdown on context cancellation.
const shutdownGrace = 5 * time.Second

// ServeStdio runs the MCP server over a newline-delimited JSON-RPC stream on
// r/w (the stdio transport). Each line is one JSON-RPC message; responses are
// written one per line. It returns when r reaches EOF or ctx is cancelled.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	// Allow large messages (diffs, file contents) — default 64 KiB is too small.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := sc.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		resp := s.HandleRaw(ctx, line)
		if resp == nil {
			continue // notification
		}
		if _, err := w.Write(append(resp, '\n')); err != nil {
			return err
		}
	}
	return sc.Err()
}

// trimSpace trims ASCII whitespace from a byte slice without allocating.
func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// HTTPHandler returns an http.Handler that accepts a single JSON-RPC request per
// POST and writes the JSON-RPC response. A notification (no id) yields 202 with
// an empty body. This is a minimal JSON-RPC-over-HTTP transport suitable for
// local MCP clients.
//
// SECURITY: the handler runs commands in the caller's sandbox via the `exec`
// tool. Bind it to localhost (the `agentbox mcp --http` default) unless you have
// a specific reason to expose it. See the README trust model.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, 16*1024*1024))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		resp := s.HandleRaw(req.Context(), body)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted) // notification
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	})
	return mux
}

// ListenAndServeHTTP serves the MCP HTTP transport on addr until ctx is
// cancelled, then shuts the server down gracefully.
func (s *Server) ListenAndServeHTTP(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.HTTPHandler()}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("mcp http: %w", err)
	}
}
