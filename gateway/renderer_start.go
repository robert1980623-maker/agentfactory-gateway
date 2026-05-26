package gateway

import (
	"fmt"

	"github.com/agentfactory/gateway/protocol"

	"github.com/slack-go/slack"
)

func buildStartBlocks(event *protocol.SlackEvent) []slack.Block {
	p := event.Payload
	if p == nil {
		p = &protocol.EventPayload{}
	}

	userInput := p.UserInput
	if userInput == "" {
		userInput = "_No input provided_"
	}

	return []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "🚀 Task Started", true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Input:* %s", userInput), false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
		slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, "Initializing task...", false, false,
		)),
	}
}

func buildDispatchStartBlocks(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil {
		p = &protocol.EventPayload{}
	}

	userInput := p.UserInput
	if userInput == "" {
		userInput = "_No input provided_"
	}

	agentsText := fmt.Sprintf("Launching %d agents...", p.TotalAgents)

	return []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "⚡ Dispatch Started", true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Input:* %s", userInput), false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
		slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, agentsText, false, false,
		)),
	}
}
