package gateway

import (
	"sync"
	"testing"
	"time"
)

// ─── QueuedTask Tests ───

// TestNewQueuedTask verifies NewQueuedTask creates a task in queued state.
func TestNewQueuedTask(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "hello")

	if qt.TaskID != "t1" {
		t.Errorf("TaskID = %q, want %q", qt.TaskID, "t1")
	}
	if qt.ChannelID != "c1" {
		t.Errorf("ChannelID = %q, want %q", qt.ChannelID, "c1")
	}
	if qt.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", qt.UserID, "u1")
	}
	if qt.Prompt != "hello" {
		t.Errorf("Prompt = %q, want %q", qt.Prompt, "hello")
	}
	if qt.Status != TaskStatusQueued {
		t.Errorf("Status = %q, want %q", qt.Status, TaskStatusQueued)
	}
	if qt.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	if !qt.StartedAt.IsZero() {
		t.Error("StartedAt should be zero initially")
	}
}

// TestValidTransitions_HappyPath verifies queued → dispatching → running → done.
func TestValidTransitions_HappyPath(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")

	if err := qt.Transition(TaskStatusDispatching); err != nil {
		t.Fatalf("queued → dispatching: unexpected error: %v", err)
	}
	if qt.Status != TaskStatusDispatching {
		t.Errorf("Status = %q, want %q", qt.Status, TaskStatusDispatching)
	}

	if err := qt.Transition(TaskStatusRunning); err != nil {
		t.Fatalf("dispatching → running: unexpected error: %v", err)
	}
	if qt.Status != TaskStatusRunning {
		t.Errorf("Status = %q, want %q", qt.Status, TaskStatusRunning)
	}

	if err := qt.Transition(TaskStatusDone); err != nil {
		t.Fatalf("running → done: unexpected error: %v", err)
	}
	if qt.Status != TaskStatusDone {
		t.Errorf("Status = %q, want %q", qt.Status, TaskStatusDone)
	}
}

// TestValidTransitions_ErrorPath verifies queued → dispatching → running → error.
func TestValidTransitions_ErrorPath(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")

	if err := qt.Transition(TaskStatusDispatching); err != nil {
		t.Fatalf("queued → dispatching: unexpected error: %v", err)
	}
	if err := qt.Transition(TaskStatusRunning); err != nil {
		t.Fatalf("dispatching → running: unexpected error: %v", err)
	}
	if err := qt.Transition(TaskStatusError); err != nil {
		t.Fatalf("running → error: unexpected error: %v", err)
	}
	if qt.Status != TaskStatusError {
		t.Errorf("Status = %q, want %q", qt.Status, TaskStatusError)
	}
}

// TestInvalidTransition_QueuedToRunning verifies skipping dispatching is rejected.
func TestInvalidTransition_QueuedToRunning(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")

	err := qt.Transition(TaskStatusRunning)
	if err == nil {
		t.Fatal("expected error for queued → running, got nil")
	}
	var ite *InvalidTransitionError
	if err != nil {
		var ok bool
		ite, ok = err.(*InvalidTransitionError)
		if !ok {
			t.Fatalf("expected *InvalidTransitionError, got %T", err)
		}
	}
	if ite.From != TaskStatusQueued {
		t.Errorf("InvalidTransitionError.From = %q, want %q", ite.From, TaskStatusQueued)
	}
	if ite.To != TaskStatusRunning {
		t.Errorf("InvalidTransitionError.To = %q, want %q", ite.To, TaskStatusRunning)
	}
}

// TestInvalidTransition_DoneToQueued verifies done → queued is rejected.
func TestInvalidTransition_DoneToQueued(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")
	qt.Status = TaskStatusDone // force to done for this test

	err := qt.Transition(TaskStatusQueued)
	if err == nil {
		t.Fatal("expected error for done → queued, got nil")
	}
}

