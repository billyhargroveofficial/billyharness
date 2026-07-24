package jobruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

const (
	runnerTestProvider = "qwen"
	runnerTestModel    = "qwen3.8-max-preview"
)

func TestRunnerRetryableStepErrorClassification(t *testing.T) {
	runner := &Runner{}
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "nil", err: nil},
		{name: "provider transport", err: errors.New("provider transport unavailable"), retryable: true},
		{name: "runner busy", err: fmt.Errorf("wrapped: %w", ErrRunnerBusy), retryable: true},
		{name: "foreign conflict", err: ErrForeignConflict, retryable: true},
		{name: "control coordination", err: ErrJobNotControllable, retryable: true},
		{name: "store conflict", err: jobstore.ErrConflict, retryable: true},
		{name: "post-commit warning", err: jobstore.ErrCommitted, retryable: true},
		{name: "invoker conformance", err: ErrInvokerConformance},
		{name: "ambiguous writer", err: ErrAmbiguousWriter},
		{name: "workflow binding", err: ErrWorkflowBinding},
		{name: "invalid event", err: jobs.ErrInvalidEvent},
		{name: "invalid transition", err: jobs.ErrInvalidTransition},
		{name: "terminal reducer state", err: jobs.ErrTerminalState},
		{name: "usage overflow", err: jobs.ErrUsageOverflow},
		{name: "missing job", err: jobstore.ErrNotFound},
		{name: "duplicate job", err: jobstore.ErrAlreadyExists},
		{name: "corrupt store", err: jobstore.ErrCorrupt},
		{name: "tampered store", err: jobstore.ErrTampered},
		{name: "closed store", err: jobstore.ErrClosed},
		{name: "ownership failure", err: jobstore.ErrOwnership},
		{name: "oversized value", err: jobstore.ErrTooLarge},
		{name: "invalid id", err: jobstore.ErrInvalidID},
		{name: "fatal joined with transient", err: errors.Join(errors.New("temporary"), jobstore.ErrCorrupt)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := runner.RetryableStepError(test.err); got != test.retryable {
				t.Fatalf("RetryableStepError(%v) = %t, want %t", test.err, got, test.retryable)
			}
		})
	}
}

func TestRemainingJobBudgetExcludesUsageAndRunningReservations(t *testing.T) {
	state := jobs.JobState{
		Spec: jobs.JobSpec{Budget: jobs.Budget{
			MaxCycles: 8, MaxAttempts: 20, MaxModelCalls: 100, MaxTokens: 1_000,
		}},
		Usage: jobs.Usage{
			Cycles: 2, Attempts: 5, ModelCalls: 10, InputTokens: 100, OutputTokens: 50,
		},
		Attempts: []jobs.Attempt{
			{Status: jobs.AttemptStatusRunning, Reservation: jobs.AttemptReservation{ModelCalls: 7, Tokens: 200}},
			{Status: jobs.AttemptStatusSucceeded, Reservation: jobs.AttemptReservation{ModelCalls: 99, Tokens: 999}},
			{Status: jobs.AttemptStatusRunning, Reservation: jobs.AttemptReservation{ModelCalls: 3, Tokens: 100}},
		},
	}
	want := JobRemainingBudget{Cycles: 6, Attempts: 15, ModelCalls: 80, Tokens: 550}
	if got := remainingJobBudget(state); got != want {
		t.Fatalf("remaining job budget = %+v, want %+v", got, want)
	}
}

func TestRunnerCompletesGeneralWorkflow(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "general-complete", jobs.PresetGeneral, 2, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)

	invoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if state.Cycle != 1 || state.Usage.Cycles != 1 {
		t.Fatalf("cycle state = %d, usage = %d, want 1", state.Cycle, state.Usage.Cycles)
	}
	if len(state.Attempts) != 4 || state.Usage.Attempts != 4 || state.Usage.ModelCalls != 4 {
		t.Fatalf("attempts/usage = %d/%+v, want four attempts and calls", len(state.Attempts), state.Usage)
	}
	for _, attempt := range state.Attempts {
		if attempt.Status != jobs.AttemptStatusSucceeded {
			t.Fatalf("attempt %q status = %q", attempt.ID, attempt.Status)
		}
	}
	assertRunnerTestQwenInvocations(t, invoker.Invocations(), spec)
}

func TestRunnerCodingStageOrderAndWriterSerialization(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "coding-order", jobs.PresetCoding, 3, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)

	invoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)

	wantStages := []string{"analyze", "implement", "verify", "reduce", "supervise"}
	if got := compactRunnerTestStages(invoker.Invocations()); !slices.Equal(got, wantStages) {
		t.Fatalf("invocation stage order = %v, want %v", got, wantStages)
	}
	if got := invoker.MaxActiveForStage("implement"); got != 1 {
		t.Fatalf("implement max concurrency = %d, want 1", got)
	}
	writerCalls := 0
	for _, invocation := range invoker.Invocations() {
		if invocation.RoleID == "coding.implementer" {
			writerCalls++
			if invocation.StageID != "implement" {
				t.Fatalf("writer invoked in stage %q", invocation.StageID)
			}
		}
	}
	if writerCalls != 1 {
		t.Fatalf("writer calls = %d, want 1", writerCalls)
	}
}

