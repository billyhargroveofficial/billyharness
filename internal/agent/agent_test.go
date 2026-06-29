package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunWithMockProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(cfg)
	a := New(cfg, prov, registry)

	var content string
	if err := a.Run(context.Background(), "hello", func(event protocol.Event) {
		if event.Type == protocol.EventAssistantDelta {
			content += fmt.Sprint(event.Data)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if content != "mock: hello" {
		t.Fatalf("content = %q", content)
	}
}

func TestRunMessagesEmitsTypedTurnAndModelStepEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 3
	a := New(cfg, &captureProvider{}, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	runStarted, ok := firstEventData(events, protocol.EventRunStarted)
	if !ok || runStarted["submission_id"] == "" || runStarted["run_id"] == "" {
		t.Fatalf("run started = %#v ok=%v", runStarted, ok)
	}
	for _, event := range events {
		if event.Type == "" {
			continue
		}
		if event.SubmissionID == "" || event.SubmissionID != runStarted["submission_id"] {
			t.Fatalf("event missing stable submission id: %#v run=%#v", event, runStarted)
		}
	}
	started, ok := firstTurnEvent(events, protocol.EventTurnStarted)
	if !ok || started.TurnID != "turn-001" || started.Round != 1 || started.Status != protocol.TurnStatusStarted || started.Model != "mock" {
		t.Fatalf("turn started = %#v ok=%v", started, ok)
	}
	for _, key := range []string{"provider_id", "model_id", "tool_snapshot_hash", "mcp_status_snapshot_hash", "profile_instruction_hash", "dangerous_permission_mode"} {
		if started.Metadata[key] == nil {
			t.Fatalf("turn snapshot missing %s: %#v", key, started.Metadata)
		}
	}
	completed, ok := firstTurnEvent(events, protocol.EventTurnCompleted)
	if !ok || completed.TurnID != "turn-001" || completed.Status != protocol.TurnStatusCompleted || completed.StopReason != protocol.TurnStopFinalAnswer || completed.DurationMS < 0 {
		t.Fatalf("turn completed = %#v ok=%v", completed, ok)
	}
	modelStarted, ok := firstStepEvent(events, protocol.EventStepStarted, protocol.StepKindModelCall)
	if !ok || modelStarted.StepID != "turn-001:model-call-001" || modelStarted.Status != protocol.StepStatusStarted || modelStarted.MessageCount != 2 {
		t.Fatalf("model step started = %#v ok=%v", modelStarted, ok)
	}
	callStarted, ok := firstEventData(events, protocol.EventModelCallStarted)
	if !ok || callStarted["request_id"] == "" || callStarted["provider_id"] != "mock" || callStarted["model_id"] != "mock" || callStarted["status"] != protocol.StepStatusStarted {
		t.Fatalf("model call started = %#v ok=%v", callStarted, ok)
	}
	callFinished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		callFinished["request_id"] != callStarted["request_id"] ||
		callFinished["provider_id"] != "mock" ||
		callFinished["model_id"] != "mock" ||
		callFinished["provider_request_id"] != "mock-request" ||
		callFinished["status"] != protocol.StepStatusCompleted ||
		callFinished["retries"] == nil ||
		callFinished["first_delta_ms"] == nil ||
		callFinished["total_latency_ms"] == nil ||
		callFinished["input_tokens"] == nil ||
		callFinished["output_tokens"] == nil {
		t.Fatalf("model call finished = %#v ok=%v", callFinished, ok)
	}
	modelCompleted, ok := firstStepEvent(events, protocol.EventStepCompleted, protocol.StepKindModelCall)
	if _, hasFirstDelta := modelCompleted.Metadata["first_delta_ms"]; !ok || modelCompleted.StepID != modelStarted.StepID || modelCompleted.Status != protocol.StepStatusCompleted || modelCompleted.Metadata["tool_call_count"] == nil || !hasFirstDelta {
		t.Fatalf("model step completed = %#v ok=%v", modelCompleted, ok)
	}
	if modelCompleted.Metadata["request_id"] != callStarted["request_id"] || modelCompleted.Metadata["input_tokens"] == nil {
		t.Fatalf("model step metadata missing request/usage: %#v", modelCompleted.Metadata)
	}
}

func TestSystemPromptDocumentsTerminalSafeMarkdown(t *testing.T) {
	prompt := systemPrompt()
	for _, want := range []string{
		"simple Markdown",
		"fenced code blocks",
		"simple pipe tables",
		"Do not put math formulas in code fences",
		"HTML",
		"images",
		"Mermaid",
		"LaTeX",
		"парилка",
		"telegram-parilka",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, prompt)
		}
	}
}

func TestInitialMessagesInjectProfileAsSystemContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	messages := InitialMessages(config.Config{
		Profile:            "billy",
		ProjectDocMaxBytes: 0,
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != protocol.RoleSystem || messages[1].Role != protocol.RoleSystem {
		t.Fatalf("roles = %#v", messages)
	}
	if !strings.Contains(messages[1].Content, "# Billyharness profile: billy") ||
		!strings.Contains(messages[1].Content, "<SOUL>") ||
		!strings.Contains(messages[1].Content, "Формулы пиши в LaTeX") {
		t.Fatalf("profile message = %s", messages[1].Content)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles", "billy", "SOUL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestInitialMessagesInjectAgentsAsUserContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("project rules"), 0o600); err != nil {
		t.Fatal(err)
	}

	messages := InitialMessages(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != protocol.RoleSystem {
		t.Fatalf("first role = %q", messages[0].Role)
	}
	if messages[1].Role != protocol.RoleUser || !strings.Contains(messages[1].Content, "# AGENTS.md instructions") {
		t.Fatalf("agents message = %#v", messages[1])
	}
}

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

func TestRunMessagesExecutesToolAndContinuesLoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 3
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"hello"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "write file"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello" {
		t.Fatalf("written content = %q", bytes)
	}
	if len(next) < 5 || next[len(next)-1].Content != "finished" {
		t.Fatalf("messages = %#v", next)
	}
	if !sawEvent(events, protocol.EventToolCallRequested) || !sawEvent(events, protocol.EventToolCallFinished) || !sawEvent(events, protocol.EventRunCompleted) {
		t.Fatalf("events missing tool/run completion: %#v", events)
	}
	assertAgentLifecycleValid(t, events)
	if !sawToolAudit(events, "fs_write_file", protocol.RiskWrite, true) {
		t.Fatalf("write tool audit event missing: %#v", events)
	}
	if result, ok := firstToolResult(events); !ok || result.Name != "fs_write_file" || result.CallID != "call_1" || result.IsError {
		t.Fatalf("tool result event = %#v ok=%v", result, ok)
	} else if result.Metadata["attempt_id"] == "" ||
		result.Metadata["permission_decision"] != "allow" ||
		result.Metadata["permission_source"] != "config" ||
		result.Metadata["permission_reason"] != "auto_approve_dangerous" {
		t.Fatalf("tool result metadata missing orchestrator fields: %#v", result.Metadata)
	}
	if !sawPermissionDecision(events, "fs_write_file", "allow", "config", "auto_approve_dangerous") {
		t.Fatalf("permission decision missing: %#v", events)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
}

func TestRunMessagesToolOrchestratorEmitsSafePermissionAndAttempt(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 2
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_time", ToolName: "time_now", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "time"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPermissionDecision(events, "time_now", "allow", "auto", "safe_tool") {
		t.Fatalf("safe permission decision missing: %#v", events)
	}
	decisionIndex := eventIndex(events, protocol.EventToolPermissionDecided)
	startIndex := eventIndex(events, protocol.EventToolCallStarted)
	if decisionIndex < 0 || startIndex < 0 || decisionIndex > startIndex {
		t.Fatalf("permission decision should precede call start; decision=%d start=%d events=%#v", decisionIndex, startIndex, events)
	}
	result, ok := firstToolResult(events)
	if !ok ||
		result.CallID != "call_time" ||
		result.Metadata["attempt_id"] == "" ||
		result.Metadata["tool_name"] != "time_now" ||
		result.Metadata["args_summary"] != "{}" ||
		result.Metadata["permission_decision"] != "allow" ||
		result.Metadata["permission_source"] != "auto" ||
		result.Metadata["started_at"] == nil ||
		result.Metadata["finished_at"] == nil ||
		result.Metadata["duration_ms"] == nil ||
		result.Metadata["output_bytes"] == nil ||
		result.Metadata["output_estimated_tokens"] == nil ||
		result.Metadata["truncated"] == nil {
		t.Fatalf("tool result metadata = %#v ok=%v", result, ok)
	}
	progress := toolProgressEvents(events, "call_time")
	wantPhases := []string{
		toolPhasePrepare,
		toolPhasePermissionDecision,
		toolPhaseAttemptStarted,
		toolPhaseExecuting,
		toolPhaseAttemptFinished,
		toolPhaseRetryDecision,
		toolPhaseFinalize,
	}
	if len(progress) != len(wantPhases) {
		t.Fatalf("tool progress events = %#v", progress)
	}
	for i, want := range wantPhases {
		if progress[i].Phase != want {
			t.Fatalf("progress[%d].phase = %q, want %q: %#v", i, progress[i].Phase, want, progress)
		}
	}
	if progress[0].Status != protocol.StepStatusStarted ||
		progress[1].Status != "allow" ||
		progress[3].Status != protocol.StepStatusStarted ||
		progress[4].Status != protocol.StepStatusCompleted ||
		progress[5].Status != toolProgressStatusSkipped ||
		progress[6].Status != protocol.StepStatusCompleted {
		t.Fatalf("tool progress statuses = %#v", progress)
	}
}

