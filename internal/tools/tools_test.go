package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
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

func TestTodoWriteStoresBoundedPlanState(t *testing.T) {
	registry := NewRegistry(config.Default())
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "todo_write",
		Arguments: rawArgs(map[string]any{"todos": []map[string]any{
			{"id": "inspect", "content": "Inspect existing plan code", "status": "completed", "priority": "high"},
			{"id": "build", "content": "Build todo_write state", "status": "in_progress", "priority": "high"},
			{"id": "verify", "content": "Run focused tests", "status": "pending", "priority": "medium"},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plan 3 todos", "1 in progress", "1 pending", "1 completed", "now: Build todo_write state"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("todo_write content missing %q:\n%s", want, result.Content)
		}
	}
	state, ok := result.Metadata["todo_state"].(protocol.TodoState)
	if !ok {
		t.Fatalf("todo_state metadata = %#v", result.Metadata["todo_state"])
	}
	if len(state.Todos) != 3 || state.InProgress != 1 || state.Pending != 1 || state.Completed != 1 {
		t.Fatalf("todo state = %#v", state)
	}
	if got := result.Metadata["display_summary"]; got != "plan 3 todos · 1 in progress · 1 pending · 1 completed · now: Build todo_write state" {
		t.Fatalf("display summary = %#v", got)
	}
}

func TestTodoWriteRejectsInvalidPlanState(t *testing.T) {
	registry := NewRegistry(config.Default())
	for _, tc := range []struct {
		name      string
		todos     []map[string]any
		want      string
		errorCode string
	}{
		{
			name: "two in progress",
			todos: []map[string]any{
				{"id": "one", "content": "one", "status": "in_progress"},
				{"id": "two", "content": "two", "status": "in_progress"},
			},
			want:      "max 1",
			errorCode: "todo_invalid",
		},
		{
			name: "bad status",
			todos: []map[string]any{
				{"id": "one", "content": "one", "status": "started"},
			},
			want:      "must be one of",
			errorCode: "validation_error",
		},
		{
			name: "duplicate id",
			todos: []map[string]any{
				{"id": "one", "content": "one", "status": "pending"},
				{"id": "one", "content": "again", "status": "completed"},
			},
			want:      "duplicate todo id",
			errorCode: "todo_invalid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := registry.Call(context.Background(), protocol.ToolCall{
				Name:      "todo_write",
				Arguments: rawArgs(map[string]any{"todos": tc.todos}),
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !result.IsError || result.ErrorCode != tc.errorCode {
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
		{Name: "web_cache_clear", Arguments: rawArgs(map[string]any{})},
	} {
		if _, err := registry.Call(context.Background(), call); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("%s expected disabled error, got %v", call.Name, err)
		}
	}
}

func TestRegistryPolicyDeniesRiskBeforeHandlerRuns(t *testing.T) {
	cfg := config.Default()
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)
	var called bool
	if err := registry.Register(Tool{
		Spec: protocol.ToolSpec{
			Name:        "dangerous_custom",
			Description: "Dangerous test tool.",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			called = true
			return Result{Content: "ran"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := registry.Call(context.Background(), protocol.ToolCall{Name: "dangerous_custom"})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got result=%#v err=%v", result, err)
	}
	if called {
		t.Fatal("handler ran despite denied policy")
	}
	if !result.IsError || result.ErrorCode != "permission_denied" ||
		result.Metadata["permission_decision"] != "deny" ||
		result.Metadata["permission_source"] != "config" ||
		result.Metadata["permission_reason"] != "dangerous_tools_disabled" ||
		result.Metadata["risk"] != protocol.RiskExecute {
		t.Fatalf("result = %#v", result)
	}
}

func TestPlanModeFiltersAndDeniesWriteExecuteExternalTools(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)
	var externalCalled bool
	if err := registry.Register(Tool{
		Spec: protocol.ToolSpec{
			Name:        "external_custom",
			Description: "External test tool.",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			externalCalled = true
			return Result{Content: "ran"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	specs := registry.Specs()
	for _, hidden := range []string{"fs_write_file", "fs_edit_file", "shell_exec", "shell_output", "shell_kill", "external_custom"} {
		if hasSpec(specs, hidden) {
			t.Fatalf("plan mode spec %s should be hidden: %#v", hidden, specs)
		}
	}
	for _, visible := range []string{"fs_read_file", "fs_grep", "fs_find_files", "todo_write", "web_search"} {
		if !hasSpec(specs, visible) {
			t.Fatalf("plan mode spec %s should remain visible: %#v", visible, specs)
		}
	}

	result, err := registry.Call(context.Background(), protocol.ToolCall{Name: "external_custom"})
	if err == nil || !strings.Contains(err.Error(), "plan mode") {
		t.Fatalf("expected plan-mode denial, got result=%#v err=%v", result, err)
	}
	if externalCalled {
		t.Fatal("external handler ran despite plan-mode denial")
	}
	if result.Metadata["permission_reason"] != "plan_mode_read_only" ||
		result.Metadata["permission_source"] != "access_mode" ||
		result.Metadata["access_mode"] != config.AccessModePlan {
		t.Fatalf("plan denial metadata = %#v", result.Metadata)
	}
}

func TestGuardedModeDeniesDangerousToolsEvenWhenAutoApproved(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModeGuarded
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "true"}}),
	})
	if err == nil || !strings.Contains(err.Error(), "guarded mode") {
		t.Fatalf("expected guarded-mode denial, got result=%#v err=%v", result, err)
	}
	if result.Metadata["permission_reason"] != "guarded_mode_dangerous_tools_disabled" ||
		result.Metadata["access_mode"] != config.AccessModeGuarded {
		t.Fatalf("guarded denial metadata = %#v", result.Metadata)
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

func TestFSReadLegacyPathReturnsFullContent(t *testing.T) {
	root := t.TempDir()
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(root, "legacy.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": "legacy.txt"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != content || result.Truncated || result.Metadata != nil {
		t.Fatalf("legacy read result=%#v", result)
	}
}

func TestFSReadOffsetLimitReturnsNumberedLineWindow(t *testing.T) {
	root := t.TempDir()
	content := strings.Join([]string{"one", "two", "three", "four", "five"}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "lines.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_read_file",
		Arguments: rawArgs(map[string]any{
			"path":   "lines.txt",
			"offset": 2,
			"limit":  2,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "2: two\n3: three\n...[truncated; next_offset=4 total_lines=5]"
	if result.Content != want {
		t.Fatalf("content = %q, want %q", result.Content, want)
	}
	if !result.Truncated ||
		anyInt64(result.Metadata["offset"]) != 2 ||
		anyInt64(result.Metadata["limit"]) != 2 ||
		anyInt64(result.Metadata["line_start"]) != 2 ||
		anyInt64(result.Metadata["line_end"]) != 3 ||
		anyInt64(result.Metadata["line_count"]) != 2 ||
		anyInt64(result.Metadata["total_lines"]) != 5 ||
		anyInt64(result.Metadata["next_offset"]) != 4 ||
		result.Metadata["lines_truncated"] != true {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestFSReadLimitClampAndLongLineTruncation(t *testing.T) {
	root := t.TempDir()
	lines := []string{strings.Repeat("x", maxFSReadLineRunes+8), "tail"}
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_read_file",
		Arguments: rawArgs(map[string]any{
			"path":  "long.txt",
			"limit": maxFSReadLimit + 50,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, fsReadTruncationLabel) || !result.Truncated {
		t.Fatalf("expected long-line truncation result=%#v content=%q", result, result.Content)
	}
	if anyInt64(result.Metadata["limit"]) != maxFSReadLimit ||
		anyInt64(result.Metadata["long_lines_truncated"]) != 1 ||
		anyInt64(result.Metadata["total_lines"]) != 2 ||
		anyInt64(result.Metadata["next_offset"]) != 0 {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestFSReadRejectsBinaryWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{'o', 'k', 0, 'x'}, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": "bin.dat", "offset": 1, "limit": 1}),
	})
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary error, got %v", err)
	}
}

func TestFSReadSensitiveAndSymlinkSafetyForWindows(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": ".env", "offset": 1, "limit": 1}),
	})
	if err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("expected sensitive path error, got %v", err)
	}
	_, err = registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(link, "outside.txt"), "offset": 1, "limit": 1}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape error, got %v", err)
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

func TestRegistryExposesExplicitParallelMetadata(t *testing.T) {
	registry := NewRegistry(config.Config{})
	registry.mcpTools["mcp__fake__echo"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__fake__echo",
			Description: "Echo.",
			Parameters:  raw(`{"type":"object","properties":{}}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: "ok"}, nil
		},
	}
	registry.addMCPGateway()
	readMeta, ok := registry.ParallelMetadata("fs_read_file")
	if !ok || readMeta.Policy != ParallelPolicyReadOnly || !readMeta.Idempotent || readMeta.RequiresExclusiveWorkspace || !readMeta.CanRunParallel() {
		t.Fatalf("read metadata = %#v ok=%v", readMeta, ok)
	}
	webMeta, ok := registry.ParallelMetadata("web_fetch")
	if !ok || webMeta.Policy != ParallelPolicyNetworkRateLimited || webMeta.RateLimitKey != "web" || webMeta.MaxConcurrency != 3 || !webMeta.CanRunParallel() {
		t.Fatalf("web metadata = %#v ok=%v", webMeta, ok)
	}
	writeMeta, ok := registry.ParallelMetadata("fs_write_file")
	if !ok || writeMeta.Policy != ParallelPolicyExclusiveWorkspace || !writeMeta.RequiresExclusiveWorkspace || writeMeta.CanRunParallel() {
		t.Fatalf("write metadata = %#v ok=%v", writeMeta, ok)
	}
	mcpMeta, ok := registry.ParallelMetadata("mcp_call")
	if !ok || mcpMeta.Policy != ParallelPolicyUnknownExternal || !mcpMeta.RequiresExclusiveWorkspace || mcpMeta.CanRunParallel() {
		t.Fatalf("mcp metadata = %#v ok=%v", mcpMeta, ok)
	}
}

func TestSkillsListAndReadAreOnDemandBoundedAndCompatOptional(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	writeSkill(t, filepath.Join(home, "skills", "teacher", "SKILL.md"), "# Teacher\nHome skill body")
	writeSkill(t, filepath.Join(project, ".billyharness", "skills", "review", "SKILL.md"), "# Review\nProject skill body with more text")
	writeSkill(t, filepath.Join(project, ".claude", "skills", "legacy", "SKILL.md"), "# Legacy\nClaude compat body")

	cfg := config.Default()
	cfg.WorkspaceRoots = []string{project}
	registry := NewRegistry(cfg)
	if !hasSpec(registry.Specs(), "skill_list") || !hasSpec(registry.Specs(), "skill_read") {
		t.Fatalf("skill tools missing from specs: %#v", registry.Specs())
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "skill_list",
		Arguments: rawArgs(map[string]any{"limit": 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name": "teacher"`, `"source": "home"`, `"name": "review"`, `"source": "project"`, `"content_injected": false`} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("skill_list missing %q in:\n%s", want, list.Content)
		}
	}
	if strings.Contains(list.Content, `"name": "legacy"`) {
		t.Fatalf("compat skill leaked without include_compat:\n%s", list.Content)
	}

	compat, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "skill_list",
		Arguments: rawArgs(map[string]any{"include_compat": true, "query": "legacy"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compat.Content, `"name": "legacy"`) || !strings.Contains(compat.Content, `"source": "claude_compat"`) {
		t.Fatalf("compat skill missing:\n%s", compat.Content)
	}

	read, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "skill_read",
		Arguments: rawArgs(map[string]any{
			"name":      "review",
			"source":    "project",
			"max_chars": 8,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !read.Truncated || read.Metadata["skill_name"] != "review" || read.Metadata["skill_source"] != "project" ||
		!strings.Contains(read.Content, `...[truncated]`) {
		t.Fatalf("bounded skill_read result=%#v content=\n%s", read, read.Content)
	}

	legacy, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "skill_read",
		Arguments: rawArgs(map[string]any{
			"name":           "legacy",
			"include_compat": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(legacy.Content, "Claude compat body") {
		t.Fatalf("legacy skill read = %s", legacy.Content)
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

func specDescriptionContains(specs []protocol.ToolSpec, name, want string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return strings.Contains(spec.Description, want)
		}
	}
	return false
}

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rawArgs(value any) json.RawMessage {
	bytes, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return bytes
}

func assertGoldenJSON(t *testing.T, name string, value any, want string) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != want {
		t.Fatalf("%s JSON mismatch\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

type fatalSummarizer struct {
	t *testing.T
}

func (s fatalSummarizer) SummarizeWeb(context.Context, webtools.SummaryRequest) (webtools.SummaryResult, error) {
	s.t.Fatalf("summarizer should not be called")
	return webtools.SummaryResult{}, nil
}

type scriptedSummarizer struct {
	check  func(webtools.SummaryRequest)
	result webtools.SummaryResult
}

func (s scriptedSummarizer) SummarizeWeb(_ context.Context, req webtools.SummaryRequest) (webtools.SummaryResult, error) {
	if s.check != nil {
		s.check(req)
	}
	return s.result, nil
}

type blockingSummarizer struct {
	entered chan webtools.SummaryRequest
	release chan struct{}
	result  webtools.SummaryResult
}

func (s *blockingSummarizer) SummarizeWeb(ctx context.Context, req webtools.SummaryRequest) (webtools.SummaryResult, error) {
	select {
	case s.entered <- req:
	case <-ctx.Done():
		return webtools.SummaryResult{}, ctx.Err()
	}
	select {
	case <-s.release:
		return s.result, nil
	case <-ctx.Done():
		return webtools.SummaryResult{}, ctx.Err()
	}
}

type failingSummarizer struct{}

func (failingSummarizer) SummarizeWeb(context.Context, webtools.SummaryRequest) (webtools.SummaryResult, error) {
	return webtools.SummaryResult{}, fmt.Errorf("summary failed")
}

func anyInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
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
