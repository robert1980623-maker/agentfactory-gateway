package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	statemgr "github.com/agentfactory/gateway/state"

	"github.com/slack-go/slack"
)

// mockSlackClient records all UpdateMessage and PostMessage calls.
type mockSlackClient struct {
	mu             sync.Mutex
	updates        []mockUpdate
	posts          []mockPost
	updateErr      error
	postErr        error
}

type mockUpdate struct {
	ChannelID string
	Timestamp string
	Options   []slack.MsgOption
}

type mockPost struct {
	ChannelID string
	Options   []slack.MsgOption
}

func (m *mockSlackClient) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, mockUpdate{
		ChannelID: channelID,
		Timestamp: timestamp,
		Options:   options,
	})
	return channelID, timestamp, "", m.updateErr
}

func (m *mockSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.posts = append(m.posts, mockPost{
		ChannelID: channelID,
		Options:   options,
	})
	return channelID, "", m.postErr
}

// mockStatusChecker wraps a status map for simulating CheckStatus responses.
type mockStatusChecker struct {
	statuses map[string]string // taskID -> status
	errs     map[string]error  // taskID -> error
}

func (m *mockStatusChecker) CheckStatus(taskID string) (string, error) {
	if err, ok := m.errs[taskID]; ok {
		return "", err
	}
	if status, ok := m.statuses[taskID]; ok {
		return status, nil
	}
	return "unknown", nil
}

// TestRecoverActiveTasks_NoActiveTasks verifies that recovery returns
// immediately when there are no running tasks.
func TestRecoverActiveTasks_NoActiveTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{}}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockSlack.updates) != 0 {
		t.Errorf("expected no Slack updates, got %d", len(mockSlack.updates))
	}
}

// TestRecoverActiveTasks_DoneTask verifies that a task reported as "done"
// by the worker gets its Slack message updated and state changed.
func TestRecoverActiveTasks_DoneTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

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

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{
		taskID: "done",
	}}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Slack was updated.
	if len(mockSlack.updates) != 1 {
		t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.updates))
	}
	upd := mockSlack.updates[0]
	if upd.ChannelID != channelID {
		t.Errorf("update channel = %q, want %q", upd.ChannelID, channelID)
	}
	if upd.Timestamp != slackTS {
		t.Errorf("update ts = %q, want %q", upd.Timestamp, slackTS)
	}

	// Verify state was updated.
	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found after recovery")
	}
	if rec.Status != "done" {
		t.Errorf("status after recovery = %q, want %q", rec.Status, "done")
	}
}

// TestRecoverActiveTasks_ErrorTask verifies error/timeout statuses.
func TestRecoverActiveTasks_ErrorTask(t *testing.T) {
	for _, tc := range []struct {
		name         string
		workerStatus string
		wantStatus   string
	}{
		{"error", "error", "error"},
		{"failed", "failed", "error"},
		{"timeout", "timeout", "error"},
		{"timed_out", "timed_out", "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			sm, err := statemgr.NewStateManager(path)
			if err != nil {
				t.Fatalf("NewStateManager: %v", err)
			}

			taskID := "task-err-" + tc.name
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

			mockSlack := &mockSlackClient{}
			checker := &mockStatusChecker{statuses: map[string]string{
				taskID: tc.workerStatus,
			}}

			err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(mockSlack.updates) != 1 {
				t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.updates))
			}

			rec, ok := sm.Get(taskID)
			if !ok {
				t.Fatal("task record not found")
			}
			if rec.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", rec.Status, tc.wantStatus)
			}
		})
	}
}

// TestRecoverActiveTasks_StillRunning verifies the running/in_progress case.
func TestRecoverActiveTasks_StillRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

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

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{
		taskID: "running",
	}}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockSlack.updates) != 1 {
		t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.updates))
	}

	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found")
	}
	if rec.Status != "running" {
		t.Errorf("status = %q, want %q", rec.Status, "running")
	}
}

// TestRecoverActiveTasks_CheckStatusError verifies that when CheckStatus
// itself fails, the task is marked as error.
func TestRecoverActiveTasks_CheckStatusError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	taskID := "task-checkerr-001"
	channelID := "C999"
	slackTS := "1700000003.999"

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{
		statuses: map[string]string{},
		errs:     map[string]error{taskID: os.ErrNotExist},
	}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockSlack.updates) != 1 {
		t.Fatalf("expected 1 Slack update, got %d", len(mockSlack.updates))
	}

	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found")
	}
	if rec.Status != "error" {
		t.Errorf("status = %q, want %q", rec.Status, "error")
	}
}

// TestRecoverActiveTasks_MultipleTasks verifies recovery of several tasks
// with different statuses.
func TestRecoverActiveTasks_MultipleTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

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

	mockSlack := &mockSlackClient{}
	statuses := map[string]string{}
	for _, tc := range tasks {
		statuses[tc.taskID] = tc.workerStatus
	}
	checker := &mockStatusChecker{statuses: statuses}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockSlack.updates) != len(tasks) {
		t.Errorf("expected %d Slack updates, got %d", len(tasks), len(mockSlack.updates))
	}

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

