package gateway

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// TaskQueueConfig holds configuration for the task queue.
type TaskQueueConfig struct {
	MaxConcurrentTasks int // global max concurrent running tasks (default 5)
	MaxPerChannel      int // max concurrent tasks per channel (default 1)
}

// TaskQueue manages a queue of tasks with concurrency limits.
// It enforces:
//   - Global concurrency limit (MaxConcurrentTasks)
//   - Per-channel concurrency limit (MaxPerChannel)
//   - Same channel tasks are not dispatched until the previous one completes
type TaskQueue struct {
	mu       sync.Mutex
	config   TaskQueueConfig
	waiting  []*QueuedTask        // FIFO queue of waiting tasks
	running  map[string]*QueuedTask // task_id -> running task
	channels map[string]int       // channel_id -> count of running/queued-for-dispatch tasks for that channel
	onDequeue func(task *QueuedTask) // callback when a task is dequeued for dispatch
}

// NewTaskQueue creates a new task queue with the given config.
func NewTaskQueue(config TaskQueueConfig) *TaskQueue {
	if config.MaxConcurrentTasks <= 0 {
		config.MaxConcurrentTasks = 5
	}
	if config.MaxPerChannel <= 0 {
		config.MaxPerChannel = 1
	}
	return &TaskQueue{
		config:  config,
		waiting: make([]*QueuedTask, 0),
		running: make(map[string]*QueuedTask),
		channels: make(map[string]int),
	}
}

// SetDequeueCallback sets the callback invoked when a task is dequeued for dispatch.
func (tq *TaskQueue) SetDequeueCallback(fn func(task *QueuedTask)) {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	tq.onDequeue = fn
}

// Enqueue adds a task to the waiting queue.
// Returns the position in queue (1-based) after enqueueing.
func (tq *TaskQueue) Enqueue(task *QueuedTask) (int, error) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	if task == nil {
		return 0, fmt.Errorf("task cannot be nil")
	}
	if task.TaskID == "" {
		return 0, fmt.Errorf("task_id cannot be empty")
	}

	// Check if task is already in queue or running.
	if _, exists := tq.running[task.TaskID]; exists {
		return 0, fmt.Errorf("task %s is already running", task.TaskID)
	}
	for _, wt := range tq.waiting {
		if wt.TaskID == task.TaskID {
			return 0, fmt.Errorf("task %s is already in queue", task.TaskID)
		}
	}

	tq.waiting = append(tq.waiting, task)
	tq.channels[task.ChannelID]++
	atomic.AddInt64(&totalTasksEnqueued, 1)
	atomic.AddInt64(&queuedTasks, 1)

	return tq.positionOf(task.TaskID), nil
}

// MarkRunning marks a task as running and removes it from the waiting queue.
// Returns true if the task was found and marked, false otherwise.
func (tq *TaskQueue) MarkRunning(taskID string) bool {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	for i, wt := range tq.waiting {
		if wt.TaskID == taskID {
			// Remove from waiting queue.
			tq.waiting = append(tq.waiting[:i], tq.waiting[i+1:]...)
			tq.channels[wt.ChannelID]--
			if tq.channels[wt.ChannelID] <= 0 {
				delete(tq.channels, wt.ChannelID)
			}
			tq.running[taskID] = wt
			if err := wt.Transition(TaskStatusRunning); err != nil {
				// Should not happen if state machine is correct.
			}
			// Update metrics.
			atomic.AddInt64(&activeTasks, 1)
			atomic.AddInt64(&queuedTasks, -1)
			return true
		}
	}
	return false
}

// MarkDone marks a running task as done and triggers dequeue of the next eligible task.
func (tq *TaskQueue) MarkDone(taskID string) {
	atomic.AddInt64(&totalTasksCompleted, 1)
	atomic.AddInt64(&activeTasks, -1)
	tq.mu.Lock()
	delete(tq.running, taskID)
	tq.mu.Unlock()
	tq.tryDequeue()
}

// MarkError marks a running task as error and triggers dequeue of the next eligible task.
func (tq *TaskQueue) MarkError(taskID string) {
	atomic.AddInt64(&totalTasksCompleted, 1)
	atomic.AddInt64(&activeTasks, -1)
	tq.mu.Lock()
	delete(tq.running, taskID)
	tq.mu.Unlock()
	tq.tryDequeue()
}

// releaseTerminalTask removes a terminal (done/error) task from the running
// map, properly decrementing the channel counter. Used by ReconcileAfterRecovery
// where the task was added directly via MarkRunningDirect (not from the waiting
// queue). The isDone flag determines whether tryDequeue runs after release.
func (tq *TaskQueue) releaseTerminalTask(taskID string, isDone bool) {
	tq.mu.Lock()
	task, ok := tq.running[taskID]
	if ok {
		delete(tq.running, taskID)
		tq.channels[task.ChannelID]--
		if tq.channels[task.ChannelID] <= 0 {
			delete(tq.channels, task.ChannelID)
		}
	}
	tq.mu.Unlock()

	if ok {
		atomic.AddInt64(&activeTasks, -1)
		if isDone {
			tq.tryDequeue()
		}
	}
}

