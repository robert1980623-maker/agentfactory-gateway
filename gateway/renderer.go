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

// buildAgentGrid creates grid rows for 6-15 agents (3 per line).
func buildAgentGrid(agents []protocol.SubAgentInfo) []string {
	var rows []string
	currentRow := ""
	cols := 0
	for _, agent := range agents {
		emoji := agentEmoji(agent.Role)
		shortID := agent.AgentID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		statusIcon := "⏳"
		if agent.Status == "done" {
			statusIcon = "✅"
		} else if agent.Status == "error" {
			statusIcon = "❌"
		}
		item := fmt.Sprintf("%s %s [%.0f%%] %s", emoji, shortID, agent.Progress*100, statusIcon)
		
		if cols == 0 {
			currentRow = item
		} else {
			currentRow += "  |  " + item
		}
		cols++
		
		if cols >= 3 {
			rows = append(rows, currentRow)
			currentRow = ""
			cols = 0
		}
	}
	if currentRow != "" {
		rows = append(rows, currentRow)
	}
	return rows
}

// buildAgentSummary creates a summary section for >15 agents.
func buildAgentSummary(agents []protocol.SubAgentInfo) []slack.Block {
	doneCount := 0
	runCount := 0
	errCount := 0
	for _, a := range agents {
		switch a.Status {
		case "done":
			doneCount++
		case "error":
			errCount++
		default:
			runCount++
		}
	}
	
	summary := fmt.Sprintf("✅ %d done  |  ⏳ %d running  |  ❌ %d error", doneCount, runCount, errCount)
	
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, summary, false, false),
			nil, nil,
		),
		slack.NewContextBlock("", slack.NewTextBlockObject(
			slack.PlainTextType, "[View details in thread →]", false, false,
		)),
	}
}

// agentEmoji maps agent roles to emojis.
func agentEmoji(role string) string {
	switch role {
	case "db", "database", "model":
		return "🤖"
	case "test", "tests", "qa":
		return "🧪"
	case "docker", "deploy", "ops", "devops":
		return "🐳"
	case "doc", "docs", "writer":
		return "📝"
	case "search", "research":
		return "🔍"
	default:
		return "🤖"
	}
}

// renderAgentRow formats a single agent line for dispatch view.
func renderAgentRow(agent protocol.SubAgentInfo) string {
	emoji := agentEmoji(agent.Role)
	bar := renderProgressBar(agent.Progress)
	action := agent.CurrentAction
	if action == "" {
		action = "Processing..."
	}
	statusIcon := "⏳"
	if agent.Status == "done" {
		statusIcon = "✅"
	} else if agent.Status == "error" {
		statusIcon = "❌"
	}

	return fmt.Sprintf("%s %s  [%s]  %.0f%%  %s %s", emoji, agent.AgentID, bar, agent.Progress*100, statusIcon, action)
}

// buildDispatchStartBlocks creates blocks for a dispatch task start.
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

// buildDispatchBlocks creates blocks for multi-agent dispatch progress.
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
