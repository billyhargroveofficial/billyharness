package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunMessagesExecutesToolAndContinuesLoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 3
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"hello"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "write file"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello" {
		t.Fatalf("written content = %q", bytes)
	}
	if len(next) < 5 || next[len(next)-1].Content != "finished" {
		t.Fatalf("messages = %#v", next)
	}
	if !sawEvent(events, protocol.EventToolCallRequested) || !sawEvent(events, protocol.EventToolCallFinished) || !sawEvent(events, protocol.EventRunCompleted) {
		t.Fatalf("events missing tool/run completion: %#v", events)
	}
	assertAgentLifecycleValid(t, events)
	if !sawToolAudit(events, "fs_write_file", protocol.RiskWrite, true) {
		t.Fatalf("write tool audit event missing: %#v", events)
	}
	if result, ok := firstToolResult(events); !ok || result.Name != "fs_write_file" || result.CallID != "call_1" || result.IsError {
		t.Fatalf("tool result event = %#v ok=%v", result, ok)
	} else if result.Metadata["attempt_id"] == "" ||
		result.Metadata["permission_decision"] != "allow" ||
		result.Metadata["permission_source"] != "config" ||
		result.Metadata["permission_reason"] != "auto_approve_dangerous" {
		t.Fatalf("tool result metadata missing orchestrator fields: %#v", result.Metadata)
	}
	if !sawPermissionDecision(events, "fs_write_file", "allow", "config", "auto_approve_dangerous") {
		t.Fatalf("permission decision missing: %#v", events)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
}

func TestRunMessagesMutatingToolEmitsTurnChangeRecorded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"hello\n"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "write file"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	change, ok := firstTurnChange(events)
	if !ok {
		t.Fatalf("turn change event missing: %#v", events)
	}
	if change.ChangeID == "" || change.ToolName != "fs_write_file" || change.FileCount != 1 || change.Added != 1 || change.PatchOutputRef == "" {
		t.Fatalf("turn change = %#v", change)
	}
	if !strings.HasPrefix(change.PatchOutputRef, filepath.Join(home, "tool-output")) {
		t.Fatalf("patch output ref = %q, want under %q", change.PatchOutputRef, home)
	}
	if _, err := os.Stat(change.PatchOutputRef); err != nil {
		t.Fatal(err)
	}
	result, ok := firstToolResult(events)
	if !ok || result.Metadata["turn_change_id"] != change.ChangeID || result.Metadata["turn_change_output_ref"] != change.PatchOutputRef {
		t.Fatalf("tool result metadata = %#v ok=%v change=%#v", result.Metadata, ok, change)
	}
}

func TestRunMessagesFSEditFileEmitsTurnChangeRecorded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "out.txt"), []byte("hello old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_edit", ToolName: "fs_edit_file", ArgsDelta: `{"path":"out.txt","edits":[{"old_string":"old","new_string":"new"}]}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "edit file"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello new\n" {
		t.Fatalf("edited content = %q", bytes)
	}
	change, ok := firstTurnChange(events)
	if !ok {
		t.Fatalf("turn change event missing: %#v", events)
	}
	if change.ToolName != "fs_edit_file" || change.FileCount != 1 || change.Modified != 1 || change.PatchOutputRef == "" {
		t.Fatalf("turn change = %#v", change)
	}
	result, ok := firstToolResult(events)
	if !ok || result.Metadata["turn_change_id"] != change.ChangeID || result.Metadata["before_sha256"] == "" || result.Metadata["after_sha256"] == "" {
		t.Fatalf("tool result metadata = %#v ok=%v change=%#v", result.Metadata, ok, change)
	}
}

func TestRunMessagesShellExecEmitsTurnChangeRecorded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_shell", ToolName: "shell_exec", ArgsDelta: `{"argv":["sh","-c","printf shell > shell.txt"],"cwd":"."}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run shell"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	change, ok := firstTurnChange(events)
	if !ok {
		t.Fatalf("turn change event missing: %#v", events)
	}
	if change.ToolName != "shell_exec" || change.FileCount != 1 || change.Added != 1 || change.PatchOutputRef == "" {
		t.Fatalf("turn change = %#v", change)
	}
}

