package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestBoundedExecutionAttestationPrecedesModelCall(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	settings := SettingsFromConfig(cfg)
	settings.Runtime.MaxToolCalls = 4
	settings.ExecutionContract = &protocol.ExecutionContractAttestation{
		ExecutionContract:       "bounded-isolated-plan-v1",
		ProviderMaxRetries:      0,
		ProviderFailoverEnabled: false,
		MaxToolCalls:            4,
	}
	a := NewFromSettings(settings, &captureProvider{}, tools.NewRegistry(cfg))

	var events []protocol.Event
	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleUser, Content: "hello"},
	}, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Type != protocol.EventRunStarted {
		t.Fatalf("first event = %#v, want run.started", events)
	}
	data := eventDataMap(events[0])
	if len(data) != 7 ||
		data["submission_id"] == "" ||
		data["run_id"] == "" ||
		data["status"] == "" ||
		data["execution_contract"] != "bounded-isolated-plan-v1" ||
		data["provider_max_retries"] != float64(0) ||
		data["provider_failover_enabled"] != false ||
		data["max_tool_calls"] != float64(4) {
		t.Fatalf("run.started attestation = %#v", data)
	}
	if modelIndex := firstEventIndex(events, protocol.EventModelCallStarted); modelIndex <= 0 {
		t.Fatalf("model.call_started index = %d, events=%#v", modelIndex, events)
	}
	finished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		finished["attempts"] != float64(1) ||
		finished["retries"] != float64(0) ||
		finished["access_mode"] != config.AccessModeBuild ||
		finished["status"] != protocol.StepStatusCompleted {
		t.Fatalf("model.call_finished bounded accounting = %#v ok=%v", finished, ok)
	}
}

func TestMaxToolCallsRejectsWholeBatchBeforeExecution(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 3
	var executions atomic.Int32
	registry := boundedTestRegistry(t, cfg, &executions)
	prov := &scriptedProvider{steps: [][]provider.Event{boundedToolBatch(
		"call-1",
		"call-2",
		"call-3",
	)}}
	settings := SettingsFromConfig(cfg)
	settings.Runtime.MaxToolCalls = 2
	a := NewFromSettings(settings, prov, registry)

	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err == nil || !strings.Contains(err.Error(), "requested batch of 3 after 0 executed (limit 2)") {
		t.Fatalf("err = %v", err)
	}
	if got := executions.Load(); got != 0 {
		t.Fatalf("executed tools = %d, want 0", got)
	}
	if firstEventIndex(events, protocol.EventToolCallStarted) >= 0 {
		t.Fatalf("tool execution started despite rejected batch: %#v", events)
	}
}

func TestMaxToolCallsIsCumulativeAndDoesNotPartiallyExecuteOverflowBatch(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 4
	var executions atomic.Int32
	registry := boundedTestRegistry(t, cfg, &executions)
	prov := &scriptedProvider{steps: [][]provider.Event{
		boundedToolBatch("call-1"),
		boundedToolBatch("call-2", "call-3"),
	}}
	settings := SettingsFromConfig(cfg)
	settings.Runtime.MaxToolCalls = 2
	a := NewFromSettings(settings, prov, registry)

	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(protocol.Event) {})
	if err == nil || !strings.Contains(err.Error(), "requested batch of 2 after 1 executed (limit 2)") {
		t.Fatalf("err = %v", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("executed tools = %d, want only the first batch", got)
	}
}

func TestMaxToolCallsAllowsExactLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 3
	var executions atomic.Int32
	registry := boundedTestRegistry(t, cfg, &executions)
	prov := &scriptedProvider{steps: [][]provider.Event{
		boundedToolBatch("call-1", "call-2"),
		{
			{Kind: provider.EventContent, Text: "done"},
			{Kind: provider.EventDone},
		},
	}}
	settings := SettingsFromConfig(cfg)
	settings.Runtime.MaxToolCalls = 2
	a := NewFromSettings(settings, prov, registry)

	if _, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(protocol.Event) {}); err != nil {
		t.Fatal(err)
	}
	if got := executions.Load(); got != 2 {
		t.Fatalf("executed tools = %d, want 2", got)
	}
}

