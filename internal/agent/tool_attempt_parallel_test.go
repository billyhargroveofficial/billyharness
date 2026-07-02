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
	fullOutput := strings.Repeat("large-output-", 42_000)
	if len(fullOutput) < 500_000 {
		t.Fatalf("test fixture must exercise at least 500k chars, got %d", len(fullOutput))
	}
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
