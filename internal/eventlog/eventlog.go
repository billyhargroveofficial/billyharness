package eventlog

import (
	"encoding/json"
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
	RequireEnvelope  bool
}

type RecordValidator struct {
	schemaVersion    int
	scopeName        string
	expectedScopeID  string
	validateEnvelope bool
	requireEnvelope  bool
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
		requireEnvelope:  opts.RequireEnvelope,
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
	if record.HasEvent && record.Event.Seq != 0 && record.Event.Seq != record.Seq {
		return fmt.Errorf("event seq = %d, record seq = %d", record.Event.Seq, record.Seq)
	}
	if v.requireEnvelope && record.HasEvent && record.Event.SchemaVersion == 0 {
		return fmt.Errorf("missing event schema_version")
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
	runs             map[string]struct{}
	terminalRun      map[string]protocol.EventType
	turns            map[string]struct{}
	terminalTurn     map[string]protocol.EventType
	steps            map[string]stepState
	terminalStep     map[string]protocol.EventType
	calls            map[string]struct{}
	attempts         map[string]struct{}
	attemptCalls     map[string]string
	terminalAttempts map[string]protocol.EventType
	outputRefs       map[string]struct{}
	userInputs       map[string]struct{}
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

func ValidateClosedLifecycle(events []protocol.Event) error {
	validator := NewLifecycleValidator()
	for _, event := range events {
		if err := validator.Observe(event); err != nil {
			return err
		}
	}
	return validator.ValidateClosed()
}

func (v *LifecycleValidator) Clone() *LifecycleValidator {
	if v == nil {
		return NewLifecycleValidator()
	}
	v.ensure()
	return &LifecycleValidator{
		runs:             cloneSet(v.runs),
		terminalRun:      cloneEventTypeMap(v.terminalRun),
		turns:            cloneSet(v.turns),
		terminalTurn:     cloneEventTypeMap(v.terminalTurn),
		steps:            cloneStepStateMap(v.steps),
		terminalStep:     cloneEventTypeMap(v.terminalStep),
		calls:            cloneSet(v.calls),
		attempts:         cloneSet(v.attempts),
		attemptCalls:     cloneStringMap(v.attemptCalls),
		terminalAttempts: cloneEventTypeMap(v.terminalAttempts),
		outputRefs:       cloneSet(v.outputRefs),
		userInputs:       cloneSet(v.userInputs),
	}
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
		if runID == "" {
			return fmt.Errorf("%s missing run_id", event.Type)
		}
		v.runs[runID] = struct{}{}
	case protocol.EventRunCompleted, protocol.EventRunFailed:
		if runID == "" {
			return fmt.Errorf("%s missing run_id", event.Type)
		}
		if _, ok := v.runs[runID]; !ok {
			return fmt.Errorf("%s without started run %q", event.Type, runID)
		}
		if previous, ok := v.terminalRun[runID]; ok {
			return fmt.Errorf("duplicate terminal run event for %q: got %s after %s", runID, event.Type, previous)
		}
		v.terminalRun[runID] = event.Type
	case protocol.EventTurnStarted:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
		if turnID == "" {
			return fmt.Errorf("%s missing turn_id", event.Type)
		}
		v.turns[turnKey(runID, turnID)] = struct{}{}
	case protocol.EventTurnCompleted:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
		if turnID == "" {
			return fmt.Errorf("%s missing turn_id", event.Type)
		}
		key := turnKey(runID, turnID)
		if _, ok := v.turns[key]; !ok {
			return fmt.Errorf("%s without started turn %q", event.Type, turnID)
		}
		if previous, ok := v.terminalTurn[key]; ok {
			return fmt.Errorf("duplicate terminal turn event for %q: got %s after %s", turnID, event.Type, previous)
		}
		v.terminalTurn[key] = event.Type
	case protocol.EventTurnChangeRecorded, protocol.EventTurnChangeReverted:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
		if turnID != "" {
			if _, ok := v.turns[turnKey(runID, turnID)]; !ok {
				return fmt.Errorf("%s without started turn %q", event.Type, turnID)
			}
		}
	case protocol.EventStepStarted:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
		if turnID == "" {
			return fmt.Errorf("%s missing turn_id", event.Type)
		}
		if _, ok := v.turns[turnKey(runID, turnID)]; !ok {
			return fmt.Errorf("%s without started turn %q", event.Type, turnID)
		}
		if stepID == "" {
			return fmt.Errorf("%s missing step_id", event.Type)
		}
		if event.ParentStepID != "" {
			if _, ok := v.steps[stepKey(runID, turnID, event.ParentStepID)]; !ok {
				return fmt.Errorf("%s references unknown parent_step_id %q", event.Type, event.ParentStepID)
			}
		}
		v.steps[stepKey(runID, turnID, stepID)] = stepState{runID: runID, turnID: turnID, stepID: stepID}
	case protocol.EventStepCompleted:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
		if turnID == "" {
			return fmt.Errorf("%s missing turn_id", event.Type)
		}
		if _, ok := v.turns[turnKey(runID, turnID)]; !ok {
			return fmt.Errorf("%s without started turn %q", event.Type, turnID)
		}
		if stepID == "" {
			return fmt.Errorf("%s missing step_id", event.Type)
		}
		if _, ok := v.steps[stepKey(runID, turnID, stepID)]; !ok {
			return fmt.Errorf("orphan step completion %q", stepID)
		}
		key := stepKey(runID, turnID, stepID)
		if previous, ok := v.terminalStep[key]; ok {
			return fmt.Errorf("duplicate terminal step event for %q: got %s after %s", stepID, event.Type, previous)
		}
		v.terminalStep[key] = event.Type
	case protocol.EventModelCallStarted, protocol.EventModelCallFinished, protocol.EventAssistantDelta, protocol.EventAssistantReasoning, protocol.EventProviderUsageUpdate:
		if err := v.requireKnownStep(event.Type, runID, turnID, stepID); err != nil {
			return err
		}
	case protocol.EventContextThreshold, protocol.EventContextCompacted, protocol.EventProviderHelperUsage:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
	case protocol.EventToolCallRequested:
		if err := v.requireKnownRun(event.Type, runID); err != nil {
			return err
		}
		if callID == "" {
			return fmt.Errorf("%s missing call_id", event.Type)
		}
		key := callKey(runID, callID)
		if _, ok := v.calls[key]; ok {
			return fmt.Errorf("duplicate %s for call_id %q", event.Type, callID)
		}
		v.calls[key] = struct{}{}
	case protocol.EventToolPermissionRequested, protocol.EventToolPermissionDecided, protocol.EventToolAudit:
		if err := v.requireKnownCall(event.Type, runID, callID); err != nil {
			return err
		}
	case protocol.EventToolCallProgress:
		if err := v.requireKnownCall(event.Type, runID, callID); err != nil {
			return err
		}
		phase := lifecycleDataString(event.Data, "phase")
		if attemptID != "" {
			key := attemptKey(runID, attemptID)
			if previous, ok := v.terminalAttempts[key]; ok && !allowedPostTerminalProgressPhase(phase) {
				return fmt.Errorf("%s after terminal tool attempt %q: got phase %q after %s", event.Type, attemptID, phase, previous)
			}
			if phase == "attempt_started" {
				if previousCall, ok := v.attemptCalls[key]; ok && previousCall != callID {
					return fmt.Errorf("%s attempt_id %q was started for call_id %q, got call_id %q", event.Type, attemptID, previousCall, callID)
				}
				v.attemptCalls[key] = callID
			}
			if _, ok := v.attempts[key]; !ok && phase != "attempt_started" {
				return fmt.Errorf("%s without matching attempt_id %q", event.Type, attemptID)
			}
			if _, ok := v.attempts[key]; ok {
				if err := v.requireKnownAttemptForCall(event.Type, runID, callID, attemptID); err != nil {
					return err
				}
			}
		}
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
		key := attemptKey(runID, attemptID)
		if previousCall, ok := v.attemptCalls[key]; ok {
			if previousCall != callID {
				return fmt.Errorf("%s attempt_id %q was started for call_id %q, got call_id %q", event.Type, attemptID, previousCall, callID)
			}
			if _, ok := v.attempts[key]; ok {
				return fmt.Errorf("duplicate %s for attempt_id %q", event.Type, attemptID)
			}
		}
		if previous, ok := v.terminalAttempts[key]; ok {
			return fmt.Errorf("%s after terminal tool attempt %q: got start after %s", event.Type, attemptID, previous)
		}
		v.attempts[key] = struct{}{}
		v.attemptCalls[key] = callID
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
		if err := v.requireKnownAttemptForCall(event.Type, runID, callID, attemptID); err != nil {
			return err
		}
		key := attemptKey(runID, attemptID)
		if previous, ok := v.terminalAttempts[key]; ok {
			return fmt.Errorf("duplicate terminal tool attempt event for %q: got %s after %s", attemptID, event.Type, previous)
		}
		if outputRef := lifecycleDataString(event.Data, "output_ref"); outputRef != "" {
			if _, ok := v.outputRefs[key]; !ok {
				return fmt.Errorf("%s for attempt_id %q references output_ref without settled output_ref event", event.Type, attemptID)
			}
		}
		v.terminalAttempts[key] = event.Type
	case protocol.EventToolOutputRefCreated:
		if err := v.requireKnownCall(event.Type, runID, callID); err != nil {
			return err
		}
		if err := v.requireKnownAttemptForCall(event.Type, runID, callID, attemptID); err != nil {
			return err
		}
		v.outputRefs[attemptKey(runID, attemptID)] = struct{}{}
	case protocol.EventUserInputRequested:
		if err := v.requireKnownCall(event.Type, runID, callID); err != nil {
			return err
		}
		if err := v.requireKnownAttemptForCall(event.Type, runID, callID, attemptID); err != nil {
			return err
		}
		if requestID := lifecycleDataString(event.Data, "request_id"); requestID != "" {
			v.userInputs[userInputKey(runID, requestID)] = struct{}{}
		}
	case protocol.EventUserInputAnswered, protocol.EventUserInputRejected:
		if err := v.requireKnownCall(event.Type, runID, callID); err != nil {
			return err
		}
		if err := v.requireKnownAttemptForCall(event.Type, runID, callID, attemptID); err != nil {
			return err
		}
		if requestID := lifecycleDataString(event.Data, "request_id"); requestID != "" {
			if _, ok := v.userInputs[userInputKey(runID, requestID)]; !ok {
				return fmt.Errorf("%s without matching user_input.requested %q", event.Type, requestID)
			}
		}
	case protocol.EventHookStarted, protocol.EventHookFinished, protocol.EventHookFailed:
		if callID != "" {
			if err := v.requireKnownCall(event.Type, runID, callID); err != nil {
				return err
			}
		}
		if attemptID != "" {
			if err := v.requireKnownAttemptForCall(event.Type, runID, callID, attemptID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *LifecycleValidator) ValidateClosed() error {
	if v == nil {
		return nil
	}
	v.ensure()
	for runID := range v.runs {
		if _, ok := v.terminalRun[runID]; !ok {
			return fmt.Errorf("run %q has no terminal event", runID)
		}
	}
	for key := range v.turns {
		if _, ok := v.terminalTurn[key]; !ok {
			return fmt.Errorf("turn %q has no terminal event", lastKeyPart(key))
		}
	}
	for key := range v.steps {
		if _, ok := v.terminalStep[key]; !ok {
			return fmt.Errorf("step %q has no terminal event", lastKeyPart(key))
		}
	}
	for key := range v.attempts {
		if _, ok := v.terminalAttempts[key]; !ok {
			return fmt.Errorf("tool attempt %q has no terminal event", lastKeyPart(key))
		}
	}
	return nil
}

func (v *LifecycleValidator) requireKnownRun(eventType protocol.EventType, runID string) error {
	if runID == "" {
		return fmt.Errorf("%s missing run_id", eventType)
	}
	if _, ok := v.runs[runID]; !ok {
		return fmt.Errorf("%s without started run %q", eventType, runID)
	}
	return nil
}

func (v *LifecycleValidator) requireKnownStep(eventType protocol.EventType, runID, turnID, stepID string) error {
	if err := v.requireKnownRun(eventType, runID); err != nil {
		return err
	}
	if turnID == "" {
		return fmt.Errorf("%s missing turn_id", eventType)
	}
	if _, ok := v.turns[turnKey(runID, turnID)]; !ok {
		return fmt.Errorf("%s without started turn %q", eventType, turnID)
	}
	if stepID == "" {
		return fmt.Errorf("%s missing step_id", eventType)
	}
	if _, ok := v.steps[stepKey(runID, turnID, stepID)]; !ok {
		return fmt.Errorf("%s without started step %q", eventType, stepID)
	}
	return nil
}

func (v *LifecycleValidator) requireKnownCall(eventType protocol.EventType, runID, callID string) error {
	if err := v.requireKnownRun(eventType, runID); err != nil {
		return err
	}
	if callID == "" {
		return fmt.Errorf("%s missing call_id", eventType)
	}
	if _, ok := v.calls[callKey(runID, callID)]; !ok {
		return fmt.Errorf("%s without matching call_id %q", eventType, callID)
	}
	return nil
}

func (v *LifecycleValidator) requireKnownAttempt(eventType protocol.EventType, runID, attemptID string) error {
	if err := v.requireKnownRun(eventType, runID); err != nil {
		return err
	}
	if attemptID == "" {
		return fmt.Errorf("%s missing attempt_id", eventType)
	}
	if _, ok := v.attempts[attemptKey(runID, attemptID)]; !ok {
		return fmt.Errorf("%s without matching attempt_id %q", eventType, attemptID)
	}
	return nil
}

func (v *LifecycleValidator) requireKnownAttemptForCall(eventType protocol.EventType, runID, callID, attemptID string) error {
	if err := v.requireKnownAttempt(eventType, runID, attemptID); err != nil {
		return err
	}
	if callID == "" {
		return fmt.Errorf("%s missing call_id", eventType)
	}
	key := attemptKey(runID, attemptID)
	if previousCall, ok := v.attemptCalls[key]; ok && previousCall != callID {
		return fmt.Errorf("%s attempt_id %q was started for call_id %q, got call_id %q", eventType, attemptID, previousCall, callID)
	}
	return nil
}

func allowedPostTerminalProgressPhase(phase string) bool {
	switch phase {
	case "attempt_finished", "cancel_abort", "retry_decision", "finalize":
		return true
	default:
		return false
	}
}

func lifecycleDataString(value any, key string) string {
	switch data := value.(type) {
	case protocol.ToolProgressEvent:
		return lifecycleToolProgressString(data, key)
	case *protocol.ToolProgressEvent:
		if data == nil {
			return ""
		}
		return lifecycleToolProgressString(*data, key)
	case protocol.ToolResult:
		return lifecycleToolResultString(data, key)
	case *protocol.ToolResult:
		if data == nil {
			return ""
		}
		return lifecycleToolResultString(*data, key)
	case protocol.ToolOutputRefEvent:
		return lifecycleToolOutputRefString(data, key)
	case *protocol.ToolOutputRefEvent:
		if data == nil {
			return ""
		}
		return lifecycleToolOutputRefString(*data, key)
	case protocol.UserInputRequestEvent:
		return lifecycleUserInputRequestString(data, key)
	case *protocol.UserInputRequestEvent:
		if data == nil {
			return ""
		}
		return lifecycleUserInputRequestString(*data, key)
	case protocol.UserInputAnswerEvent:
		return lifecycleUserInputAnswerString(data, key)
	case *protocol.UserInputAnswerEvent:
		if data == nil {
			return ""
		}
		return lifecycleUserInputAnswerString(*data, key)
	case protocol.UserInputRejectEvent:
		return lifecycleUserInputRejectString(data, key)
	case *protocol.UserInputRejectEvent:
		if data == nil {
			return ""
		}
		return lifecycleUserInputRejectString(*data, key)
	case map[string]any:
		if s, ok := data[key].(string); ok {
			return strings.TrimSpace(s)
		}
	case json.RawMessage:
		return lifecycleRawDataString(data, key)
	case []byte:
		return lifecycleRawDataString(json.RawMessage(data), key)
	}
	return ""
}

func lifecycleToolProgressString(progress protocol.ToolProgressEvent, key string) string {
	switch key {
	case "phase":
		return strings.TrimSpace(progress.Phase)
	default:
		return ""
	}
}

func lifecycleToolResultString(result protocol.ToolResult, key string) string {
	switch key {
	case "output_ref":
		return strings.TrimSpace(result.OutputRef)
	default:
		return ""
	}
}

func lifecycleToolOutputRefString(ref protocol.ToolOutputRefEvent, key string) string {
	switch key {
	case "output_ref":
		return strings.TrimSpace(ref.OutputRef)
	default:
		return ""
	}
}

func lifecycleUserInputRequestString(input protocol.UserInputRequestEvent, key string) string {
	switch key {
	case "request_id":
		return strings.TrimSpace(input.RequestID)
	default:
		return ""
	}
}

func lifecycleUserInputAnswerString(input protocol.UserInputAnswerEvent, key string) string {
	switch key {
	case "request_id":
		return strings.TrimSpace(input.RequestID)
	default:
		return ""
	}
}

func lifecycleUserInputRejectString(input protocol.UserInputRejectEvent, key string) string {
	switch key {
	case "request_id":
		return strings.TrimSpace(input.RequestID)
	default:
		return ""
	}
}

func lifecycleRawDataString(raw json.RawMessage, key string) string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	if s, ok := data[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func (v *LifecycleValidator) ensure() {
	if v.runs == nil {
		v.runs = map[string]struct{}{}
	}
	if v.terminalRun == nil {
		v.terminalRun = map[string]protocol.EventType{}
	}
	if v.turns == nil {
		v.turns = map[string]struct{}{}
	}
	if v.terminalTurn == nil {
		v.terminalTurn = map[string]protocol.EventType{}
	}
	if v.steps == nil {
		v.steps = map[string]stepState{}
	}
	if v.terminalStep == nil {
		v.terminalStep = map[string]protocol.EventType{}
	}
	if v.calls == nil {
		v.calls = map[string]struct{}{}
	}
	if v.attempts == nil {
		v.attempts = map[string]struct{}{}
	}
	if v.attemptCalls == nil {
		v.attemptCalls = map[string]string{}
	}
	if v.terminalAttempts == nil {
		v.terminalAttempts = map[string]protocol.EventType{}
	}
	if v.outputRefs == nil {
		v.outputRefs = map[string]struct{}{}
	}
	if v.userInputs == nil {
		v.userInputs = map[string]struct{}{}
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

func userInputKey(runID, requestID string) string {
	return runID + "\x00" + requestID
}

func lastKeyPart(key string) string {
	parts := strings.Split(key, "\x00")
	if len(parts) == 0 {
		return key
	}
	return parts[len(parts)-1]
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneEventTypeMap(in map[string]protocol.EventType) map[string]protocol.EventType {
	out := make(map[string]protocol.EventType, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStepStateMap(in map[string]stepState) map[string]stepState {
	out := make(map[string]stepState, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
