package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/testkit/fakeprovider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestFakeProviderRegressionSuiteExercisesRecoveryRetryAndPairing(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 4
	prov := fakeprovider.New(
		fakeprovider.Step{
			Delay: time.Millisecond,
			Events: []provider.Event{
				{Kind: provider.EventReasoning, Text: "Need a tool. "},
				{Kind: provider.EventContent, Text: "Checking. "},
				{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_bad", ToolName: "time_now", ArgsDelta: `{bad`},
				{Kind: provider.EventDone},
			},
		},
		fakeprovider.Step{Events: []provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_partial", ToolName: "time_now", ArgsDelta: `{`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ArgsDelta: `}`},
			{Kind: provider.EventDone},
		}},
		fakeprovider.Step{Events: []provider.Event{
			{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
				RequestID:         "turn-003:model-call-001",
				ProviderID:        "mock",
				ModelID:           "mock",
				ProviderRequestID: "fake-retry-request",
				Attempts:          2,
				Retries:           1,
				StatusCode:        200,
			}},
			{Kind: provider.EventContent, Text: "recovered"},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 33, OutputTokens: 7, CacheHitTokens: 10, CacheMissTokens: 23}},
			{Kind: provider.EventDone},
		}},
	)
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	messages, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "exercise fake provider"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	if prov.Calls() != 3 {
		t.Fatalf("provider calls = %d", prov.Calls())
	}
	for i, req := range prov.Requests() {
		if err := validateTranscriptPairing(req.Messages); err != nil {
			t.Fatalf("request %d transcript pairing: %v\nmessages=%#v", i+1, err, req.Messages)
		}
	}
	if !toolResultMatches(events, protocol.EventToolCallFailed, "call_bad", "invalid_json_args") {
		t.Fatalf("missing invalid-json tool failure: %#v", events)
	}
	if !toolResultMatches(events, protocol.EventToolCallFinished, "call_partial", "") {
		t.Fatalf("missing recovered partial-json tool finish: %#v", events)
	}
	if sawEvent(events, protocol.EventRunFailed) {
		t.Fatalf("run.failed should not be emitted after fake-provider recovery: %#v", events)
	}
	if !modelFinishedWithRetry(events, "fake-retry-request", 2, 1) {
		t.Fatalf("retry metadata missing from model finish: %#v", events)
	}
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "recovered") {
		t.Fatalf("final messages = %#v", messages)
	}
}

func TestFakeProviderCancellationEmitsTerminalFailureEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 1
	started := make(chan struct{})
	prov := fakeprovider.New(fakeprovider.Step{WaitForCancel: true, Started: started})
	a := New(cfg, prov, tools.NewRegistry(cfg))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []protocol.Event
	errCh := make(chan error, 1)
	go func() {
		_, err := a.RunMessages(ctx, []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "cancel"},
		}, func(event protocol.Event) {
			events = append(events, event)
		})
		errCh <- err
	}()
	<-started
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	assertAgentLifecycleValid(t, events)
	if !sawEvent(events, protocol.EventRunFailed) {
		t.Fatalf("missing run.failed after cancellation: %#v", events)
	}
	if !modelCallFailedWith(events, "context canceled") {
		t.Fatalf("missing failed model call after cancellation: %#v", events)
	}
	if prov.Calls() != 1 || len(prov.Requests()) != 1 {
		t.Fatalf("provider calls=%d requests=%#v", prov.Calls(), prov.Requests())
	}
	if err := validateTranscriptPairing(prov.Requests()[0].Messages); err != nil {
		t.Fatalf("cancellation request transcript pairing: %v", err)
	}
}

func toolResultMatches(events []protocol.Event, eventType protocol.EventType, callID, errorCode string) bool {
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		var result protocol.ToolResult
		body, _ := json.Marshal(event.Data)
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}
		if result.CallID != callID {
			continue
		}
		if errorCode == "" || result.ErrorCode == errorCode {
			return true
		}
	}
	return false
}

func modelFinishedWithRetry(events []protocol.Event, requestID string, attempts, retries int) bool {
	for _, event := range events {
		if event.Type != protocol.EventModelCallFinished {
			continue
		}
		data := eventDataMap(event)
		if fmt.Sprint(data["provider_request_id"]) == requestID &&
			fmt.Sprint(data["attempts"]) == fmt.Sprint(float64(attempts)) &&
			fmt.Sprint(data["retries"]) == fmt.Sprint(float64(retries)) {
			return true
		}
	}
	return false
}

func modelCallFailedWith(events []protocol.Event, text string) bool {
	for _, event := range events {
		if event.Type != protocol.EventModelCallFinished {
			continue
		}
		data := eventDataMap(event)
		if fmt.Sprint(data["status"]) == protocol.StepStatusFailed && strings.Contains(fmt.Sprint(data["error"]), text) {
			return true
		}
	}
	return false
}
