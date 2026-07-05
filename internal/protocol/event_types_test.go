package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestEventTypeDocsCoverKnownTypes(t *testing.T) {
	docs := EventTypeDocs()
	if len(docs) != 37 {
		t.Fatalf("EventTypeDocs length = %d, want 37", len(docs))
	}
	if len(eventTypeSpecByType) != len(docs) {
		t.Fatalf("event type spec map length = %d, want %d", len(eventTypeSpecByType), len(docs))
	}
	seen := map[EventType]bool{}
	for _, spec := range docs {
		if spec.Type == "" || spec.Doc == "" {
			t.Fatalf("incomplete event type spec: %#v", spec)
		}
		if seen[spec.Type] {
			t.Fatalf("duplicate event type spec %s", spec.Type)
		}
		seen[spec.Type] = true
		if !isKnownEventType(spec.Type) {
			t.Fatalf("isKnownEventType(%s) = false", spec.Type)
		}
	}
	for _, eventType := range knownEventTypeConstants() {
		if !seen[eventType] {
			t.Fatalf("event type constant %s missing from EventTypeDocs", eventType)
		}
	}
}

func TestEventSourceDocsCoverKnownSources(t *testing.T) {
	docs := EventSourceDocs()
	if len(docs) != 8 {
		t.Fatalf("EventSourceDocs length = %d, want 8", len(docs))
	}
	for _, spec := range docs {
		if spec.Source == "" || spec.Doc == "" {
			t.Fatalf("incomplete event source spec: %#v", spec)
		}
		if !isKnownEventSource(spec.Source) {
			t.Fatalf("isKnownEventSource(%s) = false", spec.Source)
		}
	}
}

func TestValidateEventEnvelopeUsesEventTypeRequiredIDs(t *testing.T) {
	for _, spec := range EventTypeDocs() {
		event := validEventForSpec(spec)
		if err := ValidateEventEnvelope(event); err != nil {
			t.Fatalf("valid %s event rejected: %v", spec.Type, err)
		}
		for _, field := range spec.RequiredIDs {
			missing := event
			clearEnvelopeField(&missing, field)
			err := ValidateEventEnvelope(missing)
			if err == nil || !strings.Contains(err.Error(), "missing "+field) {
				t.Fatalf("%s without %s error = %v", spec.Type, field, err)
			}
		}
	}
}

func TestEventTypeDocsReturnDefensiveCopies(t *testing.T) {
	docs := EventTypeDocs()
	for i, doc := range docs {
		if len(doc.RequiredIDs) == 0 {
			continue
		}
		want := doc.RequiredIDs[0]
		docs[i].RequiredIDs[0] = "mutated"
		if got := EventTypeDocs()[i].RequiredIDs[0]; got != want {
			t.Fatalf("EventTypeDocs leaked RequiredIDs slice: got %q, want %q", got, want)
		}
		return
	}
	t.Fatal("test fixture expected at least one required ID")
}

func validEventForSpec(spec EventTypeSpec) Event {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		Seq:           1,
		Source:        EventSourceAgent,
		TS:            time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
		Type:          spec.Type,
	}
	for _, field := range spec.RequiredIDs {
		setEnvelopeField(&event, field)
	}
	return event
}

func setEnvelopeField(event *Event, field string) {
	switch field {
	case "run_id":
		event.RunID = "run-1"
	case "turn_id":
		event.TurnID = "turn-1"
	case "step_id":
		event.StepID = "step-1"
	case "call_id":
		event.CallID = "call-1"
	case "attempt_id":
		event.AttemptID = "attempt-1"
	}
}

func clearEnvelopeField(event *Event, field string) {
	switch field {
	case "run_id":
		event.RunID = ""
	case "turn_id":
		event.TurnID = ""
	case "step_id":
		event.StepID = ""
	case "call_id":
		event.CallID = ""
	case "attempt_id":
		event.AttemptID = ""
	}
}

func knownEventTypeConstants() []EventType {
	return []EventType{
		EventRunStarted,
		EventTurnStarted,
		EventTurnCompleted,
		EventTurnChangeRecorded,
		EventTurnChangeReverted,
		EventStepStarted,
		EventStepCompleted,
		EventModelCallStarted,
		EventModelCallFinished,
		EventAssistantReasoning,
		EventAssistantDelta,
		EventToolCallRequested,
		EventToolPermissionRequested,
		EventToolPermissionDecided,
		EventToolAudit,
		EventToolCallProgress,
		EventToolCallStarted,
		EventToolCallFinished,
		EventToolCallFailed,
		EventToolCallAborted,
		EventToolOutputRefCreated,
		EventContextThreshold,
		EventContextCompacted,
		EventHookStarted,
		EventHookFinished,
		EventHookFailed,
		EventRunCompleted,
		EventRunFailed,
		EventProviderUsageUpdate,
		EventProviderHelperUsage,
		EventSessionStatus,
		EventGatewayStreamGap,
		EventStreamStillRunning,
		EventSessionImported,
		EventUserInputRequested,
		EventUserInputAnswered,
		EventUserInputRejected,
	}
}
