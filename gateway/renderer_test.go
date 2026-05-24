package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentfactory/gateway/protocol"

	"github.com/slack-go/slack"
)

func TestBuildTaskCard_Start(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeStart,
		Payload: &protocol.EventPayload{
			UserInput: "Write a hello world program",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for start event")
	}

	// Expect: Header, Section (input), Divider, Context
	if len(blocks) != 4 {
		t.Errorf("expected 4 blocks, got %d", len(blocks))
	}

	// Check header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "🚀 Task Started" {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	// Check input section
	section, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatal("second block should be SectionBlock")
	}
	if section.Text.Text != "*Input:* Write a hello world program" {
		t.Errorf("unexpected input text: %s", section.Text.Text)
	}
}

func TestBuildTaskCard_Start_NoInput(t *testing.T) {
	event := &protocol.SlackEvent{
		Type:    protocol.EventTypeStart,
		Payload: &protocol.EventPayload{},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for start event")
	}

	section, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatal("second block should be SectionBlock")
	}
	if section.Text.Text != "*Input:* _No input provided_" {
		t.Errorf("unexpected default input text: %s", section.Text.Text)
	}
}

func TestBuildTaskCard_Progress(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			Progress:    0.5,
			Action:      "Generating response",
			Model:       "gpt-4",
			Tokens:      1200,
			ElapsedTime: "5.2s",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for progress event")
	}

	// Check header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "⏳ Processing..." {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	// Check progress bar section
	progressSection, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatal("second block should be SectionBlock")
	}
	if progressSection.Text.Text != "```█████░░░░░```  50%" {
		t.Errorf("unexpected progress bar: %s", progressSection.Text.Text)
	}

	// Check action section
	actionSection, ok := blocks[2].(*slack.SectionBlock)
	if !ok {
		t.Fatal("third block should be SectionBlock")
	}
	if actionSection.Text.Text != "*Action:* Generating response" {
		t.Errorf("unexpected action text: %s", actionSection.Text.Text)
	}

	// Check footer context (should include model, tokens, elapsed time)
	ctxBlock, ok := blocks[4].(*slack.ContextBlock)
	if !ok {
		t.Fatal("fifth block should be ContextBlock")
	}
	footerText := ctxBlock.ContextElements.Elements[0].(*slack.TextBlockObject).Text
	if footerText != "🤖 gpt-4 | 📊 1200 tokens | ⏱️ 5.2s" {
		t.Errorf("unexpected footer: %s", footerText)
	}
}

func TestBuildTaskCard_Done(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeDone,
		Payload: &protocol.EventPayload{
			Output:      "**Response generated successfully.**",
			Code:        "print('hello')",
			Model:       "gpt-4",
			Tokens:      2500,
			ElapsedTime: "10.1s",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for done event")
	}

	// Check header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "✅ Task Completed" {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	// Check output section
	outputSection, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatal("second block should be SectionBlock")
	}
	if outputSection.Text.Text != "**Response generated successfully.**" {
		t.Errorf("unexpected output text: %s", outputSection.Text.Text)
	}

	// Check code section
	codeSection, ok := blocks[2].(*slack.SectionBlock)
	if !ok {
		t.Fatal("third block should be SectionBlock")
	}
	if codeSection.Text.Text != "```print('hello')```" {
		t.Errorf("unexpected code text: %s", codeSection.Text.Text)
	}

	// Check footer
	ctxBlock, ok := blocks[4].(*slack.ContextBlock)
	if !ok {
		t.Fatal("fifth block should be ContextBlock")
	}
	footerText := ctxBlock.ContextElements.Elements[0].(*slack.TextBlockObject).Text
	if footerText != "🤖 gpt-4 | 📊 2500 tokens | ⏱️ 10.1s" {
		t.Errorf("unexpected footer: %s", footerText)
	}
}

func TestBuildTaskCard_Error(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeError,
		Payload: &protocol.EventPayload{
			Message: "Connection timeout",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for error event")
	}

	// Check header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "❌ Task Failed" {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	// Check error section
	section, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatal("second block should be SectionBlock")
	}
	if section.Text.Text != "*Error:* Connection timeout" {
		t.Errorf("unexpected error text: %s", section.Text.Text)
	}
}

