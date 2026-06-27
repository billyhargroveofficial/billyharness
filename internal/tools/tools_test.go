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
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestParseSearchResultsUnwrapsDuckDuckGoRedirects(t *testing.T) {
	body := `
		<a rel="nofollow" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fdoc&amp;rut=abc">Example &amp; Docs</a>
		<a href="https://direct.example/path">Direct</a>
		<a href="https://duckduckgo.com/about">Skip engine link</a>
	`
	results := parseSearchResults("https://lite.duckduckgo.com/lite/?q=x", body, 10)
	if len(results) != 2 {
		t.Fatalf("results len = %d: %#v", len(results), results)
	}
	if results[0].Title != "Example & Docs" || results[0].URL != "https://example.com/doc" {
		t.Fatalf("first result = %#v", results[0])
	}
	if results[1].URL != "https://direct.example/path" {
		t.Fatalf("second result = %#v", results[1])
	}
}

func TestWriteToolCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(root, "out.txt"), "content": "hello"}),
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file should not exist, stat err = %v", statErr)
	}
}

func TestToolArgumentsValidatedAgainstSchema(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	for _, tc := range []struct {
		name string
		call protocol.ToolCall
		want string
	}{
		{
			name: "missing required",
			call: protocol.ToolCall{Name: "fs_read_file", Arguments: rawArgs(map[string]any{})},
			want: `missing required property "path"`,
		},
		{
			name: "wrong type",
			call: protocol.ToolCall{Name: "fs_list", Arguments: rawArgs(map[string]any{"path": ".", "limit": "ten"})},
			want: "$.limit must be integer",
		},
		{
			name: "extra property",
			call: protocol.ToolCall{Name: "time_now", Arguments: rawArgs(map[string]any{"unused": true})},
			want: `unknown property "unused"`,
		},
		{
			name: "array min items",
			call: protocol.ToolCall{Name: "shell_exec", Arguments: rawArgs(map[string]any{"argv": []string{}})},
			want: "$.argv must contain at least 1 items",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := registry.Call(context.Background(), tc.call)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !result.IsError || result.ErrorCode != "validation_error" || !strings.Contains(result.Content, tc.want) {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestDangerousToolsCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)
	for _, call := range []protocol.ToolCall{
		{Name: "fs_write_file", Arguments: rawArgs(map[string]any{"path": "x.txt", "content": "x"})},
		{Name: "fs_make_dir", Arguments: rawArgs(map[string]any{"path": "dir"})},
		{Name: "shell_exec", Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "true"}})},
	} {
		if _, err := registry.Call(context.Background(), call); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("%s expected disabled error, got %v", call.Name, err)
		}
	}
}

func TestWriteToolEnabledByDefault(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	target := filepath.Join(root, "default-on.txt")
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": target, "content": "enabled"}),
	}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "enabled" {
		t.Fatalf("content = %q", bytes)
	}
}

func TestWriteToolEnabledCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	target := filepath.Join(root, "nested", "out.txt")
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": target, "content": "hello"}),
	}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello" {
		t.Fatalf("content = %q", bytes)
	}
}

func TestFSMakeDirEnabledAndRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_make_dir",
		Arguments: rawArgs(map[string]any{"path": "nested/dir"}),
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "nested", "dir")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%v err=%v", info, err)
	}
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_make_dir",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(t.TempDir(), "outside")}),
	}); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func TestReadToolRejectsSensitivePath(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(root, ".env")}),
	})
	if err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("expected sensitive path error, got %v", err)
	}
}

func TestReadToolRejectsPathOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": outside}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func TestReadToolReturnsFullContentForAgentManagedOutput(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("full-content-", 200)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolOutputBytes = 64
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": "big.txt"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != content {
		t.Fatalf("read content len=%d, want full len=%d", len(result.Content), len(content))
	}
}

func TestSafePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(link, "outside.txt")}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape read error, got %v", err)
	}
	_, err = registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(link, "new.txt"), "content": "escape"}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape write error, got %v", err)
	}
}

func TestRelativePathUsesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("from workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": "note.txt"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "from workspace" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestFSListLimitAndSearch(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("needle secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_list",
		Arguments: rawArgs(map[string]any{"path": ".", "limit": 2}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "...[truncated at 2]") {
		t.Fatalf("list output = %q", list.Content)
	}

	search, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_search",
		Arguments: rawArgs(map[string]any{"query": "NEEDLE", "path": ".", "limit": 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search.Content, "a.txt") || strings.Contains(search.Content, ".env") {
		t.Fatalf("search output = %q", search.Content)
	}
}

func TestShellExecGateAndWorkspaceCWD(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "pwd"}}),
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled shell error, got %v", err)
	}

	cfg.AutoApproveDangerous = true
	registry = NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "pwd"}, "cwd": ".", "max_output_bytes": 4096}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.TrimSpace(result.Content), root) {
		t.Fatalf("pwd output = %q, want workspace root %q", result.Content, root)
	}
}

func TestWriteRejectsOversizedContent(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": "big.txt", "content": strings.Repeat("x", maxWriteBytes+1)}),
	})
	if err == nil || !strings.Contains(err.Error(), "content too large") {
		t.Fatalf("expected content too large, got %v", err)
	}
}

