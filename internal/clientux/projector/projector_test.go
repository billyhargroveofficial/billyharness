package projector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
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

func TestProjectorCoalescedDeltasMatchUncoalescedReplay(t *testing.T) {
	uncoalesced := New()
	for _, event := range []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted},
		{Seq: 3, Type: protocol.EventAssistantReasoning, Data: "think "},
		{Seq: 4, Type: protocol.EventAssistantReasoning, Data: "more"},
		{Seq: 5, Type: protocol.EventAssistantDelta, Data: "hello "},
		{Seq: 6, Type: protocol.EventAssistantDelta, Data: "world"},
		{Seq: 7, Type: protocol.EventRunCompleted},
	} {
		uncoalesced.Apply(event)
	}
	coalesced := New()
	var snap Snapshot
	for _, event := range []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted},
		{Seq: 3, Type: protocol.EventAssistantReasoning, Data: "think more"},
		{Seq: 4, Type: protocol.EventAssistantDelta, Data: "hello world"},
		{Seq: 5, Type: protocol.EventRunCompleted},
	} {
		snap = coalesced.Apply(event)
	}
	want := uncoalesced.Snapshot()
	if snap.AssistantText != want.AssistantText || snap.ReasoningText != want.ReasoningText || snap.RunState != want.RunState {
		t.Fatalf("coalesced snapshot = %#v want %#v", snap, want)
	}
}

func TestProjectorTracksToolCompactDisplay(t *testing.T) {
	p := New()
	progressCompact := protocol.ToolCompact{
		CallID:    "call_1",
		Name:      "custom_tool",
		Lifecycle: "executing",
		Status:    protocol.StepStatusStarted,
		Summary:   "started custom_tool",
		Hints:     []string{"safe"},
	}
	p.Apply(protocol.Event{Seq: 1, Type: protocol.EventToolCallProgress, Data: protocol.ToolProgressEvent{
		CallID:  "call_1",
		Name:    "custom_tool",
		Phase:   "executing",
		Status:  protocol.StepStatusStarted,
		Compact: &progressCompact,
	}})
	resultCompact := protocol.ToolCompact{
		CallID:          "call_1",
		Name:            "custom_tool",
		Lifecycle:       "result",
		Status:          protocol.StepStatusCompleted,
		Summary:         "completed custom_tool",
		OutputRef:       "/root/billyharness/tool-output/custom.txt",
		EstimatedTokens: 42,
		Hints:           []string{"output_ref"},
	}
	snap := p.Apply(protocol.Event{Seq: 2, Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		CallID:  "call_1",
		Name:    "custom_tool",
		Content: "compact output",
		Compact: &resultCompact,
	}})
	item := snap.ToolsByCallID["call_1"]
	if item.Compact == nil || item.Compact.OutputRef != resultCompact.OutputRef || item.Compact.EstimatedTokens != 42 {
		t.Fatalf("tool item compact = %#v", item.Compact)
	}
	item.Compact.Hints[0] = "mutated"
	if got := p.Snapshot().ToolsByCallID["call_1"].Compact.Hints[0]; got != "output_ref" {
		t.Fatalf("compact hints were not cloned: %q", got)
	}
}

