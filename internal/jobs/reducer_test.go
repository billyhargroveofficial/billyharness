package jobs

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReduceFullStageTraversalChargesOneCycle(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestStateWithWorkers(t, now, 2), now)
	workflow := state.Spec.Workflow

	for stageIndex := range workflow.StageOrder {
		decision := (*Decision)(nil)
		if stageIndex == len(workflow.StageOrder)-1 {
			decision = &Decision{Kind: DecisionComplete, Reason: "rubric satisfied"}
		}
		beforeCycles := state.Usage.Cycles
		state = runReducerStage(t, state, stageIndex, decision, now.Add(time.Duration(stageIndex+1)*time.Minute))
		if state.Cycle != 1 || state.NextStageIndex != stageIndex+1 {
			t.Fatalf("stage %d cursor/cycle = %d/%d", stageIndex, state.NextStageIndex, state.Cycle)
		}
		if stageIndex < len(workflow.StageOrder)-1 && state.Usage.Cycles != beforeCycles {
			t.Fatalf("intermediate stage charged a cycle: %#v", state.Usage)
		}
	}

	if state.Usage.Cycles != 1 || state.CurrentBatch != nil || len(state.CompletedBatches) != len(workflow.StageOrder) {
		t.Fatalf("completed traversal = %#v", state)
	}
	persisted, err := persistedSupervisorDecision(state)
	if err != nil {
		t.Fatal(err)
	}
	state = reduceOK(t, state, Event{
		ID: "decision-1", Type: EventDecisionMade, At: now.Add(10 * time.Minute), Decision: persisted,
	})
	assertReducerState(t, state, JobStatusCompleted, TerminalReasonSuccess)
	if state.FinalResult == "" {
		t.Fatal("completed workflow did not materialize final reducer result")
	}
}

func TestReduceRejectsOutOfOrderWrongCycleAndEarlyDecision(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)

	wrongStage := reducerTestBatch(t, state, 1, 1)
	assertReduceErrorUnchanged(t, state, Event{
		ID: "wrong-stage", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &wrongStage,
	}, "expected stage")

	wrongCycle := reducerTestBatch(t, state, 0, 2)
	assertReduceErrorUnchanged(t, state, Event{
		ID: "wrong-cycle", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &wrongCycle,
	}, "batch cycle")

	assertReduceErrorUnchanged(t, state, Event{
		ID: "early-decision", Type: EventDecisionMade, At: now.Add(time.Minute),
		Decision: &Decision{Kind: DecisionComplete, Reason: "too early"},
	}, "supervisor barrier")
}

func TestReduceTwoPhaseAttemptRetryAndLatestBarrier(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	batch := reducerTestBatch(t, state, 0, 1)
	state = reduceOK(t, state, Event{ID: "batch-start", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch})

	first := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{ID: "attempt-1-start", Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &first})
	if state.Usage.Attempts != 1 || len(state.Attempts) != 1 || state.Attempts[0].Status != AttemptStatusRunning {
		t.Fatalf("started attempt state = %#v", state)
	}
	first.Status = AttemptStatusAbandoned
	first.Dispatched = true
	first.Error = "process interrupted"
	state = reduceOK(t, state, Event{ID: "attempt-1-finish", Type: EventAttemptFinished, At: now.Add(3 * time.Minute), Attempt: &first})
	assertReduceErrorUnchanged(t, state, Event{
		ID: "early-barrier", Type: EventBatchCompleted, At: now.Add(4 * time.Minute), BatchID: batch.ID,
	}, "waiting")

	second := reducerTestAttempt(batch, batch.Items[0], 2, AttemptStatusRunning)
	state = reduceOK(t, state, Event{ID: "attempt-2-start", Type: EventAttemptStarted, At: now.Add(5 * time.Minute), Attempt: &second})
	second.Status = AttemptStatusSucceeded
	second.Dispatched = true
	second.Result = "recovered result"
	second.Fingerprint = "result-v2"
	second.Usage = Usage{ModelCalls: 1, InputTokens: 12, OutputTokens: 7}
	state = reduceOK(t, state, Event{ID: "attempt-2-finish", Type: EventAttemptFinished, At: now.Add(6 * time.Minute), Attempt: &second})
	state = reduceOK(t, state, Event{ID: "barrier", Type: EventBatchCompleted, At: now.Add(7 * time.Minute), BatchID: batch.ID})
	if state.Usage.Attempts != 2 || state.Usage.ModelCalls != 1 || state.NextStageIndex != 1 {
		t.Fatalf("retry usage/cursor = %#v / %d", state.Usage, state.NextStageIndex)
	}
}

