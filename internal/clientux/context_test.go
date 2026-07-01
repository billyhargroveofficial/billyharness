package clientux

import (
	"strings"
	"testing"

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
	for _, source := range []string{"web_summaries", "mcp_outputs", "assistant_tool_calls", "user_messages", "system_instructions", "reasoning_summaries"} {
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
	for _, want := range []string{"active context:", "thresholds:", "web_summaries", "mcp_outputs", "top contributors:", "large inline", "output_ref"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted context missing %q:\n%s", want, formatted)
		}
	}
}