func TestProjectorTracksHelperUsageWithoutDoubleCountingToolMetadata(t *testing.T) {
	p := New()
	p.Apply(protocol.Event{Seq: 1, Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
		Kind:            "web_summary",
		CallID:          "call-web",
		InputTokens:     90,
		OutputTokens:    10,
		CacheHitTokens:  50,
		CacheMissTokens: 40,
		APITokens:       100,
	}})
	snap := p.Apply(protocol.Event{Seq: 2, Type: protocol.EventToolCallFinished, CallID: "call-web", Data: protocol.ToolResult{
		CallID:  "call-web",
		Name:    "web_fetch",
		Content: "summary",
		Metadata: map[string]any{
			"tool_summary_input_tokens":          200,
			"tool_summary_output_tokens":         25,
			"tool_summary_api_input_tokens":      90,
			"tool_summary_api_output_tokens":     10,
			"tool_summary_api_total_tokens":      100,
			"tool_summary_api_cache_hit_tokens":  50,
			"tool_summary_api_cache_miss_tokens": 40,
			"tool_summary_external_model_used":   true,
		},
	}})
	if snap.ToolSummaryInputTokens != 200 || snap.ToolSummaryOutputTokens != 25 {
		t.Fatalf("web summary compression = %#v", snap)
	}
	if snap.HelperModelCalls != 1 || snap.HelperModelInputTokens != 90 || snap.HelperModelOutputTokens != 10 ||
		snap.HelperModelCacheHitTokens != 50 || snap.HelperModelCacheMissTokens != 40 ||
		snap.HelperModelAPITokens != 100 || snap.ToolSummaryAPITokens != 100 {
		t.Fatalf("helper usage = %#v", snap)
	}
}

func TestProjectorReplaysTodoPlanState(t *testing.T) {
	p := New()
	state := protocol.TodoState{Todos: []protocol.TodoItem{
		{ID: "done", Content: "Inspect plan", Status: "completed", Priority: "high"},
		{ID: "build", Content: "Build todo_write", Status: "in_progress", Priority: "high"},
		{ID: "verify", Content: "Run tests", Status: "pending", Priority: "medium"},
	}}
	snap := p.Apply(protocol.Event{Seq: 1, Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		CallID:  "call_todo",
		Name:    "todo_write",
		Content: "plan updated",
		Metadata: map[string]any{
			"todo_state": state,
		},
	}})
	if len(snap.TodoState.Todos) != 3 || snap.TodoState.InProgress != 1 || snap.TodoState.Pending != 1 || snap.TodoState.Completed != 1 {
		t.Fatalf("todo state = %#v", snap.TodoState)
	}
	snap.TodoState.Todos[1].Content = "mutated"
	if got := p.Snapshot().TodoState.Todos[1].Content; got != "Build todo_write" {
		t.Fatalf("todo state was not cloned: %q", got)
	}
	snap = p.Apply(protocol.Event{Seq: 2, Type: protocol.EventRunStarted})
	if len(snap.TodoState.Todos) != 3 || snap.TodoState.Todos[1].Status != "in_progress" {
		t.Fatalf("todo state did not survive run start: %#v", snap.TodoState)
	}
}

