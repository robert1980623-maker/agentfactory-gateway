package gateway

import (
	"fmt"
	"time"
)

// TaskStatus represents the lifecycle state of a queued task.
type TaskStatus string

const (
	TaskStatusQueued      TaskStatus = "queued"
	TaskStatusDispatching TaskStatus = "dispatching"
	TaskStatusRunning     TaskStatus = "running"
	TaskStatusDone        TaskStatus = "done"
	TaskStatusError       TaskStatus = "error"
)

// IsValidTransition returns true if the transition from current to next is valid.
// State machine: queued → dispatching → running → done/error
func IsValidTransition(current, next TaskStatus) bool {
	transitions := map[TaskStatus]map[TaskStatus]bool{
		TaskStatusQueued:      {TaskStatusDispatching: true},
		TaskStatusDispatching: {TaskStatusRunning: true, TaskStatusError: true},
		TaskStatusRunning:     {TaskStatusDone: true, TaskStatusError: true},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	return allowed[next]
}

// QueuedTask represents a task waiting in or being processed by the task queue.
type QueuedTask struct {
	TaskID    string     `json:"task_id"`
	ChannelID string     `json:"channel_id"`
	UserID    string     `json:"user_id"`
	Prompt    string     `json:"prompt"`
	Status    TaskStatus `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt time.Time  `json:"started_at,omitempty"`
	SlackTS   string     `json:"slack_ts,omitempty"` // Slack message timestamp for updates
}

// NewQueuedTask creates a new task in queued state.
func NewQueuedTask(taskID, channelID, userID, prompt string) *QueuedTask {
	return &QueuedTask{
		TaskID:    taskID,
		ChannelID: channelID,
		UserID:    userID,
		Prompt:    prompt,
		Status:    TaskStatusQueued,
		CreatedAt: time.Now().UTC(),
	}
}

// Transition attempts to move the task to the next status.
// Returns an error if the transition is invalid.
func (qt *QueuedTask) Transition(next TaskStatus) error {
	if !IsValidTransition(qt.Status, next) {
		return &InvalidTransitionError{From: qt.Status, To: next}
	}
	qt.Status = next
	if next == TaskStatusRunning || next == TaskStatusDispatching {
		if qt.StartedAt.IsZero() {
			qt.StartedAt = time.Now().UTC()
		}
	}
	return nil
}

// InvalidTransitionError is returned when an invalid state transition is attempted.
type InvalidTransitionError struct {
	From TaskStatus
	To   TaskStatus
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid task status transition: %s → %s", e.From, e.To)
}
