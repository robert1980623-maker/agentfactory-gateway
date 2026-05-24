package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCheckStatus_WithAFCLIBin verifies CheckStatus uses the AF CLI binary
// when configured, and correctly parses the JSON response.
func TestCheckStatus_WithAFCLIBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	// Create a fake CLI binary (shell script) that outputs valid JSON.
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-af-cli")
	scriptContent := `#!/bin/sh
echo '{"status":"done","task_id":"test-123"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	w := &PythonWorker{
		AFCLIBin: scriptPath,
	}

	status, err := w.CheckStatus("test-123")
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want %q", status, "done")
	}
}

// TestCheckStatus_WithPythonCLI verifies CheckStatus falls back to
// `python cli.py check-task --task-id <id>` when AFCLIBin is empty.
func TestCheckStatus_WithPythonCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tmpDir := t.TempDir()

	// Create a fake Python CLI script.
	scriptPath := filepath.Join(tmpDir, "cli.py")
	scriptContent := `import sys
import json

# Parse --task-id argument
task_id = None
for i, arg in enumerate(sys.argv):
    if arg == "--task-id" and i + 1 < len(sys.argv):
        task_id = sys.argv[i + 1]

result = {"status": "running", "task_id": task_id}
print(json.dumps(result))
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// Find python3.
	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}

	w := &PythonWorker{
		PythonBin: pythonBin,
		CLIScript: scriptPath,
	}

	status, err := w.CheckStatus("task-456")
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if status != "running" {
		t.Errorf("status = %q, want %q", status, "running")
	}
}

// TestCheckStatus_ErrorStatus verifies that error/timeout statuses are parsed correctly.
func TestCheckStatus_ErrorStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-af-cli")
	scriptContent := `#!/bin/sh
echo '{"status":"error","task_id":"fail-1","message":"something broke"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	w := &PythonWorker{
		AFCLIBin: scriptPath,
	}

	status, err := w.CheckStatus("fail-1")
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if status != "error" {
		t.Errorf("status = %q, want %q", status, "error")
	}
}

// TestCheckStatus_CLIError verifies that a failing CLI command returns an error.
func TestCheckStatus_CLIError(t *testing.T) {
	w := &PythonWorker{
		AFCLIBin: "/nonexistent/binary",
	}

	status, err := w.CheckStatus("some-task")
	if err == nil {
		t.Fatal("expected error for nonexistent binary, got nil")
	}
	if status != "" {
		t.Errorf("expected empty status on error, got %q", status)
	}
}

// TestCheckStatus_InvalidJSON verifies that non-JSON output returns an error.
func TestCheckStatus_InvalidJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "bad-cli")
	scriptContent := `#!/bin/sh
echo "not json at all"
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	w := &PythonWorker{
		AFCLIBin: scriptPath,
	}

	_, err := w.CheckStatus("task-x")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// TestCheckStatus_EmptyStatus verifies that a JSON response with empty status returns an error.
func TestCheckStatus_EmptyStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "empty-cli")
	scriptContent := `#!/bin/sh
echo '{"task_id":"task-y"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	w := &PythonWorker{
		AFCLIBin: scriptPath,
	}

	_, err := w.CheckStatus("task-y")
	if err == nil {
		t.Fatal("expected error for empty status, got nil")
	}
}

// TestPythonWorkerBuilders verifies the fluent builder methods.
func TestPythonWorkerBuilders(t *testing.T) {
	w := NewPythonWorker("python3")
	if w.Script != "worker.py" {
		t.Errorf("default script = %q, want %q", w.Script, "worker.py")
	}
	if w.CLIScript != "cli.py" {
		t.Errorf("default CLI script = %q, want %q", w.CLIScript, "cli.py")
	}
	if w.AFCLIBin != "" {
		t.Errorf("default AFCLIBin = %q, want empty", w.AFCLIBin)
	}

	// Test WithAFCLI.
	w2 := w.WithAFCLI("/usr/local/bin/af")
	if w2.AFCLIBin != "/usr/local/bin/af" {
		t.Errorf("WithAFCLI = %q, want %q", w2.AFCLIBin, "/usr/local/bin/af")
	}
	// Should return same pointer (fluent).
	if w2 != w {
		t.Error("WithAFCLI should return the same worker pointer")
	}

	// Test WithCLIScript.
	w3 := w.WithCLIScript("/custom/cli.py")
	if w3.CLIScript != "/custom/cli.py" {
		t.Errorf("WithCLIScript = %q, want %q", w3.CLIScript, "/custom/cli.py")
	}
}