func TestBuildTaskCard_NilEvent(t *testing.T) {
	blocks := BuildTaskCard(nil, nil)
	if blocks != nil {
		t.Errorf("expected nil blocks for nil event, got %d blocks", len(blocks))
	}
}

func TestBuildTaskCard_Progress_UsesTaskData(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			Progress: 0.75,
			Action:   "Thinking...",
		},
	}

	taskData := &TaskData{
		Model:  "claude-3",
		Tokens: 500,
	}

	blocks := BuildTaskCard(event, taskData)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	ctxBlock, ok := blocks[4].(*slack.ContextBlock)
	if !ok {
		t.Fatal("fifth block should be ContextBlock")
	}
	footerText := ctxBlock.ContextElements.Elements[0].(*slack.TextBlockObject).Text
	if footerText != "🤖 claude-3 | 📊 500 tokens" {
		t.Errorf("unexpected footer from taskData: %s", footerText)
	}
}

func TestRenderProgressBar(t *testing.T) {
	tests := []struct {
		progress float64
		expected string
	}{
		{0.0, "░░░░░░░░░░"},
		{0.1, "█░░░░░░░░░"},
		{0.5, "█████░░░░░"},
		{1.0, "██████████"},
		{-0.1, "░░░░░░░░░░"},
		{1.5, "██████████"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := renderProgressBar(tt.progress)
			if result != tt.expected {
				t.Errorf("progressBar(%.2f) = %q, want %q", tt.progress, result, tt.expected)
			}
		})
	}
}

func TestBuildTaskCard_ToolCall(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeToolCall,
		Payload: &protocol.EventPayload{
			Tool: &protocol.ToolInfo{
				Name: "web_search",
				Args: `{"query": "latest Go version"}`,
			},
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for tool_call event")
	}

	// Expect 1 block: Section with tool info
	if len(blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(blocks))
	}

	section, ok := blocks[0].(*slack.SectionBlock)
	if !ok {
		t.Fatal("first block should be SectionBlock")
	}
	if !strings.Contains(section.Text.Text, "🔧") {
		t.Errorf("expected tool icon in text, got: %s", section.Text.Text)
	}
	if !strings.Contains(section.Text.Text, "web_search") {
		t.Errorf("expected tool name in text, got: %s", section.Text.Text)
	}
	if !strings.Contains(section.Text.Text, "latest Go version") {
		t.Errorf("expected args in text, got: %s", section.Text.Text)
	}
}

func TestBuildTaskCard_ToolCall_WithResult(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeToolCall,
		Payload: &protocol.EventPayload{
			Tool: &protocol.ToolInfo{
				Name:   "code_exec",
				Args:   `{"code": "print(42)"}`,
				Result: "42",
			},
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	section, ok := blocks[0].(*slack.SectionBlock)
	if !ok {
		t.Fatal("first block should be SectionBlock")
	}
	if !strings.Contains(section.Text.Text, "*Result:*") {
		t.Errorf("expected result in text, got: %s", section.Text.Text)
	}
}

func TestBuildTaskCard_Progress_WithTool(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			Progress: 0.5,
			Action:   "Running tools",
			Tool: &protocol.ToolInfo{
				Name: "file_read",
				Args: `{"path": "main.go"}`,
			},
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Expect: Header, progress bar, action, tool call section
	if len(blocks) < 4 {
		t.Fatalf("expected at least 4 blocks, got %d", len(blocks))
	}

	// Find the tool call section
	foundTool := false
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "🔧") && strings.Contains(section.Text.Text, "file_read") {
				foundTool = true
				break
			}
		}
	}
	if !foundTool {
		t.Error("expected tool call section in progress blocks")
	}
}

