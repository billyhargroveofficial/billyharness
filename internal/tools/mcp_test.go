package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestLazyMCPGatewayHidesRawSpecsAndCanCallTool(t *testing.T) {
	registry := NewRegistry(config.Default())
	registry.mcpTools["mcp__fake__echo"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__fake__echo",
			Description: "MCP fake/echo. Echo text",
			Parameters:  raw(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			return Result{Content: in.Text}, nil
		},
	}
	registry.mcpTools["mcp__telegram_parilka__read_history"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__telegram_parilka__read_history",
			Description: "Read messages from the local SQLite cache.",
			Parameters:  raw(`{"type":"object","properties":{"limit":{"type":"integer"}}}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: "history"}, nil
		},
	}
	registry.addMCPGateway()

	for _, spec := range registry.Specs() {
		if spec.Name == "mcp__fake__echo" {
			t.Fatalf("raw MCP tool leaked into model specs: %#v", registry.Specs())
		}
	}
	if !hasSpec(registry.Specs(), "mcp_list_tools") || !hasSpec(registry.Specs(), "mcp_call") || !hasSpec(registry.Specs(), "tool_search") {
		t.Fatalf("lazy MCP tools missing: %#v", registry.Specs())
	}
	if !specDescriptionContains(registry.Specs(), "tool_search", "static model-visible gateway tools") ||
		!specDescriptionContains(registry.Specs(), "mcp_list_tools", "not direct model-visible specs") ||
		!specDescriptionContains(registry.Specs(), "mcp_call", "dynamic MCP catalog tool") {
		t.Fatalf("MCP gateway tool descriptions should name static gateway specs vs dynamic MCP catalog: %#v", registry.Specs())
	}

	direct, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp__fake__echo",
		Arguments: rawArgs(map[string]any{"text": "bypass"}),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("raw MCP tool should not be directly callable, got result=%#v err=%v", direct, err)
	}
	if direct.ErrorCode != "unknown_tool" {
		t.Fatalf("direct raw MCP call result = %#v", direct)
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"query": "echo"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "mcp__fake__echo") || strings.Contains(list.Content, "input_schema") {
		t.Fatalf("unexpected list output: %s", list.Content)
	}
	for _, want := range []string{
		`"model_visible_tools"`,
		`"kind": "static_gateway_tools"`,
		`"includes_dynamic_mcp_tools": false`,
		`"mcp_catalog"`,
		`"kind": "dynamic_mcp_catalog"`,
		`"model_visible": false`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing catalog clarity field %q:\n%s", want, list.Content)
		}
	}
	if list.Metadata["model_visible_tool_catalog_kind"] != "static_gateway_tools" ||
		list.Metadata["model_visible_includes_dynamic_mcp_tools"] != false ||
		list.Metadata["mcp_catalog_kind"] != "dynamic_mcp_catalog" ||
		list.Metadata["mcp_catalog_model_visible"] != false {
		t.Fatalf("mcp_list_tools metadata missing catalog clarity: %#v", list.Metadata)
	}

	parilkaByServer, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "telegram-parilka"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parilkaByServer.Content, "mcp__telegram_parilka__read_history") ||
		!strings.Contains(parilkaByServer.Content, `"server": "telegram-parilka"`) {
		t.Fatalf("telegram-parilka filter failed: %s", parilkaByServer.Content)
	}

	parilkaByRussianAlias, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"query": "парилка"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parilkaByRussianAlias.Content, "mcp__telegram_parilka__read_history") {
		t.Fatalf("russian parilka alias failed: %s", parilkaByRussianAlias.Content)
	}

	called, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "ok"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if called.Content != "ok" {
		t.Fatalf("mcp_call result = %q", called.Content)
	}

	rejected, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "ok", "extra": true},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), `unknown property "extra"`) {
		t.Fatalf("expected target schema validation error, got result=%#v err=%v", rejected, err)
	}
	if !rejected.IsError || rejected.ErrorCode != "validation_error" {
		t.Fatalf("expected validation error result, got %#v", rejected)
	}

	nullArgs, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__telegram_parilka__read_history",
			"arguments": nil,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if nullArgs.Content != "history" {
		t.Fatalf("null mcp_call arguments result = %q", nullArgs.Content)
	}
}

func TestToolSearchFindsNativeAndMCPTools(t *testing.T) {
	registry := NewRegistry(config.Default())
	registry.mcpTools["mcp__github__search_repositories"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__github__search_repositories",
			Description: "Search GitHub repositories by query.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: "ok"}, nil
		},
	}
	registry.addMCPGateway()

	native, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "tool_search",
		Arguments: rawArgs(map[string]any{"query": "read file", "limit": 5}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var nativeResp struct {
		Tools []struct {
			Name      string `json:"name"`
			Source    string `json:"source"`
			Namespace string `json:"namespace"`
			Risk      string `json:"risk"`
			CallTool  string `json:"call_tool"`
		} `json:"tools"`
		Metrics struct {
			DiscoveryCalls int `json:"discovery_calls"`
			Returned       int `json:"returned"`
			ScannedNative  int `json:"scanned_native"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(native.Content), &nativeResp); err != nil {
		t.Fatalf("native tool_search json = %v\n%s", err, native.Content)
	}
	for _, want := range []string{
		`"name": "fs_read_file"`,
		`"source": "native"`,
		`"namespace": "fs"`,
		`"risk": "read_only"`,
		`"call_tool": "fs_read_file"`,
		`"metrics"`,
		`"model_visible_tools"`,
		`"kind": "static_gateway_tools"`,
		`"includes_dynamic_mcp_tools": false`,
		`"mcp_catalog"`,
		`"kind": "dynamic_mcp_catalog"`,
		`"model_visible": false`,
	} {
		if !strings.Contains(native.Content, want) {
			t.Fatalf("native tool_search missing %q in:\n%s", want, native.Content)
		}
	}
	if nativeResp.Metrics.DiscoveryCalls != 1 || nativeResp.Metrics.Returned == 0 || nativeResp.Metrics.ScannedNative == 0 {
		t.Fatalf("native metrics = %#v", nativeResp.Metrics)
	}
	if native.Metadata["model_visible_tool_catalog_kind"] != "static_gateway_tools" ||
		native.Metadata["model_visible_includes_dynamic_mcp_tools"] != false ||
		native.Metadata["mcp_catalog_kind"] != "dynamic_mcp_catalog" ||
		native.Metadata["mcp_catalog_model_visible"] != false {
		t.Fatalf("native tool_search metadata missing catalog clarity: %#v", native.Metadata)
	}

	filteredNative, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":     "file",
			"namespace": "fs",
			"risk":      "read_only",
			"limit":     10,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filteredNative.Content, `"name": "fs_read_file"`) ||
		strings.Contains(filteredNative.Content, `"name": "web_fetch"`) {
		t.Fatalf("native namespace/risk filter failed:\n%s", filteredNative.Content)
	}

	mcp, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":             "repositories",
			"server":            "github",
			"namespace":         "mcp.github",
			"risk":              "external",
			"include_schema":    true,
			"max_schema_tokens": 200,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var mcpResp struct {
		Tools []struct {
			Name        string          `json:"name"`
			Source      string          `json:"source"`
			Namespace   string          `json:"namespace"`
			Server      string          `json:"server"`
			Risk        string          `json:"risk"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		Metrics struct {
			DiscoveryCalls     int `json:"discovery_calls"`
			Returned           int `json:"returned"`
			SchemaIncluded     int `json:"schema_included"`
			SchemaTokens       int `json:"schema_tokens"`
			SchemaBudgetTokens int `json:"schema_budget_tokens"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(mcp.Content), &mcpResp); err != nil {
		t.Fatalf("mcp tool_search json = %v\n%s", err, mcp.Content)
	}
	for _, want := range []string{
		`"name": "mcp__github__search_repositories"`,
		`"source": "mcp"`,
		`"namespace": "mcp.github"`,
		`"server": "github"`,
		`"risk": "external"`,
		`"call_tool": "mcp_call"`,
		`"call_name": "mcp__github__search_repositories"`,
		`"input_schema"`,
		`"model_visible_tools"`,
		`"kind": "static_gateway_tools"`,
		`"includes_dynamic_mcp_tools": false`,
		`"mcp_catalog"`,
		`"kind": "dynamic_mcp_catalog"`,
		`"model_visible": false`,
	} {
		if !strings.Contains(mcp.Content, want) {
			t.Fatalf("mcp tool_search missing %q in:\n%s", want, mcp.Content)
		}
	}
	if mcpResp.Metrics.DiscoveryCalls != 1 || mcpResp.Metrics.Returned != 1 ||
		mcpResp.Metrics.SchemaIncluded != 1 || mcpResp.Metrics.SchemaTokens == 0 ||
		mcpResp.Metrics.SchemaBudgetTokens != 200 {
		t.Fatalf("mcp metrics = %#v", mcpResp.Metrics)
	}
	if mcp.Metadata["model_visible_tool_catalog_kind"] != "static_gateway_tools" ||
		mcp.Metadata["model_visible_includes_dynamic_mcp_tools"] != false ||
		mcp.Metadata["mcp_catalog_kind"] != "dynamic_mcp_catalog" ||
		mcp.Metadata["mcp_catalog_model_visible"] != false {
		t.Fatalf("mcp tool_search metadata missing catalog clarity: %#v", mcp.Metadata)
	}

	overBudget, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":             "repositories",
			"server":            "github",
			"include_schema":    true,
			"max_schema_tokens": 1,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overBudget.Content, `"schema_omitted"`) ||
		!strings.Contains(overBudget.Content, `"schema_truncated": true`) ||
		!strings.Contains(overBudget.Content, `"truncated": true`) {
		t.Fatalf("schema budget omission missing:\n%s", overBudget.Content)
	}
}

