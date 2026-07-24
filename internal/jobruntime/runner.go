package jobruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const (
	// Leave recovery headroom under the public default job budget. A four-way
	// batch of unknown/cancelled calls can consume at most half of the default
	// 128-call/1M-token budget, so read-only attempts remain retryable after a
	// daemon restart instead of exhausting the job in one boundary.
	DefaultMaxModelCallsPerAttempt = uint64(16)
	DefaultMaxTokensPerAttempt     = uint64(120 << 10)
	DefaultMaxOutputTokensPerCall  = uint64(16 << 10)
	// DefaultMaxControlAttempts gives reducer/supervisor output a bounded
	// repair loop. Ordinary worker failures remain evidence for the reducer;
	// malformed control output is retried before the barrier is closed.
	DefaultMaxControlAttempts  = uint64(jobs.MaxControlAttempts)
	maxAppendReconcileAttempts = 4
	cancelStoreTimeout         = 30 * time.Second
)

var (
	ErrRunnerBusy          = errors.New("job runner is already active")
	ErrForeignConflict     = errors.New("foreign job revision conflict")
	ErrInvokerConformance  = errors.New("invoker conformance failure")
	ErrAmbiguousWriter     = errors.New("writer outcome is ambiguous")
	ErrWorkflowBinding     = errors.New("persisted workflow binding mismatch")
	ErrJobNotControllable  = errors.New("job cannot accept the requested control operation")
	ErrTransientInvocation = errors.New("transient provider invocation failure")
)

type TransientInvocationError struct {
	Cause      error
	RetryAfter time.Duration
}

func (e *TransientInvocationError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrTransientInvocation.Error()
	}
	return ErrTransientInvocation.Error() + ": " + e.Cause.Error()
}

func (e *TransientInvocationError) Unwrap() error {
	return errors.Join(ErrTransientInvocation, e.Cause)
}

// RetryDelay is consumed by the service scheduler in addition to its local
// exponential backoff. It is intentionally advisory and cancellable.
func (e *TransientInvocationError) RetryDelay() time.Duration {
	if e == nil || e.RetryAfter < 0 {
		return 0
	}
	return e.RetryAfter
}

type RunnerOptions struct {
	ServerAuthority         jobs.Authority
	Now                     func() time.Time
	MaxModelCallsPerAttempt uint64
	MaxTokensPerAttempt     uint64
	MaxOutputTokensPerCall  uint64
}

func (o RunnerOptions) resolve() (RunnerOptions, error) {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.MaxModelCallsPerAttempt == 0 {
		o.MaxModelCallsPerAttempt = DefaultMaxModelCallsPerAttempt
	}
	if o.MaxTokensPerAttempt == 0 {
		o.MaxTokensPerAttempt = DefaultMaxTokensPerAttempt
	}
	if o.MaxOutputTokensPerCall == 0 {
		o.MaxOutputTokensPerCall = DefaultMaxOutputTokensPerCall
	}
	if err := o.ServerAuthority.Validate(); err != nil {
		return RunnerOptions{}, fmt.Errorf("server authority: %w", err)
	}
	if o.MaxModelCallsPerAttempt == 0 || o.MaxTokensPerAttempt == 0 || o.MaxOutputTokensPerCall == 0 {
		return RunnerOptions{}, errors.New("per-attempt limits must be positive")
	}
	return o, nil
}

type Runner struct {
	store   jobstore.Store
	invoker Invoker
	options RunnerOptions
}

func NewRunner(store jobstore.Store, invoker Invoker, options RunnerOptions) (*Runner, error) {
	if store == nil {
		return nil, errors.New("job store is required")
	}
	if invoker == nil {
		return nil, errors.New("invoker is required")
	}
	resolved, err := options.resolve()
	if err != nil {
		return nil, err
	}
	if len(store.ProtectedRoots()) == 0 {
		return nil, errors.New("job store must expose protected roots")
	}
	if strings.TrimSpace(store.CoordinationKey()) == "" {
		return nil, errors.New("job store must expose a coordination key")
	}
	return &Runner{store: store, invoker: invoker, options: resolved}, nil
}

