package provider

import (
	"encoding/json"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestDeepSeekBodyThinkingHigh(t *testing.T) {
	temp := 0.7
	d := &DeepSeek{Thinking: "enabled", ReasoningEffort: "high"}
	body, err := d.body(Request{
		Model:       "deepseek-v4-flash",
		Temperature: &temp,
		Messages: []protocol.Message{
			{Role: protocol.RoleUser, Content: "hello"},
		},
		Tools: []protocol.ToolSpec{
			{
				Name:        "time_now",
				Description: "Return time.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Risk:        protocol.RiskReadOnly,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "deepseek-v4-flash" {
		t.Fatalf("model = %v", payload["model"])
	}
	if _, ok := payload["temperature"]; ok {
		t.Fatalf("temperature must be omitted when thinking is enabled: %s", body)
	}
	if payload["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", payload["reasoning_effort"])
	}
	thinking, ok := payload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v", payload["thinking"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", payload["tools"])
	}
}

func TestParseChunkEmitsReasoningContentToolAndUsage(t *testing.T) {
	events, err := parseChunk([]byte(`{
		"choices":[{
			"delta":{
				"reasoning_content":"think",
				"content":"answer",
				"tool_calls":[{
					"index":0,
					"id":"call_1",
					"function":{"name":"time_now","arguments":"{}"}
				}]
			}
		}],
		"usage":{"prompt_tokens":3,"completion_tokens":5}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events len = %d: %#v", len(events), events)
	}
	if events[0].Kind != EventUsage || events[0].Usage.InputTokens != 3 || events[0].Usage.OutputTokens != 5 {
		t.Fatalf("usage event = %#v", events[0])
	}
	if events[1].Kind != EventContent || events[1].Text != "answer" {
		t.Fatalf("content event = %#v", events[1])
	}
	if events[2].Kind != EventReasoning || events[2].Text != "think" {
		t.Fatalf("reasoning event = %#v", events[2])
	}
	if events[3].Kind != EventToolCallDelta || events[3].ToolID != "call_1" || events[3].ToolName != "time_now" || events[3].ArgsDelta != "{}" {
		t.Fatalf("tool event = %#v", events[3])
	}
}

func TestToolAccumulatorAssemblesArgumentDeltas(t *testing.T) {
	var acc ToolAccumulator
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "fs_list", ArgsDelta: `{"path"`})
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ArgsDelta: `:"."}`})

	calls, err := acc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls len = %d", len(calls))
	}
	if calls[0].ID != "call_a" || calls[0].Name != "fs_list" || string(calls[0].Arguments) != `{"path":"."}` {
		t.Fatalf("call = %#v", calls[0])
	}
}
