package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunMessagesCompactsBeforeProviderCall(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.ContextCompactTokens = 10
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	capture := &captureProvider{}
	a := New(cfg, capture, tools.NewRegistry(cfg))
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old user ", 200)},
		{Role: protocol.RoleAssistant, Content: "old assistant", ReasoningContent: strings.Repeat("reasoning ", 100)},
		{Role: protocol.RoleUser, Content: "latest prompt"},
	}
	var compacted bool
	var compactEvent map[string]any
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), messages, func(event protocol.Event) {
		events = append(events, event)
		if event.Type == protocol.EventContextCompacted {
			compacted = true
			bytes, _ := json.Marshal(event.Data)
			_ = json.Unmarshal(bytes, &compactEvent)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	if !compacted {
		t.Fatalf("expected context compaction event")
	}
	if compactEvent["compaction_id"] == "" ||
		compactEvent["trigger_prompt_tokens"] == nil ||
		compactEvent["threshold_tokens"] == nil ||
		compactEvent["compacted_messages"] == nil ||
		compactEvent["keep_messages"] == nil ||
		compactEvent["max_summary_chars"] == nil ||
		compactEvent["protected_prefix"] == nil ||
		compactEvent["compacted_chars"] == nil ||
		compactEvent["compacted_estimated_tokens"] == nil {
		t.Fatalf("compaction event missing structured fields: %#v", compactEvent)
	}
	if compactEvent["reason"] != "prompt_tokens_at_or_above_threshold" || compactEvent["trigger_source"] != "estimated_messages" {
		t.Fatalf("compaction event missing reason/source: %#v", compactEvent)
	}
	if len(capture.messages) < 3 {
		t.Fatalf("provider messages were not compacted: %#v", capture.messages)
	}
	if capture.messages[0].Role != protocol.RoleSystem || capture.messages[0].Content != "system" {
		t.Fatalf("system prompt not preserved: %#v", capture.messages[0])
	}
	if !strings.HasPrefix(capture.messages[1].Content, compactionMarker) {
		t.Fatalf("summary message missing: %#v", capture.messages[1])
	}
	if strings.Contains(capture.messages[1].Content, "reasoning reasoning") {
		t.Fatalf("summary should not include reasoning content")
	}
	if capture.messages[len(capture.messages)-1].Content != "latest prompt" {
		t.Fatalf("latest prompt not preserved: %#v", capture.messages)
	}
	if len(next) < len(capture.messages) {
		t.Fatalf("returned messages should include compacted context and answer")
	}
}

func TestEstimateMessagesTokensIgnoresStoredReasoningContent(t *testing.T) {
	base := []protocol.Message{{Role: protocol.RoleAssistant, Content: "answer"}}
	withReasoning := []protocol.Message{{Role: protocol.RoleAssistant, Content: "answer", ReasoningContent: strings.Repeat("hidden reasoning ", 1000)}}
	if estimateMessagesTokens(base) != estimateMessagesTokens(withReasoning) {
		t.Fatalf("stored reasoning should not inflate fallback context estimate")
	}
}

func TestCompactMessagesUsesCurrentEstimateAfterHugeToolOutput(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 100
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "old request"},
		{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{
			ID:        "call-1",
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com"}`),
		}}},
		{Role: protocol.RoleTool, ToolCallID: "call-1", Name: "web_fetch", Content: strings.Repeat("large tool output ", 80)},
		{Role: protocol.RoleUser, Content: "latest task"},
	}

	compacted, report, ok := compactMessages(messages, cfg.RuntimeLimits(), 10)
	if !ok {
		t.Fatalf("expected compaction from current estimated transcript tokens, estimate=%d", estimateMessagesTokens(messages))
	}
	if report.TriggerSource != "estimated_messages" || report.TriggerPromptTokens <= int64(cfg.ContextCompactTokens) {
		t.Fatalf("trigger should use current estimate, report=%#v", report)
	}
	if compacted[len(compacted)-1].Content != "latest task" {
		t.Fatalf("latest task not preserved: %#v", compacted)
	}
	if !strings.Contains(compacted[1].Content, "large tool output") {
		t.Fatalf("summary should include compacted tool output: %#v", compacted)
	}
}

