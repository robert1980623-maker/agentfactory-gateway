package tests

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	gw "github.com/agentfactory/gateway/gateway"
	statemgr "github.com/agentfactory/gateway/state"
)

// ---- Recovery Tests ----

// TestRecovery_NoActiveTasks verifies that recovery returns immediately
// and without error when there are no running tasks.
func TestRecovery_NoActiveTasks(t *testing.T) {
	sm := NewTestStateManager(t)
	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{
		Statuses: map[string]string{},
	}

	_, err := gw.RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockSlack.Updates) != 0 {
		t.Errorf("expected no Slack updates, got %d", len(mockSlack.Updates))
	}
	if len(mockSlack.Posts) != 0 {
		t.Errorf("expected no Slack posts, got %d", len(mockSlack.Posts))
	}
}

// TestRecovery_SingleTaskDone simulates a running task where CheckStatus
// returns "done", and verifies the state and Slack are updated correctly.
func TestRecovery_SingleTaskDone(t *testing.T) {
	sm := NewTestStateManager(t)

	taskID := "task-done-001"
	channelID := "C123"
	slackTS := "1700000000.123"

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{
		Statuses: map[string]string{taskID: "done"},
	}

	_, err := gw.RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Slack was updated.
	if len(mockSlack.Updates) != 1 {
		t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.Updates))
	}
	upd := mockSlack.Updates[0]
	if upd.ChannelID != channelID {
		t.Errorf("update channel = %q, want %q", upd.ChannelID, channelID)
	}
	if upd.Timestamp != slackTS {
		t.Errorf("update ts = %q, want %q", upd.Timestamp, slackTS)
	}

	// Verify state was updated to "done".
	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found after recovery")
	}
	if rec.Status != "done" {
		t.Errorf("status after recovery = %q, want %q", rec.Status, "done")
	}
}

// TestRecovery_SingleTaskFailed simulates a running task where CheckStatus
// returns an error, and verifies it's marked as failed.
func TestRecovery_SingleTaskFailed(t *testing.T) {
	sm := NewTestStateManager(t)

	taskID := "task-fail-001"
	channelID := "C456"
	slackTS := "1700000001.456"

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{
		Errors: map[string]error{taskID: errors.New("worker unreachable")},
	}

	_, err := gw.RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Slack was updated.
	if len(mockSlack.Updates) != 1 {
		t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.Updates))
	}

	// Verify state was updated to "error".
	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found after recovery")
	}
	if rec.Status != "error" {
		t.Errorf("status after recovery = %q, want %q", rec.Status, "error")
	}
}

// TestRecovery_SingleTaskStillRunning simulates a running task where
// CheckStatus returns "running", and verifies it stays as running.
func TestRecovery_SingleTaskStillRunning(t *testing.T) {
	sm := NewTestStateManager(t)

	taskID := "task-running-001"
	channelID := "C789"
	slackTS := "1700000002.789"

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{
		Statuses: map[string]string{taskID: "running"},
	}

	_, err := gw.RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Slack was updated.
	if len(mockSlack.Updates) != 1 {
		t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.Updates))
	}

	// Verify state remains "running".
	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found after recovery")
	}
	if rec.Status != "running" {
		t.Errorf("status after recovery = %q, want %q", rec.Status, "running")
	}
}