func TestBuildTaskCard_Done_WithActionButtons(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeDone,
		Payload: &protocol.EventPayload{
			Output: "**Done**",
			Code:   "fmt.Println(\"hello\")",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Find the action block
	foundActions := false
	for _, b := range blocks {
		actionBlock, ok := b.(*slack.ActionBlock)
		if ok && actionBlock.BlockID == "done_actions" {
			foundActions = true
			elements := actionBlock.Elements.ElementSet
			if len(elements) != 3 {
				t.Errorf("expected 3 action buttons, got %d", len(elements))
			}

			// Check button texts
			btn0 := elements[0].(*slack.ButtonBlockElement)
			if btn0.Text.Text != "📋 Copy Code" {
				t.Errorf("expected '📋 Copy Code', got %s", btn0.Text.Text)
			}

			btn1 := elements[1].(*slack.ButtonBlockElement)
			if btn1.Text.Text != "🔄 Retry" {
				t.Errorf("expected '🔄 Retry', got %s", btn1.Text.Text)
			}
			if btn1.Style != slack.StylePrimary {
				t.Errorf("expected primary style for Retry, got %s", btn1.Style)
			}

			btn2 := elements[2].(*slack.ButtonBlockElement)
			if btn2.Text.Text != "📝 New Task" {
				t.Errorf("expected '📝 New Task', got %s", btn2.Text.Text)
			}
			break
		}
	}
	if !foundActions {
		t.Error("expected action block with buttons in done state")
	}
}

func TestBuildTaskCard_Error_WithRetryButton(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeError,
		Payload: &protocol.EventPayload{
			Message: "timeout",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Find the action block
	foundRetry := false
	for _, b := range blocks {
		actionBlock, ok := b.(*slack.ActionBlock)
		if ok && actionBlock.BlockID == "error_actions" {
			foundRetry = true
			elements := actionBlock.Elements.ElementSet
			if len(elements) != 1 {
				t.Errorf("expected 1 action button, got %d", len(elements))
			}

			btn := elements[0].(*slack.ButtonBlockElement)
			if btn.Text.Text != "🔄 Retry" {
				t.Errorf("expected '🔄 Retry', got %s", btn.Text.Text)
			}
			if btn.Style != slack.StyleDanger {
				t.Errorf("expected danger style for Retry in error state, got %s", btn.Style)
			}
			if btn.ActionID != "retry_task" {
				t.Errorf("expected action_id 'retry_task', got %s", btn.ActionID)
			}
			break
		}
	}
	if !foundRetry {
		t.Error("expected retry action block in error state")
	}
}

func TestBuildTaskCard_ToolCall_NilTool(t *testing.T) {
	event := &protocol.SlackEvent{
		Type:    protocol.EventTypeToolCall,
		Payload: &protocol.EventPayload{},
	}

	blocks := BuildTaskCard(event, nil)
	// Nil tool should produce nil/empty blocks from buildToolCallBlocks
	if blocks != nil && len(blocks) > 0 {
		t.Errorf("expected nil or empty blocks for nil tool, got %d", len(blocks))
	}
}

func TestBuildTaskCard_DispatchStart(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeStart,
		Payload: &protocol.EventPayload{
			TaskType:    "dispatch",
			UserInput:   "Build API project",
			TotalAgents: 3,
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "⚡ Dispatch Started" {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	ctxBlock, ok := blocks[3].(*slack.ContextBlock)
	if !ok {
		t.Fatal("fourth block should be ContextBlock")
	}
	if !strings.Contains(ctxBlock.ContextElements.Elements[0].(*slack.TextBlockObject).Text, "Launching 3 agents") {
		t.Errorf("expected agent launch text: %s", ctxBlock.ContextElements.Elements[0].(*slack.TextBlockObject).Text)
	}
}

func TestBuildTaskCard_MultiAgentProgress(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			TaskType: "dispatch",
			Agents: []protocol.SubAgentInfo{
				{AgentID: "Agent A", Role: "db", Progress: 0.4, CurrentAction: "Creating models"},
				{AgentID: "Agent B", Role: "tests", Progress: 0.2, CurrentAction: "Writing tests"},
				{AgentID: "Agent C", Role: "docker", Progress: 0.1, CurrentAction: "Configuring"},
			},
			TotalAgents: 3,
			ElapsedTime: "5s",
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Check overall progress in header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "⏳ Dispatch: 23%" {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	// Check agent rows (should have 3 sections for agents)
	agentSections := 0
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "Agent A") {
				agentSections++
				if !strings.Contains(section.Text.Text, "🤖") {
					t.Errorf("expected db agent emoji, got: %s", section.Text.Text)
				}
			}
			if strings.Contains(section.Text.Text, "Agent B") {
				agentSections++
				if !strings.Contains(section.Text.Text, "🧪") {
					t.Errorf("expected tests agent emoji, got: %s", section.Text.Text)
				}
			}
			if strings.Contains(section.Text.Text, "Agent C") {
				agentSections++
				if !strings.Contains(section.Text.Text, "🐳") {
					t.Errorf("expected docker agent emoji, got: %s", section.Text.Text)
				}
			}
		}
	}
	if agentSections != 3 {
		t.Errorf("expected 3 agent sections, got %d", agentSections)
	}

	// Check footer mentions agents active
	foundAgents := false
	for _, b := range blocks {
		if ctx, ok := b.(*slack.ContextBlock); ok {
			text := ctx.ContextElements.Elements[0].(*slack.TextBlockObject).Text
			if strings.Contains(text, "3 agents active") {
				foundAgents = true
			}
		}
	}
	if !foundAgents {
		t.Error("expected 'agents active' in footer")
	}
}

func TestBuildTaskCard_SubtaskDone(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			TaskType: "dispatch",
			Agents: []protocol.SubAgentInfo{
				{AgentID: "Agent A", Role: "db", Progress: 1.0, Status: "done", CurrentAction: "Done"},
				{AgentID: "Agent B", Role: "tests", Progress: 0.5, CurrentAction: "Writing tests"},
			},
			TotalAgents: 2,
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Check Agent A shows ✅
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "Agent A") {
				if !strings.Contains(section.Text.Text, "✅") {
					t.Errorf("expected done icon for Agent A: %s", section.Text.Text)
				}
			}
		}
	}
}