// Run owns one job in this process until it reaches a dormant/terminal state,
// the caller context ends, or an unrecoverable runtime error occurs. Context
// cancellation is process control, never an implicit operator cancellation.
func (r *Runner) Run(ctx context.Context, jobID string) (jobs.JobState, error) {
	coordinator, release, err := acquireJobCoordinator(r.store, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if !coordinator.run.TryLock() {
		release()
		return jobs.JobState{}, fmt.Errorf("%w: %s", ErrRunnerBusy, jobID)
	}
	if coordinator.isQuarantined() {
		coordinator.run.Unlock()
		release()
		return jobs.JobState{}, fmt.Errorf("%w: %s has an invocation still draining after context cancellation", ErrRunnerBusy, jobID)
	}
	defer func() {
		coordinator.run.Unlock()
		release()
	}()

	for {
		state, progressed, err := r.stepLocked(ctx, jobID, coordinator)
		if err != nil {
			return state, err
		}
		if !progressed || state.Status.IsTerminal() || state.Status == jobs.JobStatusWaiting || state.Status == jobs.JobStatusPaused {
			return state, nil
		}
	}
}

// Step advances one durable boundary. It is useful for deterministic recovery
// tests and for daemon schedulers that interleave multiple jobs.
func (r *Runner) Step(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
	coordinator, release, err := acquireJobCoordinator(r.store, jobID)
	if err != nil {
		return jobs.JobState{}, false, err
	}
	if !coordinator.run.TryLock() {
		release()
		return jobs.JobState{}, false, fmt.Errorf("%w: %s", ErrRunnerBusy, jobID)
	}
	if coordinator.isQuarantined() {
		coordinator.run.Unlock()
		release()
		return jobs.JobState{}, false, fmt.Errorf("%w: %s has an invocation still draining after context cancellation", ErrRunnerBusy, jobID)
	}
	defer func() {
		coordinator.run.Unlock()
		release()
	}()
	return r.stepLocked(ctx, jobID, coordinator)
}

// ExpireDue terminalizes a dormant job only when its hard deadline has
// elapsed. Unlike Step it never starts a non-expired queued job, so daemon
// recovery can safely clean up QUEUED and PAUSED state without changing their
// explicit-admission semantics.
func (r *Runner) ExpireDue(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
	coordinator, release, err := acquireJobCoordinator(r.store, jobID)
	if err != nil {
		return jobs.JobState{}, false, err
	}
	if !coordinator.run.TryLock() {
		release()
		return jobs.JobState{}, false, fmt.Errorf("%w: %s", ErrRunnerBusy, jobID)
	}
	if coordinator.isQuarantined() {
		coordinator.run.Unlock()
		release()
		return jobs.JobState{}, false, fmt.Errorf("%w: %s has an invocation still draining after context cancellation", ErrRunnerBusy, jobID)
	}
	defer func() {
		coordinator.run.Unlock()
		release()
	}()
	state, err := r.store.Load(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, false, err
	}
	if state.Status.IsTerminal() || r.now().Before(state.Spec.Deadline) {
		return state, false, nil
	}
	return r.stepLocked(ctx, jobID, coordinator)
}

// RetryableStepError classifies the non-terminal scheduler errors which a
// service-level loop may retry after reloading canonical durable state. Domain
// conformance, immutable binding, ambiguous writer, and durable-store
// integrity/ownership failures require operator intervention and fail closed.
// Other non-terminal errors include provider transports, temporary I/O,
// revision coordination, and a still-draining local runner.
func (r *Runner) RetryableStepError(err error) bool {
	if err == nil {
		return false
	}
	fatal := [...]error{
		ErrInvokerConformance,
		ErrAmbiguousWriter,
		ErrWorkflowBinding,
		jobs.ErrInvalidEvent,
		jobs.ErrInvalidTransition,
		jobs.ErrTerminalState,
		jobs.ErrUsageOverflow,
		jobstore.ErrNotFound,
		jobstore.ErrAlreadyExists,
		jobstore.ErrCorrupt,
		jobstore.ErrTampered,
		jobstore.ErrClosed,
		jobstore.ErrOwnership,
		jobstore.ErrTooLarge,
		jobstore.ErrInvalidID,
	}
	for _, sentinel := range fatal {
		if errors.Is(err, sentinel) {
			return false
		}
	}
	return true
}

func (r *Runner) stepLocked(ctx context.Context, jobID string, coordinator *jobCoordinator) (jobs.JobState, bool, error) {
	if err := ctx.Err(); err != nil {
		state, _ := r.store.Load(context.WithoutCancel(ctx), jobID)
		return state, false, err
	}
	state, err := r.store.Load(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, false, err
	}
	if state.Status.IsTerminal() {
		return state, false, nil
	}

	bindingErr := ValidateWorkflowBinding(state.Spec)
	// A pre-existing running attempt is an orphan from an interrupted process.
	// When the immutable binding is invalid, persist the higher-priority failure
	// before draining every orphan. Otherwise cancellation/deadline on the last
	// finish could terminalize the job and permanently mask the binding fault.
	if state.Status == jobs.JobStatusRunning && state.CurrentBatch != nil {
		if orphan, ok := firstRunningAttempt(state); ok {
			if bindingErr != nil {
				return r.drainOrphansForBindingFailure(ctx, state, bindingErr)
			}
			next, err := r.recoverOrphan(ctx, state, orphan)
			return next, err == nil, err
		}
	}
	if bindingErr != nil {
		failed, appendErr := r.failJob(ctx, state, TerminalFailure{Reason: jobs.TerminalReasonUnrecoverable, Detail: bindingErr.Error()})
		if appendErr != nil {
			return state, false, errors.Join(fmt.Errorf("%w: %v", ErrWorkflowBinding, bindingErr), appendErr)
		}
		return failed, true, fmt.Errorf("%w: %v", ErrWorkflowBinding, bindingErr)
	}
	if !r.now().Before(state.Spec.Deadline) {
		next, err := r.append(ctx, state, jobs.Event{
			ID:   eventID("deadline", state.Spec.ID, strconv.FormatUint(state.Revision, 10)),
			Type: jobs.EventDeadlineExceeded, At: r.now(),
		}, false)
		return next, err == nil, err
	}
	if state.Status == jobs.JobStatusWaiting || state.Status == jobs.JobStatusPaused {
		return state, false, nil
	}
	if state.Status == jobs.JobStatusQueued {
		next, err := r.append(ctx, state, jobs.Event{
			ID: eventID("job-start", state.Spec.ID), Type: jobs.EventJobStarted, At: r.now(),
		}, false)
		return next, err == nil, err
	}
	if state.Status != jobs.JobStatusRunning {
		return state, false, fmt.Errorf("unsupported job status %q", state.Status)
	}
	if state.CurrentBatch == nil {
		if state.NextStageIndex == len(state.Spec.Workflow.StageOrder) {
			decision, err := supervisorDecision(state)
			if err != nil {
				next, failErr := r.failJob(ctx, state, TerminalFailure{Reason: jobs.TerminalReasonUnrecoverable, Detail: err.Error()})
				return next, failErr == nil, failErr
			}
			next, err := r.append(ctx, state, jobs.Event{
				ID:   eventID("decision", state.Spec.ID, strconv.FormatUint(state.Cycle, 10)),
				Type: jobs.EventDecisionMade, At: r.now(), Decision: decision,
			}, false)
			return next, err == nil, err
		}
		batch, err := MaterializeStageBatch(state, nil)
		if err != nil {
			next, failErr := r.failJob(ctx, state, TerminalFailure{Reason: jobs.TerminalReasonUnrecoverable, Detail: err.Error()})
			return next, failErr == nil, errors.Join(err, failErr)
		}
		next, err := r.append(ctx, state, jobs.Event{
			ID: eventID("batch-start", batch.ID), Type: jobs.EventBatchStarted, At: r.now(), Batch: &batch,
		}, false)
		return next, err == nil, err
	}

	missing := unresolvedItems(state)
	if len(missing) == 0 {
		next, err := r.append(ctx, state, jobs.Event{
			ID: eventID("batch-complete", state.CurrentBatch.ID), Type: jobs.EventBatchCompleted,
			At: r.now(), BatchID: state.CurrentBatch.ID,
		}, false)
		return next, err == nil, err
	}
	next, err := r.executeMissing(ctx, state, missing, coordinator)
	return next, err == nil, err
}

type TerminalFailure struct {
	Reason jobs.TerminalReason
	Detail string
}

func (r *Runner) failJob(ctx context.Context, state jobs.JobState, failure TerminalFailure) (jobs.JobState, error) {
	return r.appendJobFailure(ctx, state, failure, false)
}

// failJobWhileDraining records failure priority while attempts are still
// running. It may rebase over the one permitted concurrent control event,
// cancellation_requested, so the eventual last finish cannot mask a more
// important unrecoverable fault.
func (r *Runner) failJobWhileDraining(ctx context.Context, state jobs.JobState, failure TerminalFailure) (jobs.JobState, error) {
	return r.appendJobFailure(ctx, state, failure, true)
}

func (r *Runner) appendJobFailure(ctx context.Context, state jobs.JobState, failure TerminalFailure, allowCancellationRebase bool) (jobs.JobState, error) {
	reason := failure.Reason
	if reason == "" {
		reason = jobs.TerminalReasonUnrecoverable
	}
	return r.append(ctx, state, jobs.Event{
		ID:   eventID("job-failed", state.Spec.ID, string(reason), strconv.FormatUint(state.Revision, 10)),
		Type: jobs.EventJobFailed, At: r.now(), Reason: reason,
	}, allowCancellationRebase)
}

func (r *Runner) drainOrphansForBindingFailure(ctx context.Context, state jobs.JobState, bindingErr error) (jobs.JobState, bool, error) {
	failure := fmt.Errorf("%w: %v", ErrWorkflowBinding, bindingErr)
	current, err := r.failJobWhileDraining(context.WithoutCancel(ctx), state, TerminalFailure{
		Reason: jobs.TerminalReasonUnrecoverable,
		Detail: bindingErr.Error(),
	})
	if err != nil {
		return state, false, errors.Join(failure, err)
	}
	var recoveryErr error
	for !current.Status.IsTerminal() {
		orphan, ok := firstRunningAttempt(current)
		if !ok {
			break
		}
		next, recoverErr := r.recoverOrphan(context.WithoutCancel(ctx), current, orphan)
		current = next
		recoveryErr = errors.Join(recoveryErr, recoverErr)
		if recoverErr != nil && !errors.Is(recoverErr, ErrAmbiguousWriter) {
			break
		}
	}
	return current, false, errors.Join(failure, recoveryErr)
}

func (r *Runner) recoverOrphan(ctx context.Context, state jobs.JobState, orphan jobs.Attempt) (jobs.JobState, error) {
	// Recovery must rely only on the already persisted role identity/Writer
	// flag. Calling RoleByID here would re-run immutable preset binding checks
	// and could wedge the running attempt behind the very mismatch we need to
	// drain before failing the job.
	role, ok := roleByIDUnchecked(state.Spec, orphan.RoleID)
	if !ok {
		return state, fmt.Errorf("orphan attempt references undeclared role %q", orphan.RoleID)
	}
	finished := orphan
	finished.Result = ""
	finished.Fingerprint = ""
	finished.Artifacts = nil
	finished.Usage = conservativeReservationUsage(orphan.Reservation)
	finished.Decision = nil
	// A persisted running attempt may have crossed dispatch before the process
	// died. Recovery must classify it conservatively.
	finished.Dispatched = true
	if role.Writer {
		finished.Status = jobs.AttemptStatusAmbiguous
		finished.Error = "writer outcome is unknown after process interruption"
	} else {
		finished.Status = jobs.AttemptStatusAbandoned
		finished.Error = "read-only attempt was interrupted and may be retried"
	}
	next, appendErr := r.append(ctx, state, jobs.Event{
		ID: eventID("attempt-finish", finished.ID), Type: jobs.EventAttemptFinished, At: r.now(), Attempt: &finished,
	}, true)
	if appendErr != nil {
		return state, appendErr
	}
	if role.Writer {
		return next, fmt.Errorf("%w: attempt %s", ErrAmbiguousWriter, orphan.ID)
	}
	return next, nil
}

func (r *Runner) executeMissing(ctx context.Context, state jobs.JobState, items []jobs.WorkItem, coordinator *jobCoordinator) (jobs.JobState, error) {
	limits, err := r.allocateLimits(state, len(items))
	if err != nil {
		return r.failJob(ctx, state, TerminalFailure{Reason: jobs.TerminalReasonBudget, Detail: err.Error()})
	}
	callCtx, cancel := context.WithDeadline(ctx, state.Spec.Deadline)
	coordinator.gate.Lock()
	generation := coordinator.installDispatch(cancel)
	coordinator.gate.Unlock()
	dispatchCleared := false
	defer func() {
		if !dispatchCleared {
			coordinator.clearDispatch(generation)
			cancel()
		}
	}()

	started := make([]jobs.Attempt, 0, len(items))
	planned := make([]jobs.Attempt, 0, len(items))
	current := state
	for index, item := range items {
		attemptNo := nextAttemptNo(current, current.CurrentBatch.ID, item.ID)
		attempt := jobs.Attempt{
			ID:         portableMaterializedID("attempt", state.Spec.ID, state.CurrentBatch.ID, item.ID, strconv.FormatUint(attemptNo, 10)),
			BatchID:    state.CurrentBatch.ID,
			WorkItemID: item.ID,
			RoleID:     item.RoleID,
			AttemptNo:  attemptNo,
			Cycle:      state.CurrentBatch.Cycle,
			StageID:    state.CurrentBatch.StageID,
			Reservation: jobs.AttemptReservation{
				ModelCalls: limits[index].ModelCalls, Tokens: limits[index].Tokens, MaxOutputTokens: limits[index].MaxOutputTokens,
			},
			Status: jobs.AttemptStatusRunning,
		}
		planned = append(planned, attempt)
		if callCtx.Err() != nil {
			return r.abortKnownUninvoked(ctx, current, planned, callCtx.Err())
		}
		current, err = r.append(ctx, current, jobs.Event{
			ID: eventID("attempt-start", attempt.ID), Type: jobs.EventAttemptStarted, At: r.now(), Attempt: &attempt,
		}, false)
		if err != nil {
			return r.abortKnownUninvoked(ctx, current, planned, err)
		}
		started = append(started, attempt)
	}

	invocations := make([]Invocation, len(started))
	for index, attempt := range started {
		invocation, err := r.buildInvocation(current, attempt)
		if err != nil {
			return r.finishStartsAsRuntimeFailure(ctx, current, started, fmt.Errorf("build invocation: %w", err))
		}
		invocations[index] = invocation
	}

	// Gate order is always coordinator gate -> Store. RequestCancel follows the
	// same order, so its durable append and this dispatch admission are totally
	// ordered across every Runner using the store namespace.
	coordinator.gate.Lock()
	dispatchState, gateErr := r.store.Load(context.WithoutCancel(ctx), state.Spec.ID)
	if gateErr != nil {
		coordinator.gate.Unlock()
		return r.abortKnownUninvoked(ctx, current, planned, gateErr)
	}
	if callCtx.Err() != nil {
		coordinator.gate.Unlock()
		return r.abortKnownUninvoked(ctx, dispatchState, planned, callCtx.Err())
	}
	if dispatchState.CancelRequested {
		coordinator.gate.Unlock()
		return r.finishUninvoked(context.WithoutCancel(ctx), dispatchState, runningPlannedAttempts(dispatchState, planned), jobs.AttemptStatusCancelled, "operator cancelled before dispatch")
	}
	if dispatchState.Revision != current.Revision {
		coordinator.gate.Unlock()
		return r.abortKnownUninvoked(ctx, dispatchState, planned, fmt.Errorf("%w: state changed before dispatch", ErrForeignConflict))
	}
	if !r.now().Before(state.Spec.Deadline) {
		current, err = r.append(context.WithoutCancel(ctx), current, jobs.Event{
			ID: eventID("deadline", state.Spec.ID, strconv.FormatUint(current.Revision, 10)), Type: jobs.EventDeadlineExceeded, At: r.now(),
		}, false)
		coordinator.gate.Unlock()
		if err != nil {
			return r.abortKnownUninvoked(ctx, current, planned, err)
		}
		return r.finishUninvoked(context.WithoutCancel(ctx), current, runningPlannedAttempts(current, planned), jobs.AttemptStatusCancelled, "deadline reached before dispatch")
	}
	results := make([]invokeOutcome, len(invocations))
	received := make([]bool, len(invocations))
	completions := make(chan indexedInvokeOutcome, len(invocations))
	var invocationsDone sync.WaitGroup
	var admitted sync.WaitGroup
	invocationsDone.Add(len(invocations))
	admitted.Add(len(invocations))
	for index := range invocations {
		go func(index int) {
			defer invocationsDone.Done()
			// This is the dispatch linearization point. RequestCancel cannot
			// durably commit while the gate is held, and the gate is not
			// released until every invocation has crossed this point.
			admitted.Done()
			result, invokeErr := r.invoker.Invoke(callCtx, invocations[index])
			completions <- indexedInvokeOutcome{
				index: index,
				outcome: invokeOutcome{
					result: result, err: invokeErr, contextErr: callCtx.Err(),
				},
			}
		}(index)
	}
	admitted.Wait()
	coordinator.gate.Unlock()

	remaining := len(invocations)
	detached := false
	for remaining > 0 {
		select {
		case completed := <-completions:
			if !received[completed.index] {
				results[completed.index] = completed.outcome
				received[completed.index] = true
				remaining--
			}
		case <-callCtx.Done():
			// Prefer any factual completions already buffered at the cutoff.
			// Calls which still have no result are detached and accounted for
			// conservatively; their eventual late result is observationally
			// ignored.
		drainReady:
			for remaining > 0 {
				select {
				case completed := <-completions:
					if !received[completed.index] {
						results[completed.index] = completed.outcome
						received[completed.index] = true
						remaining--
					}
				default:
					break drainReady
				}
			}
			if remaining > 0 {
				cause := callCtx.Err()
				for index := range results {
					if !received[index] {
						results[index] = invokeOutcome{err: cause, contextErr: cause}
					}
				}
				detached = true
				remaining = 0
			}
		}
	}
	if detached {
		retained, releaseQuarantine, retainErr := acquireJobCoordinator(r.store, state.Spec.ID)
		if retainErr != nil || retained != coordinator {
			if releaseQuarantine != nil {
				releaseQuarantine()
			}
			// This path is structurally unreachable while Run/Step still holds
			// its coordinator reference. Waiting is safer than allowing a live
			// external call to escape the per-job concurrency quarantine.
			invocationsDone.Wait()
		} else {
			coordinator.setQuarantined(true)
			go func() {
				invocationsDone.Wait()
				coordinator.setQuarantined(false)
				releaseQuarantine()
			}()
		}
	} else {
		invocationsDone.Wait()
	}
	coordinator.clearDispatch(generation)
	cancel()
	dispatchCleared = true

	latest, loadErr := r.store.Load(context.WithoutCancel(ctx), state.Spec.ID)
	if loadErr != nil {
		return current, loadErr
	}
	operatorCancelled := latest.CancelRequested
	current = latest
	normalized := make([]normalizedInvokeOutcome, len(results))
	var conformanceErr error
	var transientErr error
	var retryAfter time.Duration
	budgetExceeded := false
	completionObservedAt := r.now()
	for index, outcome := range results {
		role, roleErr := RoleByID(current.Spec, started[index].RoleID)
		if roleErr != nil {
			return current, roleErr
		}
		normalized[index] = normalizeOutcome(current, invocations[index], started[index], outcome, role.Writer, operatorCancelled, outcome.contextErr, completionObservedAt)
		if normalized[index].err != nil {
			dispatched := outcomeWasDispatched(outcome)
			normalized[index].attempt = failedConformanceAttempt(started[index], normalized[index].err, role.Writer, dispatched)
			normalized[index].excess = jobs.Usage{}
			conformanceErr = errors.Join(conformanceErr, normalized[index].err)
		}
		budgetExceeded = budgetExceeded || normalized[index].budgetExceeded
		if normalized[index].transientErr != nil {
			transientErr = errors.Join(transientErr, normalized[index].transientErr)
			if normalized[index].retryAfter > retryAfter {
				retryAfter = normalized[index].retryAfter
			}
		}
	}
	if usageErr := validateNormalizedUsageTotal(current.Usage, normalized); usageErr != nil {
		// Reservations were admitted against the remaining durable budget, so
		// dropping only unrepresentable excess leaves a persistable conservative
		// charge while the job is failed closed.
		for index := range normalized {
			normalized[index].excess = jobs.Usage{}
		}
		conformanceErr = errors.Join(conformanceErr, usageErr)
	}
	if conformanceErr != nil {
		current, err = r.failJobWhileDraining(context.WithoutCancel(ctx), current, TerminalFailure{
			Reason: jobs.TerminalReasonUnrecoverable,
			Detail: conformanceErr.Error(),
		})
		if err != nil {
			return current, errors.Join(conformanceErr, err)
		}
	} else if budgetExceeded {
		current, err = r.failJobWhileDraining(context.WithoutCancel(ctx), current, TerminalFailure{
			Reason: jobs.TerminalReasonBudget,
			Detail: "factual provider usage exceeded its durable attempt reservation",
		})
		if err != nil {
			return current, err
		}
	}
	// Excess usage is durable before its attempt finish and while all attempts
	// are still running. A crash can therefore never persist a terminal attempt
	// while silently losing known provider billing.
	for index := range normalized {
		if normalized[index].excess == (jobs.Usage{}) {
			continue
		}
		current, err = r.append(context.WithoutCancel(ctx), current, jobs.Event{
			ID: eventID("attempt-usage-excess", normalized[index].attempt.ID), Type: jobs.EventUsageRecorded, At: r.now(), Usage: normalized[index].excess,
		}, true)
		if err != nil {
			return current, err
		}
	}
	for index := range normalized {
		finished := normalized[index].attempt
		current, err = r.append(context.WithoutCancel(ctx), current, jobs.Event{
			ID: eventID("attempt-finish", finished.ID), Type: jobs.EventAttemptFinished, At: r.now(), Attempt: &finished,
		}, true)
		if err != nil {
			return current, err
		}
		if current.Status.IsTerminal() {
			break
		}
	}
	if conformanceErr != nil {
		if !current.Status.IsTerminal() {
			current, err = r.failJob(context.WithoutCancel(ctx), current, TerminalFailure{Reason: jobs.TerminalReasonUnrecoverable, Detail: conformanceErr.Error()})
			if err != nil {
				return current, errors.Join(conformanceErr, err)
			}
		}
		return current, conformanceErr
	}
	if ctx.Err() != nil && !operatorCancelled {
		return current, ctx.Err()
	}
	if transientErr != nil && !current.Status.IsTerminal() && !operatorCancelled {
		return current, &TransientInvocationError{Cause: transientErr, RetryAfter: retryAfter}
	}
	return current, nil
}

func (r *Runner) abortKnownUninvoked(ctx context.Context, state jobs.JobState, planned []jobs.Attempt, cause error) (jobs.JobState, error) {
	durableCtx := context.WithoutCancel(ctx)
	latest, loadErr := r.store.Load(durableCtx, state.Spec.ID)
	if loadErr != nil {
		return state, errors.Join(cause, loadErr)
	}
	running := runningPlannedAttempts(latest, planned)
	if len(running) == 0 {
		if latest.CancelRequested || latest.Status.IsTerminal() {
			return latest, nil
		}
		return latest, cause
	}
	status := jobs.AttemptStatusAbandoned
	detail := "dispatch preparation failed before invocation: " + compactRuntimeError(cause)
	if latest.CancelRequested {
		status = jobs.AttemptStatusCancelled
		detail = "operator cancelled before dispatch"
	} else if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		status = jobs.AttemptStatusAbandoned
		detail = "runner context ended before dispatch"
	}
	finished, finishErr := r.finishUninvoked(durableCtx, latest, running, status, detail)
	if finishErr != nil {
		return latest, errors.Join(cause, finishErr)
	}
	if latest.CancelRequested {
		return finished, nil
	}
	return finished, cause
}

