package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFullLifecycle simulates a complete task lifecycle:
// Start -> Progress -> Done and verifies the state file is correctly updated.
func TestFullLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_state.json")

	sm, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	taskID := "task-abc-123"
	channelID := "C01ABCDEF"
	slackTS := "1700000000.123456"

	// --- Phase 1: Start ---
	if err := sm.Set(TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set(start): %v", err)
	}

	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("expected task record after start")
	}
	if rec.Status != "running" {
		t.Errorf("status after start: got %q, want %q", rec.Status, "running")
	}
	if rec.ChannelID != channelID {
		t.Errorf("channel_id after start: got %q, want %q", rec.ChannelID, channelID)
	}
	if rec.SlackTS != slackTS {
		t.Errorf("slack_ts after start: got %q, want %q", rec.SlackTS, slackTS)
	}
	if rec.UpdatedAt.IsZero() {
		t.Error("updated_at should be set after start")
	}

	// Verify file exists and is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file should exist: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("state file should not be empty")
	}

	// --- Phase 2: Progress (update timestamp) ---
	// Small delay to ensure UpdatedAt changes.
	time.Sleep(10 * time.Millisecond)

	if err := sm.Set(TaskRecord{
		TaskID: taskID,
		Status: "running",
	}); err != nil {
		t.Fatalf("Set(progress): %v", err)
	}

	rec, ok = sm.Get(taskID)
	if !ok {
		t.Fatal("expected task record after progress")
	}
	if rec.Status != "running" {
		t.Errorf("status after progress: got %q, want %q", rec.Status, "running")
	}
	// ChannelID and SlackTS should be preserved (partial update keeps existing values).
	if rec.ChannelID != channelID {
		t.Errorf("channel_id after progress: got %q, want %q", rec.ChannelID, channelID)
	}
	if rec.SlackTS != slackTS {
		t.Errorf("slack_ts after progress: got %q, want %q", rec.SlackTS, slackTS)
	}

	// --- Phase 3: Done ---
	if err := sm.Set(TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "done",
	}); err != nil {
		t.Fatalf("Set(done): %v", err)
	}

	rec, ok = sm.Get(taskID)
	if !ok {
		t.Fatal("expected task record after done")
	}
	if rec.Status != "done" {
		t.Errorf("status after done: got %q, want %q", rec.Status, "done")
	}

	// --- Verify persistence ---
	// Reload from file to confirm durability.
	sm2, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager(reload): %v", err)
	}

	rec2, ok := sm2.Get(taskID)
	if !ok {
		t.Fatal("expected task record after reload")
	}
	if rec2.Status != "done" {
		t.Errorf("status after reload: got %q, want %q", rec2.Status, "done")
	}
	if rec2.ChannelID != channelID {
		t.Errorf("channel_id after reload: got %q, want %q", rec2.ChannelID, channelID)
	}
	if rec2.SlackTS != slackTS {
		t.Errorf("slack_ts after reload: got %q, want %q", rec2.SlackTS, slackTS)
	}

	// --- Verify only active tasks ---
	// After marking done, ListActive should not return this task.
	active := sm2.ListActive()
	for _, a := range active {
		if a.TaskID == taskID {
			t.Error("completed task should not appear in ListActive")
		}
	}
}

// TestFullLifecycleWithError verifies the error path:
// Start -> Progress -> Error.
func TestFullLifecycleWithError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_state.json")

	sm, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	taskID := "task-err-001"
	channelID := "C01XYZ"
	slackTS := "1700000001.654321"

	// Start
	if err := sm.Set(TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set(start): %v", err)
	}

	// Progress
	time.Sleep(10 * time.Millisecond)
	if err := sm.Set(TaskRecord{
		TaskID: taskID,
		Status: "running",
	}); err != nil {
		t.Fatalf("Set(progress): %v", err)
	}

	// Error
	if err := sm.Set(TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "error",
	}); err != nil {
		t.Fatalf("Set(error): %v", err)
	}

	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("expected task record after error")
	}
	if rec.Status != "error" {
		t.Errorf("status after error: got %q, want %q", rec.Status, "error")
	}
}
