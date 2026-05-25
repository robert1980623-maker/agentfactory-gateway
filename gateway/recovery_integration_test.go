package gateway

import (
	"context"
	"testing"

	statemgr "github.com/agentfactory/gateway/state"
	"github.com/agentfactory/gateway/worker"
)

// TestGracefulShutdown_Integration verifies the full shutdown sequence:
// 1. Create tasks in "running" state
// 2. Call Stop() on the gateway
// 3. Verify tasks are marked as "interrupted"
func TestGracefulShutdown_Integration(t *testing.T) {
	path := t.TempDir() + "/state.json"
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate two running tasks.
	sm.Set(statemgr.TaskRecord{TaskID: "t1", ChannelID: "C01", Status: "running"})
	sm.Set(statemgr.TaskRecord{TaskID: "t2", ChannelID: "C02", Status: "running"})

	// Verify they are active.
	active := sm.ListActive()
	if len(active) != 2 {
		t.Fatalf("expected 2 active tasks, got %d", len(active))
	}

	// Create a mock gateway to test Stop().
	w := worker.NewPythonWorker("python3")
	sw := worker.NewStreamWorker("python3")
	g := &SlackGateway{
		worker:       w,
		streamWorker: sw,
		stateMgr:     sm,
	}

	// Call Stop().
	if err := g.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Verify tasks are now interrupted.
	active = sm.ListActive()
	if len(active) != 0 {
		t.Errorf("expected 0 active tasks after shutdown, got %d", len(active))
	}

	// Verify individual records show "interrupted".
	rec1, _ := sm.Get("t1")
	if rec1.Status != "interrupted" {
		t.Errorf("t1 status: got %q, want interrupted", rec1.Status)
	}
	rec2, _ := sm.Get("t2")
	if rec2.Status != "interrupted" {
		t.Errorf("t2 status: got %q, want interrupted", rec2.Status)
	}
}

// TestHasActiveTask_Concurrency verifies that concurrent task submission is blocked.
func TestHasActiveTask_Concurrency(t *testing.T) {
	path := t.TempDir() + "/state.json"
	sm, err := statemgr.NewStateManager(path)
	if err != nil {
		t.Fatal(err)
	}

	ch := "C01"

	// First task starts.
	sm.Set(statemgr.TaskRecord{TaskID: "t1", ChannelID: ch, Status: "running"})

	// HasActiveTask should block a second task.
	if !sm.HasActiveTask(ch) {
		t.Error("HasActiveTask should return true for running task")
	}

	// After task completes, should allow new task.
	sm.Set(statemgr.TaskRecord{TaskID: "t1", Status: "done"})
	if sm.HasActiveTask(ch) {
		t.Error("HasActiveTask should return false for done task")
	}
}
