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

func TestReduceLifecycleAndRevision(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerTestState(t, now)

	state = reduceOK(t, state, Event{ID: "event-start", Type: EventJobStarted, At: now})
	assertReducerState(t, state, JobStatusRunning, "", 1)

	beforeInvalid := cloneJobState(state)
	if got, err := Reduce(state, Event{ID: "event-start-again", Type: EventJobStarted, At: now.Add(time.Minute)}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second start error = %v, want ErrInvalidTransition", err)
	} else if !reflect.DeepEqual(got, beforeInvalid) {
		t.Fatalf("invalid transition changed state:\n got: %#v\nwant: %#v", got, beforeInvalid)
	}

	state = reduceOK(t, state, Event{ID: "event-pause", Type: EventJobPaused, At: now.Add(2 * time.Minute)})
	assertReducerState(t, state, JobStatusPaused, "", 2)

	state = reduceOK(t, state, Event{ID: "event-resume", Type: EventJobResumed, At: now.Add(3 * time.Minute)})
	assertReducerState(t, state, JobStatusRunning, "", 3)

	state = reduceOK(t, state, Event{
		ID:   "event-wait",
		Type: EventDecisionMade,
		At:   now.Add(4 * time.Minute),
		Decision: &Decision{
			Kind:   DecisionWait,
			Reason: "waiting for operator evidence",
		},
	})
	assertReducerState(t, state, JobStatusWaiting, "", 4)
	if state.WaitingReason != "waiting for operator evidence" {
		t.Fatalf("waiting reason = %q", state.WaitingReason)
	}

	state = reduceOK(t, state, Event{ID: "event-pause-waiting", Type: EventJobPaused, At: now.Add(5 * time.Minute)})
	assertReducerState(t, state, JobStatusPaused, "", 5)
	state = reduceOK(t, state, Event{ID: "event-resume-waiting", Type: EventJobResumed, At: now.Add(6 * time.Minute)})
	assertReducerState(t, state, JobStatusRunning, "", 6)
	if state.WaitingReason != "" {
		t.Fatalf("resume retained waiting reason %q", state.WaitingReason)
	}
}

func TestReduceTerminalReplayIsIdempotentAndTerminalIsImmutable(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerRunningState(t, now)
	complete := Event{
		ID:   "event-complete",
		Type: EventDecisionMade,
		At:   now.Add(time.Minute),
		Decision: &Decision{
			Kind:   DecisionComplete,
			Reason: "completion rubric satisfied",
		},
	}

	terminal := reduceOK(t, state, complete)
	assertReducerState(t, terminal, JobStatusCompleted, TerminalReasonSuccess, 2)

	replayed, err := Reduce(terminal, complete)
	if err != nil {
		t.Fatalf("terminal replay: %v", err)
	}
	if !reflect.DeepEqual(replayed, terminal) {
		t.Fatalf("terminal replay changed state:\n got: %#v\nwant: %#v", replayed, terminal)
	}

	got, err := Reduce(terminal, Event{
		ID:   "different-event",
		Type: EventJobCancelled,
		At:   now.Add(2 * time.Minute),
	})
	if !errors.Is(err, ErrTerminalState) {
		t.Fatalf("different terminal event error = %v, want ErrTerminalState", err)
	}
	if !reflect.DeepEqual(got, terminal) {
		t.Fatalf("post-terminal event changed state:\n got: %#v\nwant: %#v", got, terminal)
	}
}