func TestRunMessagesShellExecBackgroundReturnsManagedProcessID(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_shell_bg", ToolName: "shell_exec", ArgsDelta: `{"argv":["sh","-c","printf ready; sleep 1"],"cwd":".","background":true}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "started"},
			{Kind: provider.EventDone},
		},
	}}
	registry := tools.NewRegistry(cfg)
	defer registry.Close()
	a := New(cfg, prov, registry)
	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "start dev server"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	result, ok := firstToolResult(events)
	if !ok || result.Name != "shell_exec" || result.Metadata["process_id"] == "" || result.Metadata["running"] != true {
		t.Fatalf("tool result = %#v ok=%v", result, ok)
	}
}

func TestRunMessagesDiagnosticsRunReturnsOutputRefMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = true
	cfg.DiagnosticsCommands = []config.DiagnosticCommand{{
		Name:             "lint",
		Command:          "sh",
		Args:             []string{"-c", "printf 'pkg/main.go:3:2: error: bad\\n'; exit 1"},
		Timeout:          time.Second,
		MaxOutputBytes:   4096,
		MaxIssues:        10,
		MaxIssuesPerFile: 5,
		Enabled:          true,
	}}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_diagnostics", ToolName: "diagnostics_run", ArgsDelta: `{"name":"lint"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "checked"},
			{Kind: provider.EventDone},
		},
	}}
	registry := tools.NewRegistry(cfg)
	a := New(cfg, prov, registry)
	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "check diagnostics"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	result, ok := firstToolResult(events)
	if !ok || result.Name != "diagnostics_run" || result.OutputRef == "" || result.Metadata["diagnostics_issue_count"] == nil {
		t.Fatalf("tool result = %#v ok=%v", result, ok)
	}
}

func TestRunMessagesToolOrchestratorEmitsSafePermissionAndAttempt(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 2
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_time", ToolName: "time_now", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "time"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPermissionDecision(events, "time_now", "allow", "auto", "safe_tool") {
		t.Fatalf("safe permission decision missing: %#v", events)
	}
	decisionIndex := eventIndex(events, protocol.EventToolPermissionDecided)
	startIndex := eventIndex(events, protocol.EventToolCallStarted)
	if decisionIndex < 0 || startIndex < 0 || decisionIndex > startIndex {
		t.Fatalf("permission decision should precede call start; decision=%d start=%d events=%#v", decisionIndex, startIndex, events)
	}
	result, ok := firstToolResult(events)
	if !ok ||
		result.CallID != "call_time" ||
		result.Metadata["attempt_id"] == "" ||
		result.Metadata["tool_name"] != "time_now" ||
		result.Metadata["args_summary"] != "{}" ||
		result.Metadata["permission_decision"] != "allow" ||
		result.Metadata["permission_source"] != "auto" ||
		result.Metadata["started_at"] == nil ||
		result.Metadata["finished_at"] == nil ||
		result.Metadata["duration_ms"] == nil ||
		result.Metadata["output_bytes"] == nil ||
		result.Metadata["output_estimated_tokens"] == nil ||
		result.Metadata["truncated"] == nil {
		t.Fatalf("tool result metadata = %#v ok=%v", result, ok)
	}
	if result.Compact == nil ||
		result.Compact.CallID != "call_time" ||
		result.Compact.Name != "time_now" ||
		result.Compact.Status != protocol.StepStatusCompleted ||
		result.Compact.Title != "time_now" {
		t.Fatalf("tool result compact = %#v", result.Compact)
	}
	progress := toolProgressEvents(events, "call_time")
	wantPhases := []string{
		toolPhasePrepare,
		toolPhasePermissionDecision,
		toolPhaseAttemptStarted,
		toolPhaseExecuting,
		toolPhaseAttemptFinished,
		toolPhaseRetryDecision,
		toolPhaseFinalize,
	}
	if len(progress) != len(wantPhases) {
		t.Fatalf("tool progress events = %#v", progress)
	}
	for i, want := range wantPhases {
		if progress[i].Phase != want {
			t.Fatalf("progress[%d].phase = %q, want %q: %#v", i, progress[i].Phase, want, progress)
		}
	}
	if progress[0].Status != protocol.StepStatusStarted ||
		progress[1].Status != "allow" ||
		progress[3].Status != protocol.StepStatusStarted ||
		progress[4].Status != protocol.StepStatusCompleted ||
		progress[5].Status != toolProgressStatusSkipped ||
		progress[6].Status != protocol.StepStatusCompleted {
		t.Fatalf("tool progress statuses = %#v", progress)
	}
	if progress[0].Compact == nil ||
		progress[0].Compact.CallID != "call_time" ||
		progress[0].Compact.Lifecycle != toolPhasePrepare ||
		progress[0].Compact.Target != "{}" {
		t.Fatalf("tool progress compact = %#v", progress[0].Compact)
	}
}

