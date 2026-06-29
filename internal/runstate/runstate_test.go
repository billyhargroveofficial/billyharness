package runstate

import (
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestNewSnapshotCapturesTurnRuntimeState(t *testing.T) {
	cfg := config.Config{
		Provider:             "deepseek",
		Model:                "deepseek-v4-flash",
		Profile:              "billy",
		Thinking:             "enabled",
		ReasoningEffort:      "high",
		ContextWindowTokens:  1_000_000,
		AutoApproveDangerous: true,
		MCPEnabled:           true,
		MCPConfigFiles:       []string{"/root/billyharness/mcp.config.toml"},
		MCPAllowedServers:    []string{"context7", "telegram-parilka"},
		MCPServers: []config.MCPServer{
			{Name: "telegram-parilka", Command: "/root/telegram-parilka-mcp/dist/index.js", Enabled: true},
			{Name: "context7", URL: "https://mcp.example.test", Enabled: true},
		},
	}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleSystem, Content: "profile"},
		{Role: protocol.RoleUser, Content: "hello"},
	}
	specs := []protocol.ToolSpec{
		{Name: "web_fetch", Description: "fetch", Parameters: []byte(`{"type":"object"}`), Risk: protocol.RiskNetwork},
		{Name: "fs_read_file", Description: "read", Parameters: []byte(`{"type":"object"}`), Risk: protocol.RiskReadOnly},
	}

	snapshot := NewSnapshot(snapshotInput(cfg), messages, specs)
	metadata := snapshot.Metadata()
	if snapshot.ProviderID != "deepseek" ||
		snapshot.ModelID != "deepseek-v4-flash" ||
		snapshot.ReasoningMode != "enabled/high" ||
		snapshot.ContextBudgetTokens != 1_000_000 ||
		snapshot.DangerousPermissionMode != "auto_approve_dangerous" ||
		snapshot.ToolSnapshotHash == "" ||
		snapshot.MCPStatusSnapshotHash == "" ||
		snapshot.ProfileInstructionHash == "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, key := range []string{
		"provider_id",
		"model_id",
		"reasoning_mode",
		"context_budget_tokens",
		"tool_snapshot_hash",
		"mcp_status_snapshot_hash",
		"profile_instruction_hash",
		"dangerous_permission_mode",
	} {
		if metadata[key] == nil {
			t.Fatalf("metadata missing %s: %#v", key, metadata)
		}
	}
}

func TestSnapshotHashesAreStableForEquivalentToolAndMCPOrder(t *testing.T) {
	cfgA := config.Config{
		Model:             "deepseek-v4-flash",
		Provider:          "deepseek",
		MCPEnabled:        true,
		MCPConfigFiles:    []string{"/b.toml", "/a.toml"},
		MCPAllowedServers: []string{"telegram", "github"},
		MCPServers: []config.MCPServer{
			{Name: "github", Command: "/usr/bin/github-mcp", Enabled: true, DisabledTools: []string{"b", "a"}},
			{Name: "telegram", Command: "/usr/bin/telegram-mcp", Enabled: true},
		},
	}
	cfgB := cfgA
	cfgB.MCPConfigFiles = []string{"/a.toml", "/b.toml"}
	cfgB.MCPAllowedServers = []string{"github", "telegram"}
	cfgB.MCPServers = []config.MCPServer{
		{Name: "telegram", Command: "/usr/bin/telegram-mcp", Enabled: true},
		{Name: "github", Command: "/usr/bin/github-mcp", Enabled: true, DisabledTools: []string{"a", "b"}},
	}
	specsA := []protocol.ToolSpec{
		{Name: "z", Parameters: []byte(`{"z":true}`), Risk: protocol.RiskReadOnly},
		{Name: "a", Parameters: []byte(`{"a":true}`), Risk: protocol.RiskNetwork},
	}
	specsB := []protocol.ToolSpec{
		{Name: "a", Parameters: []byte(`{"a":true}`), Risk: protocol.RiskNetwork},
		{Name: "z", Parameters: []byte(`{"z":true}`), Risk: protocol.RiskReadOnly},
	}

	a := NewSnapshot(snapshotInput(cfgA), nil, specsA)
	b := NewSnapshot(snapshotInput(cfgB), nil, specsB)
	if a.ToolSnapshotHash != b.ToolSnapshotHash {
		t.Fatalf("tool hashes differ: %s != %s", a.ToolSnapshotHash, b.ToolSnapshotHash)
	}
	if a.MCPStatusSnapshotHash != b.MCPStatusSnapshotHash {
		t.Fatalf("mcp hashes differ: %s != %s", a.MCPStatusSnapshotHash, b.MCPStatusSnapshotHash)
	}
}

func snapshotInput(cfg config.Config) SnapshotInput {
	return SnapshotInput{
		Provider:   cfg.ProviderBinding(),
		Profile:    cfg.ProfileSelection(),
		Runtime:    cfg.RuntimeLimits(),
		ToolPolicy: cfg.ToolPolicySettings(),
		MCP:        cfg.MCPSettings(),
	}
}
