package tui

import (
	"strings"
	"testing"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestProviderUsageUpdateDeduplicatesCumulativeSnapshots(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	m.applyEvent(protocol.Event{Type: protocol.EventRunStarted})
	m.applyEvent(protocol.Event{Type: protocol.EventModelCallStarted})

	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      100,
		"output_tokens":     20,
		"cache_hit_tokens":  40,
		"cache_miss_tokens": 60,
		"reasoning_tokens":  5,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      100,
		"output_tokens":     20,
		"cache_hit_tokens":  40,
		"cache_miss_tokens": 60,
		"reasoning_tokens":  5,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      125,
		"output_tokens":     25,
		"cache_hit_tokens":  50,
		"cache_miss_tokens": 75,
		"reasoning_tokens":  7,
	}})

	if m.inputTok != 125 || m.outputTok != 25 || m.cacheHitTok != 50 || m.cacheMissTok != 75 || m.reasoningTok != 7 {
		t.Fatalf("usage totals = in:%d out:%d hit:%d miss:%d reasoning:%d",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok, m.reasoningTok)
	}
	if got := m.contextTokens(); got != 150 {
		t.Fatalf("contextTokens = %d, want current snapshot input+output 150", got)
	}
	status := m.inlineStatusView()
	if !strings.Contains(status, "cache hit 50") || !strings.Contains(status, "cache miss 75") {
		t.Fatalf("status should show last cache snapshot, got %q", status)
	}
	for _, bad := range []string{"cache hit 90", "cache miss 135", "reasoning 7", "157 used"} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not show cumulative raw counter %q: %q", bad, status)
		}
	}
}

func TestTUIAccountingMatchesClientUXProjector(t *testing.T) {
	events := []protocol.Event{
		{Type: protocol.EventRunStarted},
		{Type: protocol.EventModelCallStarted},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      100,
			"output_tokens":     20,
			"cache_hit_tokens":  40,
			"cache_miss_tokens": 60,
			"reasoning_tokens":  5,
		}},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      125,
			"output_tokens":     25,
			"cache_hit_tokens":  50,
			"cache_miss_tokens": 75,
			"reasoning_tokens":  7,
		}},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-1", Name: "web_fetch"}},
		{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
			CallID:  "call-1",
			Name:    "web_fetch",
			Content: "ok",
			Metadata: map[string]any{
				"tool_summary_input_tokens":      int64(30),
				"tool_summary_output_tokens":     int64(8),
				"tool_summary_api_input_tokens":  int64(40),
				"tool_summary_api_output_tokens": int64(10),
			},
		}},
		{Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
			Kind:     "web_backend",
			Provider: "exa",
			APICalls: 2,
			CostUSD:  0.0045,
		}},
		{Type: protocol.EventRunCompleted},
	}

	m := newTestModel(t)
	p := uxprojector.New()
	for _, event := range events {
		p.Apply(event)
		m.applyEvent(event)
	}
	snapshot := p.Snapshot()
	if m.modelCalls != snapshot.ModelCalls || m.toolCalls != snapshot.ToolCalls {
		t.Fatalf("counts = model:%d tools:%d, projector model:%d tools:%d",
			m.modelCalls, m.toolCalls, snapshot.ModelCalls, snapshot.ToolCalls)
	}
	if m.inputTok != snapshot.InputTokens || m.outputTok != snapshot.OutputTokens ||
		m.cacheHitTok != snapshot.CacheHitTokens || m.cacheMissTok != snapshot.CacheMissTokens ||
		m.reasoningTok != snapshot.ReasoningTokens {
		t.Fatalf("usage = in:%d out:%d hit:%d miss:%d reasoning:%d, projector=%#v",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok, m.reasoningTok, snapshot)
	}
	if m.contextTokens() != snapshot.LastInputTokens+snapshot.LastOutputTokens {
		t.Fatalf("context tokens = %d, projector last context = %d",
			m.contextTokens(), snapshot.LastInputTokens+snapshot.LastOutputTokens)
	}
	if m.toolSummaryInTok != snapshot.ToolSummaryInputTokens ||
		m.toolSummaryOutTok != snapshot.ToolSummaryOutputTokens ||
		m.toolSummaryAPITok != snapshot.ToolSummaryAPITokens {
		t.Fatalf("tool summary = in:%d out:%d api:%d, projector=%#v",
			m.toolSummaryInTok, m.toolSummaryOutTok, m.toolSummaryAPITok, snapshot)
	}
	if m.helperAPICalls != snapshot.HelperAPICalls || m.helperCostUSD != snapshot.HelperCostUSD {
		t.Fatalf("helper api = calls:%d cost:%f, projector=%#v", m.helperAPICalls, m.helperCostUSD, snapshot)
	}
	if m.status != "completed" || snapshot.RunState != uxprojector.RunStateCompleted {
		t.Fatalf("terminal state = tui:%q projector:%q", m.status, snapshot.RunState)
	}
}