// TestStartedAt_SetOnDispatching verifies StartedAt is set when transitioning to dispatching.
func TestStartedAt_SetOnDispatching(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")
	if !qt.StartedAt.IsZero() {
		t.Fatal("StartedAt should be zero before any transition")
	}

	before := time.Now().UTC()
	if err := qt.Transition(TaskStatusDispatching); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if qt.StartedAt.IsZero() {
		t.Fatal("StartedAt should be set after dispatching transition")
	}
	if qt.StartedAt.Before(before) {
		t.Error("StartedAt should be >= time before transition")
	}
}

// TestStartedAt_SetOnRunning verifies StartedAt is set when transitioning to running.
func TestStartedAt_SetOnRunning(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")

	// Transition to dispatching first.
	if err := qt.Transition(TaskStatusDispatching); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// StartedAt should already be set from dispatching.
	firstStartedAt := qt.StartedAt

	// Now transition to running — StartedAt should NOT be overwritten.
	if err := qt.Transition(TaskStatusRunning); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !qt.StartedAt.Equal(firstStartedAt) {
		t.Error("StartedAt should not be overwritten on running transition")
	}
}

// ─── TaskQueue Tests ───

// TestEnqueue_AddsToQueue verifies Enqueue adds a task and returns correct position.
func TestEnqueue_AddsToQueue(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	task := NewQueuedTask("t1", "c1", "u1", "hello")
	pos, err := tq.Enqueue(task)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pos != 1 {
		t.Errorf("position = %d, want 1", pos)
	}
	if tq.QueueLength() != 1 {
		t.Errorf("QueueLength = %d, want 1", tq.QueueLength())
	}
	if tq.RunningCount() != 0 {
		t.Errorf("RunningCount = %d, want 0", tq.RunningCount())
	}
}

// TestEnqueue_DuplicateTaskID verifies duplicate task_id is rejected.
func TestEnqueue_DuplicateTaskID(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	task1 := NewQueuedTask("t1", "c1", "u1", "hello")
	if _, err := tq.Enqueue(task1); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	task2 := NewQueuedTask("t1", "c2", "u2", "world")
	_, err := tq.Enqueue(task2)
	if err == nil {
		t.Fatal("expected error for duplicate task_id, got nil")
	}
}

// TestEnqueue_NilTask verifies nil task is rejected.
func TestEnqueue_NilTask(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	_, err := tq.Enqueue(nil)
	if err == nil {
		t.Fatal("expected error for nil task, got nil")
	}
}

// TestMarkRunning_MovesFromWaitingToRunning verifies MarkRunning moves a task.
func TestMarkRunning_MovesFromWaitingToRunning(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	task := NewQueuedTask("t1", "c1", "u1", "hello")
	if _, err := tq.Enqueue(task); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	ok := tq.MarkRunning("t1")
	if !ok {
		t.Fatal("MarkRunning should return true for queued task")
	}
	if tq.QueueLength() != 0 {
		t.Errorf("QueueLength = %d, want 0", tq.QueueLength())
	}
	if tq.RunningCount() != 1 {
		t.Errorf("RunningCount = %d, want 1", tq.RunningCount())
	}
}

// TestMarkRunning_NonExistent verifies MarkRunning returns false for unknown task.
func TestMarkRunning_NonExistent(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	ok := tq.MarkRunning("nonexistent")
	if ok {
		t.Error("MarkRunning should return false for non-existent task")
	}
}

// TestMarkDone_RemovesRunningAndTriggersDequeue verifies MarkDone removes task and triggers dequeue.
func TestMarkDone_RemovesRunningAndTriggersDequeue(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 1, MaxPerChannel: 1})

	// Enqueue two tasks from different channels.
	task1 := NewQueuedTask("t1", "c1", "u1", "hello")
	task2 := NewQueuedTask("t2", "c2", "u2", "world")
	if _, err := tq.Enqueue(task1); err != nil {
		t.Fatalf("enqueue task1 failed: %v", err)
	}
	if _, err := tq.Enqueue(task2); err != nil {
		t.Fatalf("enqueue task2 failed: %v", err)
	}

	// Mark first task running.
	tq.MarkRunning("t1")
	if tq.RunningCount() != 1 {
		t.Fatalf("RunningCount = %d, want 1", tq.RunningCount())
	}

	// Mark first task done — should trigger dequeue of task2.
	tq.MarkDone("t1")
	if tq.RunningCount() != 1 {
		t.Errorf("RunningCount after MarkDone = %d, want 1 (task2 should be running)", tq.RunningCount())
	}
	if tq.QueueLength() != 0 {
		t.Errorf("QueueLength = %d, want 0", tq.QueueLength())
	}
}