func TestApplyToolCompactMetadataUsesDisplaySummary(t *testing.T) {
	compact := protocol.ToolCompact{
		Summary: "fallback summary",
		Target:  "fallback target",
	}
	applyToolCompactMetadata(&compact, map[string]any{
		"display_summary": "plan 2 todos | 1 in progress",
		"display_target":  "Build todo_write",
	})
	if compact.Summary != "plan 2 todos | 1 in progress" || compact.Target != "Build todo_write" {
		t.Fatalf("compact metadata = %#v", compact)
	}
}

func TestRunMessagesToolOrchestratorDeniesDangerousToolBeforeExecution(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = false
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"blocked"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "write"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied tool should not write file, stat err=%v", err)
	}
	if !sawPermissionDecision(events, "fs_write_file", "deny", "config", "dangerous_tools_disabled") {
		t.Fatalf("deny permission decision missing: %#v", events)
	}
	result, ok := firstToolResult(events)
	if !ok || !result.IsError || result.ErrorCode != "permission_denied" || !strings.Contains(result.Content, "tool disabled") {
		t.Fatalf("denied tool result = %#v ok=%v", result, ok)
	}
	if !sawEvent(events, protocol.EventToolCallFailed) {
		t.Fatalf("tool.call_failed missing: %#v", events)
	}
	progress := toolProgressEvents(events, "call_write")
	if len(progress) == 0 || progress[len(progress)-1].Phase != toolPhaseFinalize || progress[len(progress)-1].Status != protocol.StepStatusFailed {
		t.Fatalf("denied tool progress = %#v", progress)
	}
}

func TestRunMessagesPlanModeFiltersSpecsAndDeniesDangerousTool(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 2
	cfg.AutoApproveDangerous = true
	cfg.AccessMode = config.AccessModePlan
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"blocked"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "plan only"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	if hasToolSpec(prov.requests[0].Tools, "fs_write_file") || hasToolSpec(prov.requests[0].Tools, "shell_exec") {
		t.Fatalf("plan mode provider tools include dangerous specs: %#v", prov.requests[0].Tools)
	}
	if !hasToolSpec(prov.requests[0].Tools, "fs_read_file") || !hasToolSpec(prov.requests[0].Tools, "todo_write") {
		t.Fatalf("plan mode provider tools missing read/plan specs: %#v", prov.requests[0].Tools)
	}
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("plan-mode denied tool should not write file, stat err=%v", err)
	}
	if !sawPermissionDecision(events, "fs_write_file", "deny", "access_mode", "plan_mode_read_only") {
		t.Fatalf("plan-mode deny permission decision missing: %#v", events)
	}
	started, ok := firstModelCallEvent(events, protocol.EventModelCallStarted)
	if !ok || started.AccessMode != config.AccessModePlan || started.DangerousPermissionMode != "plan_mode_read_only" {
		t.Fatalf("model call access metadata = %#v ok=%v", started, ok)
	}
}

