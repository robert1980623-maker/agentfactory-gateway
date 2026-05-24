package worker

import (
	"strings"
	"testing"

	"github.com/agentfactory/gateway/protocol"
)

func TestClineAdapter_ParseLine_ToolCall_WriteFile(t *testing.T) {
	a := NewClineAdapter()

	tests := []struct {
		input    string
		wantType protocol.SlackEventType
		wantTool string
		wantArgs string
	}{
		{
			input:    "Writing to file: src/main.go",
			wantType: protocol.EventTypeToolCall,
			wantTool: "write_file",
			wantArgs: "src/main.go",
		},
		{
			input:    "Writing to file src/utils/helper.py",
			wantType: protocol.EventTypeToolCall,
			wantTool: "write_file",
			wantArgs: "src/utils/helper.py",
		},
		{
			input:    "Reading file: config.yaml",
			wantType: protocol.EventTypeToolCall,
			wantTool: "read_file",
			wantArgs: "config.yaml",
		},
		{
			input:    "Running: go test ./...",
			wantType: protocol.EventTypeToolCall,
			wantTool: "run_command",
			wantArgs: "go test ./...",
		},
		{
			input:    "Executing: make build",
			wantType: protocol.EventTypeToolCall,
			wantTool: "run_command",
			wantArgs: "make build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			event := a.parseLine(tt.input)
			if event == nil {
				t.Fatalf("expected event, got nil")
			}
			if event.Type != tt.wantType {
				t.Errorf("type = %q, want %q", event.Type, tt.wantType)
			}
			if event.Payload == nil || event.Payload.Tool == nil {
				t.Fatalf("expected tool info in payload")
			}
			if event.Payload.Tool.Name != tt.wantTool {
				t.Errorf("tool name = %q, want %q", event.Payload.Tool.Name, tt.wantTool)
			}
			if event.Payload.Tool.Args != tt.wantArgs {
				t.Errorf("tool args = %q, want %q", event.Payload.Tool.Args, tt.wantArgs)
			}
		})
	}
}

func TestClineAdapter_ParseLine_Done(t *testing.T) {
	a := NewClineAdapter()

	inputs := []string{
		"Tests passed",
		"All tests passed successfully",
		"Task complete",
		"Finished",
		"All done",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			event := a.parseLine(input)
			if event == nil {
				t.Fatalf("expected event, got nil for %q", input)
			}
			if event.Type != protocol.EventTypeDone {
				t.Errorf("type = %q, want %q", event.Type, protocol.EventTypeDone)
			}
		})
	}
}

func TestClineAdapter_ParseLine_Error(t *testing.T) {
	a := NewClineAdapter()

	inputs := []struct {
		input       string
		wantMessage string
	}{
		{"Error: file not found", "file not found"},
		{"Error connection timeout", "connection timeout"},
		{"error: something broke", "something broke"},
	}

	for _, tt := range inputs {
		t.Run(tt.input, func(t *testing.T) {
			event := a.parseLine(tt.input)
			if event == nil {
				t.Fatalf("expected event, got nil")
			}
			if event.Type != protocol.EventTypeError {
				t.Errorf("type = %q, want %q", event.Type, protocol.EventTypeError)
			}
			if event.Payload == nil {
				t.Fatalf("expected payload")
			}
			if event.Payload.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", event.Payload.Message, tt.wantMessage)
			}
		})
	}
}

func TestClineAdapter_ParseLine_Progress(t *testing.T) {
	a := NewClineAdapter()

	event := a.parseLine("Processing file contents...")
	if event == nil {
		t.Fatalf("expected event, got nil")
	}
	if event.Type != protocol.EventTypeProgress {
		t.Errorf("type = %q, want %q", event.Type, protocol.EventTypeProgress)
	}
	if event.Payload == nil || event.Payload.Action != "Processing file contents..." {
		t.Errorf("action = %q, want %q", event.Payload.Action, "Processing file contents...")
	}
}

func TestClineAdapter_ParseLine_SkipsEmpty(t *testing.T) {
	a := NewClineAdapter()

	skips := []string{"", "   ", "\x1b[32m\x1b[0m", "ok", "x"}
	for _, input := range skips {
		t.Run(input, func(t *testing.T) {
			event := a.parseLine(input)
			if event != nil {
				t.Errorf("expected nil for %q, got %+v", input, event)
			}
		})
	}
}

func TestClineAdapter_ParseLine_ANSIStripping(t *testing.T) {
	a := NewClineAdapter()

	// ANSI-colored error line
	input := "\x1b[31mError: compilation failed\x1b[0m"
	event := a.parseLine(input)
	if event == nil {
		t.Fatalf("expected event, got nil")
	}
	if event.Type != protocol.EventTypeError {
		t.Errorf("type = %q, want %q", event.Type, protocol.EventTypeError)
	}
}

func TestClineAdapter_Stream(t *testing.T) {
	a := NewClineAdapter()

	input := `Writing to file: main.go
Some progress output here
Reading file: config.yaml
Running: go test ./...
Tests passed
`

	var events []*protocol.SlackEvent
	reader := strings.NewReader(input)

	err := a.Stream(reader, func(event *protocol.SlackEvent, err error) {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	expectedTypes := []protocol.SlackEventType{
		protocol.EventTypeToolCall,  // Writing to file
		protocol.EventTypeProgress,  // Some progress
		protocol.EventTypeToolCall,  // Reading file
		protocol.EventTypeToolCall,  // Running
		protocol.EventTypeDone,      // Tests passed
	}

	for i, et := range expectedTypes {
		if events[i].Type != et {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, et)
		}
	}
}

func TestClineAdapter_Stream_SkipsNonMatching(t *testing.T) {
	a := NewClineAdapter()

	input := `Some random output
More output
Tests passed
`

	var events []*protocol.SlackEvent
	reader := strings.NewReader(input)

	err := a.Stream(reader, func(event *protocol.SlackEvent, err error) {
		if err != nil {
			return
		}
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	// "Some random output" (len > 3) -> progress
	// "More output" (len > 3) -> progress
	// "Tests passed" -> done
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}
