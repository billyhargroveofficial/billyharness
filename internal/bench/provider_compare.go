package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

type ProviderComparisonTarget struct {
	Model     string `json:"model"`
	Provider  string `json:"provider,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

type ProviderComparisonOptions struct {
	TasksPath              string
	OutDir                 string
	Targets                []ProviderComparisonTarget
	Live                   bool
	Limit                  int
	Timeout                time.Duration
	MaxRounds              int
	AllowDangerous         bool
	ContextCompactTokens   int
	ContextCompactKeep     int
	ContextCompactMaxChars int
}

type ProviderComparisonReport struct {
	CreatedAt      time.Time                  `json:"created_at"`
	TasksPath      string                     `json:"tasks_path"`
	OutDir         string                     `json:"out_dir"`
	Mode           string                     `json:"mode"`
	Targets        []ProviderComparisonResult `json:"targets"`
	Recommendation ProviderRecommendation     `json:"recommendation,omitempty"`
}

type ProviderComparisonResult struct {
	Provider              string   `json:"provider"`
	Model                 string   `json:"model"`
	Reasoning             string   `json:"reasoning,omitempty"`
	QualityOutcome        string   `json:"quality_outcome"`
	ToolCorrectness       string   `json:"tool_correctness"`
	ElapsedMS             int64    `json:"elapsed_ms"`
	PassRate              float64  `json:"pass_rate"`
	Passed                int      `json:"passed"`
	Failed                int      `json:"failed"`
	Timeouts              int      `json:"timeouts"`
	Crashes               int      `json:"crashes"`
	ToolCalls             int      `json:"tool_calls"`
	ToolOutputTruncations int      `json:"tool_output_truncations,omitempty"`
	ContextMaxTokens      int64    `json:"max_context_tokens,omitempty"`
	ContextGrowthTokens   int64    `json:"max_context_growth_tokens,omitempty"`
	InputTokens           int64    `json:"input_tokens,omitempty"`
	OutputTokens          int64    `json:"output_tokens,omitempty"`
	CacheHitTokens        int64    `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens       int64    `json:"cache_miss_tokens,omitempty"`
	CostMarker            string   `json:"cost_marker,omitempty"`
	Subscription          bool     `json:"subscription,omitempty"`
	EstimatedCostUSD      float64  `json:"estimated_cost_usd,omitempty"`
	ReplayVerified        bool     `json:"replay_verified"`
	ManifestJSON          string   `json:"manifest_json,omitempty"`
	ResultsJSONL          string   `json:"results_jsonl,omitempty"`
	EventsJSONL           string   `json:"events_jsonl,omitempty"`
	FailureModes          []string `json:"failure_modes,omitempty"`
}