// MarkDispatched marks a task (already in the running map with Dispatching status)
// as Running. This is called by the dequeue callback after the task has been
// dispatched to the worker.
func (tq *TaskQueue) MarkDispatched(taskID string) bool {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	task, exists := tq.running[taskID]
	if !exists {
		return false
	}
	if err := task.Transition(TaskStatusRunning); err != nil {
		return false
	}
	return true
}

// MarkRunningDirect adds a task directly to the running map without going
// through the waiting queue. Used for tasks that are executed immediately
// (not queued) but still need to be tracked.
func (tq *TaskQueue) MarkRunningDirect(task *QueuedTask) {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	if task.TaskID == "" {
		return
	}
	// Avoid double-registration.
	if _, exists := tq.running[task.TaskID]; exists {
		return
	}
	// Set status to running if not already past queued.
	if task.Status == TaskStatusQueued {
		task.Status = TaskStatusRunning
		if task.StartedAt.IsZero() {
			task.StartedAt = time.Now().UTC()
		}
	}
	tq.running[task.TaskID] = task
	tq.channels[task.ChannelID]++
	atomic.AddInt64(&totalTasksEnqueued, 1)
	atomic.AddInt64(&activeTasks, 1)
}

// FindByChannel finds a running or waiting task for the given channel.
// Returns the first matching task or nil.
func (tq *TaskQueue) FindByChannel(channelID string) *QueuedTask {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	for _, t := range tq.running {
		if t.ChannelID == channelID {
			return t
		}
	}
	for _, t := range tq.waiting {
		if t.ChannelID == channelID {
			return t
		}
	}
	return nil
}

// RunningCount returns the number of currently running tasks.
func (tq *TaskQueue) RunningCount() int {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	return len(tq.running)
}

// QueueLength returns the number of tasks waiting in the queue.
func (tq *TaskQueue) QueueLength() int {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	return len(tq.waiting)
}

// Position returns the 1-based position of a task in the queue.
// Returns 0 if the task is not in the queue.
func (tq *TaskQueue) Position(taskID string) int {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	return tq.positionOf(taskID)
}

// positionOf returns the 1-based position of a task in the waiting queue (caller must hold lock).
func (tq *TaskQueue) positionOf(taskID string) int {
	for i, wt := range tq.waiting {
		if wt.TaskID == taskID {
			return i + 1
		}
	}
	return 0
}

// CanEnqueue checks if a new task can be enqueued (always true since we queue instead of reject).
func (tq *TaskQueue) CanEnqueue() bool {
	return true
}

// IsAtCapacity returns true if the global concurrent limit is reached.
func (tq *TaskQueue) IsAtCapacity() bool {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	return len(tq.running) >= tq.config.MaxConcurrentTasks
}

// ChannelHasActiveTask returns true if the channel has a running or queued task.
func (tq *TaskQueue) ChannelHasActiveTask(channelID string) bool {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	return tq.channels[channelID] > 0
}

// ListWaiting returns a copy of all waiting tasks in FIFO order.
func (tq *TaskQueue) ListWaiting() []*QueuedTask {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	result := make([]*QueuedTask, len(tq.waiting))
	copy(result, tq.waiting)
	return result
}

// ListRunning returns a copy of all running tasks.
func (tq *TaskQueue) ListRunning() []*QueuedTask {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	result := make([]*QueuedTask, 0, len(tq.running))
	for _, t := range tq.running {
		result = append(result, t)
	}
	return result
}

// tryDequeue attempts to find and dispatch the next eligible task from the queue.
// It must be called with the lock released to avoid calling onDequeue while locked.
func (tq *TaskQueue) tryDequeue() {
	tq.mu.Lock()
	next := tq.findNextEligibleLocked()
	if next != nil {
		// Remove from waiting queue.
		for i, wt := range tq.waiting {
			if wt.TaskID == next.TaskID {
				tq.waiting = append(tq.waiting[:i], tq.waiting[i+1:]...)
				break
			}
		}
		tq.channels[next.ChannelID]--
		if tq.channels[next.ChannelID] <= 0 {
			delete(tq.channels, next.ChannelID)
		}
		tq.running[next.TaskID] = next
		next.Transition(TaskStatusDispatching)
		// Update metrics: task moved from waiting to running.
		atomic.AddInt64(&queuedTasks, -1)
		atomic.AddInt64(&activeTasks, 1)
	}
	tq.mu.Unlock()

	if next != nil && tq.onDequeue != nil {
		tq.onDequeue(next)
	}
}

// findNextEligibleLocked finds the next task that can be dispatched (caller must hold lock).
// A task is eligible if:
//   - Global concurrent limit is not reached
//   - No other task for the same channel is running
func (tq *TaskQueue) findNextEligibleLocked() *QueuedTask {
	// Check global limit.
	if len(tq.running) >= tq.config.MaxConcurrentTasks {
		return nil
	}

	// Find which channels already have running tasks.
	runningChannels := make(map[string]bool)
	for _, t := range tq.running {
		runningChannels[t.ChannelID] = true
	}

	// Scan waiting queue in FIFO order.
	for _, wt := range tq.waiting {
		if !runningChannels[wt.ChannelID] {
			return wt
		}
	}

	return nil
}
