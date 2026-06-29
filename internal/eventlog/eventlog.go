package eventlog

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Record struct {
	SchemaVersion int
	Seq           int64
	ScopeID       string
	EventType     string
	Event         protocol.Event
	HasEvent      bool
}

type RecordValidatorOptions struct {
	SchemaVersion    int
	ScopeName        string
	ExpectedScopeID  string
	ValidateEnvelope bool
}

type RecordValidator struct {
	schemaVersion    int
	scopeName        string
	expectedScopeID  string
	validateEnvelope bool
	nextSeq          int64
	firstSeq         int64
	lastSeq          int64
	scopeID          string
}

func NewRecordValidator(opts RecordValidatorOptions) *RecordValidator {
	return &RecordValidator{
		schemaVersion:    opts.SchemaVersion,
		scopeName:        strings.TrimSpace(opts.ScopeName),
		expectedScopeID:  strings.TrimSpace(opts.ExpectedScopeID),
		validateEnvelope: opts.ValidateEnvelope,
		nextSeq:          1,
	}
}

func (v *RecordValidator) NextSeq() int64 {
	if v == nil || v.nextSeq <= 0 {
		return 1
	}
	return v.nextSeq
}

func (v *RecordValidator) FirstSeq() int64 {
	if v == nil {
		return 0
	}
	return v.firstSeq
}

func (v *RecordValidator) LastSeq() int64 {
	if v == nil {
		return 0
	}
	return v.lastSeq
}

func (v *RecordValidator) ScopeID() string {
	if v == nil {
		return ""
	}
	return v.scopeID
}

func ValidateEnvelope(event protocol.Event) error {
	return protocol.ValidateEventEnvelope(event)
}

func (v *RecordValidator) Validate(record Record) error {
	if v == nil {
		return nil
	}
	expectedSeq := v.NextSeq()
	if v.schemaVersion > 0 && record.SchemaVersion != 0 && record.SchemaVersion != v.schemaVersion {
		return fmt.Errorf("unsupported schema_version %d", record.SchemaVersion)
	}
	if record.Seq != expectedSeq {
		return fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq)
	}
	if err := v.validateScope(record.ScopeID); err != nil {
		return err
	}
	if record.EventType != "" && record.HasEvent && record.Event.Type != "" && record.EventType != string(record.Event.Type) {
		return fmt.Errorf("event_type = %q, event.type = %q", record.EventType, record.Event.Type)
	}
	if v.validateEnvelope && record.HasEvent {
		if err := ValidateEnvelope(record.Event); err != nil {
			return fmt.Errorf("invalid event envelope: %w", err)
		}
	}
	if v.firstSeq == 0 {
		v.firstSeq = record.Seq
	}
	v.lastSeq = record.Seq
	v.nextSeq = expectedSeq + 1
	return nil
}

func (v *RecordValidator) validateScope(scopeID string) error {
	if v.scopeName == "" {
		return nil
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return fmt.Errorf("missing %s", v.scopeName)
	}
	if v.expectedScopeID != "" {
		if scopeID != v.expectedScopeID {
			return fmt.Errorf("%s = %q, want %q", v.scopeName, scopeID, v.expectedScopeID)
		}
	} else if v.scopeID != "" && scopeID != v.scopeID {
		return fmt.Errorf("%s changed from %q to %q", v.scopeName, v.scopeID, scopeID)
	}
	if v.scopeID == "" {
		v.scopeID = scopeID
	}
	return nil
}

type LifecycleValidator struct {
	runs     map[string]struct{}
	turns    map[string]struct{}
	steps    map[string]stepState
	calls    map[string]struct{}
	attempts map[string]struct{}
}

type stepState struct {
	runID  string
	turnID string
	stepID string
}

func NewLifecycleValidator() *LifecycleValidator {
	v := &LifecycleValidator{}
	v.ensure()
	return v
}

func ValidateLifecycle(events []protocol.Event) error {
	validator := NewLifecycleValidator()
	for _, event := range events {
		if err := validator.Observe(event); err != nil {
			return err
		}
	}
	return nil
}

