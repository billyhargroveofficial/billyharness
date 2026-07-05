package eventlog

import (
	"encoding/json"
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

func TestRecordValidatorRequiresEnvelopeAndChecksEventSeq(t *testing.T) {
	validator := NewRecordValidator(RecordValidatorOptions{
		SchemaVersion:    1,
		ScopeName:        "session_id",
		ExpectedScopeID:  "session-1",
		ValidateEnvelope: true,
		RequireEnvelope:  true,
	})
	err := validator.Validate(Record{
		SchemaVersion: 1,
		Seq:           1,
		ScopeID:       "session-1",
		EventType:     string(protocol.EventRunStarted),
		Event:         protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"},
		HasEvent:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "missing event schema_version") {
		t.Fatalf("missing envelope error = %v", err)
	}

	event := protocol.EnrichEvent(protocol.Event{Type: protocol.EventRunStarted}, protocol.EventEnvelope{
		Seq:    2,
		Source: protocol.EventSourceGateway,
		RunID:  "run-1",
		TS:     time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
	})
	validator = NewRecordValidator(RecordValidatorOptions{
		SchemaVersion:    1,
		ScopeName:        "session_id",
		ExpectedScopeID:  "session-1",
		ValidateEnvelope: true,
		RequireEnvelope:  true,
	})
	err = validator.Validate(Record{
		SchemaVersion: 1,
		Seq:           1,
		ScopeID:       "session-1",
		EventType:     string(protocol.EventRunStarted),
		Event:         event,
		HasEvent:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "event seq = 2, record seq = 1") {
		t.Fatalf("seq mismatch error = %v", err)
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

func TestReplayJSONLPreservesMCPStructuredToolMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	event := protocol.Event{
		SchemaVersion: protocol.EventSchemaVersion,
		Seq:           1,
		Source:        protocol.EventSourceAgent,
		RunID:         "run-1",
		Type:          protocol.EventToolCallFinished,
		CallID:        "call-1",
		AttemptID:     "attempt-1",
		Data: protocol.ToolResult{
			CallID:  "call-1",
			Name:    "mcp_call",
			Content: "visible",
			Metadata: map[string]any{
				"mcp_result_content": []any{
					map[string]any{"type": "text", "text": "visible"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": "BASE64"},
				},
				"mcp_structured_content": map[string]any{"answer": "structured", "count": 2},
				"mcp_result_meta":        map[string]any{"request_id": "fake-call-1"},
			},
		},
	}
	if err := AppendJSONL(path, event); err != nil {
		t.Fatal(err)
	}
	var replayed []protocol.Event
	err := ReplayJSONL[protocol.Event](path, JSONLOptions{}, func(record JSONLRecord[protocol.Event]) error {
		replayed = append(replayed, record.Value)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 {
		t.Fatalf("replayed = %#v", replayed)
	}
	body, err := json.Marshal(replayed[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	var result protocol.ToolResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	structured, ok := result.Metadata["mcp_structured_content"].(map[string]any)
	if !ok || structured["answer"] != "structured" || structured["count"].(float64) != 2 {
		t.Fatalf("structured metadata = %#v", result.Metadata["mcp_structured_content"])
	}
	content, ok := result.Metadata["mcp_result_content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content metadata = %#v", result.Metadata["mcp_result_content"])
	}
	image, ok := content[1].(map[string]any)
	if !ok || image["mimeType"] != "image/png" || image["data"] != "BASE64" {
		t.Fatalf("image metadata = %#v", content[1])
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
			name: "turn without started run",
			events: []protocol.Event{
				{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
			},
			want: "without started run",
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
			name: "model call without started step",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
				{Type: protocol.EventModelCallStarted, RunID: "run-1", TurnID: "turn-1", StepID: "step-1"},
			},
			want: "without started step",
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
		{
			name: "tool attempt cannot move between calls",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-2"},
				{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-2", AttemptID: "attempt-1"},
			},
			want: "was started for call_id",
		},
		{
			name: "attempt started progress cannot move between calls",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallProgress, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolProgressEvent{CallID: "call-1", AttemptID: "attempt-1", Phase: "attempt_started", Status: protocol.StepStatusStarted}},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-2"},
				{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-2", AttemptID: "attempt-1"},
			},
			want: "was started for call_id",
		},
		{
			name: "duplicate tool request",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
			},
			want: "duplicate tool.call_requested",
		},
		{
			name: "permission without matching call",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolPermissionRequested, RunID: "run-1", CallID: "call-1"},
			},
			want: "matching call_id",
		},
		{
			name: "orphan progress",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallProgress, RunID: "run-1", CallID: "call-1", Data: protocol.ToolProgressEvent{CallID: "call-1", Phase: "executing", Status: protocol.StepStatusStarted}},
			},
			want: "matching call_id",
		},
		{
			name: "orphan output ref",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolOutputRefCreated, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolOutputRefEvent{CallID: "call-1", AttemptID: "attempt-1", OutputRef: "/tmp/out"}},
			},
			want: "matching attempt_id",
		},
		{
			name: "user input without matching attempt",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventUserInputRequested, RunID: "run-1", TurnID: "turn-1", StepID: "step-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.UserInputRequestEvent{RequestID: "request-1", RunID: "run-1", TurnID: "turn-1", StepID: "step-1", CallID: "call-1", AttemptID: "attempt-1"}},
			},
			want: "matching attempt_id",
		},
		{
			name: "hook with unknown call",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventHookFinished, RunID: "run-1", CallID: "call-1", Data: protocol.HookEvent{HookEvent: "before_tool", Status: protocol.StepStatusCompleted, CallID: "call-1"}},
			},
			want: "matching call_id",
		},
		{
			name: "progress after terminal attempt",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
				{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
				{Type: protocol.EventToolCallProgress, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolProgressEvent{CallID: "call-1", AttemptID: "attempt-1", Phase: "executing", Status: protocol.StepStatusStarted}},
			},
			want: "after terminal tool attempt",
		},
		{
			name: "terminal tool result with unsettled output ref",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
				{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolResult{CallID: "call-1", OutputRef: "/tmp/out"}},
			},
			want: "without settled output_ref event",
		},
		{
			name: "duplicate terminal run event",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventRunCompleted, RunID: "run-1"},
				{Type: protocol.EventRunFailed, RunID: "run-1", Data: "late failure"},
			},
			want: "duplicate terminal run event",
		},
		{
			name: "duplicate terminal turn event",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
				{Type: protocol.EventTurnCompleted, RunID: "run-1", TurnID: "turn-1"},
				{Type: protocol.EventTurnCompleted, RunID: "run-1", TurnID: "turn-1"},
			},
			want: "duplicate terminal turn event",
		},
		{
			name: "duplicate terminal step event",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
				{Type: protocol.EventStepStarted, RunID: "run-1", TurnID: "turn-1", StepID: "step-1"},
				{Type: protocol.EventStepCompleted, RunID: "run-1", TurnID: "turn-1", StepID: "step-1"},
				{Type: protocol.EventStepCompleted, RunID: "run-1", TurnID: "turn-1", StepID: "step-1"},
			},
			want: "duplicate terminal step event",
		},
		{
			name: "duplicate terminal tool attempt event",
			events: []protocol.Event{
				{Type: protocol.EventRunStarted, RunID: "run-1"},
				{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
				{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
				{Type: protocol.EventToolCallFailed, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
				{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
			},
			want: "duplicate terminal tool attempt event",
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

func TestLifecycleRulesExposeRuntimeRunRules(t *testing.T) {
	rules := LifecycleRules()
	if got, want := len(rules), 3; got != want {
		t.Fatalf("LifecycleRules() = %d rules, want %d", got, want)
	}
	seen := map[protocol.EventType]LifecycleRuleDoc{}
	for _, rule := range rules {
		if rule.Entity != "run" {
			t.Fatalf("unexpected lifecycle entity %q in partial table", rule.Entity)
		}
		if _, ok := lifecycleRuleFor(rule.Event); !ok {
			t.Fatalf("exported rule %s is not consumed by runtime table", rule.Event)
		}
		seen[rule.Event] = rule
	}
	for _, eventType := range []protocol.EventType{protocol.EventRunStarted, protocol.EventRunCompleted, protocol.EventRunFailed} {
		if _, ok := seen[eventType]; !ok {
			t.Fatalf("missing lifecycle rule for %s", eventType)
		}
	}
	if seen[protocol.EventRunStarted].Kind != string(ruleStarts) {
		t.Fatalf("run.started kind = %q, want %q", seen[protocol.EventRunStarted].Kind, ruleStarts)
	}
	if !seen[protocol.EventRunCompleted].Terminal || !seen[protocol.EventRunFailed].Terminal {
		t.Fatalf("terminal run rules = completed:%v failed:%v", seen[protocol.EventRunCompleted].Terminal, seen[protocol.EventRunFailed].Terminal)
	}
}

func TestLifecycleValidatorRejectsOpenTerminalStateWhenRequired(t *testing.T) {
	err := ValidateClosedLifecycle([]protocol.Event{
		{Type: protocol.EventRunStarted, RunID: "run-1"},
		{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"},
		{Type: protocol.EventStepStarted, RunID: "run-1", TurnID: "turn-1", StepID: "step-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "run \"run-1\" has no terminal event") {
		t.Fatalf("closed lifecycle error = %v", err)
	}
}

func TestLifecycleValidatorAllowsOutputRefBeforeTerminalAndCleanupProgress(t *testing.T) {
	events := []protocol.Event{
		{Type: protocol.EventRunStarted, RunID: "run-1"},
		{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1"},
		{Type: protocol.EventToolCallProgress, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolProgressEvent{CallID: "call-1", AttemptID: "attempt-1", Phase: "attempt_started", Status: protocol.StepStatusStarted}},
		{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
		{Type: protocol.EventToolOutputRefCreated, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolOutputRefEvent{CallID: "call-1", AttemptID: "attempt-1", OutputRef: "/tmp/out"}},
		{Type: protocol.EventToolCallFinished, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1"},
		{Type: protocol.EventToolCallProgress, RunID: "run-1", CallID: "call-1", AttemptID: "attempt-1", Data: protocol.ToolProgressEvent{CallID: "call-1", AttemptID: "attempt-1", Phase: "finalize", Status: protocol.StepStatusCompleted}},
	}
	if err := ValidateLifecycle(events); err != nil {
		t.Fatal(err)
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