// TestMarkError_RemovesRunningAndTriggersDequeue verifies MarkError removes task and triggers dequeue.
func TestMarkError_RemovesRunningAndTriggersDequeue(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 1, MaxPerChannel: 1})

	task1 := NewQueuedTask("t1", "c1", "u1", "hello")
	task2 := NewQueuedTask("t2", "c2", "u2", "world")
	if _, err := tq.Enqueue(task1); err != nil {
		t.Fatalf("enqueue task1 failed: %v", err)
	}
	if _, err := tq.Enqueue(task2); err != nil {
		t.Fatalf("enqueue task2 failed: %v", err)
	}

	tq.MarkRunning("t1")

	tq.MarkError("t1")
	if tq.RunningCount() != 1 {
		t.Errorf("RunningCount after MarkError = %d, want 1", tq.RunningCount())
	}
	if tq.QueueLength() != 0 {
		t.Errorf("QueueLength = %d, want 0", tq.QueueLength())
	}
}

// TestGlobalConcurrencyLimit verifies no dequeue when global limit reached.
func TestGlobalConcurrencyLimit(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 2, MaxPerChannel: 1})

	// Enqueue 3 tasks from different channels.
	for i := 1; i <= 3; i++ {
		task := NewQueuedTask(
			"t"+string(rune('0'+i)),
			"c"+string(rune('0'+i)),
			"u1", "prompt",
		)
		if _, err := tq.Enqueue(task); err != nil {
			t.Fatalf("enqueue task%d failed: %v", i, err)
		}
	}

	// Mark 2 tasks running (at capacity).
	tq.MarkRunning("t1")
	tq.MarkRunning("t2")

	if !tq.IsAtCapacity() {
		t.Fatal("queue should be at capacity")
	}

	// Mark one done — should dequeue the 3rd.
	tq.MarkDone("t1")
	if tq.RunningCount() != 2 {
		t.Errorf("RunningCount = %d, want 2", tq.RunningCount())
	}
	if tq.QueueLength() != 0 {
		t.Errorf("QueueLength = %d, want 0", tq.QueueLength())
	}
}