func TestReduceAllowsOnlyBoundedFailedControlRetries(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	state = runReducerStage(t, state, 0, nil, now.Add(time.Minute))
	reducerIndex := reducerStageIndex(t, state.Spec, "reduce")
	batch := reducerTestBatch(t, state, reducerIndex, 1)
	state = reduceOK(t, state, Event{ID: "control-batch", Type: EventBatchStarted, At: now.Add(3 * time.Minute), Batch: &batch})

	for attemptNo := uint64(1); attemptNo <= uint64(MaxControlAttempts); attemptNo++ {
		attempt := reducerTestAttempt(batch, batch.Items[0], attemptNo, AttemptStatusRunning)
		state = reduceOK(t, state, Event{
			ID: fmt.Sprintf("control-%d-start", attemptNo), Type: EventAttemptStarted,
			At: now.Add(time.Duration(3+attemptNo*2) * time.Minute), Attempt: &attempt,
		})
		attempt.Status = AttemptStatusFailed
		attempt.Dispatched = true
		attempt.Error = "control output failed validation"
		attempt.Usage = Usage{ModelCalls: 1, InputTokens: 2, OutputTokens: 1}
		state = reduceOK(t, state, Event{
			ID: fmt.Sprintf("control-%d-finish", attemptNo), Type: EventAttemptFinished,
			At: now.Add(time.Duration(4+attemptNo*2) * time.Minute), Attempt: &attempt,
		})
		barrier := Event{ID: fmt.Sprintf("control-%d-barrier", attemptNo), Type: EventBatchCompleted, At: now.Add(time.Duration(5+attemptNo*2) * time.Minute), BatchID: batch.ID}
		if attemptNo < uint64(MaxControlAttempts) {
			assertReduceErrorUnchanged(t, state, barrier, "waiting")
			continue
		}
		state = reduceOK(t, state, barrier)
	}
	if state.NextStageIndex != reducerIndex+1 || state.Usage.Attempts != uint64(MaxControlAttempts+1) {
		t.Fatalf("bounded control retry state = %#v", state)
	}
}

func TestReduceAmbiguousAttemptNeverResolvesOrRetries(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestStateForPreset(t, now, PresetCoding, 1), now)
	implementIndex := reducerStageIndex(t, state.Spec, "implement")

	// Complete analyze to reach the isolated writer stage.
	state = runReducerStage(t, state, 0, nil, now.Add(time.Minute))
	batch := reducerTestBatch(t, state, implementIndex, 1)
	state = reduceOK(t, state, Event{ID: "writer-batch", Type: EventBatchStarted, At: now.Add(4 * time.Minute), Batch: &batch})
	started := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{ID: "writer-start", Type: EventAttemptStarted, At: now.Add(5 * time.Minute), Attempt: &started})
	started.Status = AttemptStatusAmbiguous
	started.Dispatched = true
	started.Error = "writer outcome unknown after restart"
	state = reduceOK(t, state, Event{ID: "writer-ambiguous", Type: EventAttemptFinished, At: now.Add(6 * time.Minute), Attempt: &started})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonUnrecoverable)
	retry := reducerTestAttempt(batch, batch.Items[0], 2, AttemptStatusRunning)
	if _, err := Reduce(state, Event{
		ID: "writer-retry", Type: EventAttemptStarted, At: now.Add(8 * time.Minute), Attempt: &retry,
	}); !errors.Is(err, ErrTerminalState) {
		t.Fatalf("writer retry after ambiguity = %v, want terminal state", err)
	}
}

func TestReduceDecisionMustExactlyMatchPersistedSupervisorAttempt(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	decision := Decision{Kind: DecisionComplete, Reason: "verified"}
	state = runReducerCycle(t, state, &decision, now.Add(time.Minute))

	different := decision
	different.Reason = "provider tried to substitute output"
	assertReduceErrorUnchanged(t, state, Event{
		ID: "substitute", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &different,
	}, "differs")
	state = reduceOK(t, state, Event{
		ID: "exact", Type: EventDecisionMade, At: now.Add(21 * time.Minute), Decision: &decision,
	})
	assertReducerState(t, state, JobStatusCompleted, TerminalReasonSuccess)
}

func TestReduceContinueAuthorizesOnlyExactNextFirstBatch(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	nextBatch := reducerTestBatchForCycle(t, state.Spec, 0, 2)
	decision := Decision{Kind: DecisionContinue, Reason: "one more pass", NextBatch: &nextBatch, Fingerprint: "progress-v1"}
	state = runReducerCycle(t, state, &decision, now.Add(time.Minute))
	state = reduceOK(t, state, Event{ID: "continue", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &decision})
	if state.NextStageIndex != 0 || state.Cycle != 1 {
		t.Fatalf("continue cursor/cycle = %d/%d", state.NextStageIndex, state.Cycle)
	}

	substituted := cloneWorkBatch(nextBatch)
	substituted.Items[0].Objective += " changed"
	assertReduceErrorUnchanged(t, state, Event{
		ID: "substituted", Type: EventBatchStarted, At: now.Add(21 * time.Minute), Batch: &substituted,
	}, "differs")
	state = reduceOK(t, state, Event{ID: "exact-next", Type: EventBatchStarted, At: now.Add(22 * time.Minute), Batch: &nextBatch})
	if state.Cycle != 2 || state.NextStageIndex != 0 {
		t.Fatalf("next cycle start = %#v", state)
	}
}

func TestReduceLateFinishRetainsFactualUsageBeforeDeadlineTerminal(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state.Spec.Deadline = now.Add(3 * time.Minute)
	state = startReducerJob(t, state, now)
	batch := reducerTestBatch(t, state, 0, 1)
	state = reduceOK(t, state, Event{ID: "batch", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{ID: "start", Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &attempt})
	attempt.Status = AttemptStatusSucceeded
	attempt.Dispatched = true
	attempt.Result = "completed at provider boundary"
	attempt.Usage = Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5}
	state = reduceOK(t, state, Event{ID: "late-finish", Type: EventAttemptFinished, At: now.Add(3 * time.Minute), Attempt: &attempt})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonDeadline)
	if len(state.Attempts) != 1 || state.Attempts[0].Result != attempt.Result || state.Usage.TotalTokens() != 15 {
		t.Fatalf("late factual finish lost: %#v", state)
	}
}