func TestRunMessagesExecutesParallelSafeToolsConcurrentlyAndPreservesOrder(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	registry := tools.NewRegistry(cfg)
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_a",
			Description: "Wait for slow_b.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(startedA)
			select {
			case <-startedB:
				return tools.Result{Content: "A"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_b",
			Description: "Wait for slow_a.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(startedB)
			select {
			case <-startedA:
				return tools.Result{Content: "B"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "slow_a", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_b", ToolName: "slow_b", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var events []protocol.Event
	next, err := a.RunMessages(ctx, []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	var toolMessages []protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && (msg.Name == "slow_a" || msg.Name == "slow_b") {
			toolMessages = append(toolMessages, msg)
		}
	}
	if len(toolMessages) != 2 {
		t.Fatalf("tool messages = %#v", toolMessages)
	}
	if toolMessages[0].Name != "slow_a" || toolMessages[0].Content != "A" ||
		toolMessages[1].Name != "slow_b" || toolMessages[1].Content != "B" {
		t.Fatalf("tool message order/content = %#v", toolMessages)
	}
	batchStarted, ok := firstStepEvent(events, protocol.EventStepStarted, protocol.StepKindToolBatch)
	if !ok || !batchStarted.Parallel || batchStarted.BatchSize != 2 || batchStarted.ParallelLimit != 2 || batchStarted.BatchID == "" {
		t.Fatalf("parallel batch step started = %#v ok=%v", batchStarted, ok)
	}
	batchCompleted, ok := firstStepEvent(events, protocol.EventStepCompleted, protocol.StepKindToolBatch)
	if !ok || batchCompleted.StepID != batchStarted.StepID || batchCompleted.Status != protocol.StepStatusCompleted {
		t.Fatalf("parallel batch step completed = %#v ok=%v", batchCompleted, ok)
	}
	var parallelToolStarts int
	for _, event := range events {
		step, ok := stepEvent(event, protocol.EventStepStarted)
		if ok && step.Kind == protocol.StepKindToolCall && step.BatchID == batchStarted.BatchID && step.Parallel {
			if step.Metadata["parallel_policy"] != tools.ParallelPolicyReadOnly ||
				step.Metadata["parallel_decision"] != "parallel_batch" ||
				step.Metadata["parallel_safe"] != true ||
				step.Metadata["idempotent"] != true ||
				step.Metadata["requires_exclusive_workspace"] != false ||
				step.Metadata["cancellable"] != true ||
				step.Metadata["risk"] != string(protocol.RiskReadOnly) ||
				step.Metadata["attempt_id"] == "" ||
				step.Metadata["permission_decision"] != "allow" {
				t.Fatalf("parallel tool metadata = %#v", step.Metadata)
			}
			parallelToolStarts++
		}
	}
	if parallelToolStarts != 2 {
		t.Fatalf("parallel tool step starts = %d; events=%#v", parallelToolStarts, events)
	}
}

func TestRunMessagesParallelBatchCompletesOutOfOrderWithCallIDs(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	registry := tools.NewRegistry(cfg)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_first",
			Description: "Slow read-only test tool.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			time.Sleep(50 * time.Millisecond)
			return tools.Result{Content: "alpha"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "fast_second",
			Description: "Fast read-only test tool.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: "beta"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call-a", ToolName: "slow_first", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call-b", ToolName: "fast_second", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	var completed []string
	for _, event := range events {
		step, ok := stepEvent(event, protocol.EventStepCompleted)
		if ok && step.Kind == protocol.StepKindToolCall {
			completed = append(completed, step.ToolCallID)
		}
	}
	if len(completed) != 2 || completed[0] != "call-b" || completed[1] != "call-a" {
		t.Fatalf("tool completion call_id order = %#v; events=%#v", completed, events)
	}
	var toolMessages []protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && (msg.ToolCallID == "call-a" || msg.ToolCallID == "call-b") {
			toolMessages = append(toolMessages, msg)
		}
	}
	if len(toolMessages) != 2 ||
		toolMessages[0].ToolCallID != "call-a" || toolMessages[0].Content != "alpha" ||
		toolMessages[1].ToolCallID != "call-b" || toolMessages[1].Content != "beta" {
		t.Fatalf("tool message order/content = %#v", toolMessages)
	}
}

func TestRunMessagesEmitsConfiguredHookEvents(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.HooksEnabled = true
	cfg.Hooks = []config.Hook{
		testHook("session_start", "session_start"),
		testHook("before_tool", "before_tool"),
		testHook("after_tool", "after_tool"),
		testHook("session_done", "session_done"),
	}
	registry := tools.NewRegistry(cfg)
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call-hook", ToolName: "time_now", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tool"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hookEvent := range []string{"session_start", "before_tool", "after_tool", "session_done"} {
		if !sawHookFinished(events, hookEvent) {
			t.Fatalf("missing hook %s in events %#v", hookEvent, events)
		}
	}
	if !sawHookToolPayload(events, "before_tool", "call-hook", "time_now") ||
		!sawHookToolPayload(events, "after_tool", "call-hook", "time_now") {
		t.Fatalf("tool hook payload missing call/tool ids: %#v", events)
	}
}

func TestRunMessagesEmitsProviderRetryHook(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	cfg.HooksEnabled = true
	cfg.Hooks = []config.Hook{testHook("provider_retry", "provider_retry")}
	registry := tools.NewRegistry(cfg)
	prov := &scriptedProvider{steps: [][]provider.Event{{
		{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
			RequestID:         "req-1",
			ProviderID:        "deepseek",
			ModelID:           "deepseek-v4-flash",
			ProviderRequestID: "provider-req-2",
			Attempts:          2,
			Retries:           1,
			StatusCode:        200,
		}},
		{Kind: provider.EventContent, Text: "finished"},
		{Kind: provider.EventDone},
	}}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "say hi"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawHookFinished(events, "provider_retry") {
		t.Fatalf("missing provider_retry hook: %#v", events)
	}
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] == "provider_retry" && data["request_id"] == "req-1" && data["retries"] == float64(1) {
			return
		}
	}
	t.Fatalf("provider_retry payload missing request metadata: %#v", events)
}

func TestRunMessagesEmitsMCPStatusChangeHookSnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 1
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:    "remote",
		URL:     "https://example.com/mcp",
		Enabled: true,
	}}
	cfg.HooksEnabled = true
	cfg.Hooks = []config.Hook{testHook("mcp", "mcp_status_change")}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	a := New(cfg, &captureProvider{}, registry)
	var events []protocol.Event
	_, err = a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "say hi"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] != "mcp_status_change" {
			continue
		}
		if data["server_name"] != "remote" ||
			data["transport"] != "streamable-http" ||
			data["state"] != "unsupported" ||
			data["connected"] != false {
			t.Fatalf("mcp status hook payload = %#v", data)
		}
		payload, _ := data["payload"].(map[string]any)
		if payload["phase"] != "snapshot" || payload["unsupported_reason"] == "" {
			t.Fatalf("mcp status hook nested payload = %#v", payload)
		}
		return
	}
	t.Fatalf("missing mcp_status_change hook: %#v", events)
}