func (v *LifecycleValidator) Observe(event protocol.Event) error {
	if v == nil {
		return nil
	}
	v.ensure()
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	runID := strings.TrimSpace(event.RunID)
	turnID := strings.TrimSpace(event.TurnID)
	stepID := strings.TrimSpace(event.StepID)
	callID := strings.TrimSpace(event.CallID)
	attemptID := strings.TrimSpace(event.AttemptID)

	switch event.Type {
	case protocol.EventRunStarted:
		if runID != "" {
			v.runs[runID] = struct{}{}
		}
	case protocol.EventRunCompleted, protocol.EventRunFailed:
		if runID == "" {
			return fmt.Errorf("%s missing run_id", event.Type)
		}
		if _, ok := v.runs[runID]; !ok {
			return fmt.Errorf("%s without started run %q", event.Type, runID)
		}
	case protocol.EventTurnStarted:
		if runID != "" && turnID != "" {
			v.turns[turnKey(runID, turnID)] = struct{}{}
		}
	case protocol.EventTurnCompleted:
		if runID != "" && turnID != "" {
			if _, ok := v.turns[turnKey(runID, turnID)]; !ok {
				return fmt.Errorf("%s without started turn %q", event.Type, turnID)
			}
		}
	case protocol.EventStepStarted:
		if stepID == "" {
			return nil
		}
		if event.ParentStepID != "" {
			if _, ok := v.steps[stepKey(runID, turnID, event.ParentStepID)]; !ok {
				return fmt.Errorf("%s references unknown parent_step_id %q", event.Type, event.ParentStepID)
			}
		}
		v.steps[stepKey(runID, turnID, stepID)] = stepState{runID: runID, turnID: turnID, stepID: stepID}
	case protocol.EventStepCompleted:
		if stepID == "" {
			return fmt.Errorf("%s missing step_id", event.Type)
		}
		if _, ok := v.steps[stepKey(runID, turnID, stepID)]; !ok {
			return fmt.Errorf("orphan step completion %q", stepID)
		}
	case protocol.EventToolCallRequested:
		if callID == "" {
			return fmt.Errorf("%s missing call_id", event.Type)
		}
		v.calls[callKey(runID, callID)] = struct{}{}
	case protocol.EventToolCallStarted:
		if callID == "" {
			return fmt.Errorf("%s missing call_id", event.Type)
		}
		if _, ok := v.calls[callKey(runID, callID)]; !ok {
			return fmt.Errorf("%s without matching call_id %q", event.Type, callID)
		}
		if attemptID == "" {
			return fmt.Errorf("%s missing attempt_id", event.Type)
		}
		v.attempts[attemptKey(runID, attemptID)] = struct{}{}
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		if callID == "" {
			return fmt.Errorf("%s missing call_id", event.Type)
		}
		if _, ok := v.calls[callKey(runID, callID)]; !ok {
			return fmt.Errorf("%s without matching call_id %q", event.Type, callID)
		}
		if attemptID == "" {
			return fmt.Errorf("%s missing attempt_id", event.Type)
		}
		if _, ok := v.attempts[attemptKey(runID, attemptID)]; !ok {
			return fmt.Errorf("%s without matching attempt_id %q", event.Type, attemptID)
		}
	}
	return nil
}

func (v *LifecycleValidator) ensure() {
	if v.runs == nil {
		v.runs = map[string]struct{}{}
	}
	if v.turns == nil {
		v.turns = map[string]struct{}{}
	}
	if v.steps == nil {
		v.steps = map[string]stepState{}
	}
	if v.calls == nil {
		v.calls = map[string]struct{}{}
	}
	if v.attempts == nil {
		v.attempts = map[string]struct{}{}
	}
}

func turnKey(runID, turnID string) string {
	return runID + "\x00" + turnID
}

func stepKey(runID, turnID, stepID string) string {
	return runID + "\x00" + turnID + "\x00" + stepID
}

func callKey(runID, callID string) string {
	return runID + "\x00" + callID
}

func attemptKey(runID, attemptID string) string {
	return runID + "\x00" + attemptID
}