func TestEmitContextThresholdEventsOncePerRun(t *testing.T) {
	cfg := config.Default()
	cfg.ContextWindowTokens = 1000
	var events []protocol.Event
	emitted := map[int]bool{}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("x", 2200)},
	}
	emitContextThresholdEvents(messages, cfg.RuntimeLimits(), 2, "after_tool_results", emitted, func(event protocol.Event) {
		events = append(events, event)
	})
	emitContextThresholdEvents(messages, cfg.RuntimeLimits(), 3, "before_turn", emitted, func(event protocol.Event) {
		events = append(events, event)
	})
	if len(events) != 1 {
		t.Fatalf("threshold events = %d, want exactly one 50%% crossing: %#v", len(events), events)
	}
	data := eventDataMap(events[0])
	if events[0].Type != protocol.EventContextThreshold ||
		int(data["percent"].(float64)) != 50 ||
		int64(data["context_window_tokens"].(float64)) != 1000 ||
		data["stage"] != "after_tool_results" ||
		int(data["round"].(float64)) != 2 {
		t.Fatalf("threshold event = %#v data=%#v", events[0], data)
	}
	if emitted[50] != true || emitted[70] || emitted[85] || emitted[95] {
		t.Fatalf("emitted thresholds = %#v", emitted)
	}
}

func TestCompactMessagesPreservesAgentsContextPrefix(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nproject rules\n</INSTRUCTIONS>"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest"},
	}
	compacted, _, ok := compactMessages(messages, cfg.RuntimeLimits(), 100)
	if !ok {
		t.Fatal("expected compaction")
	}
	if len(compacted) < 4 || compacted[1].Content != messages[1].Content {
		t.Fatalf("AGENTS context not preserved: %#v", compacted)
	}
	if !strings.HasPrefix(compacted[2].Content, compactionMarker) {
		t.Fatalf("summary should be after AGENTS context: %#v", compacted)
	}
}

func TestCompactMessagesPreservesProfileSystemPrefix(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "base system"},
		{Role: protocol.RoleSystem, Content: "# Billyharness profile: billy\n\n<SOUL>\nprofile rules\n</SOUL>"},
		{Role: protocol.RoleUser, Content: "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nproject rules\n</INSTRUCTIONS>"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest"},
	}
	compacted, _, ok := compactMessages(messages, cfg.RuntimeLimits(), 100)
	if !ok {
		t.Fatal("expected compaction")
	}
	if len(compacted) < 5 ||
		compacted[0].Content != messages[0].Content ||
		compacted[1].Content != messages[1].Content ||
		compacted[2].Content != messages[2].Content {
		t.Fatalf("protected prefix not preserved: %#v", compacted)
	}
	if !strings.HasPrefix(compacted[3].Content, compactionMarker) {
		t.Fatalf("summary should be after protected prefix: %#v", compacted)
	}
}