func TestGoldenTraceProjectsClientSnapshot(t *testing.T) {
	p := New()
	var snap Snapshot
	for _, event := range goldenTraceEvents(t) {
		snap = p.Apply(event)
	}
	if snap.RunState != RunStateCompleted || snap.LastSeq != 36 || snap.SeqGap != nil {
		t.Fatalf("terminal snapshot = %#v", snap)
	}
	for _, want := range []string{
		"I'll check the web summary and MCP catalog.",
		"Final answer: web context and MCP state agree",
	} {
		if !strings.Contains(snap.AssistantText, want) {
			t.Fatalf("assistant text missing %q in %q", want, snap.AssistantText)
		}
	}
	if !strings.Contains(snap.ReasoningText, "Need web context") {
		t.Fatalf("reasoning text = %q", snap.ReasoningText)
	}
	if snap.ModelCalls != 2 || snap.ToolCalls != 2 {
		t.Fatalf("call counts = model %d tool %d", snap.ModelCalls, snap.ToolCalls)
	}
	if snap.InputTokens != 2100 || snap.OutputTokens != 135 ||
		snap.CacheHitTokens != 1100 || snap.CacheMissTokens != 1000 || snap.ReasoningTokens != 20 {
		t.Fatalf("usage = %#v", snap)
	}
	if snap.ToolSummaryInputTokens != 100 || snap.ToolSummaryOutputTokens != 25 || snap.ToolSummaryAPITokens != 125 {
		t.Fatalf("tool summary metrics = %#v", snap)
	}
	web := snap.ToolsByCallID["call-web"]
	if web.Name != "web_fetch" || web.Status != "finished" || web.IsError ||
		!strings.Contains(web.Content, "bounded web digest") {
		t.Fatalf("web tool = %#v", web)
	}
	mcp := snap.ToolsByCallID["call-mcp"]
	if mcp.Name != "mcp_call" || mcp.Status != "finished" || !strings.Contains(mcp.Content, "MCP catalog") {
		t.Fatalf("mcp tool = %#v", mcp)
	}
	if len(snap.ContextThresholds) != 1 || snap.ContextThresholds[0].Percent != 70 ||
		snap.ContextThresholds[0].Stage != "before_turn" {
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

func goldenTraceEvents(t *testing.T) []protocol.Event {
	t.Helper()
	records := testkit.ReadTraceRecords(t, testkit.CanonicalAgentLoopTracePath(t))
	events := make([]protocol.Event, 0, len(records))
	for _, record := range records {
		var event protocol.Event
		if err := json.Unmarshal(record.Event, &event); err != nil {
			t.Fatalf("decode event seq %d: %v", record.Seq, err)
		}
		events = append(events, event)
	}
	return events
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

func TestProjectorRecordsSequenceGap(t *testing.T) {
	p := New()
	p.Apply(protocol.Event{Seq: 5, Type: protocol.EventAssistantDelta, Data: "fresh"})
	snap := p.Apply(protocol.Event{Seq: 7, Type: protocol.EventAssistantDelta, Data: "after gap"})
	if snap.LastSeq != 7 || snap.SeqGap == nil {
		t.Fatalf("snapshot = %#v", snap)
	}
	if snap.SeqGap.AfterSeq != 5 || snap.SeqGap.GotSeq != 7 {
		t.Fatalf("seq gap = %#v", snap.SeqGap)
	}
	snap.SeqGap.AfterSeq = 99
	if got := p.Snapshot().SeqGap.AfterSeq; got != 5 {
		t.Fatalf("snapshot gap was not cloned: %d", got)
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

func TestProjectorTracksTurnDiffDisplayState(t *testing.T) {
	p := New()
	recorded := protocol.TurnChangeEvent{
		ChangeID:         "change-1",
		ToolName:         "fs_write_file",
		FileCount:        1,
		Modified:         1,
		Additions:        4,
		Deletions:        1,
		Reversible:       true,
		PatchOutputRef:   "/root/billyharness/tool-output/change-1.json",
		PatchOutputRefID: "artifact-1",
		Files: []protocol.TurnChangeFile{
			{RelPath: "README.md", Change: "modified", Additions: 4, Deletions: 1, Reversible: true},
		},
	}
	snap := p.Apply(protocol.Event{Seq: 1, Type: protocol.EventTurnChangeRecorded, Data: recorded})
	if len(snap.TurnChanges) != 1 || snap.LatestTurnChange == nil {
		t.Fatalf("turn changes = %#v latest=%#v", snap.TurnChanges, snap.LatestTurnChange)
	}
	latest := snap.LatestTurnChange
	if latest.ChangeID != "change-1" || latest.FileCount != 1 || latest.Additions != 4 ||
		latest.PatchOutputRef != recorded.PatchOutputRef || latest.LastEvent != protocol.EventTurnChangeRecorded {
		t.Fatalf("latest change = %#v", latest)
	}
	snap.TurnChanges[0].Files[0].RelPath = "mutated"
	if got := p.Snapshot().TurnChanges[0].Files[0].RelPath; got != "README.md" {
		t.Fatalf("turn change files were not cloned: %q", got)
	}

	recorded.Status = "reverted"
	snap = p.Apply(protocol.Event{Seq: 2, Type: protocol.EventTurnChangeReverted, Data: recorded})
	if len(snap.TurnChanges) != 1 || snap.TurnChanges[0].Status != "reverted" ||
		snap.LatestTurnChange == nil || snap.LatestTurnChange.LastEvent != protocol.EventTurnChangeReverted {
		t.Fatalf("reverted snapshot = %#v latest=%#v", snap.TurnChanges, snap.LatestTurnChange)
	}
}
