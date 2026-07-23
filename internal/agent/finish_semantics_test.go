package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunMessagesClassifiesNoToolFinish(t *testing.T) {
	tests := []struct {
		name       string
		finish     provider.Finish
		omitDone   bool
		wantKind   provider.FinishKind
		wantRaw    string
		wantLegacy bool
		wantOK     bool
	}{
		{name: "natural", finish: provider.Finish{Kind: provider.FinishNatural, RawReason: "stop"}, wantKind: provider.FinishNatural, wantRaw: "stop", wantOK: true},
		{name: "legacy zero", wantKind: provider.FinishNatural, wantRaw: "legacy_zero", wantLegacy: true, wantOK: true},
		{name: "output limit", finish: provider.Finish{Kind: provider.FinishOutputLimit, RawReason: "length"}, wantKind: provider.FinishOutputLimit, wantRaw: "length"},
		{name: "context limit", finish: provider.Finish{Kind: provider.FinishContextLimit, RawReason: "context_window_exceeded"}, wantKind: provider.FinishContextLimit, wantRaw: "context_window_exceeded"},
		{name: "pause", finish: provider.Finish{Kind: provider.FinishPause, RawReason: "pause_turn"}, wantKind: provider.FinishPause, wantRaw: "pause_turn"},
		{name: "refusal", finish: provider.Finish{Kind: provider.FinishRefusal, RawReason: "refusal"}, wantKind: provider.FinishRefusal, wantRaw: "refusal"},
		{name: "content filter", finish: provider.Finish{Kind: provider.FinishContentFilter, RawReason: "content_filter"}, wantKind: provider.FinishContentFilter, wantRaw: "content_filter"},
		{name: "resource limit", finish: provider.Finish{Kind: provider.FinishResourceLimit, RawReason: "resource_exhausted"}, wantKind: provider.FinishResourceLimit, wantRaw: "resource_exhausted"},
		{name: "unknown", finish: provider.Finish{Kind: provider.FinishUnknown, RawReason: "vendor_new_reason"}, wantKind: provider.FinishUnknown, wantRaw: "vendor_new_reason"},
		{name: "missing done", omitDone: true, wantKind: provider.FinishUnknown, wantRaw: "stream_closed_without_done"},
		{name: "malformed nonzero finish", finish: provider.Finish{RawReason: "reason_without_kind"}, wantKind: provider.FinishUnknown, wantRaw: "reason_without_kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Provider = "mock"
			cfg.Model = "mock"
			stream := []provider.Event{{Kind: provider.EventContent, Text: "candidate"}}
			if !tt.omitDone {
				stream = append(stream, provider.Event{Kind: provider.EventDone, Finish: tt.finish})
			}
			a := New(cfg, &scriptedProvider{steps: [][]provider.Event{stream}}, tools.NewRegistry(cfg))
			var emitted []protocol.Event
			messages, err := a.RunMessages(context.Background(), finishTestMessages(), func(event protocol.Event) {
				emitted = append(emitted, event)
			})

			if tt.wantOK {
				if err != nil {
					t.Fatalf("RunMessages() error = %v", err)
				}
				if got := lastAssistantContent(messages); got != "candidate" {
					t.Fatalf("assistant content = %q", got)
				}
				if !sawEvent(emitted, protocol.EventRunCompleted) {
					t.Fatal("successful finish did not complete run")
				}
			} else {
				if err == nil {
					t.Fatal("RunMessages() error = nil")
				}
				finish, ok := provider.FinishFromError(err)
				if !ok {
					t.Fatalf("error %T is not classifiable FinishError: %v", err, err)
				}
				if finish.Kind != tt.wantKind || finish.RawReason != tt.wantRaw {
					t.Fatalf("finish from error = %#v, want kind=%q raw=%q", finish, tt.wantKind, tt.wantRaw)
				}
				if sawEvent(emitted, protocol.EventRunCompleted) || !sawEvent(emitted, protocol.EventRunFailed) {
					t.Fatalf("terminal lifecycle is not failed: %#v", emitted)
				}
				if got := lastAssistantContent(messages); got != "" {
					t.Fatalf("failed candidate was appended to transcript: %q", got)
				}
			}

			finished, ok := firstModelCallEvent(emitted, protocol.EventModelCallFinished)
			if !ok {
				t.Fatal("model.call.finished telemetry missing")
			}
			if finished.FinishKind != string(tt.wantKind) || finished.FinishRawReason != tt.wantRaw || finished.FinishLegacy != tt.wantLegacy {
				t.Fatalf("finish telemetry = kind=%q raw=%q legacy=%t", finished.FinishKind, finished.FinishRawReason, finished.FinishLegacy)
			}
			wantStatus := protocol.StepStatusFailed
			if tt.wantOK {
				wantStatus = protocol.StepStatusCompleted
			}
			if finished.Status != wantStatus {
				t.Fatalf("model finish status = %q, want %q", finished.Status, wantStatus)
			}
		})
	}
}

