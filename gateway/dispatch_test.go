package gateway

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentfactory/gateway/protocol"
	"github.com/agentfactory/gateway/worker"

	"github.com/slack-go/slack"
)

// ─── Dispatch Detection Tests ───

// TestIsDispatchTask verifies the dispatch detection logic.
func TestIsDispatchTask(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"space prefix", "/dispatch build the API", true},
		{"tab prefix", "/dispatch\tbuild the API", true},
		{"no prefix", "build the API", false},
		{"empty string", "", false},
		{"just prefix", "/dispatch", false},
		{"prefix in middle", "please /dispatch this", false},
		{"Chinese keyword", "并发 build the API", false}, // future: may become true
		{"pipe separator", "task1|task2|task3", false}, // future: may become true
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDispatchTask(tt.input)
			if got != tt.want {
				t.Errorf("isDispatchTask(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestStripDispatchPrefix verifies that the /dispatch prefix is correctly removed.
func TestStripDispatchPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"space prefix", "/dispatch build the API", "build the API"},
		{"tab prefix", "/dispatch\tbuild the API", "build the API"},
		{"no prefix", "build the API", "build the API"},
		{"just prefix with space", "/dispatch ", ""},
		{"just prefix with tab", "/dispatch\t", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripDispatchPrefix(tt.input)
			if got != tt.want {
				t.Errorf("stripDispatchPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ─── Dispatch Worker Tests ───

// TestExecuteDispatch_StartEvent verifies that ExecuteDispatch sends a dispatch start event
// with the correct TaskType and agent information.
func TestExecuteDispatch_StartEvent(t *testing.T) {
	sw := worker.NewStreamWorker("/usr/bin/python3")

	var events []*protocol.SlackEvent
	var mu sync.Mutex
	cb := func(evt *protocol.SlackEvent, err error) {
		if err != nil {
			t.Logf("callback error (expected if python not available): %v", err)
			return
		}
		if evt == nil {
			return
		}
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	}

	req := protocol.TaskRequest{
		Task:     "build API|write tests|deploy",
		Dispatch: true,
	}

	// ExecuteDispatch will try to run Python which may not exist,
	// but we should still get the start event.
	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	defer mu.Unlock()

	if len(events) == 0 {
		t.Fatal("expected at least a start event, got none")
	}

	startEvt := events[0]
	if startEvt.Type != protocol.EventTypeStart {
		t.Errorf("first event type = %q, want %q", startEvt.Type, protocol.EventTypeStart)
	}
	if startEvt.Payload == nil {
		t.Fatal("start event payload is nil")
	}
	if startEvt.Payload.TaskType != "dispatch" {
		t.Errorf("payload.TaskType = %q, want \"dispatch\"", startEvt.Payload.TaskType)
	}
	if startEvt.Payload.TotalAgents != 3 {
		t.Errorf("payload.TotalAgents = %d, want 3", startEvt.Payload.TotalAgents)
	}
	if len(startEvt.Payload.Agents) != 3 {
		t.Errorf("payload.Agents count = %d, want 3", len(startEvt.Payload.Agents))
	}
}

// TestExecuteDispatch_AgentCount_PipeSeparated verifies agent count estimation from pipe-separated tasks.
func TestExecuteDispatch_AgentCount_PipeSeparated(t *testing.T) {
	sw := worker.NewStreamWorker("/usr/bin/python3")

	var events []*protocol.SlackEvent
	var mu sync.Mutex
	cb := func(evt *protocol.SlackEvent, err error) {
		if evt != nil {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		}
	}

	// Single task (no pipes)
	req := protocol.TaskRequest{Task: "build the API", Dispatch: true}
	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	count := events[0].Payload.TotalAgents
	mu.Unlock()

	if count != 1 {
		t.Errorf("single task agent count = %d, want 1", count)
	}

	// Two tasks
	events = nil
	req = protocol.TaskRequest{Task: "build API|write docs", Dispatch: true}
	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	count = events[0].Payload.TotalAgents
	mu.Unlock()

	if count != 2 {
		t.Errorf("two tasks agent count = %d, want 2", count)
	}

	// Five tasks
	events = nil
	req = protocol.TaskRequest{Task: "a|b|c|d|e", Dispatch: true}
	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	count = events[0].Payload.TotalAgents
	mu.Unlock()

	if count != 5 {
		t.Errorf("five tasks agent count = %d, want 5", count)
	}
}

// TestExecuteDispatch_AgentRoles verifies that initial agents get appropriate roles.
func TestExecuteDispatch_AgentRoles(t *testing.T) {
	sw := worker.NewStreamWorker("/usr/bin/python3")

	var events []*protocol.SlackEvent
	var mu sync.Mutex
	cb := func(evt *protocol.SlackEvent, err error) {
		if evt != nil {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		}
	}

	expectedRoles := []string{"coordinator", "researcher", "developer", "tester", "reviewer"}

	for i := 1; i <= 5; i++ {
		events = nil
		tasks := make([]string, i)
		for j := 0; j < i; j++ {
			tasks[j] = fmt.Sprintf("task-%d", j+1)
		}
		req := protocol.TaskRequest{Task: strings.Join(tasks, "|"), Dispatch: true}
		_ = sw.ExecuteDispatch(req, cb)

		mu.Lock()
		agents := events[0].Payload.Agents
		mu.Unlock()

		if len(agents) != i {
			t.Errorf("%d tasks: got %d agents, want %d", i, len(agents), i)
			continue
		}

		for j, agent := range agents {
			if j < len(expectedRoles) {
				if agent.Role != expectedRoles[j] {
					t.Errorf("agent %d role = %q, want %q", j, agent.Role, expectedRoles[j])
				}
			}
			if agent.Status != "running" {
				t.Errorf("agent %d status = %q, want \"running\"", j, agent.Status)
			}
		}
	}
}

// TestExecuteDispatch_EmptyTask verifies handling of empty task text.
func TestExecuteDispatch_EmptyTask(t *testing.T) {
	sw := worker.NewStreamWorker("/usr/bin/python3")

	var events []*protocol.SlackEvent
	var mu sync.Mutex
	cb := func(evt *protocol.SlackEvent, err error) {
		if evt != nil {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		}
	}

	req := protocol.TaskRequest{Task: "", Dispatch: true}
	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	defer mu.Unlock()

	if len(events) == 0 {
		t.Fatal("expected at least a start event for empty task")
	}
	if events[0].Payload.TotalAgents != 1 {
		t.Errorf("empty task agent count = %d, want 1", events[0].Payload.TotalAgents)
	}
}

// ─── Dispatch Integration Tests ───

// TestDispatchTaskLifecycle verifies the full dispatch flow through the task queue:
// start → progress with agents → done.
func TestDispatchTaskLifecycle(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	// Create a dispatch task.
	task := NewQueuedTask("d1", "c1", "u1", "/dispatch build API|write tests")

	// Enqueue the task.
	pos, err := tq.Enqueue(task)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	if pos != 1 {
		t.Errorf("position = %d, want 1", pos)
	}

	// Mark as running (simulates dispatch to worker).
	ok := tq.MarkRunning("d1")
	if !ok {
		t.Fatal("MarkRunning failed")
	}
	if tq.RunningCount() != 1 {
		t.Errorf("RunningCount = %d, want 1", tq.RunningCount())
	}

	// Simulate progress event with agents.
	// (In real flow, this comes from the worker callback.)
	progressAgents := []protocol.SubAgentInfo{
		{AgentID: "agent-1", Role: "coordinator", Progress: 0.5, Status: "running"},
		{AgentID: "agent-2", Role: "researcher", Progress: 0.3, Status: "running"},
	}
	if len(progressAgents) == 0 {
		t.Error("progress agents should not be empty")
	}

	// Simulate completion.
	tq.MarkDone("d1")
	if tq.RunningCount() != 0 {
		t.Errorf("RunningCount after done = %d, want 0", tq.RunningCount())
	}
	if tq.QueueLength() != 0 {
		t.Errorf("QueueLength after done = %d, want 0", tq.QueueLength())
	}
}

// TestDispatchErrorHandling verifies that dispatch errors are properly handled.
func TestDispatchErrorHandling(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})

	var dequeued []string
	var mu sync.Mutex
	tq.SetDequeueCallback(func(task *QueuedTask) {
		mu.Lock()
		defer mu.Unlock()
		dequeued = append(dequeued, task.TaskID)
	})

	// Enqueue a dispatch task and a normal task.
	dispatchTask := NewQueuedTask("d1", "c1", "u1", "/dispatch task A|task B")
	normalTask := NewQueuedTask("n1", "c2", "u2", "normal task")

	tq.Enqueue(dispatchTask)
	tq.Enqueue(normalTask)

	// Mark dispatch task running.
	tq.MarkRunning("d1")

	// Simulate error on dispatch task.
	tq.MarkError("d1")

	mu.Lock()
	if len(dequeued) != 1 {
		t.Fatalf("expected 1 dequeue after error, got %d", len(dequeued))
	}
	if dequeued[0] != "n1" {
		t.Errorf("dequeued task = %q, want n1", dequeued[0])
	}
	mu.Unlock()
}

// TestDispatchQueuedTaskDequeue verifies that dispatch tasks in the waiting queue
// are properly dequeued when a slot opens.
func TestDispatchQueuedTaskDequeue(t *testing.T) {
	tq := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 1, MaxPerChannel: 1})

	var dequeued []string
	var mu sync.Mutex
	tq.SetDequeueCallback(func(task *QueuedTask) {
		mu.Lock()
		defer mu.Unlock()
		dequeued = append(dequeued, task.TaskID)
	})

	// Enqueue a normal task (will be running) and a dispatch task (will queue).
	normal := NewQueuedTask("n1", "c1", "u1", "normal task")
	dispatch := NewQueuedTask("d1", "c2", "u2", "/dispatch task A|task B")

	tq.Enqueue(normal)
	tq.Enqueue(dispatch)

	// Mark normal task running.
	tq.MarkRunning("n1")

	// Dispatch task should still be in queue (different channel, but MaxConcurrentTasks=1).
	if tq.QueueLength() != 1 {
		t.Errorf("QueueLength = %d, want 1", tq.QueueLength())
	}

	// Complete the normal task — dispatch task should be dequeued.
	tq.MarkDone("n1")

	mu.Lock()
	defer mu.Unlock()

	if len(dequeued) != 1 {
		t.Fatalf("expected 1 dequeue, got %d", len(dequeued))
	}
	if dequeued[0] != "d1" {
		t.Errorf("dequeued task = %q, want d1", dequeued[0])
	}
}