func runningPlannedAttempts(state jobs.JobState, planned []jobs.Attempt) []jobs.Attempt {
	plannedIDs := make(map[string]struct{}, len(planned))
	for _, attempt := range planned {
		plannedIDs[attempt.ID] = struct{}{}
	}
	running := make([]jobs.Attempt, 0, len(planned))
	for _, attempt := range state.Attempts {
		if attempt.Status != jobs.AttemptStatusRunning {
			continue
		}
		if _, ok := plannedIDs[attempt.ID]; ok {
			running = append(running, attempt)
		}
	}
	return running
}

func (r *Runner) finishUninvoked(ctx context.Context, state jobs.JobState, started []jobs.Attempt, status jobs.AttemptStatus, detail string) (jobs.JobState, error) {
	current := state
	for _, attempt := range started {
		finished := attempt
		finished.Status = status
		finished.Error = detail
		finished.Fingerprint = normalizedTextFingerprint(detail)
		var err error
		current, err = r.append(ctx, current, jobs.Event{
			ID: eventID("attempt-finish", finished.ID), Type: jobs.EventAttemptFinished, At: r.now(), Attempt: &finished,
		}, true)
		if err != nil {
			return current, err
		}
		if current.Status.IsTerminal() {
			return current, nil
		}
	}
	return current, nil
}