func TestRunMessagesToolOrchestratorDeniesDangerousToolBeforeExecution(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = false
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"blocked"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "write"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied tool should not write file, stat err=%v", err)
	}
	if !sawPermissionDecision(events, "fs_write_file", "deny", "config", "dangerous_tools_disabled") {
		t.Fatalf("deny permission decision missing: %#v", events)
	}
	result, ok := firstToolResult(events)
	if !ok || !result.IsError || result.ErrorCode != "permission_denied" || !strings.Contains(result.Content, "tool disabled") {
		t.Fatalf("denied tool result = %#v ok=%v", result, ok)
	}
	if !sawEvent(events, protocol.EventToolCallFailed) {
		t.Fatalf("tool.call_failed missing: %#v", events)
	}
	progress := toolProgressEvents(events, "call_write")
	if len(progress) == 0 || progress[len(progress)-1].Phase != toolPhaseFinalize || progress[len(progress)-1].Status != protocol.StepStatusFailed {
		t.Fatalf("denied tool progress = %#v", progress)
	}
}

func TestRunMessagesExecutesParallelSafeToolsConcurrentlyAndPreservesOrder(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	registry := tools.NewRegistry(cfg)
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_a",
			Description: "Wait for slow_b.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(startedA)
			select {
			case <-startedB:
				return tools.Result{Content: "A"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_b",
			Description: "Wait for slow_a.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(startedB)
			select {
			case <-startedA:
				return tools.Result{Content: "B"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "slow_a", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_b", ToolName: "slow_b", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var events []protocol.Event
	next, err := a.RunMessages(ctx, []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	var toolMessages []protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && (msg.Name == "slow_a" || msg.Name == "slow_b") {
			toolMessages = append(toolMessages, msg)
		}
	}
	if len(toolMessages) != 2 {
		t.Fatalf("tool messages = %#v", toolMessages)
	}
	if toolMessages[0].Name != "slow_a" || toolMessages[0].Content != "A" ||
		toolMessages[1].Name != "slow_b" || toolMessages[1].Content != "B" {
		t.Fatalf("tool message order/content = %#v", toolMessages)
	}
	batchStarted, ok := firstStepEvent(events, protocol.EventStepStarted, protocol.StepKindToolBatch)
	if !ok || !batchStarted.Parallel || batchStarted.BatchSize != 2 || batchStarted.ParallelLimit != 2 || batchStarted.BatchID == "" {
		t.Fatalf("parallel batch step started = %#v ok=%v", batchStarted, ok)
	}
	batchCompleted, ok := firstStepEvent(events, protocol.EventStepCompleted, protocol.StepKindToolBatch)
	if !ok || batchCompleted.StepID != batchStarted.StepID || batchCompleted.Status != protocol.StepStatusCompleted {
		t.Fatalf("parallel batch step completed = %#v ok=%v", batchCompleted, ok)
	}
	var parallelToolStarts int
	for _, event := range events {
		step, ok := stepEvent(event, protocol.EventStepStarted)
		if ok && step.Kind == protocol.StepKindToolCall && step.BatchID == batchStarted.BatchID && step.Parallel {
			if step.Metadata["parallel_policy"] != tools.ParallelPolicyReadOnly ||
				step.Metadata["parallel_decision"] != "parallel_batch" ||
				step.Metadata["parallel_safe"] != true ||
				step.Metadata["idempotent"] != true ||
				step.Metadata["requires_exclusive_workspace"] != false ||
				step.Metadata["cancellable"] != true ||
				step.Metadata["risk"] != string(protocol.RiskReadOnly) ||
				step.Metadata["attempt_id"] == "" ||
				step.Metadata["permission_decision"] != "allow" {
				t.Fatalf("parallel tool metadata = %#v", step.Metadata)
			}
			parallelToolStarts++
		}
	}
	if parallelToolStarts != 2 {
		t.Fatalf("parallel tool step starts = %d; events=%#v", parallelToolStarts, events)
	}
}

func TestRunMessagesParallelBatchCompletesOutOfOrderWithCallIDs(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	registry := tools.NewRegistry(cfg)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_first",
			Description: "Slow read-only test tool.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			time.Sleep(50 * time.Millisecond)
			return tools.Result{Content: "alpha"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "fast_second",
			Description: "Fast read-only test tool.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: "beta"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call-a", ToolName: "slow_first", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call-b", ToolName: "fast_second", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	var completed []string
	for _, event := range events {
		step, ok := stepEvent(event, protocol.EventStepCompleted)
		if ok && step.Kind == protocol.StepKindToolCall {
			completed = append(completed, step.ToolCallID)
		}
	}
	if len(completed) != 2 || completed[0] != "call-b" || completed[1] != "call-a" {
		t.Fatalf("tool completion call_id order = %#v; events=%#v", completed, events)
	}
	var toolMessages []protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && (msg.ToolCallID == "call-a" || msg.ToolCallID == "call-b") {
			toolMessages = append(toolMessages, msg)
		}
	}
	if len(toolMessages) != 2 ||
		toolMessages[0].ToolCallID != "call-a" || toolMessages[0].Content != "alpha" ||
		toolMessages[1].ToolCallID != "call-b" || toolMessages[1].Content != "beta" {
		t.Fatalf("tool message order/content = %#v", toolMessages)
	}
}

func TestRunMessagesEmitsConfiguredHookEvents(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.HooksEnabled = true
	cfg.Hooks = []config.Hook{
		testHook("session_start", "session_start"),
		testHook("before_tool", "before_tool"),
		testHook("after_tool", "after_tool"),
		testHook("session_done", "session_done"),
	}
	registry := tools.NewRegistry(cfg)
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call-hook", ToolName: "time_now", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tool"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hookEvent := range []string{"session_start", "before_tool", "after_tool", "session_done"} {
		if !sawHookFinished(events, hookEvent) {
			t.Fatalf("missing hook %s in events %#v", hookEvent, events)
		}
	}
	if !sawHookToolPayload(events, "before_tool", "call-hook", "time_now") ||
		!sawHookToolPayload(events, "after_tool", "call-hook", "time_now") {
		t.Fatalf("tool hook payload missing call/tool ids: %#v", events)
	}
}

func TestRunMessagesEmitsProviderRetryHook(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	cfg.HooksEnabled = true
	cfg.Hooks = []config.Hook{testHook("provider_retry", "provider_retry")}
	registry := tools.NewRegistry(cfg)
	prov := &scriptedProvider{steps: [][]provider.Event{{
		{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
			RequestID:         "req-1",
			ProviderID:        "deepseek",
			ModelID:           "deepseek-v4-flash",
			ProviderRequestID: "provider-req-2",
			Attempts:          2,
			Retries:           1,
			StatusCode:        200,
		}},
		{Kind: provider.EventContent, Text: "finished"},
		{Kind: provider.EventDone},
	}}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "say hi"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawHookFinished(events, "provider_retry") {
		t.Fatalf("missing provider_retry hook: %#v", events)
	}
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] == "provider_retry" && data["request_id"] == "req-1" && data["retries"] == float64(1) {
			return
		}
	}
	t.Fatalf("provider_retry payload missing request metadata: %#v", events)
}

func TestRunMessagesEmitsMCPStatusChangeHookSnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 1
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:    "remote",
		URL:     "https://example.com/mcp",
		Enabled: true,
	}}
	cfg.HooksEnabled = true
	cfg.Hooks = []config.Hook{testHook("mcp", "mcp_status_change")}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	a := New(cfg, &captureProvider{}, registry)
	var events []protocol.Event
	_, err = a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "say hi"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] != "mcp_status_change" {
			continue
		}
		if data["server_name"] != "remote" ||
			data["transport"] != "streamable-http" ||
			data["state"] != "unsupported" ||
			data["connected"] != false {
			t.Fatalf("mcp status hook payload = %#v", data)
		}
		payload, _ := data["payload"].(map[string]any)
		if payload["phase"] != "snapshot" || payload["unsupported_reason"] == "" {
			t.Fatalf("mcp status hook nested payload = %#v", payload)
		}
		return
	}
	t.Fatalf("missing mcp_status_change hook: %#v", events)
}