type ProviderRecommendation struct {
	CodingModel string `json:"coding_model,omitempty"`
	ChatModel   string `json:"chat_model,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func CompareProviders(ctx context.Context, cfg config.Config, opts ProviderComparisonOptions) (ProviderComparisonReport, error) {
	if strings.TrimSpace(opts.TasksPath) == "" {
		return ProviderComparisonReport{}, fmt.Errorf("tasks path required")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return ProviderComparisonReport{}, fmt.Errorf("output directory required")
	}
	targets := normalizeComparisonTargets(opts.Targets)
	if len(targets) == 0 {
		return ProviderComparisonReport{}, fmt.Errorf("at least one target model required")
	}
	mode := "mock"
	if opts.Live {
		mode = "live"
	}
	report := ProviderComparisonReport{
		CreatedAt: time.Now().UTC(),
		TasksPath: opts.TasksPath,
		OutDir:    opts.OutDir,
		Mode:      mode,
		Targets:   make([]ProviderComparisonResult, 0, len(targets)),
	}
	for _, target := range targets {
		targetCfg := cfg
		targetCfg.Model = modelinfo.NormalizeAlias(target.Model)
		targetCfg.Provider = modelinfo.ProviderForModel(targetCfg.Model, target.Provider)
		if strings.TrimSpace(target.Reasoning) != "" {
			targetCfg.ReasoningEffort = strings.ToLower(strings.TrimSpace(target.Reasoning))
		}
		if opts.MaxRounds > 0 {
			targetCfg.MaxToolRounds = opts.MaxRounds
		}
		targetCfg.StoreReasoningContent = true
		if opts.AllowDangerous {
			targetCfg.AutoApproveDangerous = true
		}
		targetCfg.ApplyModelProviderDefaults()
		runCfg := RunConfig{
			TasksPath:              opts.TasksPath,
			OutDir:                 filepath.Join(opts.OutDir, safeTaskOutputName(targetCfg.Model)),
			Limit:                  opts.Limit,
			Mock:                   !opts.Live,
			Model:                  targetCfg.Model,
			Timeout:                opts.Timeout,
			ContextCompactTokens:   opts.ContextCompactTokens,
			ContextCompactKeep:     opts.ContextCompactKeep,
			ContextCompactMaxChars: opts.ContextCompactMaxChars,
		}
		summary, err := Run(ctx, targetCfg, runCfg)
		if err != nil {
			return report, fmt.Errorf("compare %s: %w", targetCfg.Model, err)
		}
		results, err := LoadResults(summary.ResultsJSONL)
		if err != nil {
			return report, fmt.Errorf("compare %s load results: %w", targetCfg.Model, err)
		}
		report.Targets = append(report.Targets, comparisonResult(targetCfg, summary, results))
	}
	report.Recommendation = recommendProvider(report.Targets)
	return report, nil
}

func LoadResults(path string) ([]Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var results []Result
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, scanner.Err()
}

func normalizeComparisonTargets(targets []ProviderComparisonTarget) []ProviderComparisonTarget {
	if len(targets) == 0 {
		targets = []ProviderComparisonTarget{
			{Model: "deepseek-v4-flash"},
			{Model: "deepseek-v4-pro"},
		}
	}
	out := make([]ProviderComparisonTarget, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		target.Model = modelinfo.NormalizeAlias(strings.TrimSpace(target.Model))
		if target.Model == "" || seen[target.Model] {
			continue
		}
		seen[target.Model] = true
		out = append(out, target)
	}
	return out
}

func comparisonResult(cfg config.Config, summary Summary, results []Result) ProviderComparisonResult {
	costMarker := summary.CostMarker
	subscription := summary.Subscription
	if costMarker == "" {
		costMarker, subscription = benchCostMarker(cfg)
	}
	estimatedCost := 0.0
	if costMarker != "none" {
		estimatedCost = estimateSummaryCostUSD(cfg, summary)
	}
	out := ProviderComparisonResult{
		Provider:              cfg.Provider,
		Model:                 cfg.Model,
		Reasoning:             cfg.ReasoningEffort,
		QualityOutcome:        qualityOutcome(summary),
		ToolCorrectness:       toolCorrectness(summary, results),
		ElapsedMS:             summary.WallTimeMS,
		PassRate:              summary.PassRate,
		Passed:                summary.Passed,
		Failed:                summary.Failed,
		Timeouts:              summary.Timeouts,
		Crashes:               summary.Crashes,
		ToolCalls:             summary.ToolCalls,
		ToolOutputTruncations: summary.ToolOutputTruncations,
		ContextMaxTokens:      summary.MaxContextTokens,
		ContextGrowthTokens:   summary.MaxContextGrowthTokens,
		InputTokens:           summary.InputTokens,
		OutputTokens:          summary.OutputTokens,
		CacheHitTokens:        summary.CacheHitTokens,
		CacheMissTokens:       summary.CacheMissTokens,
		CostMarker:            costMarker,
		Subscription:          subscription,
		EstimatedCostUSD:      estimatedCost,
		ReplayVerified:        summary.ReplayVerified,
		ManifestJSON:          summary.ManifestJSON,
		ResultsJSONL:          summary.ResultsJSONL,
		EventsJSONL:           summary.EventsJSONL,
		FailureModes:          failureModes(results),
	}
	return out
}

func qualityOutcome(summary Summary) string {
	if summary.Total > 0 && summary.Passed == summary.Total {
		return "pass"
	}
	if summary.Passed > 0 {
		return "partial"
	}
	return "fail"
}

func toolCorrectness(summary Summary, results []Result) string {
	if summary.ToolCalls == 0 {
		return "not_exercised"
	}
	for _, result := range results {
		if result.ToolErrors > 0 {
			return "tool_errors"
		}
	}
	if summary.Total > 0 && summary.Passed == summary.Total {
		return "ok"
	}
	return "unverified"
}

func failureModes(results []Result) []string {
	seen := map[string]bool{}
	var modes []string
	for _, result := range results {
		if result.Outcome == "pass" && strings.TrimSpace(result.Error) == "" {
			continue
		}
		mode := result.Outcome
		if strings.TrimSpace(result.Error) != "" {
			mode += ": " + truncate(result.Error, 240)
		}
		if !seen[mode] {
			seen[mode] = true
			modes = append(modes, mode)
		}
	}
	return modes
}

func estimateSummaryCostUSD(cfg config.Config, summary Summary) float64 {
	info := modelinfo.Lookup(cfg.Model)
	pricing := info.Pricing
	if pricing.CacheHitPer1M > 0 || pricing.CacheMissPer1M > 0 {
		return float64(summary.CacheHitTokens)*pricing.CacheHitPer1M/1_000_000 +
			float64(summary.CacheMissTokens)*pricing.CacheMissPer1M/1_000_000 +
			float64(summary.OutputTokens)*pricing.OutputPer1M/1_000_000
	}
	if pricing.InputPer1M > 0 || pricing.OutputPer1M > 0 {
		return float64(summary.InputTokens)*pricing.InputPer1M/1_000_000 +
			float64(summary.OutputTokens)*pricing.OutputPer1M/1_000_000
	}
	return 0
}

func recommendProvider(results []ProviderComparisonResult) ProviderRecommendation {
	if len(results) == 0 {
		return ProviderRecommendation{}
	}
	bestCoding := results[0]
	bestChat := results[0]
	for _, result := range results[1:] {
		if comparisonScore(result) > comparisonScore(bestCoding) {
			bestCoding = result
		}
		if chatScore(result) > chatScore(bestChat) {
			bestChat = result
		}
	}
	return ProviderRecommendation{
		CodingModel: bestCoding.Model,
		ChatModel:   bestChat.Model,
		Reason:      "coding favors pass rate and tool correctness; chat favors pass rate, latency, and cost marker",
	}
}

func comparisonScore(result ProviderComparisonResult) float64 {
	score := result.PassRate * 100
	if result.ToolCorrectness == "ok" {
		score += 20
	}
	if result.ReplayVerified {
		score += 5
	}
	score -= float64(result.Timeouts+result.Crashes) * 10
	if result.ElapsedMS > 0 {
		score -= float64(result.ElapsedMS) / 10_000
	}
	return score
}

func chatScore(result ProviderComparisonResult) float64 {
	score := result.PassRate * 100
	if result.CostMarker == "subscription" || result.CostMarker == "none" {
		score += 10
	}
	if result.ElapsedMS > 0 {
		score -= float64(result.ElapsedMS) / 5_000
	}
	return score
}