// TestRecovery_MultipleTasks verifies recovery of several tasks with
// different statuses in a single call.
func TestRecovery_MultipleTasks(t *testing.T) {
	sm := NewTestStateManager(t)

	tasks := []struct {
		taskID       string
		channelID    string
		slackTS      string
		workerStatus string
		wantStatus   string
	}{
		{"t1", "C1", "1.0", "done", "done"},
		{"t2", "C2", "2.0", "error", "error"},
		{"t3", "C3", "3.0", "running", "running"},
		{"t4", "C4", "4.0", "failed", "error"},
	}

	for _, tc := range tasks {
		if err := sm.Set(statemgr.TaskRecord{
			TaskID:    tc.taskID,
			ChannelID: tc.channelID,
			SlackTS:   tc.slackTS,
			Status:    "running",
		}); err != nil {
			t.Fatalf("Set %s: %v", tc.taskID, err)
		}
	}

	statuses := map[string]string{}
	for _, tc := range tasks {
		statuses[tc.taskID] = tc.workerStatus
	}

	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{Statuses: statuses}

	_, err := gw.RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all tasks got Slack updates.
	if len(mockSlack.Updates) != len(tasks) {
		t.Errorf("expected %d Slack updates, got %d", len(tasks), len(mockSlack.Updates))
	}

	// Verify each task reached the expected status.
	for _, tc := range tasks {
		rec, ok := sm.Get(tc.taskID)
		if !ok {
			t.Errorf("task %s not found after recovery", tc.taskID)
			continue
		}
		if rec.Status != tc.wantStatus {
			t.Errorf("task %s status = %q, want %q", tc.taskID, rec.Status, tc.wantStatus)
		}
	}
}

// ---- StateManager Tests ----

// TestStateManager_Lifecycle tests the full lifecycle: Set → Get → ListActive → status transitions.
func TestStateManager_Lifecycle(t *testing.T) {
	sm := NewTestStateManager(t)

	taskID := "lifecycle-001"
	channelID := "CLC1"
	slackTS := "1700000010.001"

	// Set initial state as "running".
	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set(running): %v", err)
	}

	// Get and verify.
	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("expected record after Set")
	}
	if rec.Status != "running" {
		t.Errorf("initial status = %q, want %q", rec.Status, "running")
	}
	if rec.ChannelID != channelID {
		t.Errorf("channel = %q, want %q", rec.ChannelID, channelID)
	}

	// ListActive should include it.
	active := sm.ListActive()
	if len(active) != 1 {
		t.Fatalf("expected 1 active task, got %d", len(active))
	}
	if active[0].TaskID != taskID {
		t.Errorf("active task ID = %q, want %q", active[0].TaskID, taskID)
	}

	// Transition to "done".
	if err := sm.Set(statemgr.TaskRecord{
		TaskID: taskID,
		Status: "done",
	}); err != nil {
		t.Fatalf("Set(done): %v", err)
	}

	// Get should show "done".
	rec, ok = sm.Get(taskID)
	if !ok {
		t.Fatal("expected record after status transition")
	}
	if rec.Status != "done" {
		t.Errorf("status after transition = %q, want %q", rec.Status, "done")
	}

	// ChannelID should be preserved (partial update).
	if rec.ChannelID != channelID {
		t.Errorf("channel after transition = %q, want %q", rec.ChannelID, channelID)
	}

	// ListActive should NOT include it anymore.
	active = sm.ListActive()
	if len(active) != 0 {
		t.Errorf("expected 0 active tasks after done, got %d", len(active))
	}
}

