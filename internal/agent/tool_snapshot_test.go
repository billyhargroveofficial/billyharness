package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunMessagesUsesTurnToolSnapshotForExecution(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 3
	registry := tools.NewRegistry(cfg)
	prov := &mutatingToolSnapshotProvider{registry: registry}
	a := New(cfg, prov, registry)

	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "call late tool"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d", len(prov.requests))
	}
	if hasToolSpec(prov.requests[0].Tools, "late_tool") {
		t.Fatalf("late tool leaked into first provider request: %#v", prov.requests[0].Tools)
	}
	if !hasToolSpec(prov.requests[1].Tools, "late_tool") {
		t.Fatalf("late tool should be visible on the next provider turn: %#v", prov.requests[1].Tools)
	}
	result, ok := firstToolResult(events)
	if !ok {
		t.Fatalf("tool result missing from events: %#v", events)
	}
	if result.Name != "late_tool" || !result.IsError || result.ErrorCode != "unknown_tool" || !strings.Contains(result.Content, "unknown tool late_tool") {
		t.Fatalf("late tool should fail against the frozen snapshot, got %#v", result)
	}
	assertAgentLifecycleValid(t, events)
}

type mutatingToolSnapshotProvider struct {
	registry *tools.Registry
	requests []provider.Request
	calls    int
}

func (p *mutatingToolSnapshotProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.calls++
	p.requests = append(p.requests, req)
	call := p.calls
	events := make(chan provider.Event, 4)
	errs := make(chan error, 1)
	go func() {
		var streamErr error
		defer func() {
			if streamErr != nil {
				errs <- streamErr
			}
			close(errs)
		}()
		defer close(events)
		if call == 1 {
			streamErr = p.registry.Register(tools.Tool{
				Spec: protocol.ToolSpec{
					Name:        "late_tool",
					Description: "Registered after the provider request starts.",
					Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
					Risk:        protocol.RiskReadOnly,
				},
				Handler: func(context.Context, json.RawMessage) (tools.Result, error) {
					return tools.Result{Content: "late tool executed"}, nil
				},
			})
			if streamErr != nil {
				return
			}
			events <- provider.Event{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_late", ToolName: "late_tool", ArgsDelta: `{}`}
			events <- provider.Event{Kind: provider.EventDone}
			return
		}
		events <- provider.Event{Kind: provider.EventContent, Text: "done"}
		events <- provider.Event{Kind: provider.EventDone}
	}()
	return events, errs
}