func testHook(name, event string) config.Hook {
	return config.Hook{
		Name:           name,
		Event:          event,
		Command:        "sh",
		Args:           []string{"-c", "cat >/dev/null"},
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
		Enabled:        true,
	}
}

func TestRunMessagesExclusiveToolBreaksParallelBatches(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	cfg.AutoApproveDangerous = true
	registry := tools.NewRegistry(cfg)
	for _, name := range []string{"read_a", "read_b", "read_c", "read_d"} {
		name := name
		if err := registry.Register(tools.Tool{
			Spec: protocol.ToolSpec{
				Name:        name,
				Description: "Read-only test tool.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Risk:        protocol.RiskReadOnly,
			},
			Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: name}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "write_x",
			Description: "Exclusive write test tool.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: "write"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "read_a", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_b", ToolName: "read_b", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 2, ToolID: "call_w", ToolName: "write_x", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 3, ToolID: "call_c", ToolName: "read_c", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 4, ToolID: "call_d", ToolName: "read_d", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "mixed tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	var parallelBatches int
	var sawExclusiveWrite bool
	for _, event := range events {
		if step, ok := stepEvent(event, protocol.EventStepStarted); ok {
			if step.Kind == protocol.StepKindToolBatch && step.Parallel && step.BatchSize == 2 {
				parallelBatches++
			}
			if step.Kind == protocol.StepKindToolCall && step.Name == "write_x" {
				sawExclusiveWrite = step.Parallel == false &&
					step.Metadata["parallel_policy"] == tools.ParallelPolicyExclusiveWorkspace &&
					step.Metadata["parallel_decision"] == "serial_policy_"+tools.ParallelPolicyExclusiveWorkspace &&
					step.Metadata["requires_exclusive_workspace"] == true
			}
		}
	}
	if parallelBatches != 2 || !sawExclusiveWrite {
		t.Fatalf("parallelBatches=%d sawExclusiveWrite=%v events=%#v", parallelBatches, sawExclusiveWrite, events)
	}
}

func TestRunMessagesRateLimitsNetworkParallelBatch(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 5
	registry := tools.NewRegistry(cfg)
	var active int32
	var maxActive int32
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("web_like_%d", i)
		if err := registry.Register(tools.Tool{
			Spec: protocol.ToolSpec{
				Name:        name,
				Description: "Rate-limited network test tool.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Risk:        protocol.RiskNetwork,
			},
			Parallel: tools.ParallelMetadata{
				Policy:         tools.ParallelPolicyNetworkRateLimited,
				Idempotent:     true,
				RateLimitKey:   "webtest",
				Cancellable:    true,
				MaxConcurrency: 2,
			},
			Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
				now := atomic.AddInt32(&active, 1)
				for {
					seen := atomic.LoadInt32(&maxActive)
					if now <= seen || atomic.CompareAndSwapInt32(&maxActive, seen, now) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return tools.Result{Content: "ok"}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_0", ToolName: "web_like_0", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_1", ToolName: "web_like_1", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 2, ToolID: "call_2", ToolName: "web_like_2", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 3, ToolID: "call_3", ToolName: "web_like_3", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 4, ToolID: "call_4", ToolName: "web_like_4", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "network batch"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&maxActive); got > 2 {
		t.Fatalf("network tools exceeded bucket concurrency: maxActive=%d events=%#v", got, events)
	}
	batchStarted, ok := firstStepEvent(events, protocol.EventStepStarted, protocol.StepKindToolBatch)
	if !ok || batchStarted.ParallelLimit != 5 || batchStarted.BatchSize != 5 {
		t.Fatalf("batch started = %#v ok=%v", batchStarted, ok)
	}
	var sawRateLimitedTool bool
	for _, event := range events {
		step, ok := stepEvent(event, protocol.EventStepStarted)
		if ok && step.Kind == protocol.StepKindToolCall && step.Name == "web_like_0" {
			sawRateLimitedTool = step.Metadata["rate_limit_key"] == "webtest" &&
				step.Metadata["max_concurrency"] == float64(2) &&
				step.Metadata["parallel_policy"] == tools.ParallelPolicyNetworkRateLimited
		}
	}
	if !sawRateLimitedTool {
		t.Fatalf("rate-limited tool metadata missing: %#v", events)
	}
}

func TestRunMessagesRecordsAbortWhenActiveToolIsCanceled(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	started := make(chan struct{})
	registry := tools.NewRegistry(cfg)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "wait_for_cancel",
			Description: "Wait until the run context is canceled.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(started)
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &cancelAfterToolProvider{}
	a := New(cfg, prov, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []protocol.Event
	done := make(chan error, 1)
	go func() {
		_, err := a.RunMessages(ctx, []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "cancel tool"},
		}, func(event protocol.Event) {
			events = append(events, event)
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	assertAgentLifecycleValid(t, events)
	var aborted protocol.ToolResult
	var sawAborted bool
	for _, event := range events {
		if event.Type != protocol.EventToolCallAborted {
			continue
		}
		bytes, _ := json.Marshal(event.Data)
		if json.Unmarshal(bytes, &aborted) == nil {
			sawAborted = true
			break
		}
	}
	if !sawAborted || aborted.CallID != "call_cancel" || aborted.ErrorCode != "tool_aborted" || aborted.Metadata["attempt_id"] == "" {
		t.Fatalf("aborted result = %#v saw=%v events=%#v", aborted, sawAborted, events)
	}
	var sawCancelProgress bool
	for _, progress := range toolProgressEvents(events, "call_cancel") {
		if progress.Phase == toolPhaseCancelAbort && progress.Status == toolProgressStatusAborted {
			sawCancelProgress = true
			break
		}
	}
	if !sawCancelProgress {
		t.Fatalf("cancel progress missing: %#v", toolProgressEvents(events, "call_cancel"))
	}
}

func TestRunMessagesModelStreamCancellationIsLifecycleValid(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	prov := &cancelDuringModelProvider{started: make(chan struct{})}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []protocol.Event
	done := make(chan error, 1)
	go func() {
		_, err := a.RunMessages(ctx, []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "cancel model"},
		}, func(event protocol.Event) {
			events = append(events, event)
		})
		done <- err
	}()
	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("model stream did not start")
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	assertAgentLifecycleValid(t, events)
	finished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		finished["status"] != protocol.StepStatusFailed ||
		!strings.Contains(fmt.Sprint(finished["error"]), context.Canceled.Error()) {
		t.Fatalf("model.call_finished = %#v ok=%v", finished, ok)
	}
	if !sawEvent(events, protocol.EventRunFailed) {
		t.Fatalf("run.failed missing after model cancellation: %#v", events)
	}
}

func TestRunMessagesStoresLargeToolOutputAndSendsPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxToolOutputBytes = 512
	registry := tools.NewRegistry(cfg)
	fullOutput := strings.Repeat("large-output-", 80)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "big_output",
			Description: "Return a large output.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: fullOutput}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_big", ToolName: "big_output", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run big tool"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := firstToolResult(events)
	if !ok || !result.Truncated || result.OutputRef == "" {
		t.Fatalf("tool result = %#v ok=%v", result, ok)
	}
	if len(result.Content) > cfg.MaxToolOutputBytes {
		t.Fatalf("tool result exceeded inline budget: %d > %d", len(result.Content), cfg.MaxToolOutputBytes)
	}
	if strings.Contains(result.Content, fullOutput) || !strings.Contains(result.Content, "full tool output saved as plaintext") {
		t.Fatalf("result content should be preview with saved-output note: %q", result.Content)
	}
	bytes, err := os.ReadFile(result.OutputRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != fullOutput {
		t.Fatalf("stored output mismatch")
	}
	if !strings.HasPrefix(result.OutputRef, filepath.Join(home, "tool-output")) {
		t.Fatalf("output ref = %q, want under billy home %q", result.OutputRef, home)
	}
	assertMode(t, filepath.Join(home, "tool-output"), 0o700)
	assertMode(t, filepath.Dir(result.OutputRef), 0o700)
	assertMode(t, result.OutputRef, 0o600)
	if result.Metadata["output_ref_plaintext"] != true ||
		result.Metadata["output_ref_permissions"] != "0600" ||
		result.Metadata["output_ref_id"] == "" ||
		result.Metadata["output_ref_bytes"] == nil ||
		result.Metadata["output_ref_sha256"] == "" {
		t.Fatalf("metadata should make plaintext persistence explicit: %#v", result.Metadata)
	}
	refEvent, ok := firstEventData(events, protocol.EventToolOutputRefCreated)
	if !ok ||
		refEvent["output_ref"] != result.OutputRef ||
		refEvent["output_ref_id"] != result.Metadata["output_ref_id"] ||
		refEvent["output_ref_sha256"] != result.Metadata["output_ref_sha256"] ||
		refEvent["output_ref_permissions"] != "0600" ||
		refEvent["output_ref_plaintext"] != true ||
		refEvent["output_ref_bytes"] == nil {
		t.Fatalf("output ref event = %#v metadata=%#v ok=%v", refEvent, result.Metadata, ok)
	}
	var toolMessage protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && msg.Name == "big_output" {
			toolMessage = msg
			break
		}
	}
	if toolMessage.Content == "" || strings.Contains(toolMessage.Content, fullOutput) || !strings.Contains(toolMessage.Content, result.OutputRef) {
		t.Fatalf("tool message should contain preview and output ref, got %#v", toolMessage)
	}
}

func TestRunMessagesEnforcesBudgetForAlreadyTruncatedToolOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxToolOutputBytes = 384
	registry := tools.NewRegistry(cfg)
	fullOutput := strings.Repeat("already-truncated-output-", 80)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "pretruncated_output",
			Description: "Return a large pre-truncated output.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: fullOutput, Truncated: true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_pre", ToolName: "pretruncated_output", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run large pre-truncated tool"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	result, ok := firstToolResult(events)
	if !ok || !result.Truncated || result.OutputRef == "" {
		t.Fatalf("tool result = %#v ok=%v", result, ok)
	}
	if len(result.Content) > cfg.MaxToolOutputBytes {
		t.Fatalf("tool result exceeded inline budget: %d > %d", len(result.Content), cfg.MaxToolOutputBytes)
	}
	if strings.Contains(result.Content, fullOutput) {
		t.Fatalf("pre-truncated tool output bypassed agent budget: %q", result.Content)
	}
	if result.Metadata["inline_budget_enforced"] != true || result.Metadata["inline_budget_bytes"] != cfg.MaxToolOutputBytes {
		t.Fatalf("budget metadata = %#v", result.Metadata)
	}
}

func TestRunMessagesExecutesMCPToolAndContinuesLoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 3
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestAgentFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_AGENT_MCP_HELPER": "1"},
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		Enabled:        true,
	}}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_mcp", ToolName: "mcp_call", ArgsDelta: `{"name":"mcp__fake__echo","arguments":{"text":"mcp ok"}}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "use mcp"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
	if hasToolSpec(prov.lastTools, "mcp__fake__echo") || !hasToolSpec(prov.lastTools, "mcp_call") {
		t.Fatalf("provider saw wrong MCP tools: %#v", prov.lastTools)
	}
	if !sawToolStarted(events, "mcp_call") {
		t.Fatalf("MCP tool start event missing: %#v", events)
	}
	var sawToolMessage bool
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && msg.Name == "mcp_call" && msg.Content == "mcp ok" {
			sawToolMessage = true
		}
	}
	if !sawToolMessage {
		t.Fatalf("MCP tool result not in messages: %#v", next)
	}
	if !hasMCPInstructions(next) {
		t.Fatalf("MCP instructions not preserved in messages: %#v", next)
	}
	injected := a.withMCPInstructions([]protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleSystem, Content: compactionMarker + "\nold summary"},
		{Role: protocol.RoleUser, Content: "continue"},
	})
	if len(injected) != 4 ||
		!strings.HasPrefix(injected[1].Content, "# MCP server instructions") ||
		!strings.HasPrefix(injected[2].Content, compactionMarker) {
		t.Fatalf("MCP instructions should be inserted into protected prefix before prior summary: %#v", injected)
	}
}