func TestQwenModelCallProvenanceUsesWireEffectiveReasoning(t *testing.T) {
	for _, rawEffort := range []string{"high", "max"} {
		t.Run(rawEffort, func(t *testing.T) {
			cfg := config.BuiltIn()
			cfg.Provider = "qwen"
			cfg.Model = "qwen3.8-max-preview"
			cfg.Thinking = "disabled"
			cfg.ReasoningEffort = rawEffort
			a := NewFromSettings(SettingsFromConfig(cfg), &captureProvider{}, tools.NewRegistry(cfg))

			var events []protocol.Event
			if _, err := a.RunMessages(context.Background(), []protocol.Message{
				{Role: protocol.RoleUser, Content: "hello"},
			}, func(event protocol.Event) {
				events = append(events, event)
			}); err != nil {
				t.Fatal(err)
			}
			for _, eventType := range []protocol.EventType{
				protocol.EventModelCallStarted,
				protocol.EventModelCallFinished,
			} {
				event, ok := firstModelCallEvent(events, eventType)
				if !ok ||
					event.Reasoning != "xhigh" ||
					event.ReasoningMode != "enabled/xhigh" {
					t.Fatalf("%s provenance = %#v ok=%v", eventType, event, ok)
				}
			}
		})
	}
}

func TestQwenRetainsReasoningAcrossToolRoundButScrubsReturnedTranscript(t *testing.T) {
	cfg := config.BuiltIn()
	cfg.Provider = "qwen"
	cfg.Model = "qwen3.8-max-preview"
	cfg.StoreReasoningContent = false
	cfg.MaxToolRounds = 3
	var executions atomic.Int32
	registry := boundedTestRegistry(t, cfg, &executions)
	prov := &qwenToolLoopProvider{}
	a := NewFromSettings(SettingsFromConfig(cfg), prov, registry)

	messages, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "use the tool"},
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("Qwen requests = %d, want 2", len(prov.requests))
	}
	second := prov.requests[1]
	var historicalReasoning string
	for _, message := range second {
		if message.Role == protocol.RoleAssistant && len(message.ToolCalls) > 0 {
			historicalReasoning = message.ReasoningContent
			break
		}
	}
	if historicalReasoning != "verbatim historical reasoning" {
		t.Fatalf("second Qwen request lost reasoning_content: %#v", second)
	}
	for _, message := range messages {
		if message.ReasoningContent != "" {
			t.Fatalf("returned transcript persisted reasoning_content: %#v", messages)
		}
	}
	if executions.Load() != 1 {
		t.Fatalf("tool executions = %d, want 1", executions.Load())
	}
}

func boundedTestRegistry(t *testing.T, cfg config.Config, executions *atomic.Int32) *tools.Registry {
	t.Helper()
	registry := tools.NewRegistry(cfg)
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "bounded_test",
			Description: "Count bounded test executions.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
			executions.Add(1)
			return tools.Result{Content: "ok"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	return registry
}

func boundedToolBatch(ids ...string) []provider.Event {
	events := make([]provider.Event, 0, len(ids)+1)
	for i, id := range ids {
		events = append(events, provider.Event{
			Kind:      provider.EventToolCallDelta,
			ToolIndex: i,
			ToolID:    id,
			ToolName:  "bounded_test",
			ArgsDelta: `{}`,
		})
	}
	return append(events, provider.Event{Kind: provider.EventDone})
}

type qwenToolLoopProvider struct {
	requests [][]protocol.Message
	calls    int
}

func (p *qwenToolLoopProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.requests = append(p.requests, cloneProtocolMessages(req.Messages))
	p.calls++
	events := make(chan provider.Event, 5)
	errs := make(chan error)
	events <- provider.Event{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
		RequestID:  req.RequestID,
		ProviderID: "qwen",
		ModelID:    req.Model,
		Attempts:   1,
	}}
	if p.calls == 1 {
		events <- provider.Event{Kind: provider.EventReasoning, Text: "verbatim historical reasoning"}
		events <- provider.Event{
			Kind:      provider.EventToolCallDelta,
			ToolIndex: 0,
			ToolID:    "call-qwen-1",
			ToolName:  "bounded_test",
			ArgsDelta: `{}`,
		}
	} else {
		events <- provider.Event{Kind: provider.EventContent, Text: "done"}
	}
	events <- provider.Event{Kind: provider.EventDone}
	close(events)
	close(errs)
	return events, errs
}