type invokeOutcome struct {
	result     InvocationResult
	err        error
	contextErr error
}

type indexedInvokeOutcome struct {
	index   int
	outcome invokeOutcome
}

type normalizedInvokeOutcome struct {
	attempt        jobs.Attempt
	excess         jobs.Usage
	budgetExceeded bool
	err            error
	transientErr   error
	retryAfter     time.Duration
}

func (r *Runner) buildInvocation(state jobs.JobState, attempt jobs.Attempt) (Invocation, error) {
	if state.CurrentBatch == nil {
		return Invocation{}, errors.New("active batch is required")
	}
	var item *jobs.WorkItem
	for index := range state.CurrentBatch.Items {
		if state.CurrentBatch.Items[index].ID == attempt.WorkItemID {
			copy := state.CurrentBatch.Items[index]
			item = &copy
			break
		}
	}
	if item == nil {
		return Invocation{}, fmt.Errorf("work item %q is missing", attempt.WorkItemID)
	}
	role, err := RoleByID(state.Spec, attempt.RoleID)
	if err != nil {
		return Invocation{}, err
	}
	authority, err := ResolveEffectiveAuthority(r.store, r.options.ServerAuthority, state.Spec, role, *item)
	if err != nil {
		return Invocation{}, err
	}
	kind := InvocationKindWorker
	if role.ID == state.Spec.Workflow.ReducerRoleID {
		kind = InvocationKindReducer
	} else if role.ID == state.Spec.Workflow.SupervisorRoleID {
		kind = InvocationKindSupervisor
	}
	observedAt := r.now()
	invocation := Invocation{
		JobID:               state.Spec.ID,
		AttemptID:           attempt.ID,
		AttemptNo:           attempt.AttemptNo,
		BatchID:             attempt.BatchID,
		WorkItemID:          attempt.WorkItemID,
		Cycle:               attempt.Cycle,
		MinimumCycles:       state.Spec.EffectiveMinCycles(),
		MaximumCycles:       state.Spec.Budget.MaxCycles,
		StageID:             attempt.StageID,
		RoleID:              attempt.RoleID,
		Kind:                kind,
		Writer:              role.Writer,
		Goal:                state.Spec.Goal,
		Objective:           item.Objective,
		RolePurpose:         role.Purpose,
		Route:               state.Spec.Route,
		Authority:           authority,
		ObservedAt:          observedAt,
		NotBeforeComplete:   state.Spec.NotBeforeComplete,
		CycleCadenceSeconds: state.Spec.CycleCadenceSeconds,
		Deadline:            state.Spec.Deadline,
		JobRemainingBudget:  remainingJobBudget(state),
		Limits: RemainingLimits{
			ModelCalls: attempt.Reservation.ModelCalls, Tokens: attempt.Reservation.Tokens, MaxOutputTokens: attempt.Reservation.MaxOutputTokens,
		},
		PriorAttempts: CanonicalPriorAttempts(state),
		Artifacts:     canonicalInvocationArtifacts(state.Artifacts),
	}
	if kind == InvocationKindSupervisor {
		firstStage, err := StageAt(state.Spec, 0)
		if err != nil {
			return Invocation{}, err
		}
		invocation.AllowedNextRoleIDs = slices.Clone(firstStage.RoleIDs)
	}
	if err := invocation.Validate(); err != nil {
		return Invocation{}, err
	}
	return invocation, nil
}

