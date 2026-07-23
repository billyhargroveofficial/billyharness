package jobs

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

var (
	// ErrInvalidEvent reports an event whose envelope or typed payload is
	// malformed.
	ErrInvalidEvent = errors.New("invalid job event")
	// ErrInvalidTransition reports an event that is valid in isolation but not
	// legal from the current job state.
	ErrInvalidTransition = errors.New("invalid job transition")
	// ErrTerminalState reports an attempt to mutate an immutable terminal job.
	ErrTerminalState = errors.New("job is terminal")
	// ErrUsageOverflow reports usage accounting that cannot be represented.
	ErrUsageOverflow = errors.New("job usage overflow")
)

// StagnationFingerprintLimit is the number of identical consecutive
// supervisor fingerprints that terminates a continued job as stagnant.
const StagnationFingerprintLimit = 3

// Reduce applies one event to a job state without consulting a clock,
// provider, filesystem, or other ambient state. Event.At is the sole clock
// input, which makes the result deterministic and replayable.
//
// Every accepted, state-changing event increments Revision exactly once. A
// replay of the most recently accepted event is idempotent. Terminal states
// reject every event except the exact event replay that emitted them.
func Reduce(state JobState, event Event) (JobState, error) {
	next := cloneJobState(state)
	if err := validateEventEnvelope(event); err != nil {
		return state, err
	}
	if !validEventType(event.Type) {
		return state, eventError(event.Type, "unknown event type")
	}

	if next.LastEventID == event.ID {
		return next, nil
	}
	if next.Status.IsTerminal() {
		return state, fmt.Errorf("%w: status %q, terminal reason %q", ErrTerminalState, next.Status, next.TerminalReason)
	}
	if err := validateReducerState(next); err != nil {
		return state, err
	}

	// Operator cancellation is the highest-priority terminal condition.
	if next.CancelRequested || event.Type == EventJobCancelled {
		next.CancelRequested = true
		return emitTerminal(next, event, JobStatusCancelled, TerminalReasonOperatorCancellation), nil
	}

	// Deadline is evaluated before budget and before transition-specific model
	// payload checks. A late supervisor completion cannot win a race with a hard
	// policy limit.
	if event.Type == EventDeadlineExceeded || deadlineReached(next.Spec, event) {
		return emitTerminal(next, event, JobStatusFailed, TerminalReasonDeadline), nil
	}

	// Events that do not add usage are stopped immediately when prior usage has
	// already reached a hard cap. Resource-reporting events are applied below
	// before terminal emission so their final factual payload is not discarded.
	if event.Type != EventAttemptRecorded &&
		event.Type != EventBatchCompleted &&
		event.Type != EventUsageRecorded {
		if exceeded, _ := next.Spec.Budget.ExceededBy(next.Usage); exceeded {
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
		}
	}

	switch event.Type {
	case EventJobStarted:
		if next.Status != JobStatusQueued {
			return state, transitionError(next.Status, event.Type)
		}
	case EventBatchStarted:
		if next.Status != JobStatusRunning || next.CurrentBatch != nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Batch == nil {
			return state, eventError(event.Type, "batch is required")
		}
		if err := validateBatchForJob(next, *event.Batch); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if err := validateBatchMatchesDecision(next, *event.Batch); err != nil {
			return state, eventError(event.Type, err.Error())
		}
	case EventAttemptRecorded:
		if next.Status != JobStatusRunning || next.CurrentBatch == nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Attempt == nil {
			return state, eventError(event.Type, "attempt is required")
		}
		if err := validateAttemptForBatch(next, *event.Attempt); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if err := validateNewArtifacts(next.Artifacts, event.Attempt.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		for _, artifact := range event.Attempt.Artifacts {
			if artifact.CreatedByAttemptID != "" && artifact.CreatedByAttemptID != event.Attempt.ID {
				return state, eventError(event.Type, fmt.Sprintf(
					"artifact %q names creating attempt %q, want %q",
					artifact.ID,
					artifact.CreatedByAttemptID,
					event.Attempt.ID,
				))
			}
		}
	case EventBatchCompleted:
		if next.Status != JobStatusRunning || next.CurrentBatch == nil {
			return state, transitionError(next.Status, event.Type)
		}
		if strings.TrimSpace(event.BatchID) == "" {
			return state, eventError(event.Type, "batch ID is required")
		}
		if event.BatchID != next.CurrentBatch.ID {
			return state, eventError(event.Type, fmt.Sprintf("batch ID %q does not match active batch %q", event.BatchID, next.CurrentBatch.ID))
		}
		if err := validateBatchBarrier(next); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if err := validateNewArtifacts(next.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
	case EventUsageRecorded:
		if next.Status != JobStatusRunning && next.Status != JobStatusWaiting {
			return state, transitionError(next.Status, event.Type)
		}
		if err := validateNewArtifacts(next.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
	case EventDecisionMade:
		if next.Status != JobStatusRunning || next.CurrentBatch != nil {
			return state, transitionError(next.Status, event.Type)
		}
		if pendingDecisionBatch(next) {
			return state, eventError(event.Type, "previous continue decision still has an unstarted batch")
		}
		if event.Decision == nil {
			return state, eventError(event.Type, "decision is required")
		}
		if !validDecisionKind(event.Decision.Kind) {
			return state, eventError(event.Type, fmt.Sprintf("unknown decision kind %q", event.Decision.Kind))
		}
	case EventJobPaused:
		if next.Status != JobStatusQueued && next.Status != JobStatusRunning && next.Status != JobStatusWaiting {
			return state, transitionError(next.Status, event.Type)
		}
	case EventJobResumed:
		if next.Status != JobStatusPaused {
			return state, transitionError(next.Status, event.Type)
		}
	case EventJobFailed:
		if !validFailureReason(event.Reason) {
			return state, eventError(event.Type, fmt.Sprintf("failure reason %q is not allowed", event.Reason))
		}
	case EventDeadlineExceeded:
		// The explicit deadline event is valid from every non-terminal state.
	}

	usage, err := usageAfterEvent(next, event)
	if err != nil {
		return state, err
	}
	next.Usage = usage

	// A decision made after any hard budget is fully consumed is a budget
	// terminal, even when the model proposes completion. Factual attempt,
	// barrier, and usage payloads are still recorded before emitting the
	// terminal so the final consumed unit is not lost from replay state.
	budgetTerminal, _ := next.Spec.Budget.ExceededBy(next.Usage)
	if budgetTerminal &&
		event.Type != EventAttemptRecorded &&
		event.Type != EventBatchCompleted &&
		event.Type != EventUsageRecorded {
		return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
	}

	switch event.Type {
	case EventJobStarted:
		next.Status = JobStatusRunning
	case EventBatchStarted:
		batch := cloneWorkBatch(*event.Batch)
		next.CurrentBatch = &batch
		next.Cycle = batch.Cycle
	case EventAttemptRecorded:
		attempt := cloneAttempt(*event.Attempt)
		next.Attempts = append(next.Attempts, attempt)
		sortAttempts(next.Attempts)
		next.Artifacts = append(next.Artifacts, cloneArtifacts(attempt.Artifacts)...)
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
	case EventBatchCompleted:
		next.CurrentBatch = nil
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
	case EventUsageRecorded:
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
	case EventDecisionMade:
		return applyDecision(next, event)
	case EventJobPaused:
		next.Status = JobStatusPaused
	case EventJobResumed:
		next.Status = JobStatusRunning
		next.WaitingReason = ""
	case EventJobFailed:
		return emitTerminal(next, event, JobStatusFailed, event.Reason), nil
	}

	if budgetTerminal {
		return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
	}
	return acceptEvent(next, event), nil
}

func applyDecision(state JobState, event Event) (JobState, error) {
	decision := cloneDecision(*event.Decision)
	if err := decision.Validate(); err != nil {
		return state, eventError(event.Type, err.Error())
	}

	switch decision.Kind {
	case DecisionContinue:
		if decision.NextBatch == nil {
			return state, eventError(event.Type, "continue decision requires a next batch")
		}
		if err := validateBatchForJob(state, *decision.NextBatch); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if strings.TrimSpace(decision.Fingerprint) == "" {
			return state, eventError(event.Type, "continue decision requires a stagnation fingerprint")
		}
		state.LastDecision = &decision
		state.StagnationFingerprints = append(state.StagnationFingerprints, decision.Fingerprint)
		if consecutiveFingerprintCount(state.StagnationFingerprints, decision.Fingerprint) >= StagnationFingerprintLimit {
			return emitTerminal(state, event, JobStatusFailed, TerminalReasonStagnation), nil
		}
		state.Status = JobStatusRunning
		state.WaitingReason = ""
	case DecisionComplete:
		state.LastDecision = &decision
		return emitTerminal(state, event, JobStatusCompleted, TerminalReasonSuccess), nil
	case DecisionWait:
		state.LastDecision = &decision
		state.Status = JobStatusWaiting
		state.WaitingReason = decision.Reason
	case DecisionBlocked:
		state.LastDecision = &decision
		return emitTerminal(state, event, JobStatusFailed, TerminalReasonBlocked), nil
	default:
		return state, eventError(event.Type, fmt.Sprintf("unknown decision kind %q", decision.Kind))
	}
	return acceptEvent(state, event), nil
}

func validateEventEnvelope(event Event) error {
	if strings.TrimSpace(event.ID) == "" {
		return eventError(event.Type, "event ID is required")
	}
	if event.At.IsZero() {
		return eventError(event.Type, "event time is required")
	}
	for index, artifact := range event.Artifacts {
		if err := artifact.Validate(); err != nil {
			return eventError(event.Type, fmt.Sprintf("artifact %d: %s", index, err))
		}
	}
	return nil
}

func validateReducerState(state JobState) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("%w: state: %s", ErrInvalidTransition, err)
	}
	if state.Revision == math.MaxUint64 {
		return fmt.Errorf("%w: revision", ErrUsageOverflow)
	}
	return nil
}

func validateBatchForJob(state JobState, batch WorkBatch) error {
	if err := ValidateBatchForSpec(state.Spec, batch); err != nil {
		return err
	}
	if batch.Cycle != state.Cycle+1 {
		return fmt.Errorf("batch cycle %d must follow current cycle %d", batch.Cycle, state.Cycle)
	}
	if state.Spec.Budget.MaxAttempts > 0 {
		remaining := state.Spec.Budget.MaxAttempts - min(state.Spec.Budget.MaxAttempts, state.Usage.Attempts)
		if uint64(len(batch.Items)) > remaining {
			return fmt.Errorf("batch requires %d attempts but only %d remain", len(batch.Items), remaining)
		}
	}

	return nil
}

func validateBatchMatchesDecision(state JobState, batch WorkBatch) error {
	if state.Cycle == 0 && state.LastDecision == nil {
		return nil
	}
	if state.LastDecision == nil ||
		state.LastDecision.Kind != DecisionContinue ||
		state.LastDecision.NextBatch == nil {
		return fmt.Errorf("batch %q was not proposed by a continue decision", batch.ID)
	}
	if !equalWorkBatch(*state.LastDecision.NextBatch, batch) {
		return fmt.Errorf("batch %q differs from the supervisor-proposed batch", batch.ID)
	}
	return nil
}

func pendingDecisionBatch(state JobState) bool {
	return state.LastDecision != nil &&
		state.LastDecision.Kind == DecisionContinue &&
		state.LastDecision.NextBatch != nil &&
		state.LastDecision.NextBatch.Cycle == state.Cycle+1
}

func validateBatchBarrier(state JobState) error {
	completed := make(map[string]bool, len(state.CurrentBatch.Items))
	for _, attempt := range state.Attempts {
		if attempt.BatchID != state.CurrentBatch.ID {
			continue
		}
		switch attempt.Status {
		case AttemptStatusSucceeded, AttemptStatusFailed, AttemptStatusCancelled:
			completed[attempt.WorkItemID] = true
		}
	}
	for _, item := range state.CurrentBatch.Items {
		if !completed[item.ID] {
			return fmt.Errorf("all barrier is waiting for work item %q", item.ID)
		}
	}
	return nil
}

func validateAttemptForBatch(state JobState, attempt Attempt) error {
	if err := attempt.Validate(); err != nil {
		return err
	}
	if attempt.BatchID != state.CurrentBatch.ID {
		return fmt.Errorf("attempt batch %q does not match active batch %q", attempt.BatchID, state.CurrentBatch.ID)
	}
	var workItem *WorkItem
	for i := range state.CurrentBatch.Items {
		if state.CurrentBatch.Items[i].ID == attempt.WorkItemID {
			workItem = &state.CurrentBatch.Items[i]
			break
		}
	}
	if workItem == nil {
		return fmt.Errorf("attempt references unknown work item %q", attempt.WorkItemID)
	}
	if attempt.RoleID != workItem.RoleID {
		return fmt.Errorf("attempt role %q does not match work item role %q", attempt.RoleID, workItem.RoleID)
	}
	for _, recorded := range state.Attempts {
		if recorded.ID == attempt.ID {
			return fmt.Errorf("attempt ID %q is duplicated", attempt.ID)
		}
	}
	return nil
}

func validateNewArtifacts(existing []ArtifactRef, groups ...[]ArtifactRef) error {
	seen := make(map[string]struct{}, len(existing))
	for _, artifact := range existing {
		seen[artifact.ID] = struct{}{}
	}
	for _, group := range groups {
		for _, artifact := range group {
			if _, duplicate := seen[artifact.ID]; duplicate {
				return fmt.Errorf("artifact ID %q is duplicated", artifact.ID)
			}
			seen[artifact.ID] = struct{}{}
		}
	}
	return nil
}

func usageAfterEvent(state JobState, event Event) (Usage, error) {
	delta := Usage{}
	switch event.Type {
	case EventBatchStarted:
		// Cycle usage is charged only after the all barrier closes. Charging at
		// start would make a one-cycle budget terminate before its first batch.
	case EventBatchCompleted:
		delta.Cycles = 1
	case EventAttemptRecorded:
		delta = event.Attempt.Usage
		if delta.Cycles != 0 {
			return state.Usage, eventError(event.Type, "attempt usage cannot record cycles")
		}
		if delta.Attempts > 1 {
			return state.Usage, eventError(event.Type, "one attempt cannot record more than one attempt")
		}
		delta.Attempts = 1
	case EventUsageRecorded:
		if event.Usage.Cycles != 0 || event.Usage.Attempts != 0 {
			return state.Usage, eventError(event.Type, "standalone usage cannot record cycles or attempts")
		}
		delta = event.Usage
	}
	return addUsage(state.Usage, delta)
}

func addUsage(left, right Usage) (Usage, error) {
	var out Usage
	var ok bool
	if out.Cycles, ok = addUint64(left.Cycles, right.Cycles); !ok {
		return left, fmt.Errorf("%w: cycles", ErrUsageOverflow)
	}
	if out.Attempts, ok = addUint64(left.Attempts, right.Attempts); !ok {
		return left, fmt.Errorf("%w: attempts", ErrUsageOverflow)
	}
	if out.ModelCalls, ok = addUint64(left.ModelCalls, right.ModelCalls); !ok {
		return left, fmt.Errorf("%w: model calls", ErrUsageOverflow)
	}
	if out.InputTokens, ok = addUint64(left.InputTokens, right.InputTokens); !ok {
		return left, fmt.Errorf("%w: input tokens", ErrUsageOverflow)
	}
	if out.OutputTokens, ok = addUint64(left.OutputTokens, right.OutputTokens); !ok {
		return left, fmt.Errorf("%w: output tokens", ErrUsageOverflow)
	}
	if _, ok = addUint64(out.InputTokens, out.OutputTokens); !ok {
		return left, fmt.Errorf("%w: total tokens", ErrUsageOverflow)
	}
	return out, nil
}

func addUint64(left, right uint64) (uint64, bool) {
	if math.MaxUint64-left < right {
		return 0, false
	}
	return left + right, true
}

func deadlineReached(spec JobSpec, event Event) bool {
	return !spec.Deadline.IsZero() && !event.At.Before(spec.Deadline)
}

func emitTerminal(state JobState, event Event, status JobStatus, reason TerminalReason) JobState {
	state.Status = status
	state.TerminalReason = reason
	state.CurrentBatch = nil
	state.WaitingReason = ""
	return acceptEvent(state, event)
}

func acceptEvent(state JobState, event Event) JobState {
	state.Revision++
	state.LastEventID = event.ID
	return state
}

func validDecisionKind(kind DecisionKind) bool {
	switch kind {
	case DecisionContinue, DecisionComplete, DecisionWait, DecisionBlocked:
		return true
	default:
		return false
	}
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventJobStarted,
		EventBatchStarted,
		EventAttemptRecorded,
		EventBatchCompleted,
		EventUsageRecorded,
		EventDecisionMade,
		EventJobPaused,
		EventJobResumed,
		EventJobCancelled,
		EventJobFailed,
		EventDeadlineExceeded:
		return true
	default:
		return false
	}
}

