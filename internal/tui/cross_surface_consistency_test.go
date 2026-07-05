package tui

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/telegrambot"
)

func TestGoldenStatusTraceMatchesProjectorTelegramTUIAndContext(t *testing.T) {
	cfg := config.Default()
	cfg.ContextWindowTokens = 1000
	cfg.ContextCompactTokens = 600
	events := goldenStatusTraceEvents()

	projector := uxprojector.New()
	telegramRenderer := telegrambot.NewRendererWithContextWindow(cfg.ContextWindowTokens)
	tuiModel := newTestModel(t)
	tuiModel.width = 220
	tuiModel.runtime = cfg.RuntimeLimits()

	var snapshot uxprojector.Snapshot
	var telegramProgress []telegrambot.RenderEvent
	for _, event := range events {
		snapshot = projector.Apply(event)
		telegramProgress = append(telegramProgress, telegramRenderer.Apply(event)...)
		tuiModel.applyEvent(event)
	}

	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: strings.Repeat("question context ", 30)},
		{Role: protocol.RoleAssistant, Content: snapshot.AssistantText},
		{Role: protocol.RoleTool, Name: "web_fetch", ToolCallID: "call-web", Content: strings.Repeat("web summary ", 80) + "output_ref=/tmp/web-fetch.txt"},
	}
	contextResp := clientux.BuildContextResponseWithOptions(cfg.RuntimeLimits(), "session-golden", messages, clientux.ContextReportOptions{
		Runtime: gatewayapi.ContextRuntime{Profile: "billy", AccessMode: "build"},
		Events:  events,
	})
	contextText := gatewayclient.FormatSessionContext(contextResp)

	assertGoldenStatusSnapshot(t, snapshot)
	assertTelegramGoldenStatus(t, telegramRenderer, telegramProgress, snapshot.AssistantText)
	assertTUIGoldenStatus(t, tuiModel)
	assertContextGoldenStatus(t, contextResp, contextText)
}

func goldenStatusTraceEvents() []protocol.Event {
	return []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted, Data: protocol.ModelCallEvent{
			ProviderID:    "deepseek",
			ModelID:       "deepseek-v4-flash",
			ReasoningMode: "enabled/high",
		}},
		{Seq: 3, Type: protocol.EventAssistantDelta, Data: "Before tool. "},
		{Seq: 4, Type: protocol.EventToolCallRequested, CallID: "call-web", Data: protocol.ToolCall{ID: "call-web", Name: "web_fetch"}},
		{Seq: 5, Type: protocol.EventToolCallStarted, CallID: "call-web", Data: map[string]any{"name": "web_fetch"}},
		{Seq: 6, Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
			Kind:            "web_summary",
			CallID:          "call-web",
			InputTokens:     90,
			OutputTokens:    10,
			CacheHitTokens:  50,
			CacheMissTokens: 40,
			APITokens:       100,
		}},
		{Seq: 7, Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
			Kind:     "web_backend",
			Provider: "exa",
			CallID:   "call-web",
			APICalls: 1,
			CostUSD:  0.003,
		}},
		{Seq: 8, Type: protocol.EventToolCallFinished, CallID: "call-web", Data: protocol.ToolResult{
			CallID:    "call-web",
			Name:      "web_fetch",
			Content:   "compact web summary",
			OutputRef: "/tmp/web-fetch.txt",
			Metadata: map[string]any{
				"tool_summary_input_tokens":          int64(200),
				"tool_summary_output_tokens":         int64(25),
				"tool_summary_api_input_tokens":      int64(90),
				"tool_summary_api_output_tokens":     int64(10),
				"tool_summary_api_total_tokens":      int64(100),
				"tool_summary_api_cache_hit_tokens":  int64(50),
				"tool_summary_api_cache_miss_tokens": int64(40),
				"tool_summary_external_model_used":   true,
				"output_ref":                         "/tmp/web-fetch.txt",
			},
		}},
		{Seq: 9, Type: protocol.EventToolOutputRefCreated, CallID: "call-web", Data: protocol.ToolOutputRefEvent{
			CallID:    "call-web",
			Name:      "web_fetch",
			OutputRef: "/tmp/web-fetch.txt",
		}},
		{Seq: 10, Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      420,
			"output_tokens":     80,
			"cache_hit_tokens":  200,
			"cache_miss_tokens": 220,
			"reasoning_tokens":  12,
		}},
		{Seq: 11, Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
			Percent:             60,
			EstimatedTokens:     600,
			ContextWindowTokens: 1000,
			ThresholdTokens:     600,
			RemainingTokens:     400,
			MessageCount:        5,
			Stage:               "after_tool_results",
		}},
		{Seq: 12, Type: protocol.EventAssistantDelta, Data: "After tool final answer."},
		{Seq: 13, Type: protocol.EventRunCompleted},
	}
}

