package projector

import (
	"fmt"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func BenchmarkProjectorApplyReplay(b *testing.B) {
	events := benchmarkProjectorEvents(500)
	b.ReportAllocs()
	b.ReportMetric(float64(len(events)), "events_per_replay")
	for i := 0; i < b.N; i++ {
		p := New()
		var snap Snapshot
		for _, event := range events {
			snap = p.Apply(event)
		}
		if snap.LastSeq != int64(len(events)) || snap.ToolCalls == 0 || snap.AssistantText == "" {
			b.Fatalf("unexpected snapshot: %#v", snap)
		}
	}
}

func benchmarkProjectorEvents(count int) []protocol.Event {
	events := make([]protocol.Event, 0, count+8)
	seq := int64(1)
	next := func(event protocol.Event) {
		event.Seq = seq
		seq++
		events = append(events, event)
	}
	next(protocol.Event{Type: protocol.EventRunStarted})
	next(protocol.Event{Type: protocol.EventTurnStarted, TurnID: "turn-bench"})
	next(protocol.Event{
		Type:   protocol.EventStepStarted,
		TurnID: "turn-bench",
		StepID: "turn-bench:model-call-001",
		Data: protocol.StepEvent{
			TurnID: "turn-bench",
			StepID: "turn-bench:model-call-001",
			Round:  1,
			Kind:   protocol.StepKindModelCall,
			Status: protocol.StepStatusStarted,
		},
	})
	next(protocol.Event{
		Type:   protocol.EventModelCallStarted,
		TurnID: "turn-bench",
		StepID: "turn-bench:model-call-001",
		Data:   protocol.ModelCallEvent{RequestID: "bench-model-call", Status: protocol.StepStatusStarted},
	})
	for i := 0; i < count; i++ {
		if i > 0 && i%50 == 0 {
			callID := fmt.Sprintf("call-bench-%03d", i/50)
			attemptID := callID + ":attempt-001"
			next(protocol.Event{
				Type:   protocol.EventToolCallRequested,
				CallID: callID,
				Data:   protocol.ToolCall{ID: callID, Name: "web_fetch"},
			})
			next(protocol.Event{
				Type:      protocol.EventToolCallStarted,
				CallID:    callID,
				AttemptID: attemptID,
				Data:      "web_fetch",
			})
			next(protocol.Event{
				Type:      protocol.EventToolCallFinished,
				CallID:    callID,
				AttemptID: attemptID,
				Data: protocol.ToolResult{
					CallID:  callID,
					Name:    "web_fetch",
					Content: fmt.Sprintf("summary %03d", i),
					Metadata: map[string]any{
						"tool_summary_input_tokens":     900 + i,
						"tool_summary_output_tokens":    120,
						"tool_summary_api_total_tokens": 1020 + i,
					},
				},
			})
		}
		next(protocol.Event{
			Type:   protocol.EventAssistantDelta,
			TurnID: "turn-bench",
			StepID: "turn-bench:model-call-001",
			Data:   fmt.Sprintf("delta-%03d ", i),
		})
	}
	next(protocol.Event{Type: protocol.EventRunCompleted})
	return events
}
