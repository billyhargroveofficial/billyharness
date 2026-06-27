package bench

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

func TestRunMockTaskWritesResults(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(tasksPath, []byte(`{"id":"t1","suite":"unit","prompt":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "runs")
	cfg := config.Default()
	summary, err := Run(context.Background(), cfg, RunConfig{TasksPath: tasksPath, OutDir: outDir, Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Passed != 1 || summary.ModelCalls != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if _, err := os.Stat(summary.ResultsJSONL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(summary.EventsJSONL); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, outDir, 0o700)
	assertPerm(t, summary.ResultsJSONL, 0o600)
	assertPerm(t, summary.EventsJSONL, 0o600)
}

func TestRunEvaluatorFailure(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	line := `{"id":"t1","suite":"unit","prompt":"hello","workspace":` + quote(root) + `,"evaluator":["sh","-c","exit 7"]}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	summary, err := Run(context.Background(), cfg, RunConfig{TasksPath: tasksPath, OutDir: filepath.Join(root, "runs"), Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Failed != 1 || summary.Passed != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	bytes, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), "evaluator failed") {
		t.Fatalf("results missing evaluator error: %s", bytes)
	}
}

func TestRunMockScriptedLoopCountsRoundsAndCompactions(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	line := `{"id":"stress-1","suite":"agent-loop-stress","prompt":"run scripted loop","scripted_tool_rounds":5,"context_compact_tokens":50,"context_compact_keep":2}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MaxToolRounds = 10
	summary, err := Run(context.Background(), cfg, RunConfig{TasksPath: tasksPath, OutDir: filepath.Join(root, "runs"), Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Passed != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.ModelCalls != 6 || summary.ToolCalls != 5 {
		t.Fatalf("calls = model %d tools %d, want 6/5", summary.ModelCalls, summary.ToolCalls)
	}
	if summary.ContextCompactions == 0 {
		t.Fatalf("expected compactions in summary: %#v", summary)
	}
	events, err := os.ReadFile(summary.EventsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), string(protocol.EventContextCompacted)) {
		t.Fatalf("events missing context.compacted: %s", events)
	}
	results, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(results), `"context_compactions":`) {
		t.Fatalf("results missing context compaction count: %s", results)
	}
}

func TestRunMockScriptedLoopFailsWhenToolErrorsWithoutEvaluator(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	line := `{"id":"bad-tool","suite":"agent-loop-stress","prompt":"run scripted loop","scripted_tool_rounds":1,"scripted_tool_name":"missing_tool"}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	summary, err := Run(context.Background(), cfg, RunConfig{TasksPath: tasksPath, OutDir: filepath.Join(root, "runs"), Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Failed != 1 || summary.Passed != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	results, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(results), `"tool_errors":1`) || !strings.Contains(string(results), `"outcome":"fail"`) {
		t.Fatalf("results should fail on tool error: %s", results)
	}
}

func TestApplyRunConfigSelectsProviderAfterModelOverride(t *testing.T) {
	cfg := config.Config{Provider: "deepseek", Model: "deepseek-v4-flash"}
	got := applyRunConfig(cfg, RunConfig{Model: "gpt-5.5"})
	if got.Provider != "openai-codex" || got.Model != "gpt-5.5" {
		t.Fatalf("got = %#v", got)
	}

	got = applyRunConfig(cfg, RunConfig{Mock: true, Model: "gpt-5.5"})
	if got.Provider != "mock" || got.Model != "gpt-5.5" {
		t.Fatalf("mock override should preserve provider mock, got = %#v", got)
	}
}

func TestLoadTasksSkipsBlankAndCommentLines(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(tasksPath, []byte("\n# comment\n{\"id\":\"t1\",\"prompt\":\"hello\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadTasks(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Suite != "custom" {
		t.Fatalf("tasks = %#v", tasks)
	}
}

func TestRunRedactsEvaluatorOutputInResults(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	line := `{"id":"redact","suite":"unit","prompt":"hello","workspace":` + quote(root) + `,"evaluator":["sh","-c","printf 'API_KEY=super-secret\nplain output\n'"]}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	summary, err := Run(context.Background(), cfg, RunConfig{TasksPath: tasksPath, OutDir: filepath.Join(root, "runs"), Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	text := string(bytes)
	if strings.Contains(text, "super-secret") {
		t.Fatalf("results should redact evaluator secret: %s", text)
	}
	if !strings.Contains(text, "API_KEY=[REDACTED]") || !strings.Contains(text, "plain output") {
		t.Fatalf("results redaction removed too much or too little: %s", text)
	}
}

func TestRedactForPersistenceRedactsNestedSecretsButKeepsTokenCounts(t *testing.T) {
	event := protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
		ID:        "call_secret",
		Name:      "mcp_call",
		Arguments: json.RawMessage(`{"api_key":"super-secret","input_tokens":12,"env":{"MCP_TOKEN":"token-secret"}}`),
	}}
	redacted, err := json.Marshal(redactForPersistence(event))
	if err != nil {
		t.Fatal(err)
	}
	text := string(redacted)
	if strings.Contains(text, "super-secret") || strings.Contains(text, "token-secret") {
		t.Fatalf("redacted event still contains secret: %s", text)
	}
	if !strings.Contains(text, `"api_key":"[REDACTED]"`) ||
		!strings.Contains(text, `"MCP_TOKEN":"[REDACTED]"`) ||
		!strings.Contains(text, `"input_tokens":12`) {
		t.Fatalf("unexpected redacted event: %s", text)
	}
}

func TestPrepareWorkspaceCopiesTemplateWithPrivateModesAndSafeTaskID(t *testing.T) {
	root := t.TempDir()
	template := filepath.Join(root, "template")
	if err := os.MkdirAll(filepath.Join(template, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(root, "runs")
	workspace, err := prepareWorkspace(outDir, "runid", Task{
		ID:                "../unsafe/task",
		WorkspaceTemplate: template,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(outDir, "runid-workspaces")
	rel, err := filepath.Rel(base, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		t.Fatalf("workspace escaped output base: workspace=%q base=%q rel=%q", workspace, base, rel)
	}
	assertPerm(t, base, 0o700)
	assertPerm(t, workspace, 0o700)
	assertPerm(t, filepath.Join(workspace, "bin"), 0o700)
	assertPerm(t, filepath.Join(workspace, "README.md"), 0o600)
	assertPerm(t, filepath.Join(workspace, "bin", "run.sh"), 0o700)
}

func TestObserveCountsUsageToolErrorsAndNames(t *testing.T) {
	result := Result{ToolCallsByName: map[string]int{}}
	observe(&result, protocol.Event{Type: protocol.EventModelCallStarted})
	observe(&result, protocol.Event{Type: protocol.EventToolCallStarted, Data: "fs_read_file"})
	observe(&result, protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		Name:      "fs_read_file",
		Content:   "nope",
		IsError:   true,
		ErrorCode: "validation_error",
	}})
	observe(&result, protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      10,
		"output_tokens":     3,
		"cache_hit_tokens":  7,
		"cache_miss_tokens": 3,
	}})
	if result.ModelCalls != 1 || result.ToolCalls != 1 || result.ToolCallsByName["fs_read_file"] != 1 || result.ToolErrors != 1 {
		t.Fatalf("counts = %#v", result)
	}
	if result.InputTokens != 10 || result.OutputTokens != 3 || result.CacheHitTokens != 7 || result.CacheMissTokens != 3 {
		t.Fatalf("usage = %#v", result)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func quote(s string) string {
	out := `"`
	for _, r := range s {
		if r == '\\' || r == '"' {
			out += `\`
		}
		out += string(r)
	}
	return out + `"`
}