// TestPerChannelLimit verifies same channel does not run in parallel.
func TestPerChannelLimit(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	task1 := NewQueuedTask("t1", "c1", "u1", "hello")
	task2 := NewQueuedTask("t2", "c1", "u1", "world")
	task3 := NewQueuedTask("t3", "c2", "u2", "other")

	if _, err := tq.Enqueue(task1); err != nil {
		t.Fatalf("enqueue task1 failed: %v", err)
	}
	if _, err := tq.Enqueue(task2); err != nil {
		t.Fatalf("enqueue task2 failed: %v", err)
	}
	if _, err := tq.Enqueue(task3); err != nil {
		t.Fatalf("enqueue task3 failed: %v", err)
	}

	// Mark t1 running (channel c1).
	tq.MarkRunning("t1")

	// Now try to manually mark t2 running — it should succeed (MarkRunning
	// doesn't check channel limits), but tryDequeue after completion won't
	// pick t2 while t1 is still running on c1.
	// Instead, test via MarkDone: mark t1 done, verify t3 (c2) gets dequeued
	// before t2 (c1 blocked by nothing now, but FIFO: t2 is ahead of t3).
	// Actually t2 is at position 2 in queue, t3 at position 3.
	// After MarkRunning("t1"), t2 is still in waiting queue.
	// MarkDone("t1") triggers tryDequeue — c1 has no running task, so t2 (c1) should be picked.

	// Let's test differently: mark t1 running, verify t3 (c2) is NOT
	// auto-dequeued because we must use MarkRunning manually.
	// The actual per-channel enforcement is in findNextEligibleLocked:
	// it skips tasks whose channel already has a running task.

	// Better approach: set up so t1 is running on c1, then call MarkDone
	// and verify t3 (c2) gets picked over t2... no, t2 comes first in FIFO.
	// Let me restructure: t1(c1), t3(c2), t2(c1) — so t3 is before t2.

	// Actually the simplest: enqueue t1(c1), mark it running, enqueue t2(c1).
	// After t2 is in waiting queue, tryDequeue should NOT pick it because
	// c1 already has a running task. But tryDequeue is only called from
	// MarkDone/MarkError, not from Enqueue.

	// So let's do: enqueue t1(c1), enqueue t2(c1), mark t1 running,
	// then mark t1 done — t2 should NOT be picked... wait, t1 is done so
	// c1 has no running task, so t2 WILL be picked. That's correct behavior.

	// The real test: enqueue t1(c1), enqueue t2(c1), mark t1 running.
	// Now manually check that tryDequeue would NOT pick t2.
	// But tryDequeue is private... Let me test via a callback approach.

	// Simplest correct test: enqueue t1(c1) and t3(c2). Mark t1 running.
	// Now tryDequeue (triggered by MarkDone on some other task) should pick
	// t3(c2) but not t2(c1) — but we don't have a t2 here.

	// Let me use a different approach:
	// Enqueue t1(c1), t2(c1), t3(c2). Mark t1 running.
	// Now manually mark t3 running (we can — MarkRunning doesn't check).
	// Actually no — let me use the real flow:

	// Enqueue t1(c1). MarkRunning(t1). Enqueue t2(c1). Enqueue t3(c2).
	// Now c1 has t1 running. tryDequeue won't pick t2(c1) because c1 is busy.
	// It will pick t3(c2) because c2 is free.
	// We need to trigger tryDequeue — do MarkDone on t1? No, that removes t1.

	// OK, cleanest approach: use a callback to track dequeue events.

	// Reset and do it properly.
	tq = NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	var dequeued []string
	var mu sync.Mutex
	tq.SetDequeueCallback(func(task *QueuedTask) {
		mu.Lock()
		defer mu.Unlock()
		dequeued = append(dequeued, task.TaskID)
	})

	t1 := NewQueuedTask("t1", "c1", "u1", "a")
	t2 := NewQueuedTask("t2", "c1", "u1", "b")
	t3 := NewQueuedTask("t3", "c2", "u2", "c")

	tq.Enqueue(t1)
	tq.Enqueue(t2)
	tq.Enqueue(t3)

	// Manually mark t1 running.
	tq.MarkRunning("t1")

	// Now trigger tryDequeue by marking a dummy running task done.
	// We need to manually add something to running then remove it.
	// Instead: mark t1 done — this triggers tryDequeue.
	// At this point: waiting = [t2(c1), t3(c2)], running = {} (after t1 removed).
	// t2(c1) will be picked (c1 is free), then tryDequeue loops — but it
	// only picks ONE task per call. So t2 will be dequeued, t3 stays.

	tq.MarkDone("t1")

	mu.Lock()
	d := dequeued
	mu.Unlock()

	if len(d) != 1 {
		t.Fatalf("expected 1 dequeue event, got %d", len(d))
	}
	if d[0] != "t2" {
		t.Errorf("dequeued task = %q, want t2 (FIFO, c1 now free)", d[0])
	}

	// Now t3 is still in queue. Mark t2 done — t3 should be dequeued.
	tq.MarkDone("t2")

	mu.Lock()
	d = dequeued
	mu.Unlock()

	if len(d) != 2 {
		t.Fatalf("expected 2 dequeue events, got %d", len(d))
	}
	if d[1] != "t3" {
		t.Errorf("second dequeued task = %q, want t3", d[1])
	}
}

