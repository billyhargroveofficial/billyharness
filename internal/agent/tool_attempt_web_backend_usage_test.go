package agent

import (
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestProviderHelperUsageFromToolResultIncludesWebBackendUsage(t *testing.T) {
	usage, ok := providerHelperUsageFromToolResult(toolExecutionResult{
		RunID:     "run-1",
		TurnID:    "turn-1",
		StepID:    "step-1",
		AttemptID: "attempt-1",
		Call:      protocol.ToolCall{ID: "call-web", Name: "web_search"},
		Result: protocol.ToolResult{Metadata: map[string]any{
			"web_backend":      "exa",
			"helper_api_calls": 1,
			"helper_cost_usd":  0.003,
		}},
	})
	if !ok {
		t.Fatal("helper usage missing")
	}
	if usage.Kind != "web_backend" || usage.Provider != "exa" || usage.APICalls != 1 || usage.CostUSD != 0.003 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.CallID != "call-web" || usage.RunID != "run-1" || usage.AttemptID != "attempt-1" {
		t.Fatalf("usage identity = %#v", usage)
	}
}
