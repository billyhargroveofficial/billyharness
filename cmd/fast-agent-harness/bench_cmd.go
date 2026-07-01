package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/bench"
	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func benchCmd(args []string) error {
	if len(args) == 0 {
		benchUsage()
		return nil
	}
	switch args[0] {
	case "run":
		return benchRunCmd(args[1:])
	case "local-loop", "long-loop":
		return benchLocalLoopCmd(args[1:])
	case "compare-providers", "compare":
		return benchCompareProvidersCmd(args[1:])
	case "terminal-bench", "tb":
		return benchTerminalBenchCmd(args[1:])
	default:
		benchUsage()
		return fmt.Errorf("unknown bench command %q", args[0])
	}
}

func benchRunCmd(args []string) error {
	fs := flag.NewFlagSet("bench run", flag.ExitOnError)
	tasksPath := fs.String("tasks", "", "JSONL task file")
	outDir := fs.String("out", "bench-runs", "output directory for JSONL traces")
	limit := fs.Int("limit", 0, "max tasks to run")
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	timeoutSec := fs.Int("timeout-sec", 0, "per-task timeout override")
	maxRounds := fs.Int("max-rounds", 100, "max model/tool rounds per task")
	allowDangerous := fs.Bool("dangerous", false, "enable write and shell tools for benchmark tasks")
	scriptedRounds := fs.Int("scripted-rounds", 0, "mock-only scripted tool rounds for loop/compaction stress")
	contextCompactTokens := fs.Int("context-compact-tokens", 0, "override context compaction trigger tokens")
	contextCompactKeep := fs.Int("context-compact-keep", 0, "override context compaction keep count")
	contextCompactMaxChars := fs.Int("context-compact-max-chars", 0, "override context compaction summary max chars")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tasksPath == "" {
		return fmt.Errorf("-tasks required")
	}
	cfg := config.Default()
	cfg.MaxToolRounds = *maxRounds
	cfg.StoreReasoningContent = true
	if *allowDangerous {
		cfg.AutoApproveDangerous = true
	}
	rc := bench.RunConfig{
		TasksPath:              *tasksPath,
		OutDir:                 *outDir,
		Limit:                  *limit,
		Mock:                   *mock,
		Model:                  *model,
		ScriptedToolRounds:     *scriptedRounds,
		ContextCompactTokens:   *contextCompactTokens,
		ContextCompactKeep:     *contextCompactKeep,
		ContextCompactMaxChars: *contextCompactMaxChars,
	}
	if *timeoutSec > 0 {
		rc.Timeout = time.Duration(*timeoutSec) * time.Second
	}
	cfg.ApplyModelProviderDefaults()
	summary, err := bench.Run(context.Background(), cfg, rc)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

func benchLocalLoopCmd(args []string) error {
	fs := flag.NewFlagSet("bench local-loop", flag.ExitOnError)
	outDir := fs.String("out", "bench-runs/local-loop", "output directory for the local long-loop trace bundle")
	tasksPath := fs.String("tasks", "", "JSONL task file to write; defaults to <out>/local-loop-tasks.jsonl")
	turns := fs.Int("turns", 60, "target agent turns across the generated local benchmark, clamped to 50..100")
	runNow := fs.Bool("run", true, "run the generated benchmark immediately")
	liveWeb := fs.Bool("live-web", false, "use real native web_search in the web task; default uses offline tool discovery")
	timeoutSec := fs.Int("timeout-sec", 180, "per-task timeout in seconds")
	maxRounds := fs.Int("max-rounds", 0, "max model/tool rounds per generated task; 0 derives from -turns")
	contextCompactTokens := fs.Int("context-compact-tokens", 0, "override context compaction trigger tokens")
	contextCompactKeep := fs.Int("context-compact-keep", 0, "override context compaction keep count")
	contextCompactMaxChars := fs.Int("context-compact-max-chars", 0, "override context compaction summary max chars")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tasksPath) == "" {
		*tasksPath = filepath.Join(*outDir, "local-loop-tasks.jsonl")
	}
	generated, err := bench.WriteLocalLoopTasks(bench.LocalLoopOptions{
		TasksPath: *tasksPath,
		Turns:     *turns,
		LiveWeb:   *liveWeb,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if !*runNow {
		return enc.Encode(generated)
	}

	cfg := config.Default()
	cfg.StoreReasoningContent = true
	cfg.AutoApproveDangerous = true
	if *maxRounds > 0 {
		cfg.MaxToolRounds = *maxRounds
	} else {
		cfg.MaxToolRounds = max(100, generated.ExpectedTurns+10)
	}
	cfg.ApplyModelProviderDefaults()
	rc := bench.RunConfig{
		TasksPath:              generated.TasksPath,
		OutDir:                 *outDir,
		Mock:                   true,
		ContextCompactTokens:   *contextCompactTokens,
		ContextCompactKeep:     *contextCompactKeep,
		ContextCompactMaxChars: *contextCompactMaxChars,
	}
	if *timeoutSec > 0 {
		rc.Timeout = time.Duration(*timeoutSec) * time.Second
	}
	summary, err := bench.Run(context.Background(), cfg, rc)
	if err != nil {
		return err
	}
	return enc.Encode(struct {
		Generated bench.LocalLoopSummary `json:"generated"`
		Run       bench.Summary          `json:"run"`
	}{
		Generated: generated,
		Run:       summary,
	})
}

func benchCompareProvidersCmd(args []string) error {
	cfg := config.Default()
	fs := flag.NewFlagSet("bench compare-providers", flag.ExitOnError)
	tasksPath := fs.String("tasks", "", "JSONL task file to run for every provider/model")
	outDir := fs.String("out", "bench-runs/provider-compare", "output directory for provider comparison trace bundles")
	modelsRaw := fs.String("models", "deepseek-v4-flash,deepseek-v4-pro", "comma-separated models to compare")
	includeCodex := fs.Bool("codex", false, "also compare the default Codex/OpenAI OAuth model")
	live := fs.Bool("live", false, "run real providers; default false uses mock scripted runs and spends no API tokens")
	limit := fs.Int("limit", 0, "max tasks to run per target")
	timeoutSec := fs.Int("timeout-sec", 0, "per-task timeout override")
	maxRounds := fs.Int("max-rounds", cfg.MaxToolRounds, "max model/tool rounds per task")
	allowDangerous := fs.Bool("dangerous", true, "enable write and shell tools for benchmark tasks")
	reasoning := fs.String("reasoning", cfg.ReasoningEffort, "reasoning effort for live provider runs")
	contextCompactTokens := fs.Int("context-compact-tokens", 0, "override context compaction trigger tokens")
	contextCompactKeep := fs.Int("context-compact-keep", 0, "override context compaction keep count")
	contextCompactMaxChars := fs.Int("context-compact-max-chars", 0, "override context compaction summary max chars")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tasksPath) == "" {
		return fmt.Errorf("-tasks required")
	}
	targets := comparisonTargetsFromModels(*modelsRaw, *includeCodex, *reasoning)
	if len(targets) == 0 {
		return fmt.Errorf("no models to compare")
	}
	opts := bench.ProviderComparisonOptions{
		TasksPath:              *tasksPath,
		OutDir:                 *outDir,
		Targets:                targets,
		Live:                   *live,
		Limit:                  *limit,
		MaxRounds:              *maxRounds,
		AllowDangerous:         *allowDangerous,
		ContextCompactTokens:   *contextCompactTokens,
		ContextCompactKeep:     *contextCompactKeep,
		ContextCompactMaxChars: *contextCompactMaxChars,
	}
	if *timeoutSec > 0 {
		opts.Timeout = time.Duration(*timeoutSec) * time.Second
	}
	report, err := bench.CompareProviders(context.Background(), cfg, opts)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func comparisonTargetsFromModels(modelsRaw string, includeCodex bool, reasoning string) []bench.ProviderComparisonTarget {
	var targets []bench.ProviderComparisonTarget
	for _, model := range strings.Split(modelsRaw, ",") {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		targets = append(targets, bench.ProviderComparisonTarget{Model: model, Reasoning: reasoning})
	}
	if includeCodex {
		targets = append(targets, bench.ProviderComparisonTarget{Model: "gpt-5.5", Reasoning: reasoning})
	}
	return targets
}

func benchTerminalBenchCmd(args []string) error {
	if len(args) == 0 {
		benchTerminalBenchUsage()
		return nil
	}
	switch args[0] {
	case "export":
		return benchTerminalBenchExportCmd(args[1:])
	case "import":
		return benchTerminalBenchImportCmd(args[1:])
	default:
		benchTerminalBenchUsage()
		return fmt.Errorf("unknown bench terminal-bench command %q", args[0])
	}
}

func benchTerminalBenchExportCmd(args []string) error {
	fs := flag.NewFlagSet("bench terminal-bench export", flag.ExitOnError)
	tasksPath := fs.String("tasks", "", "billyharness JSONL task file")
	outDir := fs.String("out", "benchmarks/terminal-bench-export", "Terminal-Bench dataset output directory")
	force := fs.Bool("force", false, "replace an existing Terminal-Bench dataset output directory")
	authorName := fs.String("author-name", "billyharness", "Terminal-Bench author_name")
	authorEmail := fs.String("author-email", "unknown", "Terminal-Bench author_email")
	difficulty := fs.String("difficulty", "unknown", "Terminal-Bench difficulty: easy, medium, hard, or unknown")
	category := fs.String("category", "software_engineering", "Terminal-Bench category")
	maxTestTimeoutSec := fs.Int("max-test-timeout-sec", 60, "Terminal-Bench max_test_timeout_sec")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tasksPath == "" {
		return fmt.Errorf("-tasks required")
	}
	summary, err := bench.ExportTerminalBenchDataset(bench.TerminalBenchExportOptions{
		TasksPath:             *tasksPath,
		OutDir:                *outDir,
		Force:                 *force,
		AuthorName:            *authorName,
		AuthorEmail:           *authorEmail,
		Difficulty:            *difficulty,
		Category:              *category,
		MaxTestTimeoutSeconds: *maxTestTimeoutSec,
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

func benchTerminalBenchImportCmd(args []string) error {
	fs := flag.NewFlagSet("bench terminal-bench import", flag.ExitOnError)
	datasetDir := fs.String("dataset", "", "Terminal-Bench dataset directory")
	outPath := fs.String("out", "", "billyharness JSONL task output; stdout when omitted")
	suite := fs.String("suite", bench.TerminalBenchSuite, "suite for generic Terminal-Bench imports")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *datasetDir == "" {
		return fmt.Errorf("-dataset required")
	}
	tasks, err := bench.ImportTerminalBenchDataset(bench.TerminalBenchImportOptions{
		DatasetDir: *datasetDir,
		Suite:      *suite,
	})
	if err != nil {
		return err
	}
	if *outPath == "" {
		return bench.EncodeTasksJSONL(os.Stdout, tasks)
	}
	return bench.WriteTasksJSONL(*outPath, tasks)
}

func benchUsage() {
	fmt.Println("usage:")
	fmt.Println("  fast-agent-harness bench run -tasks tasks.jsonl -out runs")
	fmt.Println("  fast-agent-harness bench local-loop [-out runs/local-loop] [-turns 60]")
	fmt.Println("  fast-agent-harness bench compare-providers -tasks tasks.jsonl [-out runs/provider-compare] [-live]")
	fmt.Println("  fast-agent-harness bench terminal-bench export -tasks tasks.jsonl -out tb-dataset")
	fmt.Println("  fast-agent-harness bench terminal-bench import -dataset tb-dataset [-out tasks.jsonl]")
}

func benchTerminalBenchUsage() {
	fmt.Println("usage:")
	fmt.Println("  fast-agent-harness bench terminal-bench export -tasks tasks.jsonl -out tb-dataset")
	fmt.Println("  fast-agent-harness bench terminal-bench import -dataset tb-dataset [-out tasks.jsonl]")
}
