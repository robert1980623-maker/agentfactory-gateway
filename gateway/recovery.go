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

// RecoveryResult captures the outcome of recovering a single task.
type RecoveryResult struct {
	TaskID     string
	ChannelID  string
	FinalStatus string // the status written to the state store
}

// RecoverActiveTasks checks all tasks currently marked as "running" in the
// StateManager against the worker, and updates both the Slack message
// and the persisted state to reflect the true status.
//
// This should be called once at startup, before the main Slack event loop,
// to reconcile state after a gateway crash or restart.
//
// It returns a slice of RecoveryResult (one per active task found) and an
// error only if the context is cancelled. Individual task failures are logged
// but do not abort the recovery of remaining tasks.
func RecoverActiveTasks(
	ctx context.Context,
	stateMgr statemgr.StateManager,
	checker StatusChecker,
	slackClient SlackClient,
) ([]RecoveryResult, error) {
	active := stateMgr.ListActive()
	if len(active) == 0 {
		log.Println("Recovery: no active tasks to recover")
		return nil, nil
	}

	log.Printf("Recovery: checking %d active task(s)", len(active))

	var results []RecoveryResult

	for _, rec := range active {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		result, err := recoverSingle(ctx, stateMgr, checker, slackClient, rec)
		if err != nil {
			log.Printf("Recovery: failed to recover task %s: %v", rec.TaskID, err)
			// Continue recovering other tasks even if one fails.
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

func recoverSingle(
	ctx context.Context,
	stateMgr statemgr.StateManager,
	checker StatusChecker,
	slackClient SlackClient,
	rec *statemgr.TaskRecord,
) (RecoveryResult, error) {
	log.Printf("Recovery: checking task %s (channel=%s, ts=%s)", rec.TaskID, rec.ChannelID, rec.SlackTS)

	status, err := checker.CheckStatus(rec.TaskID)
	if err != nil {
		log.Printf("Recovery: CheckStatus failed for %s: %v — marking as failed", rec.TaskID, err)
		if updateErr := updateRecoveredStatus(ctx, stateMgr, slackClient, rec, "error",
			fmt.Sprintf("Recovery check failed: %v", err)); updateErr != nil {
			return RecoveryResult{}, updateErr
		}
		return RecoveryResult{TaskID: rec.TaskID, ChannelID: rec.ChannelID, FinalStatus: "error"}, nil
	}

	var finalStatus string
	switch status {
	case "done", "completed":
		log.Printf("Recovery: task %s is done", rec.TaskID)
		finalStatus = "done"
	case "error", "failed":
		log.Printf("Recovery: task %s failed", rec.TaskID)
		finalStatus = "error"
	case "timeout", "timed_out":
		log.Printf("Recovery: task %s timed out", rec.TaskID)
		finalStatus = "error"
	case "running", "in_progress":
		log.Printf("Recovery: task %s is still running", rec.TaskID)
		finalStatus = "running"
	default:
		log.Printf("Recovery: unknown status %q for task %s — marking as error", status, rec.TaskID)
		finalStatus = "error"
	}

	var message string
	switch finalStatus {
	case "done":
		message = "✅ Recovered: Task completed successfully"
	case "running":
		message = "🔄 Recovered: Task still running"
	case "error":
		message = "❌ Recovered: Task failed"
	}

	if err := updateRecoveredStatus(ctx, stateMgr, slackClient, rec, finalStatus, message); err != nil {
		return RecoveryResult{}, err
	}
	return RecoveryResult{TaskID: rec.TaskID, ChannelID: rec.ChannelID, FinalStatus: finalStatus}, nil
}

func updateRecoveredStatus(
	ctx context.Context,
	stateMgr statemgr.StateManager,
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