func TestToolSnapshotFreezesMCPGatewayCatalog(t *testing.T) {
	registry := NewRegistry(config.Default())
	registry.mcpTools["mcp__fake__old"] = fakeMCPTool("mcp__fake__old", "old")
	registry.mcpCatalog = mcpCatalogState{Kind: "dynamic_mcp_catalog", Version: 1, ToolCount: 1}
	now := time.Now().UTC()
	registry.mcpStatuses = []mcpclient.ServerStatus{{
		Name:        "fake",
		Transport:   "stdio",
		Enabled:     true,
		Connected:   true,
		State:       "connected",
		ToolCount:   1,
		LastEventAt: &now,
	}}
	registry.addMCPGateway()

	snapshot := registry.Snapshot(context.Background())

	later := now.Add(time.Second)
	registry.mcpMu.Lock()
	registry.mcpTools = map[string]Tool{"mcp__fake__new": fakeMCPTool("mcp__fake__new", "new")}
	registry.mcpCatalog = mcpCatalogState{Kind: "dynamic_mcp_catalog", Version: 2, ToolCount: 1}
	registry.mcpStatuses = []mcpclient.ServerStatus{{
		Name:        "fake",
		Transport:   "stdio",
		Enabled:     true,
		Connected:   true,
		State:       "connected",
		ToolCount:   1,
		LastEventAt: &later,
	}}
	registry.mcpMu.Unlock()

	list, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"query": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "mcp__fake__old") || strings.Contains(list.Content, "mcp__fake__new") {
		t.Fatalf("snapshot list should keep old MCP catalog only:\n%s", list.Content)
	}

	oldCall, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_call",
		Arguments: rawArgs(map[string]any{"name": "mcp__fake__old"}),
	})
	if err != nil || oldCall.Content != "old" {
		t.Fatalf("snapshot old MCP call = %#v err=%v", oldCall, err)
	}
	newCall, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_call",
		Arguments: rawArgs(map[string]any{"name": "mcp__fake__new"}),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown MCP tool") {
		t.Fatalf("snapshot new MCP call should fail as unknown, result=%#v err=%v", newCall, err)
	}

	liveList, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"query": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(liveList.Content, "mcp__fake__new") || strings.Contains(liveList.Content, "mcp__fake__old") {
		t.Fatalf("live registry should see updated MCP catalog:\n%s", liveList.Content)
	}
	if snapshot.MCPStatusSnapshotHash() == "" {
		t.Fatal("snapshot MCP hash is empty")
	}
	if snapshot.MCPStatusSnapshotHash() == registry.Snapshot(context.Background()).MCPStatusSnapshotHash() {
		t.Fatal("snapshot MCP hash did not change after live catalog mutation")
	}
}