func TestValidatePublicHTTPURLRejectsLocalAndPrivateTargets(t *testing.T) {
	for _, rawURL := range []string{
		"http://localhost:8080",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"file:///etc/passwd",
	} {
		if _, err := validatePublicHTTPURL(context.Background(), rawURL); err == nil {
			t.Fatalf("validatePublicHTTPURL(%q) returned nil error", rawURL)
		}
	}
}

func TestCompactFetchedPageLimitsDefaultOutput(t *testing.T) {
	text := strings.Repeat("Alpha beta gamma. ", 1000)
	page := fetchedPage{
		URL:         "https://example.com",
		Status:      200,
		ContentType: "text/plain",
		Title:       "Example",
		Text:        text,
		Links:       []string{"https://example.com/a", "https://example.com/b"},
	}
	out := compactFetchedPage(page, webFetchOptions{MaxChars: 1200, MaxLinks: 1})
	if out.OriginalTextChars <= 1200 {
		t.Fatalf("expected large original text, got %d", out.OriginalTextChars)
	}
	if !out.OutputTextTruncated || len([]rune(out.Text)) > 1400 {
		t.Fatalf("text was not compacted: truncated=%v len=%d", out.OutputTextTruncated, len([]rune(out.Text)))
	}
	if !strings.Contains(out.Summary, "Example") || len(out.Links) != 1 {
		t.Fatalf("compact page = %#v", out)
	}
}

func TestCompactFetchedPageHonorsTokenBudgetEvenForFullText(t *testing.T) {
	text := strings.Repeat("Alpha beta gamma. ", 2000)
	page := fetchedPage{
		URL:         "https://example.com",
		Status:      200,
		ContentType: "text/plain",
		Title:       "Example",
		Text:        text,
	}
	out := compactFetchedPage(page, webFetchOptions{FullText: true, MaxTokens: 200})
	if out.BudgetTextTokens != 200 || out.BudgetTextChars != 800 {
		t.Fatalf("budget = %d tokens / %d chars, want 200 / 800", out.BudgetTextTokens, out.BudgetTextChars)
	}
	if !out.OutputTextTruncated || !strings.Contains(out.CompactNote, "full_text") {
		t.Fatalf("full_text should still be capped: %#v", out)
	}
	if out.ReturnedTextChars > 1000 || out.EstimatedTextTokens > 260 {
		t.Fatalf("returned too much text: chars=%d tokens=%d", out.ReturnedTextChars, out.EstimatedTextTokens)
	}
}

func TestCompactCrawlPagesHonorsTotalTokenBudget(t *testing.T) {
	pages := []crawlPage{
		{URL: "https://example.com/a", Depth: 0, Title: "A", Text: strings.Repeat("A page sentence. ", 800)},
		{URL: "https://example.com/b", Depth: 1, Title: "B", Text: strings.Repeat("B page sentence. ", 800)},
		{URL: "https://example.com/c", Depth: 1, Title: "C", Text: strings.Repeat("C page sentence. ", 800)},
	}
	out := compactCrawlPages(pages, webFetchOptions{MaxTokens: 2000, MaxTotalTokens: 900})
	if len(out) != len(pages) {
		t.Fatalf("pages = %d, want %d", len(out), len(pages))
	}
	totalBudgetChars := 0
	for _, page := range out {
		totalBudgetChars += page.BudgetTextChars
		if page.BudgetTextChars != 1200 {
			t.Fatalf("per-page budget = %d, want 1200: %#v", page.BudgetTextChars, page)
		}
		if !page.OutputTextTruncated || page.EstimatedTextTokens > 360 {
			t.Fatalf("page was not compacted enough: %#v", page)
		}
	}
	if totalBudgetChars != 3600 {
		t.Fatalf("total budget chars = %d, want 3600", totalBudgetChars)
	}
}

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
	for _, want := range []string{
		`"name": "fs_read_file"`,
		`"source": "native"`,
		`"call_tool": "fs_read_file"`,
	} {
		if !strings.Contains(native.Content, want) {
			t.Fatalf("native tool_search missing %q in:\n%s", want, native.Content)
		}
	}

	mcp, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":          "repositories",
			"server":         "github",
			"include_schema": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"name": "mcp__github__search_repositories"`,
		`"source": "mcp"`,
		`"server": "github"`,
		`"call_tool": "mcp_call"`,
		`"call_name": "mcp__github__search_repositories"`,
		`"input_schema"`,
	} {
		if !strings.Contains(mcp.Content, want) {
			t.Fatalf("mcp tool_search missing %q in:\n%s", want, mcp.Content)
		}
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
		`"tool_count": 1`,
		`"mcp__fake__echo"`,
		`"input_schema"`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing %q in:\n%s", want, list.Content)
		}
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

func TestToolsFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_TOOLS_MCP_HELPER") != "1" {
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
				"instructions":    "Use echo for MCP gateway tests.",
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
			if call.Name != "echo" {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				continue
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

func hasSpec(specs []protocol.ToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func rawArgs(value any) json.RawMessage {
	bytes, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return bytes
}
