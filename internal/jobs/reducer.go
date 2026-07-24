package jobs

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidEvent      = errors.New("invalid job event")
	ErrInvalidTransition = errors.New("invalid job transition")
	ErrTerminalState     = errors.New("job is terminal")
	ErrUsageOverflow     = errors.New("job usage overflow")
)

// StagnationFingerprintLimit is the number of identical consecutive
// supervisor fingerprints that terminates a continued job as stagnant.
const StagnationFingerprintLimit = 3

// Reduce applies one provider-neutral event without consulting ambient state.
// Event.At is the only clock input. External work is represented by a durable
// AttemptStarted followed by AttemptFinished; this is the boundary that makes
// read retries honest and ambiguous writer recovery fail closed.
func Reduce(state JobState, event Event) (JobState, error) {
	next := cloneJobState(state)
	if err := validateEventEnvelope(event); err != nil {
		return state, err
	}
	if !validEventType(event.Type) {
		return state, eventError(event.Type, "unknown event type")
	}
	if err := validateEventShape(event); err != nil {
		return state, err
	}
	if next.LastEventID == event.ID {
		return state, eventError(event.Type, "event ID was already used; exact replay is handled by the store")
	}
	if next.Status.IsTerminal() {
		return state, fmt.Errorf("%w: status %q, terminal reason %q", ErrTerminalState, next.Status, next.TerminalReason)
	}
	if err := validateReducerState(next); err != nil {
		return state, err
	}

	// Cancellation is a request while calls are in flight. Their factual
	// finishes remain admissible; no new external work may start afterwards.
	if event.Type == EventCancellationRequested {
		return applyCancellationRequest(next, event)
	}
	if next.CancelRequested && event.Type != EventAttemptFinished && event.Type != EventUsageRecorded && event.Type != EventJobCancelled && event.Type != EventJobFailed {
		if hasRunningAttempts(next) {
			return state, eventError(event.Type, "cancellation is draining running attempts")
		}
		return emitTerminal(next, event, JobStatusCancelled, TerminalReasonOperatorCancellation), nil
	}
	if event.Type == EventJobCancelled {
		if hasRunningAttempts(next) {
			return state, eventError(event.Type, "running attempts require cancellation_requested before terminal cancellation")
		}
		next.CancelRequested = true
		return emitTerminal(next, event, JobStatusCancelled, TerminalReasonOperatorCancellation), nil
	}

	lateFinish := event.Type == EventAttemptFinished && deadlineReached(next.Spec, event)
	lateUsage := event.Type == EventUsageRecorded && deadlineReached(next.Spec, event)
	lateDrainEvent := lateFinish || lateUsage || (event.Type == EventJobFailed && deadlineReached(next.Spec, event))
	if event.Type == EventDeadlineExceeded || (deadlineReached(next.Spec, event) && !lateDrainEvent) {
		if hasRunningAttempts(next) {
			next.PendingStop = higherPriorityStop(next.PendingStop, TerminalReasonDeadline)
			return acceptEvent(next, event), nil
		}
		return emitTerminal(next, event, JobStatusFailed, TerminalReasonDeadline), nil
	}

	switch event.Type {
	case EventJobStarted:
		if next.Status != JobStatusQueued {
			return state, transitionError(next.Status, event.Type)
		}
		next.Status = JobStatusRunning

	case EventBatchStarted:
		if next.Status != JobStatusRunning || next.CurrentBatch != nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Batch == nil {
			return state, eventError(event.Type, "batch is required")
		}
		if budgetBlocksNewWork(next) {
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
		}
		if err := validateBatchForCursor(next, *event.Batch); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		batch := cloneWorkBatch(*event.Batch)
		next.CurrentBatch = &batch
		if next.NextStageIndex == 0 {
			next.Cycle = batch.Cycle
		}

	case EventAttemptStarted:
		if next.Status != JobStatusRunning || next.CurrentBatch == nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Attempt == nil {
			return state, eventError(event.Type, "attempt is required")
		}
		if budgetBlocksNewAttempt(next) {
			if hasRunningAttempts(next) {
				next.PendingStop = higherPriorityStop(next.PendingStop, TerminalReasonBudget)
				return acceptEvent(next, event), nil
			}
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
		}
		if err := validateAttemptStart(next, *event.Attempt); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		attempt := cloneAttempt(*event.Attempt)
		next.Attempts = append(next.Attempts, attempt)
		sortAttempts(next.Attempts)
		var err error
		next.Usage, err = addUsage(next.Usage, Usage{Attempts: 1})
		if err != nil {
			return state, err
		}

	case EventAttemptFinished:
		if next.Status != JobStatusRunning || next.CurrentBatch == nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Attempt == nil {
			return state, eventError(event.Type, "attempt is required")
		}
		index, err := validateAttemptFinish(next, *event.Attempt)
		if err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if err := validateNewArtifacts(next.Artifacts, event.Attempt.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		attempt := cloneAttempt(*event.Attempt)
		next.Attempts[index] = attempt
		next.Artifacts = append(next.Artifacts, cloneArtifacts(attempt.Artifacts)...)
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
		next.Usage, err = addUsage(next.Usage, attempt.Usage)
		if err != nil {
			return state, err
		}
		sortAttempts(next.Attempts)
		if attempt.Status == AttemptStatusAmbiguous {
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonUnrecoverable), nil
		}
		if lateFinish {
			next.PendingStop = higherPriorityStop(next.PendingStop, TerminalReasonDeadline)
		}
		if !hasRunningAttempts(next) && (next.CancelRequested || next.PendingStop != "") {
			return emitPendingStop(next, event), nil
		}

	case EventAttemptRecorded:
		// Compatibility-only atomic attempt. It still has to satisfy the new
		// identity/cursor rules and is never emitted by the durable runtime.
		if next.Status != JobStatusRunning || next.CurrentBatch == nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Attempt == nil {
			return state, eventError(event.Type, "attempt is required")
		}
		if budgetBlocksNewAttempt(next) {
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
		}
		if err := validateAtomicAttempt(next, *event.Attempt); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if err := validateNewArtifacts(next.Artifacts, event.Attempt.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		attempt := cloneAttempt(*event.Attempt)
		next.Attempts = append(next.Attempts, attempt)
		sortAttempts(next.Attempts)
		next.Artifacts = append(next.Artifacts, cloneArtifacts(attempt.Artifacts)...)
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
		delta := attempt.Usage
		delta.Attempts = 1
		var err error
		next.Usage, err = addUsage(next.Usage, delta)
		if err != nil {
			return state, err
		}

	case EventBatchCompleted:
		if next.Status != JobStatusRunning || next.CurrentBatch == nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.BatchID == "" || event.BatchID != next.CurrentBatch.ID {
			return state, eventError(event.Type, "batch_id must match the active batch")
		}
		if err := validateBatchBarrier(next); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if err := validateNewArtifacts(next.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		completed := CompletedBatch{
			ID:      next.CurrentBatch.ID,
			StageID: next.CurrentBatch.StageID,
			Cycle:   next.CurrentBatch.Cycle,
		}
		next.CompletedBatches = append(next.CompletedBatches, completed)
		next.CurrentBatch = nil
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
		next.NextStageIndex++
		finalStage := next.NextStageIndex == len(next.Spec.Workflow.StageOrder)
		if finalStage {
			var err error
			next.Usage, err = addUsage(next.Usage, Usage{Cycles: 1})
			if err != nil {
				return state, err
			}
		} else if budgetBlocksNewWork(next) {
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
		}

	case EventDecisionMade:
		if next.Status != JobStatusRunning || next.CurrentBatch != nil {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Decision == nil {
			return state, eventError(event.Type, "decision is required")
		}
		if next.NextStageIndex != len(next.Spec.Workflow.StageOrder) {
			return state, eventError(event.Type, "decision requires the completed supervisor barrier")
		}
		persisted, err := persistedSupervisorDecision(next)
		if err != nil {
			return state, eventError(event.Type, err.Error())
		}
		if !equalDecision(*persisted, *event.Decision) {
			return state, eventError(event.Type, "decision differs from the persisted supervisor attempt")
		}
		return applyDecision(next, event)

	case EventUsageRecorded:
		if next.Status != JobStatusRunning && next.Status != JobStatusWaiting {
			return state, transitionError(next.Status, event.Type)
		}
		if event.Usage.Cycles != 0 || event.Usage.Attempts != 0 {
			return state, eventError(event.Type, "standalone usage cannot record cycles or attempts")
		}
		if err := validateNewArtifacts(next.Artifacts, event.Artifacts); err != nil {
			return state, eventError(event.Type, err.Error())
		}
		var err error
		next.Usage, err = addUsage(next.Usage, event.Usage)
		if err != nil {
			return state, err
		}
		next.Artifacts = append(next.Artifacts, cloneArtifacts(event.Artifacts)...)
		if lateUsage {
			next.PendingStop = higherPriorityStop(next.PendingStop, TerminalReasonDeadline)
		}
		if budgetBlocksNewWork(next) {
			if hasRunningAttempts(next) {
				next.PendingStop = higherPriorityStop(next.PendingStop, TerminalReasonBudget)
				return acceptEvent(next, event), nil
			}
			return emitTerminal(next, event, JobStatusFailed, TerminalReasonBudget), nil
		}

	case EventJobPaused:
		if next.CurrentBatch != nil || (next.Status != JobStatusQueued && next.Status != JobStatusRunning && next.Status != JobStatusWaiting) {
			return state, transitionError(next.Status, event.Type)
		}
		next.Status = JobStatusPaused

	case EventJobResumed:
		if next.Status != JobStatusPaused && next.Status != JobStatusWaiting {
			return state, transitionError(next.Status, event.Type)
		}
		if !next.NextWakeAt.IsZero() && event.At.Before(next.NextWakeAt) {
			if next.Status == JobStatusWaiting {
				return state, eventError(event.Type, fmt.Sprintf("scheduled cadence wait continues until %s", next.NextWakeAt.Format(time.RFC3339Nano)))
			}
			// An operator pause must not become a way to bypass scheduler-owned
			// pacing. Resume returns the job to its persisted cadence wait.
			next.Status = JobStatusWaiting
			return acceptEvent(next, event), nil
		}
		next.Status = JobStatusRunning
		next.WaitingReason = ""
		next.NextWakeAt = time.Time{}
		if next.NextStageIndex == len(next.Spec.Workflow.StageOrder) && next.LastDecision != nil && next.LastDecision.Kind == DecisionWait {
			next.NextStageIndex = 0
		}

	case EventJobFailed:
		if !validFailureReason(event.Reason) {
			return state, eventError(event.Type, fmt.Sprintf("failure reason %q is not allowed", event.Reason))
		}
		reason := higherPriorityStop(next.PendingStop, event.Reason)
		if deadlineReached(next.Spec, event) {
			reason = higherPriorityStop(reason, TerminalReasonDeadline)
		}
		if hasRunningAttempts(next) {
			next.PendingStop = reason
			return acceptEvent(next, event), nil
		}
		return emitTerminal(next, event, JobStatusFailed, reason), nil

	case EventDeadlineExceeded:
		// Handled before the switch.

	default:
		return state, eventError(event.Type, "unsupported event")
	}

	return acceptEvent(next, event), nil
}

func applyCancellationRequest(state JobState, event Event) (JobState, error) {
	if state.Status != JobStatusQueued && state.Status != JobStatusRunning && state.Status != JobStatusWaiting && state.Status != JobStatusPaused {
		return state, transitionError(state.Status, event.Type)
	}
	state.CancelRequested = true
	state.PendingStop = higherPriorityStop(state.PendingStop, TerminalReasonOperatorCancellation)
	if hasRunningAttempts(state) {
		return acceptEvent(state, event), nil
	}
	return emitTerminal(state, event, JobStatusCancelled, TerminalReasonOperatorCancellation), nil
}

func higherPriorityStop(current, candidate TerminalReason) TerminalReason {
	priority := func(reason TerminalReason) int {
		switch reason {
		case TerminalReasonUnrecoverable:
			return 4
		case TerminalReasonOperatorCancellation:
			return 3
		case TerminalReasonDeadline:
			return 2
		case TerminalReasonBudget:
			return 1
		default:
			return 0
		}
	}
	if priority(candidate) > priority(current) {
		return candidate
	}
	return current
}

func emitPendingStop(state JobState, event Event) JobState {
	reason := state.PendingStop
	if state.CancelRequested {
		reason = higherPriorityStop(reason, TerminalReasonOperatorCancellation)
	}
	if reason == TerminalReasonOperatorCancellation {
		return emitTerminal(state, event, JobStatusCancelled, reason)
	}
	if reason == "" {
		reason = TerminalReasonUnrecoverable
	}
	return emitTerminal(state, event, JobStatusFailed, reason)
}

func applyDecision(state JobState, event Event) (JobState, error) {
	original := cloneJobState(state)
	decision := cloneDecision(*event.Decision)
	if err := decision.Validate(); err != nil {
		return original, eventError(event.Type, err.Error())
	}
	state.LastDecision = &decision

	switch decision.Kind {
	case DecisionContinue:
		if budgetBlocksNewWork(state) {
			return emitTerminal(state, event, JobStatusFailed, TerminalReasonBudget), nil
		}
		if err := validateContinueBatch(state, *decision.NextBatch); err != nil {
			return original, eventError(event.Type, err.Error())
		}
		state.StagnationFingerprints = append(state.StagnationFingerprints, decision.Fingerprint)
		if consecutiveFingerprintCount(state.StagnationFingerprints, decision.Fingerprint) >= StagnationFingerprintLimit {
			return emitTerminal(state, event, JobStatusFailed, TerminalReasonStagnation), nil
		}
		state.NextStageIndex = 0
		state.NextWakeAt = time.Time{}
		if state.Spec.CycleCadenceSeconds > 0 {
			wakeAt := event.At.UTC().Add(time.Duration(state.Spec.CycleCadenceSeconds) * time.Second)
			if wakeAt.After(state.Spec.Deadline) {
				wakeAt = state.Spec.Deadline
			}
			state.Status = JobStatusWaiting
			state.NextWakeAt = wakeAt
			state.WaitingReason = "scheduled cycle cadence"
		} else {
			state.Status = JobStatusRunning
			state.WaitingReason = ""
		}
	case DecisionComplete:
		if !state.Spec.NotBeforeComplete.IsZero() && event.At.Before(state.Spec.NotBeforeComplete) {
			return original, eventError(
				event.Type,
				fmt.Sprintf("successful completion is not allowed before %s", state.Spec.NotBeforeComplete.Format(time.RFC3339Nano)),
			)
		}
		state.FinalResult = latestSuccessfulReducerResult(state)
		return emitTerminal(state, event, JobStatusCompleted, TerminalReasonSuccess), nil
	case DecisionWait:
		state.Status = JobStatusWaiting
		state.WaitingReason = decision.Reason
		state.NextWakeAt = time.Time{}
	case DecisionBlocked:
		return emitTerminal(state, event, JobStatusFailed, TerminalReasonBlocked), nil
	default:
		return original, eventError(event.Type, fmt.Sprintf("unknown decision kind %q", decision.Kind))
	}
	return acceptEvent(state, event), nil
}

func latestSuccessfulReducerResult(state JobState) string {
	if len(state.Spec.Workflow.StageOrder) < 2 {
		return ""
	}
	reducerStage := state.Spec.Workflow.StageOrder[len(state.Spec.Workflow.StageOrder)-2]
	var latest *Attempt
	for index := range state.Attempts {
		attempt := &state.Attempts[index]
		if attempt.Cycle != state.Cycle || attempt.StageID != reducerStage ||
			attempt.RoleID != state.Spec.Workflow.ReducerRoleID ||
			attempt.Status != AttemptStatusSucceeded || strings.TrimSpace(attempt.Result) == "" {
			continue
		}
		if latest == nil || attempt.AttemptNo > latest.AttemptNo {
			latest = attempt
		}
	}
	if latest == nil {
		return ""
	}
	return latest.Result
}

func validateBatchForCursor(state JobState, batch WorkBatch) error {
	if err := ValidateBatchForSpec(state.Spec, batch); err != nil {
		return err
	}
	if state.NextStageIndex < 0 || state.NextStageIndex >= len(state.Spec.Workflow.StageOrder) {
		return fmt.Errorf("workflow cursor %d cannot start a batch", state.NextStageIndex)
	}
	expectedStage := state.Spec.Workflow.StageOrder[state.NextStageIndex]
	if batch.StageID != expectedStage {
		return fmt.Errorf("batch stage %q must be expected stage %q", batch.StageID, expectedStage)
	}
	expectedCycle := state.Cycle
	if state.NextStageIndex == 0 {
		expectedCycle++
	}
	if batch.Cycle != expectedCycle {
		return fmt.Errorf("batch cycle %d must be %d at stage cursor %d", batch.Cycle, expectedCycle, state.NextStageIndex)
	}
	if err := validateBatchExactStage(state.Spec, batch); err != nil {
		return err
	}
	if state.NextStageIndex == 0 && state.Cycle > 0 && state.LastDecision != nil && state.LastDecision.Kind == DecisionContinue {
		if state.LastDecision.NextBatch == nil || !equalWorkBatch(*state.LastDecision.NextBatch, batch) {
			return fmt.Errorf("batch %q differs from the supervisor-proposed batch", batch.ID)
		}
	}
	return nil
}

func validateBatchExactStage(spec JobSpec, batch WorkBatch) error {
	var stage *StageSpec
	for index := range spec.Stages {
		if spec.Stages[index].ID == batch.StageID {
			stage = &spec.Stages[index]
			break
		}
	}
	if stage == nil {
		return fmt.Errorf("unknown stage %q", batch.StageID)
	}
	if len(batch.Items) != len(stage.RoleIDs) {
		return fmt.Errorf("batch %q must contain every role declared by stage %q", batch.ID, stage.ID)
	}
	got := make([]string, len(batch.Items))
	for index := range batch.Items {
		got[index] = batch.Items[index].RoleID
	}
	want := slices.Clone(stage.RoleIDs)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("batch %q roles %v do not match stage %q roles %v", batch.ID, got, stage.ID, want)
	}
	return nil
}

func validateAttemptStart(state JobState, attempt Attempt) error {
	if err := attempt.Validate(); err != nil {
		return err
	}
	if attempt.Status != AttemptStatusRunning {
		return fmt.Errorf("attempt start status must be %q", AttemptStatusRunning)
	}
	if err := validateAttemptReservationCapacity(state, attempt.Reservation); err != nil {
		return err
	}
	item, err := attemptWorkItem(state, attempt)
	if err != nil {
		return err
	}
	if attempt.RoleID != item.RoleID {
		return fmt.Errorf("attempt role %q does not match work item role %q", attempt.RoleID, item.RoleID)
	}
	latest, found := latestAttemptForItem(state.Attempts, attempt.BatchID, attempt.WorkItemID)
	wantNo := uint64(1)
	if found {
		wantNo = latest.AttemptNo + 1
		if latest.Status != AttemptStatusAbandoned && !controlAttemptMayRetry(state, latest) {
			return fmt.Errorf("work item %q latest attempt is %q and cannot be retried", attempt.WorkItemID, latest.Status)
		}
	}
	if attempt.AttemptNo != wantNo {
		return fmt.Errorf("attempt_no %d must be %d", attempt.AttemptNo, wantNo)
	}
	for _, recorded := range state.Attempts {
		if recorded.ID == attempt.ID {
			return fmt.Errorf("attempt ID %q is duplicated", attempt.ID)
		}
	}
	return nil
}

func validateAttemptFinish(state JobState, attempt Attempt) (int, error) {
	if err := attempt.Validate(); err != nil {
		return -1, err
	}
	if attempt.Status == AttemptStatusRunning || attempt.Status == AttemptStatusQueued {
		return -1, fmt.Errorf("attempt finish requires a terminal status")
	}
	if _, err := attemptWorkItem(state, attempt); err != nil {
		return -1, err
	}
	index := -1
	for i := range state.Attempts {
		if state.Attempts[i].ID == attempt.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return -1, fmt.Errorf("attempt %q was not started", attempt.ID)
	}
	started := state.Attempts[index]
	if started.Status != AttemptStatusRunning {
		return -1, fmt.Errorf("attempt %q is already %q", attempt.ID, started.Status)
	}
	if started.BatchID != attempt.BatchID || started.WorkItemID != attempt.WorkItemID || started.RoleID != attempt.RoleID ||
		started.AttemptNo != attempt.AttemptNo || started.Cycle != attempt.Cycle || started.StageID != attempt.StageID ||
		started.Reservation != attempt.Reservation {
		return -1, fmt.Errorf("attempt finish identity differs from its start")
	}
	role, err := roleForID(state.Spec, attempt.RoleID)
	if err != nil {
		return -1, err
	}
	if role.Writer && attempt.Status == AttemptStatusAbandoned && attempt.Dispatched {
		return -1, fmt.Errorf("dispatched writer attempt cannot be abandoned; unknown outcome must be ambiguous")
	}
	if !role.Writer && attempt.Status == AttemptStatusAmbiguous {
		return -1, fmt.Errorf("only a writer attempt may have ambiguous mutation status")
	}
	if err := validateAttemptDecision(state, attempt); err != nil {
		return -1, err
	}
	for _, artifact := range attempt.Artifacts {
		if artifact.CreatedByAttemptID != "" && artifact.CreatedByAttemptID != attempt.ID {
			return -1, fmt.Errorf("artifact %q names a different creating attempt", artifact.ID)
		}
	}
	return index, nil
}

func validateAttemptReservationCapacity(state JobState, reservation AttemptReservation) error {
	reservedCalls := uint64(0)
	reservedTokens := uint64(0)
	for _, attempt := range state.Attempts {
		if attempt.Status != AttemptStatusRunning {
			continue
		}
		var ok bool
		reservedCalls, ok = addUint64(reservedCalls, attempt.Reservation.ModelCalls)
		if !ok {
			return fmt.Errorf("running model-call reservations overflow")
		}
		reservedTokens, ok = addUint64(reservedTokens, attempt.Reservation.Tokens)
		if !ok {
			return fmt.Errorf("running token reservations overflow")
		}
	}
	usedCalls, ok := addUint64(state.Usage.ModelCalls, reservedCalls)
	if !ok || usedCalls > state.Spec.Budget.MaxModelCalls || reservation.ModelCalls > state.Spec.Budget.MaxModelCalls-usedCalls {
		return fmt.Errorf("attempt reservation exceeds remaining model-call budget")
	}
	usedTokens, ok := addUint64(state.Usage.TotalTokens(), reservedTokens)
	if !ok || usedTokens > state.Spec.Budget.MaxTokens || reservation.Tokens > state.Spec.Budget.MaxTokens-usedTokens {
		return fmt.Errorf("attempt reservation exceeds remaining token budget")
	}
	return nil
}

func roleForID(spec JobSpec, roleID string) (RoleSpec, error) {
	for _, role := range spec.Roles {
		if role.ID == roleID {
			return role, nil
		}
	}
	return RoleSpec{}, fmt.Errorf("unknown role %q", roleID)
}

func validateAtomicAttempt(state JobState, attempt Attempt) error {
	if err := attempt.Validate(); err != nil {
		return err
	}
	if attempt.Status == AttemptStatusRunning || attempt.Status == AttemptStatusQueued || attempt.Status == AttemptStatusAbandoned || attempt.Status == AttemptStatusAmbiguous {
		return fmt.Errorf("atomic attempt requires a resolved terminal status")
	}
	if !attempt.Dispatched {
		return fmt.Errorf("atomic attempt must have crossed dispatch")
	}
	if _, err := attemptWorkItem(state, attempt); err != nil {
		return err
	}
	if _, found := latestAttemptForItem(state.Attempts, attempt.BatchID, attempt.WorkItemID); found {
		return fmt.Errorf("work item %q already has an attempt", attempt.WorkItemID)
	}
	if attempt.AttemptNo != 1 {
		return fmt.Errorf("atomic attempt_no must be 1")
	}
	return validateAttemptDecision(state, attempt)
}

func attemptWorkItem(state JobState, attempt Attempt) (*WorkItem, error) {
	if attempt.BatchID != state.CurrentBatch.ID || attempt.Cycle != state.CurrentBatch.Cycle || attempt.StageID != state.CurrentBatch.StageID {
		return nil, fmt.Errorf("attempt does not match active batch identity")
	}
	for index := range state.CurrentBatch.Items {
		if state.CurrentBatch.Items[index].ID == attempt.WorkItemID {
			return &state.CurrentBatch.Items[index], nil
		}
	}
	return nil, fmt.Errorf("attempt references unknown work item %q", attempt.WorkItemID)
}

func validateAttemptDecision(state JobState, attempt Attempt) error {
	isSupervisor := attempt.RoleID == state.Spec.Workflow.SupervisorRoleID && attempt.StageID == state.Spec.Workflow.StageOrder[len(state.Spec.Workflow.StageOrder)-1]
	if !isSupervisor && attempt.Decision != nil {
		return fmt.Errorf("only the supervisor attempt may carry a decision")
	}
	if isSupervisor && attempt.Status == AttemptStatusSucceeded && attempt.Decision == nil {
		return fmt.Errorf("successful supervisor attempt requires a decision")
	}
	if isSupervisor && attempt.Decision != nil {
		if err := attempt.Decision.Validate(); err != nil {
			return err
		}
		if attempt.Decision.Kind == DecisionContinue {
			if err := validateContinueBatch(state, *attempt.Decision.NextBatch); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateContinueBatch(state JobState, batch WorkBatch) error {
	// A supervisor decision is factual output and must remain persistable at the
	// final allowed cycle. Budget policy is applied by applyDecision; temporarily
	// widen only this validation copy so catalog/authority checks still run.
	validationSpec := state.Spec
	if batch.Cycle > validationSpec.Budget.MaxCycles {
		validationSpec.Budget.MaxCycles = batch.Cycle
	}
	if err := ValidateBatchForSpec(validationSpec, batch); err != nil {
		return err
	}
	if batch.Cycle != state.Cycle+1 {
		return fmt.Errorf("continue batch cycle %d must follow cycle %d", batch.Cycle, state.Cycle)
	}
	if batch.StageID != state.Spec.Workflow.StageOrder[0] {
		return fmt.Errorf("continue batch must target first stage %q", state.Spec.Workflow.StageOrder[0])
	}
	return validateBatchExactStage(state.Spec, batch)
}

func validateBatchBarrier(state JobState) error {
	for _, item := range state.CurrentBatch.Items {
		latest, found := latestAttemptForItem(state.Attempts, state.CurrentBatch.ID, item.ID)
		if !found || !attemptResolvesBarrier(latest.Status) || controlAttemptMayRetry(state, latest) {
			return fmt.Errorf("all barrier is waiting for work item %q", item.ID)
		}
	}
	return nil
}

func controlAttemptMayRetry(state JobState, attempt Attempt) bool {
	if attempt.Status != AttemptStatusFailed || attempt.AttemptNo >= uint64(MaxControlAttempts) {
		return false
	}
	return attempt.RoleID == state.Spec.Workflow.ReducerRoleID || attempt.RoleID == state.Spec.Workflow.SupervisorRoleID
}

func attemptResolvesBarrier(status AttemptStatus) bool {
	return status == AttemptStatusSucceeded || status == AttemptStatusFailed || status == AttemptStatusCancelled
}

func latestAttemptForItem(attempts []Attempt, batchID, itemID string) (Attempt, bool) {
	var latest Attempt
	found := false
	for _, attempt := range attempts {
		if attempt.BatchID != batchID || attempt.WorkItemID != itemID {
			continue
		}
		if !found || attempt.AttemptNo > latest.AttemptNo {
			latest = attempt
			found = true
		}
	}
	return latest, found
}

func persistedSupervisorDecision(state JobState) (*Decision, error) {
	finalStage := state.Spec.Workflow.StageOrder[len(state.Spec.Workflow.StageOrder)-1]
	var latest *Attempt
	for index := range state.Attempts {
		attempt := &state.Attempts[index]
		if attempt.Cycle != state.Cycle || attempt.StageID != finalStage || attempt.RoleID != state.Spec.Workflow.SupervisorRoleID {
			continue
		}
		if latest == nil || attempt.AttemptNo > latest.AttemptNo {
			latest = attempt
		}
	}
	if latest == nil || latest.Status != AttemptStatusSucceeded || latest.Decision == nil {
		return nil, fmt.Errorf("completed supervisor stage has no successful persisted decision")
	}
	decision := cloneDecision(*latest.Decision)
	return &decision, nil
}

func hasRunningAttempts(state JobState) bool {
	for _, attempt := range state.Attempts {
		if attempt.Status == AttemptStatusRunning {
			return true
		}
	}
	return false
}

func budgetBlocksNewAttempt(state JobState) bool {
	return state.Usage.Attempts >= state.Spec.Budget.MaxAttempts ||
		state.Usage.ModelCalls >= state.Spec.Budget.MaxModelCalls ||
		state.Usage.TotalTokens() >= state.Spec.Budget.MaxTokens ||
		state.Usage.Cycles >= state.Spec.Budget.MaxCycles
}

func budgetBlocksNewWork(state JobState) bool {
	exceeded, _ := state.Spec.Budget.ExceededBy(state.Usage)
	return exceeded
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

func validateEventShape(event Event) error {
	allowBatch := event.Type == EventBatchStarted
	allowBatchID := event.Type == EventBatchCompleted
	allowAttempt := event.Type == EventAttemptStarted || event.Type == EventAttemptFinished || event.Type == EventAttemptRecorded
	allowDecision := event.Type == EventDecisionMade
	allowUsage := event.Type == EventUsageRecorded
	allowArtifacts := event.Type == EventAttemptFinished || event.Type == EventAttemptRecorded || event.Type == EventBatchCompleted || event.Type == EventUsageRecorded
	allowReason := event.Type == EventJobFailed

	unexpected := ""
	switch {
	case event.Batch != nil && !allowBatch:
		unexpected = "batch"
	case event.BatchID != "" && !allowBatchID:
		unexpected = "batch_id"
	case event.Attempt != nil && !allowAttempt:
		unexpected = "attempt"
	case event.Decision != nil && !allowDecision:
		unexpected = "decision"
	case event.Usage != (Usage{}) && !allowUsage:
		unexpected = "usage"
	case len(event.Artifacts) != 0 && !allowArtifacts:
		unexpected = "artifacts"
	case event.Reason != "" && !allowReason:
		unexpected = "reason"
	}
	if unexpected != "" {
		return eventError(event.Type, "unexpected "+unexpected+" payload")
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
	state.PendingStop = ""
	state.WaitingReason = ""
	state.NextWakeAt = time.Time{}
	return acceptEvent(state, event)
}

func acceptEvent(state JobState, event Event) JobState {
	state.Revision++
	state.LastEventID = event.ID
	return state
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventJobStarted, EventBatchStarted, EventAttemptStarted,
		EventAttemptFinished, EventAttemptRecorded, EventBatchCompleted,
		EventUsageRecorded, EventDecisionMade, EventJobPaused, EventJobResumed,
		EventCancellationRequested, EventJobCancelled, EventJobFailed,
		EventDeadlineExceeded:
		return true
	default:
		return false
	}
}

func validFailureReason(reason TerminalReason) bool {
	return reason == TerminalReasonBlocked || reason == TerminalReasonUnrecoverable || reason == TerminalReasonBudget
}

func consecutiveFingerprintCount(fingerprints []string, fingerprint string) int {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return 0
	}
	count := 0
	for index := len(fingerprints) - 1; index >= 0 && fingerprints[index] == fingerprint; index-- {
		count++
	}
	return count
}

func cloneJobState(state JobState) JobState {
	out := state
	out.Spec = cloneJobSpec(state.Spec)
	if state.CurrentBatch != nil {
		batch := cloneWorkBatch(*state.CurrentBatch)
		out.CurrentBatch = &batch
	}
	out.Attempts = make([]Attempt, len(state.Attempts))
	for index := range state.Attempts {
		out.Attempts[index] = cloneAttempt(state.Attempts[index])
	}
	out.CompletedBatches = slices.Clone(state.CompletedBatches)
	out.Artifacts = cloneArtifacts(state.Artifacts)
	if state.LastDecision != nil {
		decision := cloneDecision(*state.LastDecision)
		out.LastDecision = &decision
	}
	out.StagnationFingerprints = slices.Clone(state.StagnationFingerprints)
	return out
}

func cloneJobSpec(spec JobSpec) JobSpec {
	out := spec
	out.Authority = cloneAuthority(spec.Authority)
	out.Roles = make([]RoleSpec, len(spec.Roles))
	for index, role := range spec.Roles {
		out.Roles[index] = role
		out.Roles[index].Authority = cloneAuthority(role.Authority)
	}
	out.Stages = make([]StageSpec, len(spec.Stages))
	for index, stage := range spec.Stages {
		out.Stages[index] = stage
		out.Stages[index].RoleIDs = slices.Clone(stage.RoleIDs)
	}
	out.Workflow.StageOrder = slices.Clone(spec.Workflow.StageOrder)
	out.Workflow.WorkerRoleIDs = slices.Clone(spec.Workflow.WorkerRoleIDs)
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

func cloneAttempt(attempt Attempt) Attempt {
	out := attempt
	out.Artifacts = cloneArtifacts(attempt.Artifacts)
	if attempt.Decision != nil {
		decision := cloneDecision(*attempt.Decision)
		out.Decision = &decision
	}
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

func equalDecision(left, right Decision) bool {
	if left.Kind != right.Kind || left.Reason != right.Reason || left.Fingerprint != right.Fingerprint {
		return false
	}
	if left.NextBatch == nil || right.NextBatch == nil {
		return left.NextBatch == nil && right.NextBatch == nil
	}
	return equalWorkBatch(*left.NextBatch, *right.NextBatch)
}

func equalWorkBatch(left, right WorkBatch) bool {
	if left.ID != right.ID || left.StageID != right.StageID || left.Cycle != right.Cycle ||
		left.Barrier != right.Barrier || len(left.Items) != len(right.Items) {
		return false
	}
	for index := range left.Items {
		leftItem, rightItem := left.Items[index], right.Items[index]
		if leftItem.ID != rightItem.ID || leftItem.RoleID != rightItem.RoleID ||
			leftItem.Objective != rightItem.Objective || !equalAuthority(leftItem.Authority, rightItem.Authority) {
			return false
		}
	}
	return true
}

func equalAuthority(left, right Authority) bool {
	return left.Mode == right.Mode && slices.Equal(left.Tools, right.Tools) &&
		slices.Equal(left.ReadRoots, right.ReadRoots) && slices.Equal(left.WriteRoots, right.WriteRoots) &&
		slices.Equal(left.NetworkHosts, right.NetworkHosts) && slices.Equal(left.Providers, right.Providers)
}

func sortAttempts(attempts []Attempt) {
	slices.SortStableFunc(attempts, func(left, right Attempt) int {
		if left.Cycle != right.Cycle {
			if left.Cycle < right.Cycle {
				return -1
			}
			return 1
		}
		if cmp := strings.Compare(left.StageID, right.StageID); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(left.WorkItemID, right.WorkItemID); cmp != 0 {
			return cmp
		}
		if left.AttemptNo < right.AttemptNo {
			return -1
		}
		if left.AttemptNo > right.AttemptNo {
			return 1
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