func fakeMCPTool(name, content string) Tool {
	return Tool{
		Spec: protocol.ToolSpec{
			Name:        name,
			Description: "fake MCP tool",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: content}, nil
		},
	}
}

func TestMCPGatewayListsServerStatusesAndValidatesStdioCalls(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1"},
		CWD:            root,
		Enabled:        true,
		Required:       true,
		StartupTimeout: 5 * time.Second,
		ToolTimeout:    5 * time.Second,
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_list_tools",
		Arguments: rawArgs(map[string]any{
			"server":         "fake",
			"include_schema": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"servers"`,
		`"name": "fake"`,
		`"connected": true`,
		`"state": "connected"`,
		`"tool_count": 1`,
		`"mcp__fake__echo"`,
		`"namespace": "mcp.fake"`,
		`"risk": "external"`,
		`"input_schema"`,
		`"metrics"`,
		`"schema_included": 1`,
		`"model_visible_tools"`,
		`"kind": "static_gateway_tools"`,
		`"includes_dynamic_mcp_tools": false`,
		`"mcp_catalog"`,
		`"kind": "dynamic_mcp_catalog"`,
		`"model_visible": false`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing %q in:\n%s", want, list.Content)
		}
	}
	if list.Metadata["model_visible_tool_catalog_kind"] != "static_gateway_tools" ||
		list.Metadata["model_visible_includes_dynamic_mcp_tools"] != false ||
		list.Metadata["mcp_catalog_kind"] != "dynamic_mcp_catalog" ||
		list.Metadata["mcp_catalog_model_visible"] != false {
		t.Fatalf("mcp_list_tools metadata missing catalog clarity: %#v", list.Metadata)
	}

	invalid, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"extra": "nope"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), `missing required property "text"`) {
		t.Fatalf("expected target schema validation error, got result=%#v err=%v", invalid, err)
	}
	if !invalid.IsError || invalid.ErrorCode != "validation_error" {
		t.Fatalf("expected validation error result, got %#v", invalid)
	}

	valid, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "hello"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if valid.Content != "hello" {
		t.Fatalf("mcp_call content = %q", valid.Content)
	}
}

func TestMCPGatewayReconnectsCrashedStdioServer(t *testing.T) {
	root := t.TempDir()
	phaseFile := filepath.Join(root, "tools-reconnect.phase")
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1", "BILLYHARNESS_TOOLS_MCP_MODE": "close_once_then_echo", "BILLYHARNESS_TOOLS_MCP_PHASE_FILE": phaseFile},
		CWD:            root,
		Enabled:        true,
		Required:       true,
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	first, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "first"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("first mcp_call result=%#v err=%v", first, err)
	}

	second, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "second"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "second" {
		t.Fatalf("second mcp_call content = %q", second.Content)
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"state": "reconnected"`,
		`"retry_count": 1`,
		`"restart_count": 1`,
		`"connected": true`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing %q in:\n%s", want, list.Content)
		}
	}
}