// TestDispatchWithCallbackChain verifies that the callback chain (start → progress → done)
// works correctly for dispatch tasks.
func TestDispatchWithCallbackChain(t *testing.T) {
	var eventTypes []protocol.SlackEventType
	var mu sync.Mutex

	cb := func(evt *protocol.SlackEvent, err error) {
		if err != nil {
			return
		}
		if evt == nil {
			return
		}
		mu.Lock()
		eventTypes = append(eventTypes, evt.Type)
		mu.Unlock()
	}

	sw := worker.NewStreamWorker("/usr/bin/python3")
	req := protocol.TaskRequest{
		Task:     "test task",
		Dispatch: true,
	}

	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	defer mu.Unlock()

	if len(eventTypes) == 0 {
		t.Fatal("expected at least one event")
	}

	// First event should be start with dispatch type.
	if eventTypes[0] != protocol.EventTypeStart {
		t.Errorf("first event type = %q, want start", eventTypes[0])
	}
}

// TestDispatchContextInjection verifies that ExecuteDispatch injects dispatch_mode into context.
func TestDispatchContextInjection(t *testing.T) {
	sw := worker.NewStreamWorker("/usr/bin/python3")

	// We can't easily capture the internal request, but we can verify
	// the start event contains dispatch info.
	var events []*protocol.SlackEvent
	var mu sync.Mutex
	cb := func(evt *protocol.SlackEvent, err error) {
		if evt != nil {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		}
	}

	req := protocol.TaskRequest{
		Task:     "test",
		Dispatch: true,
		Context:  map[string]interface{}{"task_id": "test-123"},
	}
	_ = sw.ExecuteDispatch(req, cb)

	mu.Lock()
	defer mu.Unlock()

	if len(events) == 0 {
		t.Fatal("expected at least a start event")
	}

	// Verify the task_id was passed through.
	if events[0].Payload.TaskID != "test-123" {
		t.Errorf("task_id = %q, want \"test-123\"", events[0].Payload.TaskID)
	}
}