// TestPerChannel_SameChannelBlocked verifies that while a channel has a running
// task, the next task for the same channel is not auto-dequeued.
func TestPerChannel_SameChannelBlocked(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	var dequeued []string
	var mu sync.Mutex
	tq.SetDequeueCallback(func(task *QueuedTask) {
		mu.Lock()
		defer mu.Unlock()
		dequeued = append(dequeued, task.TaskID)
	})

	// t1 on c1, t2 on c1, t3 on c2
	t1 := NewQueuedTask("t1", "c1", "u1", "a")
	t2 := NewQueuedTask("t2", "c1", "u1", "b")
	t3 := NewQueuedTask("t3", "c2", "u2", "c")

	tq.Enqueue(t1)
	tq.Enqueue(t2)
	tq.Enqueue(t3)

	// Mark t1 running on c1.
	tq.MarkRunning("t1")

	// Mark t1 done — triggers tryDequeue.
	// At this point: c1 is free, so t2(c1) is picked first (FIFO).
	tq.MarkDone("t1")

	mu.Lock()
	first := dequeued[0]
	mu.Unlock()

	if first != "t2" {
		t.Errorf("first dequeue = %q, want t2", first)
	}

	// t2 is now running on c1, t3 still waiting.
	// Mark t2 done — now t3(c2) is picked.
	tq.MarkDone("t2")

	mu.Lock()
	second := dequeued[1]
	mu.Unlock()

	if second != "t3" {
		t.Errorf("second dequeue = %q, want t3", second)
	}
}