func TestRunnerHonorsFourWorkerParallelCap(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "parallel-four", jobs.PresetGeneral, 4, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)

	entered := make(chan Invocation, 4)
	release := make(chan struct{})
	invoker := newRunnerTestInvoker(func(ctx context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		if invocation.StageID == "explore" {
			entered <- invocation
			select {
			case <-release:
			case <-ctx.Done():
				return InvocationResult{}, ctx.Err()
			}
		}
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(context.Background(), spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestInvocations(t, entered, 4)
	if got := invoker.MaxActiveForStage("explore"); got != 4 {
		t.Fatalf("explore max concurrency = %d, want 4", got)
	}
	if got := invoker.MaxActive(); got > 4 {
		t.Fatalf("global max concurrency = %d, exceeds 4", got)
	}
	close(release)
	completed := waitRunnerTestRun(t, result)
	if completed.err != nil {
		t.Fatalf("Run(): %v", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if got := invoker.MaxActive(); got != 4 {
		t.Fatalf("global max concurrency = %d, want 4", got)
	}
}

func TestRunnerSupervisorContinueThenComplete(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "continue-complete", jobs.PresetGeneral, 2, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)

	var supervisorCalls atomic.Int64
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		result := runnerTestSuccess(invocation)
		if invocation.Kind != InvocationKindSupervisor {
			return result, nil
		}
		if supervisorCalls.Add(1) == 1 {
			objectives := make(map[string]string, len(invocation.AllowedNextRoleIDs))
			for _, roleID := range invocation.AllowedNextRoleIDs {
				objectives[roleID] = "cycle two objective for " + roleID
			}
			result.Proposal = &SupervisorProposal{
				Kind: jobs.DecisionContinue, Reason: "Run one independent verification cycle.", NextObjectives: objectives,
			}
		}
		return result, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if state.Cycle != 2 || state.Usage.Cycles != 2 || supervisorCalls.Load() != 2 {
		t.Fatalf("cycles/supervisor calls = %d/%d/%d, want 2/2/2", state.Cycle, state.Usage.Cycles, supervisorCalls.Load())
	}
	if len(state.Attempts) != 8 {
		t.Fatalf("attempt count = %d, want 8", len(state.Attempts))
	}
	cycleTwoWorkers := 0
	for _, invocation := range invoker.Invocations() {
		if invocation.Cycle == 2 && invocation.StageID == "explore" {
			cycleTwoWorkers++
			want := "cycle two objective for " + invocation.RoleID
			if invocation.Objective != want {
				t.Fatalf("cycle-two objective for %q = %q, want %q", invocation.RoleID, invocation.Objective, want)
			}
		}
	}
	if cycleTwoWorkers != 2 {
		t.Fatalf("cycle-two worker calls = %d, want 2", cycleTwoWorkers)
	}
}

func TestRunnerRepairsFailedControlAttemptsBeforeClosingBarrier(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "control-repair", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)

	var reducerCalls, supervisorCalls atomic.Int64
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		result := runnerTestSuccess(invocation)
		switch invocation.Kind {
		case InvocationKindReducer:
			if reducerCalls.Add(1) == 1 {
				result.Status = jobs.AttemptStatusFailed
				result.Result = ""
				result.Error = "reducer output needs repair"
			}
		case InvocationKindSupervisor:
			if supervisorCalls.Add(1) < int64(DefaultMaxControlAttempts) {
				result.Status = jobs.AttemptStatusFailed
				result.Result = ""
				result.Error = "supervisor JSON needs repair"
				result.Proposal = nil
			}
		}
		return result, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if reducerCalls.Load() != 2 || supervisorCalls.Load() != int64(DefaultMaxControlAttempts) {
		t.Fatalf("control calls reducer/supervisor = %d/%d, want 2/%d", reducerCalls.Load(), supervisorCalls.Load(), DefaultMaxControlAttempts)
	}
	var supervisorAttempts []jobs.Attempt
	for _, attempt := range state.Attempts {
		if attempt.RoleID == state.Spec.Workflow.SupervisorRoleID {
			supervisorAttempts = append(supervisorAttempts, attempt)
		}
	}
	if len(supervisorAttempts) != int(DefaultMaxControlAttempts) ||
		supervisorAttempts[len(supervisorAttempts)-1].Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("supervisor attempts = %#v", supervisorAttempts)
	}
}

func TestRunnerRepairsEarlyCompleteAndHonorsMinimumCycles(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "minimum-cycle-repair", jobs.PresetResearch, 1, time.Now().Add(time.Hour))
	spec.MinCycles = 2
	createRunnerTestJob(t, store, spec)

	var supervisorCalls atomic.Int64
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		result := runnerTestSuccess(invocation)
		if invocation.Kind != InvocationKindSupervisor {
			return result, nil
		}
		call := supervisorCalls.Add(1)
		if invocation.Cycle == 1 && call == 2 {
			objectives := make(map[string]string, len(invocation.AllowedNextRoleIDs))
			for _, roleID := range invocation.AllowedNextRoleIDs {
				objectives[roleID] = "run a second independent verification for " + roleID
			}
			result.Proposal = &SupervisorProposal{
				Kind: jobs.DecisionContinue, Reason: "minimum quality cycle remains", NextObjectives: objectives,
			}
		}
		return result, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if state.Usage.Cycles != 2 || supervisorCalls.Load() != 3 {
		t.Fatalf("cycles/supervisor calls = %d/%d, want 2/3", state.Usage.Cycles, supervisorCalls.Load())
	}
	var firstSupervisor jobs.Attempt
	for _, attempt := range state.Attempts {
		if attempt.RoleID == state.Spec.Workflow.SupervisorRoleID && attempt.Cycle == 1 && attempt.AttemptNo == 1 {
			firstSupervisor = attempt
			break
		}
	}
	if firstSupervisor.Status != jobs.AttemptStatusFailed || !strings.Contains(firstSupervisor.Error, "min_cycles 2") {
		t.Fatalf("early supervisor attempt = %#v", firstSupervisor)
	}
}

func TestRunnerExpireDueTerminalizesQueuedAndPausedWithoutStartingWork(t *testing.T) {
	deadline := time.Date(2026, time.July, 24, 8, 0, 0, 0, time.UTC)
	for _, status := range []jobs.JobStatus{jobs.JobStatusQueued, jobs.JobStatusPaused} {
		t.Run(string(status), func(t *testing.T) {
			store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
			spec := runnerTestSpec(t, "expire-"+string(status), jobs.PresetGeneral, 1, deadline)
			state := createRunnerTestJob(t, store, spec)
			if status == jobs.JobStatusPaused {
				var err error
				state, err = store.Append(context.Background(), spec.ID, state.Revision, jobs.Event{
					ID: "pause-before-deadline", Type: jobs.EventJobPaused, At: deadline.Add(-time.Second),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			invoker := newRunnerTestInvoker(nil)
			runner := newRunnerTestRunner(t, store, invoker, func() time.Time { return deadline.Add(time.Minute) }, RunnerOptions{})
			expired, progressed, err := runner.ExpireDue(context.Background(), spec.ID)
			if err != nil || !progressed {
				t.Fatalf("ExpireDue() = progressed %v, err %v", progressed, err)
			}
			if expired.Status != jobs.JobStatusFailed || expired.TerminalReason != jobs.TerminalReasonDeadline || len(invoker.Invocations()) != 0 {
				t.Fatalf("expired state = %#v, invocations=%d", expired, len(invoker.Invocations()))
			}
		})
	}

	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "not-due-queued", jobs.PresetGeneral, 1, deadline)
	createRunnerTestJob(t, store, spec)
	runner := newRunnerTestRunner(t, store, newRunnerTestInvoker(nil), func() time.Time { return deadline.Add(-time.Minute) }, RunnerOptions{})
	state, progressed, err := runner.ExpireDue(context.Background(), spec.ID)
	if err != nil || progressed || state.Status != jobs.JobStatusQueued {
		t.Fatalf("non-expired ExpireDue() = state %#v, progressed %v, err %v", state, progressed, err)
	}
}

func TestRunnerWaitingStillTerminatesAtDeadlineAndCarriesSchedule(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	now := time.Now().UTC()
	deadline := now.Add(30 * time.Second)
	spec := runnerTestSpec(t, "waiting-deadline", jobs.PresetResearch, 1, deadline)
	spec.NotBeforeComplete = now.Add(10 * time.Second)
	spec.CycleCadenceSeconds = 5
	createRunnerTestJob(t, store, spec)

	clock := &runnerTestClock{now: now}
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		result := runnerTestSuccess(invocation)
		if invocation.Kind == InvocationKindSupervisor {
			result.Proposal = &SupervisorProposal{Kind: jobs.DecisionWait, Reason: "external evidence required"}
		}
		return result, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, clock.Now, RunnerOptions{})
	waiting, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if waiting.Status != jobs.JobStatusWaiting || !waiting.NextWakeAt.IsZero() {
		t.Fatalf("manual waiting state = %#v", waiting)
	}
	var supervisorInvocation *Invocation
	for _, invocation := range invoker.Invocations() {
		if invocation.Kind == InvocationKindSupervisor {
			copy := invocation
			supervisorInvocation = &copy
		}
	}
	if supervisorInvocation == nil || !supervisorInvocation.NotBeforeComplete.Equal(spec.NotBeforeComplete) || supervisorInvocation.CycleCadenceSeconds != 5 {
		t.Fatalf("supervisor invocation schedule = %#v", supervisorInvocation)
	}

	clock.Set(deadline)
	terminal, progressed, err := runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("deadline Step() = progressed %t, err %v", progressed, err)
	}
	assertRunnerTestTerminal(t, terminal, jobs.JobStatusFailed, jobs.TerminalReasonDeadline)
}

func TestRunnerFailsAfterBoundedSupervisorRepairAttempts(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "control-repair-exhausted", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)

	var supervisorCalls atomic.Int64
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		result := runnerTestSuccess(invocation)
		if invocation.Kind == InvocationKindSupervisor {
			supervisorCalls.Add(1)
			result.Status = jobs.AttemptStatusFailed
			result.Result = ""
			result.Error = "persistently invalid supervisor JSON"
			result.Proposal = nil
		}
		return result, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if supervisorCalls.Load() != int64(DefaultMaxControlAttempts) {
		t.Fatalf("supervisor calls = %d, want %d", supervisorCalls.Load(), DefaultMaxControlAttempts)
	}
}