func TestCompactMessagesReportsProtectedPrefixPolicyAndCompactedBudget(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 50
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 1500
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "base system"},
		{Role: protocol.RoleSystem, Content: "# Billyharness profile: billy\n\n<SOUL>\nprofile rules\n</SOUL>"},
		{Role: protocol.RoleUser, Content: "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nproject rules\n</INSTRUCTIONS>"},
		{Role: protocol.RoleUser, Content: "# MCP server instructions\n\nUse external tools sparingly."},
		{Role: protocol.RoleUser, Content: strings.Repeat("old user ", 80)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest"},
	}
	compacted, report, ok := compactMessages(messages, cfg.RuntimeLimits(), 1234)
	if !ok {
		t.Fatal("expected compaction")
	}
	if report == nil {
		t.Fatal("expected compaction report")
	}
	if report.Reason != "prompt_tokens_at_or_above_threshold" ||
		report.TriggerSource != "provider_usage" ||
		report.TriggerPromptTokens != 1234 ||
		report.ThresholdTokens != 50 ||
		report.BeforeEstimatedTokens <= 0 ||
		report.AfterEstimatedTokens <= 0 ||
		report.CutStartIndex != 4 ||
		report.CutEndIndex <= report.CutStartIndex ||
		report.ReplacementIndex != 4 ||
		report.KeepMessages != 1 ||
		report.MaxSummaryChars != 1500 ||
		report.SummaryStrategy != "deterministic" {
		t.Fatalf("policy fields = %#v", report)
	}
	if report.ProtectedPrefix.EndIndex != 4 ||
		report.ProtectedPrefixMessages != 4 ||
		report.ProtectedPrefix.Reasons["system_prompt"] != 1 ||
		report.ProtectedPrefix.Reasons["profile_soul"] != 1 ||
		report.ProtectedPrefix.Reasons["agents_instructions"] != 1 ||
		report.ProtectedPrefix.Reasons["mcp_instructions"] != 1 {
		t.Fatalf("protected prefix report = %#v", report.ProtectedPrefix)
	}
	if len(report.ProtectedPrefix.Entries) != 4 || report.ProtectedPrefix.Entries[3].Reason != "mcp_instructions" {
		t.Fatalf("protected prefix entries = %#v", report.ProtectedPrefix.Entries)
	}
	if report.CompactedMessages != 2 ||
		report.CompactedChars <= 0 ||
		report.CompactedEstimatedTokens <= 0 ||
		report.ActiveMessages != len(compacted) ||
		report.ActiveChars <= 0 ||
		report.ActiveEstimatedTokens <= 0 ||
		report.SummaryChars <= 0 ||
		report.SummaryEstimatedTokens <= 0 {
		t.Fatalf("budget fields = %#v", report)
	}
	if !strings.HasPrefix(compacted[4].Content, compactionMarker) {
		t.Fatalf("summary should follow protected prefix: %#v", compacted)
	}
	if strings.Contains(compacted[4].Content, "threshold:") || strings.Contains(compacted[4].Content, "trigger prompt tokens") {
		t.Fatalf("summary should not carry audit policy details: %s", compacted[4].Content)
	}
}

func TestCompactMessagesReportsTopContextContributors(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 10
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("small ", 20)},
		{Role: protocol.RoleTool, Name: "web_fetch", ToolCallID: "call-web", Content: strings.Repeat("web summary ", 200)},
		{Role: protocol.RoleTool, Name: "mcp_call", ToolCallID: "call-mcp", Content: strings.Repeat("mcp ", 50)},
		{Role: protocol.RoleUser, Content: "latest"},
	}
	_, report, ok := compactMessages(messages, cfg.RuntimeLimits(), 1000)
	if !ok {
		t.Fatal("expected compaction")
	}
	if len(report.TopContextContributors) == 0 {
		t.Fatalf("expected top contributors: %#v", report)
	}
	top := report.TopContextContributors[0]
	if top.Source != "web_summaries" || top.Name != "web_fetch" || top.Role != string(protocol.RoleTool) || top.EstimatedTokens <= 0 {
		t.Fatalf("top contributor = %#v", top)
	}
	if len(top.Preview) > 120 {
		t.Fatalf("preview too long: %q", top.Preview)
	}
}

