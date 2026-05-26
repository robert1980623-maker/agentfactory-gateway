package gateway

import (
	"fmt"
	"strings"

	"github.com/agentfactory/gateway/protocol"

	"github.com/slack-go/slack"
)

// stepTitle maps a paused_step value to a human-readable name and emoji.
func stepTitle(step string) (string, string) {
	switch step {
	case "architect":
		return "Architect Design", "📐"
	case "developer":
		return "Developer Code", "💻"
	case "reviewer":
		return "Reviewer", "🔍"
	case "planner":
		return "Planner", "📋"
	case "devops":
		return "DevOps", "🚀"
	default:
		return step, "⏸️"
	}
}

// buildReviewCard creates a Slack Block Kit card for a HITL pause event.
// It renders a clean review card with Approve/Request Changes/Feedback buttons.
func buildReviewCard(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil || p.ChainContext == nil {
		return buildGenericPauseBlocks(event, taskData)
	}

	cc := p.ChainContext

	// Devops step gets a special Git Review Card with Push/Rebase buttons.
	if cc.PausedStep == "devops" {
		return buildGitReviewCard(event, taskData)
	}

	title, emoji := stepTitle(cc.PausedStep)

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, fmt.Sprintf("⏸️ PAUSED: %s Review", title), true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("%s The agent has paused at the **%s** step and is awaiting your review.", emoji, title),
				false, false),
			nil, nil,
		),
	}

	if cc.DesignDoc != "" {
		designText := cc.DesignDoc
		if len(designText) > 2000 {
			designText = designText[:2000] + "...\n_(truncated)_"
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("*Design Summary:*\n%s", designText), false, false),
			nil, nil,
		))
	}

	if cc.ModificationLog != "" {
		blocks = append(blocks, buildModificationLogCard(cc.ModificationLog)...)
	}

	if cc.FeedbackRequired {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"💡 *Feedback required* — please provide your comments before approving.", false, false),
			nil, nil,
		))
	}

	blocks = append(blocks, slack.NewDividerBlock())

	footerParts := []string{}
	if taskData != nil && taskData.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", taskData.Model))
	}
	if p.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", p.Model))
	}
	if p.ElapsedTime != "" {
		footerParts = append(footerParts, fmt.Sprintf("⏱️ %s", p.ElapsedTime))
	}
	if len(footerParts) > 0 {
		footer := strings.Join(footerParts, " | ")
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, footer, false, false,
		)))
	}

	blocks = append(blocks, buildHITLActionButtons(event)...)

	return blocks
}

// buildGenericPauseBlocks creates a fallback pause card when ChainContext is missing.
func buildGenericPauseBlocks(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil {
		p = &protocol.EventPayload{}
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, "⏸️ Task Paused", true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"The task has been paused and is awaiting your input.", false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
	}

	blocks = append(blocks, buildHITLActionButtons(event)...)

	return blocks
}

// buildModificationLogCard renders the MODIFICATION_LOG.md content in a code block.
func buildModificationLogCard(logContent string) []slack.Block {
	if logContent == "" {
		return nil
	}

	if len(logContent) > 2500 {
		logContent = logContent[:2500] + "\n_(truncated)_"
	}

	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Modification Log:*", false, false),
			nil, nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("```%s```", logContent), false, false),
			nil, nil,
		),
	}
}

// buildHITLActionButtons creates Approve / Request Changes / Give Feedback buttons.
func buildHITLActionButtons(event *protocol.SlackEvent) []slack.Block {
	taskID := ""
	channelID := ""
	if event.Payload != nil {
		taskID = event.Payload.TaskID
		channelID = event.Payload.ChannelID
	}
	valuePayload := taskID
	if channelID != "" {
		valuePayload = taskID + "|" + channelID
	}

	elements := []slack.BlockElement{
		slack.NewButtonBlockElement("hitl_approve", valuePayload, slack.NewTextBlockObject(
			slack.PlainTextType, "✅ Approve", false, false,
		)).WithStyle(slack.StylePrimary),
		slack.NewButtonBlockElement("hitl_reject", valuePayload, slack.NewTextBlockObject(
			slack.PlainTextType, "📝 Request Changes", false, false,
		)).WithStyle(slack.StyleDanger),
		slack.NewButtonBlockElement("hitl_feedback", valuePayload, slack.NewTextBlockObject(
			slack.PlainTextType, "💬 Give Feedback", false, false,
		)),
	}

	return []slack.Block{
		slack.NewActionBlock("hitl_actions", elements...),
	}
}

// buildGitReviewCard creates a Slack Block Kit card for the DevOps HITL step.
// It shows git status summary and provides Push/Rebase/Manual action buttons.
func buildGitReviewCard(event *protocol.SlackEvent, taskData *TaskData) []slack.Block {
	p := event.Payload
	if p == nil || p.ChainContext == nil {
		return buildGenericPauseBlocks(event, taskData)
	}

	cc := p.ChainContext

	// Determine header based on whether we have git status info.
	headerText := "🚀 DevOps Ready"
	if cc.GitStatusSummary == "" {
		headerText = "⚠️ Git Pre-flight"
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(
			slack.PlainTextType, headerText, true, false,
		)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"The agent has completed git pre-flight checks. Choose an action below:",
				false, false),
			nil, nil,
		),
	}

	// Show git status summary in a code block.
	if cc.GitStatusSummary != "" {
		summary := cc.GitStatusSummary
		if len(summary) > 3000 {
			summary = summary[:3000] + "\n_(truncated)_"
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("*Git Status:*\n```%s```", summary), false, false),
			nil, nil,
		))
	}

	blocks = append(blocks, slack.NewDividerBlock())

	// Footer
	footerParts := []string{}
	if taskData != nil && taskData.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", taskData.Model))
	}
	if p.Model != "" {
		footerParts = append(footerParts, fmt.Sprintf("🤖 %s", p.Model))
	}
	if p.ElapsedTime != "" {
		footerParts = append(footerParts, fmt.Sprintf("⏱️ %s", p.ElapsedTime))
	}
	if len(footerParts) > 0 {
		footer := strings.Join(footerParts, " | ")
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, footer, false, false,
		)))
	}

	blocks = append(blocks, buildGitReviewActionButtons(event)...)

	return blocks
}

// buildGitReviewActionButtons creates Push & PR / Rebase & Push / Manual buttons.
func buildGitReviewActionButtons(event *protocol.SlackEvent) []slack.Block {
	taskID := ""
	channelID := ""
	if event.Payload != nil {
		taskID = event.Payload.TaskID
		channelID = event.Payload.ChannelID
	}
	valuePayload := taskID
	if channelID != "" {
		valuePayload = taskID + "|" + channelID
	}

	elements := []slack.BlockElement{
		slack.NewButtonBlockElement("git_push", valuePayload, slack.NewTextBlockObject(
			slack.PlainTextType, "🚀 Push & PR", false, false,
		)).WithStyle(slack.StylePrimary),
		slack.NewButtonBlockElement("git_rebase", valuePayload, slack.NewTextBlockObject(
			slack.PlainTextType, "🔄 Rebase & Push", false, false,
		)).WithStyle(slack.StyleDanger),
		slack.NewButtonBlockElement("git_manual", valuePayload, slack.NewTextBlockObject(
			slack.PlainTextType, "📝 Manual", false, false,
		)),
	}

	return []slack.Block{
		slack.NewActionBlock("git_review_actions", elements...),
	}
}