func testHook(name, event string) config.Hook {
	return config.Hook{
		Name:           name,
		Event:          event,
		Command:        "sh",
		Args:           []string{"-c", "cat >/dev/null"},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
		Enabled:        true,
	}
}

func TestRunMessagesExclusiveToolBreaksParallelBatches(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	cfg.AutoApproveDangerous = true
	registry := tools.NewRegistry(cfg)
	for _, name := range []string{"read_a", "read_b", "read_c", "read_d"} {
		name := name
		if err := registry.Register(tools.Tool{
			Spec: protocol.ToolSpec{
				Name:        name,
				Description: "Read-only test tool.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Risk:        protocol.RiskReadOnly,
			},
			Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: name}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "write_x",
			Description: "Exclusive write test tool.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: "write"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "read_a", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_b", ToolName: "read_b", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 2, ToolID: "call_w", ToolName: "write_x", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 3, ToolID: "call_c", ToolName: "read_c", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 4, ToolID: "call_d", ToolName: "read_d", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "mixed tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	var parallelBatches int
	var sawExclusiveWrite bool
	for _, event := range events {
		if step, ok := stepEvent(event, protocol.EventStepStarted); ok {
			if step.Kind == protocol.StepKindToolBatch && step.Parallel && step.BatchSize == 2 {
				parallelBatches++
			}
			if step.Kind == protocol.StepKindToolCall && step.Name == "write_x" {
				sawExclusiveWrite = step.Parallel == false &&
					step.Metadata["parallel_policy"] == tools.ParallelPolicyExclusiveWorkspace &&
					step.Metadata["parallel_decision"] == "serial_policy_"+tools.ParallelPolicyExclusiveWorkspace &&
					step.Metadata["requires_exclusive_workspace"] == true
			}
		}
	}
	if parallelBatches != 2 || !sawExclusiveWrite {
		t.Fatalf("parallelBatches=%d sawExclusiveWrite=%v events=%#v", parallelBatches, sawExclusiveWrite, events)
	}
}

func TestRunMessagesRateLimitsNetworkParallelBatch(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 5
	registry := tools.NewRegistry(cfg)
	var active int32
	var maxActive int32
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("web_like_%d", i)
		if err := registry.Register(tools.Tool{
			Spec: protocol.ToolSpec{
				Name:        name,
				Description: "Rate-limited network test tool.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Risk:        protocol.RiskNetwork,
			},
			Parallel: tools.ParallelMetadata{
				Policy:         tools.ParallelPolicyNetworkRateLimited,
				Idempotent:     true,
				RateLimitKey:   "webtest",
				Cancellable:    true,
				MaxConcurrency: 2,
			},
			Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
				now := atomic.AddInt32(&active, 1)
				for {
					seen := atomic.LoadInt32(&maxActive)
					if now <= seen || atomic.CompareAndSwapInt32(&maxActive, seen, now) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return tools.Result{Content: "ok"}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_0", ToolName: "web_like_0", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_1", ToolName: "web_like_1", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 2, ToolID: "call_2", ToolName: "web_like_2", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 3, ToolID: "call_3", ToolName: "web_like_3", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 4, ToolID: "call_4", ToolName: "web_like_4", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "network batch"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&maxActive); got > 2 {
		t.Fatalf("network tools exceeded bucket concurrency: maxActive=%d events=%#v", got, events)
	}
	batchStarted, ok := firstStepEvent(events, protocol.EventStepStarted, protocol.StepKindToolBatch)
	if !ok || batchStarted.ParallelLimit != 5 || batchStarted.BatchSize != 5 {
		t.Fatalf("batch started = %#v ok=%v", batchStarted, ok)
	}
	var sawRateLimitedTool bool
	for _, event := range events {
		step, ok := stepEvent(event, protocol.EventStepStarted)
		if ok && step.Kind == protocol.StepKindToolCall && step.Name == "web_like_0" {
			sawRateLimitedTool = step.Metadata["rate_limit_key"] == "webtest" &&
				step.Metadata["max_concurrency"] == float64(2) &&
				step.Metadata["parallel_policy"] == tools.ParallelPolicyNetworkRateLimited
		}
	}
	if !sawRateLimitedTool {
		t.Fatalf("rate-limited tool metadata missing: %#v", events)
	}
}

func TestRunMessagesRecordsAbortWhenActiveToolIsCanceled(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	started := make(chan struct{})
	registry := tools.NewRegistry(cfg)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "wait_for_cancel",
			Description: "Wait until the run context is canceled.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(started)
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &cancelAfterToolProvider{}
	a := New(cfg, prov, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []protocol.Event
	done := make(chan error, 1)
	go func() {
		_, err := a.RunMessages(ctx, []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "cancel tool"},
		}, func(event protocol.Event) {
			events = append(events, event)
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	assertAgentLifecycleValid(t, events)
	var aborted protocol.ToolResult
	var sawAborted bool
	for _, event := range events {
		if event.Type != protocol.EventToolCallAborted {
			continue
		}
		bytes, _ := json.Marshal(event.Data)
		if json.Unmarshal(bytes, &aborted) == nil {
			sawAborted = true
			break
		}
	}
	if !sawAborted || aborted.CallID != "call_cancel" || aborted.ErrorCode != "tool_aborted" || aborted.Metadata["attempt_id"] == "" {
		t.Fatalf("aborted result = %#v saw=%v events=%#v", aborted, sawAborted, events)
	}
	var sawCancelProgress bool
	for _, progress := range toolProgressEvents(events, "call_cancel") {
		if progress.Phase == toolPhaseCancelAbort && progress.Status == toolProgressStatusAborted {
			sawCancelProgress = true
			break
		}
	}
	if !sawCancelProgress {
		t.Fatalf("cancel progress missing: %#v", toolProgressEvents(events, "call_cancel"))
	}
}

func TestRunMessagesModelStreamCancellationIsLifecycleValid(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	prov := &cancelDuringModelProvider{started: make(chan struct{})}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []protocol.Event
	done := make(chan error, 1)
	go func() {
		_, err := a.RunMessages(ctx, []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "cancel model"},
		}, func(event protocol.Event) {
			events = append(events, event)
		})
		done <- err
	}()
	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("model stream did not start")
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	assertAgentLifecycleValid(t, events)
	finished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		finished["status"] != protocol.StepStatusFailed ||
		!strings.Contains(fmt.Sprint(finished["error"]), context.Canceled.Error()) {
		t.Fatalf("model.call_finished = %#v ok=%v", finished, ok)
	}
	if !sawEvent(events, protocol.EventRunFailed) {
		t.Fatalf("run.failed missing after model cancellation: %#v", events)
	}
}

func TestRunMessagesStoresLargeToolOutputAndSendsPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxToolOutputBytes = 128
	registry := tools.NewRegistry(cfg)
	fullOutput := strings.Repeat("large-output-", 80)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "big_output",
			Description: "Return a large output.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: fullOutput}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_big", ToolName: "big_output", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run big tool"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := firstToolResult(events)
	if !ok || !result.Truncated || result.OutputRef == "" {
		t.Fatalf("tool result = %#v ok=%v", result, ok)
	}
	if strings.Contains(result.Content, fullOutput) || !strings.Contains(result.Content, "full tool output saved as plaintext") {
		t.Fatalf("result content should be preview with saved-output note: %q", result.Content)
	}
	bytes, err := os.ReadFile(result.OutputRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != fullOutput {
		t.Fatalf("stored output mismatch")
	}
	if !strings.HasPrefix(result.OutputRef, filepath.Join(home, "tool-output")) {
		t.Fatalf("output ref = %q, want under billy home %q", result.OutputRef, home)
	}
	assertMode(t, filepath.Join(home, "tool-output"), 0o700)
	assertMode(t, filepath.Dir(result.OutputRef), 0o700)
	assertMode(t, result.OutputRef, 0o600)
	if result.Metadata["output_ref_plaintext"] != true ||
		result.Metadata["output_ref_permissions"] != "0600" ||
		result.Metadata["output_ref_id"] == "" ||
		result.Metadata["output_ref_bytes"] == nil ||
		result.Metadata["output_ref_sha256"] == "" {
		t.Fatalf("metadata should make plaintext persistence explicit: %#v", result.Metadata)
	}
	refEvent, ok := firstEventData(events, protocol.EventToolOutputRefCreated)
	if !ok ||
		refEvent["output_ref"] != result.OutputRef ||
		refEvent["output_ref_id"] != result.Metadata["output_ref_id"] ||
		refEvent["output_ref_sha256"] != result.Metadata["output_ref_sha256"] ||
		refEvent["output_ref_permissions"] != "0600" ||
		refEvent["output_ref_plaintext"] != true ||
		refEvent["output_ref_bytes"] == nil {
		t.Fatalf("output ref event = %#v metadata=%#v ok=%v", refEvent, result.Metadata, ok)
	}
	var toolMessage protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && msg.Name == "big_output" {
			toolMessage = msg
			break
		}
	}
	if toolMessage.Content == "" || strings.Contains(toolMessage.Content, fullOutput) || !strings.Contains(toolMessage.Content, result.OutputRef) {
		t.Fatalf("tool message should contain preview and output ref, got %#v", toolMessage)
	}
}

func TestRunMessagesExecutesMCPToolAndContinuesLoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 3
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestAgentFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_AGENT_MCP_HELPER": "1"},
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		Enabled:        true,
	}}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_mcp", ToolName: "mcp_call", ArgsDelta: `{"name":"mcp__fake__echo","arguments":{"text":"mcp ok"}}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "use mcp"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
	if hasToolSpec(prov.lastTools, "mcp__fake__echo") || !hasToolSpec(prov.lastTools, "mcp_call") {
		t.Fatalf("provider saw wrong MCP tools: %#v", prov.lastTools)
	}
	if !sawToolStarted(events, "mcp_call") {
		t.Fatalf("MCP tool start event missing: %#v", events)
	}
	var sawToolMessage bool
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && msg.Name == "mcp_call" && msg.Content == "mcp ok" {
			sawToolMessage = true
		}
	}
	if !sawToolMessage {
		t.Fatalf("MCP tool result not in messages: %#v", next)
	}
	if !hasMCPInstructions(next) {
		t.Fatalf("MCP instructions not preserved in messages: %#v", next)
	}
	injected := a.withMCPInstructions([]protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleSystem, Content: compactionMarker + "\nold summary"},
		{Role: protocol.RoleUser, Content: "continue"},
	})
	if len(injected) != 4 ||
		!strings.HasPrefix(injected[1].Content, "# MCP server instructions") ||
		!strings.HasPrefix(injected[2].Content, compactionMarker) {
		t.Fatalf("MCP instructions should be inserted into protected prefix before prior summary: %#v", injected)
	}
}