func TestReduceLateUsageDrainPersistsFactualExcessBeforeAttemptFinish(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state.Spec.Deadline = now.Add(3 * time.Minute)
	state = startReducerJob(t, state, now)
	batch := reducerTestBatch(t, state, 0, 1)
	state = reduceOK(t, state, Event{ID: "batch", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{ID: "start", Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &attempt})
	state = reduceOK(t, state, Event{
		ID: "late-factual-excess", Type: EventUsageRecorded, At: state.Spec.Deadline,
		Usage: Usage{ModelCalls: 1, InputTokens: 4, OutputTokens: 2},
	})
	if state.Status != JobStatusRunning || state.PendingStop != TerminalReasonDeadline || state.Usage.ModelCalls != 1 || state.Usage.TotalTokens() != 6 {
		t.Fatalf("late factual excess was not retained while draining: %#v", state)
	}
	attempt.Status = AttemptStatusSucceeded
	attempt.Dispatched = true
	attempt.Result = "late result"
	attempt.Usage = Usage{ModelCalls: 1, InputTokens: 3, OutputTokens: 1}
	state = reduceOK(t, state, Event{ID: "late-finish", Type: EventAttemptFinished, At: state.Spec.Deadline.Add(time.Second), Attempt: &attempt})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonDeadline)
	if state.Usage.ModelCalls != 2 || state.Usage.TotalTokens() != 10 {
		t.Fatalf("late factual total = %#v", state.Usage)
	}
}

func TestReduceCancellationRequestWaitsForFactualFinishes(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	batch := reducerTestBatch(t, state, 0, 1)
	state = reduceOK(t, state, Event{ID: "batch", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{ID: "start", Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &attempt})
	state = reduceOK(t, state, Event{ID: "cancel-request", Type: EventCancellationRequested, At: now.Add(3 * time.Minute)})
	if state.Status != JobStatusRunning || !state.CancelRequested {
		t.Fatalf("cancellation request prematurely ended active call: %#v", state)
	}
	state = reduceOK(t, state, Event{
		ID: "cancelled-factual-excess", Type: EventUsageRecorded, At: now.Add(3500 * time.Millisecond),
		Usage: Usage{ModelCalls: 1, InputTokens: 2, OutputTokens: 1},
	})
	attempt.Status = AttemptStatusCancelled
	attempt.Dispatched = true
	attempt.Error = "context cancelled by operator"
	attempt.Usage = Usage{ModelCalls: 1, InputTokens: 5, OutputTokens: 1}
	state = reduceOK(t, state, Event{ID: "finish", Type: EventAttemptFinished, At: now.Add(4 * time.Minute), Attempt: &attempt})
	assertReducerState(t, state, JobStatusCancelled, TerminalReasonOperatorCancellation)
	if state.Usage.ModelCalls != 2 || state.Usage.Attempts != 1 || state.Usage.TotalTokens() != 9 {
		t.Fatalf("cancelled finish usage = %#v", state.Usage)
	}
}

func TestReduceBudgetCapAllowsReservedFinishAndFinalDecision(t *testing.T) {
	now := reducerTestTime()

	t.Run("attempt reservation finishes before budget terminal", func(t *testing.T) {
		state := reducerTestState(t, now)
		state.Spec.Budget.MaxAttempts = 1
		state = startReducerJob(t, state, now)
		batch := reducerTestBatch(t, state, 0, 1)
		state = reduceOK(t, state, Event{ID: "batch", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch})
		attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
		state = reduceOK(t, state, Event{ID: "start", Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &attempt})
		if state.Status != JobStatusRunning {
			t.Fatalf("reservation at exact cap terminated early: %#v", state)
		}
		attempt.Status = AttemptStatusSucceeded
		attempt.Dispatched = true
		attempt.Result = "factual"
		attempt.Usage = Usage{ModelCalls: 1, InputTokens: 1, OutputTokens: 1}
		state = reduceOK(t, state, Event{ID: "finish", Type: EventAttemptFinished, At: now.Add(3 * time.Minute), Attempt: &attempt})
		state = reduceOK(t, state, Event{ID: "barrier", Type: EventBatchCompleted, At: now.Add(4 * time.Minute), BatchID: batch.ID})
		assertReducerState(t, state, JobStatusFailed, TerminalReasonBudget)
		if state.Attempts[0].Result != "factual" {
			t.Fatal("budget terminal lost reserved finish")
		}
	})

	t.Run("cycle cap still admits persisted completion decision", func(t *testing.T) {
		state := reducerTestState(t, now)
		state.Spec.Budget.MaxCycles = 1
		state = startReducerJob(t, state, now)
		decision := Decision{Kind: DecisionComplete, Reason: "done within final allowed cycle"}
		state = runReducerCycle(t, state, &decision, now.Add(time.Minute))
		if state.Status != JobStatusRunning || state.Usage.Cycles != 1 {
			t.Fatalf("final barrier at cap = %#v", state)
		}
		state = reduceOK(t, state, Event{ID: "decision", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &decision})
		assertReducerState(t, state, JobStatusCompleted, TerminalReasonSuccess)
	})

	t.Run("continue at cycle cap becomes budget terminal", func(t *testing.T) {
		state := reducerTestState(t, now)
		state.Spec.Budget.MaxCycles = 1
		state = startReducerJob(t, state, now)
		next := reducerTestBatchForCycle(t, state.Spec, 0, 2)
		decision := Decision{Kind: DecisionContinue, Reason: "try again", NextBatch: &next, Fingerprint: "same"}
		state = runReducerCycle(t, state, &decision, now.Add(time.Minute))
		state = reduceOK(t, state, Event{ID: "decision", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &decision})
		assertReducerState(t, state, JobStatusFailed, TerminalReasonBudget)
	})
}

func TestReduceResumeFromWaitStartsNewCycle(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	wait := Decision{Kind: DecisionWait, Reason: "operator evidence required"}
	state = runReducerCycle(t, state, &wait, now.Add(time.Minute))
	state = reduceOK(t, state, Event{ID: "wait", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &wait})
	if state.Status != JobStatusWaiting {
		t.Fatalf("wait state = %#v", state)
	}
	state = reduceOK(t, state, Event{ID: "pause", Type: EventJobPaused, At: now.Add(21 * time.Minute)})
	state = reduceOK(t, state, Event{ID: "resume", Type: EventJobResumed, At: now.Add(22 * time.Minute)})
	if state.Status != JobStatusRunning || state.NextStageIndex != 0 {
		t.Fatalf("resumed wait cursor = %#v", state)
	}
	next := reducerTestBatchForCycle(t, state.Spec, 0, 2)
	state = reduceOK(t, state, Event{ID: "next", Type: EventBatchStarted, At: now.Add(23 * time.Minute), Batch: &next})
	if state.Cycle != 2 {
		t.Fatalf("resumed cycle = %d", state.Cycle)
	}
}

func TestReduceTerminalReplayAndMalformedEnvelope(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state = reduceOK(t, state, Event{ID: "cancel", Type: EventCancellationRequested, At: now})
	assertReducerState(t, state, JobStatusCancelled, TerminalReasonOperatorCancellation)
	replayed, err := Reduce(state, Event{ID: "cancel", Type: EventCancellationRequested, At: now})
	if !errors.Is(err, ErrInvalidEvent) || !reflect.DeepEqual(replayed, state) {
		t.Fatalf("pure reducer duplicate = %#v, %v", replayed, err)
	}
	if _, err := Reduce(state, Event{ID: "other", Type: EventJobFailed, At: now, Reason: TerminalReasonUnrecoverable}); !errors.Is(err, ErrTerminalState) {
		t.Fatalf("post-terminal error = %v", err)
	}

	initial := reducerTestState(t, now)
	for _, event := range []Event{
		{Type: EventJobStarted, At: now},
		{ID: "missing-time", Type: EventJobStarted},
		{ID: "unknown", Type: EventType("provider_stop"), At: now},
	} {
		if _, err := Reduce(initial, event); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("event %#v error = %v", event, err)
		}
	}
	initial.Revision = math.MaxUint64
	if _, err := Reduce(initial, Event{ID: "overflow", Type: EventJobStarted, At: now}); !errors.Is(err, ErrUsageOverflow) {
		t.Fatalf("revision overflow = %v", err)
	}
}