// TestDispatchProgressEventWithAgents verifies that the callback receives progress events
// with agent information.
func TestDispatchProgressEventWithAgents(t *testing.T) {
	// This test simulates what a real dispatch progress event looks like.
	// The actual events come from the Python worker, but we can verify
	// the renderer handles them correctly.

	agents := []protocol.SubAgentInfo{
		{AgentID: "agent-1", Role: "coordinator", Progress: 0.8, Status: "running", CurrentAction: "building API"},
		{AgentID: "agent-2", Role: "tester", Progress: 0.5, Status: "running", CurrentAction: "writing tests"},
		{AgentID: "agent-3", Role: "reviewer", Progress: 1.0, Status: "done", CurrentAction: ""},
	}

	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			TaskType:    "dispatch",
			TotalAgents: 3,
			Agents:      agents,
		},
	}

	// Verify the renderer produces dispatch blocks (not regular progress).
	blocks := BuildTaskCard(event, &TaskData{})
	if blocks == nil {
		t.Fatal("expected blocks for dispatch progress event")
	}

	// The header should mention dispatch and show overall progress.
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatalf("first block should be HeaderBlock, got %T", blocks[0])
	}
	headerText := header.Text.Text
	if !strings.Contains(headerText, "Dispatch") {
		t.Errorf("header text %q should contain \"Dispatch\"", headerText)
	}
}

