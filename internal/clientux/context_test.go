package clientux

import (
	"strings"
	"testing"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestContextStatusClassifiesSourcesAndThresholds(t *testing.T) {
	cfg := config.Default()
	cfg.ContextWindowTokens = 1000
	cfg.ContextCompactTokens = 600
	cfg.MaxToolOutputBytes = 256
	resp := BuildContextResponse(cfg.RuntimeLimits(), "session-test", []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system instructions"},
		{Role: protocol.RoleUser, Content: "# Memory context\n<MEMORY_CONTEXT>\nentries:\n- type=\"user\" topic=\"style\" summary=\"concise\" path=\"topics/style.md\" source=\"home\"\n</MEMORY_CONTEXT>"},
		{Role: protocol.RoleUser, Content: "# Project context\n<PROJECT_CONTEXT>\ncwd: /repo\n</PROJECT_CONTEXT>"},
		{Role: protocol.RoleUser, Content: strings.Repeat("ask ", 90)},
		{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{ID: "call_1", Name: "web_fetch", Arguments: []byte(`{"url":"https://example.com/a/very/long/path"}`)}}},
		{Role: protocol.RoleTool, Name: "web_fetch", ToolCallID: "call_1", Content: strings.Repeat("web summary ", 120) + " output_ref=/tmp/ref"},
		{Role: protocol.RoleTool, Name: "mcp_call", ToolCallID: "call_2", Content: strings.Repeat("mcp output ", 20)},
		{Role: protocol.RoleAssistant, Content: "answer", ReasoningContent: strings.Repeat("reasoning summary ", 20)},
	})
	if resp.ID != "session-test" || resp.EstimatedTokens <= 500 {
		t.Fatalf("context response = %#v", resp)
	}
	sourceTokens := map[string]int64{}
	for _, source := range resp.Sources {
		sourceTokens[source.Source] = source.EstimatedTokens
	}
	for _, source := range []string{"memory_context", "project_context", "web_summaries", "mcp_outputs", "assistant_tool_calls", "user_messages", "system_instructions", "reasoning_summaries"} {
		if sourceTokens[source] <= 0 {
			t.Fatalf("missing source %s in %#v", source, resp.Sources)
		}
	}
	var webSource gatewayapi.ContextSource
	for _, source := range resp.Sources {
		if source.Source == "web_summaries" {
			webSource = source
			break
		}
	}
	if webSource.LargeInlineCount != 1 || webSource.OutputRefCount != 1 {
		t.Fatalf("web diagnostics = %#v", webSource)
	}
	if len(resp.TopContributors) == 0 || !resp.TopContributors[0].LargeInline || !resp.TopContributors[0].HasOutputRef {
		t.Fatalf("top contributor diagnostics = %#v", resp.TopContributors)
	}
	if len(resp.Thresholds) != 4 || !resp.Thresholds[0].Crossed || resp.Thresholds[3].Crossed {
		t.Fatalf("thresholds = %#v", resp.Thresholds)
	}
	formatted := gatewayclient.FormatSessionContext(resp)
	for _, want := range []string{"active context:", "thresholds:", "memory_context", "project_context", "web_summaries", "mcp_outputs", "top contributors:", "large inline", "output_ref"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted context missing %q:\n%s", want, formatted)
		}
	}
}

func TestContextStatusCountsAttachmentsWithoutImageBytes(t *testing.T) {
	cfg := config.Default()
	resp := BuildContextResponse(cfg.RuntimeLimits(), "session-images", []protocol.Message{
		protocol.UserMessage("", []protocol.AttachmentRef{{
			ID:         "att_test",
			Kind:       protocol.AttachmentKindImage,
			StorageRef: "att_test.png",
			FileName:   "screen.png",
			MIMEType:   "image/png",
			SizeBytes:  123,
			Width:      1,
			Height:     1,
			SHA256:     "abc123",
		}}),
	})
	if resp.AttachmentCount != 1 || resp.ImageSubmissions != 1 || resp.EstimatedTokens > 8 {
		t.Fatalf("context response = %#v", resp)
	}
	if formatted := gatewayclient.FormatSessionContext(resp); !strings.Contains(formatted, "attachments: 1 image_submissions=1") {
		t.Fatalf("formatted context missing image metrics: %q", formatted)
	}
	if len(resp.TopContributors) != 1 || resp.TopContributors[0].Preview != "[1 attachment]" {
		t.Fatalf("top contributors = %#v", resp.TopContributors)
	}
}