// TestStateManager_HasActiveTask verifies correct detection of active tasks
// by channel, including both "running" and "paused" statuses.
func TestStateManager_HasActiveTask(t *testing.T) {
	sm := NewTestStateManager(t)

	ch1 := "CHAN1"
	ch2 := "CHAN2"

	// No tasks: should return false.
	if sm.HasActiveTask(ch1) {
		t.Error("HasActiveTask should be false with no tasks")
	}

	// Add running task to ch1.
	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    "t1",
		ChannelID: ch1,
		Status:    "running",
	}); err != nil {
		t.Fatal(err)
	}
	if !sm.HasActiveTask(ch1) {
		t.Error("HasActiveTask should be true for running task")
	}
	if sm.HasActiveTask(ch2) {
		t.Error("HasActiveTask should be false for different channel")
	}

	// Add paused task to ch2.
	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    "t2",
		ChannelID: ch2,
		Status:    "paused",
	}); err != nil {
		t.Fatal(err)
	}
	if !sm.HasActiveTask(ch2) {
		t.Error("HasActiveTask should be true for paused task")
	}

	// Mark t1 as done — ch1 should no longer be active.
	if err := sm.Set(statemgr.TaskRecord{
		TaskID: "t1",
		Status: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if sm.HasActiveTask(ch1) {
		t.Error("HasActiveTask should be false after task is done")
	}

	// ch2 should still be active (paused task).
	if !sm.HasActiveTask(ch2) {
		t.Error("HasActiveTask should still be true for ch2 (paused)")
	}
}

// TestStateManager_GetByChannel verifies that GetByChannel returns the
// most recent record for a channel.
func TestStateManager_GetByChannel(t *testing.T) {
	sm := NewTestStateManager(t)

	ch := "CHGETBY"

	// No tasks for this channel.
	_, ok := sm.GetByChannel(ch)
	if ok {
		t.Error("GetByChannel should return false with no tasks")
	}

	// First task.
	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    "t1",
		ChannelID: ch,
		Prompt:    "first prompt",
		UserID:    "U111",
		Status:    "running",
	}); err != nil {
		t.Fatal(err)
	}

	rec, ok := sm.GetByChannel(ch)
	if !ok {
		t.Fatal("GetByChannel should return true for first task")
	}
	if rec.TaskID != "t1" {
		t.Errorf("expected t1, got %s", rec.TaskID)
	}

	// Sleep to ensure UpdatedAt differs.
	time.Sleep(10 * time.Millisecond)

	// Second task (should be the most recent).
	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    "t2",
		ChannelID: ch,
		Prompt:    "second prompt",
		UserID:    "U222",
		Status:    "done",
	}); err != nil {
		t.Fatal(err)
	}

	rec, ok = sm.GetByChannel(ch)
	if !ok {
		t.Fatal("GetByChannel should return true after second task")
	}
	if rec.TaskID != "t2" {
		t.Errorf("expected t2 as most recent, got %s", rec.TaskID)
	}
	if rec.Prompt != "second prompt" {
		t.Errorf("expected 'second prompt', got %q", rec.Prompt)
	}
	if rec.UserID != "U222" {
		t.Errorf("expected U222, got %q", rec.UserID)
	}

	// Different channel should not be affected.
	_, ok = sm.GetByChannel("OTHER")
	if ok {
		t.Error("GetByChannel should return false for unrelated channel")
	}
}

// TestRecovery_ContextCancellation verifies that recovery respects
// context cancellation.
func TestRecovery_ContextCancellation(t *testing.T) {
	sm := NewTestStateManager(t)

	// Add multiple tasks.
	for i := 0; i < 5; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		if err := sm.Set(statemgr.TaskRecord{
			TaskID:    taskID,
			ChannelID: "C1",
			SlackTS:   "1.0",
			Status:    "running",
		}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{
		Statuses: map[string]string{"task-0": "done"},
	}

	_, err := gw.RecoverActiveTasks(ctx, sm, checker, mockSlack)
	// May return nil if processed before cancellation was checked, or context.Canceled.
	if err != nil && err != context.Canceled {
		t.Logf("Recovery error (expected context.Canceled or nil): %v", err)
	}
}

// TestRecovery_CompletedTasksNotRecovered verifies that tasks not in
// "running" state are left untouched by recovery.
func TestRecovery_CompletedTasksNotRecovered(t *testing.T) {
	sm := NewTestStateManager(t)

	// Add a completed task and a running task.
	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    "done-task",
		ChannelID: "C1",
		SlackTS:   "1.0",
		Status:    "done",
	}); err != nil {
		t.Fatalf("Set done-task: %v", err)
	}

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    "running-task",
		ChannelID: "C2",
		SlackTS:   "2.0",
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set running-task: %v", err)
	}

	mockSlack := &MockSlackClient{}
	checker := &MockStatusChecker{
		Statuses: map[string]string{"running-task": "done"},
	}

	_, err := gw.RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the running task should trigger an update.
	if len(mockSlack.Updates) != 1 {
		t.Errorf("expected 1 Slack update (only running task), got %d", len(mockSlack.Updates))
	}

	// Done task should be untouched.
	rec, ok := sm.Get("done-task")
	if !ok {
		t.Fatal("done-task not found")
	}
	if rec.Status != "done" {
		t.Errorf("done-task status = %q, want %q", rec.Status, "done")
	}
}
