package bench

import (
	"context"
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