func TestRunMessagesReturnsMaxRoundsError(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	prov := &scriptedProvider{repeat: []provider.Event{
		{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{}`},
		{Kind: provider.EventDone},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "loop"},
	}, func(event protocol.Event) {
		if event.Type == protocol.EventRunFailed {
			failed = true
		}
	})
	if err == nil || !strings.Contains(err.Error(), "exceeded max tool rounds: 1") {
		t.Fatalf("err = %v", err)
	}
	if !failed {
		t.Fatalf("run.failed event not emitted")
	}
}

func TestRunMessagesTurnsInvalidToolArgumentsIntoToolError(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 2
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{bad`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "recovered"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	var toolFailed bool
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "bad tool"},
	}, func(event protocol.Event) {
		if event.Type == protocol.EventRunFailed {
			failed = true
		}
		if event.Type == protocol.EventToolCallFailed {
			result, ok := event.Data.(protocol.ToolResult)
			if ok && result.ErrorCode == "invalid_json_args" && strings.Contains(result.Content, "not valid JSON") {
				toolFailed = true
			}
		}
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if failed {
		t.Fatalf("run.failed should not be emitted for malformed tool args")
	}
	if !toolFailed {
		t.Fatalf("tool.call_failed invalid_json_args not emitted")
	}
	if len(next) == 0 || next[len(next)-1].Content != "recovered" {
		t.Fatalf("messages = %#v", next)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("requests = %d", len(prov.requests))
	}
	second := prov.requests[1].Messages
	if len(second) < 2 {
		t.Fatalf("second request messages = %#v", second)
	}
	assistant := second[len(second)-2]
	tool := second[len(second)-1]
	if assistant.Role != protocol.RoleAssistant || len(assistant.ToolCalls) != 1 || string(assistant.ToolCalls[0].Arguments) != "{}" {
		t.Fatalf("assistant malformed call should be sanitized: %#v", assistant)
	}
	if assistant.ToolCalls[0].InvalidArguments != "{bad" {
		t.Fatalf("invalid args should be retained in memory: %#v", assistant.ToolCalls[0])
	}
	if tool.Role != protocol.RoleTool || tool.ToolCallID != "call_1" || !strings.Contains(tool.Content, "not valid JSON") {
		t.Fatalf("tool result message = %#v", tool)
	}
}

func TestRunMessagesEmitsRunFailedOnProviderError(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.MaxToolRounds = 1
	wantErr := &provider.ProviderError{
		Provider:  "deepseek",
		ModelID:   "deepseek-v4-flash",
		Kind:      provider.ErrorServer,
		Status:    503,
		Message:   "provider exploded",
		RequestID: "deepseek-request-3",
		Attempts:  3,
		Retries:   2,
	}
	prov := &scriptedProvider{err: wantErr}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "fail"},
	}, func(event protocol.Event) {
		events = append(events, event)
		if event.Type == protocol.EventRunFailed && fmt.Sprint(event.Data) == wantErr.Error() {
			failed = true
		}
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v", err)
	}
	if !failed {
		t.Fatalf("run.failed event not emitted")
	}
	finished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		finished["status"] != protocol.StepStatusFailed ||
		finished["provider_id"] != "deepseek" ||
		finished["model_id"] != "deepseek-v4-flash" ||
		finished["provider_request_id"] != "deepseek-request-3" ||
		finished["status_code"] != float64(503) ||
		finished["attempts"] != float64(3) ||
		finished["retries"] != float64(2) ||
		!strings.Contains(fmt.Sprint(finished["error"]), "provider exploded") {
		t.Fatalf("model.call_finished = %#v ok=%v", finished, ok)
	}
}

