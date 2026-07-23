package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestNaturalFinishGuardAcceptsExactlyOneNaturalDone(t *testing.T) {
	t.Parallel()
	var guard NaturalFinishGuard
	for _, event := range []Event{
		{Kind: EventContent, Text: "summary"},
		{Kind: EventUsage, Usage: Usage{OutputTokens: 1}},
		{Kind: EventDone, Finish: Finish{Kind: FinishNatural, RawReason: "stop"}},
	} {
		if err := guard.Observe(event); err != nil {
			t.Fatalf("Observe(%#v): %v", event, err)
		}
	}
	if err := guard.Complete(); err != nil {
		t.Fatal(err)
	}
}

func TestNaturalFinishGuardRejectsUnsafeFinish(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		kind FinishKind
		raw  string
	}{
		{name: "legacy zero"},
		{name: "Qwen output limit", kind: FinishOutputLimit, raw: "length"},
		{name: "context limit", kind: FinishContextLimit, raw: "context_length_exceeded"},
		{name: "pause", kind: FinishPause, raw: "pause_turn"},
		{name: "refusal", kind: FinishRefusal, raw: "refusal"},
		{name: "content filter", kind: FinishContentFilter, raw: "content_filter"},
		{name: "resource limit", kind: FinishResourceLimit, raw: "insufficient_system_resource"},
		{name: "unknown", kind: FinishUnknown, raw: "[DONE]"},
		{name: "tool finish", kind: FinishToolCalls, raw: "tool_calls"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var guard NaturalFinishGuard
			err := guard.Observe(Event{Kind: EventDone, Finish: Finish{Kind: test.kind, RawReason: test.raw}})
			var finishErr *FinishError
			if !errors.As(err, &finishErr) {
				t.Fatalf("err = %T %v, want FinishError", err, err)
			}
			if test.kind == "" {
				if finishErr.Finish.Kind != FinishUnknown {
					t.Fatalf("zero finish classified as %#v", finishErr.Finish)
				}
			} else if finishErr.Finish.Kind != test.kind {
				t.Fatalf("finish = %#v, want kind %q", finishErr.Finish, test.kind)
			}
		})
	}
}

func TestNaturalFinishGuardRejectsMissingDuplicateToolsAndEventsAfterDone(t *testing.T) {
	t.Parallel()
	t.Run("missing done", func(t *testing.T) {
		var guard NaturalFinishGuard
		if err := guard.Observe(Event{Kind: EventContent, Text: "partial"}); err != nil {
			t.Fatal(err)
		}
		err := guard.Complete()
		finish, ok := FinishFromError(err)
		if !ok || finish.Kind != FinishUnknown || finish.RawReason != "stream_closed_without_done" {
			t.Fatalf("err = %T %v finish=%#v ok=%v", err, err, finish, ok)
		}
	})
	t.Run("duplicate done", func(t *testing.T) {
		var guard NaturalFinishGuard
		done := Event{Kind: EventDone, Finish: Finish{Kind: FinishNatural, RawReason: "stop"}}
		if err := guard.Observe(done); err != nil {
			t.Fatal(err)
		}
		if err := guard.Observe(done); err == nil || !strings.Contains(err.Error(), "multiple done") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("tool delta", func(t *testing.T) {
		var guard NaturalFinishGuard
		err := guard.Observe(Event{Kind: EventToolCallDelta, ToolName: "fs_read_file"})
		finish, ok := FinishFromError(err)
		if !ok || finish.Kind != FinishToolCalls {
			t.Fatalf("err = %T %v finish=%#v ok=%v", err, err, finish, ok)
		}
	})
	t.Run("event after done", func(t *testing.T) {
		var guard NaturalFinishGuard
		if err := guard.Observe(Event{Kind: EventDone, Finish: Finish{Kind: FinishNatural}}); err != nil {
			t.Fatal(err)
		}
		if err := guard.Observe(Event{Kind: EventUsage}); err == nil || !strings.Contains(err.Error(), "after done") {
			t.Fatalf("err = %v", err)
		}
	})
}
