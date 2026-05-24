package worker

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/agentfactory/gateway/protocol"
)

// ClineAdapter parses Cline CLI stdout text and maps it to JSONL events.
//
// Mapping rules:
//   - "Writing to file..." → {type: "tool_call", tool: "write_file"}
//   - "Reading file..."    → {type: "tool_call", tool: "read_file"}
//   - "Running..." / "Executing..." → {type: "tool_call", tool: "run_command"}
//   - "Tests passed"       → {type: "done"}
//   - "Task complete" / "Finished"  → {type: "done"}
//   - "Error:"             → {type: "error"}
//   - Other non-empty lines → {type: "progress"}
//
// ANSI escape codes and terminal noise are filtered before parsing.
type ClineAdapter struct {
	startTime time.Time

	ansiRe *regexp.Regexp
}

// NewClineAdapter creates a new ClineAdapter.
func NewClineAdapter() *ClineAdapter {
	return &ClineAdapter{
		startTime: time.Now(),
		ansiRe:    regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\([a-zA-Z]|\x1b\][^\x07]*\x07`),
	}
}

// stripANSI removes ANSI escape codes from a string.
func (a *ClineAdapter) stripANSI(s string) string {
	return strings.TrimSpace(a.ansiRe.ReplaceAllString(s, ""))
}

// parseLine converts a single line of Cline stdout into a SlackEvent.
// Returns nil if the line should be skipped (empty, noise).
func (a *ClineAdapter) parseLine(line string) *protocol.SlackEvent {
	cleaned := a.stripANSI(line)
	if cleaned == "" {
		return nil
	}

	lower := strings.ToLower(cleaned)

	// Tool call: writing to file
	if strings.Contains(lower, "writing to file") {
		file := extractAfter(cleaned, "Writing to file")
		return &protocol.SlackEvent{
			Type: protocol.EventTypeToolCall,
			Payload: &protocol.EventPayload{
				Action: "write_file",
				Tool: &protocol.ToolInfo{
					Name: "write_file",
					Args: file,
				},
			},
		}
	}

	// Tool call: reading file
	if strings.Contains(lower, "reading file") {
		file := extractAfter(cleaned, "Reading file")
		return &protocol.SlackEvent{
			Type: protocol.EventTypeToolCall,
			Payload: &protocol.EventPayload{
				Action: "read_file",
				Tool: &protocol.ToolInfo{
					Name: "read_file",
					Args: file,
				},
			},
		}
	}

	// Tool call: running / executing command
	if strings.Contains(lower, "running") || strings.Contains(lower, "executing") {
		cmd := extractCommand(cleaned)
		return &protocol.SlackEvent{
			Type: protocol.EventTypeToolCall,
			Payload: &protocol.EventPayload{
				Action: "run_command",
				Tool: &protocol.ToolInfo{
					Name: "run_command",
					Args: cmd,
				},
			},
		}
	}

	// Done: tests passed
	if strings.Contains(lower, "test") && strings.Contains(lower, "passed") {
		return &protocol.SlackEvent{
			Type: protocol.EventTypeDone,
			Payload: &protocol.EventPayload{
				Output: "Tests passed",
			},
		}
	}

	// Done: task complete / finished / all done
	if strings.Contains(lower, "task complete") ||
		strings.Contains(lower, "finished") ||
		strings.Contains(lower, "all done") {
		return &protocol.SlackEvent{
			Type: protocol.EventTypeDone,
			Payload: &protocol.EventPayload{
				Output: cleaned,
			},
		}
	}

	// Error
	if strings.HasPrefix(lower, "error") || strings.Contains(lower, "error:") {
		msg := extractAfter(cleaned, "Error")
		if msg == "" {
			msg = cleaned
		}
		return &protocol.SlackEvent{
			Type: protocol.EventTypeError,
			Payload: &protocol.EventPayload{
				Message: msg,
			},
		}
	}

	// Progress: any other meaningful line (skip very short lines)
	if len(cleaned) > 3 {
		elapsed := time.Since(a.startTime).Seconds()
		return &protocol.SlackEvent{
			Type: protocol.EventTypeProgress,
			Payload: &protocol.EventPayload{
				Action:      cleaned,
				ElapsedTime: fmt.Sprintf("%.1fs", elapsed),
			},
		}
	}

	return nil
}

// extractAfter returns the text after a prefix (case-insensitive match).
func extractAfter(s, prefix string) string {
	idx := strings.Index(strings.ToLower(s), strings.ToLower(prefix))
	if idx == -1 {
		return ""
	}
	rest := s[idx+len(prefix):]
	// Strip leading punctuation: ":", ".", etc.
	rest = strings.TrimLeft(rest, ":. ")
	return strings.TrimSpace(rest)
}

// extractCommand attempts to extract a command from a "Running..." line.
func extractCommand(s string) string {
	for _, prefix := range []string{"Running", "Executing"} {
		if rest := extractAfter(s, prefix); rest != "" {
			return rest
		}
	}
	return s
}

// Stream reads from a reader (Cline stdout) and emits SlackEvents via callback.
// It blocks until the reader is exhausted or an error occurs.
func (a *ClineAdapter) Stream(r io.Reader, cb StreamCallback) error {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for potentially long lines.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		event := a.parseLine(line)
		if event != nil {
			cb(event, nil)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// Reset resets the adapter's start time for a new session.
func (a *ClineAdapter) Reset() {
	a.startTime = time.Now()
}
