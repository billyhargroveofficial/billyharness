package runstate

import (
	"encoding/json"
	"strings"
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
		snapshot.AccessMode != config.AccessModeBuild ||
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
		"access_mode",
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

func TestSnapshotInstructionHashIncludesProtectedUserContext(t *testing.T) {
	cfg := config.Config{Model: "mock", Provider: "mock", Profile: "billy"}
	base := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "# Project context\n<PROJECT_CONTEXT>\ncwd: /repo\n</PROJECT_CONTEXT>"},
		{Role: protocol.RoleUser, Content: "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nold rules\n</INSTRUCTIONS>"},
		{Role: protocol.RoleUser, Content: "prompt"},
	}
	contextChanged := append([]protocol.Message(nil), base...)
	contextChanged[1].Content = "# Project context updated\n<PROJECT_CONTEXT>\ncwd: /repo\ncap_flags: rendered_capped\n</PROJECT_CONTEXT>"
	instructionsChanged := append([]protocol.Message(nil), base...)
	instructionsChanged[2].Content = "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nnew rules\n</INSTRUCTIONS>"

	original := NewSnapshot(snapshotInput(cfg), base, nil)
	if original.ProfileInstructionHash == "" {
		t.Fatal("missing instruction hash")
	}
	if got := NewSnapshot(snapshotInput(cfg), contextChanged, nil); got.ProfileInstructionHash == original.ProfileInstructionHash {
		t.Fatalf("project context change did not affect instruction hash")
	}
	if got := NewSnapshot(snapshotInput(cfg), instructionsChanged, nil); got.ProfileInstructionHash == original.ProfileInstructionHash {
		t.Fatalf("AGENTS change did not affect instruction hash")
	}
}

func TestPromptInventoryIsStableAndOmitsArbitraryUserText(t *testing.T) {
	cfg := config.Config{Model: "mock", Provider: "mock", Profile: "billy"}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "# Project context\n<PROJECT_CONTEXT>\nenv_vars: API_TOKEN\n</PROJECT_CONTEXT>"},
		{Role: protocol.RoleUser, Content: "ordinary prompt with fake secret sk-test-123"},
	}
	specsA := []protocol.ToolSpec{
		{Name: "z", Description: "last", Parameters: []byte(`{"z":true}`), Risk: protocol.RiskReadOnly},
		{Name: "a", Description: "first", Parameters: []byte(`{"a":true}`), Risk: protocol.RiskNetwork},
	}
	specsB := []protocol.ToolSpec{
		{Name: "a", Description: "first", Parameters: []byte(`{"a":true}`), Risk: protocol.RiskNetwork},
		{Name: "z", Description: "last", Parameters: []byte(`{"z":true}`), Risk: protocol.RiskReadOnly},
	}

	a := NewSnapshot(snapshotInput(cfg), messages, specsA)
	b := NewSnapshot(snapshotInput(cfg), messages, specsB)
	if a.PromptInventory == nil || b.PromptInventory == nil {
		t.Fatalf("missing prompt inventory: %#v %#v", a.PromptInventory, b.PromptInventory)
	}
	if a.PromptInventory.Hash != b.PromptInventory.Hash {
		t.Fatalf("inventory hashes differ: %s != %s", a.PromptInventory.Hash, b.PromptInventory.Hash)
	}
	if !hasPromptSection(a.PromptInventory, "system_prompt") ||
		!hasPromptSection(a.PromptInventory, "project_context") ||
		!hasPromptSection(a.PromptInventory, "tool_schemas") {
		t.Fatalf("inventory missing expected sections: %#v", a.PromptInventory.Sections)
	}
	if hasPromptSection(a.PromptInventory, "user_prompt") {
		t.Fatalf("ordinary user prompt should not be inventoried: %#v", a.PromptInventory.Sections)
	}
	body, err := json.Marshal(a.PromptInventory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sk-test-123") || strings.Contains(string(body), "ordinary prompt") {
		t.Fatalf("inventory leaked arbitrary prompt text: %s", body)
	}
}

func TestPromptCacheBreakReasonsForModelToolAndContextChanges(t *testing.T) {
	cfg := config.Config{Model: "mock", Provider: "mock", Profile: "billy"}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "# Project context\n<PROJECT_CONTEXT>\ncwd: /repo\n</PROJECT_CONTEXT>"},
		{Role: protocol.RoleUser, Content: "prompt"},
	}
	specs := []protocol.ToolSpec{{Name: "fs_read_file", Parameters: []byte(`{"type":"object"}`), Risk: protocol.RiskReadOnly}}
	base := NewSnapshot(snapshotInput(cfg), messages, specs)

	initial := base.WithPromptCacheBreak(nil)
	if initial.PromptCacheBreak == nil || initial.PromptCacheBreak.Status != "initial" || initial.PromptCacheBreak.Reason != "initial_request" {
		t.Fatalf("initial cache diagnostic = %#v", initial.PromptCacheBreak)
	}
	unchanged := NewSnapshot(snapshotInput(cfg), messages, specs).WithPromptCacheBreak(&base)
	if unchanged.PromptCacheBreak == nil || unchanged.PromptCacheBreak.Status != "unchanged" {
		t.Fatalf("unchanged cache diagnostic = %#v", unchanged.PromptCacheBreak)
	}

	modelCfg := cfg
	modelCfg.Model = "mock-large"
	modelChanged := NewSnapshot(snapshotInput(modelCfg), messages, specs).WithPromptCacheBreak(&base)
	if !cacheBreakContains(modelChanged.PromptCacheBreak, "model_changed") {
		t.Fatalf("model cache diagnostic = %#v", modelChanged.PromptCacheBreak)
	}

	toolChanged := NewSnapshot(snapshotInput(cfg), messages, []protocol.ToolSpec{{
		Name:       "fs_read_file",
		Parameters: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		Risk:       protocol.RiskReadOnly,
	}}).WithPromptCacheBreak(&base)
	if !cacheBreakContains(toolChanged.PromptCacheBreak, "tool_schema_changed") {
		t.Fatalf("tool cache diagnostic = %#v", toolChanged.PromptCacheBreak)
	}

	contextMessages := append([]protocol.Message(nil), messages...)
	contextMessages[1].Content = "# Project context updated\n<PROJECT_CONTEXT>\ncwd: /repo\ncap_flags: rendered_capped\n</PROJECT_CONTEXT>"
	contextChanged := NewSnapshot(snapshotInput(cfg), contextMessages, specs).WithPromptCacheBreak(&base)
	if !cacheBreakContains(contextChanged.PromptCacheBreak, "prompt_section:project_context") {
		t.Fatalf("context cache diagnostic = %#v", contextChanged.PromptCacheBreak)
	}
}

func hasPromptSection(inventory *protocol.PromptInventory, name string) bool {
	if inventory == nil {
		return false
	}
	for _, section := range inventory.Sections {
		if section.Name == name {
			return true
		}
	}
	return false
}

func cacheBreakContains(breakInfo *protocol.PromptCacheBreak, want string) bool {
	if breakInfo == nil {
		return false
	}
	for _, field := range breakInfo.ChangedFields {
		if field == want {
			return true
		}
	}
	return strings.Contains(breakInfo.Reason, want)
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
