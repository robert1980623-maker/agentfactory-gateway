package gateway

import (
	"fmt"

	"github.com/agentfactory/gateway/protocol"

	"github.com/slack-go/slack"
)

// progressWidth is the number of characters in the progress bar.
const progressWidth = 10

// BuildTaskCard builds a Slack Block Kit message from a streaming event.
func BuildTaskCard(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	if event == nil {
		return nil
	}

	p := event.Payload
	var blocks []slack.Block

	switch event.Type {
	case protocol.EventTypeStart:
		if p != nil && p.TaskType == "dispatch" {
			blocks = buildDispatchStartBlocks(event, taskData)
		} else {
			blocks = buildStartBlocks(event)
		}
	case protocol.EventTypeProgress:
		if p != nil && len(p.Agents) > 0 {
			blocks = buildDispatchBlocks(event, taskData)
		} else {
			blocks = buildProgressBlocks(event, taskData)
		}
	case protocol.EventTypeDone:
		blocks = buildDoneBlocks(event, taskData)
	case protocol.EventTypeError:
		blocks = buildErrorBlocks(event, taskData)
	case protocol.EventTypeToolCall:
		blocks = buildToolCallBlocks(event, taskData)
	case protocol.EventTypePaused:
		blocks = buildReviewCard(event, taskData)
	}

	return blocks
}

// BuildQueuedCard creates a Slack Block Kit card for a queued task.
func BuildQueuedCard(position int, taskID string) []slack.Block {
	return []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "📋 Task Queued", true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("Position *#%d* in queue — your task will start automatically when the current task completes.", position),
				false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
		slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, fmt.Sprintf("Task ID: %s", taskID), false, false,
		)),
	}
}

// TaskData carries accumulated state across streaming events.
type TaskData struct {
	ChannelID string
	Ts        string
	Model     string
	Tokens    int
}