func TestReduceRejectsAbandonedWriterFinish(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestStateForPreset(t, now, PresetCoding, 1), now)
	state = runReducerStage(t, state, 0, nil, now.Add(time.Minute))
	writerStage := reducerStageIndex(t, state.Spec, "implement")
	batch := reducerTestBatch(t, state, writerStage, 1)
	state = reduceOK(t, state, Event{
		ID: "writer-batch-start", Type: EventBatchStarted, At: now.Add(4 * time.Minute), Batch: &batch,
	})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{
		ID: "writer-attempt-start", Type: EventAttemptStarted, At: now.Add(5 * time.Minute), Attempt: &attempt,
	})
	attempt.Status = AttemptStatusAbandoned
	attempt.Dispatched = true
	attempt.Error = "writer transport ended without an observable outcome"
	assertReduceErrorUnchanged(t, state, Event{
		ID: "writer-attempt-abandoned", Type: EventAttemptFinished, At: now.Add(6 * time.Minute), Attempt: &attempt,
	}, "writer attempt cannot be abandoned")
}

func TestReduceAllowsKnownUndispatchedWriterRetry(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestStateForPreset(t, now, PresetCoding, 1), now)
	state = runReducerStage(t, state, 0, nil, now.Add(time.Minute))
	writerStage := reducerStageIndex(t, state.Spec, "implement")
	batch := reducerTestBatch(t, state, writerStage, 1)
	state = reduceOK(t, state, Event{
		ID: "writer-batch-start", Type: EventBatchStarted, At: now.Add(4 * time.Minute), Batch: &batch,
	})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{
		ID: "writer-attempt-start", Type: EventAttemptStarted, At: now.Add(5 * time.Minute), Attempt: &attempt,
	})
	attempt.Status = AttemptStatusAbandoned
	attempt.Error = "live runtime proved dispatch never occurred"
	state = reduceOK(t, state, Event{
		ID: "writer-attempt-undispatched", Type: EventAttemptFinished, At: now.Add(6 * time.Minute), Attempt: &attempt,
	})
	retry := reducerTestAttempt(batch, batch.Items[0], 2, AttemptStatusRunning)
	state = reduceOK(t, state, Event{
		ID: "writer-attempt-retry", Type: EventAttemptStarted, At: now.Add(7 * time.Minute), Attempt: &retry,
	})
	var writerAttempts []Attempt
	for _, recorded := range state.Attempts {
		if recorded.WorkItemID == batch.Items[0].ID {
			writerAttempts = append(writerAttempts, recorded)
		}
	}
	if len(writerAttempts) != 2 || writerAttempts[0].Dispatched || writerAttempts[1].AttemptNo != 2 {
		t.Fatalf("undispatched writer retry state = %#v", writerAttempts)
	}
}

