package protocol

// TaskRequest represents a task sent to a worker.
type TaskRequest struct {
	Task     string                 `json:"task"`
	TaskType string                 `json:"task_type,omitempty"` // "internal" | "external"
	Context  map[string]interface{} `json:"context,omitempty"`
	Dispatch bool                   `json:"dispatch,omitempty"` // enable multi-agent dispatch mode
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
	EventTypeToolCall SlackEventType = "tool_call" // NEW
	EventTypePaused   SlackEventType = "paused"    // HITL: paused at checkpoint
)

// SlackEvent represents a single line in a JSONL streaming output from the Python worker.
type SlackEvent struct {
	Type    SlackEventType `json:"type"`
	Payload *EventPayload  `json:"payload,omitempty"`
}

// ToolInfo describes a tool/function call made by the agent.
type ToolInfo struct {
	Name   string `json:"name"`
	Args   string `json:"args"`
	Result string `json:"result,omitempty"`
}

// ButtonAction represents an interactive button in Block Kit.
type ButtonAction struct {
	Text  string `json:"text"`
	Value string `json:"value"`
	Style string `json:"style"` // primary, danger, or empty
	Type  string `json:"type"`  // always "button"
}

// SubAgentInfo represents a single agent in a multi-agent dispatch.
type SubAgentInfo struct {
	AgentID       string  `json:"agent_id"`
	Role          string  `json:"role"`
	Task          string  `json:"task"`
	Progress      float64 `json:"progress"`
	Status        string  `json:"status"` // "running" | "done" | "error"
	CurrentAction string  `json:"current_action"`
}

// ChainContext carries HITL checkpoint data when the Python Core pauses
// at a review step (e.g., Architect design review, Developer code review).
type ChainContext struct {
	PausedStep       string `json:"paused_step,omitempty"`       // e.g., "architect", "developer", "devops"
	DesignDoc        string `json:"design_doc,omitempty"`        // summary of the design
	ModificationLog  string `json:"modification_log,omitempty"`  // content of MODIFICATION_LOG.md
	FeedbackRequired bool   `json:"feedback_required"`           // true if user must provide feedback
	GitStatusSummary string `json:"git_status_summary,omitempty"` // git status/log summary for devops step
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

	// Dispatch / Multi-agent
	TaskType    string         `json:"task_type,omitempty"`    // "single" | "dispatch"
	TotalAgents int            `json:"total_agents,omitempty"`
	Agents      []SubAgentInfo `json:"agents,omitempty"`
	SubTaskID   string         `json:"subtask_id,omitempty"`

	// Progress
	Progress float64 `json:"progress,omitempty"` // 0.0 - 1.0
	Model    string  `json:"model,omitempty"`

	// Done
	Output string `json:"output,omitempty"`
	Code   string `json:"code,omitempty"`

	// Tool call
	Tool *ToolInfo `json:"tool,omitempty"`

	// Action buttons
	Actions []ButtonAction `json:"actions,omitempty"`

	// Metadata
	Tokens      int    `json:"tokens,omitempty"`
	ElapsedTime string `json:"elapsed_time,omitempty"`

	// Error
	Message string `json:"message,omitempty"`

	// HITL Chain Context
	ChainContext *ChainContext `json:"chain_context,omitempty"`
}