func validFailureReason(reason TerminalReason) bool {
	switch reason {
	case TerminalReasonBlocked, TerminalReasonUnrecoverable:
		return true
	default:
		return false
	}
}

func consecutiveFingerprintCount(fingerprints []string, fingerprint string) int {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return 0
	}
	count := 0
	for i := len(fingerprints) - 1; i >= 0; i-- {
		if fingerprints[i] != fingerprint {
			break
		}
		count++
	}
	return count
}

func cloneJobState(state JobState) JobState {
	out := state
	if state.CurrentBatch != nil {
		batch := cloneWorkBatch(*state.CurrentBatch)
		out.CurrentBatch = &batch
	}
	out.Attempts = make([]Attempt, len(state.Attempts))
	for i := range state.Attempts {
		out.Attempts[i] = cloneAttempt(state.Attempts[i])
	}
	out.Artifacts = cloneArtifacts(state.Artifacts)
	if state.LastDecision != nil {
		decision := cloneDecision(*state.LastDecision)
		out.LastDecision = &decision
	}
	out.StagnationFingerprints = slices.Clone(state.StagnationFingerprints)
	return out
}

func cloneWorkBatch(batch WorkBatch) WorkBatch {
	out := batch
	out.Items = make([]WorkItem, len(batch.Items))
	for index, item := range batch.Items {
		out.Items[index] = item
		out.Items[index].Authority = cloneAuthority(item.Authority)
	}
	return out
}

