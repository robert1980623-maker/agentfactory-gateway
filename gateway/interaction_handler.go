package gateway

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/agentfactory/gateway/protocol"
	statemgr "github.com/agentfactory/gateway/state"
	"github.com/agentfactory/gateway/worker"

	"github.com/slack-go/slack"
)

// HITLDecision represents a user's HITL response.
type HITLDecision string

const (
	DecisionApprove       HITLDecision = "APPROVE"
	DecisionReject        HITLDecision = "REJECT"
	DecisionFeedback      HITLDecision = "FEEDBACK"
	DecisionApprovePush   HITLDecision = "APPROVE_PUSH"
	DecisionApproveRebase HITLDecision = "APPROVE_REBASE"
)

// ResumePayload is sent to the Python Core to resume a paused task.
type ResumePayload struct {
	TaskID   string       `json:"task_id"`
	Decision HITLDecision `json:"decision"`
	Comment  string       `json:"comment,omitempty"`
}

// HITLHandler manages Slack interactions for Human-in-the-Loop reviews.
type HITLHandler struct {
	worker       *worker.PythonWorker
	streamWorker *worker.StreamWorker
	stateMgr     *statemgr.StateManager
	client       *slack.Client
}

// NewHITLHandler creates a new HITL interaction handler.
func NewHITLHandler(w *worker.PythonWorker, sw *worker.StreamWorker, stateMgr *statemgr.StateManager, client *slack.Client) *HITLHandler {
	return &HITLHandler{
		worker:       w,
		streamWorker: sw,
		stateMgr:     stateMgr,
		client:       client,
	}
}

// HandleAction processes a HITL button action from Slack.
// Returns true if the action was handled, false otherwise.
func (h *HITLHandler) HandleAction(callback slack.InteractionCallback) bool {
	if len(callback.ActionCallback.BlockActions) == 0 {
		return false
	}

	action := callback.ActionCallback.BlockActions[0]

	switch action.ActionID {
	case "hitl_approve":
		h.handleApprove(callback)
		return true
	case "hitl_reject":
		h.handleReject(callback)
		return true
	case "hitl_feedback":
		h.handleFeedback(callback)
		return true
	case "git_push":
		h.handleGitPush(callback)
		return true
	case "git_rebase":
		h.handleGitRebase(callback)
		return true
	case "git_manual":
		h.handleGitManual(callback)
		return true
	}

	return false
}

// HandleViewSubmission processes a view (modal) submission.
func (h *HITLHandler) HandleViewSubmission(callback slack.InteractionCallback) {
	privateData := callback.View.PrivateMetadata
	if privateData == "" {
		log.Println("HITL: view submission missing private metadata")
		return
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(privateData), &data); err != nil {
		log.Printf("HITL: failed to parse private metadata: %v", err)
		return
	}

	taskID := data["task_id"]
	mode := data["mode"]

	// Extract feedback text from the view state.
	feedbackText := ""
	if callback.View.State != nil {
		if inputBlock, ok := callback.View.State.Values["feedback_input"]; ok {
			if textInput, ok := inputBlock["feedback_text"]; ok {
				feedbackText = textInput.Value
			}
		}
	}

	if feedbackText == "" {
		feedbackText = "(no feedback provided)"
	}

	var decision HITLDecision
	switch mode {
	case "request_changes":
		decision = DecisionReject
	case "feedback":
		decision = DecisionFeedback
	case "rebase_confirm":
		// Only resume if user typed YES.
		if strings.ToUpper(strings.TrimSpace(feedbackText)) != "YES" {
			log.Printf("HITL: rebase cancelled by user task=%s", taskID)
			return
		}
		decision = DecisionApproveRebase
	case "manual_commands":
		decision = DecisionFeedback
	default:
		decision = DecisionFeedback
	}

	log.Printf("HITL: modal submit task=%s decision=%s feedback=%q", taskID, decision, feedbackText)

	if err := h.sendResume(taskID, decision, feedbackText); err != nil {
		log.Printf("HITL: failed to send resume for task %s: %v", taskID, err)
	}

	// Update state manager.
	if h.stateMgr != nil && taskID != "" {
		if err := h.stateMgr.Set(statemgr.TaskRecord{
			TaskID: taskID,
			Status: "resuming",
		}); err != nil {
			log.Printf("HITL: failed to update state for task %s: %v", taskID, err)
		}
	}
}

// parseValuePayload extracts task_id and channel_id from the button value.
func parseValuePayload(value string) (taskID, channelID string) {
	parts := strings.SplitN(value, "|", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return value, ""
}

// handleApprove sends an APPROVE resume to the Python Core.
func (h *HITLHandler) handleApprove(callback slack.InteractionCallback) {
	taskID, channelID := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)
	if channelID == "" {
		channelID = callback.Channel.ID
	}

	log.Printf("HITL: approve task=%s channel=%s user=%s", taskID, channelID, callback.User.ID)

	// Update Slack message to show approved state.
	h.updateMessageToState(channelID, callback.MessageTs, "approved")

	// Send resume command to Python Core.
	if err := h.sendResume(taskID, DecisionApprove, ""); err != nil {
		log.Printf("HITL: failed to send resume for task %s: %v", taskID, err)
		h.reply(channelID, fmt.Sprintf("⚠️ Failed to send approve command: %v", err))
	}

	// Update state manager.
	if h.stateMgr != nil && taskID != "" {
		if err := h.stateMgr.Set(statemgr.TaskRecord{
			TaskID: taskID,
			Status: "resuming",
		}); err != nil {
			log.Printf("HITL: failed to update state for task %s: %v", taskID, err)
		}
	}
}