func TestRunnerRecoversReadOrphanAfterReopenAndRetries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jobs")
	store := newRunnerTestStore(t, root)
	spec := runnerTestSpec(t, "read-orphan", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	state := createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})

	state = runnerTestStep(t, runner, spec.ID)
	state = runnerTestStep(t, runner, spec.ID)
	if state.CurrentBatch == nil || state.CurrentBatch.StageID != "explore" {
		t.Fatalf("active batch = %#v, want explore", state.CurrentBatch)
	}
	orphan := runnerTestRunningAttempt("read-orphan-attempt", *state.CurrentBatch, state.CurrentBatch.Items[0])
	var err error
	state, err = store.Append(context.Background(), spec.ID, state.Revision, jobs.Event{
		ID: "read-orphan-start", Type: jobs.EventAttemptStarted, At: time.Now(), Attempt: &orphan,
	})
	if err != nil {
		t.Fatalf("persist orphan start: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened := newRunnerTestStore(t, root)
	retryInvoker := newRunnerTestInvoker(nil)
	reopenedRunner := newRunnerTestRunner(t, reopened, retryInvoker, nil, RunnerOptions{})
	recovered, progressed, err := reopenedRunner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("orphan recovery Step() = progressed %v, err %v", progressed, err)
	}
	if len(recovered.Attempts) != 1 || recovered.Attempts[0].Status != jobs.AttemptStatusAbandoned {
		t.Fatalf("recovered attempts = %#v, want one abandoned", recovered.Attempts)
	}

	retried, progressed, err := reopenedRunner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("retry Step() = progressed %v, err %v", progressed, err)
	}
	if len(retried.Attempts) != 2 || retried.Attempts[1].AttemptNo != 2 || retried.Attempts[1].Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("retry attempts = %#v", retried.Attempts)
	}
	if got := len(retryInvoker.Invocations()); got != 1 {
		t.Fatalf("retry invocations = %d, want 1", got)
	}
}

func TestRunnerDefaultBudgetSurvivesFourWayReadBatchRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jobs")
	store := newRunnerTestStore(t, root)
	spec := runnerTestSpec(t, "four-way-restart-headroom", jobs.PresetGeneral, 4, time.Now().Add(time.Hour))
	// Match the public job defaults that the per-attempt defaults are designed
	// around. Four unknown read outcomes may consume only half, leaving enough
	// capacity to replay the entire workflow after restart.
	spec.Budget = jobs.Budget{
		MaxCycles: 4, MaxAttempts: 128, MaxModelCalls: 128, MaxTokens: 1_000_000,
	}
	state := createRunnerTestJob(t, store, spec)
	runner := newRunnerTestRunner(t, store, newRunnerTestInvoker(nil), nil, RunnerOptions{})
	state = runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")
	if len(state.CurrentBatch.Items) != 4 {
		t.Fatalf("explore items = %d, want 4", len(state.CurrentBatch.Items))
	}
	for index, item := range state.CurrentBatch.Items {
		attempt := runnerTestRunningAttempt(fmt.Sprintf("restart-orphan-%d", index), *state.CurrentBatch, item)
		attempt.Reservation = jobs.AttemptReservation{
			ModelCalls:      DefaultMaxModelCallsPerAttempt,
			Tokens:          DefaultMaxTokensPerAttempt,
			MaxOutputTokens: DefaultMaxOutputTokensPerCall,
		}
		state = runnerTestAppendEvent(t, store, state, jobs.Event{
			ID: fmt.Sprintf("restart-orphan-start-%d", index), Type: jobs.EventAttemptStarted,
			At: time.Now().UTC(), Attempt: &attempt,
		})
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened := newRunnerTestStore(t, root)
	retryInvoker := newRunnerTestInvoker(nil)
	reopenedRunner := newRunnerTestRunner(t, reopened, retryInvoker, nil, RunnerOptions{})
	completed, err := reopenedRunner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run after restart: %v", err)
	}
	assertRunnerTestTerminal(t, completed, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if completed.Usage.ModelCalls >= spec.Budget.MaxModelCalls || completed.Usage.TotalTokens() >= spec.Budget.MaxTokens {
		t.Fatalf("restart consumed entire default budget: usage=%+v budget=%+v", completed.Usage, spec.Budget)
	}
	byID := make(map[string]jobs.Attempt, len(completed.Attempts))
	for _, attempt := range completed.Attempts {
		byID[attempt.ID] = attempt
	}
	for index := 0; index < 4; index++ {
		attempt, ok := byID[fmt.Sprintf("restart-orphan-%d", index)]
		if !ok {
			t.Fatalf("orphan %d missing from attempts", index)
		}
		if attempt.Status != jobs.AttemptStatusAbandoned {
			t.Fatalf("orphan %d status = %q, want abandoned", index, attempt.Status)
		}
		assertRunnerTestReservationBurned(t, attempt)
	}
	if got := len(retryInvoker.Invocations()); got != 6 {
		t.Fatalf("post-restart invocations = %d, want four workers + reducer + supervisor", got)
	}
}

func TestRunnerWriterOrphanAfterReopenIsUnrecoverableAndNotReinvoked(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jobs")
	store := newRunnerTestStore(t, root)
	spec := runnerTestSpec(t, "writer-orphan", jobs.PresetCoding, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	preCrashInvoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, store, preCrashInvoker, nil, RunnerOptions{})
	state := runnerTestAdvanceToBatch(t, runner, spec.ID, "implement")
	writer := runnerTestRunningAttempt("writer-orphan-attempt", *state.CurrentBatch, state.CurrentBatch.Items[0])
	state, err := store.Append(context.Background(), spec.ID, state.Revision, jobs.Event{
		ID: "writer-orphan-start", Type: jobs.EventAttemptStarted, At: time.Now(), Attempt: &writer,
	})
	if err != nil {
		t.Fatalf("persist writer start: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened := newRunnerTestStore(t, root)
	postCrashInvoker := newRunnerTestInvoker(nil)
	reopenedRunner := newRunnerTestRunner(t, reopened, postCrashInvoker, nil, RunnerOptions{})
	failed, progressed, err := reopenedRunner.Step(context.Background(), spec.ID)
	if !errors.Is(err, ErrAmbiguousWriter) {
		t.Fatalf("writer recovery error = %v, want ErrAmbiguousWriter", err)
	}
	if progressed {
		t.Fatal("writer recovery unexpectedly reported progress despite error")
	}
	assertRunnerTestTerminal(t, failed, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if got := len(postCrashInvoker.Invocations()); got != 0 {
		t.Fatalf("post-crash writer invocations = %d, want 0", got)
	}
	found := false
	for _, attempt := range failed.Attempts {
		if attempt.ID == writer.ID {
			found = true
			if attempt.Status != jobs.AttemptStatusAmbiguous {
				t.Fatalf("writer orphan status = %q, want ambiguous", attempt.Status)
			}
		}
	}
	if !found {
		t.Fatalf("writer attempt %q missing after recovery", writer.ID)
	}
}

func TestRunnerPersistsFactualOutputLimitExcessAndStopsForBudget(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "conformance-failure", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		return InvocationResult{
			Status: jobs.AttemptStatusSucceeded, Result: "overspent", Usage: jobs.Usage{ModelCalls: 1, OutputTokens: 3},
		}, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{MaxOutputTokensPerCall: 2})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonBudget)
	if len(state.Attempts) != 1 || state.Attempts[0].Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("attempts = %#v, want one factual succeeded attempt", state.Attempts)
	}
	if state.Attempts[0].Usage.OutputTokens != 2 || state.Usage.OutputTokens != 3 {
		t.Fatalf("attempt/job usage = %+v/%+v, want charged output 2 and factual output 3", state.Attempts[0].Usage, state.Usage)
	}
	if got := len(invoker.Invocations()); got != 1 {
		t.Fatalf("invocations = %d, want 1", got)
	}
}

func TestRunnerUndispatchedWriterFailureIsNeverAmbiguous(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "writer-preflight-failure", jobs.PresetCoding, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	preflightErr := errors.New("credentials unavailable before provider construction")
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		if invocation.RoleID == "coding.implementer" {
			return InvocationResult{}, NewFatalPreflightFailure(preflightErr)
		}
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "implement")
	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if progressed || !errors.Is(err, ErrInvokerConformance) {
		t.Fatalf("writer Step() = progressed %v, err %v", progressed, err)
	}
	attempt := state.Attempts[len(state.Attempts)-1]
	if attempt.RoleID != "coding.implementer" || attempt.Status != jobs.AttemptStatusFailed || attempt.Dispatched || attempt.Usage != (jobs.Usage{}) {
		t.Fatalf("undispatched writer attempt = %#v", attempt)
	}
	if state.Status != jobs.JobStatusFailed || state.TerminalReason != jobs.TerminalReasonUnrecoverable {
		t.Fatalf("job status/reason = %q/%q, want failed/unrecoverable", state.Status, state.TerminalReason)
	}
	foundWriterInvocation := false
	for _, invocation := range invoker.Invocations() {
		if invocation.RoleID == "coding.implementer" {
			foundWriterInvocation = true
			if !invocation.Writer {
				t.Fatalf("writer role materialized without Writer provenance: %#v", invocation)
			}
		}
	}
	if !foundWriterInvocation {
		t.Fatal("writer invocation was not recorded")
	}
}

