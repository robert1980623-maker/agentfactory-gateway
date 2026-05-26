package worker

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentfactory/gateway/protocol"
)

// TestJSONLStreamParsing verifies that a bufio.Scanner reading
// line-by-line correctly parses JSONL events (mimics the stream worker's core loop).
func TestJSONLStreamParsing(t *testing.T) {
	input := `{"type":"start","payload":{"user_input":"hello","task_id":"t1"}}
{"type":"progress","payload":{"progress":0.25,"action":"init"}}
{"type":"progress","payload":{"progress":0.75,"action":"processing","model":"gpt-4","tokens":800}}
{"type":"done","payload":{"output":"done result","code":"x=1","model":"gpt-4","tokens":1500,"elapsed_time":"8.3s"}}
`

	scanner := bufio.NewScanner(strings.NewReader(input))

	var events []protocol.SlackEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event protocol.SlackEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Logf("skipping non-JSON line: %s", line)
			continue
		}
		events = append(events, event)
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Verify types
	expected := []protocol.SlackEventType{
		protocol.EventTypeStart,
		protocol.EventTypeProgress,
		protocol.EventTypeProgress,
		protocol.EventTypeDone,
	}
	for i, et := range expected {
		if events[i].Type != et {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, et)
		}
	}

	// Verify start payload
	if events[0].Payload.UserInput != "hello" {
		t.Errorf("start payload user_input = %q, want %q", events[0].Payload.UserInput, "hello")
	}
	if events[0].Payload.TaskID != "t1" {
		t.Errorf("start payload task_id = %q, want %q", events[0].Payload.TaskID, "t1")
	}

	// Verify progress payloads
	if events[1].Payload.Progress != 0.25 {
		t.Errorf("progress[0] = %f, want 0.25", events[1].Payload.Progress)
	}
	if events[2].Payload.Progress != 0.75 {
		t.Errorf("progress[1] = %f, want 0.75", events[2].Payload.Progress)
	}
	if events[2].Payload.Model != "gpt-4" {
		t.Errorf("progress[1] model = %q, want %q", events[2].Payload.Model, "gpt-4")
	}

	// Verify done payload
	if events[3].Payload.Output != "done result" {
		t.Errorf("done output = %q, want %q", events[3].Payload.Output, "done result")
	}
	if events[3].Payload.Code != "x=1" {
		t.Errorf("done code = %q, want %q", events[3].Payload.Code, "x=1")
	}
}

// TestJSONLStreamParsing_SkipsInvalidLines verifies that non-JSON lines
// (e.g. debug output) are silently skipped.
func TestJSONLStreamParsing_SkipsInvalidLines(t *testing.T) {
	input := `{"type":"start","payload":{"user_input":"test"}}
DEBUG: some debug output
{"type":"done","payload":{"output":"result"}}
`

	scanner := bufio.NewScanner(strings.NewReader(input))

	var events []protocol.SlackEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event protocol.SlackEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // skip invalid lines
		}
		events = append(events, event)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events (skipping debug line), got %d", len(events))
	}
	if events[0].Type != protocol.EventTypeStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, protocol.EventTypeStart)
	}
	if events[1].Type != protocol.EventTypeDone {
		t.Errorf("second event type = %q, want %q", events[1].Type, protocol.EventTypeDone)
	}
}

// TestJSONLStreamParsing_DispatchEvent verifies that dispatch events
// (with an agents array in the payload) are parsed correctly.
func TestJSONLStreamParsing_DispatchEvent(t *testing.T) {
	input := `{"type":"start","payload":{"task_id":"d1","task_type":"dispatch","total_agents":3}}
{"type":"progress","payload":{"progress":0.3,"agents":[{"agent_id":"a1","role":"researcher","progress":0.5,"status":"running"},{"agent_id":"a2","role":"coder","progress":0.2,"status":"running"},{"agent_id":"a3","role":"reviewer","progress":0.0,"status":"pending"}]}}
{"type":"done","payload":{"output":"all agents complete","total_agents":3}}
`

	scanner := bufio.NewScanner(strings.NewReader(input))

	var events []protocol.SlackEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event protocol.SlackEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("failed to parse line: %v\nline: %s", err, line)
		}
		events = append(events, event)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify start event has dispatch metadata.
	if events[0].Payload.TaskType != "dispatch" {
		t.Errorf("start task_type = %q, want %q", events[0].Payload.TaskType, "dispatch")
	}
	if events[0].Payload.TotalAgents != 3 {
		t.Errorf("start total_agents = %d, want 3", events[0].Payload.TotalAgents)
	}

	// Verify progress event has agents array.
	p := events[1].Payload
	if len(p.Agents) != 3 {
		t.Fatalf("expected 3 agents in progress, got %d", len(p.Agents))
	}
	if p.Agents[0].AgentID != "a1" || p.Agents[0].Role != "researcher" {
		t.Errorf("agent[0] = %+v, want a1/researcher", p.Agents[0])
	}
	if p.Agents[0].Progress != 0.5 {
		t.Errorf("agent[0].progress = %f, want 0.5", p.Agents[0].Progress)
	}

	// Verify done event.
	if events[2].Payload.Output != "all agents complete" {
		t.Errorf("done output = %q, want %q", events[2].Payload.Output, "all agents complete")
	}
}