func TestReduceLateDeadlineDrainsEveryParallelFactualFinish(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestStateWithWorkers(t, now, 2)
	state.Spec.Deadline = now.Add(3 * time.Minute)
	state = startReducerJob(t, state, now)
	batch := reducerTestBatch(t, state, 0, 1)
	state = reduceOK(t, state, Event{
		ID: "parallel-batch", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch,
	})

	attempts := make([]Attempt, len(batch.Items))
	for index, item := range batch.Items {
		attempts[index] = reducerTestAttempt(batch, item, 1, AttemptStatusRunning)
		state = reduceOK(t, state, Event{
			ID: fmt.Sprintf("parallel-start-%d", index), Type: EventAttemptStarted,
			At: now.Add(2*time.Minute + time.Duration(index)*time.Second), Attempt: &attempts[index],
		})
	}

	attempts[0].Status = AttemptStatusSucceeded
	attempts[0].Dispatched = true
	attempts[0].Result = "first factual late result"
	attempts[0].Usage = Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5}
	state = reduceOK(t, state, Event{
		ID: "parallel-finish-0", Type: EventAttemptFinished, At: state.Spec.Deadline, Attempt: &attempts[0],
	})
	if state.Status != JobStatusRunning || state.PendingStop != TerminalReasonDeadline {
		t.Fatalf("first late finish did not enter deadline drain: %#v", state)
	}
	if !hasRunningAttempts(state) || state.Attempts[0].Result == "" {
		t.Fatalf("first late finish was not retained while peer drained: %#v", state.Attempts)
	}

	attempts[1].Status = AttemptStatusSucceeded
	attempts[1].Dispatched = true
	attempts[1].Result = "second factual late result"
	attempts[1].Usage = Usage{ModelCalls: 1, InputTokens: 7, OutputTokens: 8}
	state = reduceOK(t, state, Event{
		ID: "parallel-finish-1", Type: EventAttemptFinished, At: state.Spec.Deadline.Add(time.Second), Attempt: &attempts[1],
	})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonDeadline)
	if len(state.Attempts) != 2 || state.Attempts[0].Result == "" || state.Attempts[1].Result == "" {
		t.Fatalf("deadline drain lost a factual finish: %#v", state.Attempts)
	}
	if state.Usage.Attempts != 2 || state.Usage.ModelCalls != 2 || state.Usage.TotalTokens() != 30 {
		t.Fatalf("deadline drain usage = %#v", state.Usage)
	}
}

func TestReduceAmbiguousWriterOutranksCancellationAndDeadline(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestStateForPreset(t, now, PresetCoding, 1)
	state.Spec.Deadline = now.Add(6 * time.Minute)
	state = startReducerJob(t, state, now)
	state = runReducerStage(t, state, 0, nil, now.Add(time.Minute))
	writerStage := reducerStageIndex(t, state.Spec, "implement")
	batch := reducerTestBatch(t, state, writerStage, 1)
	state = reduceOK(t, state, Event{
		ID: "ambiguous-writer-batch", Type: EventBatchStarted, At: now.Add(3 * time.Minute), Batch: &batch,
	})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{
		ID: "ambiguous-writer-start", Type: EventAttemptStarted, At: now.Add(4 * time.Minute), Attempt: &attempt,
	})
	state = reduceOK(t, state, Event{
		ID: "ambiguous-writer-deadline", Type: EventDeadlineExceeded, At: state.Spec.Deadline,
	})
	state = reduceOK(t, state, Event{
		ID: "ambiguous-writer-cancel", Type: EventCancellationRequested, At: state.Spec.Deadline.Add(time.Second),
	})
	if !state.CancelRequested || state.PendingStop != TerminalReasonOperatorCancellation {
		t.Fatalf("combined pending stops = %#v", state)
	}

	attempt.Status = AttemptStatusAmbiguous
	attempt.Dispatched = true
	attempt.Error = "the writer may have mutated external state before disconnect"
	attempt.Usage = Usage{ModelCalls: 1, InputTokens: 5, OutputTokens: 2}
	usageBeforeFinish := state.Usage
	state = reduceOK(t, state, Event{
		ID: "ambiguous-writer-finish", Type: EventAttemptFinished,
		At: state.Spec.Deadline.Add(2 * time.Second), Attempt: &attempt,
	})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonUnrecoverable)
	latest := state.Attempts[len(state.Attempts)-1]
	if latest.Status != AttemptStatusAmbiguous ||
		state.Usage.ModelCalls != usageBeforeFinish.ModelCalls+1 ||
		state.Usage.TotalTokens() != usageBeforeFinish.TotalTokens()+7 {
		t.Fatalf("ambiguous factual outcome was not retained: %#v", state)
	}
}

func TestReduceUnrecoverableFailureOutranksCancellationAndDeadlineWhileDraining(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestStateWithWorkers(t, now, 1)
	state.Spec.Deadline = now.Add(3 * time.Minute)
	state = startReducerJob(t, state, now)
	batch := reducerTestBatch(t, state, 0, 1)
	state = reduceOK(t, state, Event{
		ID: "priority-batch", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch,
	})
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	state = reduceOK(t, state, Event{
		ID: "priority-start", Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &attempt,
	})
	state = reduceOK(t, state, Event{
		ID: "priority-cancel", Type: EventCancellationRequested, At: state.Spec.Deadline,
	})
	state = reduceOK(t, state, Event{
		ID: "priority-failure", Type: EventJobFailed, At: state.Spec.Deadline.Add(time.Second), Reason: TerminalReasonUnrecoverable,
	})
	if state.Status != JobStatusRunning || state.PendingStop != TerminalReasonUnrecoverable || !state.CancelRequested {
		t.Fatalf("pending priority state = %#v", state)
	}

	attempt.Status = AttemptStatusFailed
	attempt.Dispatched = true
	attempt.Error = "invalid provider result"
	attempt.Usage = Usage{ModelCalls: 1, InputTokens: 3, OutputTokens: 1}
	state = reduceOK(t, state, Event{
		ID: "priority-finish", Type: EventAttemptFinished, At: state.Spec.Deadline.Add(2 * time.Second), Attempt: &attempt,
	})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonUnrecoverable)
}