func TestRunnerConformanceFailureDrainsParallelSiblings(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "conformance-drains", jobs.PresetGeneral, 2, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, ordinal int) (InvocationResult, error) {
		if invocation.StageID == "explore" && ordinal == 0 {
			// Missing token usage violates the normalized Invoker contract.
			return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "invalid", Usage: jobs.Usage{ModelCalls: 1}}, nil
		}
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if !errors.Is(err, ErrInvokerConformance) {
		t.Fatalf("Run() error = %v, want ErrInvokerConformance", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if len(state.Attempts) != 2 {
		t.Fatalf("attempts = %d, want both parallel siblings drained", len(state.Attempts))
	}
	for _, attempt := range state.Attempts {
		if attempt.Status == jobs.AttemptStatusRunning {
			t.Fatalf("parallel sibling %q remained running", attempt.ID)
		}
	}
}

func TestRunnerDeadlineDrainsAllStartedInvocations(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	deadline := time.Now().Add(time.Hour).UTC()
	spec := runnerTestSpec(t, "deadline-drain", jobs.PresetGeneral, 4, deadline)
	createRunnerTestJob(t, store, spec)
	clock := &runnerTestClock{now: deadline.Add(-time.Minute)}
	entered := make(chan Invocation, 4)
	release := make(chan struct{})
	invoker := newRunnerTestInvoker(func(ctx context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		entered <- invocation
		select {
		case <-release:
			return runnerTestSuccess(invocation), nil
		case <-ctx.Done():
			return InvocationResult{}, ctx.Err()
		}
	})
	runner := newRunnerTestRunner(t, store, invoker, clock.Now, RunnerOptions{})
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(context.Background(), spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestInvocations(t, entered, 4)
	clock.Set(deadline.Add(time.Second))
	close(release)
	drained := waitRunnerTestRun(t, result)
	if drained.err != nil {
		t.Fatalf("Run(): %v", drained.err)
	}
	assertRunnerTestTerminal(t, drained.state, jobs.JobStatusFailed, jobs.TerminalReasonDeadline)
	if len(drained.state.Attempts) != 4 {
		t.Fatalf("drained attempts = %d, want 4", len(drained.state.Attempts))
	}
	for _, attempt := range drained.state.Attempts {
		if attempt.Status == jobs.AttemptStatusRunning {
			t.Fatalf("attempt %q remained running after deadline drain", attempt.ID)
		}
	}
	if got := len(invoker.Invocations()); got != 4 {
		t.Fatalf("invocations = %d, want only the four already started", got)
	}
}

func TestRunnerOperatorCancellationDrainsBlockedInvocation(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "operator-cancel", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	entered := make(chan Invocation, 1)
	invoker := newRunnerTestInvoker(func(ctx context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		entered <- invocation
		<-ctx.Done()
		return InvocationResult{}, ctx.Err()
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(context.Background(), spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestInvocations(t, entered, 1)
	requested, err := runner.RequestCancel(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("RequestCancel(): %v", err)
	}
	if !requested.CancelRequested {
		t.Fatal("cancellation intent was not persisted")
	}
	cancelled := waitRunnerTestRun(t, result)
	if cancelled.err != nil {
		t.Fatalf("Run(): %v", cancelled.err)
	}
	assertRunnerTestTerminal(t, cancelled.state, jobs.JobStatusCancelled, jobs.TerminalReasonOperatorCancellation)
	if len(cancelled.state.Attempts) != 1 || cancelled.state.Attempts[0].Status != jobs.AttemptStatusCancelled {
		t.Fatalf("cancelled attempts = %#v", cancelled.state.Attempts)
	}
}

func TestRunnerRejectsSameProcessDuplicateRun(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "duplicate-run", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	entered := make(chan Invocation, 1)
	invoker := newRunnerTestInvoker(func(ctx context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		entered <- invocation
		<-ctx.Done()
		return InvocationResult{}, ctx.Err()
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	first := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(context.Background(), spec.ID)
		first <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestInvocations(t, entered, 1)
	if _, err := runner.Run(context.Background(), spec.ID); !errors.Is(err, ErrRunnerBusy) {
		t.Fatalf("duplicate Run() error = %v, want ErrRunnerBusy", err)
	}
	if _, err := runner.RequestCancel(context.Background(), spec.ID); err != nil {
		t.Fatalf("cleanup RequestCancel(): %v", err)
	}
	completed := waitRunnerTestRun(t, first)
	if completed.err != nil {
		t.Fatalf("first Run(): %v", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusCancelled, jobs.TerminalReasonOperatorCancellation)
}

func TestRunnerReconcilesExactCommittedAppendWithoutReinvocation(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "committed-reconcile", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, base, spec)
	faulting := &runnerTestCommittedStore{Store: base, target: jobs.EventAttemptFinished}
	invoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, faulting, invoker, nil, RunnerOptions{})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusCompleted, jobs.TerminalReasonSuccess)
	if !faulting.Fired() {
		t.Fatal("committed append fault was not injected")
	}
	if len(invoker.Invocations()) != 3 || len(state.Attempts) != 3 {
		t.Fatalf("invocations/attempts = %d/%d, want 3/3 without reinvocation", len(invoker.Invocations()), len(state.Attempts))
	}
	if state.Revision != 14 {
		t.Fatalf("revision = %d, want 14 exact events without duplicate append", state.Revision)
	}
}

func TestRunnerBindingMismatchDrainsPersistedRunningAttemptBeforeFailing(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "binding-mismatch-running", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	// JobSpec validation deliberately permits authority bindings and cannot know
	// whether immutable built-in role metadata drifted. Runner owns that check.
	spec.Roles[0].Purpose += " drifted after compilation"
	state := createRunnerTestJob(t, store, spec)
	state = runnerTestAppendEvent(t, store, state, jobs.Event{
		ID: "binding-start", Type: jobs.EventJobStarted, At: time.Now(),
	})
	stageID := spec.Workflow.StageOrder[0]
	stage := runnerTestStage(t, spec, stageID)
	role := runnerTestRole(t, spec, stage.RoleIDs[0])
	batch := jobs.WorkBatch{
		ID: "binding-mismatch-batch", StageID: stageID, Cycle: 1, Barrier: jobs.BarrierAll,
		Items: []jobs.WorkItem{{
			ID: "binding-mismatch-item", RoleID: role.ID, Objective: "Persisted work before restart.", Authority: role.Authority,
		}},
	}
	state = runnerTestAppendEvent(t, store, state, jobs.Event{
		ID: "binding-batch", Type: jobs.EventBatchStarted, At: time.Now(), Batch: &batch,
	})
	orphan := runnerTestRunningAttempt("binding-mismatch-attempt", batch, batch.Items[0])
	state = runnerTestAppendEvent(t, store, state, jobs.Event{
		ID: "binding-attempt", Type: jobs.EventAttemptStarted, At: time.Now(), Attempt: &orphan,
	})
	state = runnerTestAppendEvent(t, store, state, jobs.Event{
		ID: "binding-cancel", Type: jobs.EventCancellationRequested, At: time.Now(),
	})
	if state.Attempts[0].Status != jobs.AttemptStatusRunning {
		t.Fatalf("fixture attempt status = %q", state.Attempts[0].Status)
	}

	invoker := newRunnerTestInvoker(nil)
	clock := &runnerTestClock{now: spec.Deadline.Add(time.Second)}
	runner := newRunnerTestRunner(t, store, invoker, clock.Now, RunnerOptions{})
	failed, err := runner.Run(context.Background(), spec.ID)
	if !errors.Is(err, ErrWorkflowBinding) {
		t.Fatalf("Run() error = %v, want ErrWorkflowBinding", err)
	}
	assertRunnerTestTerminal(t, failed, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if len(failed.Attempts) != 1 || failed.Attempts[0].Status != jobs.AttemptStatusAbandoned {
		t.Fatalf("drained attempts = %#v, want one abandoned read attempt", failed.Attempts)
	}
	if got := len(invoker.Invocations()); got != 0 {
		t.Fatalf("invocations = %d, want no dispatch under mismatched binding", got)
	}
}

func TestRunnerDispatchedTransportErrorBurnsWholeReservation(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "transport-burns-reservation", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	transportErr := errors.New("connection reset after dispatch")
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		return InvocationResult{}, transportErr
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")
	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("transport Step() = progressed %v, err %v", progressed, err)
	}
	if len(state.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(state.Attempts))
	}
	attempt := state.Attempts[0]
	if attempt.Status != jobs.AttemptStatusFailed {
		t.Fatalf("attempt status = %q, want failed", attempt.Status)
	}
	assertRunnerTestReservationBurned(t, attempt)
	if state.Usage.ModelCalls != attempt.Reservation.ModelCalls || state.Usage.TotalTokens() != attempt.Reservation.Tokens {
		t.Fatalf("job usage = %+v, reservation = %+v", state.Usage, attempt.Reservation)
	}
}

func TestRunnerRetriesTransientNoGenerationWithoutBurningTokenReservation(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "transient-no-generation", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	transientCause := errors.New("qwen rate limited before generation")
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, ordinal int) (InvocationResult, error) {
		if ordinal == 0 {
			return InvocationResult{Usage: jobs.Usage{ModelCalls: 1}}, NewTransientInvocationFailure(
				transientCause, DispatchDispatched, UsageNoGeneration, 2*time.Second,
			)
		}
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")

	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if progressed || !errors.Is(err, ErrTransientInvocation) {
		t.Fatalf("transient Step() = progressed %v err %v", progressed, err)
	}
	var retry *TransientInvocationError
	if !errors.As(err, &retry) || retry.RetryDelay() != 2*time.Second {
		t.Fatalf("transient error = %#v", err)
	}
	if len(state.Attempts) != 1 || state.Attempts[0].Status != jobs.AttemptStatusAbandoned {
		t.Fatalf("attempts after rejection = %#v", state.Attempts)
	}
	if state.Usage.ModelCalls != 1 || state.Usage.TotalTokens() != 0 {
		t.Fatalf("usage after rejection = %#v", state.Usage)
	}

	state, progressed, err = runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("retry Step() = progressed %v err %v", progressed, err)
	}
	if len(state.Attempts) != 2 || state.Attempts[1].Status != jobs.AttemptStatusSucceeded || state.Attempts[1].AttemptNo != 2 {
		t.Fatalf("attempts after retry = %#v", state.Attempts)
	}
}

func TestRunnerDoesNotRetryNonTransientNoGenerationFailure(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "nontransient-no-generation", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	cause := errors.New("provider rejected credentials before generation")
	invoker := newRunnerTestInvoker(func(_ context.Context, _ Invocation, _ int) (InvocationResult, error) {
		return InvocationResult{Usage: jobs.Usage{ModelCalls: 1}}, NewInvocationFailure(
			cause, DispatchDispatched, UsageNoGeneration,
		)
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")

	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed || runner.RetryableStepError(err) {
		t.Fatalf("nontransient Step() = progressed %v err %v retryable %v", progressed, err, runner.RetryableStepError(err))
	}
	if len(state.Attempts) != 1 || state.Attempts[0].Status != jobs.AttemptStatusFailed || state.Usage.ModelCalls != 1 {
		t.Fatalf("nontransient attempts/usage = %#v / %#v", state.Attempts, state.Usage)
	}
	if got := len(invoker.Invocations()); got != 1 {
		t.Fatalf("invocations = %d, want 1", got)
	}

	state, progressed, err = runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("barrier Step() = progressed %v err %v", progressed, err)
	}
	if got := len(invoker.Invocations()); got != 1 {
		t.Fatalf("nontransient failure was redispatched: invocations=%d", got)
	}
	if state.CurrentBatch != nil || state.NextStageIndex != 1 {
		t.Fatalf("failed attempt did not resolve its barrier: %#v", state)
	}
}

func TestRunnerRetriesReadOnlyTransientUnknownUsageAfterFullCharge(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "transient-unknown-reader", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	cause := errors.New("connection reset with unknown generation state")
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, ordinal int) (InvocationResult, error) {
		if ordinal == 0 {
			return InvocationResult{}, NewTransientInvocationFailure(
				cause, DispatchDispatched, UsageUnknown, 1500*time.Millisecond,
			)
		}
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")

	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if progressed || !errors.Is(err, ErrTransientInvocation) || !errors.Is(err, cause) {
		t.Fatalf("transient unknown Step() = progressed %v err %v", progressed, err)
	}
	var retry *TransientInvocationError
	if !errors.As(err, &retry) || retry.RetryDelay() != 1500*time.Millisecond || !runner.RetryableStepError(err) {
		t.Fatalf("transient unknown retry = %#v", err)
	}
	if len(state.Attempts) != 1 || state.Attempts[0].Status != jobs.AttemptStatusAbandoned {
		t.Fatalf("attempts after transient unknown failure = %#v", state.Attempts)
	}
	assertRunnerTestReservationBurned(t, state.Attempts[0])
	if state.Usage.ModelCalls != state.Attempts[0].Reservation.ModelCalls || state.Usage.TotalTokens() != state.Attempts[0].Reservation.Tokens {
		t.Fatalf("full conservative charge = usage:%#v reservation:%#v", state.Usage, state.Attempts[0].Reservation)
	}

	state, progressed, err = runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("reader retry Step() = progressed %v err %v", progressed, err)
	}
	if len(state.Attempts) != 2 || state.Attempts[1].AttemptNo != 2 || state.Attempts[1].Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("reader retry attempts = %#v", state.Attempts)
	}
}

func TestRunnerNeverRetriesWriterWithTransientUnknownUsage(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "transient-unknown-writer", jobs.PresetCoding, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	cause := errors.New("writer stream reset after possible tool execution")
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		if invocation.StageID == "implement" {
			return InvocationResult{}, NewTransientInvocationFailure(
				cause, DispatchDispatched, UsageUnknown, time.Second,
			)
		}
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "implement")
	invocationsBefore := len(invoker.Invocations())

	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("writer transient Step() = progressed %v err %v", progressed, err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if len(state.Attempts) == 0 {
		t.Fatal("writer attempt missing")
	}
	writerAttempt := state.Attempts[len(state.Attempts)-1]
	if writerAttempt.Status != jobs.AttemptStatusAmbiguous || !strings.Contains(writerAttempt.Error, cause.Error()) {
		t.Fatalf("writer attempt = %#v", writerAttempt)
	}
	assertRunnerTestReservationBurned(t, writerAttempt)
	if got := len(invoker.Invocations()); got != invocationsBefore+1 {
		t.Fatalf("writer invocations = %d, want %d", got, invocationsBefore+1)
	}
	if _, progressed, err := runner.Step(context.Background(), spec.ID); err != nil || progressed {
		t.Fatalf("terminal writer Step() = progressed %v err %v", progressed, err)
	}
	if got := len(invoker.Invocations()); got != invocationsBefore+1 {
		t.Fatalf("ambiguous writer was redispatched: invocations=%d", got)
	}
}

func TestRunnerTypedFailuresBeforeUsageAreNotConformance(t *testing.T) {
	for _, test := range []struct {
		name       string
		cause      error
		wantStatus jobs.AttemptStatus
	}{
		{name: "cancelled", cause: context.Canceled, wantStatus: jobs.AttemptStatusAbandoned},
		{name: "dns", cause: errors.New("lookup qwen.invalid: no such host"), wantStatus: jobs.AttemptStatusFailed},
		{name: "rate limit", cause: errors.New("qwen HTTP 429"), wantStatus: jobs.AttemptStatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
			spec := runnerTestSpec(t, "typed-preusage-"+strings.ReplaceAll(test.name, " ", "-"), jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
			createRunnerTestJob(t, store, spec)
			invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
				return InvocationResult{Usage: jobs.Usage{ModelCalls: 1}}, NewInvocationFailure(test.cause, DispatchDispatched, UsageUnknown)
			})
			runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
			runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")
			state, progressed, err := runner.Step(context.Background(), spec.ID)
			if err != nil || !progressed {
				t.Fatalf("Step() = progressed %v, err %v", progressed, err)
			}
			if state.Status != jobs.JobStatusRunning || state.TerminalReason != "" || len(state.Attempts) != 1 {
				t.Fatalf("state = status %q reason %q attempts %#v", state.Status, state.TerminalReason, state.Attempts)
			}
			attempt := state.Attempts[0]
			if attempt.Status != test.wantStatus || !attempt.Dispatched || !strings.Contains(attempt.Error, test.cause.Error()) {
				t.Fatalf("attempt = %#v, want status %q dispatched provider error", attempt, test.wantStatus)
			}
			assertRunnerTestReservationBurned(t, attempt)
		})
	}
}

