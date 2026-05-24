package gateway

import (
	"context"
	"fmt"
	"log"

	"github.com/agentfactory/gateway/protocol"
	statemgr "github.com/agentfactory/gateway/state"

	"github.com/slack-go/slack"
)

// SlackClient defines the minimal Slack API surface needed for recovery.
// *slack.Client implements this interface.
type SlackClient interface {
	UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

// StatusChecker defines the interface for checking task status.
// *worker.PythonWorker implements this interface.
type StatusChecker interface {
	CheckStatus(taskID string) (status string, err error)
}

// RecoverActiveTasks checks all tasks currently marked as "running" in the
// StateManager against the worker, and updates both the Slack message
// and the persisted state to reflect the true status.
//
// This should be called once at startup, before the main Slack event loop,
// to reconcile state after a gateway crash or restart.
func RecoverActiveTasks(
	ctx context.Context,
	stateMgr *statemgr.StateManager,
	checker StatusChecker,
	slackClient SlackClient,
) error {
	active := stateMgr.ListActive()
	if len(active) == 0 {
		log.Println("Recovery: no active tasks to recover")
		return nil
	}

	log.Printf("Recovery: checking %d active task(s)", len(active))

	for _, rec := range active {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := recoverSingle(ctx, stateMgr, checker, slackClient, rec); err != nil {
			log.Printf("Recovery: failed to recover task %s: %v", rec.TaskID, err)
			// Continue recovering other tasks even if one fails.
		}
	}

	return nil
}

func recoverSingle(
	ctx context.Context,
	stateMgr *statemgr.StateManager,
	checker StatusChecker,
	slackClient SlackClient,
	rec *statemgr.TaskRecord,
) error {
	log.Printf("Recovery: checking task %s (channel=%s, ts=%s)", rec.TaskID, rec.ChannelID, rec.SlackTS)

	status, err := checker.CheckStatus(rec.TaskID)
	if err != nil {
		log.Printf("Recovery: CheckStatus failed for %s: %v — marking as failed", rec.TaskID, err)
		return updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "error",
			fmt.Sprintf("Recovery check failed: %v", err))
	}

	switch status {
	case "done", "completed":
		log.Printf("Recovery: task %s is done", rec.TaskID)
		return updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "done", "✅ Recovered: Task completed successfully")
	case "error", "failed":
		log.Printf("Recovery: task %s failed", rec.TaskID)
		return updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "error", "❌ Recovered: Task failed")
	case "timeout", "timed_out":
		log.Printf("Recovery: task %s timed out", rec.TaskID)
		return updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "error", "❌ Recovered: Task timed out")
	case "running", "in_progress":
		log.Printf("Recovery: task %s is still running", rec.TaskID)
		return updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "running", "🔄 Recovered: Task still running")
	default:
		log.Printf("Recovery: unknown status %q for task %s — marking as error", status, rec.TaskID)
		return updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "error",
			fmt.Sprintf("❌ Recovered: Unknown status %q", status))
	}
}

func updateRecoveredStatus(
	ctx context.Context,
	stateMgr *statemgr.StateManager,
	slackClient SlackClient,
	rec *statemgr.TaskRecord,
	newStatus string,
	message string,
) error {
	// Update Slack message.
	if rec.ChannelID != "" && rec.SlackTS != "" {
		blocks := buildRecoveryBlocks(newStatus, message, rec.TaskID)
		if rec.SlackTS == "" {
			_, _, err := slackClient.PostMessage(rec.ChannelID, slack.MsgOptionBlocks(blocks...))
			if err != nil {
				log.Printf("Recovery: failed to post recovery message for %s: %v", rec.TaskID, err)
			}
		} else {
			_, _, _, err := slackClient.UpdateMessage(rec.ChannelID, rec.SlackTS, slack.MsgOptionBlocks(blocks...))
			if err != nil {
				log.Printf("Recovery: failed to update message for %s: %v", rec.TaskID, err)
			}
		}
	}

	// Update persisted state.
	return stateMgr.Set(statemgr.TaskRecord{
		TaskID:    rec.TaskID,
		ChannelID: rec.ChannelID,
		SlackTS:   rec.SlackTS,
		Status:    newStatus,
	})
}

// buildRecoveryBlocks creates a Slack Block Kit message for a recovered task.
func buildRecoveryBlocks(status, message, taskID string) []slack.Block {
	var headerText string
	switch status {
	case "done":
		headerText = "✅ Recovered: Done"
	case "running":
		headerText = "🔄 Recovered: Still Running"
	case "error":
		headerText = "❌ Recovered: Failed"
	default:
		headerText = "📋 Recovered Task"
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, headerText, true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, message, false, false),
			nil, nil,
		),
	}

	if taskID != "" {
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, fmt.Sprintf("Task ID: %s", taskID), false, false,
		)))
	}

	// Add a final status indicator for done/error.
	if status == "done" || status == "error" {
		blocks = append(blocks, slack.NewDividerBlock())
		evt := &protocol.SlackEvent{}
		if status == "done" {
			evt.Type = protocol.EventTypeDone
		} else {
			evt.Type = protocol.EventTypeError
			evt.Payload = &protocol.EventPayload{Message: message}
		}
		taskData := &TaskData{}
		finalBlocks := BuildTaskCard(evt, taskData)
		if finalBlocks != nil {
			blocks = append(blocks, finalBlocks...)
		}
	}

	return blocks
}