func TestReduceParallelReservationsCannotOversubscribeBudgets(t *testing.T) {
	now := reducerTestTime()
	tests := []struct {
		name              string
		maxModelCalls     uint64
		maxTokens         uint64
		firstReservation  AttemptReservation
		secondReservation AttemptReservation
		want              string
	}{
		{
			name: "model calls", maxModelCalls: 2, maxTokens: 1_000,
			firstReservation:  AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
			secondReservation: AttemptReservation{ModelCalls: 2, Tokens: 100, MaxOutputTokens: 50},
			want:              "model-call budget",
		},
		{
			name: "tokens", maxModelCalls: 10, maxTokens: 150,
			firstReservation:  AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
			secondReservation: AttemptReservation{ModelCalls: 1, Tokens: 51, MaxOutputTokens: 50},
			want:              "token budget",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := reducerTestStateWithWorkers(t, now, 2)
			state.Spec.Budget.MaxModelCalls = test.maxModelCalls
			state.Spec.Budget.MaxTokens = test.maxTokens
			state = startReducerJob(t, state, now)
			batch := reducerTestBatch(t, state, 0, 1)
			state = reduceOK(t, state, Event{
				ID: "reservation-batch-" + test.name, Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch,
			})
			first := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
			first.Reservation = test.firstReservation
			state = reduceOK(t, state, Event{
				ID: "reservation-first-" + test.name, Type: EventAttemptStarted, At: now.Add(2 * time.Minute), Attempt: &first,
			})
			second := reducerTestAttempt(batch, batch.Items[1], 1, AttemptStatusRunning)
			second.Reservation = test.secondReservation
			assertReduceErrorUnchanged(t, state, Event{
				ID: "reservation-second-" + test.name, Type: EventAttemptStarted, At: now.Add(3 * time.Minute), Attempt: &second,
			}, test.want)
			if len(state.Attempts) != 1 || state.Usage.Attempts != 1 || !hasRunningAttempts(state) {
				t.Fatalf("rejected reservation changed running work: %#v", state)
			}
		})
	}
}

func TestReduceWaitingResumesDirectlyIntoNextCycle(t *testing.T) {
	now := reducerTestTime()
	state := startReducerJob(t, reducerTestState(t, now), now)
	wait := Decision{Kind: DecisionWait, Reason: "wait for operator evidence"}
	state = runReducerCycle(t, state, &wait, now.Add(time.Minute))
	state = reduceOK(t, state, Event{
		ID: "direct-wait", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &wait,
	})
	if state.Status != JobStatusWaiting || state.WaitingReason == "" || !state.NextWakeAt.IsZero() {
		t.Fatalf("wait decision state = %#v", state)
	}
	state = reduceOK(t, state, Event{
		ID: "direct-resume", Type: EventJobResumed, At: now.Add(21 * time.Minute),
	})
	if state.Status != JobStatusRunning || state.NextStageIndex != 0 || state.WaitingReason != "" {
		t.Fatalf("direct waiting resume = %#v", state)
	}
	next := reducerTestBatchForCycle(t, state.Spec, 0, 2)
	state = reduceOK(t, state, Event{
		ID: "direct-resume-next", Type: EventBatchStarted, At: now.Add(22 * time.Minute), Batch: &next,
	})
	if state.Cycle != 2 {
		t.Fatalf("direct resume started cycle %d, want 2", state.Cycle)
	}
}

func TestReduceContinuePersistsCadenceWaitAndCannotBeResumedEarly(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state.Spec.NotBeforeComplete = now.Add(6 * time.Hour)
	state.Spec.CycleCadenceSeconds = uint64(time.Hour / time.Second)
	state = startReducerJob(t, state, now)
	nextBatch := reducerTestBatchForCycle(t, state.Spec, 0, 2)
	decision := Decision{Kind: DecisionContinue, Reason: "review again", NextBatch: &nextBatch, Fingerprint: "cadence-fingerprint"}
	state = runReducerCycle(t, state, &decision, now.Add(time.Minute))
	decisionAt := now.Add(20 * time.Minute)
	state = reduceOK(t, state, Event{ID: "cadence-continue", Type: EventDecisionMade, At: decisionAt, Decision: &decision})
	wantWake := decisionAt.Add(time.Hour)
	if state.Status != JobStatusWaiting || !state.NextWakeAt.Equal(wantWake) || state.WaitingReason != "scheduled cycle cadence" {
		t.Fatalf("scheduled wait = %#v, want wake %s", state, wantWake)
	}
	assertReduceErrorUnchanged(t, state, Event{
		ID: "cadence-early-resume", Type: EventJobResumed, At: wantWake.Add(-time.Nanosecond),
	}, "continues until")
	state = reduceOK(t, state, Event{ID: "cadence-resume", Type: EventJobResumed, At: wantWake})
	if state.Status != JobStatusRunning || !state.NextWakeAt.IsZero() || state.WaitingReason != "" || state.NextStageIndex != 0 {
		t.Fatalf("scheduled resume = %#v", state)
	}
}