func TestModelCompactionStrategyReplacesSummaryAndReportsModel(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.ContextCompactTokens = 10
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	cfg.ContextCompactStrategy = "model"
	cfg.ContextCompactSummaryProvider = "mock"
	cfg.ContextCompactSummaryModel = "mock-summary"
	cfg.WebSummaryMaxOutputTokens = 100
	capture := &captureProvider{}
	oldSummaryProvider := newCompactionSummaryProvider
	newCompactionSummaryProvider = func(got config.ProviderBinding) (provider.Provider, error) {
		if got.Model.Model != "mock-summary" || got.Provider.Provider != "mock" || got.Model.Thinking != "disabled" {
			t.Fatalf("summary binding = provider:%q model:%q thinking:%q", got.Provider.Provider, got.Model.Model, got.Model.Thinking)
		}
		return staticContentProvider{text: "model says keep latest task and important file path", usage: provider.Usage{InputTokens: 123, OutputTokens: 9}}, nil
	}
	t.Cleanup(func() { newCompactionSummaryProvider = oldSummaryProvider })

	a := New(cfg, capture, tools.NewRegistry(cfg))
	var compactEvent map[string]any
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old context ", 100)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest task"},
	}, func(event protocol.Event) {
		if event.Type == protocol.EventContextCompacted {
			compactEvent = eventDataMap(event)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.messages) < 3 || !strings.Contains(capture.messages[1].Content, "Model summary:") ||
		!strings.Contains(capture.messages[1].Content, "model says keep latest task") {
		t.Fatalf("provider messages missing model compaction summary: %#v", capture.messages)
	}
	if compactEvent["summary_strategy"] != "model" ||
		compactEvent["summary_provider"] != "mock" ||
		compactEvent["summary_model"] != "mock-summary" ||
		int64(compactEvent["model_summary_input_tokens"].(float64)) != 123 ||
		int64(compactEvent["model_summary_output_tokens"].(float64)) != 9 {
		t.Fatalf("compaction event missing model summary fields: %#v", compactEvent)
	}
}

func TestDefaultDeepSeekCompactionThresholdTriggersAtSixHundredK(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "old context"},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest task"},
	}
	compacted, report, ok := compactMessages(messages, cfg.RuntimeLimits(), 600_000)
	if !ok {
		t.Fatal("expected default 600k compaction trigger")
	}
	if report.ThresholdTokens != 600_000 || report.TriggerPromptTokens != 600_000 {
		t.Fatalf("threshold report = %#v", report)
	}
	if compacted[len(compacted)-1].Content != "latest task" {
		t.Fatalf("latest task not preserved: %#v", compacted)
	}
}

func TestCompactionSummaryPreservesToolOutputRefs(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{ID: "call_1", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com"}`)}}},
		{Role: protocol.RoleTool, ToolCallID: "call_1", Name: "web_fetch", Content: "compact digest\noutput_ref=/root/billyharness/tool-output/20260628/ref.txt"},
		{Role: protocol.RoleUser, Content: "latest task"},
	}
	compacted, _, ok := compactMessages(messages, cfg.RuntimeLimits(), 100)
	if !ok {
		t.Fatal("expected compaction")
	}
	if len(compacted) < 3 || !strings.Contains(compacted[1].Content, "output_ref=/root/billyharness/tool-output/20260628/ref.txt") {
		t.Fatalf("summary did not preserve output_ref: %#v", compacted)
	}
}

func TestCompactMessagesPreservesToolAdjacency(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 2
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{
			ID:        "call_1",
			Name:      "fs_read",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		}}},
		{Role: protocol.RoleTool, ToolCallID: "call_1", Name: "fs_read", Content: "readme"},
		{Role: protocol.RoleUser, Content: "continue"},
	}
	compacted, _, ok := compactMessages(messages, cfg.RuntimeLimits(), 10)
	if !ok {
		t.Fatalf("expected compaction")
	}
	var assistantWithTool int
	for i, msg := range compacted {
		if msg.Role == protocol.RoleAssistant && len(msg.ToolCalls) > 0 {
			assistantWithTool = i
			break
		}
	}
	if assistantWithTool == 0 {
		t.Fatalf("assistant tool call should be preserved in tail: %#v", compacted)
	}
	if assistantWithTool+1 >= len(compacted) || compacted[assistantWithTool+1].Role != protocol.RoleTool {
		t.Fatalf("tool result should stay adjacent to assistant tool call: %#v", compacted)
	}
}