type captureProvider struct {
	messages []protocol.Message
}

func (p *captureProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.messages = append([]protocol.Message(nil), req.Messages...)
	events := make(chan provider.Event, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		events <- provider.Event{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
			RequestID:         req.RequestID,
			ProviderID:        "mock",
			ModelID:           req.Model,
			ProviderRequestID: "mock-request",
			Attempts:          1,
			Retries:           0,
			StatusCode:        200,
		}}
		events <- provider.Event{Kind: provider.EventContent, Text: "done"}
		events <- provider.Event{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 12, OutputTokens: 3, CacheHitTokens: 7, CacheMissTokens: 5}}
		events <- provider.Event{Kind: provider.EventDone}
	}()
	return events, errs
}

type scriptedProvider struct {
	steps     [][]provider.Event
	repeat    []provider.Event
	err       error
	calls     int
	lastTools []protocol.ToolSpec
	requests  []provider.Request
}

type staticContentProvider struct {
	text  string
	usage provider.Usage
}

func (p staticContentProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, 3)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		select {
		case events <- provider.Event{Kind: provider.EventContent, Text: p.text}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		if p.usage != (provider.Usage{}) {
			events <- provider.Event{Kind: provider.EventUsage, Usage: p.usage}
		}
		events <- provider.Event{Kind: provider.EventDone}
	}()
	return events, errs
}

