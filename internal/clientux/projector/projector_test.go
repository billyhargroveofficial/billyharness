package projector

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestProjectorBuildsClientSnapshot(t *testing.T) {
	p := New()
	events := []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted},
		{Seq: 3, Type: protocol.EventAssistantReasoning, Data: "thinking"},
		{Seq: 4, Type: protocol.EventAssistantDelta, Data: "hello "},
		{Seq: 5, Type: protocol.EventAssistantDelta, Data: "world"},
		{Seq: 6, Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call_1", Name: "web_fetch"}},
		{Seq: 7, Type: protocol.EventToolCallStarted, CallID: "call_1", AttemptID: "attempt_1"},
		{Seq: 8, Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      100,
			"output_tokens":     20,
			"cache_hit_tokens":  10,
			"cache_miss_tokens": 90,
			"reasoning_tokens":  7,
		}},
		{Seq: 9, Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      125,
			"output_tokens":     24,
			"cache_hit_tokens":  15,
			"cache_miss_tokens": 110,
			"reasoning_tokens":  9,
		}},
		{Seq: 10, Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
			Percent:             70,
			EstimatedTokens:     700,
			ContextWindowTokens: 1000,
			ThresholdTokens:     700,
			RemainingTokens:     300,
			MessageCount:        4,
			Stage:               "pre_model",
		}},
		{Seq: 11, Type: protocol.EventToolCallFinished, CallID: "call_1", AttemptID: "attempt_1", Data: protocol.ToolResult{
			CallID:  "call_1",
			Name:    "web_fetch",
			Content: "summary",
			Metadata: map[string]any{
				"tool_summary_input_tokens":     900,
				"tool_summary_output_tokens":    120,
				"tool_summary_api_total_tokens": 1020,
			},
		}},
		{Seq: 12, Type: protocol.EventRunCompleted},
	}
	var snap Snapshot
	for _, event := range events {
		snap = p.Apply(event)
	}

	if snap.RunState != RunStateCompleted || snap.LastSeq != 12 {
		t.Fatalf("terminal snapshot = %#v", snap)
	}
	if snap.AssistantText != "hello world" || snap.ReasoningText != "thinking" {
		t.Fatalf("text snapshot = assistant %q reasoning %q", snap.AssistantText, snap.ReasoningText)
	}
	if snap.ModelCalls != 1 || snap.ToolCalls != 1 {
		t.Fatalf("calls = model %d tool %d", snap.ModelCalls, snap.ToolCalls)
	}
	if snap.InputTokens != 125 || snap.OutputTokens != 24 || snap.LastInputTokens != 125 || snap.LastOutputTokens != 24 {
		t.Fatalf("usage = %#v", snap)
	}
	if snap.CacheHitTokens != 15 || snap.CacheMissTokens != 110 || snap.ReasoningTokens != 9 {
		t.Fatalf("cache/reasoning usage = %#v", snap)
	}
	if snap.ToolSummaryInputTokens != 900 || snap.ToolSummaryOutputTokens != 120 || snap.ToolSummaryAPITokens != 1020 {
		t.Fatalf("tool summary metrics = %#v", snap)
	}
	tool := snap.ToolsByCallID["call_1"]
	if tool.CallID != "call_1" || tool.Name != "web_fetch" || tool.Call.Name != "web_fetch" || tool.Status != "finished" || tool.Content != "summary" || tool.AttemptID != "attempt_1" {
		t.Fatalf("tool item = %#v", tool)
	}
	if len(snap.ContextThresholds) != 1 || snap.ContextThresholds[0].Percent != 70 || snap.ContextThresholds[0].Stage != "pre_model" {
		t.Fatalf("thresholds = %#v", snap.ContextThresholds)
	}
}

func TestProjectorSeparatesAssistantTextAcrossModelTurns(t *testing.T) {
	p := New()
	for _, event := range []protocol.Event{
		{Type: protocol.EventModelCallStarted, TurnID: "turn-1"},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-1", Data: "first turn."},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-1", Name: "time_now"}},
		{Type: protocol.EventModelCallStarted, TurnID: "turn-2"},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-2", Data: "Second turn."},
	} {
		p.Apply(event)
	}
	if got := p.Snapshot().AssistantText; !strings.Contains(got, "first turn.\n\nSecond turn.") {
		t.Fatalf("assistant text = %q", got)
	}
}

func TestProjectorSeparatesAssistantTextAcrossToolBoundaries(t *testing.T) {
	p := New()
	for _, event := range []protocol.Event{
		{Type: protocol.EventModelCallStarted, TurnID: "turn-1"},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-1", Data: "before tool."},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-1", Name: "web_search"}},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-1", Data: "after first tool."},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-2", Name: "web_fetch"}},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-1", Data: "after second tool."},
	} {
		p.Apply(event)
	}
	want := "before tool.\n\nafter first tool.\n\nafter second tool."
	if got := p.Snapshot().AssistantText; got != want {
		t.Fatalf("assistant text = %q, want %q", got, want)
	}
}

func TestProjectorDropsStaleSequencedEvents(t *testing.T) {
	p := New()
	p.Apply(protocol.Event{Seq: 5, Type: protocol.EventAssistantDelta, Data: "fresh"})
	snap := p.Apply(protocol.Event{Seq: 4, Type: protocol.EventAssistantDelta, Data: "stale"})
	if snap.LastSeq != 5 || snap.AssistantText != "fresh" {
		t.Fatalf("snapshot = %#v", snap)
	}
}

func TestProjectorReportsRunFailure(t *testing.T) {
	p := New()
	p.Apply(protocol.Event{Seq: 1, Type: protocol.EventRunStarted})
	snap := p.Apply(protocol.Event{Seq: 2, Type: protocol.EventRunFailed, Data: "boom"})
	if snap.RunState != RunStateFailed || snap.LastError != "boom" || len(snap.Errors) != 1 {
		t.Fatalf("snapshot = %#v", snap)
	}
}