// handleReject sends a REJECT resume to the Python Core.
func (h *HITLHandler) handleReject(callback slack.InteractionCallback) {
	taskID, channelID := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)
	if channelID == "" {
		channelID = callback.Channel.ID
	}

	log.Printf("HITL: reject task=%s channel=%s user=%s", taskID, channelID, callback.User.ID)

	// Open a modal for the user to provide change request details.
	h.openFeedbackModal(callback, "request_changes")
}

// handleFeedback opens a Slack modal for the user to provide feedback.
func (h *HITLHandler) handleFeedback(callback slack.InteractionCallback) {
	log.Printf("HITL: feedback requested task=%s channel=%s user=%s",
		callback.ActionCallback.BlockActions[0].Value,
		callback.Channel.ID,
		callback.User.ID)

	h.openFeedbackModal(callback, "feedback")
}

// openFeedbackModal pushes a modal view for user input.
func (h *HITLHandler) openFeedbackModal(callback slack.InteractionCallback, mode string) {
	taskID, _ := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)

	title := "Provide Feedback"
	if mode == "request_changes" {
		title = "Request Changes"
	}

	blocks := slack.Blocks{
		BlockSet: []slack.Block{
			slack.NewInputBlock("feedback_input",
				slack.NewTextBlockObject(slack.PlainTextType, title, false, false),
				slack.NewTextBlockObject(slack.PlainTextType,
					"Enter your feedback or change requests here...", false, false),
				slack.NewPlainTextInputBlockElement(
					slack.NewTextBlockObject(slack.PlainTextType,
						"Enter your feedback or change requests here...", false, false),
					"feedback_text",
				).WithMultiline(true),
			),
		},
	}

	// Store task_id and mode in private metadata for retrieval on submit.
	privateData := map[string]string{
		"task_id": taskID,
		"mode":    mode,
		"user_id": callback.User.ID,
	}
	privateJSON, _ := json.Marshal(privateData)

	view := slack.ModalViewRequest{
		Type: slack.VTModal,
		Title: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: title,
		},
		Submit: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Send",
		},
		Close: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Cancel",
		},
		Blocks:          blocks,
		PrivateMetadata: string(privateJSON),
	}

	_, err := h.client.OpenView(callback.TriggerID, view)
	if err != nil {
		log.Printf("HITL: failed to open modal: %v", err)
		h.reply(callback.Channel.ID, "⚠️ Failed to open feedback dialog. Please try again.")
	}
}

// sendResume sends a resume command to the Python Core.
func (h *HITLHandler) sendResume(taskID string, decision HITLDecision, comment string) error {
	if taskID == "" {
		return fmt.Errorf("cannot resume: empty task_id")
	}

	payload := ResumePayload{
		TaskID:   taskID,
		Decision: decision,
		Comment:  comment,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal resume payload: %w", err)
	}

	// Send via the Python worker as a resume task.
	req := protocol.TaskRequest{
		Task: string(payloadBytes),
		Context: map[string]interface{}{
			"action": "resume",
		},
	}

	if h.worker != nil {
		_, err := h.worker.Execute(req)
		if err != nil {
			return fmt.Errorf("worker execute resume: %w", err)
		}
	}

	return nil
}

// updateMessageToState updates the Slack message to show the HITL state.
func (h *HITLHandler) updateMessageToState(channelID, ts, state string) {
	if ts == "" {
		return
	}

	var headerText, bodyText string
	switch state {
	case "approved":
		headerText = "✅ Approved"
		bodyText = "You approved this step. The agent is resuming..."
	case "push_initiated":
		headerText = "🚀 Push Initiated"
		bodyText = "Push & PR command sent. The agent is executing..."
	default:
		headerText = "⏸️ Awaiting Response"
		bodyText = "Action recorded."
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, headerText, true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, bodyText, false, false),
			nil, nil,
		),
	}

	_, _, _, err := h.client.UpdateMessage(channelID, ts, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		log.Printf("HITL: failed to update message: %v", err)
	}
}

// reply sends a plain text reply to a channel.
func (h *HITLHandler) reply(channel, text string) {
	if h.client == nil {
		return
	}
	_, _, err := h.client.PostMessage(channel, slack.MsgOptionText(text, false))
	if err != nil {
		log.Printf("HITL: failed to reply: %v", err)
	}
}

