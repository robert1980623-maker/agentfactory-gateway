package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
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

	// Create throttler with 1s debounce for progress events.
	throttler := NewMessageThrottler(1*time.Second, func(event *protocol.SlackEvent) {
		cb(event, nil)
	})
	defer throttler.Stop()

	// Stream stdout line-by-line.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event protocol.SlackEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		throttler.Push(&event)
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

	// Flush any remaining buffered event.
	throttler.Flush()

	return nil
}