func TestReduceHardPolicyPrecedence(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)

	t.Run("cancel_over_deadline_budget_and_completion", func(t *testing.T) {
		state := reducerRunningState(t, now)
		state.Spec.Deadline = now
		state.Usage = Usage{
			Cycles:      state.Spec.Budget.MaxCycles,
			Attempts:    state.Spec.Budget.MaxAttempts,
			ModelCalls:  state.Spec.Budget.MaxModelCalls,
			InputTokens: state.Spec.Budget.MaxTokens,
		}
		state = reduceOK(t, state, Event{
			ID:       "event-cancel",
			Type:     EventJobCancelled,
			At:       now.Add(time.Hour),
			Decision: &Decision{Kind: DecisionComplete, Reason: "model says done"},
		})
		assertReducerState(t, state, JobStatusCancelled, TerminalReasonOperatorCancellation, 2)
	})

	t.Run("deadline_over_completion", func(t *testing.T) {
		state := reducerRunningState(t, now)
		state.Spec.Deadline = now.Add(time.Minute)
		active := reducerTestBatch(1)
		state.CurrentBatch = &active
		state.Cycle = 1
		state = reduceOK(t, state, Event{
			ID:       "event-late-complete",
			Type:     EventDecisionMade,
			At:       state.Spec.Deadline,
			Decision: &Decision{Kind: DecisionComplete, Reason: "model says done"},
		})
		assertReducerState(t, state, JobStatusFailed, TerminalReasonDeadline, 2)
	})

	t.Run("budget_over_completion", func(t *testing.T) {
		state := reducerRunningState(t, now)
		state.Usage.Cycles = state.Spec.Budget.MaxCycles
		active := reducerTestBatch(1)
		state.CurrentBatch = &active
		state.Cycle = 1
		state = reduceOK(t, state, Event{
			ID:       "event-over-budget-complete",
			Type:     EventDecisionMade,
			At:       now.Add(time.Minute),
			Decision: &Decision{Kind: DecisionComplete, Reason: "model says done"},
		})
		assertReducerState(t, state, JobStatusFailed, TerminalReasonBudget, 2)
	})
}

func TestReduceStagnationStopsRepeatedContinuation(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerRunningState(t, now)

	for cycle := uint64(1); cycle <= StagnationFingerprintLimit; cycle++ {
		batch := reducerTestBatch(cycle)
		state = reduceOK(t, state, Event{
			ID:   fmt.Sprintf("decision-%d", cycle),
			Type: EventDecisionMade,
			At:   now.Add(time.Duration((cycle-1)*4+1) * time.Minute),
			Decision: &Decision{
				Kind:        DecisionContinue,
				Reason:      "another pass",
				NextBatch:   &batch,
				Fingerprint: "same-result",
			},
		})
		if cycle == StagnationFingerprintLimit {
			assertReducerState(t, state, JobStatusFailed, TerminalReasonStagnation, cycle*4-2)
			break
		}

		state = reduceOK(t, state, Event{
			ID:    fmt.Sprintf("batch-start-%d", cycle),
			Type:  EventBatchStarted,
			At:    now.Add(time.Duration((cycle-1)*4+2) * time.Minute),
			Batch: &batch,
		})
		state = reduceOK(t, state, Event{
			ID:   fmt.Sprintf("attempt-%d", cycle),
			Type: EventAttemptRecorded,
			At:   now.Add(time.Duration((cycle-1)*4+3) * time.Minute),
			Attempt: &Attempt{
				ID:          fmt.Sprintf("attempt-%d", cycle),
				BatchID:     batch.ID,
				WorkItemID:  batch.Items[0].ID,
				RoleID:      batch.Items[0].RoleID,
				Status:      AttemptStatusSucceeded,
				Result:      "bounded pass completed",
				Fingerprint: "same-result",
				Usage:       Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5},
			},
		})
		state = reduceOK(t, state, Event{
			ID:      fmt.Sprintf("batch-complete-%d", cycle),
			Type:    EventBatchCompleted,
			At:      now.Add(time.Duration(cycle*4) * time.Minute),
			BatchID: batch.ID,
		})
	}
	if got := consecutiveFingerprintCount(state.StagnationFingerprints, "same-result"); got != StagnationFingerprintLimit {
		t.Fatalf("consecutive fingerprint count = %d, want %d", got, StagnationFingerprintLimit)
	}
}

func TestReduceDecisionRejectsUndeclaredRoleAndLeavesInputUntouched(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerRunningState(t, now)
	before := cloneJobState(state)
	batch := reducerTestBatch(1)
	batch.Items[0].RoleID = "undeclared.role"

	got, err := Reduce(state, Event{
		ID:   "bad-decision",
		Type: EventDecisionMade,
		At:   now.Add(time.Minute),
		Decision: &Decision{
			Kind:        DecisionContinue,
			Reason:      "expand beyond catalog",
			NextBatch:   &batch,
			Fingerprint: "bad",
		},
	})
	if !errors.Is(err, ErrInvalidEvent) || !strings.Contains(err.Error(), "undeclared role") {
		t.Fatalf("decision error = %v, want undeclared-role ErrInvalidEvent", err)
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected decision changed state:\n got: %#v\nwant: %#v", got, before)
	}
}

