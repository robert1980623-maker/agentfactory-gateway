package state

import (
	"encoding/json"
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

// TestHasActiveTask verifies per-channel active task detection.
func TestHasActiveTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_state.json")
	sm, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	ch1 := "C01CH1"
	ch2 := "C01CH2"

	// No tasks yet.
	if sm.HasActiveTask(ch1) {
		t.Error("HasActiveTask should be false with no tasks")
	}

	// Running task in ch1.
	if err := sm.Set(TaskRecord{TaskID: "t1", ChannelID: ch1, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if !sm.HasActiveTask(ch1) {
		t.Error("HasActiveTask should be true for running task")
	}
	if sm.HasActiveTask(ch2) {
		t.Error("HasActiveTask should be false for different channel")
	}

	// Paused task should also be detected.
	if err := sm.Set(TaskRecord{TaskID: "t2", ChannelID: ch2, Status: "paused"}); err != nil {
		t.Fatal(err)
	}
	if !sm.HasActiveTask(ch2) {
		t.Error("HasActiveTask should be true for paused task")
	}

	// Done/error tasks should NOT be detected.
	if err := sm.Set(TaskRecord{TaskID: "t1", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if sm.HasActiveTask(ch1) {
		t.Error("HasActiveTask should be false for done task")
	}
}

// TestGetByChannel verifies retrieving the most recent task for a channel.
func TestGetByChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_state.json")
	sm, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	ch := "C01CH"

	// No tasks yet.
	_, ok := sm.GetByChannel(ch)
	if ok {
		t.Error("GetByChannel should return false with no tasks")
	}

	// First task.
	if err := sm.Set(TaskRecord{TaskID: "t1", ChannelID: ch, Prompt: "first prompt", UserID: "U123"}); err != nil {
		t.Fatal(err)
	}
	rec, ok := sm.GetByChannel(ch)
	if !ok {
		t.Fatal("GetByChannel should return true")
	}
	if rec.TaskID != "t1" {
		t.Errorf("expected t1, got %s", rec.TaskID)
	}

	// Second task (should override t1 as most recent).
	time.Sleep(10 * time.Millisecond)
	if err := sm.Set(TaskRecord{TaskID: "t2", ChannelID: ch, Prompt: "second prompt", Status: "done", UserID: "U456"}); err != nil {
		t.Fatal(err)
	}
	rec, ok = sm.GetByChannel(ch)
	if !ok {
		t.Fatal("GetByChannel should return true")
	}
	if rec.TaskID != "t2" {
		t.Errorf("expected t2 as most recent, got %s", rec.TaskID)
	}
	if rec.Prompt != "second prompt" {
		t.Errorf("expected 'second prompt', got %q", rec.Prompt)
	}
	if rec.UserID != "U456" {
		t.Errorf("expected U456, got %q", rec.UserID)
	}
}

// TestPromptAndUserFields verifies that Prompt and UserID fields persist through Set/Get/reload.
func TestPromptAndUserFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_state.json")
	sm, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	if err := sm.Set(TaskRecord{
		TaskID:    "t1",
		ChannelID: "C01",
		UserID:    "U12345",
		Prompt:    "Write a Fibonacci function in Go",
		Status:    "running",
	}); err != nil {
		t.Fatal(err)
	}

	rec, ok := sm.Get("t1")
	if !ok {
		t.Fatal("expected record")
	}
	if rec.UserID != "U12345" {
		t.Errorf("UserID: got %q, want U12345", rec.UserID)
	}
	if rec.Prompt != "Write a Fibonacci function in Go" {
		t.Errorf("Prompt: got %q, want full prompt", rec.Prompt)
	}

	// Reload from disk.
	sm2, err := NewStateManager(path)
	if err != nil {
		t.Fatal(err)
	}
	rec2, ok := sm2.Get("t1")
	if !ok {
		t.Fatal("expected record after reload")
	}
	if rec2.UserID != "U12345" {
		t.Errorf("UserID after reload: got %q", rec2.UserID)
	}
	if rec2.Prompt != "Write a Fibonacci function in Go" {
		t.Errorf("Prompt after reload: got %q", rec2.Prompt)
	}
}

// TestAtomicWrite verifies that the state file is never partially written.
func TestAtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_state.json")
	sm, err := NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	// Write a task.
	if err := sm.Set(TaskRecord{TaskID: "t1", ChannelID: "C01", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	// Verify .tmp file does NOT exist after successful write.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful atomic write")
	}

	// Verify the actual file is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records map[string]TaskRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Errorf("state file should be valid JSON: %v", err)
	}
}