func (p *scriptedProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.calls++
	p.lastTools = req.Tools
	p.requests = append(p.requests, req)
	events := make(chan provider.Event, 8)
	errs := make(chan error, 1)
	call := p.calls
	go func() {
		var streamErr error
		defer func() {
			if streamErr != nil {
				errs <- streamErr
			}
			close(errs)
		}()
		defer close(events)
		if p.err != nil {
			streamErr = p.err
			return
		}
		step := p.repeat
		if call-1 < len(p.steps) {
			step = p.steps[call-1]
		}
		for _, event := range step {
			select {
			case events <- event:
			case <-ctx.Done():
				streamErr = ctx.Err()
				return
			}
		}
	}()
	return events, errs
}

type cancelAfterToolProvider struct {
	calls int
}

type cancelDuringModelProvider struct {
	started chan struct{}
}

func (p *cancelDuringModelProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event)
	errs := make(chan error, 1)
	go func() {
		var streamErr error
		defer func() {
			if streamErr != nil {
				errs <- streamErr
			}
			close(errs)
		}()
		defer close(events)
		close(p.started)
		<-ctx.Done()
		streamErr = ctx.Err()
	}()
	return events, errs
}

func (p *cancelAfterToolProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.calls++
	events := make(chan provider.Event, 2)
	errs := make(chan error, 1)
	call := p.calls
	go func() {
		defer close(events)
		defer close(errs)
		if call == 1 {
			events <- provider.Event{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_cancel", ToolName: "wait_for_cancel", ArgsDelta: `{}`}
			events <- provider.Event{Kind: provider.EventDone}
			return
		}
		<-ctx.Done()
		errs <- ctx.Err()
	}()
	return events, errs
}

func sawEvent(events []protocol.Event, typ protocol.EventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func assertAgentLifecycleValid(t *testing.T, events []protocol.Event) {
	t.Helper()
	if err := eventlog.ValidateLifecycle(events); err != nil {
		t.Fatalf("event lifecycle invalid: %v\nevents=%#v", err, events)
	}
}

func firstTurnEvent(events []protocol.Event, typ protocol.EventType) (protocol.TurnEvent, bool) {
	for _, event := range events {
		if event.Type != typ {
			continue
		}
		var turn protocol.TurnEvent
		bytes, _ := json.Marshal(event.Data)
		if err := json.Unmarshal(bytes, &turn); err == nil {
			return turn, true
		}
	}
	return protocol.TurnEvent{}, false
}

func firstStepEvent(events []protocol.Event, typ protocol.EventType, kind string) (protocol.StepEvent, bool) {
	for _, event := range events {
		step, ok := stepEvent(event, typ)
		if ok && step.Kind == kind {
			return step, true
		}
	}
	return protocol.StepEvent{}, false
}

func stepEvent(event protocol.Event, typ protocol.EventType) (protocol.StepEvent, bool) {
	if event.Type != typ {
		return protocol.StepEvent{}, false
	}
	var step protocol.StepEvent
	bytes, _ := json.Marshal(event.Data)
	if err := json.Unmarshal(bytes, &step); err != nil {
		return protocol.StepEvent{}, false
	}
	return step, true
}

func sawToolStarted(events []protocol.Event, name string) bool {
	for _, event := range events {
		if event.Type == protocol.EventToolCallStarted && fmt.Sprint(event.Data) == name {
			return true
		}
	}
	return false
}

func sawToolAudit(events []protocol.Event, name string, risk protocol.Risk, autoApproved bool) bool {
	for _, event := range events {
		if event.Type != protocol.EventToolAudit {
			continue
		}
		bytes, _ := json.Marshal(event.Data)
		var data struct {
			Name         string        `json:"name"`
			Risk         protocol.Risk `json:"risk"`
			AutoApproved bool          `json:"auto_approved"`
		}
		_ = json.Unmarshal(bytes, &data)
		if data.Name == name && data.Risk == risk && data.AutoApproved == autoApproved {
			return true
		}
	}
	return false
}

func sawHookFinished(events []protocol.Event, hookEvent string) bool {
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] == hookEvent {
			return true
		}
	}
	return false
}