func TestReduceBatchBarrierAndStableAttemptOrder(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerTestStateWithWorkers(t, now, 2)
	state = reduceOK(t, state, Event{ID: "event-start", Type: EventJobStarted, At: now})
	batch := WorkBatch{
		ID:      "batch-1",
		StageID: "explore",
		Cycle:   1,
		Barrier: BarrierAll,
		Items: []WorkItem{
			{
				ID:        "work-a",
				RoleID:    "general.alternative",
				Objective: "Develop an independent alternative.",
				Authority: DenyAllAuthority(),
			},
			{
				ID:        "work-b",
				RoleID:    "general.primary",
				Objective: "Develop the primary result.",
				Authority: DenyAllAuthority(),
			},
		},
	}
	state = reduceOK(t, state, Event{
		ID: "batch-start", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch,
	})
	state = reduceOK(t, state, Event{
		ID:   "attempt-b-event",
		Type: EventAttemptRecorded,
		At:   now.Add(2 * time.Minute),
		Attempt: &Attempt{
			ID:         "attempt-b",
			BatchID:    batch.ID,
			WorkItemID: "work-b",
			RoleID:     "general.primary",
			Status:     AttemptStatusSucceeded,
			Result:     "primary",
			Usage:      Usage{ModelCalls: 1, InputTokens: 8, OutputTokens: 4},
		},
	})

	beforeBarrier := cloneJobState(state)
	if got, err := Reduce(state, Event{
		ID: "early-barrier", Type: EventBatchCompleted, At: now.Add(3 * time.Minute), BatchID: batch.ID,
	}); !errors.Is(err, ErrInvalidEvent) || !strings.Contains(err.Error(), "work-a") {
		t.Fatalf("early barrier error = %v, want missing work-a ErrInvalidEvent", err)
	} else if !reflect.DeepEqual(got, beforeBarrier) {
		t.Fatalf("rejected barrier changed state:\n got: %#v\nwant: %#v", got, beforeBarrier)
	}

	state = reduceOK(t, state, Event{
		ID:   "attempt-a-event",
		Type: EventAttemptRecorded,
		At:   now.Add(4 * time.Minute),
		Attempt: &Attempt{
			ID:         "attempt-a",
			BatchID:    batch.ID,
			WorkItemID: "work-a",
			RoleID:     "general.alternative",
			Status:     AttemptStatusSucceeded,
			Result:     "alternative",
			Usage:      Usage{ModelCalls: 1, InputTokens: 7, OutputTokens: 3},
		},
	})
	if got := []string{state.Attempts[0].WorkItemID, state.Attempts[1].WorkItemID}; !reflect.DeepEqual(got, []string{"work-a", "work-b"}) {
		t.Fatalf("attempt result order = %v, want stable work-item order", got)
	}
	state = reduceOK(t, state, Event{
		ID: "barrier-complete", Type: EventBatchCompleted, At: now.Add(5 * time.Minute), BatchID: batch.ID,
	})
	if state.CurrentBatch != nil || state.Cycle != 1 {
		t.Fatalf("completed batch state = %#v", state)
	}
	if state.Usage.Cycles != 1 || state.Usage.Attempts != 2 || state.Usage.ModelCalls != 2 {
		t.Fatalf("completed batch usage = %#v", state.Usage)
	}
}

func TestReduceRecordsFinalAttemptBeforeBudgetTerminal(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerRunningState(t, now)
	state.Spec.Budget.MaxModelCalls = 1
	batch := reducerTestBatch(1)
	state = reduceOK(t, state, Event{
		ID: "batch-start", Type: EventBatchStarted, At: now.Add(time.Minute), Batch: &batch,
	})
	state = reduceOK(t, state, Event{
		ID:   "final-attempt-event",
		Type: EventAttemptRecorded,
		At:   now.Add(2 * time.Minute),
		Attempt: &Attempt{
			ID:         "final-attempt",
			BatchID:    batch.ID,
			WorkItemID: batch.Items[0].ID,
			RoleID:     batch.Items[0].RoleID,
			Status:     AttemptStatusSucceeded,
			Result:     "useful final result",
			Usage:      Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5},
		},
	})
	assertReducerState(t, state, JobStatusFailed, TerminalReasonBudget, 3)
	if len(state.Attempts) != 1 || state.Attempts[0].Result != "useful final result" {
		t.Fatalf("budget terminal discarded final attempt: %#v", state.Attempts)
	}
	if state.Usage.ModelCalls != 1 || state.Usage.Attempts != 1 {
		t.Fatalf("budget terminal usage = %#v", state.Usage)
	}
}