func TestMCPGatewayRefreshesCatalogAfterReconnect(t *testing.T) {
	root := t.TempDir()
	phaseFile := filepath.Join(root, "tools-catalog-reconnect.phase")
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1", "BILLYHARNESS_TOOLS_MCP_MODE": "close_once_then_new_tool", "BILLYHARNESS_TOOLS_MCP_PHASE_FILE": phaseFile},
		CWD:            root,
		Enabled:        true,
		Required:       true,
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		EnabledTools:   []string{"echo", "new_echo"},
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	initial, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(initial.Content, `"name": "mcp__fake__echo"`) || strings.Contains(initial.Content, `"name": "mcp__fake__new_echo"`) {
		t.Fatalf("initial catalog = %s", initial.Content)
	}

	crashed, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "first"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("expected transport crash, got result=%#v err=%v", crashed, err)
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, `"name": "mcp__fake__new_echo"`) ||
		strings.Contains(list.Content, `"name": "mcp__fake__echo"`) {
		t.Fatalf("reconnected list did not reflect new catalog:\n%s", list.Content)
	}

	search, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"server": "fake",
			"query":  "new echo",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search.Content, `"name": "mcp__fake__new_echo"`) ||
		strings.Contains(search.Content, `"name": "mcp__fake__echo"`) {
		t.Fatalf("tool_search did not reflect new catalog:\n%s", search.Content)
	}

	newCall, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__new_echo",
			"arguments": map[string]any{"text": "second"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if newCall.Content != "new: second" {
		t.Fatalf("new mcp_call content = %q", newCall.Content)
	}

	oldCall, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "old"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown MCP tool mcp__fake__echo") {
		t.Fatalf("old mcp_call should fail validation after catalog refresh, got result=%#v err=%v", oldCall, err)
	}
}

