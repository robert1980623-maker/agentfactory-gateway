package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"syscall"

	"github.com/agentfactory/gateway/protocol"
)

// TaskStatusResponse is the JSON response from a check-task CLI call.
type TaskStatusResponse struct {
	Status  string `json:"status"`
	TaskID  string `json:"task_id,omitempty"`
	Message string `json:"message,omitempty"`
}

type PythonWorker struct {
	PythonBin string
	Script    string
	AFCLIBin  string
	CLIScript string

	cmd     *exec.Cmd
	stopped chan struct{}
}

func NewPythonWorker(pythonBin string) *PythonWorker {
	return &PythonWorker{
		PythonBin: pythonBin,
		Script:    "worker.py",
		CLIScript: "cli.py",
		stopped:   make(chan struct{}),
	}
}

// WithAFCLI sets the AF CLI binary path for status checks.
func (w *PythonWorker) WithAFCLI(bin string) *PythonWorker {
	w.AFCLIBin = bin
	return w
}

// WithCLIScript sets the CLI script path (used when AFCLIBin is empty).
func (w *PythonWorker) WithCLIScript(script string) *PythonWorker {
	w.CLIScript = script
	return w
}

func (w *PythonWorker) Execute(req protocol.TaskRequest) (protocol.TaskResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return protocol.TaskResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	cmd := exec.Command(w.PythonBin, w.Script)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Store cmd reference so Stop() can signal it.
	w.cmd = cmd

	// Reset stopped channel for this execution.
	w.stopped = make(chan struct{})

	// Start the process.
	if err := cmd.Start(); err != nil {
		w.cmd = nil
		return protocol.TaskResponse{}, fmt.Errorf("python worker failed to start: %w", err)
	}

	// Wait for either the process to finish or a stop signal.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err = <-done:
		// Process completed normally.
		w.cmd = nil
		if err != nil {
			return protocol.TaskResponse{}, fmt.Errorf("python worker failed: %w (stderr: %s)", err, stderr.String())
		}
	case <-w.stopped:
		// Stop was requested — kill the process.
		w.cmd = nil
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done // reap the goroutine
		return protocol.TaskResponse{}, fmt.Errorf("python worker stopped (stderr: %s)", stderr.String())
	}

	var resp protocol.TaskResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return protocol.TaskResponse{}, fmt.Errorf("unmarshal response: %w (stdout: %s)", err, stdout.String())
	}

	return resp, nil
}

// CheckStatus queries the Python worker for the current status of a task.
// It runs `af check-task --task-id <id>` if AFCLIBin is set, otherwise
// falls back to `python <CLIScript> check-task --task-id <id>`.
// It parses the JSON response and returns the status string.
func (w *PythonWorker) CheckStatus(taskID string) (status string, err error) {
	var cmd *exec.Cmd

	if w.AFCLIBin != "" {
		cmd = exec.Command(w.AFCLIBin, "check-task", "--task-id", taskID)
	} else {
		cmd = exec.Command(w.PythonBin, w.CLIScript, "check-task", "--task-id", taskID)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("check-task failed: %w (stderr: %s)", err, stderr.String())
	}

	var resp TaskStatusResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("unmarshal status response: %w (stdout: %s)", err, stdout.String())
	}

	if resp.Status == "" {
		return "", fmt.Errorf("empty status in response for task %s", taskID)
	}

	return resp.Status, nil
}

// Stop sends SIGTERM to the currently running Python process (if any)
// and signals the stopped channel to interrupt Execute().
func (w *PythonWorker) Stop() {
	if w.cmd != nil && w.cmd.Process != nil {
		log.Printf("PythonWorker.Stop: sending SIGTERM to pid %d", w.cmd.Process.Pid)
		w.cmd.Process.Signal(syscall.SIGTERM)
	}

	// Signal the stopped channel to unblock Execute()'s wait.
	select {
	case <-w.stopped:
		// Already closed.
	default:
		close(w.stopped)
	}
}
