package gateway

import (
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
