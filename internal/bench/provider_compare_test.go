package bench

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestCompareProvidersMockReport(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	task := `{"id":"compare-1","suite":"provider-compare","prompt":"run scripted compare","scripted_tool_rounds":2,"scripted_tool_name":"time_now"}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MaxToolRounds = 5
	report, err := CompareProviders(context.Background(), cfg, ProviderComparisonOptions{
		TasksPath: tasksPath,
		OutDir:    filepath.Join(root, "runs"),
		Targets: []ProviderComparisonTarget{
			{Model: "deepseek-v4-flash", Reasoning: "high"},
			{Model: "deepseek-v4-pro", Reasoning: "high"},
		},
		Live: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "mock" || len(report.Targets) != 2 {
		t.Fatalf("report = %#v", report)
	}
	if report.Recommendation.CodingModel == "" || report.Recommendation.ChatModel == "" {
		t.Fatalf("missing recommendation: %#v", report.Recommendation)
	}
	for _, target := range report.Targets {
		if target.Provider != "deepseek" || target.Reasoning != "high" {
			t.Fatalf("target provider/reasoning = %#v", target)
		}
		if target.QualityOutcome != "pass" || target.ToolCorrectness != "ok" || !target.ReplayVerified {
			t.Fatalf("target quality = %#v", target)
		}
		if target.Passed != 1 || target.ToolCalls != 2 || target.ContextMaxTokens <= 0 {
			t.Fatalf("target counters = %#v", target)
		}
		if target.CostMarker != "none" || target.EstimatedCostUSD != 0 || target.Subscription {
			t.Fatalf("mock target should not report live cost: %#v", target)
		}
		if _, err := os.Stat(target.ManifestJSON); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(target.ResultsJSONL); err != nil {
			t.Fatal(err)
		}
	}
}
