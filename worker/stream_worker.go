package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/agentfactory/gateway/protocol"
)

// ErrWorkerStopped is returned when the StreamWorker is stopped mid-execution.
var ErrWorkerStopped = errors.New("worker stopped")

// StreamCallback is called for each parsed JSONL event from the worker.
type StreamCallback func(event *protocol.SlackEvent, err error)

// StreamWorker runs a worker (Python or Cline) and streams events back via a callback.
type StreamWorker struct {
	PythonBin string
	Script    string
	ClineBin  string
	done      chan struct{}
}

// NewStreamWorker creates a new StreamWorker.
func NewStreamWorker(pythonBin string) *StreamWorker {
	return &StreamWorker{
		PythonBin: pythonBin,
		Script:    "worker.py",
		done:      make(chan struct{}),
	}
}

// WithCline sets the Cline binary path.
func (w *StreamWorker) WithCline(bin string) *StreamWorker {
	w.ClineBin = bin
	return w
}

// Stop signals the StreamWorker to stop processing.
func (sw *StreamWorker) Stop() {
	select {
	case <-sw.done:
		return // already stopped
	default:
		close(sw.done)
	}
}

// Execute runs the appropriate worker based on task type and streams events
// back through the callback. It blocks until the process completes.
//
// If req.TaskType is "external" and ClineBin is set, the Cline CLI is used.
// Otherwise, the Python worker is used (JSONL protocol).
func (w *StreamWorker) Execute(req protocol.TaskRequest, cb StreamCallback) error {
	if req.TaskType == "external" && w.ClineBin != "" {
		return w.executeCline(req, cb)
	}
	return w.executePython(req, cb)
}

// executePython runs the Python worker and streams JSONL events.
func (w *StreamWorker) executePython(req protocol.TaskRequest, cb StreamCallback) error {
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

	// Drain stderr for logging (non-blocking, started early so stderr is captured during execution).
	go func() {
		stderrScanner := bufio.NewScanner(stderr)
		for stderrScanner.Scan() {
			log.Printf("worker stderr: %s", stderrScanner.Text())
		}
	}()

	// Stream stdout line-by-line.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-w.done:
			log.Println("StreamWorker: stopped during execution")
			return ErrWorkerStopped
		default:
		}

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

	// Wait for the process to finish.
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("python worker exited with error: %w", err)
	}

	// Flush any remaining buffered event.
	throttler.Flush()

	return nil
}

// executeCline runs the Cline CLI and adapts its stdout text to JSONL events.
func (w *StreamWorker) executeCline(req protocol.TaskRequest, cb StreamCallback) error {
	// Build prompt from task and context.
	prompt := req.Task
	if req.Context != nil {
		if ctx, ok := req.Context["prompt"].(string); ok && ctx != "" {
			prompt = ctx
		}
	}

	// Build command: cline --auto-approve true --thinking none "<prompt>"
	args := []string{"--auto-approve", "true", "--thinking", "none", prompt}
	cmd := exec.Command(w.ClineBin, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cline: %w", err)
	}

	// Send start event.
	taskID := ""
	if req.Context != nil {
		if tid, ok := req.Context["task_id"].(string); ok {
			taskID = tid
		}
	}
	cb(&protocol.SlackEvent{
		Type: protocol.EventTypeStart,
		Payload: &protocol.EventPayload{
			TaskID:    taskID,
			UserInput: req.Task,
			TaskType:  "external",
		},
	}, nil)

	// Create throttler with 1s debounce for progress events.
	throttler := NewMessageThrottler(1*time.Second, func(event *protocol.SlackEvent) {
		cb(event, nil)
	})
	defer throttler.Stop()

	// Stream stdout through the Cline adapter.
	adapter := NewClineAdapter()
	go func() {
		_ = adapter.Stream(stdout, func(event *protocol.SlackEvent, err error) {
			if err != nil {
				cb(nil, err)
				return
			}
			// Inject task_id into the event.
			if event.Payload != nil && taskID != "" {
				event.Payload.TaskID = taskID
			}
			throttler.Push(event)
		})
	}()

	// Drain stderr (non-blocking, with logging for debugging).
	go func() {
		stderrScanner := bufio.NewScanner(stderr)
		for stderrScanner.Scan() {
			log.Printf("cline stderr: %s", stderrScanner.Text())
		}
	}()

	// Wait for the process to finish.
	if err := cmd.Wait(); err != nil {
		// Send error event before returning.
		cb(&protocol.SlackEvent{
			Type: protocol.EventTypeError,
			Payload: &protocol.EventPayload{
				Message: fmt.Sprintf("cline exited with error: %v", err),
			},
		}, nil)
		return fmt.Errorf("cline exited with error: %w", err)
	}

	// Flush remaining buffered events.
	throttler.Flush()

	// Send done event.
	cb(&protocol.SlackEvent{
		Type: protocol.EventTypeDone,
		Payload: &protocol.EventPayload{
			TaskID: taskID,
			Output: "Cline task completed",
		},
	}, nil)

	return nil
}

// ExecuteDispatch runs a dispatch (multi-agent) task. It sends dispatch start
// and progress events through the callback, simulating sub-agent execution.
// In production, this would coordinate multiple sub-workers; for now it
// delegates to the Python worker with dispatch mode enabled.
func (w *StreamWorker) ExecuteDispatch(req protocol.TaskRequest, cb StreamCallback) error {
	// Set the dispatch flag so the Python worker operates in dispatch mode.
	req.Dispatch = true

	// Inject dispatch flag into request context so the Python worker knows
	// to operate in dispatch mode.
	if req.Context == nil {
		req.Context = make(map[string]interface{})
	}
	req.Context["dispatch_mode"] = true

	// Send dispatch start event with agent count derived from task text.
	taskID := ""
	if req.Context != nil {
		if tid, ok := req.Context["task_id"].(string); ok {
			taskID = tid
		}
	}

	// Estimate agent count from the task text (pipe-separated or default).
	agentCount := estimateAgentCount(req.Task)

	cb(&protocol.SlackEvent{
		Type: protocol.EventTypeStart,
		Payload: &protocol.EventPayload{
			TaskID:      taskID,
			UserInput:   req.Task,
			TaskType:    "dispatch",
			TotalAgents: agentCount,
			Agents:      buildInitialAgents(agentCount),
		},
	}, nil)

	// Execute the actual task through the Python worker in dispatch mode.
	return w.executePython(req, cb)
}

// estimateAgentCount estimates the number of sub-agents from the task text.
// Tasks separated by "|" each get their own agent.
func estimateAgentCount(task string) int {
	if task == "" {
		return 1
	}
	parts := strings.Split(task, "|")
	count := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

// buildInitialAgents creates SubAgentInfo entries for the dispatch start event.
func buildInitialAgents(count int) []protocol.SubAgentInfo {
	roles := []string{"coordinator", "researcher", "developer", "tester", "reviewer"}
	agents := make([]protocol.SubAgentInfo, 0, count)
	for i := 0; i < count; i++ {
		role := "agent"
		if i < len(roles) {
			role = roles[i]
		}
		agents = append(agents, protocol.SubAgentInfo{
			AgentID: fmt.Sprintf("agent-%d", i+1),
			Role:    role,
			Status:  "running",
		})
	}
	return agents
}