func remainingJobBudget(state jobs.JobState) JobRemainingBudget {
	reservedCalls, reservedTokens := runningReservations(state)
	return JobRemainingBudget{
		Cycles:     remainingAfterUsage(state.Spec.Budget.MaxCycles, state.Usage.Cycles, 0),
		Attempts:   remainingAfterUsage(state.Spec.Budget.MaxAttempts, state.Usage.Attempts, 0),
		ModelCalls: remainingAfterUsage(state.Spec.Budget.MaxModelCalls, state.Usage.ModelCalls, reservedCalls),
		Tokens:     remainingAfterUsage(state.Spec.Budget.MaxTokens, state.Usage.TotalTokens(), reservedTokens),
	}
}

func remainingAfterUsage(limit, used, reserved uint64) uint64 {
	if used >= limit {
		return 0
	}
	remaining := limit - used
	if reserved >= remaining {
		return 0
	}
	return remaining - reserved
}

func normalizeOutcome(state jobs.JobState, invocation Invocation, started jobs.Attempt, outcome invokeOutcome, writer, operatorCancelled bool, runErr error, completionObservedAt time.Time) normalizedInvokeOutcome {
	finished := started
	dispatch, usageProvenance, typed := InvocationFailureFromError(outcome.err)
	retryAfter, transient := TransientInvocationFailureFromError(outcome.err)
	if outcome.err == nil {
		dispatch = DispatchDispatched
		usageProvenance = UsageFactual
		typed = true
	} else if !typed {
		// An untyped Invoker error cannot prove that the provider boundary was
		// untouched. Preserve the historical fail-closed behavior.
		dispatch = DispatchDispatched
		usageProvenance = UsageUnknown
	}
	finished.Dispatched = dispatch == DispatchDispatched
	if dispatch == DispatchNotDispatched {
		if !emptyInvocationResult(outcome.result) {
			return normalizedInvokeOutcome{err: fmt.Errorf("%w: undispatched failure returned provider output", ErrInvokerConformance)}
		}
		if FatalPreflightFailureFromError(outcome.err) {
			return normalizedInvokeOutcome{err: fmt.Errorf("%w: fatal preflight rejection: %v", ErrInvokerConformance, outcome.err)}
		}
		finished.Error = compactRuntimeError(outcome.err)
		finished.Fingerprint = normalizedTextFingerprint(finished.Error)
		if operatorCancelled && runErr != nil {
			finished.Status = jobs.AttemptStatusCancelled
		} else {
			// Proven non-dispatch is always safe to retry, including for an
			// isolated writer. Attempt budgets bound persistent preflight faults.
			finished.Status = jobs.AttemptStatusAbandoned
		}
		return normalizedInvokeOutcome{attempt: finished, transientErr: outcome.err}
	}
	if usageProvenance == UsageNoGeneration {
		if outcome.result.Status != "" || outcome.result.Result != "" || outcome.result.Fingerprint != "" ||
			len(outcome.result.Artifacts) != 0 || outcome.result.Proposal != nil || outcome.result.Error != "" ||
			outcome.result.Usage.ModelCalls == 0 || outcome.result.Usage.ModelCalls > invocation.Limits.ModelCalls ||
			outcome.result.Usage.InputTokens != 0 || outcome.result.Usage.OutputTokens != 0 {
			return normalizedInvokeOutcome{err: fmt.Errorf("%w: invalid no-generation provider failure envelope", ErrInvokerConformance)}
		}
		finished.Dispatched = true
		finished.Usage = outcome.result.Usage
		finished.Error = compactRuntimeError(outcome.err)
		finished.Fingerprint = normalizedTextFingerprint(finished.Error)
		switch {
		case operatorCancelled:
			finished.Status = jobs.AttemptStatusCancelled
		case transient:
			// No model output means no tool call or writer mutation could have
			// occurred. Retrying is safe even for the isolated writer role.
			finished.Status = jobs.AttemptStatusAbandoned
		default:
			finished.Status = jobs.AttemptStatusFailed
		}
		normalized := normalizedInvokeOutcome{attempt: finished}
		if transient {
			normalized.transientErr = outcome.err
			normalized.retryAfter = retryAfter
		}
		return normalized
	}
	if outcome.err == nil {
		if err := outcome.result.validateFor(invocation, false); err != nil {
			return normalizedInvokeOutcome{err: fmt.Errorf("%w: %v", ErrInvokerConformance, err)}
		}
		finished.Status = outcome.result.Status
		finished.Result = outcome.result.Result
		finished.Error = outcome.result.Error
		finished.Artifacts = slices.Clone(outcome.result.Artifacts)
		finished.Usage = outcome.result.Usage
		finished.Fingerprint = normalizedResultFingerprint(outcome.result)
		if writer && outcome.result.Status == jobs.AttemptStatusCancelled {
			finished.Status = jobs.AttemptStatusAmbiguous
			finished.Result = ""
			finished.Error = "writer cancellation left its external mutation outcome unknown"
			return finalizeFactualUsage(finished, outcome.result.Usage)
		}
		if invocation.Kind == InvocationKindSupervisor && outcome.result.Status == jobs.AttemptStatusSucceeded {
			decision, err := MaterializeDecisionAt(state, *outcome.result.Proposal, completionObservedAt)
			if err != nil {
				if errors.Is(err, ErrMinimumCyclesNotReached) || errors.Is(err, ErrMinimumRuntimeNotReached) {
					finished.Status = jobs.AttemptStatusFailed
					finished.Result = ""
					finished.Error = compactRuntimeError(err)
					finished.Fingerprint = normalizedTextFingerprint(finished.Error)
					return finalizeFactualUsage(finished, outcome.result.Usage)
				}
				return normalizedInvokeOutcome{err: fmt.Errorf("%w: materialize supervisor proposal: %v", ErrInvokerConformance, err)}
			}
			finished.Decision = &decision
		}
		return finalizeFactualUsage(finished, outcome.result.Usage)
	}

	if usageProvenance == UsageFactual {
		probe := outcome.result
		probe.Status = jobs.AttemptStatusFailed
		probe.Error = "provider invocation failed after reporting factual usage"
		probe.Proposal = nil
		if err := probe.validateFor(invocation, false); err != nil {
			return normalizedInvokeOutcome{err: fmt.Errorf("%w: invalid factual error result: %v", ErrInvokerConformance, err)}
		}
		finished.Result = outcome.result.Result
		finished.Artifacts = slices.Clone(outcome.result.Artifacts)
		finished.Fingerprint = normalizedResultFingerprint(probe)
	} else if outcome.result.Usage != (jobs.Usage{}) || outcome.result.Result != "" || len(outcome.result.Artifacts) != 0 || outcome.result.Proposal != nil {
		// Unknown usage may contain a streamed prefix. Validate only the bounded
		// non-usage envelope; the reservation remains the accounting authority.
		probe := outcome.result
		probe.Status = jobs.AttemptStatusFailed
		probe.Error = "provider invocation failed with incomplete usage"
		probe.Proposal = nil
		probe.Usage = conservativeReservationUsage(started.Reservation)
		if err := probe.ValidateFor(invocation); err != nil {
			return normalizedInvokeOutcome{err: fmt.Errorf("%w: invalid partial error result: %v", ErrInvokerConformance, err)}
		}
		finished.Result = outcome.result.Result
		finished.Artifacts = slices.Clone(outcome.result.Artifacts)
		finished.Fingerprint = normalizedResultFingerprint(probe)
	}
	// A transport error means the returned usage may be only a prefix of the
	// external work. Once dispatch was admitted, charge the full reservation so
	// retries and later cycles cannot reuse potentially spent provider budget.
	if usageProvenance == UsageFactual {
		finished.Usage = outcome.result.Usage
	} else {
		finished.Usage = conservativeReservationUsage(started.Reservation)
	}
	finished.Error = compactRuntimeError(outcome.err)
	retryableUnknownRead := transient && usageProvenance == UsageUnknown && !writer && !operatorCancelled && runErr == nil &&
		!errors.Is(outcome.err, context.Canceled) && !errors.Is(outcome.err, context.DeadlineExceeded)
	if writer {
		finished.Status = jobs.AttemptStatusAmbiguous
		finished.Error = "writer invocation ended without a provable external mutation outcome: " + finished.Error
	} else if operatorCancelled && runErr != nil {
		finished.Status = jobs.AttemptStatusCancelled
	} else if errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded) || runErr != nil {
		finished.Status = jobs.AttemptStatusAbandoned
	} else if retryableUnknownRead {
		finished.Status = jobs.AttemptStatusAbandoned
	} else {
		finished.Status = jobs.AttemptStatusFailed
	}
	if usageProvenance == UsageFactual {
		return finalizeFactualUsage(finished, outcome.result.Usage)
	}
	normalized := normalizedInvokeOutcome{attempt: finished}
	if retryableUnknownRead {
		normalized.transientErr = outcome.err
		normalized.retryAfter = retryAfter
	}
	return normalized
}