func TestReduceScheduledPauseResumePreservesWakeAndDeadlineCapsIt(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state.Spec.CycleCadenceSeconds = uint64((48 * time.Hour) / time.Second)
	state = startReducerJob(t, state, now)
	nextBatch := reducerTestBatchForCycle(t, state.Spec, 0, 2)
	decision := Decision{Kind: DecisionContinue, Reason: "review again", NextBatch: &nextBatch, Fingerprint: "deadline-cap"}
	state = runReducerCycle(t, state, &decision, now.Add(time.Minute))
	state = reduceOK(t, state, Event{ID: "deadline-capped-continue", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &decision})
	if !state.NextWakeAt.Equal(state.Spec.Deadline) {
		t.Fatalf("wake = %s, want deadline %s", state.NextWakeAt, state.Spec.Deadline)
	}
	wake := state.NextWakeAt
	state = reduceOK(t, state, Event{ID: "pause-scheduled", Type: EventJobPaused, At: now.Add(21 * time.Minute)})
	if state.Status != JobStatusPaused || !state.NextWakeAt.Equal(wake) {
		t.Fatalf("paused scheduled wait = %#v", state)
	}
	state = reduceOK(t, state, Event{ID: "resume-paused-early", Type: EventJobResumed, At: now.Add(22 * time.Minute)})
	if state.Status != JobStatusWaiting || !state.NextWakeAt.Equal(wake) {
		t.Fatalf("early paused resume bypassed cadence: %#v", state)
	}
}

func TestReduceRejectsSuccessfulCompletionBeforeRuntimeFloor(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state.Spec.NotBeforeComplete = now.Add(6 * time.Hour)
	state.Spec.CycleCadenceSeconds = uint64(time.Hour / time.Second)
	state = startReducerJob(t, state, now)
	decision := Decision{Kind: DecisionComplete, Reason: "premature"}
	state = runReducerCycle(t, state, &decision, now.Add(time.Minute))
	assertReduceErrorUnchanged(t, state, Event{
		ID: "premature-complete", Type: EventDecisionMade, At: now.Add(20 * time.Minute), Decision: &decision,
	}, "not allowed before")
}

func TestReduceRejectsReusedLastEventIDWithDifferentPayload(t *testing.T) {
	now := reducerTestTime()
	state := reduceOK(t, reducerTestState(t, now), Event{
		ID: "reused-event-id", Type: EventJobStarted, At: now,
	})
	assertReduceErrorUnchanged(t, state, Event{
		ID: "reused-event-id", Type: EventCancellationRequested, At: now.Add(time.Minute),
	}, "already used")
}

func TestReduceRejectsUnrelatedEventPayloads(t *testing.T) {
	now := reducerTestTime()
	state := reducerTestState(t, now)
	state.Attempts = []Attempt{}
	batch := reducerTestBatchForCycle(t, state.Spec, 0, 1)
	attempt := reducerTestAttempt(batch, batch.Items[0], 1, AttemptStatusRunning)
	decision := Decision{Kind: DecisionComplete, Reason: "unrelated"}
	tests := []struct {
		name  string
		event Event
	}{
		{
			name:  "batch on job started",
			event: Event{ID: "shape-start", Type: EventJobStarted, At: now, Batch: &batch},
		},
		{
			name:  "decision on batch started",
			event: Event{ID: "shape-batch", Type: EventBatchStarted, At: now, Batch: &batch, Decision: &decision},
		},
		{
			name:  "batch id on attempt started",
			event: Event{ID: "shape-attempt", Type: EventAttemptStarted, At: now, Attempt: &attempt, BatchID: batch.ID},
		},
		{
			name:  "usage on decision",
			event: Event{ID: "shape-decision", Type: EventDecisionMade, At: now, Decision: &decision, Usage: Usage{ModelCalls: 1}},
		},
		{
			name:  "reason on cancellation",
			event: Event{ID: "shape-cancel", Type: EventCancellationRequested, At: now, Reason: TerminalReasonDeadline},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertReduceErrorUnchanged(t, state, test.event, "unexpected")
		})
	}
}

func reducerTestTime() time.Time {
	return time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
}

func reducerTestState(t *testing.T, now time.Time) JobState {
	t.Helper()
	return reducerTestStateWithWorkers(t, now, 1)
}

func reducerTestStateWithWorkers(t *testing.T, now time.Time, workers int) JobState {
	t.Helper()
	return reducerTestStateForPreset(t, now, PresetGeneral, workers)
}

func reducerTestStateForPreset(t *testing.T, now time.Time, preset string, workers int) JobState {
	t.Helper()
	workflow, err := CompilePreset(preset, workers)
	if err != nil {
		t.Fatal(err)
	}
	return JobState{Spec: JobSpec{
		ID:        "job-test",
		Goal:      "Produce and verify one bounded result.",
		Preset:    preset,
		Workers:   workers,
		Deadline:  now.Add(24 * time.Hour),
		Budget:    Budget{MaxCycles: 8, MaxAttempts: 128, MaxModelCalls: 128, MaxTokens: 1_000_000},
		Route:     ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview"},
		Workflow:  WorkflowControlFromWorkflow(workflow),
		Authority: DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}, Status: JobStatusQueued}
}