// handleGitPush sends an APPROVE_PUSH resume to the Python Core.
func (h *HITLHandler) handleGitPush(callback slack.InteractionCallback) {
	taskID, channelID := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)
	if channelID == "" {
		channelID = callback.Channel.ID
	}

	log.Printf("HITL: git_push task=%s channel=%s user=%s", taskID, channelID, callback.User.ID)

	// Update Slack message to show push initiated state.
	h.updateMessageToState(channelID, callback.MessageTs, "push_initiated")

	// Send resume command to Python Core.
	if err := h.sendResume(taskID, DecisionApprovePush, ""); err != nil {
		log.Printf("HITL: failed to send git_push resume for task %s: %v", taskID, err)
		h.reply(channelID, fmt.Sprintf("⚠️ Failed to send push command: %v", err))
	}

	// Update state manager.
	if h.stateMgr != nil && taskID != "" {
		if err := h.stateMgr.Set(statemgr.TaskRecord{
			TaskID: taskID,
			Status: "resuming",
		}); err != nil {
			log.Printf("HITL: failed to update state for task %s: %v", taskID, err)
		}
	}
}

// handleGitRebase opens a confirmation modal before sending APPROVE_REBASE.
func (h *HITLHandler) handleGitRebase(callback slack.InteractionCallback) {
	taskID, _ := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)

	log.Printf("HITL: git_rebase task=%s channel=%s user=%s", taskID, callback.Channel.ID, callback.User.ID)

	// Open a confirmation modal for rebase.
	h.openRebaseConfirmModal(callback)
}

// handleGitManual opens a modal for the user to enter manual git commands.
func (h *HITLHandler) handleGitManual(callback slack.InteractionCallback) {
	taskID, _ := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)

	log.Printf("HITL: git_manual task=%s channel=%s user=%s", taskID, callback.Channel.ID, callback.User.ID)

	h.openManualCommandsModal(callback)
}

// openRebaseConfirmModal pushes a modal to confirm rebase & push.
func (h *HITLHandler) openRebaseConfirmModal(callback slack.InteractionCallback) {
	taskID, _ := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)

	blocks := slack.Blocks{
		BlockSet: []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					"⚠️ *Rebase & Push*\n\nThis will rebase your current branch onto the target branch and then push. This may rewrite history.", false, false),
				nil, nil,
			),
			slack.NewInputBlock("rebase_confirm_input",
				slack.NewTextBlockObject(slack.PlainTextType, "Confirm (type YES to proceed)", false, false),
				slack.NewTextBlockObject(slack.PlainTextType,
					"Type YES to confirm rebase & push", false, false),
				slack.NewPlainTextInputBlockElement(
					slack.NewTextBlockObject(slack.PlainTextType,
						"Type YES to confirm", false, false),
					"rebase_confirm_text",
				),
			),
		},
	}

	privateData := map[string]string{
		"task_id": taskID,
		"mode":    "rebase_confirm",
		"user_id": callback.User.ID,
	}
	privateJSON, _ := json.Marshal(privateData)

	view := slack.ModalViewRequest{
		Type: slack.VTModal,
		Title: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Confirm Rebase & Push",
		},
		Submit: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Confirm",
		},
		Close: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Cancel",
		},
		Blocks:          blocks,
		PrivateMetadata: string(privateJSON),
	}

	_, err := h.client.OpenView(callback.TriggerID, view)
	if err != nil {
		log.Printf("HITL: failed to open rebase confirm modal: %v", err)
		h.reply(callback.Channel.ID, "⚠️ Failed to open confirmation dialog. Please try again.")
	}
}

// openManualCommandsModal pushes a modal for entering manual git commands.
func (h *HITLHandler) openManualCommandsModal(callback slack.InteractionCallback) {
	taskID, _ := parseValuePayload(callback.ActionCallback.BlockActions[0].Value)

	blocks := slack.Blocks{
		BlockSet: []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					"📝 *Manual Git Commands*\n\nEnter the git commands you want to execute (one per line).", false, false),
				nil, nil,
			),
			slack.NewInputBlock("manual_commands_input",
				slack.NewTextBlockObject(slack.PlainTextType, "Commands", false, false),
				slack.NewTextBlockObject(slack.PlainTextType,
					"Enter git commands, one per line (e.g., git checkout main)", false, false),
				slack.NewPlainTextInputBlockElement(
					slack.NewTextBlockObject(slack.PlainTextType,
						"git checkout main\ngit merge feature-branch", false, false),
					"manual_commands_text",
				).WithMultiline(true),
			),
		},
	}

	privateData := map[string]string{
		"task_id": taskID,
		"mode":    "manual_commands",
		"user_id": callback.User.ID,
	}
	privateJSON, _ := json.Marshal(privateData)

	view := slack.ModalViewRequest{
		Type: slack.VTModal,
		Title: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Manual Git Commands",
		},
		Submit: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Execute",
		},
		Close: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "Cancel",
		},
		Blocks:          blocks,
		PrivateMetadata: string(privateJSON),
	}

	_, err := h.client.OpenView(callback.TriggerID, view)
	if err != nil {
		log.Printf("HITL: failed to open manual commands modal: %v", err)
		h.reply(callback.Channel.ID, "⚠️ Failed to open dialog. Please try again.")
	}
}