func emptyInvocationResult(result InvocationResult) bool {
	return result.Status == "" && result.Result == "" && result.Fingerprint == "" && len(result.Artifacts) == 0 &&
		result.Usage == (jobs.Usage{}) && result.Proposal == nil && result.Error == ""
}

func outcomeWasDispatched(outcome invokeOutcome) bool {
	dispatch, _, ok := InvocationFailureFromError(outcome.err)
	return !ok || dispatch != DispatchNotDispatched
}

func finalizeFactualUsage(finished jobs.Attempt, factual jobs.Usage) normalizedInvokeOutcome {
	charged, excess := splitUsageForReservation(factual, finished.Reservation)
	finished.Usage = charged
	return normalizedInvokeOutcome{
		attempt: finished, excess: excess, budgetExceeded: excess != (jobs.Usage{}),
	}
}

// splitUsageForReservation keeps Attempt.Usage valid while preserving every
// factual component in a standalone excess delta. Output is admitted first so
// its per-call cap remains explicit, followed by input within total capacity.
func splitUsageForReservation(factual jobs.Usage, reservation jobs.AttemptReservation) (jobs.Usage, jobs.Usage) {
	charged := jobs.Usage{ModelCalls: min(factual.ModelCalls, reservation.ModelCalls)}
	maxOutput := saturatingMultiply(charged.ModelCalls, reservation.MaxOutputTokens)
	charged.OutputTokens = min(factual.OutputTokens, min(reservation.Tokens, maxOutput))
	charged.InputTokens = min(factual.InputTokens, reservation.Tokens-charged.OutputTokens)
	excess := jobs.Usage{
		ModelCalls:   factual.ModelCalls - charged.ModelCalls,
		InputTokens:  factual.InputTokens - charged.InputTokens,
		OutputTokens: factual.OutputTokens - charged.OutputTokens,
	}
	return charged, excess
}

func saturatingMultiply(left, right uint64) uint64 {
	if left != 0 && right > ^uint64(0)/left {
		return ^uint64(0)
	}
	return left * right
}

func validateNormalizedUsageTotal(base jobs.Usage, normalized []normalizedInvokeOutcome) error {
	total := base
	var ok bool
	for _, outcome := range normalized {
		for _, delta := range []jobs.Usage{outcome.excess, outcome.attempt.Usage} {
			if total, ok = addInvocationUsage(total, delta); !ok {
				return errors.New("factual invocation usage would overflow durable job accounting")
			}
		}
	}
	return nil
}

