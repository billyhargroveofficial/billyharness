package eventlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestRecordValidatorRejectsCorruptRecords(t *testing.T) {
	validEvent := protocol.EnrichEvent(protocol.Event{Type: protocol.EventRunStarted}, protocol.EventEnvelope{
		Seq:    1,
		Source: protocol.EventSourceAgent,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})

	tests := []struct {
		name    string
		records []Record
		want    string
	}{
		{
			name: "sequence gap",
			records: []Record{
				{SchemaVersion: 1, Seq: 1, ScopeID: "run-1", EventType: string(protocol.EventRunStarted), Event: validEvent, HasEvent: true},
				{SchemaVersion: 1, Seq: 3, ScopeID: "run-1", EventType: string(protocol.EventRunCompleted), Event: validEvent, HasEvent: true},
			},
			want: "sequence gap",
		},
		{
			name: "scope change",
			records: []Record{
				{SchemaVersion: 1, Seq: 1, ScopeID: "run-1", EventType: string(protocol.EventRunStarted), Event: validEvent, HasEvent: true},
				{SchemaVersion: 1, Seq: 2, ScopeID: "run-2", EventType: string(protocol.EventRunStarted), Event: validEvent, HasEvent: true},
			},
			want: "run_id changed",
		},
		{
			name: "event type mismatch",
			records: []Record{
				{SchemaVersion: 1, Seq: 1, ScopeID: "run-1", EventType: string(protocol.EventRunCompleted), Event: validEvent, HasEvent: true},
			},
			want: "event_type",
		},
		{
			name: "invalid envelope",
			records: []Record{
				{SchemaVersion: 1, Seq: 1, ScopeID: "run-1", EventType: string(protocol.EventToolCallStarted), Event: protocol.EnrichEvent(protocol.Event{Type: protocol.EventToolCallStarted}, protocol.EventEnvelope{
					Seq:    1,
					Source: protocol.EventSourceAgent,
					RunID:  "run-1",
					TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
				}), HasEvent: true},
			},
			want: "missing call_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewRecordValidator(RecordValidatorOptions{
				SchemaVersion:    1,
				ScopeName:        "run_id",
				ValidateEnvelope: true,
			})
			var err error
			for _, record := range tt.records {
				err = validator.Validate(record)
				if err != nil {
					break
				}
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestAppendAndReplayJSONL(t *testing.T) {
	type testRecord struct {
		Seq  int    `json:"seq"`
		Name string `json:"name"`
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := AppendJSONL(path, testRecord{Seq: 1, Name: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendJSONL(path, testRecord{Seq: 2, Name: "two"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %v, want 0600", got)
	}
	var got []testRecord
	err = ReplayJSONL[testRecord](path, JSONLOptions{}, func(record JSONLRecord[testRecord]) error {
		if record.Line != record.Value.Seq || record.RecordNo != int64(record.Value.Seq) || record.Path != path {
			t.Fatalf("replay metadata = %#v", record)
		}
		got = append(got, record.Value)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "two" {
		t.Fatalf("records = %#v", got)
	}
}

func TestReplayJSONLMissingOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	if err := ReplayJSONL[Record](path, JSONLOptions{MissingOK: true}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReplayJSONLReturnsStructuredCorruptionError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":1}\n{bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ReplayJSONL[Record](path, JSONLOptions{}, nil)
	if err == nil {
		t.Fatal("expected invalid JSONL error")
	}
	var corrupt *CorruptionError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error %T does not expose CorruptionError", err)
	}
	if corrupt.Path != path || corrupt.Line != 2 || corrupt.RecordNo != 2 || corrupt.Kind != "invalid JSONL record" {
		t.Fatalf("corruption error = %#v", corrupt)
	}
}

func TestRecordValidatorAcceptsExpectedScope(t *testing.T) {
	validator := NewRecordValidator(RecordValidatorOptions{
		SchemaVersion:   1,
		ScopeName:       "session_id",
		ExpectedScopeID: "session-1",
	})
	if err := validator.Validate(Record{SchemaVersion: 1, Seq: 1, ScopeID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	if validator.FirstSeq() != 1 || validator.LastSeq() != 1 || validator.ScopeID() != "session-1" {
		t.Fatalf("validator state = first:%d last:%d scope:%q", validator.FirstSeq(), validator.LastSeq(), validator.ScopeID())
	}
}

func TestLifecycleValidatorRejectsOrderingViolations(t *testing.T) {
	tests := []struct {
		name   string
		events []protocol.Event
		want   string
	}{
		{
			name:   "completed run without started run",
			events: []protocol.Event{{Type: protocol.EventRunCompleted, RunID: "run-1"}},
			want:   "without started run",
		},
		{
			name: "orphan step completion",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
				{Type: protocol.EventStepCompleted, RunID: "run-1", TurnID: "turn-1", StepID: "step-1"},
			},
			want: "orphan step completion",
		},
		{
			name: "tool result without matching call id",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
			},
			want: "matching call_id",
		},
		{
			name: "tool finish without matching attempt id",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
			},
			want: "matching attempt_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLifecycle(tt.events)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLifecycleValidatorAllowsParallelChildStepsOutOfOrder(t *testing.T) {
	events := []protocol.Event{
		{Type: protocol.EventRunStarted, RunID: "run-1"},
		{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
		{Type: protocol.EventStepStarted, RunID: "run-1", TurnID: "turn-1", StepID: "batch-1"},
		{Type: protocol.EventStepStarted, RunID: "run-1", TurnID: "turn-1", StepID: "child-1", ParentStepID: "batch-1"},
		{Type: protocol.EventStepStarted, RunID: "run-1", TurnID: "turn-1", StepID: "child-2", ParentStepID: "batch-1"},
		{Type: protocol.EventStepCompleted, RunID: "run-1", TurnID: "turn-1", StepID: "child-2", ParentStepID: "batch-1"},
		{Type: protocol.EventStepCompleted, RunID: "run-1", TurnID: "turn-1", StepID: "child-1", ParentStepID: "batch-1"},
		{Type: protocol.EventStepCompleted, RunID: "run-1", TurnID: "turn-1", StepID: "batch-1"},
		{Type: protocol.EventRunCompleted, RunID: "run-1"},
	}
	if err := ValidateLifecycle(events); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleValidatorDoesNotTreatBatchIDAsParentForMapBatchStep(t *testing.T) {
	events := []protocol.Event{
		{Type: protocol.EventRunStarted, RunID: "run-1"},
		{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
		{Type: protocol.EventStepStarted, RunID: "run-1", Data: map[string]any{
			"turn_id":  "turn-1",
			"step_id":  "turn-1:tool-batch-1",
			"batch_id": "turn-1:tool-batch-1",
			"kind":     "tool_batch",
		}},
	}
	if err := ValidateLifecycle(events); err != nil {
		t.Fatalf("batch step with matching batch_id should not require a parent: %v", err)
	}
}