func assertGoldenStatusSnapshot(t *testing.T, snapshot uxprojector.Snapshot) {
	t.Helper()
	if snapshot.RunState != uxprojector.RunStateCompleted ||
		snapshot.ModelCalls != 1 || snapshot.ToolCalls != 1 ||
		snapshot.AssistantText != "Before tool. \n\nAfter tool final answer." {
		t.Fatalf("projector terminal snapshot drifted: %#v", snapshot)
	}
	if snapshot.InputTokens != 420 || snapshot.OutputTokens != 80 ||
		snapshot.CacheHitTokens != 200 || snapshot.CacheMissTokens != 220 ||
		snapshot.ReasoningTokens != 12 || snapshot.LastInputTokens != 420 ||
		snapshot.LastOutputTokens != 80 {
		t.Fatalf("projector usage drifted: %#v", snapshot)
	}
	if snapshot.ToolSummaryInputTokens != 200 || snapshot.ToolSummaryOutputTokens != 25 ||
		snapshot.ToolSummaryAPITokens != 100 ||
		snapshot.HelperModelCalls != 1 || snapshot.HelperModelInputTokens != 90 ||
		snapshot.HelperModelOutputTokens != 10 || snapshot.HelperModelAPITokens != 100 ||
		snapshot.HelperAPICalls != 1 || snapshot.HelperCostUSD != 0.003 {
		t.Fatalf("projector helper usage drifted: %#v", snapshot)
	}
	web := snapshot.ToolsByCallID["call-web"]
	if web.Status != "finished" || web.Compact == nil ||
		web.Compact.OutputRef != "/tmp/web-fetch.txt" ||
		web.Compact.Status != protocol.StepStatusCompleted {
		t.Fatalf("projector output-ref tool drifted: %#v", web)
	}
	if len(snapshot.ContextThresholds) != 1 || snapshot.ContextThresholds[0].Percent != 60 ||
		snapshot.ContextThresholds[0].Stage != "after_tool_results" {
		t.Fatalf("projector context thresholds drifted: %#v", snapshot.ContextThresholds)
	}
}

func assertTelegramGoldenStatus(t *testing.T, renderer *telegrambot.Renderer, progress []telegrambot.RenderEvent, assistantText string) {
	t.Helper()
	if !renderer.Done || renderer.ModelCalls != 1 || renderer.ToolCalls != 1 ||
		renderer.InputTokens != 420 || renderer.OutputTokens != 80 ||
		renderer.CacheHit != 200 || renderer.CacheMiss != 220 ||
		renderer.ToolSummaryIn != 200 || renderer.ToolSummaryOut != 25 ||
		renderer.HelperModelAPI != 100 || renderer.HelperAPICalls != 1 ||
		renderer.HelperCostUSD != 0.003 {
		t.Fatalf("telegram renderer drifted: %#v", renderer)
	}
	progressText := telegramGoldenProgressText(progress)
	for _, want := range []string{"web_fetch", "ref web-fetch.txt", "context 60%"} {
		if !strings.Contains(progressText, want) {
			t.Fatalf("telegram progress missing %q:\n%s", want, progressText)
		}
	}
	finalText := strings.Join(renderer.FinalChunks("deepseek-v4-flash", "high"), "\n")
	for _, want := range []string{assistantText, "agent turns 1", "tools 1", "websum 200→25", "sumapi 100", "helper API calls 1", "helper API cost $0.0030"} {
		if !strings.Contains(finalText, want) {
			t.Fatalf("telegram final missing %q:\n%s", want, finalText)
		}
	}
}