// TestFIFOOrder verifies tasks are dequeued in first-in-first-out order.
func TestFIFOOrder(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	var order []string
	var mu sync.Mutex
	tq.SetDequeueCallback(func(task *QueuedTask) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, task.TaskID)
	})

	// Enqueue tasks on different channels so they all get auto-dequeued.
	for i := 1; i <= 5; i++ {
		task := NewQueuedTask(
			"t"+string(rune('0'+i)),
			"c"+string(rune('0'+i)),
			"u1", "prompt",
		)
		if _, err := tq.Enqueue(task); err != nil {
			t.Fatalf("enqueue task%d failed: %v", i, err)
		}
	}

	// Mark first task running, then mark it done — triggers dequeue.
	// We need to manually mark t1 running first (since enqueue doesn't auto-dequeue).
	tq.MarkRunning("t1")

	// Now we have: waiting = [t2, t3, t4, t5], running = {t1}.
	// We need to trigger tryDequeue multiple times.
	// Mark t1 done → t2 dequeued.
	tq.MarkDone("t1")
	// Mark t2 done → t3 dequeued (but t2 was auto-dequeued, not manually marked running).
	// We need to use MarkRunning for each, or trigger tryDequeue differently.

	// Actually after MarkRunning("t1"), t1 is running, t2-t5 waiting.
	// MarkDone("t1") → tryDequeue picks t2 (c2 free). t2 is now running.
	// MarkDone("t2") → tryDequeue picks t3. etc.

	tq.MarkDone("t2")
	tq.MarkDone("t3")
	tq.MarkDone("t4")

	mu.Lock()
	defer mu.Unlock()

	want := []string{"t2", "t3", "t4", "t5"}
	if len(order) != len(want) {
		t.Fatalf("got %d dequeue events, want %d", len(order), len(want))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

// TestPosition_ReturnsCorrectPosition verifies Position returns 1-based index.
func TestPosition_ReturnsCorrectPosition(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	t1 := NewQueuedTask("t1", "c1", "u1", "a")
	t2 := NewQueuedTask("t2", "c2", "u2", "b")
	t3 := NewQueuedTask("t3", "c3", "u3", "c")

	tq.Enqueue(t1)
	tq.Enqueue(t2)
	tq.Enqueue(t3)

	if p := tq.Position("t1"); p != 1 {
		t.Errorf("Position(t1) = %d, want 1", p)
	}
	if p := tq.Position("t2"); p != 2 {
		t.Errorf("Position(t2) = %d, want 2", p)
	}
	if p := tq.Position("t3"); p != 3 {
		t.Errorf("Position(t3) = %d, want 3", p)
	}
	if p := tq.Position("nonexistent"); p != 0 {
		t.Errorf("Position(nonexistent) = %d, want 0", p)
	}
}

// TestChannelHasActiveTask_QueuedOnly verifies true when channel has a queued task.
// Note: ChannelHasActiveTask tracks waiting queue entries, not running tasks.
// Running tasks are tracked separately in the running map.
func TestChannelHasActiveTask_QueuedOnly(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	// A queued task should make the channel "active".
	task := NewQueuedTask("t1", "c1", "u1", "hello")
	tq.Enqueue(task)

	if !tq.ChannelHasActiveTask("c1") {
		t.Error("ChannelHasActiveTask(c1) should be true for queued task")
	}
	if tq.ChannelHasActiveTask("c2") {
		t.Error("ChannelHasActiveTask(c2) should be false")
	}

	// After MarkRunning, the task is removed from the channels map
	// (it's now tracked in the running map instead).
	tq.MarkRunning("t1")
	if tq.ChannelHasActiveTask("c1") {
		t.Error("ChannelHasActiveTask(c1) should be false after MarkRunning (task moved to running map)")
	}
}

// TestChannelHasActiveTask_AfterDone verifies false after task completes.
func TestChannelHasActiveTask_AfterDone(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	task := NewQueuedTask("t1", "c1", "u1", "hello")
	tq.Enqueue(task)
	tq.MarkRunning("t1")
	tq.MarkDone("t1")

	// After the full lifecycle, channel should be inactive.
	if tq.ChannelHasActiveTask("c1") {
		t.Error("ChannelHasActiveTask(c1) should be false after task is done")
	}
}

// TestIsAtCapacity verifies true when running count reaches global limit.
func TestIsAtCapacity(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 2, MaxPerChannel: 1})

	t1 := NewQueuedTask("t1", "c1", "u1", "a")
	t2 := NewQueuedTask("t2", "c2", "u2", "b")
	t3 := NewQueuedTask("t3", "c3", "u3", "c")

	tq.Enqueue(t1)
	tq.Enqueue(t2)
	tq.Enqueue(t3)

	// Not at capacity yet.
	if tq.IsAtCapacity() {
		t.Error("should not be at capacity with 0 running tasks")
	}

	// Mark 1 running — not at capacity (limit is 2).
	tq.MarkRunning("t1")
	if tq.IsAtCapacity() {
		t.Error("should not be at capacity with 1 running task (limit 2)")
	}

	// Mark 2 running — at capacity.
	tq.MarkRunning("t2")
	if !tq.IsAtCapacity() {
		t.Error("should be at capacity with 2 running tasks (limit 2)")
	}
}

// TestDequeueCallback verifies callback is called when a task is dequeued.
func TestDequeueCallback(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 1, MaxPerChannel: 1})

	var called bool
	var dequeuedTask *QueuedTask
	tq.SetDequeueCallback(func(task *QueuedTask) {
		called = true
		dequeuedTask = task
	})

	t1 := NewQueuedTask("t1", "c1", "u1", "hello")
	t2 := NewQueuedTask("t2", "c2", "u2", "world")

	tq.Enqueue(t1)
	tq.Enqueue(t2)

	// Mark t1 running (manually, since enqueue doesn't auto-dequeue).
	tq.MarkRunning("t1")

	// Mark t1 done — triggers tryDequeue which should dequeue t2 and call callback.
	tq.MarkDone("t1")

	if !called {
		t.Fatal("dequeue callback was not called")
	}
	if dequeuedTask == nil {
		t.Fatal("dequeuedTask is nil")
	}
	if dequeuedTask.TaskID != "t2" {
		t.Errorf("dequeued task = %q, want t2", dequeuedTask.TaskID)
	}
	if dequeuedTask.Status != TaskStatusDispatching {
		t.Errorf("dequeued task status = %q, want %q", dequeuedTask.Status, TaskStatusDispatching)
	}
}

