package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
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