func startReducerJob(t *testing.T, state JobState, at time.Time) JobState {
	t.Helper()
	return reduceOK(t, state, Event{ID: "job-start", Type: EventJobStarted, At: at})
}

func runReducerCycle(t *testing.T, state JobState, decision *Decision, at time.Time) JobState {
	t.Helper()
	for stageIndex := range state.Spec.Workflow.StageOrder {
		stageDecision := (*Decision)(nil)
		if stageIndex == len(state.Spec.Workflow.StageOrder)-1 {
			stageDecision = decision
		}
		state = runReducerStage(t, state, stageIndex, stageDecision, at.Add(time.Duration(stageIndex*4)*time.Minute))
	}
	return state
}

func runReducerStage(t *testing.T, state JobState, stageIndex int, decision *Decision, at time.Time) JobState {
	t.Helper()
	cycle := state.Cycle
	if stageIndex == 0 {
		cycle++
	}
	batch := reducerTestBatchForCycle(t, state.Spec, stageIndex, cycle)
	prefix := fmt.Sprintf("c%d-s%d", cycle, stageIndex)
	state = reduceOK(t, state, Event{ID: prefix + "-batch-start", Type: EventBatchStarted, At: at, Batch: &batch})
	for itemIndex, item := range batch.Items {
		attempt := reducerTestAttempt(batch, item, 1, AttemptStatusRunning)
		attempt.ID = fmt.Sprintf("attempt-c%d-s%d-i%d-n1", cycle, stageIndex, itemIndex)
		state = reduceOK(t, state, Event{ID: attempt.ID + "-start", Type: EventAttemptStarted, At: at.Add(time.Second), Attempt: &attempt})
		attempt.Status = AttemptStatusSucceeded
		attempt.Dispatched = true
		attempt.Result = fmt.Sprintf("result for %s", item.RoleID)
		attempt.Fingerprint = fmt.Sprintf("fp-c%d-s%d-i%d", cycle, stageIndex, itemIndex)
		attempt.Usage = Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5}
		if item.RoleID == state.Spec.Workflow.SupervisorRoleID {
			attempt.Decision = decision
		}
		state = reduceOK(t, state, Event{ID: attempt.ID + "-finish", Type: EventAttemptFinished, At: at.Add(2 * time.Second), Attempt: &attempt})
	}
	return reduceOK(t, state, Event{ID: prefix + "-batch-complete", Type: EventBatchCompleted, At: at.Add(3 * time.Second), BatchID: batch.ID})
}

func reducerTestBatch(t *testing.T, state JobState, stageIndex int, cycle uint64) WorkBatch {
	t.Helper()
	return reducerTestBatchForCycle(t, state.Spec, stageIndex, cycle)
}

func reducerTestBatchForCycle(t *testing.T, spec JobSpec, stageIndex int, cycle uint64) WorkBatch {
	t.Helper()
	stageID := spec.Workflow.StageOrder[stageIndex]
	var stage StageSpec
	for _, candidate := range spec.Stages {
		if candidate.ID == stageID {
			stage = candidate
			break
		}
	}
	batch := WorkBatch{ID: fmt.Sprintf("batch-c%d-s%d", cycle, stageIndex), StageID: stageID, Cycle: cycle, Barrier: BarrierAll}
	for itemIndex, roleID := range stage.RoleIDs {
		batch.Items = append(batch.Items, WorkItem{
			ID:        fmt.Sprintf("work-c%d-s%d-i%d", cycle, stageIndex, itemIndex),
			RoleID:    roleID,
			Objective: "Run one bounded stage task for " + roleID + ".",
			Authority: DenyAllAuthority(),
		})
	}
	return batch
}

func reducerTestAttempt(batch WorkBatch, item WorkItem, attemptNo uint64, status AttemptStatus) Attempt {
	return Attempt{
		ID:          fmt.Sprintf("attempt-%s-n%d", item.ID, attemptNo),
		BatchID:     batch.ID,
		WorkItemID:  item.ID,
		RoleID:      item.RoleID,
		AttemptNo:   attemptNo,
		Cycle:       batch.Cycle,
		StageID:     batch.StageID,
		Reservation: AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
		Dispatched:  status != AttemptStatusRunning,
		Status:      status,
	}
}

func reducerStageIndex(t *testing.T, spec JobSpec, stageID string) int {
	t.Helper()
	for index, candidate := range spec.Workflow.StageOrder {
		if candidate == stageID {
			return index
		}
	}
	t.Fatalf("stage %q not found", stageID)
	return -1
}

func reduceOK(t *testing.T, state JobState, event Event) JobState {
	t.Helper()
	next, err := Reduce(state, event)
	if err != nil {
		t.Fatalf("Reduce(%s/%s): %v", event.Type, event.ID, err)
	}
	return next
}

func assertReduceErrorUnchanged(t *testing.T, state JobState, event Event, contains string) {
	t.Helper()
	before := cloneJobState(state)
	got, err := Reduce(state, event)
	if !errors.Is(err, ErrInvalidEvent) || !strings.Contains(err.Error(), contains) {
		t.Fatalf("Reduce(%s) error = %v, want ErrInvalidEvent containing %q", event.Type, err, contains)
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected event changed state:\n got: %#v\nwant: %#v", got, before)
	}
}

func assertReducerState(t *testing.T, state JobState, status JobStatus, reason TerminalReason) {
	t.Helper()
	if state.Status != status || state.TerminalReason != reason {
		t.Fatalf("state status/reason = %q/%q, want %q/%q: %#v", state.Status, state.TerminalReason, status, reason, state)
	}
}
