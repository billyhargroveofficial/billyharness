package jobruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func TestTwoRunnerInstancesShareJobOwnershipAndActiveCancellation(t *testing.T) {
	store := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	spec := runnerTestSpec(t, "two-runner-owner", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	entered := make(chan Invocation, 1)
	invoker := newRunnerTestInvoker(func(ctx context.Context, invocation Invocation, _ int) (InvocationResult, error) {
		entered <- invocation
		<-ctx.Done()
		return InvocationResult{}, ctx.Err()
	})
	first := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	second := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	result := make(chan runnerTestRunResult, 1)
	go func() {
		state, err := first.Run(context.Background(), spec.ID)
		result <- runnerTestRunResult{state: state, err: err}
	}()
	waitRunnerTestInvocations(t, entered, 1)

	if _, _, err := second.Step(context.Background(), spec.ID); !errors.Is(err, ErrRunnerBusy) {
		t.Fatalf("second Runner Step() error = %v, want ErrRunnerBusy", err)
	}
	active, err := store.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Attempts) != 1 || active.Attempts[0].Status != jobs.AttemptStatusRunning {
		t.Fatalf("active attempt was orphaned by second Runner: %#v", active.Attempts)
	}

	requested, err := second.RequestCancel(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("second Runner RequestCancel(): %v", err)
	}
	if !requested.CancelRequested {
		t.Fatal("second Runner did not durably commit cancellation")
	}
	completed := waitRunnerTestRun(t, result)
	if completed.err != nil {
		t.Fatalf("first Runner Run(): %v", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusCancelled, jobs.TerminalReasonOperatorCancellation)
	if got := len(invoker.Invocations()); got != 1 {
		t.Fatalf("invocations = %d, want exactly one without orphan recovery/reinvoke", got)
	}
}

func TestCommittedCancellationDuringPreparationPreventsDispatch(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	store := newAttemptStartBarrierStore(base)
	spec := runnerTestSpec(t, "cancel-before-dispatch", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	canceller := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestStep(t, runner, spec.ID)
	runnerTestStep(t, runner, spec.ID)
	store.Arm()

	result := make(chan runnerTestStepResult, 1)
	go func() {
		state, progressed, err := runner.Step(context.Background(), spec.ID)
		result <- runnerTestStepResult{state: state, progressed: progressed, err: err}
	}()
	store.WaitAttemptStarted(t)
	requested, err := canceller.RequestCancel(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("RequestCancel(): %v", err)
	}
	if !requested.CancelRequested {
		t.Fatal("cancellation did not reach its durable linearization point")
	}
	store.Release()
	completed := waitRunnerTestStep(t, result)
	if completed.err != nil {
		t.Fatalf("Step(): %v", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusCancelled, jobs.TerminalReasonOperatorCancellation)
	if got := len(invoker.Invocations()); got != 0 {
		t.Fatalf("invocations after committed pre-dispatch cancellation = %d, want 0", got)
	}
	if len(completed.state.Attempts) != 1 || completed.state.Attempts[0].Dispatched || completed.state.Attempts[0].Status != jobs.AttemptStatusCancelled {
		t.Fatalf("known-uninvoked cancellation attempt = %#v", completed.state.Attempts)
	}
}

func TestBuildFailureRacingWithCancellationDrainsUndispatchedAttempt(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	store := newAttemptStartBarrierStore(base)
	spec := runnerTestSpec(t, "build-failure-cancel-race", jobs.PresetGeneral, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(nil)
	preparer := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, preparer, spec.ID, "explore")

	failing, err := NewRunner(store, invoker, RunnerOptions{
		ServerAuthority: jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{"kimi"}},
	})
	if err != nil {
		t.Fatalf("NewRunner(failing): %v", err)
	}
	canceller := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	beforeCalls := len(invoker.Invocations())
	store.Arm()
	result := make(chan runnerTestStepResult, 1)
	go func() {
		state, progressed, stepErr := failing.Step(context.Background(), spec.ID)
		result <- runnerTestStepResult{state: state, progressed: progressed, err: stepErr}
	}()
	store.WaitAttemptStarted(t)
	if _, err := canceller.RequestCancel(context.Background(), spec.ID); err != nil {
		t.Fatalf("RequestCancel(): %v", err)
	}
	store.Release()
	completed := waitRunnerTestStep(t, result)
	if completed.err == nil || !strings.Contains(completed.err.Error(), "build invocation") {
		t.Fatalf("Step() error = %v, want build invocation failure", completed.err)
	}
	assertRunnerTestTerminal(t, completed.state, jobs.JobStatusFailed, jobs.TerminalReasonUnrecoverable)
	if len(completed.state.Attempts) == 0 {
		t.Fatal("undispatched attempt was not retained")
	}
	latest := completed.state.Attempts[len(completed.state.Attempts)-1]
	if latest.Dispatched || latest.Status != jobs.AttemptStatusFailed || latest.Usage != (jobs.Usage{}) {
		t.Fatalf("build-failure attempt = %#v, want undispatched failed with zero provider usage", latest)
	}
	if got := len(invoker.Invocations()); got != beforeCalls {
		t.Fatalf("provider invocations = %d, want unchanged %d", got, beforeCalls)
	}
}

func TestCallerCancellationAfterWriterStartRemainsUndispatchedAndRetryable(t *testing.T) {
	base := newRunnerTestStore(t, filepath.Join(t.TempDir(), "jobs"))
	store := newAttemptStartBarrierStore(base)
	spec := runnerTestSpec(t, "writer-predispatch-retry", jobs.PresetCoding, 1, time.Now().Add(time.Hour))
	createRunnerTestJob(t, store, spec)
	invoker := newRunnerTestInvoker(nil)
	runner := newRunnerTestRunner(t, store, invoker, nil, RunnerOptions{})
	runnerTestAdvanceToBatch(t, runner, spec.ID, "implement")
	beforeInvocations := len(invoker.Invocations())
	store.Arm()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan runnerTestStepResult, 1)
	go func() {
		state, progressed, err := runner.Step(ctx, spec.ID)
		result <- runnerTestStepResult{state: state, progressed: progressed, err: err}
	}()
	store.WaitAttemptStarted(t)
	cancel()
	store.Release()
	aborted := waitRunnerTestStep(t, result)
	if !errors.Is(aborted.err, context.Canceled) {
		t.Fatalf("Step() error = %v, want context.Canceled", aborted.err)
	}
	if len(aborted.state.Attempts) == 0 {
		t.Fatal("known-uninvoked writer attempt was not durably finished")
	}
	latest := aborted.state.Attempts[len(aborted.state.Attempts)-1]
	if latest.RoleID != "coding.implementer" || latest.Status != jobs.AttemptStatusAbandoned || latest.Dispatched {
		t.Fatalf("predispatch writer outcome = %#v, want undispatched abandoned", latest)
	}
	if got := len(invoker.Invocations()); got != beforeInvocations {
		t.Fatalf("writer was invoked after caller cancellation: calls %d, want %d", got, beforeInvocations)
	}

	retried, progressed, err := runner.Step(context.Background(), spec.ID)
	if err != nil || !progressed {
		t.Fatalf("retry Step() = progressed %v, err %v", progressed, err)
	}
	latest = retried.Attempts[len(retried.Attempts)-1]
	if latest.RoleID != "coding.implementer" || latest.AttemptNo != 2 || latest.Status != jobs.AttemptStatusSucceeded || !latest.Dispatched {
		t.Fatalf("retried writer outcome = %#v", latest)
	}
	if got := len(invoker.Invocations()); got != beforeInvocations+1 {
		t.Fatalf("writer retry invocations = %d, want %d", got, beforeInvocations+1)
	}
}

type runnerTestStepResult struct {
	state      jobs.JobState
	progressed bool
	err        error
}

func waitRunnerTestStep(t *testing.T, result <-chan runnerTestStepResult) runnerTestStepResult {
	t.Helper()
	select {
	case completed := <-result:
		return completed
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runner step")
		return runnerTestStepResult{}
	}
}

type attemptStartBarrierStore struct {
	jobstore.Store

	mu      sync.Mutex
	armed   bool
	blocked chan struct{}
	release chan struct{}
}

func newAttemptStartBarrierStore(store jobstore.Store) *attemptStartBarrierStore {
	return &attemptStartBarrierStore{
		Store: store, blocked: make(chan struct{}, 1), release: make(chan struct{}),
	}
}

func (s *attemptStartBarrierStore) Arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

func (s *attemptStartBarrierStore) Append(ctx context.Context, jobID string, revision uint64, event jobs.Event) (jobs.JobState, error) {
	next, err := s.Store.Append(ctx, jobID, revision, event)
	if err != nil || event.Type != jobs.EventAttemptStarted {
		return next, err
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
	return next, nil
}

func (s *attemptStartBarrierStore) WaitAttemptStarted(t *testing.T) {
	t.Helper()
	select {
	case <-s.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for committed AttemptStarted")
	}
}

func (s *attemptStartBarrierStore) Release() {
	close(s.release)
}
