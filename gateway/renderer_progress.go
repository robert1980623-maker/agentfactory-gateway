package gateway

import (
	"fmt"
	"strings"

	"github.com/agentfactory/gateway/protocol"

	"github.com/slack-go/slack"
)

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

func buildDispatchBlocks(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil || len(p.Agents) == 0 {
		return buildProgressBlocks(event, taskData)
	}

	// Calculate overall progress
	totalProgress := 0.0
	for _, a := range p.Agents {
		totalProgress += a.Progress
	}
	overallProgress := totalProgress / float64(len(p.Agents))
	if overallProgress > 1.0 {
		overallProgress = 1.0
	}

	// Header
	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, fmt.Sprintf("⏳ Dispatch: %.0f%%", overallProgress*100), true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("```%s```  %.0f%%", renderProgressBar(overallProgress), overallProgress*100), false, false),
			nil, nil,
		),
	}

	// Agent rows with Compact Mode
	agentCount := len(p.Agents)
	switch {
	case agentCount <= 5:
		// Level 1: Full rows
		for _, agent := range p.Agents {
			blocks = append(blocks, slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("```\n%s\n```", renderAgentRow(agent)), false, false),
				nil, nil,
			))
		}
	case agentCount <= 15:
		// Level 2: Grid format (3 per line)
		gridRows := buildAgentGrid(p.Agents)
		for _, row := range gridRows {
			blocks = append(blocks, slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, row, false, false),
				nil, nil,
			))
		}
	default:
		// Level 3: Summary only
		blocks = append(blocks, buildAgentSummary(p.Agents)...)
	}

	// Tool call for specific subtask
	if p.Tool != nil && p.SubTaskID != "" {
		toolText := fmt.Sprintf("🔧 *%s* (%s)", p.Tool.Name, p.SubTaskID)
		if p.Tool.Args != "" {
			toolText += fmt.Sprintf("\n*Args:* ```%s```", p.Tool.Args)
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, toolText, false, false),
			nil, nil,
		))
	}

	// Metadata footer
	footerParts := []string{}
	if p.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", p.Model))
	} else if taskData != nil && taskData.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", taskData.Model))
	}
	if p.TotalAgents > 0 {
		footerParts = append(footerParts, fmt.Sprintf("👥 %d agents active", p.TotalAgents))
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
