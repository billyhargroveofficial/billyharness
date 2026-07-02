package tui

import (
	"encoding/json"
	"strings"
	"testing"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/telegrambot"
)

func TestGoldenStatusEventPresentationPolicyMatchesTUITelegram(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	telegramRenderer := telegrambot.NewRenderer()
	var telegramProgress []telegrambot.RenderEvent
	events := []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted},
		{Seq: 3, Type: protocol.EventAssistantDelta, Data: "before tool."},
		{Seq: 4, Type: protocol.EventToolCallRequested, CallID: "call-web", Data: protocol.ToolCall{
			ID:        "call-web",
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com/forecast"}`),
		}},
		{Seq: 5, Type: protocol.EventToolCallStarted, CallID: "call-web", Data: map[string]any{"name": "web_fetch"}},
		{Seq: 6, Type: protocol.EventToolCallProgress, CallID: "call-web", Data: protocol.ToolProgressEvent{
			CallID:  "call-web",
			Name:    "web_fetch",
			Message: "downloaded bytes",
		}},
		{Seq: 7, Type: protocol.EventToolOutputRefCreated, CallID: "call-web", Data: protocol.ToolOutputRefEvent{
			CallID:    "call-web",
			Name:      "web_fetch",
			OutputRef: "tool-output/web-fetch.json",
		}},
		{Seq: 8, Type: protocol.EventToolPermissionRequested, CallID: "call-web", Data: protocol.ToolPermissionEvent{
			CallID:           "call-web",
			Name:             "web_fetch",
			RequiresApproval: true,
		}},
		{Seq: 9, Type: protocol.EventToolCallFinished, CallID: "call-web", Data: protocol.ToolResult{
			CallID:  "call-web",
			Name:    "web_fetch",
			Content: "forecast text",
			Metadata: map[string]any{
				"estimated_text_tokens": 123,
			},
		}},
		{Seq: 10, Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      120,
			"output_tokens":     30,
			"cache_hit_tokens":  10,
			"cache_miss_tokens": 110,
		}},
		{Seq: 11, Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
			Kind:         "web_summary",
			CallID:       "call-web",
			InputTokens:  40,
			OutputTokens: 8,
			APICalls:     1,
			CostUSD:      0.0123,
		}},
		{Seq: 12, Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
			Percent:             60,
			EstimatedTokens:     600,
			ContextWindowTokens: 1000,
			ThresholdTokens:     600,
			RemainingTokens:     400,
			MessageCount:        5,
			Stage:               "pre-run",
		}},
		{Seq: 13, Type: protocol.EventStreamStillRunning, Data: protocol.StreamStillRunningEvent{
			Phase:     "tool",
			IdleMS:    2000,
			ElapsedMS: 5000,
		}},
		{Seq: 14, Type: protocol.EventRunCompleted},
	}

	for _, event := range events {
		policy := uxprojector.EventPresentationPolicy(event.Type)
		beforeBlocks := len(m.blocks)
		rendered := telegramRenderer.Apply(event)
		m.applyEvent(event)
		if policy.LowLevelToolLifecycle {
			if len(m.blocks) != beforeBlocks {
				t.Fatalf("%s added TUI transcript noise: before=%d after=%d blocks=%#v", event.Type, beforeBlocks, len(m.blocks), m.blocks)
			}
			if len(rendered) != 0 {
				t.Fatalf("%s added Telegram compact progress noise: %#v", event.Type, rendered)
			}
		}
		if policy.CompactProgress && !hasTelegramProgressEvent(rendered) {
			t.Fatalf("%s policy requires Telegram compact progress, got %#v", event.Type, rendered)
		}
		if !policy.CompactProgress && hasTelegramProgressEvent(rendered) {
			t.Fatalf("%s policy rejects Telegram compact progress, got %#v", event.Type, rendered)
		}
		telegramProgress = append(telegramProgress, rendered...)
	}

	if m.modelCalls != telegramRenderer.ModelCalls || m.toolCalls != telegramRenderer.ToolCalls {
		t.Fatalf("TUI/Telegram call counts drifted: tui model/tools=%d/%d telegram=%d/%d",
			m.modelCalls, m.toolCalls, telegramRenderer.ModelCalls, telegramRenderer.ToolCalls)
	}
	if m.inputTok != telegramRenderer.InputTokens || m.outputTok != telegramRenderer.OutputTokens ||
		m.cacheHitTok != telegramRenderer.CacheHit || m.cacheMissTok != telegramRenderer.CacheMiss {
		t.Fatalf("TUI/Telegram provider usage drifted: tui in/out/cache=%d/%d/%d/%d telegram=%d/%d/%d/%d",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok,
			telegramRenderer.InputTokens, telegramRenderer.OutputTokens, telegramRenderer.CacheHit, telegramRenderer.CacheMiss)
	}
	progressText := telegramProgressText(telegramProgress)
	for _, want := range []string{"web_fetch", "✅", "context 60%", "still running"} {
		if !strings.Contains(progressText, want) {
			t.Fatalf("Telegram progress missing %q:\n%s", want, progressText)
		}
	}
	for _, notWant := range []string{"downloaded bytes", "tool-output/web-fetch.json", "approval"} {
		if strings.Contains(progressText, notWant) {
			t.Fatalf("Telegram progress leaked low-level lifecycle %q:\n%s", notWant, progressText)
		}
	}
}

func hasTelegramProgressEvent(events []telegrambot.RenderEvent) bool {
	for _, event := range events {
		if event.Kind == "tool" || event.Kind == "status" || event.Kind == "error" {
			return true
		}
	}
	return false
}

func telegramProgressText(events []telegrambot.RenderEvent) string {
	var parts []string
	for _, event := range events {
		parts = append(parts, event.Title, event.Body)
	}
	return strings.Join(parts, "\n")
}