func TestRegistrySubscribesToMCPCatalogChanges(t *testing.T) {
	root := t.TempDir()
	phaseFile := filepath.Join(root, "tools-catalog-listener.phase")
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1", "BILLYHARNESS_TOOLS_MCP_MODE": "close_once_then_new_tool", "BILLYHARNESS_TOOLS_MCP_PHASE_FILE": phaseFile},
		CWD:            root,
		Enabled:        true,
		Required:       true,
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		EnabledTools:   []string{"echo", "new_echo"},
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	initialCatalog := registry.mcpCatalogSnapshot()
	if initialCatalog.Version == 0 || initialCatalog.Stale || initialCatalog.ToolCount != 1 {
		t.Fatalf("initial catalog = %#v", initialCatalog)
	}
	echo, ok := registry.mcpToolsSnapshot()["mcp__fake__echo"]
	if !ok {
		t.Fatal("initial echo tool missing")
	}

	crashed, err := echo.Handler(context.Background(), json.RawMessage(`{"text":"first"}`))
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("expected fake MCP transport crash, got result=%#v err=%v", crashed, err)
	}
	registry.manager.Refresh(context.Background())

	results := registry.searchTools("new echo", "fake", "", "", 10, false, 0)
	if len(results.Items) != 1 || results.Items[0].Name != "mcp__fake__new_echo" {
		t.Fatalf("registry search did not observe subscribed catalog change: %#v", results.Items)
	}
	if _, oldOK := registry.mcpToolsSnapshot()["mcp__fake__echo"]; oldOK {
		t.Fatal("old MCP tool remained in registry mirror after catalog listener sync")
	}
	catalog := registry.mcpCatalogSnapshot()
	if catalog.Stale || catalog.Version <= initialCatalog.Version || catalog.ToolCount != 1 {
		t.Fatalf("catalog after listener sync = %#v, initial=%#v", catalog, initialCatalog)
	}

	search, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"server": "fake",
			"query":  "new echo",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"model_visible_tools"`, `"kind": "static_gateway_tools"`, `"mcp_catalog"`, `"kind": "dynamic_mcp_catalog"`, `"model_visible": false`, `"version":`, `"stale": false`, `"tool_count": 1`} {
		if !strings.Contains(search.Content, want) {
			t.Fatalf("tool_search missing catalog field %q:\n%s", want, search.Content)
		}
	}
	if search.Metadata["mcp_catalog_version"] == nil ||
		search.Metadata["mcp_catalog_tool_count"] != 1 ||
		search.Metadata["mcp_catalog_stale"] != false ||
		search.Metadata["mcp_catalog_kind"] != "dynamic_mcp_catalog" ||
		search.Metadata["mcp_catalog_model_visible"] != false ||
		search.Metadata["model_visible_tool_catalog_kind"] != "static_gateway_tools" ||
		search.Metadata["model_visible_includes_dynamic_mcp_tools"] != false {
		t.Fatalf("tool_search metadata missing catalog state: %#v", search.Metadata)
	}
}

func TestMCPGatewayRefreshesCatalogAfterOptionalStartupFailure(t *testing.T) {
	root := t.TempDir()
	phaseFile := filepath.Join(root, "tools-startup-reconnect.phase")
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1", "BILLYHARNESS_TOOLS_MCP_MODE": "bad_list_once_then_new_tool", "BILLYHARNESS_TOOLS_MCP_PHASE_FILE": phaseFile},
		CWD:            root,
		Enabled:        true,
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		EnabledTools:   []string{"new_echo"},
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if !hasSpec(registry.Specs(), "mcp_list_tools") || !hasSpec(registry.Specs(), "mcp_call") || !hasSpec(registry.Specs(), "tool_search") {
		t.Fatalf("MCP gateway tools should be present after optional startup failure: %#v", registry.Specs())
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"name": "mcp__fake__new_echo"`,
		`"connected": true`,
		`"retry_count": 1`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing %q after reconnect:\n%s", want, list.Content)
		}
	}

	search, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"server": "fake",
			"query":  "new echo",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search.Content, `"name": "mcp__fake__new_echo"`) {
		t.Fatalf("tool_search did not see reconnected catalog:\n%s", search.Content)
	}

	called, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__new_echo",
			"arguments": map[string]any{"text": "later"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if called.Content != "new: later" {
		t.Fatalf("mcp_call content after reconnect = %q", called.Content)
	}
}

func TestToolsFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_TOOLS_MCP_HELPER") != "1" {
		return
	}
	mode := os.Getenv("BILLYHARNESS_TOOLS_MCP_MODE")
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
				"instructions":    "Use echo for MCP gateway tests.",
			}})
		case "tools/list":
			name := "echo"
			description := "Echo text"
			if mode == "bad_list_once_then_new_tool" && !toolsMCPPhaseExists() {
				writeToolsMCPPhase()
				_, _ = os.Stdout.Write([]byte("{not json\n"))
				os.Exit(0)
			}
			if (mode == "close_once_then_new_tool" || mode == "bad_list_once_then_new_tool") && toolsMCPPhaseExists() {
				name = "new_echo"
				description = "New echo text"
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        name,
				"description": description,
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false},
			}}}})
		case "tools/call":
			var call struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &call)
			if call.Name == "new_echo" && (mode == "close_once_then_new_tool" || mode == "bad_list_once_then_new_tool") && toolsMCPPhaseExists() {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "new: " + fmt.Sprint(call.Arguments["text"])}},
					"isError": false,
				}})
				continue
			}
			if call.Name != "echo" {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				continue
			}
			if (mode == "close_once_then_new_tool" || mode == "bad_list_once_then_new_tool") && toolsMCPPhaseExists() {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				continue
			}
			if (mode == "close_once_then_echo" || mode == "close_once_then_new_tool") && !toolsMCPPhaseExists() {
				writeToolsMCPPhase()
				os.Exit(0)
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprint(call.Arguments["text"])}},
				"isError": false,
			}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}

func toolsMCPPhaseExists() bool {
	path := os.Getenv("BILLYHARNESS_TOOLS_MCP_PHASE_FILE")
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeToolsMCPPhase() {
	path := os.Getenv("BILLYHARNESS_TOOLS_MCP_PHASE_FILE")
	if path != "" {
		_ = os.WriteFile(path, []byte("closed"), 0o600)
	}
}