func sawHookToolPayload(events []protocol.Event, hookEvent, callID, toolName string) bool {
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] == hookEvent && data["call_id"] == callID && data["tool_name"] == toolName {
			return true
		}
	}
	return false
}

func sawPermissionDecision(events []protocol.Event, name, decision, source, reason string) bool {
	for _, event := range events {
		if event.Type != protocol.EventToolPermissionDecided {
			continue
		}
		data := eventDataMap(event)
		if data["name"] == name &&
			data["decision"] == decision &&
			data["source"] == source &&
			data["reason"] == reason {
			return true
		}
	}
	return false
}

func toolProgressEvents(events []protocol.Event, callID string) []protocol.ToolProgressEvent {
	var progress []protocol.ToolProgressEvent
	for _, event := range events {
		if event.Type != protocol.EventToolCallProgress {
			continue
		}
		var item protocol.ToolProgressEvent
		bytes, _ := json.Marshal(event.Data)
		if err := json.Unmarshal(bytes, &item); err != nil {
			continue
		}
		if item.CallID == callID {
			progress = append(progress, item)
		}
	}
	return progress
}

func eventIndex(events []protocol.Event, typ protocol.EventType) int {
	for i, event := range events {
		if event.Type == typ {
			return i
		}
	}
	return -1
}

func firstEventData(events []protocol.Event, typ protocol.EventType) (map[string]any, bool) {
	for _, event := range events {
		if event.Type == typ {
			return eventDataMap(event), true
		}
	}
	return nil, false
}

func eventDataMap(event protocol.Event) map[string]any {
	bytes, _ := json.Marshal(event.Data)
	var data map[string]any
	_ = json.Unmarshal(bytes, &data)
	return data
}

func firstToolResult(events []protocol.Event) (protocol.ToolResult, bool) {
	for _, event := range events {
		if event.Type != protocol.EventToolCallFinished {
			continue
		}
		result, ok := event.Data.(protocol.ToolResult)
		return result, ok
	}
	return protocol.ToolResult{}, false
}

func hasToolSpec(specs []protocol.ToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestAgentFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_AGENT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
				"instructions":    "Use echo when asked to repeat text.",
			}})
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo text",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false},
			}}}})
		case "tools/call":
			var call struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &call)
			text := fmt.Sprint(call.Arguments["text"])
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}
