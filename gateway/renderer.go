package gateway

import (
	"fmt"
	"strings"

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

	var blocks []slack.Block

	switch event.Type {
	case protocol.EventTypeStart:
		blocks = buildStartBlocks(event)
	case protocol.EventTypeProgress:
		blocks = buildProgressBlocks(event, taskData)
	case protocol.EventTypeDone:
		blocks = buildDoneBlocks(event, taskData)
	case protocol.EventTypeError:
		blocks = buildErrorBlocks(event, taskData)
	case protocol.EventTypeToolCall:
		blocks = buildToolCallBlocks(event, taskData)
	}

	return blocks
}

// TaskData carries accumulated state across streaming events.
type TaskData struct {
	ChannelID string
	Ts        string
	Model     string
	Tokens    int
}

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

func buildProgressBlocks(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil {
		p = &protocol.EventPayload{}
	}

	progress := 0.0
	if p.Progress > 0 {
		progress = p.Progress
	}
	if progress > 1.0 {
		progress = 1.0
	}

	bar := renderProgressBar(progress)

	action := p.Action
	if action == "" {
		action = "Processing..."
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "⏳ Processing...", true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("```%s```  %.0f%%", bar, progress*100), false, false),
			nil, nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Action:* %s", action), false, false),
			nil, nil,
		),
	}

	// Append tool call card if present
	if p.Tool != nil {
		blocks = append(blocks, buildToolCallBlocks(event, taskData)...)
	}

	// Metadata footer
	footerParts := []string{}
	if taskData != nil && taskData.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", taskData.Model))
	}
	if p.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", p.Model))
	}
	if taskData != nil && taskData.Tokens > 0 {
		footerParts = append(footerParts, fmt.Sprintf("📊 %d tokens", taskData.Tokens))
	}
	if p.Tokens > 0 {
		footerParts = append(footerParts, fmt.Sprintf("📊 %d tokens", p.Tokens))
	}
	if p.ElapsedTime != "" {
		footerParts = append(footerParts, fmt.Sprintf("⏱️ %s", p.ElapsedTime))
	}

	if len(footerParts) > 0 {
		footer := strings.Join(footerParts, " | ")
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, footer, false, false,
		)))
	}

	return blocks
}

func buildDoneBlocks(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil {
		p = &protocol.EventPayload{}
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "✅ Task Completed", true, false,
		)),
	}

	// Output
	if p.Output != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, p.Output, false, false),
			nil, nil,
		))
	}

	// Code snippet
	if p.Code != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("```%s```", p.Code), false, false),
			nil, nil,
		))
	}

	// Metadata footer
	footerParts := []string{}
	model := p.Model
	if model == "" && taskData != nil {
		model = taskData.Model
	}
	if model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", model))
	}

	tokens := p.Tokens
	if tokens == 0 && taskData != nil {
		tokens = taskData.Tokens
	}
	if tokens > 0 {
		footerParts = append(footerParts, fmt.Sprintf("📊 %d tokens", tokens))
	}
	if p.ElapsedTime != "" {
		footerParts = append(footerParts, fmt.Sprintf("⏱️ %s", p.ElapsedTime))
	}

	if len(footerParts) > 0 {
		footer := strings.Join(footerParts, " | ")
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, footer, false, false,
		)))
	}

	// Action buttons: Copy Code, Retry, New Task
	blocks = append(blocks, buildActionButtons(event)...)

	return blocks
}

func buildErrorBlocks(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil {
		p = &protocol.EventPayload{}
	}

	msg := p.Message
	if msg == "" {
		msg = "An unknown error occurred."
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "❌ Task Failed", true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Error:* %s", msg), false, false),
			nil, nil,
		),
	}

	footerParts := []string{}
	if p.ElapsedTime != "" {
		footerParts = append(footerParts, fmt.Sprintf("⏱️ %s", p.ElapsedTime))
	}
	if len(footerParts) > 0 {
		footer := strings.Join(footerParts, " | ")
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, footer, false, false,
		)))
	}

	// Retry button
	blocks = append(blocks, buildErrorActionButtons(event)...)

	return blocks
}

// buildToolCallBlocks creates Block Kit blocks for a tool call event.
func buildToolCallBlocks(event *protocol.SlackEvent, _ *TaskData) []slack.Block {
	p := event.Payload
	if p == nil || p.Tool == nil {
		return nil
	}

	tool := p.Tool
	toolText := fmt.Sprintf("🔧 *%s*", tool.Name)
	if tool.Args != "" {
		toolText += fmt.Sprintf("\n*Args:* ```%s```", tool.Args)
	}
	if tool.Result != "" {
		toolText += fmt.Sprintf("\n*Result:* ```%s```", tool.Result)
	}

	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, toolText, false, false),
			nil, nil,
		),
	}
}

// buildActionButtons creates action buttons for the Done state.
func buildActionButtons(event *protocol.SlackEvent) []slack.Block {
	elements := []slack.BlockElement{
		slack.NewButtonBlockElement("copy_code", "", slack.NewTextBlockObject(
			slack.PlainTextType, "📋 Copy Code", false, false,
		)),
		slack.NewButtonBlockElement("retry_task", "", slack.NewTextBlockObject(
			slack.PlainTextType, "🔄 Retry", false, false,
		)).WithStyle(slack.StylePrimary),
		slack.NewButtonBlockElement("new_task", "", slack.NewTextBlockObject(
			slack.PlainTextType, "📝 New Task", false, false,
		)),
	}

	return []slack.Block{
		slack.NewActionBlock("done_actions", elements...),
	}
}

// buildErrorActionButtons creates a Retry button for the Error state.
func buildErrorActionButtons(event *protocol.SlackEvent) []slack.Block {
	elements := []slack.BlockElement{
		slack.NewButtonBlockElement("retry_task", "", slack.NewTextBlockObject(
			slack.PlainTextType, "🔄 Retry", false, false,
		)).WithStyle(slack.StyleDanger),
	}

	return []slack.Block{
		slack.NewActionBlock("error_actions", elements...),
	}
}

// renderProgressBar creates a text-based progress bar.
func renderProgressBar(progress float64) string {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	filled := int(progress * float64(progressWidth))
	empty := progressWidth - filled

	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}