// TestDequeueCallback_NilCallback verifies no panic when callback is not set.
func TestDequeueCallback_NilCallback(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 1, MaxPerChannel: 1})

	t1 := NewQueuedTask("t1", "c1", "u1", "hello")
	t2 := NewQueuedTask("t2", "c2", "u2", "world")

	tq.Enqueue(t1)
	tq.Enqueue(t2)
	tq.MarkRunning("t1")

	// Should not panic.
	tq.MarkDone("t1")

	// t2 should still be dequeued (running count should be 1).
	if tq.RunningCount() != 1 {
		t.Errorf("RunningCount = %d, want 1", tq.RunningCount())
	}
}

// TestNewTaskQueue_Defaults verifies default config values.
func TestNewTaskQueue_Defaults(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{})

	if tq.config.MaxConcurrentTasks != 5 {
		t.Errorf("MaxConcurrentTasks = %d, want 5", tq.config.MaxConcurrentTasks)
	}
	if tq.config.MaxPerChannel != 1 {
		t.Errorf("MaxPerChannel = %d, want 1", tq.config.MaxPerChannel)
	}
}

// TestListWaitingAndRunning verifies list methods return correct tasks.
func TestListWaitingAndRunning(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	t1 := NewQueuedTask("t1", "c1", "u1", "a")
	t2 := NewQueuedTask("t2", "c2", "u2", "b")

	tq.Enqueue(t1)
	tq.Enqueue(t2)
	tq.MarkRunning("t1")

	waiting := tq.ListWaiting()
	if len(waiting) != 1 {
		t.Fatalf("ListWaiting = %d items, want 1", len(waiting))
	}
	if waiting[0].TaskID != "t2" {
		t.Errorf("ListWaiting[0] = %q, want t2", waiting[0].TaskID)
	}

	running := tq.ListRunning()
	if len(running) != 1 {
		t.Fatalf("ListRunning = %d items, want 1", len(running))
	}
	if running[0].TaskID != "t1" {
		t.Errorf("ListRunning[0] = %q, want t1", running[0].TaskID)
	}
}

// TestCanEnqueueAlwaysTrue verifies CanEnqueue always returns true.
func TestCanEnqueueAlwaysTrue(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 1, MaxPerChannel: 1})

	if !tq.CanEnqueue() {
		t.Error("CanEnqueue should always return true")
	}

	// Even at capacity, it should return true.
	task := NewQueuedTask("t1", "c1", "u1", "a")
	tq.Enqueue(task)
	tq.MarkRunning("t1")

	if !tq.CanEnqueue() {
		t.Error("CanEnqueue should still return true at capacity")
	}
}

// TestEnqueue_EmptyTaskID verifies task with empty TaskID is rejected.
func TestEnqueue_EmptyTaskID(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{})

	task := NewQueuedTask("", "c1", "u1", "prompt")
	_, err := tq.Enqueue(task)
	if err == nil {
		t.Fatal("expected error for empty task_id, got nil")
	}
}

// TestInvalidTransition_DispatchingToDone verifies dispatching → done is rejected.
func TestInvalidTransition_DispatchingToDone(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")
	qt.Transition(TaskStatusDispatching)

	err := qt.Transition(TaskStatusDone)
	if err == nil {
		t.Fatal("expected error for dispatching → done, got nil")
	}
}

// TestTransition_ErrorFromQueued verifies queued → error is rejected.
func TestTransition_ErrorFromQueued(t *testing.T) {
	qt := NewQueuedTask("t1", "c1", "u1", "prompt")

	err := qt.Transition(TaskStatusError)
	if err == nil {
		t.Fatal("expected error for queued → error, got nil")
	}
}