func cloneAuthority(authority Authority) Authority {
	out := authority
	out.Tools = slices.Clone(authority.Tools)
	out.ReadRoots = slices.Clone(authority.ReadRoots)
	out.WriteRoots = slices.Clone(authority.WriteRoots)
	out.NetworkHosts = slices.Clone(authority.NetworkHosts)
	out.Providers = slices.Clone(authority.Providers)
	return out
}

func equalWorkBatch(left, right WorkBatch) bool {
	if left.ID != right.ID ||
		left.StageID != right.StageID ||
		left.Cycle != right.Cycle ||
		left.Barrier != right.Barrier ||
		len(left.Items) != len(right.Items) {
		return false
	}
	for index := range left.Items {
		leftItem, rightItem := left.Items[index], right.Items[index]
		if leftItem.ID != rightItem.ID ||
			leftItem.RoleID != rightItem.RoleID ||
			leftItem.Objective != rightItem.Objective ||
			!equalAuthority(leftItem.Authority, rightItem.Authority) {
			return false
		}
	}
	return true
}

func equalAuthority(left, right Authority) bool {
	return left.Mode == right.Mode &&
		slices.Equal(left.Tools, right.Tools) &&
		slices.Equal(left.ReadRoots, right.ReadRoots) &&
		slices.Equal(left.WriteRoots, right.WriteRoots) &&
		slices.Equal(left.NetworkHosts, right.NetworkHosts) &&
		slices.Equal(left.Providers, right.Providers)
}

func cloneAttempt(attempt Attempt) Attempt {
	out := attempt
	out.Artifacts = cloneArtifacts(attempt.Artifacts)
	return out
}

func cloneDecision(decision Decision) Decision {
	out := decision
	if decision.NextBatch != nil {
		batch := cloneWorkBatch(*decision.NextBatch)
		out.NextBatch = &batch
	}
	return out
}

func cloneArtifacts(artifacts []ArtifactRef) []ArtifactRef {
	return slices.Clone(artifacts)
}

func sortAttempts(attempts []Attempt) {
	slices.SortStableFunc(attempts, func(left, right Attempt) int {
		if cmp := strings.Compare(left.BatchID, right.BatchID); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(left.WorkItemID, right.WorkItemID); cmp != 0 {
			return cmp
		}
		return strings.Compare(left.ID, right.ID)
	})
}

func eventError(eventType EventType, message string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidEvent, eventType, message)
}

func transitionError(status JobStatus, eventType EventType) error {
	return fmt.Errorf("%w: event %q from status %q", ErrInvalidTransition, eventType, status)
}
