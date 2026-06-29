package protocol

import (
	"sync"
	"sync/atomic"
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

func TestEventEnricherSerializesConcurrentEmit(t *testing.T) {
	const total = 128
	var activeCallbacks int32
	var overlapped int32
	seqs := make([]int64, 0, total)
	enricher := NewEventEnricherWithEnvelope(EventEnvelope{
		RunID:  "run-concurrent",
		Source: EventSourceAgent,
	}, func(event Event) {
		if active := atomic.AddInt32(&activeCallbacks, 1); active != 1 {
			atomic.StoreInt32(&overlapped, 1)
		}
		time.Sleep(100 * time.Microsecond)
		seqs = append(seqs, event.Seq)
		atomic.AddInt32(&activeCallbacks, -1)
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			enricher.Emit(Event{Type: EventAssistantDelta, Data: "delta"})
		}(i)
	}
	close(start)
	wg.Wait()

	if atomic.LoadInt32(&overlapped) != 0 {
		t.Fatal("event callbacks overlapped")
	}
	if len(seqs) != total {
		t.Fatalf("events = %d, want %d", len(seqs), total)
	}
	for i, seq := range seqs {
		if want := int64(i + 1); seq != want {
			t.Fatalf("seq[%d] = %d, want %d; seqs=%v", i, seq, want, seqs)
		}
	}
}

func TestEventEnricherDoesNotTreatBatchIDAsParentFromMapData(t *testing.T) {
	event := EnrichEvent(Event{
		Type: EventStepStarted,
		Data: map[string]any{
			"turn_id":  "turn-001",
			"step_id":  "turn-001:tool-batch-001",
			"batch_id": "turn-001:tool-batch-001",
			"kind":     "tool_batch",
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.ParentStepID != "" {
		t.Fatalf("batch_id should not be copied as parent_step_id for map data: %#v", event)
	}
	if event.TurnID != "turn-001" || event.StepID != "turn-001:tool-batch-001" {
		t.Fatalf("event IDs were not extracted: %#v", event)
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

func TestEventEnricherExtractsToolPermissionIDs(t *testing.T) {
	event := EnrichEvent(Event{
		Type: EventToolPermissionDecided,
		Data: ToolPermissionEvent{
			CallID:           "call-1",
			Name:             "fs_write_file",
			Risk:             RiskWrite,
			RequiresApproval: true,
			Decision:         "deny",
			Source:           "config",
			Reason:           "dangerous_tools_disabled",
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.CallID != "call-1" {
		t.Fatalf("tool permission envelope = %#v", event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}

func TestEventEnricherExtractsToolOutputRefIDs(t *testing.T) {
	event := EnrichEvent(Event{
		Type: EventToolOutputRefCreated,
		Data: ToolOutputRefEvent{
			CallID:               "call-1",
			Name:                 "web_fetch",
			AttemptID:            "turn-001:tool-call-001:attempt-001",
			OutputRef:            "/tmp/tool-output/ref.txt",
			OutputRefID:          "ref.txt",
			OutputRefBytes:       123,
			OutputRefSHA256:      "abc123",
			OutputRefPermissions: "0600",
			OutputRefPlaintext:   true,
			Truncated:            true,
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.CallID != "call-1" || event.AttemptID != "turn-001:tool-call-001:attempt-001" {
		t.Fatalf("tool output ref envelope = %#v", event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}

func TestEventEnricherExtractsModelCallMetadata(t *testing.T) {
	totalLatency := int64(42)
	event := EnrichEvent(Event{
		Type:   EventModelCallFinished,
		TurnID: "turn-001",
		StepID: "turn-001:model-call-001",
		Data: ModelCallEvent{
			RequestID:              "turn-001:provider-request-001",
			ProviderID:             "mock",
			ModelID:                "mock-model",
			ProfileInstructionHash: "profile-sha",
			Status:                 StepStatusCompleted,
			Retries:                1,
			TotalLatencyMS:         &totalLatency,
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.ProfileHash != "profile-sha" || event.DurationMS != totalLatency {
		t.Fatalf("model call envelope = %#v", event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}

func TestEventEnricherExtractsHookMetadata(t *testing.T) {
	duration := int64(12)
	event := EnrichEvent(Event{
		Type: EventHookFinished,
		Data: HookEvent{
			HookEvent:  "before_tool",
			HookName:   "audit",
			Status:     StepStatusCompleted,
			DurationMS: &duration,
			TurnID:     "turn-001",
			StepID:     "turn-001:tool-call-001",
			CallID:     "call-1",
			AttemptID:  "turn-001:tool-call-001:attempt-001",
			ToolName:   "time_now",
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.TurnID != "turn-001" ||
		event.StepID != "turn-001:tool-call-001" ||
		event.CallID != "call-1" ||
		event.AttemptID != "turn-001:tool-call-001:attempt-001" ||
		event.DurationMS != duration {
		t.Fatalf("hook envelope = %#v", event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}

func TestEventEnricherExtractsProfileHashFromMetadata(t *testing.T) {
	event := EnrichEvent(Event{
		Type: EventTurnStarted,
		Data: TurnEvent{
			TurnID: "turn-001",
			Round:  1,
			Status: TurnStatusStarted,
			Metadata: map[string]any{
				"profile_instruction_hash": "profile-sha",
			},
		},
	}, EventEnvelope{
		Seq:    1,
		Source: EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	if event.ProfileHash != "profile-sha" {
		t.Fatalf("profile hash = %q, want profile-sha: %#v", event.ProfileHash, event)
	}
	if err := ValidateEventEnvelope(event); err != nil {
		t.Fatal(err)
	}
}
