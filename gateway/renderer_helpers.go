package gateway

import (
	"fmt"
	"strings"

	"github.com/agentfactory/gateway/protocol"

	"github.com/slack-go/slack"
)

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
