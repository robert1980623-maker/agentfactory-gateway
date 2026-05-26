package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseJSONL_Start(t *testing.T) {
	line := `{"type":"start","payload":{"user_input":"hello","task_id":"abc-123"}}`

	var event SlackEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("failed to parse JSONL: %v", err)
	}

	if event.Type != EventTypeStart {
		t.Errorf("expected type %q, got %q", EventTypeStart, event.Type)
	}
	if event.Payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if event.Payload.UserInput != "hello" {
		t.Errorf("expected user_input %q, got %q", "hello", event.Payload.UserInput)
	}
	if event.Payload.TaskID != "abc-123" {
		t.Errorf("expected task_id %q, got %q", "abc-123", event.Payload.TaskID)
	}
}

func TestParseJSONL_Progress(t *testing.T) {
	line := `{"type":"progress","payload":{"progress":0.5,"action":"generating","model":"gpt-4","tokens":1200,"elapsed_time":"5.2s"}}`

	var event SlackEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("failed to parse JSONL: %v", err)
	}

	if event.Type != EventTypeProgress {
		t.Errorf("expected type %q, got %q", EventTypeProgress, event.Type)
	}
	p := event.Payload
	if p == nil {
		t.Fatal("expected non-nil payload")
	}
	if p.Progress != 0.5 {
		t.Errorf("expected progress 0.5, got %f", p.Progress)
	}
	if p.Action != "generating" {
		t.Errorf("expected action %q, got %q", "generating", p.Action)
	}
	if p.Model != "gpt-4" {
		t.Errorf("expected model %q, got %q", "gpt-4", p.Model)
	}
	if p.Tokens != 1200 {
		t.Errorf("expected tokens 1200, got %d", p.Tokens)
	}
}

func TestParseJSONL_Done(t *testing.T) {
	line := `{"type":"done","payload":{"output":"**Done!**","code":"print(1)","tokens":2500,"model":"gpt-4","elapsed_time":"10s"}}`

	var event SlackEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("failed to parse JSONL: %v", err)
	}

	if event.Type != EventTypeDone {
		t.Errorf("expected type %q, got %q", EventTypeDone, event.Type)
	}
	p := event.Payload
	if p == nil {
		t.Fatal("expected non-nil payload")
	}
	if p.Output != "**Done!**" {
		t.Errorf("expected output %q, got %q", "**Done!**", p.Output)
	}
	if p.Code != "print(1)" {
		t.Errorf("expected code %q, got %q", "print(1)", p.Code)
	}
}

func TestParseJSONL_Error(t *testing.T) {
	line := `{"type":"error","payload":{"message":"Connection timeout"}}`

	var event SlackEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("failed to parse JSONL: %v", err)
	}

	if event.Type != EventTypeError {
		t.Errorf("expected type %q, got %q", EventTypeError, event.Type)
	}
	if event.Payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if event.Payload.Message != "Connection timeout" {
		t.Errorf("expected message %q, got %q", "Connection timeout", event.Payload.Message)
	}
}

func TestParseJSONL_MultipleLines(t *testing.T) {
	input := `{"type":"start","payload":{"user_input":"test"}}
{"type":"progress","payload":{"progress":0.5,"action":"working"}}
{"type":"done","payload":{"output":"result"}}`

	scanner := func(input string) ([]SlackEvent, error) {
		var events []SlackEvent
		lines := strings.Split(input, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var event SlackEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				return nil, err
			}
			events = append(events, event)
		}
		return events, nil
	}

	events, err := scanner(input)
	if err != nil {
		t.Fatalf("failed to parse multi-line JSONL: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	expectedTypes := []SlackEventType{EventTypeStart, EventTypeProgress, EventTypeDone}
	for i, et := range expectedTypes {
		if events[i].Type != et {
			t.Errorf("event[%d] type = %q, want %q", i, events[i].Type, et)
		}
	}
}

func TestParseJSONL_InvalidLine(t *testing.T) {
	line := `this is not json`

	var event SlackEvent
	err := json.Unmarshal([]byte(line), &event)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseJSONL_EmptyPayload(t *testing.T) {
	line := `{"type":"start"}`

	var event SlackEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("failed to parse JSONL: %v", err)
	}

	if event.Type != EventTypeStart {
		t.Errorf("expected type %q, got %q", EventTypeStart, event.Type)
	}
	// Payload can be nil when absent.
	if event.Payload != nil {
		t.Error("expected nil payload for empty payload event")
	}
}

func TestSlackEventTypeConstants(t *testing.T) {
	if EventTypeStart != "start" {
		t.Errorf("EventTypeStart = %q, want %q", EventTypeStart, "start")
	}
	if EventTypeProgress != "progress" {
		t.Errorf("EventTypeProgress = %q, want %q", EventTypeProgress, "progress")
	}
	if EventTypeDone != "done" {
		t.Errorf("EventTypeDone = %q, want %q", EventTypeDone, "done")
	}
	if EventTypeError != "error" {
		t.Errorf("EventTypeError = %q, want %q", EventTypeError, "error")
	}
}

func TestTaskRequest_DispatchField(t *testing.T) {
	// Default (zero value) should omit dispatch from JSON.
	req := TaskRequest{Task: "hello"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if strings.Contains(string(data), "dispatch") {
		t.Errorf("expected 'dispatch' to be omitted for zero value, got: %s", string(data))
	}

	// When true, dispatch should appear in JSON.
	req.Dispatch = true
	data, err = json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if !strings.Contains(string(data), `"dispatch":true`) {
		t.Errorf("expected 'dispatch':true in JSON, got: %s", string(data))
	}

	// Round-trip: unmarshal should restore the field.
	var decoded TaskRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !decoded.Dispatch {
		t.Error("expected Dispatch=true after round-trip, got false")
	}
}

func TestTaskRequest_DispatchUnmarshal_DefaultFalse(t *testing.T) {
	// When dispatch is absent in JSON, it should default to false.
	data := `{"task":"hello"}`
	var req TaskRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if req.Dispatch {
		t.Error("expected Dispatch=false when absent from JSON, got true")
	}
}