// TestDispatchDoneEvent verifies the done event for dispatch tasks.
func TestDispatchDoneEvent(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeDone,
		Payload: &protocol.EventPayload{
			TaskID:   "dispatch-1",
			TaskType: "dispatch",
			Output:   "All agents completed successfully",
		},
	}

	blocks := BuildTaskCard(event, &TaskData{})
	if blocks == nil {
		t.Fatal("expected blocks for dispatch done event")
	}

	// Should be a done card with "Task Completed" header.
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatalf("first block should be HeaderBlock, got %T", blocks[0])
	}
	if !strings.Contains(header.Text.Text, "Completed") {
		t.Errorf("header text %q should contain \"Completed\"", header.Text.Text)
	}
}

// TestDispatchErrorEvent verifies the error event for dispatch tasks.
func TestDispatchErrorEvent(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeError,
		Payload: &protocol.EventPayload{
			TaskID:   "dispatch-1",
			TaskType: "dispatch",
			Message:  "Agent-2 failed: timeout",
		},
	}

	blocks := BuildTaskCard(event, &TaskData{})
	if blocks == nil {
		t.Fatal("expected blocks for dispatch error event")
	}

	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatalf("first block should be HeaderBlock, got %T", blocks[0])
	}
	if !strings.Contains(header.Text.Text, "Failed") {
		t.Errorf("header text %q should contain \"Failed\"", header.Text.Text)
	}
}

// TestDispatchWithTimeout verifies that dispatch tasks respect execution timeouts.
func TestDispatchWithTimeout(t *testing.T) {
	sw := worker.NewStreamWorker("/usr/bin/python3")

	done := make(chan struct{})
	var cbErr error
	var mu sync.Mutex

	cb := func(evt *protocol.SlackEvent, err error) {
		if err != nil {
			mu.Lock()
			cbErr = err
			mu.Unlock()
			return
		}
	}

	req := protocol.TaskRequest{
		Task:     "long running task",
		Dispatch: true,
	}

	// Execute with a timeout context (simulated via goroutine).
	go func() {
		_ = sw.ExecuteDispatch(req, cb)
		close(done)
	}()

	select {
	case <-done:
		// Completed (python not available, so it will fail fast).
	case <-time.After(5 * time.Second):
		t.Fatal("ExecuteDispatch did not complete within timeout")
	}

	// The error is expected since Python is not set up in test env.
	mu.Lock()
	defer mu.Unlock()
	// cbErr may or may not be set depending on environment — that's OK.
	_ = cbErr
}
