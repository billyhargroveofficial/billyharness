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
	"github.com/billyhargroveofficial/billyharness/internal/trace"
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
	if summary.CostMarker != "none" || summary.Subscription {
		t.Fatalf("unexpected cost marker: %#v", summary)
	}
	if _, err := os.Stat(summary.ResultsJSONL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(summary.EventsJSONL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(summary.ManifestJSON); err != nil {
		t.Fatal(err)
	}
	if !summary.ReplayVerified {
		t.Fatalf("summary should mark replay verified: %#v", summary)
	}
	if summary.ProfileHash == "" {
		t.Fatalf("summary missing profile hash: %#v", summary)
	}
	assertPerm(t, outDir, 0o700)
	assertPerm(t, summary.ResultsJSONL, 0o600)
	assertPerm(t, summary.EventsJSONL, 0o600)
	assertPerm(t, summary.ManifestJSON, 0o600)
	assertPerm(t, summary.PayloadsDir, 0o700)

	var manifest trace.Manifest
	bytes, err := os.ReadFile(summary.ManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != trace.CurrentManifestVersion ||
		manifest.RunID == "" ||
		manifest.ResultsJSONL != summary.ResultsJSONL ||
		manifest.EventsJSONL != summary.EventsJSONL ||
		manifest.PayloadsDir != summary.PayloadsDir ||
		manifest.ProfileHash != summary.ProfileHash {
		t.Fatalf("manifest = %#v summary = %#v", manifest, summary)
	}
	if manifest.ConfigSnapshot["model"] != "mock" ||
		manifest.ConfigSnapshot["provider"] != "mock" ||
		manifest.ProviderModelMetadata["model"] != "mock" ||
		manifest.ProviderModelMetadata["provider"] != "mock" ||
		manifest.MCPStatus["enabled"] == nil {
		t.Fatalf("manifest missing audit snapshots: %#v", manifest)
	}

	resultsBytes, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(resultsBytes))), &result); err != nil {
		t.Fatal(err)
	}
	if result.ProfileHash != summary.ProfileHash {
		t.Fatalf("result profile hash = %q, want %q", result.ProfileHash, summary.ProfileHash)
	}
	if result.CostMarker != "none" || result.Subscription {
		t.Fatalf("unexpected result cost marker: %#v", result)
	}
	replay, err := trace.ReplayEvents(summary.EventsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.ProfileHashes) != 1 || replay.ProfileHashes[0] != summary.ProfileHash {
		t.Fatalf("replay profile hashes = %#v, want %q", replay.ProfileHashes, summary.ProfileHash)
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
	if summary.Turns != 6 || summary.Steps != 11 || summary.StepErrors != 0 || summary.ParallelBatches != 0 {
		t.Fatalf("turn/step counters = turns %d steps %d errors %d batches %d, want 6/11/0/0", summary.Turns, summary.Steps, summary.StepErrors, summary.ParallelBatches)
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
	replay, err := trace.ReplayEvents(summary.EventsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if replay.EventTypes[string(protocol.EventContextCompacted)] == 0 ||
		replay.EventTypes[string(protocol.EventToolCallStarted)] != 5 ||
		replay.TurnsStarted != 6 ||
		replay.StepsStarted != 11 {
		t.Fatalf("replay = %#v", replay)
	}
	results, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(results), `"context_compactions":`) {
		t.Fatalf("results missing context compaction count: %s", results)
	}
	if !strings.Contains(string(results), `"turns":6`) || !strings.Contains(string(results), `"steps":11`) {
		t.Fatalf("results missing turn/step counts: %s", results)
	}
}

func TestLocalLoopBenchmarkGeneratesReplayableFiftyTurnSuite(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "local-loop", "tasks.jsonl")
	generated, err := WriteLocalLoopTasks(LocalLoopOptions{TasksPath: tasksPath, Turns: 50})
	if err != nil {
		t.Fatal(err)
	}
	if generated.Tasks != 5 || generated.ExpectedTurns != 50 || generated.ScriptedRounds+generated.Tasks != generated.ExpectedTurns {
		t.Fatalf("generated summary = %#v", generated)
	}
	if _, err := os.Stat(filepath.Join(generated.WorkspaceTemplate, "README.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(generated.WorkspaceTemplate, "app", "main.txt")); err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadTasks(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != generated.Tasks {
		t.Fatalf("loaded %d tasks, want %d", len(tasks), generated.Tasks)
	}
	wantIDs := map[string]bool{
		"local-loop-app-build":     false,
		"local-loop-file-search":   false,
		"local-loop-web-caps":      false,
		"local-loop-mcp-discovery": false,
		"local-loop-cancel-resume": false,
	}
	wantTools := map[string]bool{
		"fs_write_file": false,
		"fs_search":     false,
		"tool_search":   false,
		"time_now":      false,
	}
	for _, task := range tasks {
		if task.Suite != LocalLongLoopSuite || task.WorkspaceTemplate != generated.WorkspaceTemplate {
			t.Fatalf("task has wrong suite/template: %#v", task)
		}
		if _, ok := wantIDs[task.ID]; !ok {
			t.Fatalf("unexpected task id %q", task.ID)
		}
		wantIDs[task.ID] = true
		if _, ok := wantTools[task.ScriptedToolName]; !ok {
			t.Fatalf("unexpected scripted tool %q", task.ScriptedToolName)
		}
		wantTools[task.ScriptedToolName] = true
		if task.ScriptedToolRounds <= 0 || task.ScriptedToolArgs == "" {
			t.Fatalf("task missing scripted loop fields: %#v", task)
		}
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Fatalf("missing generated task %s", id)
		}
	}
	for name, seen := range wantTools {
		if !seen {
			t.Fatalf("missing scripted tool %s", name)
		}
	}
	liveGenerated, err := WriteLocalLoopTasks(LocalLoopOptions{TasksPath: filepath.Join(root, "local-loop-live", "tasks.jsonl"), Turns: 50, LiveWeb: true})
	if err != nil {
		t.Fatal(err)
	}
	liveTasks, err := LoadTasks(liveGenerated.TasksPath)
	if err != nil {
		t.Fatal(err)
	}
	var sawLiveWeb bool
	for _, task := range liveTasks {
		if task.ID == "local-loop-web-caps" && task.ScriptedToolName == "web_search" && hasTag(task.Tags, "live-network") {
			sawLiveWeb = true
		}
	}
	if !sawLiveWeb {
		t.Fatalf("live web local-loop task not generated: %#v", liveTasks)
	}

	cfg := config.Default()
	cfg.AutoApproveDangerous = true
	cfg.StoreReasoningContent = true
	cfg.MaxToolRounds = 60
	summary, err := Run(context.Background(), cfg, RunConfig{
		TasksPath: tasksPath,
		OutDir:    filepath.Join(root, "runs"),
		Mock:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != generated.Tasks || summary.Passed != generated.Tasks || !summary.ReplayVerified {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.Turns != generated.ExpectedTurns || summary.ToolCalls != generated.ScriptedRounds {
		t.Fatalf("turn/tool counts = turns %d tools %d, want %d/%d", summary.Turns, summary.ToolCalls, generated.ExpectedTurns, generated.ScriptedRounds)
	}
	if summary.ToolCalls <= 0 || summary.ModelCalls != summary.Turns {
		t.Fatalf("unexpected model/tool summary = %#v", summary)
	}
	if summary.AvgFirstDeltaMS <= 0 || summary.FirstDeltaP50MS <= 0 || summary.FirstDeltaP95MS < summary.FirstDeltaP50MS {
		t.Fatalf("missing first-delta metrics: %#v", summary)
	}
	if summary.ModelLatencyP95MS < summary.ModelLatencyP50MS {
		t.Fatalf("missing latency percentiles: %#v", summary)
	}
	if summary.MaxContextTokens <= 0 || summary.MaxContextGrowthTokens <= 0 {
		t.Fatalf("missing context growth counters: %#v", summary)
	}
	resultsBytes, err := os.ReadFile(summary.ResultsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	resultsText := string(resultsBytes)
	for _, name := range []string{"fs_write_file", "fs_search", "tool_search", "time_now"} {
		if !strings.Contains(resultsText, name) {
			t.Fatalf("results missing tool name %s: %s", name, resultsText)
		}
	}
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func TestRunTraceEventsHaveSeqPayloadRefsAndRedaction(t *testing.T) {
	root := t.TempDir()
	tasksPath := filepath.Join(root, "tasks.jsonl")
	line := `{"id":"secret-tool","suite":"unit","prompt":"run scripted loop","scripted_tool_rounds":1,"scripted_tool_name":"missing_tool","scripted_tool_args":"{\"api_key\":\"super-secret\",\"input_tokens\":12}"}` + "\n"
	if err := os.WriteFile(tasksPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	summary, err := Run(context.Background(), cfg, RunConfig{TasksPath: tasksPath, OutDir: filepath.Join(root, "runs"), Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Failed != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	records := readTraceRecords(t, summary.EventsJSONL)
	if len(records) == 0 {
		t.Fatal("no event records")
	}
	var payloadRefs []trace.PayloadRef
	for i, record := range records {
		if record.SchemaVersion != trace.CurrentManifestVersion {
			t.Fatalf("record %d schema = %d", i, record.SchemaVersion)
		}
		if record.Seq != int64(i+1) {
			t.Fatalf("record %d seq = %d", i, record.Seq)
		}
		if record.RunID == "" || record.TaskID != "secret-tool" || record.EventType == "" {
			t.Fatalf("record %d = %#v", i, record)
		}
		payloadRefs = append(payloadRefs, record.PayloadRefs...)
	}
	if len(payloadRefs) == 0 {
		t.Fatalf("expected payload refs in records: %#v", records)
	}
	eventsBytes, err := os.ReadFile(summary.EventsJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(eventsBytes), "super-secret") {
		t.Fatalf("events leaked secret: %s", eventsBytes)
	}
	var sawRedaction bool
	for _, ref := range payloadRefs {
		rel, err := filepath.Rel(summary.PayloadsDir, ref.Path)
		if err != nil {
			t.Fatal(err)
		}
		if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			t.Fatalf("payload escaped dir: dir=%q path=%q rel=%q", summary.PayloadsDir, ref.Path, rel)
		}
		bytes, err := os.ReadFile(ref.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(bytes), "super-secret") {
			t.Fatalf("payload leaked secret: %s", bytes)
		}
		if strings.Contains(string(bytes), "[REDACTED]") {
			sawRedaction = true
		}
		assertPerm(t, ref.Path, 0o600)
	}
	if !sawRedaction {
		t.Fatalf("no payload contained redaction marker")
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
	observe(&result, protocol.Event{Type: protocol.EventTurnStarted, Data: protocol.TurnEvent{TurnID: "turn-001", Round: 1, Status: protocol.TurnStatusStarted}})
	observe(&result, protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:tool-batch-001", Kind: protocol.StepKindToolBatch, Status: protocol.StepStatusStarted, Parallel: true}})
	observe(&result, protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:model-call-001", Kind: protocol.StepKindModelCall, Status: protocol.StepStatusCompleted, DurationMS: 12, Metadata: map[string]any{"first_delta_ms": 5}}})
	observe(&result, protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:tool-call-001", Kind: protocol.StepKindToolCall, Status: protocol.StepStatusCompleted, DurationMS: 3}})
	observe(&result, protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:tool-batch-001", Kind: protocol.StepKindToolBatch, Status: protocol.StepStatusFailed, DurationMS: 8}})
	observe(&result, protocol.Event{Type: protocol.EventModelCallStarted})
	observe(&result, protocol.Event{Type: protocol.EventToolCallStarted, Data: "fs_read_file"})
	observe(&result, protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		Name:      "fs_read_file",
		Content:   "nope",
		IsError:   true,
		ErrorCode: "validation_error",
		Metadata: map[string]any{
			"tool_summary_saved_tokens":        120,
			"tool_summary_api_total_tokens":    40,
			"tool_summary_estimated_cost_usd":  0.001,
			"tool_summary_external_model_used": true,
		},
	}})
	observe(&result, protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      10,
		"output_tokens":     3,
		"cache_hit_tokens":  7,
		"cache_miss_tokens": 3,
	}})
	observe(&result, protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      22,
		"output_tokens":     1,
		"cache_hit_tokens":  12,
		"cache_miss_tokens": 10,
	}})
	if result.ModelCalls != 1 || result.ToolCalls != 1 || result.ToolCallsByName["fs_read_file"] != 1 || result.ToolErrors != 1 {
		t.Fatalf("counts = %#v", result)
	}
	if result.Turns != 1 || result.Steps != 1 || result.StepErrors != 1 || result.ParallelBatches != 1 {
		t.Fatalf("turn/step counts = %#v", result)
	}
	if result.FirstDeltaMS != 5 || result.ModelLatencyMS != 12 || result.ToolLatencyMS != 3 || result.ParallelBatchLatencyMS != 8 {
		t.Fatalf("latency counts = %#v", result)
	}
	if result.InputTokens != 32 || result.OutputTokens != 4 || result.CacheHitTokens != 19 || result.CacheMissTokens != 13 {
		t.Fatalf("usage = %#v", result)
	}
	if result.ContextFirstTokens != 10 || result.ContextMaxTokens != 22 || result.ContextGrowthTokens != 12 {
		t.Fatalf("context growth = %#v", result)
	}
	if result.WebSummarySavedTokens != 120 || result.WebSummaryAPITokens != 40 ||
		result.WebSummaryCostUSD != 0.001 || result.WebSummaryModelCalls != 1 {
		t.Fatalf("web summary counters = %#v", result)
	}
}

func TestLatencyPercentilesUseNearestRank(t *testing.T) {
	p50, p95 := latencyPercentiles([]int64{40, 10, 20, 30})
	if p50 != 20 || p95 != 40 {
		t.Fatalf("percentiles = %d/%d, want 20/40", p50, p95)
	}
	p50, p95 = latencyPercentiles([]int64{7})
	if p50 != 7 || p95 != 7 {
		t.Fatalf("single percentiles = %d/%d, want 7/7", p50, p95)
	}
	p50, p95 = latencyPercentiles(nil)
	if p50 != 0 || p95 != 0 {
		t.Fatalf("empty percentiles = %d/%d, want 0/0", p50, p95)
	}
}

func TestBenchCostMarkerUsesProviderCatalog(t *testing.T) {
	marker, subscription := benchCostMarker(config.Config{Provider: "mock", Model: "mock"})
	if marker != "none" || subscription {
		t.Fatalf("mock marker = %q subscription=%v", marker, subscription)
	}
	marker, subscription = benchCostMarker(config.Config{Provider: "deepseek", Model: "deepseek-v4-flash"})
	if marker != "metered" || subscription {
		t.Fatalf("deepseek marker = %q subscription=%v", marker, subscription)
	}
	marker, subscription = benchCostMarker(config.Config{Provider: "openai-codex", Model: "gpt-5.5"})
	if marker != "subscription" || !subscription {
		t.Fatalf("codex marker = %q subscription=%v", marker, subscription)
	}
}

func TestVerifyReplayAgainstResultsRejectsCounterMismatch(t *testing.T) {
	results := []Result{{
		ModelCalls:         2,
		ToolCalls:          1,
		ContextCompactions: 1,
		InputTokens:        100,
		OutputTokens:       8,
		CacheHitTokens:     70,
		CacheMissTokens:    30,
	}}
	replay := trace.ReplaySummary{
		ModelCallsStarted:  2,
		ToolCallsStarted:   1,
		ToolCallsFinished:  1,
		ContextCompactions: 1,
		InputTokens:        100,
		OutputTokens:       8,
		CacheHitTokens:     70,
		CacheMissTokens:    30,
	}
	if err := verifyReplayAgainstResults(replay, results); err != nil {
		t.Fatalf("expected matching replay, got %v", err)
	}

	replay.ToolCallsFinished = 0
	err := verifyReplayAgainstResults(replay, results)
	if err == nil || !strings.Contains(err.Error(), "tool_calls_finished") {
		t.Fatalf("expected tool_calls_finished mismatch, got %v", err)
	}
}

func readTraceRecords(t *testing.T, path string) []trace.EventRecord {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(bytes)), "\n")
	records := make([]trace.EventRecord, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record trace.EventRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
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