func TestContextReportV2IncludesEventsRuntimePromptAndHelperUsage(t *testing.T) {
	cfg := config.Default()
	cfg.ContextWindowTokens = 1000
	cfg.ContextCompactTokens = 600
	inventory := &protocol.PromptInventory{
		Hash:            "inventory-sha",
		ToolSchemaCount: 2,
		TotalBytes:      120,
		ApproxTokens:    30,
		Sections: []protocol.PromptSection{{
			Name:         "system_prompt",
			Role:         protocol.RoleSystem,
			Index:        0,
			ByteCount:    80,
			ApproxTokens: 20,
			SHA256:       "system-sha",
		}},
	}
	resp := BuildContextResponseWithOptions(cfg.RuntimeLimits(), "session-v2", []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleTool, Name: "web_fetch", Content: strings.Repeat("web ", 80) + "output_ref=/tmp/ref"},
	}, ContextReportOptions{
		Runtime: gatewayapi.ContextRuntime{Profile: "billy", AccessMode: "build"},
		Events: []protocol.Event{
			{Seq: 1, Type: protocol.EventModelCallStarted, Data: protocol.ModelCallEvent{
				ProviderID:          "deepseek",
				ModelID:             "deepseek-v4-flash",
				ReasoningMode:       "enabled/high",
				PromptInventoryHash: "inventory-sha",
				PromptInventory:     inventory,
				PromptCacheBreak:    &protocol.PromptCacheBreak{Status: "changed", Reason: "prompt_section:project_context"},
			}},
			{Seq: 2, Type: protocol.EventProviderUsageUpdate, Data: map[string]any{"input_tokens": 100, "output_tokens": 5, "cache_hit_tokens": 70, "cache_miss_tokens": 30}},
			{Seq: 3, Type: protocol.EventProviderUsageUpdate, Data: map[string]any{"input_tokens": 125, "output_tokens": 8, "cache_hit_tokens": 90, "cache_miss_tokens": 35}},
			{Seq: 4, Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-web", Name: "web_fetch"}},
			{Seq: 5, Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
				Kind:            "web_summary",
				CallID:          "call-web",
				InputTokens:     90,
				OutputTokens:    10,
				CacheHitTokens:  50,
				CacheMissTokens: 40,
				APITokens:       100,
			}},
			{Seq: 6, Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
				Kind:     "web_backend",
				Provider: "exa",
				CallID:   "call-search",
				APICalls: 1,
				CostUSD:  0.003,
			}},
			{Seq: 7, Type: protocol.EventToolCallFinished, CallID: "call-web", Data: protocol.ToolResult{CallID: "call-web", Name: "web_fetch", Metadata: map[string]any{
				"tool_summary_input_tokens":          200,
				"tool_summary_output_tokens":         25,
				"tool_summary_api_input_tokens":      90,
				"tool_summary_api_output_tokens":     10,
				"tool_summary_api_total_tokens":      100,
				"tool_summary_external_model_used":   true,
				"tool_summary_api_cache_hit_tokens":  50,
				"tool_summary_api_cache_miss_tokens": 40,
			}}},
			{Seq: 8, Type: protocol.EventToolOutputRefCreated},
			{Seq: 9, Type: protocol.EventContextCompacted, Data: map[string]any{
				"compaction_id":           "compact-1",
				"summary_strategy":        "deterministic",
				"before_estimated_tokens": 800,
				"after_estimated_tokens":  320,
				"reason":                  "prompt_tokens_at_or_above_threshold",
			}},
		},
	})
	if resp.Runtime.Model != "deepseek-v4-flash" || resp.Runtime.Provider != "deepseek" ||
		resp.Runtime.Profile != "billy" || resp.Runtime.AccessMode != "build" ||
		resp.Runtime.ReasoningMode != "enabled/high" {
		t.Fatalf("runtime = %#v", resp.Runtime)
	}
	if resp.Usage.ModelCalls != 1 || resp.Usage.ToolCalls != 1 ||
		resp.Usage.InputTokens != 125 || resp.Usage.OutputTokens != 8 ||
		resp.Usage.CacheHitTokens != 90 || resp.Usage.CacheMissTokens != 35 ||
		resp.Usage.LastCacheHitTokens != 90 || resp.Usage.LastCacheMissTokens != 35 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	if resp.Usage.WebSummaryInputTokens != 200 || resp.Usage.WebSummaryOutputTokens != 25 ||
		resp.Usage.HelperModelCalls != 1 ||
		resp.Usage.HelperModelInputTokens != 90 || resp.Usage.HelperModelOutputTokens != 10 ||
		resp.Usage.HelperModelCacheHit != 50 || resp.Usage.HelperModelCacheMiss != 40 ||
		resp.Usage.HelperModelAPITokens != 100 ||
		resp.Usage.HelperAPICalls != 1 || resp.Usage.HelperCostUSD != 0.003 {
		t.Fatalf("helper usage = %#v", resp.Usage)
	}
	if resp.Prompt.InventoryHash != "inventory-sha" || resp.Prompt.SectionCount != 1 ||
		resp.Prompt.CacheStatus != "changed" || resp.Prompt.CacheReason != "prompt_section:project_context" {
		t.Fatalf("prompt = %#v", resp.Prompt)
	}
	if resp.LastCompaction == nil || resp.LastCompaction.CompactionID != "compact-1" ||
		resp.LastCompaction.BeforeTokens != 800 || resp.LastCompaction.AfterTokens != 320 {
		t.Fatalf("last compaction = %#v", resp.LastCompaction)
	}
	if resp.OutputRefs.Count != 1 || resp.OutputRefs.SourceBucketCount != 1 {
		t.Fatalf("output refs = %#v", resp.OutputRefs)
	}
	formatted := gatewayclient.FormatSessionContext(resp)
	for _, want := range []string{"runtime:", "cache: hit=90", "helper usage: websum=200", "provider_api_calls=1", "provider_cost=$0.003000", "prompt sections:", "prompt cache: status=changed", "last compaction:", "output refs:"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted v2 context missing %q:\n%s", want, formatted)
		}
	}
}