func assertTUIGoldenStatus(t *testing.T, model Model) {
	t.Helper()
	if model.status != "completed" || model.modelCalls != 1 || model.toolCalls != 1 ||
		model.inputTok != 420 || model.outputTok != 80 ||
		model.cacheHitTok != 200 || model.cacheMissTok != 220 ||
		model.toolSummaryInTok != 200 || model.toolSummaryOutTok != 25 ||
		model.helperModelAPITok != 100 || model.helperAPICalls != 1 ||
		model.helperCostUSD != 0.003 {
		t.Fatalf("tui model accounting drifted: %#v", model)
	}
	status := stripANSITest(model.inlineStatusView())
	for _, want := range []string{"📁", "⎇", "🤖 v4-flash high", "50.0% 500"} {
		if !strings.Contains(status, want) {
			t.Fatalf("tui status missing %q:\n%s", want, status)
		}
	}
	for _, bad := range []string{"cache hit", "cache miss", "websum", "sumapi", "helper API", "agent turns", "tools 1"} {
		if strings.Contains(status, bad) {
			t.Fatalf("tui status should omit noisy segment %q:\n%s", bad, status)
		}
	}
	transcriptText := tuiGoldenTranscriptText(model)
	for _, want := range []string{"Before tool.", "After tool final answer.", "compact web summary", "ref web-fetch.txt", "CONTEXT"} {
		if !strings.Contains(transcriptText, want) {
			t.Fatalf("tui transcript missing %q:\n%s", want, transcriptText)
		}
	}
}

func assertContextGoldenStatus(t *testing.T, resp gatewayapi.SessionContextResponse, formatted string) {
	t.Helper()
	if resp.Usage.ModelCalls != 1 || resp.Usage.ToolCalls != 1 ||
		resp.Usage.InputTokens != 420 || resp.Usage.OutputTokens != 80 ||
		resp.Usage.CacheHitTokens != 200 || resp.Usage.CacheMissTokens != 220 ||
		resp.Usage.WebSummaryInputTokens != 200 || resp.Usage.WebSummaryOutputTokens != 25 ||
		resp.Usage.HelperModelCalls != 1 || resp.Usage.HelperModelAPITokens != 100 ||
		resp.Usage.HelperAPICalls != 1 || resp.Usage.HelperCostUSD != 0.003 ||
		resp.OutputRefs.Count != 1 {
		t.Fatalf("context response usage drifted: %#v", resp.Usage)
	}
	for _, want := range []string{"active context:", "thresholds:", "activity: model_calls=1 tools=1", "provider usage: input=420 output=80 reasoning=12", "provider cache: hit=200 miss=220", "helper usage: websum=200", "sumapi=100", "helper API calls=1", "helper API cost=$0.003000", "output refs: 1"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted context missing %q:\n%s", want, formatted)
		}
	}
}

func telegramGoldenProgressText(events []telegrambot.RenderEvent) string {
	var parts []string
	for _, event := range events {
		parts = append(parts, event.Title, event.Body)
	}
	return strings.Join(parts, "\n")
}

func tuiGoldenTranscriptText(model Model) string {
	var parts []string
	for _, block := range model.blocks {
		parts = append(parts, block.Title, block.Content)
	}
	return strings.Join(parts, "\n")
}