func addInvocationUsage(left, right jobs.Usage) (jobs.Usage, bool) {
	add := func(a, b uint64) (uint64, bool) {
		if ^uint64(0)-a < b {
			return 0, false
		}
		return a + b, true
	}
	out := left
	var ok bool
	if out.ModelCalls, ok = add(left.ModelCalls, right.ModelCalls); !ok {
		return left, false
	}
	if out.InputTokens, ok = add(left.InputTokens, right.InputTokens); !ok {
		return left, false
	}
	if out.OutputTokens, ok = add(left.OutputTokens, right.OutputTokens); !ok {
		return left, false
	}
	if _, ok = add(out.InputTokens, out.OutputTokens); !ok {
		return left, false
	}
	return out, true
}

func failedConformanceAttempt(started jobs.Attempt, err error, writer, dispatched bool) jobs.Attempt {
	finished := started
	finished.Dispatched = dispatched
	finished.Status = jobs.AttemptStatusFailed
	if writer && dispatched {
		finished.Status = jobs.AttemptStatusAmbiguous
	}
	finished.Error = compactRuntimeError(err)
	finished.Fingerprint = normalizedTextFingerprint(finished.Error)
	if dispatched {
		finished.Usage = conservativeReservationUsage(started.Reservation)
	}
	return finished
}

func conservativeReservationUsage(reservation jobs.AttemptReservation) jobs.Usage {
	return jobs.Usage{ModelCalls: reservation.ModelCalls, InputTokens: reservation.Tokens}
}

func (r *Runner) finishStartsAsRuntimeFailure(ctx context.Context, state jobs.JobState, started []jobs.Attempt, cause error) (jobs.JobState, error) {
	current, err := r.failJobWhileDraining(context.WithoutCancel(ctx), state, TerminalFailure{
		Reason: jobs.TerminalReasonUnrecoverable,
		Detail: cause.Error(),
	})
	if err != nil {
		return state, errors.Join(cause, err)
	}
	for _, attempt := range started {
		finished := failedConformanceAttempt(attempt, cause, false, false)
		var finishErr error
		current, finishErr = r.append(context.WithoutCancel(ctx), current, jobs.Event{
			ID: eventID("attempt-finish", finished.ID), Type: jobs.EventAttemptFinished, At: r.now(), Attempt: &finished,
		}, true)
		if finishErr != nil {
			return current, errors.Join(cause, finishErr)
		}
		if current.Status.IsTerminal() {
			break
		}
	}
	return current, cause
}

func (r *Runner) allocateLimits(state jobs.JobState, count int) ([]RemainingLimits, error) {
	if count <= 0 || count > jobs.MaxWorkers {
		return nil, fmt.Errorf("invalid invocation count %d", count)
	}
	if state.Usage.Attempts > state.Spec.Budget.MaxAttempts || uint64(count) > state.Spec.Budget.MaxAttempts-state.Usage.Attempts {
		return nil, errors.New("attempt budget cannot reserve every barrier item")
	}
	reservedCalls, reservedTokens := runningReservations(state)
	if state.Usage.ModelCalls > state.Spec.Budget.MaxModelCalls || reservedCalls > state.Spec.Budget.MaxModelCalls-state.Usage.ModelCalls {
		return nil, errors.New("model-call budget is exhausted")
	}
	usedTokens := state.Usage.TotalTokens()
	if usedTokens > state.Spec.Budget.MaxTokens || reservedTokens > state.Spec.Budget.MaxTokens-usedTokens {
		return nil, errors.New("token budget is exhausted")
	}
	remainingCalls := state.Spec.Budget.MaxModelCalls - state.Usage.ModelCalls - reservedCalls
	remainingTokens := state.Spec.Budget.MaxTokens - usedTokens - reservedTokens
	if remainingCalls < uint64(count) || remainingTokens < uint64(count) {
		return nil, errors.New("remaining budget cannot reserve every barrier item")
	}
	limits := make([]RemainingLimits, count)
	for index := 0; index < count; index++ {
		slots := uint64(count - index)
		calls := min(r.options.MaxModelCallsPerAttempt, remainingCalls/slots)
		tokens := min(r.options.MaxTokensPerAttempt, remainingTokens/slots)
		maxOutput := min(r.options.MaxOutputTokensPerCall, tokens)
		limits[index] = RemainingLimits{ModelCalls: calls, Tokens: tokens, MaxOutputTokens: maxOutput}
		remainingCalls -= calls
		remainingTokens -= tokens
	}
	return limits, nil
}

func (r *Runner) append(ctx context.Context, state jobs.JobState, event jobs.Event, allowCancellationRebase bool) (jobs.JobState, error) {
	expected := state.Revision
	next, err := r.store.Append(ctx, state.Spec.ID, expected, event)
	if err == nil {
		return next, nil
	}
	if errors.Is(err, jobstore.ErrCommitted) {
		// Exact replay is safe at its original revision even if later events won.
		return r.store.Append(context.WithoutCancel(ctx), state.Spec.ID, expected, event)
	}
	rebasableEvent := event.Type == jobs.EventAttemptFinished || event.Type == jobs.EventJobFailed
	if !errors.Is(err, jobstore.ErrConflict) || !allowCancellationRebase || !rebasableEvent {
		if errors.Is(err, jobstore.ErrConflict) {
			return state, fmt.Errorf("%w: %v", ErrForeignConflict, err)
		}
		return state, err
	}
	loaded, loadErr := r.store.Load(context.WithoutCancel(ctx), state.Spec.ID)
	if loadErr != nil {
		return state, loadErr
	}
	if !isExactCancellationSuccessor(state, loaded) {
		return state, fmt.Errorf("%w: intervening revision was not exactly cancellation_requested", ErrForeignConflict)
	}
	next, err = r.store.Append(context.WithoutCancel(ctx), loaded.Spec.ID, loaded.Revision, event)
	if err == nil {
		return next, nil
	}
	if errors.Is(err, jobstore.ErrCommitted) {
		return r.store.Append(context.WithoutCancel(ctx), loaded.Spec.ID, loaded.Revision, event)
	}
	if errors.Is(err, jobstore.ErrConflict) {
		return loaded, fmt.Errorf("%w: second revision changed during cancellation reconciliation", ErrForeignConflict)
	}
	return loaded, err
}

func isExactCancellationSuccessor(before, after jobs.JobState) bool {
	if before.Spec.ID == "" || after.Revision != before.Revision+1 {
		return false
	}
	cancelEvent := jobs.Event{
		ID: eventID("cancel-request", before.Spec.ID), Type: jobs.EventCancellationRequested,
		At: before.Spec.Deadline,
	}
	expected, err := jobs.Reduce(before, cancelEvent)
	return err == nil && reflect.DeepEqual(expected, after)
}

