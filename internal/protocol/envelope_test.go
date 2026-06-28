package protocol

import (
	"testing"
	"time"
)

func TestEventEnricherAddsEnvelopeAndExtractsStepIDs(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 11, 12, 13, time.UTC)
	var got []Event
	enricher := NewEventEnricherWithEnvelope(EventEnvelope{
		SubmissionID: "submission-1",
		RunID:        "run-1",
		Source:       EventSourceAgent,
	}, func(event Event) {
		got = append(got, event)
	})
	enricher.env.Now = func() time.Time { return now }

	enricher.Emit(Event{
		Type: EventStepCompleted,
		Data: StepEvent{
			TurnID:     "turn-001",
			StepID:     "turn-001:tool-call-001",
			Kind:       StepKindToolCall,
			Status:     StepStatusCompleted,
			ToolCallID: "call-1",
			BatchID:    "turn-001:tool-batch-001",
			DurationMS: 25,
			Metadata: map[string]any{
				"attempt_id": "turn-001:tool-call-001:attempt-001",
			},
		},
	})

	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	event := got[0]
	if event.SchemaVersion != EventSchemaVersion || event.Seq != 1 || event.Source != EventSourceAgent ||
		event.TS != now.Format(time.RFC3339Nano) || event.RunID != "run-1" || event.SubmissionID != "submission-1" {
		t.Fatalf("envelope = %#v", event)
	}
	if event.TurnID != "turn-001" || event.StepID != "turn-001:tool-call-001" ||
		event.CallID != "call-1" || event.AttemptID != "turn-001:tool-call-001:attempt-001" ||
		event.ParentStepID != "turn-001:tool-batch-001" || event.DurationMS != 25 {
		t.Fatalf("ids = %#v", event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEventEnvelopeAllowsLegacyAndRejectsMissingIDs(t *testing.T) {
	if err := ValidateEventEnvelope(Event{Type: EventToolCallStarted}); err != nil {
		t.Fatalf("legacy event should validate: %v", err)
	}

	event := EnrichEvent(Event{
		Type: EventToolCallStarted,
		Data: "fs_read_file",
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if err := ValidateEventEnvelope(event); err == nil {
		t.Fatal("expected missing call_id/attempt_id error")
	}
}

func TestEventEnricherExtractsToolProgressIDs(t *testing.T) {
	event := EnrichEvent(Event{
		Type: EventToolCallProgress,
		Data: ToolProgressEvent{
			CallID:    "call-1",
			AttemptID: "turn-001:tool-call-001:attempt-001",
			Phase:     "executing",
			Status:    StepStatusStarted,
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.CallID != "call-1" || event.AttemptID != "turn-001:tool-call-001:attempt-001" {
		t.Fatalf("tool progress envelope = %#v", event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}
