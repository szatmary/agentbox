package run

import "strings"

// Sentinel is the parsed first line of a STATUS file.
type Sentinel struct {
	// Done is true when the agent declared success (first line "DONE").
	Done bool
	// Failed is true when the agent aborted (first line "FAILED: <reason>").
	Failed bool
	// Reason is the text after "FAILED:" (trimmed), if any.
	Reason string
}

// Terminal reports whether the sentinel ends the run (DONE or FAILED).
func (s Sentinel) Terminal() bool { return s.Done || s.Failed }

// ParseStatus interprets the contents of a STATUS file. Per the control
// protocol, only the first line matters: exactly "DONE" means success, and a
// line beginning "FAILED" (optionally "FAILED: reason") means failure. Anything
// else is treated as not-yet-terminal.
func ParseStatus(content string) Sentinel {
	line := content
	if i := strings.IndexAny(content, "\r\n"); i >= 0 {
		line = content[:i]
	}
	line = strings.TrimSpace(line)
	switch {
	case line == "DONE":
		return Sentinel{Done: true}
	case line == "FAILED" || strings.HasPrefix(line, "FAILED:") || strings.HasPrefix(line, "FAILED "):
		reason := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "FAILED:"), "FAILED"))
		return Sentinel{Failed: true, Reason: reason}
	default:
		return Sentinel{}
	}
}
