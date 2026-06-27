package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestLazyMCPGatewayHidesRawSpecsAndCanCallTool(t *testing.T) {
	registry := NewRegistry(config.Default())
	registry.mcpTools["mcp__fake__echo"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__fake__echo",
			Description: "MCP fake/echo. Echo text",
			Parameters:  raw(`{"type":"object","properties":{"text":{"type":"string"}}}`),
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
	if !hasSpec(registry.Specs(), "mcp_list_tools") || !hasSpec(registry.Specs(), "mcp_call") {
		t.Fatalf("lazy MCP tools missing: %#v", registry.Specs())
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
