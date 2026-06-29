package discovery

import (
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestSearchAppliesSharedFilters(t *testing.T) {
	candidates := testCandidates()

	native := Search(candidates, Query{
		Query:     "read file",
		Namespace: "fs",
		Risk:      "readonly",
		Limit:     10,
	})
	if len(native.Items) != 1 || native.Items[0].Name != "fs_read_file" ||
		native.Items[0].Source != SourceNative || native.Items[0].Namespace != "fs" ||
		native.Items[0].CallTool != "fs_read_file" || native.Items[0].Risk != protocol.RiskReadOnly {
		t.Fatalf("native results = %#v", native.Items)
	}
	if native.Metrics.DiscoveryCalls != 1 || native.Metrics.Returned != 1 || native.Metrics.ScannedNative == 0 || native.Metrics.ScannedMCP == 0 {
		t.Fatalf("native metrics = %#v", native.Metrics)
	}

	mcp := Search(candidates, Query{
		Query:           "query github",
		Server:          "github",
		Namespace:       "github",
		Risk:            "mcp",
		Limit:           10,
		IncludeSchema:   true,
		MaxSchemaTokens: 200,
	})
	if len(mcp.Items) != 1 || mcp.Items[0].Name != "mcp__github__search_repositories" ||
		mcp.Items[0].Source != SourceMCP || mcp.Items[0].Server != "github" ||
		mcp.Items[0].Namespace != "mcp.github" || mcp.Items[0].CallTool != "mcp_call" ||
		mcp.Items[0].CallName != "mcp__github__search_repositories" ||
		len(mcp.Items[0].InputSchema) == 0 || mcp.Items[0].Risk != protocol.RiskExternal {
		t.Fatalf("mcp results = %#v", mcp.Items)
	}
	if mcp.Metrics.ScannedNative != 0 || mcp.Metrics.ScannedMCP != 1 ||
		mcp.Metrics.SchemaIncluded != 1 || mcp.Metrics.SchemaBudgetTokens != 200 ||
		mcp.Metrics.Filters.Server != "github" || mcp.Metrics.Filters.Namespace != "github" ||
		mcp.Metrics.Filters.Risk != "external" {
		t.Fatalf("mcp metrics = %#v", mcp.Metrics)
	}
}

func TestSearchHandlesAliasesLimitsAndSchemaBudget(t *testing.T) {
	candidates := testCandidates()

	alias := Search(candidates, Query{Query: "парилка", Limit: 10})
	if len(alias.Items) != 1 || alias.Items[0].Name != "mcp__telegram_parilka__read_history" ||
		alias.Items[0].Server != "telegram-parilka" ||
		alias.Metrics.Filters.Server != "telegram-parilka" ||
		alias.Metrics.Filters.Query != "" {
		t.Fatalf("parilka alias results=%#v metrics=%#v", alias.Items, alias.Metrics)
	}

	limited := Search(candidates, Query{Limit: 1})
	if len(limited.Items) != 1 || !limited.Truncated || limited.Metrics.Matched <= limited.Metrics.Returned {
		t.Fatalf("limited results=%#v metrics=%#v truncated=%v", limited.Items, limited.Metrics, limited.Truncated)
	}

	overBudget := Search(candidates, Query{
		Server:          "github",
		IncludeSchema:   true,
		MaxSchemaTokens: 1,
		Limit:           10,
	})
	if len(overBudget.Items) != 1 || overBudget.Items[0].InputSchema != nil ||
		overBudget.Items[0].SchemaOmittedReason == "" || !overBudget.Truncated ||
		!overBudget.Metrics.SchemaTruncated || overBudget.Metrics.SchemaOmitted != 1 {
		t.Fatalf("over-budget results=%#v metrics=%#v truncated=%v", overBudget.Items, overBudget.Metrics, overBudget.Truncated)
	}
}

func testCandidates() []Candidate {
	return []Candidate{
		{
			Spec: protocol.ToolSpec{
				Name:        "fs_read_file",
				Description: "Read a file from the workspace.",
				Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
				Risk:        protocol.RiskReadOnly,
			},
			Source:    SourceNative,
			Namespace: NativeNamespace("fs_read_file"),
			CallTool:  "fs_read_file",
		},
		{
			Spec: protocol.ToolSpec{
				Name:        "web_fetch",
				Description: "Fetch a public web page.",
				Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
				Risk:        protocol.RiskNetwork,
			},
			Source:    SourceNative,
			Namespace: NativeNamespace("web_fetch"),
			CallTool:  "web_fetch",
		},
		{
			Spec: protocol.ToolSpec{
				Name:        "mcp__github__search_repositories",
				Description: "Search GitHub repositories by query.",
				Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
				Risk:        protocol.RiskExternal,
			},
			Source:    SourceMCP,
			Namespace: MCPNamespace("github"),
			Server:    "github",
			CallTool:  "mcp_call",
			CallName:  "mcp__github__search_repositories",
		},
		{
			Spec: protocol.ToolSpec{
				Name:        "mcp__telegram_parilka__read_history",
				Description: "Read messages from the Parilka Telegram cache.",
				Parameters:  raw(`{"type":"object","properties":{"limit":{"type":"integer"}}}`),
				Risk:        protocol.RiskExternal,
			},
			Source:    SourceMCP,
			Namespace: MCPNamespace("telegram_parilka"),
			Server:    "telegram_parilka",
			CallTool:  "mcp_call",
			CallName:  "mcp__telegram_parilka__read_history",
		},
	}
}

func raw(s string) []byte {
	return []byte(s)
}