func TestRunnerTransportErrorWithPartialUsageStillBurnsWholeReservation(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "partial-transport-burn", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	transportErr := errors.New("stream reset after partial usage")
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		return InvocationResult{
			Result: "partial", Usage: jobs.Usage{ModelCalls: 1, InputTokens: 2, OutputTokens: 1},
		}, transportErr
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "explore")
	state, progressed, err := runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("transport Step() = progressed %v, err %v", progressed, err)
	}
	if len(state.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(state.Attempts))
	}
	attempt := state.Attempts[0]
	if attempt.Status != jobs.AttemptStatusFailed || attempt.Result != "partial" {
		t.Fatalf("partial transport attempt = %#v", attempt)
	}
	assertRunnerTestReservationBurned(t, attempt)
}

func TestRunnerSplitsEveryFactualUsageComponentAndStopsForBudget(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "component-usage-excess", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	factual := jobs.Usage{ModelCalls: 2, InputTokens: 7, OutputTokens: 9}
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "overspent", Usage: factual}, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{
		MaxModelCallsPerAttempt: 1, MaxTokensPerAttempt: 10, MaxOutputTokensPerCall: 3,
	})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonBudget)
	if len(state.Attempts) != 1 {
		t.Fatalf("attempts = %#v", state.Attempts)
	}
	wantCharged := jobs.Usage{ModelCalls: 1, InputTokens: 7, OutputTokens: 3}
	if state.Attempts[0].Usage != wantCharged {
		t.Fatalf("charged usage = %+v, want %+v", state.Attempts[0].Usage, wantCharged)
	}
	if state.Usage.ModelCalls != factual.ModelCalls || state.Usage.InputTokens != factual.InputTokens || state.Usage.OutputTokens != factual.OutputTokens {
		t.Fatalf("durable factual usage = %+v, want %+v (plus attempt counter)", state.Usage, factual)
	}
}

