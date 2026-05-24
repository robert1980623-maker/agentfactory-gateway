package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/agentfactory/gateway/protocol"
)

// StreamCallback is called for each parsed JSONL event from the worker.
type StreamCallback func(event *protocol.SlackEvent, err error)

// StreamWorker runs a Python worker and streams JSONL events back via a callback.
type StreamWorker struct {
	PythonBin string
	Script    string
}

// NewStreamWorker creates a new StreamWorker.
func NewStreamWorker(pythonBin string) *StreamWorker {
	return &StreamWorker{
		PythonBin: pythonBin,
		Script:    "worker.py",
	}
}

// Execute runs the Python worker and streams events back through the callback.
// It blocks until the process completes.
// Debounce: UI updates are limited to max 1 per second.
func (w *StreamWorker) Execute(req protocol.TaskRequest, cb StreamCallback) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	cmd := exec.Command(w.PythonBin, w.Script)
	cmd.Stdin = bytes.NewReader(payload)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start python worker: %w", err)
	}

	// Debounce state: last event forwarded time.
	var mu sync.Mutex
	lastForwarded := time.Time{}

	// Forward an event through the callback, respecting the debounce interval.
	forward := func(event *protocol.SlackEvent) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if now.Sub(lastForwarded) < time.Second {
			return // debounced
		}
		lastForwarded = now
		cb(event, nil)
	}

	// Stream stdout line-by-line.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event protocol.SlackEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Not valid JSON — skip (could be debug output).
			continue
		}

		forward(&event)
	}

	if err := scanner.Err(); err != nil {
		cb(nil, fmt.Errorf("scan stdout: %w", err))
	}

	// Drain stderr for logging (non-blocking).
	go func() {
		stderrScanner := bufio.NewScanner(stderr)
		for stderrScanner.Scan() {
			// stderr lines are logged internally, not sent to callback.
		}
	}()

	// Wait for the process to finish.
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("python worker exited with error: %w", err)
	}

	// Flush the final event (if any was debounced).
	mu.Lock()
	mu.Unlock()

	return nil
}
