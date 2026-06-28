package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if report.Recommendation.CodingScore == 0 || report.Recommendation.ChatScore == 0 ||
		report.Recommendation.CodingReason == "" || report.Recommendation.ChatReason == "" {
		t.Fatalf("incomplete recommendation: %#v", report.Recommendation)
	}
	if report.ReportJSON == "" || report.DecisionMD == "" {
		t.Fatalf("missing report artifacts: %#v", report)
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
		if target.CodingScore == 0 || target.ChatScore == 0 || target.DecisionSummary == "" {
			t.Fatalf("target missing decision fields: %#v", target)
		}
		if _, err := os.Stat(target.ManifestJSON); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(target.ResultsJSONL); err != nil {
			t.Fatal(err)
		}
	}
	reportJSON, err := os.ReadFile(report.ReportJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reportJSON), `"recommendation"`) || !strings.Contains(string(reportJSON), `"coding_score"`) {
		t.Fatalf("report json missing decision fields: %s", string(reportJSON))
	}
	decision, err := os.ReadFile(report.DecisionMD)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Provider Decision", "Coding default", "Normal chat default", "## Targets", "Scoring Notes"} {
		if !strings.Contains(string(decision), want) {
			t.Fatalf("decision markdown missing %q: %s", want, string(decision))
		}
	}
}