func TestRunnerPersistsParallelFactualExcessBeforeDrainingSiblings(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "parallel-usage-excess", jobs.PresetGeneral, 2, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(func(_ context.Context, _ Invocation, ordinal int) (InvocationResult, error) {
		usage := jobs.Usage{ModelCalls: 1, InputTokens: 2, OutputTokens: 1}
		if ordinal == 0 {
			usage = jobs.Usage{ModelCalls: 2, InputTokens: 7, OutputTokens: 9}
		}
		return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "factual", Usage: usage}, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{
		MaxModelCallsPerAttempt: 1, MaxTokensPerAttempt: 10, MaxOutputTokensPerCall: 3,
	})
	state, err := runner.Run(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonBudget)
	if len(state.Attempts) != 2 || state.Usage.Attempts != 2 || state.Usage.ModelCalls != 3 || state.Usage.InputTokens != 9 || state.Usage.OutputTokens != 10 {
		t.Fatalf("parallel factual state = attempts %d usage %+v", len(state.Attempts), state.Usage)
	}
	for _, attempt := range state.Attempts {
		if attempt.Status == jobs.AttemptStatusRunning {
			t.Fatalf("parallel sibling remained running: %#v", attempt)
		}
	}
}

func TestRunnerFactualUsageAggregationOverflowFailsUnrecoverablyWithoutCorruption(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "factual-usage-overflow", jobs.PresetGeneral, 2, time.Now().Add(time.Hour))
	spec.Budget.MaxModelCalls = 2
	spec.Budget.MaxTokens = ^uint64(0)
	createRunnerTestJob(t, store, spec)
	perCall := ^uint64(0)/2 + 100
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "huge factual bill", Usage: jobs.Usage{ModelCalls: 1, InputTokens: perCall}}, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{
		MaxModelCallsPerAttempt: 1, MaxTokensPerAttempt: ^uint64(0), MaxOutputTokensPerCall: 1,
	})
	state, err := runner.Run(context.Background(), spec.ID)
	if err == nil || !strings.Contains(err.Error(), "overflow durable job accounting") {
		t.Fatalf("Run() error = %v, want durable accounting overflow", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if len(state.Attempts) != 2 {
		t.Fatalf("attempts = %#v, want both siblings drained", state.Attempts)
	}
}

func TestRunnerConformanceOutranksParallelFactualBudgetExcess(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "conformance-over-budget", jobs.PresetGeneral, 2, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(func(_ context.Context, _ Invocation, ordinal int) (InvocationResult, error) {
		if ordinal == 0 {
			return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "malformed", Usage: jobs.Usage{ModelCalls: 1}}, nil
		}
		return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "overspent", Usage: jobs.Usage{ModelCalls: 2, InputTokens: 7, OutputTokens: 9}}, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{
		MaxModelCallsPerAttempt: 1, MaxTokensPerAttempt: 10, MaxOutputTokensPerCall: 3,
	})
	state, err := runner.Run(context.Background(), spec.ID)
	if !errors.Is(err, ErrInvokerConformance) {
		t.Fatalf("Run() error = %v, want ErrInvokerConformance", err)
	}
	assertRunnerTestTerminal(t, state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if state.Usage.ModelCalls != 3 || state.Usage.InputTokens != 17 || state.Usage.OutputTokens != 9 {
		// The malformed sibling burns its 1-call/10-token reservation; the
		// factual sibling remains exact (2 calls, 7 input, 9 output).
		t.Fatalf("usage = %+v, want conservative malformed plus factual excess", state.Usage)
	}
}

func TestRunnerCompletedProviderErrorBeforeLateOperatorCancelStaysFailed(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	store := newRunnerTestPreLoadBarrierStore(base)
	spec := runnerTestSpec(t, "provider-error-before-operator-cancel", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	providerErr := errors.New("provider rejected the request")
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		store.Arm()
		return InvocationResult{}, providerErr
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	canceller := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(context.Background(), spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	store.Wait(t)
	if _, err := canceller.RequestCancel(context.Background(), spec.ID); err != nil {
		t.Fatalf("RequestCancel(): %v", err)
	}
	store.Release()
	completed := waitRunnerTestRun(t, result)
	if completed.err != nil {
		t.Fatalf("Run(): %v", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusCancelled, jobs.TerminalReasonOperatorCancellation)
	if len(completed.state.Attempts) != 1 || completed.state.Attempts[0].Status != jobs.AttemptStatusFailed {
		t.Fatalf("completed-before-cancel attempt = %#v, want failed factual outcome", completed.state.Attempts)
	}
	if !strings.Contains(completed.state.Attempts[0].Error, providerErr.Error()) {
		t.Fatalf("attempt error = %q, want %q", completed.state.Attempts[0].Error, providerErr)
	}
}

func TestRunnerConformanceFailureOutranksLateOperatorCancellation(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	store := newRunnerTestPreLoadBarrierStore(base)
	spec := runnerTestSpec(t, "conformance-before-operator-cancel", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		store.Arm()
		return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "invalid", Usage: jobs.Usage{ModelCalls: 1}}, nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	canceller := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(context.Background(), spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	store.Wait(t)
	if _, err := canceller.RequestCancel(context.Background(), spec.ID); err != nil {
		t.Fatalf("RequestCancel(): %v", err)
	}
	store.Release()
	completed := waitRunnerTestRun(t, result)
	if !errors.Is(completed.err, ErrInvokerConformance) {
		t.Fatalf("Run() error = %v, want ErrInvokerConformance", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
}

func TestRunnerDefinitiveProviderErrorBeforeLateCallerCancelStaysFailed(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "provider-error-before-cancel", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, base, spec)
	blocking := newRunnerTestBlockingLoadStore(base)
	providerErr := errors.New("provider rejected the request")
	invoker := newRunnerTestInvoker(func(context.Context, Invocation, int) (InvocationResult, error) {
		blocking.Arm()
		return InvocationResult{}, providerErr
	})
	runner := newRunnerTestRunner(t, blocking, invoker, nil, RunnerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(ctx, spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestSignal(t, blocking.blocked, "post-invocation load")
	cancel()
	close(blocking.release)
	completed := waitRunnerTestRun(t, result)
	if !errors.Is(completed.err, context.Canceled) {
		t.Fatalf("Run() error = %v, want caller context cancellation", completed.err)
	}
	if len(completed.state.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(completed.state.Attempts))
	}
	attempt := completed.state.Attempts[0]
	if attempt.Status != jobs.AttemptStatusFailed {
		t.Fatalf("attempt status = %q, want definitive failed (not abandoned)", attempt.Status)
	}
	if !strings.Contains(attempt.Error, providerErr.Error()) {
		t.Fatalf("attempt error = %q, want %q", attempt.Error, providerErr)
	}
	assertRunnerTestReservationBurned(t, attempt)
}

func TestRunnerReturnsWhenInvokerIgnoresContextAndLateResultIsIgnored(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "ignore-context", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	entered := make(chan Invocation, 1)
	release := make(chan struct{})
	lateReturning := make(chan struct{})
	var releaseOnce sync.Once
	releaseInvoker := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseInvoker()
	invoker := newRunnerTestInvoker(func(_ context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		entered <- invocation
		<-release // Deliberately violate prompt context-cooperation.
		close(lateReturning)
		return runnerTestSuccess(invocation), nil
	})
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := runner.Run(ctx, spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestInvocations(t, entered, 1)
	cancel()

	var completed runnerTestRunResult
	select {
	case completed = <-result:
	case <-time.After(2 * time.Second):
		releaseInvoker()
		_ = waitRunnerTestRun(t, result)
		t.Fatal("Run waited for an Invoker that ignored its cancelled context")
	}
	if !errors.Is(completed.err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", completed.err)
	}
	if len(completed.state.Attempts) != 1 || completed.state.Attempts[0].Status != jobs.AttemptStatusAbandoned {
		t.Fatalf("attempts = %#v, want one abandoned reader", completed.state.Attempts)
	}
	assertRunnerTestReservationBurned(t, completed.state.Attempts[0])
	revision := completed.state.Revision

	secondRunner := newRunnerTestRunner(t, store, newRunnerTestInvoker(nil), nil, RunnerOptions{})
	if _, _, err := secondRunner.Step(context.Background(), spec.ID); !errors.Is(err, ErrRunnerBusy) {
		t.Fatalf("retry while detached invocation is live error = %v, want ErrRunnerBusy", err)
	}

	// Contract: cancellation detaches the uncooperative call. Its eventually
	// returned success is observationally ignored and must not race with state.
	releaseInvoker()
	waitRunnerTestSignal(t, lateReturning, "late invoker return")
	afterLate, err := store.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Load after late result: %v", err)
	}
	if afterLate.Revision != revision || len(afterLate.Attempts) != 1 || afterLate.Attempts[0].Status != jobs.AttemptStatusAbandoned {
		t.Fatalf("late result mutated durable state: revision/attempts = %d/%#v, want %d/abandoned", afterLate.Revision, afterLate.Attempts, revision)
	}
}

type runnerTestInvoker struct {
	mu               sync.Mutex
	handler          func(context.Context, Invocation, int) (InvocationResult, error)
	invocations      []Invocation
	active           int
	maxActive        int
	activeByStage    map[string]int
	maxActiveByStage map[string]int
}

func newRunnerTestInvoker(handler func(context.Context, Invocation, int) (InvocationResult, error)) *runnerTestInvoker {
	return &runnerTestInvoker{
		handler: handler, activeByStage: make(map[string]int), maxActiveByStage: make(map[string]int),
	}
}

func (i *runnerTestInvoker) Invoke(ctx context.Context, invocation Invocation) (InvocationResult, error) {
	i.mu.Lock()
	ordinal := len(i.invocations)
	i.invocations = append(i.invocations, invocation)
	i.active++
	i.activeByStage[invocation.StageID]++
	i.maxActive = max(i.maxActive, i.active)
	i.maxActiveByStage[invocation.StageID] = max(i.maxActiveByStage[invocation.StageID], i.activeByStage[invocation.StageID])
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		i.active--
		i.activeByStage[invocation.StageID]--
		i.mu.Unlock()
	}()
	if i.handler != nil {
		return i.handler(ctx, invocation, ordinal)
	}
	return runnerTestSuccess(invocation), nil
}

func (i *runnerTestInvoker) Invocations() []Invocation {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.invocations)
}

func (i *runnerTestInvoker) MaxActive() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.maxActive
}

func (i *runnerTestInvoker) MaxActiveForStage(stageID string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.maxActiveByStage[stageID]
}

func runnerTestSuccess(invocation Invocation) InvocationResult {
	result := InvocationResult{
		Status: jobs.AttemptStatusSucceeded,
		Result: fmt.Sprintf("cycle %d stage %s role %s", invocation.Cycle, invocation.StageID, invocation.RoleID),
		Usage:  jobs.Usage{ModelCalls: 1, InputTokens: 2, OutputTokens: 1},
	}
	if invocation.Kind == InvocationKindSupervisor {
		result.Proposal = &SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "The bounded workflow is complete."}
	}
	return result
}

func runnerTestSpec(t *testing.T, id, preset string, workers int, deadline time.Time) jobs.JobSpec {
	t.Helper()
	workflow, err := jobs.CompilePreset(preset, workers)
	if err != nil {
		t.Fatalf("CompilePreset(%q): %v", preset, err)
	}
	authority := runnerTestAuthority()
	for index := range workflow.Roles {
		workflow.Roles[index].Authority = authority
	}
	return jobs.JobSpec{
		ID: id, Goal: "Complete the bounded test goal.", Preset: preset, Workers: workers, Deadline: deadline.UTC(),
		Budget:   jobs.Budget{MaxCycles: 4, MaxAttempts: 128, MaxModelCalls: 256, MaxTokens: 1_000_000},
		Route:    jobs.ExecutionRoute{ProviderID: runnerTestProvider, ModelID: runnerTestModel},
		Workflow: jobs.WorkflowControlFromWorkflow(workflow), Authority: authority,
		Roles: workflow.Roles, Stages: workflow.Stages,
	}
}

func runnerTestAuthority() jobs.Authority {
	return jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{runnerTestProvider}}
}

func newRunnerTestStore(t *testing.T, root string) *jobstore.FileStore {
	t.Helper()
	store, err := jobstore.NewFileStore(root, jobstore.Options{})
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close FileStore: %v", err)
		}
	})
	return store
}