func TestBuildTaskCard_CompactGrid(t *testing.T) {
	var agents []protocol.SubAgentInfo
	for i := 0; i < 8; i++ {
		agents = append(agents, protocol.SubAgentInfo{
			AgentID:       fmt.Sprintf("Agent-%d", i+1),
			Role:          "db",
			Progress:      0.5,
			CurrentAction: "Working",
		})
	}
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			TaskType:    "dispatch",
			Agents:      agents,
			TotalAgents: 8,
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Check grid rows
	gridCount := 0
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "Agent-") && strings.Contains(section.Text.Text, "|") {
				gridCount++
			}
		}
	}
	if gridCount != 3 { // 8 agents / 3 per line = 3 rows
		t.Errorf("expected 3 grid rows, got %d", gridCount)
	}

	// Check total blocks < 50
	if len(blocks) >= 50 {
		t.Errorf("expected <50 blocks, got %d", len(blocks))
	}
}

func TestBuildTaskCard_CompactSummary(t *testing.T) {
	var agents []protocol.SubAgentInfo
	for i := 0; i < 20; i++ {
		status := "running"
		if i%3 == 0 {
			status = "done"
		}
		if i%7 == 0 {
			status = "error"
		}
		agents = append(agents, protocol.SubAgentInfo{
			AgentID: fmt.Sprintf("Agent-%d", i+1),
			Role:    "db",
			Progress: 0.5,
			Status:  status,
		})
	}
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeProgress,
		Payload: &protocol.EventPayload{
			TaskType:    "dispatch",
			Agents:      agents,
			TotalAgents: 20,
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks")
	}

	// Check summary
	foundSummary := false
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "done") && strings.Contains(section.Text.Text, "running") {
				foundSummary = true
			}
		}
	}
	if !foundSummary {
		t.Error("expected summary block with done/running counts")
	}

	// Check total blocks < 50
	if len(blocks) >= 50 {
		t.Errorf("expected <50 blocks, got %d", len(blocks))
	}
}