// RequestCancel persists operator intent independently of the request context
// and then interrupts any live invocation owned by this Runner.
func (r *Runner) RequestCancel(ctx context.Context, jobID string) (jobs.JobState, error) {
	coordinator, release, err := acquireJobCoordinator(r.store, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	defer release()
	coordinator.gate.Lock()
	defer coordinator.gate.Unlock()
	durableCtx, durableCancel := context.WithTimeout(context.WithoutCancel(ctx), cancelStoreTimeout)
	defer durableCancel()

	for attempts := 0; attempts < maxAppendReconcileAttempts; attempts++ {
		state, err := r.store.Load(durableCtx, jobID)
		if err != nil {
			return jobs.JobState{}, err
		}
		if state.Status.IsTerminal() {
			return state, nil
		}
		if state.CancelRequested {
			coordinator.cancelActiveLocked()
			return state, nil
		}
		event := jobs.Event{
			ID: eventID("cancel-request", jobID), Type: jobs.EventCancellationRequested, At: r.now(),
		}
		next, err := r.store.Append(durableCtx, jobID, state.Revision, event)
		if err == nil || errors.Is(err, jobstore.ErrCommitted) {
			// The successful durable append is the cancellation linearization
			// point. Dispatch admission uses the same gate, so nothing can be
			// admitted after this point without observing CancelRequested.
			coordinator.cancelActiveLocked()
			if err == nil {
				return next, nil
			}
			return r.store.Append(durableCtx, jobID, state.Revision, event)
		}
		if !errors.Is(err, jobstore.ErrConflict) {
			return state, err
		}
	}
	return jobs.JobState{}, fmt.Errorf("%w: cancel retry limit", ErrForeignConflict)
}

func (r *Runner) RequestPause(ctx context.Context, jobID string) (jobs.JobState, error) {
	coordinator, release, err := acquireJobCoordinator(r.store, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if !coordinator.run.TryLock() {
		release()
		return jobs.JobState{}, fmt.Errorf("%w: active invocations must drain before pause", ErrJobNotControllable)
	}
	if coordinator.isQuarantined() {
		coordinator.run.Unlock()
		release()
		return jobs.JobState{}, fmt.Errorf("%w: invocation is still draining", ErrJobNotControllable)
	}
	defer func() {
		coordinator.run.Unlock()
		release()
	}()
	state, err := r.store.Load(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if state.CurrentBatch != nil {
		return state, fmt.Errorf("%w: batch is active", ErrJobNotControllable)
	}
	return r.append(ctx, state, jobs.Event{
		ID: eventID("pause", jobID, strconv.FormatUint(state.Revision, 10)), Type: jobs.EventJobPaused, At: r.now(),
	}, false)
}

func (r *Runner) RequestResume(ctx context.Context, jobID string) (jobs.JobState, error) {
	coordinator, release, err := acquireJobCoordinator(r.store, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if !coordinator.run.TryLock() {
		release()
		return jobs.JobState{}, fmt.Errorf("%w: runner active", ErrJobNotControllable)
	}
	if coordinator.isQuarantined() {
		coordinator.run.Unlock()
		release()
		return jobs.JobState{}, fmt.Errorf("%w: invocation is still draining", ErrJobNotControllable)
	}
	defer func() {
		coordinator.run.Unlock()
		release()
	}()
	state, err := r.store.Load(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if state.Status != jobs.JobStatusPaused && state.Status != jobs.JobStatusWaiting {
		return state, fmt.Errorf("%w: status %q", ErrJobNotControllable, state.Status)
	}
	return r.append(ctx, state, jobs.Event{
		ID: eventID("resume", jobID, strconv.FormatUint(state.Revision, 10)), Type: jobs.EventJobResumed, At: r.now(),
	}, false)
}
func (r *Runner) now() time.Time {
	return r.options.Now().UTC()
}

func firstRunningAttempt(state jobs.JobState) (jobs.Attempt, bool) {
	for _, attempt := range state.Attempts {
		if attempt.Status == jobs.AttemptStatusRunning {
			return attempt, true
		}
	}
	return jobs.Attempt{}, false
}

func unresolvedItems(state jobs.JobState) []jobs.WorkItem {
	if state.CurrentBatch == nil {
		return nil
	}
	items := make([]jobs.WorkItem, 0, len(state.CurrentBatch.Items))
	for _, item := range state.CurrentBatch.Items {
		latest, found := latestRuntimeAttempt(state.Attempts, state.CurrentBatch.ID, item.ID)
		if found {
			switch latest.Status {
			case jobs.AttemptStatusSucceeded, jobs.AttemptStatusCancelled:
				continue
			case jobs.AttemptStatusFailed:
				isControl := item.RoleID == state.Spec.Workflow.ReducerRoleID || item.RoleID == state.Spec.Workflow.SupervisorRoleID
				if !isControl || latest.AttemptNo >= DefaultMaxControlAttempts {
					continue
				}
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func latestRuntimeAttempt(attempts []jobs.Attempt, batchID, itemID string) (jobs.Attempt, bool) {
	var latest jobs.Attempt
	found := false
	for _, attempt := range attempts {
		if attempt.BatchID == batchID && attempt.WorkItemID == itemID && (!found || attempt.AttemptNo > latest.AttemptNo) {
			latest, found = attempt, true
		}
	}
	return latest, found
}

func nextAttemptNo(state jobs.JobState, batchID, itemID string) uint64 {
	latest, found := latestRuntimeAttempt(state.Attempts, batchID, itemID)
	if !found {
		return 1
	}
	return latest.AttemptNo + 1
}

func supervisorDecision(state jobs.JobState) (*jobs.Decision, error) {
	var latest *jobs.Attempt
	for index := range state.Attempts {
		attempt := &state.Attempts[index]
		if attempt.Cycle != state.Cycle || attempt.RoleID != state.Spec.Workflow.SupervisorRoleID {
			continue
		}
		if latest == nil || attempt.AttemptNo > latest.AttemptNo {
			latest = attempt
		}
	}
	if latest == nil || latest.Status != jobs.AttemptStatusSucceeded || latest.Decision == nil {
		return nil, errors.New("supervisor barrier closed without a successful persisted decision")
	}
	decision := *latest.Decision
	if latest.Decision.NextBatch != nil {
		batch := *latest.Decision.NextBatch
		batch.Items = slices.Clone(batch.Items)
		decision.NextBatch = &batch
	}
	return &decision, nil
}

func runningReservations(state jobs.JobState) (uint64, uint64) {
	var calls, tokens uint64
	for _, attempt := range state.Attempts {
		if attempt.Status == jobs.AttemptStatusRunning {
			calls = saturatingAddUint64(calls, attempt.Reservation.ModelCalls)
			tokens = saturatingAddUint64(tokens, attempt.Reservation.Tokens)
		}
	}
	return calls, tokens
}

func saturatingAddUint64(left, right uint64) uint64 {
	if right > ^uint64(0)-left {
		return ^uint64(0)
	}
	return left + right
}

func canonicalInvocationArtifacts(artifacts []jobs.ArtifactRef) []jobs.ArtifactRef {
	out := slices.Clone(artifacts)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > MaxInvocationArtifacts {
		out = slices.Clone(out[len(out)-MaxInvocationArtifacts:])
	}
	return out
}

func normalizedResultFingerprint(result InvocationResult) string {
	canonical := struct {
		Status    jobs.AttemptStatus `json:"status"`
		Result    string             `json:"result,omitempty"`
		Error     string             `json:"error,omitempty"`
		Artifacts []jobs.ArtifactRef `json:"artifacts,omitempty"`
	}{Status: result.Status, Result: strings.TrimSpace(result.Result), Error: strings.TrimSpace(result.Error), Artifacts: slices.Clone(result.Artifacts)}
	sort.Slice(canonical.Artifacts, func(i, j int) bool { return canonical.Artifacts[i].ID < canonical.Artifacts[j].ID })
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedTextFingerprint(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compactRuntimeError(err error) string {
	if err == nil {
		return "runtime failure"
	}
	value := secrets.Redact(strings.TrimSpace(err.Error()))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value)
	if !utf8.ValidString(value) {
		value = "runtime error contained invalid UTF-8"
	}
	if len(value) > MaxInvocationErrorBytes {
		value = string([]byte(value)[:MaxInvocationErrorBytes])
		for !utf8.ValidString(value) && len(value) > 0 {
			value = value[:len(value)-1]
		}
	}
	if strings.TrimSpace(value) == "" {
		return "runtime failure"
	}
	return value
}

func eventID(kind string, parts ...string) string {
	return portableMaterializedID("event", append([]string{kind}, parts...)...)
}