func createRunnerTestJob(t *testing.T, store jobstore.Store, spec jobs.JobSpec) jobs.JobState {
	t.Helper()
	state, err := store.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	return state
}

func newRunnerTestRunner(t *testing.T, store jobstore.Store, invoker Invoker, now func() time.Time, overrides RunnerOptions) *Runner {
	t.Helper()
	options := overrides
	options.ServerAuthority = runnerTestAuthority()
	options.Now = now
	runner, err := NewRunner(store, invoker, options)
	if err != nil {
		t.Fatalf("NewRunner(): %v", err)
	}
	return runner
}

func runnerTestStep(t *testing.T, runner *Runner, jobID string) jobs.JobState {
	t.Helper()
	state, progressed, err := runner.Step(context.Background(), jobID)
	if err != nil || !progressed {
		t.Fatalf("Step() = progressed %v, err %v", progressed, err)
	}
	return state
}

func runnerTestAdvanceToBatch(t *testing.T, runner *Runner, jobID, stageID string) jobs.JobState {
	t.Helper()
	for step := 0; step < 64; step++ {
		state := runnerTestStep(t, runner, jobID)
		if state.CurrentBatch != nil && state.CurrentBatch.StageID == stageID {
			return state
		}
		if state.Status.IsTerminal() {
			t.Fatalf("job became terminal before stage %q: %+v", stageID, state)
		}
	}
	t.Fatalf("stage %q not reached", stageID)
	return jobs.JobState{}
}