func TestReduceContinueBatchIsClonedAndCannotBeSubstituted(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerRunningState(t, now)
	batch := reducerTestBatch(1)
	state = reduceOK(t, state, Event{
		ID:   "continue",
		Type: EventDecisionMade,
		At:   now.Add(time.Minute),
		Decision: &Decision{
			Kind:        DecisionContinue,
			Reason:      "one bounded pass is needed",
			NextBatch:   &batch,
			Fingerprint: "hypothesis-v1",
		},
	})

	batch.Items[0].Objective = "mutated after reduction"
	if state.LastDecision.NextBatch.Items[0].Objective == batch.Items[0].Objective {
		t.Fatal("accepted decision aliases caller-owned batch")
	}
	if got, err := Reduce(state, Event{
		ID: "substituted-start", Type: EventBatchStarted, At: now.Add(2 * time.Minute), Batch: &batch,
	}); !errors.Is(err, ErrInvalidEvent) || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("substituted batch error = %v, want mismatch ErrInvalidEvent", err)
	} else if !reflect.DeepEqual(got, state) {
		t.Fatalf("substituted batch changed state:\n got: %#v\nwant: %#v", got, state)
	}
}

func TestReduceRejectsMalformedEnvelopeAndRevisionOverflow(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	state := reducerTestState(t, now)

	for _, event := range []Event{
		{Type: EventJobStarted, At: now},
		{ID: "missing-time", Type: EventJobStarted},
		{ID: "unknown", Type: EventType("provider_stop"), At: now},
	} {
		if _, err := Reduce(state, event); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("event %#v error = %v, want ErrInvalidEvent", event, err)
		}
	}

	state.Revision = math.MaxUint64
	if _, err := Reduce(state, Event{ID: "overflow", Type: EventJobStarted, At: now}); !errors.Is(err, ErrUsageOverflow) {
		t.Fatalf("revision overflow error = %v, want ErrUsageOverflow", err)
	}
}

func reducerTestState(t *testing.T, now time.Time) JobState {
	t.Helper()
	return reducerTestStateWithWorkers(t, now, 1)
}

func reducerTestStateWithWorkers(t *testing.T, now time.Time, workers int) JobState {
	t.Helper()
	workflow, err := CompilePreset(PresetGeneral, workers)
	if err != nil {
		t.Fatalf("compile reducer test preset: %v", err)
	}
	spec := JobSpec{
		ID:        "job-test",
		Goal:      "Produce and verify one bounded result.",
		Preset:    PresetGeneral,
		Workers:   workers,
		Deadline:  now.Add(24 * time.Hour),
		Budget:    Budget{MaxCycles: 8, MaxAttempts: 8, MaxModelCalls: 32, MaxTokens: 100_000},
		Authority: DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}
	return JobState{Spec: spec, Status: JobStatusQueued}
}

func reducerRunningState(t *testing.T, now time.Time) JobState {
	t.Helper()
	return reduceOK(t, reducerTestState(t, now), Event{
		ID:   "event-start",
		Type: EventJobStarted,
		At:   now,
	})
}

func reducerTestBatch(cycle uint64) WorkBatch {
	return WorkBatch{
		ID:      fmt.Sprintf("batch-%d", cycle),
		StageID: "explore",
		Cycle:   cycle,
		Barrier: BarrierAll,
		Items: []WorkItem{{
			ID:        fmt.Sprintf("work-%d", cycle),
			RoleID:    "general.primary",
			Objective: "Run an independent bounded pass.",
			Authority: DenyAllAuthority(),
		}},
	}
}

func reduceOK(t *testing.T, state JobState, event Event) JobState {
	t.Helper()
	next, err := Reduce(state, event)
	if err != nil {
		t.Fatalf("Reduce(%s): %v", event.Type, err)
	}
	return next
}

func assertReducerState(t *testing.T, state JobState, status JobStatus, reason TerminalReason, revision uint64) {
	t.Helper()
	if state.Status != status || state.TerminalReason != reason || state.Revision != revision {
		t.Fatalf(
			"state status/reason/revision = %q/%q/%d, want %q/%q/%d: %#v",
			state.Status,
			state.TerminalReason,
			state.Revision,
			status,
			reason,
			revision,
			state,
		)
	}
}