func TestRunMessagesRejectsLegacyZeroFromRealProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "qwen"
	cfg.Model = "qwen3.8-max-preview"
	prov := rawFinishProvider{events: []provider.Event{
		{Kind: provider.EventContent, Text: "candidate"},
		{Kind: provider.EventDone},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var emitted []protocol.Event
	messages, err := a.RunMessages(context.Background(), finishTestMessages(), func(event protocol.Event) {
		emitted = append(emitted, event)
	})
	finish, ok := provider.FinishFromError(err)
	if !ok || finish.Kind != provider.FinishUnknown || finish.RawReason != "legacy_zero_not_allowed" {
		t.Fatalf("error finish = %#v, classifiable=%t, err=%v", finish, ok, err)
	}
	if got := lastAssistantContent(messages); got != "" {
		t.Fatalf("failed candidate was appended to transcript: %q", got)
	}
	finished, ok := firstModelCallEvent(emitted, protocol.EventModelCallFinished)
	if !ok || finished.Status != protocol.StepStatusFailed || finished.FinishKind != string(provider.FinishUnknown) || finished.FinishLegacy {
		t.Fatalf("model finish telemetry = %#v, present=%t", finished, ok)
	}
}

func TestRunMessagesNormalizesRawFinishReasonBeforeTelemetryAndClassification(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "qwen"
	cfg.Model = "qwen3.8-max-preview"
	raw := " \x00length\n" + strings.Repeat("x", 200) + "\t "
	prov := &scriptedProvider{steps: [][]provider.Event{{
		{Kind: provider.EventContent, Text: "truncated"},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishOutputLimit, RawReason: raw}},
	}}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var emitted []protocol.Event
	_, err := a.RunMessages(context.Background(), finishTestMessages(), func(event protocol.Event) {
		emitted = append(emitted, event)
	})
	finish, ok := provider.FinishFromError(err)
	if !ok || finish.Kind != provider.FinishOutputLimit {
		t.Fatalf("error finish = %#v, classifiable=%t, err=%v", finish, ok, err)
	}
	if strings.ContainsAny(finish.RawReason, "\x00\n\t") || len([]rune(finish.RawReason)) != 128 {
		t.Fatalf("error raw reason was not normalized: %q", finish.RawReason)
	}
	finished, ok := firstModelCallEvent(emitted, protocol.EventModelCallFinished)
	if !ok || finished.FinishRawReason != finish.RawReason {
		t.Fatalf("telemetry finish = %#v, error finish=%#v", finished, finish)
	}
}