func runnerTestRunningAttempt(id string, batch jobs.WorkBatch, item jobs.WorkItem) jobs.Attempt {
	return jobs.Attempt{
		ID: id, BatchID: batch.ID, WorkItemID: item.ID, RoleID: item.RoleID, AttemptNo: 1,
		Cycle: batch.Cycle, StageID: batch.StageID,
		Reservation: jobs.AttemptReservation{ModelCalls: 4, Tokens: 10_000, MaxOutputTokens: 1_000},
		Status:      jobs.AttemptStatusRunning,
	}
}

func runnerTestAppendEvent(t *testing.T, store jobstore.Store, state jobs.JobState, event jobs.Event) jobs.JobState {
	t.Helper()
	next, err := store.Append(context.Background(), state.Spec.ID, state.Revision, event)
	if err != nil {
		t.Fatalf("Append(%s): %v", event.Type, err)
	}
	return next
}

func runnerTestRole(t *testing.T, spec jobs.JobSpec, roleID string) jobs.RoleSpec {
	t.Helper()
	for _, role := range spec.Roles {
		if role.ID == roleID {
			return role
		}
	}
	t.Fatalf("role %q not found", roleID)
	return jobs.RoleSpec{}
}

func runnerTestStage(t *testing.T, spec jobs.JobSpec, stageID string) jobs.StageSpec {
	t.Helper()
	for _, stage := range spec.Stages {
		if stage.ID == stageID {
			return stage
		}
	}
	t.Fatalf("stage %q not found", stageID)
	return jobs.StageSpec{}
}

func assertRunnerTestReservationBurned(t *testing.T, attempt jobs.Attempt) {
	t.Helper()
	if attempt.Usage.ModelCalls != attempt.Reservation.ModelCalls || attempt.Usage.TotalTokens() != attempt.Reservation.Tokens {
		t.Fatalf("attempt usage = %+v, want conservative full reservation %+v", attempt.Usage, attempt.Reservation)
	}
}

func compactRunnerTestStages(invocations []Invocation) []string {
	var stages []string
	for _, invocation := range invocations {
		if len(stages) == 0 || stages[len(stages)-1] != invocation.StageID {
			stages = append(stages, invocation.StageID)
		}
	}
	return stages
}

func assertRunnerTestQwenInvocations(t *testing.T, invocations []Invocation, spec jobs.JobSpec) {
	t.Helper()
	if len(invocations) == 0 {
		t.Fatal("no invocations recorded")
	}
	for _, invocation := range invocations {
		if invocation.Route.ProviderID != runnerTestProvider || invocation.Route.ModelID != runnerTestModel {
			t.Fatalf("route = %+v, want qwen/qwen3.8-max-preview", invocation.Route)
		}
		if !slices.Equal(invocation.Authority.Providers, []string{runnerTestProvider}) {
			t.Fatalf("effective providers = %v, want [qwen]", invocation.Authority.Providers)
		}
		if !invocation.Deadline.Equal(spec.Deadline) {
			t.Fatalf("deadline = %s, want %s", invocation.Deadline, spec.Deadline)
		}
		if invocation.ObservedAt.IsZero() || invocation.ObservedAt.After(invocation.Deadline) {
			t.Fatalf("observed_at/deadline = %s/%s", invocation.ObservedAt, invocation.Deadline)
		}
		if err := invocation.Limits.Validate(); err != nil {
			t.Fatalf("limits = %+v: %v", invocation.Limits, err)
		}
	}
}

func assertRunnerTestTerminal(t *testing.T, state jobs.JobState, status jobs.JobStatus, reason jobs.TerminalReason) {
	t.Helper()
	if state.Status != status || state.TerminalReason != reason {
		t.Fatalf("terminal state = status %q reason %q, want %q/%q", state.Status, state.TerminalReason, status, reason)
	}
	if state.CurrentBatch != nil {
		t.Fatalf("terminal state retained batch %q", state.CurrentBatch.ID)
	}
	for _, attempt := range state.Attempts {
		if attempt.Status == jobs.AttemptStatusRunning {
			t.Fatalf("terminal state retained running attempt %q", attempt.ID)
		}
	}
}

type runnerTestRunResult struct {
	state jobs.JobState
	err   error
}

func waitRunnerTestInvocations(t *testing.T, entered <-chan Invocation, count int) []Invocation {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	got := make([]Invocation, 0, count)
	for len(got) < count {
		select {
		case invocation := <-entered:
			got = append(got, invocation)
		case <-timer.C:
			t.Fatalf("timed out after %d/%d invocations", len(got), count)
		}
	}
	return got
}

func waitRunnerTestRun(t *testing.T, result <-chan runnerTestRunResult) runnerTestRunResult {
	t.Helper()
	select {
	case completed := <-result:
		return completed
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runner")
		return runnerTestRunResult{}
	}
}

func waitRunnerTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

type runnerTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *runnerTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *runnerTestClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type runnerTestCommittedStore struct {
	jobstore.Store
	mu     sync.Mutex
	target jobs.EventType
	fired  bool
}

func (s *runnerTestCommittedStore) Append(ctx context.Context, jobID string, revision uint64, event jobs.Event) (jobs.JobState, error) {
	next, err := s.Store.Append(ctx, jobID, revision, event)
	if err != nil {
		return next, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired || event.Type != s.target {
		return next, nil
	}
	s.fired = true
	return jobs.JobState{}, &jobstore.CommitError{
		Operation: "append", JobID: jobID, Revision: next.Revision, Err: errors.New("injected post-commit warning"),
	}
}

func (s *runnerTestCommittedStore) Fired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fired
}

type runnerTestBlockingLoadStore struct {
	jobstore.Store
	mu      sync.Mutex
	armed   bool
	blocked chan struct{}
	release chan struct{}
}

// runnerTestPreLoadBarrierStore blocks one armed Load before reading the
// underlying store. It lets a concurrent control append become visible to the
// exact post-invocation snapshot used for outcome normalization.
type runnerTestPreLoadBarrierStore struct {
	jobstore.Store
	mu      sync.Mutex
	armed   bool
	blocked chan struct{}
	release chan struct{}
}

func newRunnerTestPreLoadBarrierStore(store jobstore.Store) *runnerTestPreLoadBarrierStore {
	return &runnerTestPreLoadBarrierStore{
		Store: store, blocked: make(chan struct{}, 1), release: make(chan struct{}),
	}
}

func (s *runnerTestPreLoadBarrierStore) Arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

func (s *runnerTestPreLoadBarrierStore) Load(ctx context.Context, jobID string) (jobs.JobState, error) {
	s.mu.Lock()
	block := s.armed
	if block {
		s.armed = false
	}
	s.mu.Unlock()
	if block {
		s.blocked <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return jobs.JobState{}, ctx.Err()
		}
	}
	return s.Store.Load(ctx, jobID)
}

func (s *runnerTestPreLoadBarrierStore) Wait(t *testing.T) {
	t.Helper()
	waitRunnerTestSignal(t, s.blocked, "pre-load barrier")
}

func (s *runnerTestPreLoadBarrierStore) Release() { close(s.release) }

func newRunnerTestBlockingLoadStore(store jobstore.Store) *runnerTestBlockingLoadStore {
	return &runnerTestBlockingLoadStore{
		Store: store, blocked: make(chan struct{}, 1), release: make(chan struct{}),
	}
}

func (s *runnerTestBlockingLoadStore) Arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

func (s *runnerTestBlockingLoadStore) Load(ctx context.Context, jobID string) (jobs.JobState, error) {
	state, err := s.Store.Load(ctx, jobID)
	if err != nil {
		return state, err
	}
	s.mu.Lock()
	block := s.armed
	if block {
		s.armed = false
	}
	s.mu.Unlock()
	if block {
		s.blocked <- struct{}{}
		<-s.release
	}
	return state, nil
}