func TestBuildTaskCard_Paused_Devops(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypePaused,
		Payload: &protocol.EventPayload{
			TaskID:    "test-task-123",
			ChannelID: "C123456",
			Model:     "gpt-4",
			ChainContext: &protocol.ChainContext{
				PausedStep:       "devops",
				GitStatusSummary: "On branch main\nYour branch is ahead of 'origin/main' by 2 commits.\nChanges to be committed:\n  modified: main.go",
			},
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for devops paused event")
	}

	// Check header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "🚀 DevOps Ready" {
		t.Errorf("unexpected header text: %s", header.Text.Text)
	}

	// Check that git status summary is present
	foundGitStatus := false
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "Git Status:") && strings.Contains(section.Text.Text, "main.go") {
				foundGitStatus = true
				break
			}
		}
	}
	if !foundGitStatus {
		t.Error("expected git status summary in blocks")
	}

	// Check action buttons
	foundGitActions := false
	for _, b := range blocks {
		actionBlock, ok := b.(*slack.ActionBlock)
		if ok && actionBlock.BlockID == "git_review_actions" {
			foundGitActions = true
			elements := actionBlock.Elements.ElementSet
			if len(elements) != 3 {
				t.Errorf("expected 3 action buttons, got %d", len(elements))
			}

			// Check button texts
			btn0 := elements[0].(*slack.ButtonBlockElement)
			if btn0.Text.Text != "🚀 Push & PR" {
				t.Errorf("expected '🚀 Push & PR', got %s", btn0.Text.Text)
			}
			if btn0.Style != slack.StylePrimary {
				t.Errorf("expected primary style for Push & PR, got %s", btn0.Style)
			}
			if btn0.ActionID != "git_push" {
				t.Errorf("expected action_id 'git_push', got %s", btn0.ActionID)
			}

			btn1 := elements[1].(*slack.ButtonBlockElement)
			if btn1.Text.Text != "🔄 Rebase & Push" {
				t.Errorf("expected '🔄 Rebase & Push', got %s", btn1.Text.Text)
			}
			if btn1.Style != slack.StyleDanger {
				t.Errorf("expected danger style for Rebase & Push, got %s", btn1.Style)
			}
			if btn1.ActionID != "git_rebase" {
				t.Errorf("expected action_id 'git_rebase', got %s", btn1.ActionID)
			}

			btn2 := elements[2].(*slack.ButtonBlockElement)
			if btn2.Text.Text != "📝 Manual" {
				t.Errorf("expected '📝 Manual', got %s", btn2.Text.Text)
			}
			if btn2.ActionID != "git_manual" {
				t.Errorf("expected action_id 'git_manual', got %s", btn2.ActionID)
			}
			break
		}
	}
	if !foundGitActions {
		t.Error("expected git_review_actions block with buttons")
	}
}

func TestBuildTaskCard_Paused_Devops_NoSummary(t *testing.T) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypePaused,
		Payload: &protocol.EventPayload{
			TaskID:    "test-task-456",
			ChannelID: "C789012",
			ChainContext: &protocol.ChainContext{
				PausedStep: "devops",
			},
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for devops paused event without summary")
	}

	// Check header shows pre-flight warning
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if header.Text.Text != "⚠️ Git Pre-flight" {
		t.Errorf("unexpected header text for empty summary: %s", header.Text.Text)
	}

	// Verify git status section is NOT present
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "Git Status:") {
				t.Error("should not have Git Status section when summary is empty")
			}
		}
	}
}

func TestBuildTaskCard_Paused_Architect(t *testing.T) {
	// Verify non-devops paused steps still use the standard review card.
	event := &protocol.SlackEvent{
		Type: protocol.EventTypePaused,
		Payload: &protocol.EventPayload{
			TaskID:    "test-task-789",
			ChannelID: "C345678",
			ChainContext: &protocol.ChainContext{
				PausedStep:       "architect",
				DesignDoc:        "Design: Create REST API",
				FeedbackRequired: true,
			},
		},
	}

	blocks := BuildTaskCard(event, nil)
	if blocks == nil {
		t.Fatal("expected non-nil blocks for architect paused event")
	}

	// Check header
	header, ok := blocks[0].(*slack.HeaderBlock)
	if !ok {
		t.Fatal("first block should be HeaderBlock")
	}
	if !strings.Contains(header.Text.Text, "Architect Design") {
		t.Errorf("expected 'Architect Design' in header, got: %s", header.Text.Text)
	}

	// Check design doc is present
	foundDesign := false
	for _, b := range blocks {
		if section, ok := b.(*slack.SectionBlock); ok {
			if strings.Contains(section.Text.Text, "Design Summary:") {
				foundDesign = true
				break
			}
		}
	}
	if !foundDesign {
		t.Error("expected design summary in blocks")
	}

	// Verify it uses standard HITL buttons, not git buttons
	foundHitlActions := false
	foundGitActions := false
	for _, b := range blocks {
		actionBlock, ok := b.(*slack.ActionBlock)
		if ok {
			if actionBlock.BlockID == "hitl_actions" {
				foundHitlActions = true
			}
			if actionBlock.BlockID == "git_review_actions" {
				foundGitActions = true
			}
		}
	}
	if !foundHitlActions {
		t.Error("expected hitl_actions block for architect step")
	}
	if foundGitActions {
		t.Error("should not have git_review_actions for architect step")
	}
}
