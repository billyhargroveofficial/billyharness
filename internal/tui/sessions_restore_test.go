package tui

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestResumeChatRestoresProjectedUsageSnapshot(t *testing.T) {
	m := newTestModel(t)
	m.width = 240
	events := []protocol.Event{
		{Type: protocol.EventRunStarted},
		{Type: protocol.EventModelCallStarted},
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
	for _, event := range events {
		m.applyEvent(event)
	}
	if err := m.saveCurrentSession(); err != nil {
		t.Fatal(err)
	}
	saved, err := loadChatSession(m.sessionsDir, m.localChatID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProjectedUsage == nil {
		t.Fatal("saved session missing projected usage snapshot")
	}
	if got := saved.ProjectedUsage.HelperModelAPITokens; got != 50 {
		t.Fatalf("saved helper model api tokens = %d, want 50", got)
	}
	if got := saved.ProjectedUsage.HelperModelCalls; got != 1 {
		t.Fatalf("saved helper model calls = %d, want 1", got)
	}
	if got := saved.ProjectedUsage.ToolSummaryAPITokens; got != 50 {
		t.Fatalf("saved tool summary api tokens = %d, want 50", got)
	}

	restored := newTestModel(t)
	restored.width = 240
	restored.applyChatSession(saved)
	if got := restored.contextTokens(); got != 150 {
		t.Fatalf("restored contextTokens = %d, want 150", got)
	}
	if restored.toolSummaryInTok != 30 || restored.toolSummaryOutTok != 8 || restored.toolSummaryAPITok != 50 {
		t.Fatalf("restored web summary = in %d out %d api %d, want 30/8/50",
			restored.toolSummaryInTok, restored.toolSummaryOutTok, restored.toolSummaryAPITok)
	}
	if restored.helperModelCalls != 1 || restored.helperModelInTok != 40 || restored.helperModelOutTok != 10 || restored.helperModelAPITok != 50 ||
		restored.helperAPICalls != 2 || restored.helperCostUSD != 0.0045 {
		t.Fatalf("restored helper usage = model calls %d in %d out %d api %d calls %d cost %.6f, want 1/40/10/50/2/0.004500",
			restored.helperModelCalls, restored.helperModelInTok, restored.helperModelOutTok, restored.helperModelAPITok,
			restored.helperAPICalls, restored.helperCostUSD)
	}

	status := stripANSITest(restored.inlineStatusView())
	for _, want := range []string{
		"Context 150/1.0m 0.0% used",
		"cache hit 50",
		"cache miss 75",
		"websum 30→8",
		"helper 40→10",
		"sumapi 50",
		"helper API calls 2",
		"helper API cost $0.0045",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("restored status %q missing %q", status, want)
		}
	}

	contextText, err := restored.loadContextStatus()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"provider usage: input=125 output=25 reasoning=7",
		"provider cache: hit=50 miss=75 last_hit=50 last_miss=75",
		"helper usage: websum=30→8 helper=40→10 sumapi=50",
		"helper API calls=2",
		"helper API cost=$0.004500",
		"helper_calls=1",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("restored context %q missing %q", contextText, want)
		}
	}
}
