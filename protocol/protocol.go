package protocol

// TaskRequest represents a task sent to a Python worker.
type TaskRequest struct {
	Task    string                 `json:"task"`
	Context map[string]interface{} `json:"context,omitempty"`
}

// TaskResponse represents a response from a Python worker.
type TaskResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// SlackEventType represents the type of a streaming JSONL event.
type SlackEventType string

const (
	EventTypeStart    SlackEventType = "start"
	EventTypeProgress SlackEventType = "progress"
	EventTypeDone     SlackEventType = "done"
	EventTypeError    SlackEventType = "error"
)

// SlackEvent represents a single line in a JSONL streaming output from the Python worker.
type SlackEvent struct {
	Type    SlackEventType `json:"type"`
	Payload *EventPayload  `json:"payload,omitempty"`
}

// EventPayload carries the data for a streaming event. Fields vary by type.
type EventPayload struct {
	// Common
	TaskID     string `json:"task_id,omitempty"`
	ChannelID  string `json:"channel_id,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
	Action     string `json:"action,omitempty"`
	UserInput  string `json:"user_input,omitempty"`
	TotalSteps int    `json:"total_steps,omitempty"`
	CurrentStep int   `json:"current_step,omitempty"`

	// Progress
	Progress float64 `json:"progress,omitempty"` // 0.0 - 1.0
	Model    string  `json:"model,omitempty"`

	// Done
	Output string `json:"output,omitempty"`
	Code   string `json:"code,omitempty"`

	// Metadata
	Tokens     int    `json:"tokens,omitempty"`
	ElapsedTime string `json:"elapsed_time,omitempty"`

	// Error
	Message string `json:"message,omitempty"`
}
