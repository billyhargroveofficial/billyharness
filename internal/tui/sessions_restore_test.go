package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func TestApplyChatSessionDropsRoutineRunSummaryBlocks(t *testing.T) {
	m := newTestModel(t)
	session := chatSession{
		ID:        newChatID(),
		Title:     "saved noise",
		CreatedAt: time.Now().UTC(),
		Blocks: []savedBlock{
			{
				Kind:      "status",
				CellType:  transcript.CellTypeRunSummary,
				Title:     "Run done · gpt-5.5 · xhigh · 5s",
				Content:   "state: completed\nelapsed: 5s\nagent turns: 1 / session 1\ntools: 0 / session 0\ncontext: 6.0k / 256k\nsubscription",
				EventType: protocol.EventRunCompleted,
				RawCopy:   "state: completed",
			},
			{
				Kind:     "assistant",
				CellType: transcript.CellTypeAssistantFinal,
				Title:    "ASSISTANT",
				Content:  "actual answer",
				RawCopy:  "actual answer",
			},
		},
	}

	m.applyChatSession(session)
	if summaries := countCells(m.blocks, cellTypeRunSummary); summaries != 0 {
		t.Fatalf("routine run summaries should be dropped on restore, got %d: %#v", summaries, m.blocks)
	}
	if len(m.blocks) != 1 || m.blocks[0].Content != "actual answer" {
		t.Fatalf("restore should keep non-summary transcript blocks: %#v", m.blocks)
	}
	saved, err := loadChatSession(m.sessionsDir, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range saved.Blocks {
		if block.CellType == transcript.CellTypeRunSummary {
			t.Fatalf("routine run summary should not be saved again: %#v", saved.Blocks)
		}
	}
}

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
		"📁",
		"🤖 v4-flash high",
		"0.0% 150",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("restored status %q missing %q", status, want)
		}
	}
	for _, bad := range []string{"cache hit", "cache miss", "websum", "helper ", "sumapi", "helper API"} {
		if strings.Contains(status, bad) {
			t.Fatalf("restored inline status should omit noisy segment %q: %q", bad, status)
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