func TestRunMessagesRejectsFinishToolCallContradictions(t *testing.T) {
	tests := []struct {
		name   string
		stream []provider.Event
		kind   provider.FinishKind
		calls  int
	}{
		{
			name: "natural with parsed tool call",
			stream: []provider.Event{
				{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{}`},
				{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural, RawReason: "stop"}},
			},
			kind:  provider.FinishNatural,
			calls: 1,
		},
		{
			name: "tool finish without parsed tool call",
			stream: []provider.Event{
				{Kind: provider.EventContent, Text: "no call"},
				{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls, RawReason: "tool_calls"}},
			},
			kind: provider.FinishToolCalls,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Provider = "mock"
			cfg.Model = "mock"
			a := New(cfg, &scriptedProvider{steps: [][]provider.Event{tt.stream}}, tools.NewRegistry(cfg))
			var emitted []protocol.Event
			_, err := a.RunMessages(context.Background(), finishTestMessages(), func(event protocol.Event) {
				emitted = append(emitted, event)
			})
			var mismatch *ModelFinishMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("error = %T %v, want ModelFinishMismatchError", err, err)
			}
			if mismatch.Finish.Kind != tt.kind || mismatch.ToolCallCount != tt.calls {
				t.Fatalf("mismatch = %#v", mismatch)
			}
			finished, ok := firstModelCallEvent(emitted, protocol.EventModelCallFinished)
			if !ok || finished.Status != protocol.StepStatusFailed || finished.FinishKind != string(tt.kind) {
				t.Fatalf("failed finish telemetry = %#v, present=%t", finished, ok)
			}
			if sawEvent(emitted, protocol.EventToolCallStarted) || sawEvent(emitted, protocol.EventRunCompleted) {
				t.Fatalf("contradictory finish progressed execution: %#v", emitted)
			}
		})
	}
}

func TestRunMessagesAcceptsExplicitAndLegacyToolFinish(t *testing.T) {
	for _, tt := range []struct {
		name       string
		finish     provider.Finish
		wantLegacy bool
		wantRaw    string
	}{
		{name: "explicit", finish: provider.Finish{Kind: provider.FinishToolCalls, RawReason: "tool_calls"}, wantRaw: "tool_calls"},
		{name: "legacy zero", wantLegacy: true, wantRaw: "legacy_zero"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Provider = "mock"
			cfg.Model = "mock"
			cfg.MaxToolRounds = 2
			prov := &scriptedProvider{steps: [][]provider.Event{
				{
					{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{}`},
					{Kind: provider.EventDone, Finish: tt.finish},
				},
				{
					{Kind: provider.EventContent, Text: "done"},
					{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural, RawReason: "stop"}},
				},
			}}
			a := New(cfg, prov, tools.NewRegistry(cfg))
			var emitted []protocol.Event
			messages, err := a.RunMessages(context.Background(), finishTestMessages(), func(event protocol.Event) {
				emitted = append(emitted, event)
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := lastAssistantContent(messages); got != "done" {
				t.Fatalf("assistant content = %q", got)
			}
			finished := modelCallFinishEvents(emitted)
			if len(finished) != 2 {
				t.Fatalf("model finish events = %#v", finished)
			}
			if finished[0].FinishKind != string(provider.FinishToolCalls) || finished[0].FinishRawReason != tt.wantRaw || finished[0].FinishLegacy != tt.wantLegacy {
				t.Fatalf("first finish telemetry = %#v", finished[0])
			}
			if finished[1].FinishKind != string(provider.FinishNatural) || finished[1].FinishLegacy {
				t.Fatalf("second finish telemetry = %#v", finished[1])
			}
		})
	}
}

func TestRunMessagesRejectsEventsAfterDone(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov := &scriptedProvider{steps: [][]provider.Event{{
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
		{Kind: provider.EventContent, Text: "late"},
	}}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	_, err := a.RunMessages(context.Background(), finishTestMessages(), func(protocol.Event) {})
	if err == nil {
		t.Fatal("RunMessages() error = nil")
	}
}

func finishTestMessages() []protocol.Message {
	return []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "test finish"},
	}
}

func modelCallFinishEvents(events []protocol.Event) []protocol.ModelCallEvent {
	finished := make([]protocol.ModelCallEvent, 0)
	for _, event := range events {
		if event.Type != protocol.EventModelCallFinished {
			continue
		}
		if data, ok := event.Data.(protocol.ModelCallEvent); ok {
			finished = append(finished, data)
		}
	}
	return finished
}

type rawFinishProvider struct {
	events []provider.Event
}

func (p rawFinishProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, len(p.events))
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		for _, event := range p.events {
			select {
			case events <- event:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
	}()
	return events, errs
}