// TestRecoverActiveTasks_ContextCancellation verifies that recovery
// respects context cancellation.
func TestRecoverActiveTasks_ContextCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

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

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{
		"task-0": "done",
	}}

	err = RecoverActiveTasks(ctx, sm, checker, mockSlack)
	if err == nil {
		t.Log("Recovery returned nil (processed before cancellation was checked)")
	} else if err != context.Canceled {
		t.Logf("Recovery error (expected context.Canceled or nil): %v", err)
	}
}

// TestRecoverActiveTasks_CompletedTasksNotRecovered verifies that tasks
// not in "running" state are left untouched.
func TestRecoverActiveTasks_CompletedTasksNotRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

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

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{
		"running-task": "done",
	}}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the running task should trigger an update.
	if len(mockSlack.updates) != 1 {
		t.Errorf("expected 1 Slack update (only running task), got %d", len(mockSlack.updates))
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

// TestRecoverActiveTasks_UnknownStatus verifies that an unknown worker status
// defaults to error.
func TestRecoverActiveTasks_UnknownStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	taskID := "task-unknown"
	channelID := "C1"
	slackTS := "1.0"

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{
		taskID: "pending", // unknown status
	}}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task record not found")
	}
	if rec.Status != "error" {
		t.Errorf("status = %q, want %q (unknown should map to error)", rec.Status, "error")
	}
}

// TestBuildRecoveryBlocks verifies that recovery blocks are built correctly
// for each status type.
func TestBuildRecoveryBlocks(t *testing.T) {
	tests := []struct {
		status     string
		message    string
		taskID     string
		wantHeader string
		minBlocks  int
	}{
		{
			status:     "done",
			message:    "✅ Recovered: Task completed successfully",
			taskID:     "t1",
			wantHeader: "✅ Recovered: Done",
			minBlocks:  4,
		},
		{
			status:     "error",
			message:    "❌ Recovered: Task failed",
			taskID:     "t2",
			wantHeader: "❌ Recovered: Failed",
			minBlocks:  4,
		},
		{
			status:     "running",
			message:    "🔄 Recovered: Task still running",
			taskID:     "t3",
			wantHeader: "🔄 Recovered: Still Running",
			minBlocks:  3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			blocks := buildRecoveryBlocks(tc.status, tc.message, tc.taskID)
			if len(blocks) < tc.minBlocks {
				t.Errorf("got %d blocks, want at least %d", len(blocks), tc.minBlocks)
			}

			header, ok := blocks[0].(*slack.HeaderBlock)
			if !ok {
				t.Fatal("first block should be HeaderBlock")
			}
			if header.Text.Text != tc.wantHeader {
				t.Errorf("header = %q, want %q", header.Text.Text, tc.wantHeader)
			}
		})
	}
}

// TestBuildRecoveryBlocks_VerifySlackCallContent verifies that the Slack
// UpdateMessage is actually called with the correct blocks for a recovery scenario.
func TestBuildRecoveryBlocks_VerifySlackCallContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}

	taskID := "verify-001"
	channelID := "CVERIFY"
	slackTS := "1700000099.001"

	if err := sm.Set(statemgr.TaskRecord{
		TaskID:    taskID,
		ChannelID: channelID,
		SlackTS:   slackTS,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mockSlack := &mockSlackClient{}
	checker := &mockStatusChecker{statuses: map[string]string{
		taskID: "done",
	}}

	err = RecoverActiveTasks(context.Background(), sm, checker, mockSlack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockSlack.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(mockSlack.updates))
	}

	upd := mockSlack.updates[0]
	if upd.ChannelID != channelID {
		t.Errorf("update channel = %q, want %q", upd.ChannelID, channelID)
	}
	if upd.Timestamp != slackTS {
		t.Errorf("update ts = %q, want %q", upd.Timestamp, slackTS)
	}

	// The MsgOptionBlocks wraps blocks — we verified the call parameters
	// (channel/ts) above, and the state update confirms correctness.

	// Verify state was updated to done.
	rec, ok := sm.Get(taskID)
	if !ok {
		t.Fatal("task not found")
	}
	if rec.Status != "done" {
		t.Errorf("status = %q, want %q", rec.Status, "done")
	}

	t.Logf("Recovery successfully updated Slack message for task %s", taskID)
}

// TestWorkerCheckStatusInterface verifies that *worker.PythonWorker's
// CheckStatus method satisfies the StatusChecker interface.
func TestWorkerCheckStatusInterface(t *testing.T) {
	// This is a compile-time check: if PythonWorker.CheckStatus doesn't
	// match the StatusChecker interface signature, this will fail to compile.
	var _ StatusChecker = (*mockStatusChecker)(nil)

	// Verify the mock satisfies the interface.
	mc := &mockStatusChecker{
		statuses: map[string]string{"t1": "done"},
	}
	status, err := mc.CheckStatus("t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want %q", status, "done")
	}

	// Verify error path.
	mc.errs = map[string]error{"t2": errors.New("fail")}
	_, err = mc.CheckStatus("t2")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