func TestContextToolCountMatchesRequestedToolCalls(t *testing.T) {
	cfg := config.Default()
	events := toolCountSemanticsEvents()

	p := uxprojector.New()
	var snapshot uxprojector.Snapshot
	for _, event := range events {
		snapshot = p.Apply(event)
	}
	if snapshot.ToolCalls != 3 {
		t.Fatalf("projector tool calls = %d, want 3", snapshot.ToolCalls)
	}

	resp := BuildContextResponseWithOptions(cfg.RuntimeLimits(), "session-tools", nil, ContextReportOptions{Events: events})
	if resp.Usage.ToolCalls != snapshot.ToolCalls {
		t.Fatalf("/context tool calls = %d, projector = %d", resp.Usage.ToolCalls, snapshot.ToolCalls)
	}
	formatted := gatewayclient.FormatSessionContext(resp)
	if !strings.Contains(formatted, "activity: model_calls=1 tools=3") {
		t.Fatalf("formatted context missing requested-count activity:\n%s", formatted)
	}
	if strings.Contains(formatted, "tools=4") {
		t.Fatalf("formatted context counted started-only tool:\n%s", formatted)
	}
}

func toolCountSemanticsEvents() []protocol.Event {
	return []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted},
		{Seq: 3, Type: protocol.EventToolCallRequested, CallID: "call-requested-only", Data: protocol.ToolCall{ID: "call-requested-only", Name: "time_now"}},
		{Seq: 4, Type: protocol.EventToolCallRequested, CallID: "call-failed", Data: protocol.ToolCall{ID: "call-failed", Name: "shell_exec"}},
		{Seq: 5, Type: protocol.EventToolCallStarted, CallID: "call-failed"},
		{Seq: 6, Type: protocol.EventToolCallFailed, CallID: "call-failed", Data: protocol.ToolResult{CallID: "call-failed", Name: "shell_exec", Content: "permission denied", IsError: true}},
		{Seq: 7, Type: protocol.EventToolCallRequested, CallID: "call-aborted", Data: protocol.ToolCall{ID: "call-aborted", Name: "web_fetch"}},
		{Seq: 8, Type: protocol.EventToolCallStarted, CallID: "call-aborted"},
		{Seq: 9, Type: protocol.EventToolCallAborted, CallID: "call-aborted", Data: protocol.ToolResult{CallID: "call-aborted", Name: "web_fetch", Content: "interrupted", IsError: true}},
		{Seq: 10, Type: protocol.EventToolCallStarted, CallID: "call-started-only"},
		{Seq: 11, Type: protocol.EventRunCompleted},
	}
}
