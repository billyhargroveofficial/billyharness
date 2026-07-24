package jobservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobruntime"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

var _ StepRunner = (*jobruntime.Runner)(nil)

func TestManagerRecoverResumesRunningAndRearmsWaiting(t *testing.T) {
	store := newMemoryStore()
	for _, item := range []struct {
		id     string
		status jobs.JobStatus
	}{
		{id: "recover-running", status: jobs.JobStatusRunning},
		{id: "recover-queued", status: jobs.JobStatusQueued},
		{id: "recover-paused", status: jobs.JobStatusPaused},
		{id: "recover-waiting", status: jobs.JobStatusWaiting},
	} {
		state := createMemoryJob(t, store, item.id, item.status)
		if item.status == jobs.JobStatusWaiting {
			state.Spec.Deadline = time.Now().UTC().Add(time.Hour)
			store.set(state)
		}
	}

	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		state := store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
		})
		return state, true, nil
	}
	manager := newTestManager(t, store, runner)
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatalf("Recover(): %v", err)
	}
	waitInactive(t, manager, "recover-running")

	if got := runner.stepCount("recover-running"); got != 1 {
		t.Fatalf("running step calls = %d, want 1", got)
	}
	for _, id := range []string{"recover-queued", "recover-paused"} {
		if got := runner.stepCount(id); got != 0 {
			t.Fatalf("%s step calls = %d, want 0", id, got)
		}
		if manager.Active(id) {
			t.Fatalf("%s unexpectedly active", id)
		}
	}
	if got := runner.stepCount("recover-waiting"); got != 0 {
		t.Fatalf("waiting step calls before deadline = %d, want 0", got)
	}
	if !manager.Active("recover-waiting") {
		t.Fatal("waiting job deadline watcher was not recovered")
	}
	view, err := manager.Get(context.Background(), "recover-running")
	if err != nil {
		t.Fatal(err)
	}
	if view.Active || view.State.Status != jobs.JobStatusCompleted {
		t.Fatalf("recovered view = %+v", view)
	}
}

func TestManagerRecoverTerminalizesExpiredQueuedAndPausedJobs(t *testing.T) {
	store := newMemoryStore()
	for _, item := range []struct {
		id      string
		status  jobs.JobStatus
		expired bool
	}{
		{id: "expired-queued", status: jobs.JobStatusQueued, expired: true},
		{id: "expired-paused", status: jobs.JobStatusPaused, expired: true},
		{id: "live-queued", status: jobs.JobStatusQueued},
	} {
		state := createMemoryJob(t, store, item.id, item.status)
		if item.expired {
			state.Spec.Deadline = time.Now().UTC().Add(-time.Second)
		} else {
			state.Spec.Deadline = time.Now().UTC().Add(time.Hour)
		}
		store.set(state)
	}

	runner := &scriptedRunner{store: store}
	runner.expire = func(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
		state, err := store.Load(ctx, jobID)
		if err != nil || time.Now().UTC().Before(state.Spec.Deadline) {
			return state, false, err
		}
		state = store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusFailed
			state.TerminalReason = jobs.TerminalReasonDeadline
		})
		return state, true, nil
	}
	manager := newTestManager(t, store, runner)
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.expireCalls.Load() != 3 {
		t.Fatalf("ExpireDue calls = %d, want 3", runner.expireCalls.Load())
	}
	for _, id := range []string{"expired-queued", "expired-paused"} {
		state, err := store.Load(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status != jobs.JobStatusFailed || state.TerminalReason != jobs.TerminalReasonDeadline {
			t.Fatalf("%s recovery state = %#v", id, state)
		}
	}
	live, err := store.Load(context.Background(), "live-queued")
	if err != nil || live.Status != jobs.JobStatusQueued {
		t.Fatalf("live queued state = %#v err=%v", live, err)
	}
}

func TestManagerRecoveredScheduledWaitSleepsThenResumesWithoutHotLoop(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	state := createMemoryJob(t, store, "scheduled-recovery", jobs.JobStatusWaiting)
	state.Spec.Deadline = now.Add(2 * time.Second)
	state.Spec.CycleCadenceSeconds = 1
	state.NextWakeAt = now.Add(200 * time.Millisecond)
	state.WaitingReason = "scheduled cycle cadence"
	store.set(state)

	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
			state.NextWakeAt = time.Time{}
			state.WaitingReason = ""
		}), true, nil
	}
	manager := newTestManager(t, store, runner)
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatalf("Recover(): %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if !manager.Active(state.Spec.ID) || runner.resumeCalls.Load() != 0 || runner.stepCount(state.Spec.ID) != 0 {
		t.Fatalf("scheduled wait hot-looped: active=%t resume=%d step=%d", manager.Active(state.Spec.ID), runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
	}
	waitInactive(t, manager, state.Spec.ID)
	if runner.resumeCalls.Load() != 1 || runner.stepCount(state.Spec.ID) != 1 {
		t.Fatalf("scheduled wake calls resume/step = %d/%d, want 1/1", runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
	}
}

func TestManagerRecoveredManualWaitRemainsManualButDeadlineTerminates(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	state := createMemoryJob(t, store, "manual-wait-deadline", jobs.JobStatusWaiting)
	state.Spec.Deadline = now.Add(200 * time.Millisecond)
	state.NextWakeAt = time.Time{}
	state.WaitingReason = "operator evidence required"
	store.set(state)

	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusFailed
			state.TerminalReason = jobs.TerminalReasonDeadline
			state.WaitingReason = ""
		}), true, nil
	}
	manager := newTestManager(t, store, runner)
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatalf("Recover(): %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if runner.resumeCalls.Load() != 0 || runner.stepCount(state.Spec.ID) != 0 {
		t.Fatalf("manual wait advanced before deadline: resume=%d step=%d", runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
	}
	waitInactive(t, manager, state.Spec.ID)
	terminal, err := store.Load(context.Background(), state.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != jobs.JobStatusFailed || terminal.TerminalReason != jobs.TerminalReasonDeadline || runner.resumeCalls.Load() != 0 || runner.stepCount(state.Spec.ID) != 1 {
		t.Fatalf("manual wait deadline = %#v resume=%d step=%d", terminal, runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
	}
}

func TestManagerResumeActiveManualWaitAndRejectsEarlyScheduledResume(t *testing.T) {
	t.Run("manual", func(t *testing.T) {
		store := newMemoryStore()
		state := createMemoryJob(t, store, "active-manual-resume", jobs.JobStatusWaiting)
		state.Spec.Deadline = time.Now().UTC().Add(2 * time.Second)
		state.WaitingReason = "operator evidence required"
		store.set(state)
		runner := &scriptedRunner{store: store}
		runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
			return store.mutate(t, jobID, func(state *jobs.JobState) {
				state.Status = jobs.JobStatusCompleted
				state.TerminalReason = jobs.TerminalReasonSuccess
			}), true, nil
		}
		manager := newTestManager(t, store, runner)
		if err := manager.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Resume(context.Background(), state.Spec.ID); err != nil {
			t.Fatalf("Resume(): %v", err)
		}
		waitInactive(t, manager, state.Spec.ID)
		if runner.resumeCalls.Load() != 1 || runner.stepCount(state.Spec.ID) != 1 {
			t.Fatalf("manual resume calls = %d/%d", runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
		}
	})

	t.Run("scheduled", func(t *testing.T) {
		store := newMemoryStore()
		now := time.Now().UTC()
		state := createMemoryJob(t, store, "early-scheduled-resume", jobs.JobStatusWaiting)
		state.Spec.Deadline = now.Add(2 * time.Second)
		state.Spec.CycleCadenceSeconds = 1
		state.NextWakeAt = now.Add(200 * time.Millisecond)
		state.WaitingReason = "scheduled cycle cadence"
		store.set(state)
		runner := &scriptedRunner{store: store}
		runner.resume = func(_ context.Context, jobID string) (jobs.JobState, error) {
			current, err := store.Load(context.Background(), jobID)
			if err != nil {
				return jobs.JobState{}, err
			}
			if time.Now().Before(current.NextWakeAt) {
				return current, errors.New("scheduled cadence wait is not due")
			}
			return store.mutate(t, jobID, func(state *jobs.JobState) {
				state.Status = jobs.JobStatusRunning
				state.NextWakeAt = time.Time{}
				state.WaitingReason = ""
			}), nil
		}
		runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
			return store.mutate(t, jobID, func(state *jobs.JobState) {
				state.Status = jobs.JobStatusCompleted
				state.TerminalReason = jobs.TerminalReasonSuccess
			}), true, nil
		}
		manager := newTestManager(t, store, runner)
		if err := manager.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		view, err := manager.Resume(context.Background(), state.Spec.ID)
		if !errors.Is(err, ErrNotControllable) || !view.Active || view.State.Status != jobs.JobStatusWaiting {
			t.Fatalf("early Resume() = %+v, %v", view, err)
		}
		waitInactive(t, manager, state.Spec.ID)
		if runner.resumeCalls.Load() != 2 || runner.stepCount(state.Spec.ID) != 1 {
			t.Fatalf("scheduled resume calls = %d/%d, want 2/1", runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
		}
	})
}

func TestManagerCancelScheduledWaitPreventsWake(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	state := createMemoryJob(t, store, "cancel-scheduled-wait", jobs.JobStatusWaiting)
	state.Spec.Deadline = now.Add(2 * time.Second)
	state.Spec.CycleCadenceSeconds = 1
	state.NextWakeAt = now.Add(500 * time.Millisecond)
	store.set(state)
	runner := &scriptedRunner{store: store}
	manager := newTestManager(t, store, runner)
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Cancel(context.Background(), state.Spec.ID); err != nil {
		t.Fatal(err)
	}
	waitInactive(t, manager, state.Spec.ID)
	if runner.resumeCalls.Load() != 0 || runner.stepCount(state.Spec.ID) != 0 {
		t.Fatalf("cancelled wait advanced: resume=%d step=%d", runner.resumeCalls.Load(), runner.stepCount(state.Spec.ID))
	}
}

func TestManagerRecoverSkipsAndReportsQuarantineWhileResumingHealthyJob(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "recover-healthy", jobs.JobStatusRunning)
	store.quarantined = []jobstore.JobSummary{{
		ID: "recover-corrupt",
		Quarantine: &jobstore.QuarantineReport{
			Kind: jobstore.CorruptionHashMismatch,
			Line: 3,
			Seq:  3,
		},
	}}

	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
		}), true, nil
	}
	manager := newTestManager(t, store, runner)
	if err := manager.Recover(context.Background()); err != nil {
		t.Fatalf("Recover(): %v", err)
	}
	waitInactive(t, manager, "recover-healthy")
	if got := runner.stepCount("recover-healthy"); got != 1 {
		t.Fatalf("healthy recovery step count = %d, want 1", got)
	}
	if got := runner.stepCount("recover-corrupt"); got != 0 {
		t.Fatalf("quarantined recovery step count = %d, want 0", got)
	}

	summaries, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var corrupt *Summary
	for index := range summaries {
		if summaries[index].Job.ID == "recover-corrupt" {
			corrupt = &summaries[index]
			break
		}
	}
	if corrupt == nil || corrupt.Active ||
		corrupt.LastError != "quarantined: corruption kind=hash_mismatch line=3 seq=3" {
		t.Fatalf("quarantine summary = %#v", corrupt)
	}
}

func TestManagerShutdownLeavesRunningForRestart(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "shutdown-restart", jobs.JobStatusRunning)
	entered := make(chan struct{})
	var enteredOnce sync.Once
	runner1 := &scriptedRunner{store: store}
	runner1.step = func(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		state, _ := store.Load(context.Background(), jobID)
		return state, false, ctx.Err()
	}
	manager1 := newTestManager(t, store, runner1)
	if _, err := manager1.Start(context.Background(), "shutdown-restart"); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	waitClosed(t, entered, "first Step admission")
	if err := manager1.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager1.Shutdown(cancelledContext); err != nil {
		t.Fatalf("idempotent Shutdown after completion: %v", err)
	}
	state, err := store.Load(context.Background(), "shutdown-restart")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != jobs.JobStatusRunning || state.CancelRequested || state.TerminalReason != "" {
		t.Fatalf("shutdown mutated durable operator state: %+v", state)
	}
	if got := manager1.LastError("shutdown-restart"); got != "" {
		t.Fatalf("shutdown last error = %q", got)
	}

	runner2 := &scriptedRunner{store: store}
	runner2.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
		}), true, nil
	}
	manager2 := newTestManager(t, store, runner2)
	if err := manager2.Recover(context.Background()); err != nil {
		t.Fatalf("Recover after restart: %v", err)
	}
	waitInactive(t, manager2, "shutdown-restart")
	state, _ = store.Load(context.Background(), "shutdown-restart")
	if state.Status != jobs.JobStatusCompleted || runner2.stepCount("shutdown-restart") != 1 {
		t.Fatalf("restart state=%+v step calls=%d", state, runner2.stepCount("shutdown-restart"))
	}
}

func TestManagerDuplicateStartIsIdempotent(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "duplicate-start", jobs.JobStatusRunning)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		once.Do(func() { close(entered) })
		<-release
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
		}), true, nil
	}
	manager := newTestManager(t, store, runner)

	const callers = 64
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			view, err := manager.Start(context.Background(), "duplicate-start")
			if err == nil && !view.Active {
				err = errors.New("duplicate Start returned inactive view")
			}
			errs <- err
		}()
	}
	close(start)
	waitClosed(t, entered, "single Step")
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := runner.stepCount("duplicate-start"); got != 1 {
		t.Fatalf("Step calls while blocked = %d, want 1", got)
	}
	if got := runner.maxConcurrent.Load(); got != 1 {
		t.Fatalf("max concurrent Step calls = %d, want 1", got)
	}
	close(release)
	waitInactive(t, manager, "duplicate-start")
	if _, err := manager.Start(context.Background(), "duplicate-start"); !errors.Is(err, ErrNotStartable) {
		t.Fatalf("Start terminal error = %v, want ErrNotStartable", err)
	}
}

func TestManagerStartAcknowledgesOnlyAfterDurableJobStarted(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "durable-start-ack", jobs.JobStatusQueued)
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	runEntered := make(chan struct{})
	releaseRun := make(chan struct{})
	var calls atomic.Int64
	runner := &scriptedRunner{store: store}
	runner.step = func(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
		switch calls.Add(1) {
		case 1:
			close(startEntered)
			select {
			case <-releaseStart:
			case <-ctx.Done():
				return jobs.JobState{}, false, ctx.Err()
			}
			state, err := store.Load(ctx, jobID)
			if err != nil {
				return jobs.JobState{}, false, err
			}
			next, err := store.Append(ctx, jobID, state.Revision, jobs.Event{
				ID: "durable-job-started", Type: jobs.EventJobStarted, At: time.Now().UTC(),
			})
			return next, err == nil, err
		case 2:
			close(runEntered)
			select {
			case <-releaseRun:
			case <-ctx.Done():
				state, _ := store.Load(context.Background(), jobID)
				return state, false, ctx.Err()
			}
			return store.mutate(t, jobID, func(state *jobs.JobState) {
				state.Status = jobs.JobStatusCompleted
				state.TerminalReason = jobs.TerminalReasonSuccess
			}), true, nil
		default:
			return jobs.JobState{}, false, fmt.Errorf("unexpected Step call %d", calls.Load())
		}
	}
	manager := newTestManager(t, store, runner)
	result := make(chan struct {
		view View
		err  error
	}, 1)
	go func() {
		view, err := manager.Start(context.Background(), "durable-start-ack")
		result <- struct {
			view View
			err  error
		}{view, err}
	}()
	waitClosed(t, startEntered, "queued start admission")
	select {
	case got := <-result:
		t.Fatalf("Start acknowledged QUEUED before JobStarted committed: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
	queued, err := store.Load(context.Background(), "durable-start-ack")
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != jobs.JobStatusQueued || queued.Revision != 0 {
		t.Fatalf("pre-linearization state = %+v", queued)
	}

	close(releaseStart)
	waitClosed(t, runEntered, "post-start run loop")
	started := waitValue(t, result, "durable Start acknowledgement")
	if started.err != nil {
		t.Fatalf("Start(): %v", started.err)
	}
	if !started.view.Active || started.view.State.Status != jobs.JobStatusRunning || started.view.State.LastEventID != "durable-job-started" {
		t.Fatalf("Start acknowledgement = %+v", started.view)
	}
	durable, err := store.Load(context.Background(), "durable-start-ack")
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != jobs.JobStatusRunning || durable.Revision != 1 || durable.LastEventID != "durable-job-started" {
		t.Fatalf("durable state at acknowledgement = %+v", durable)
	}
	close(releaseRun)
	waitInactive(t, manager, "durable-start-ack")
}

func TestManagerRetriesTransientStepAfterReloadingDurableState(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "transient-retry", jobs.JobStatusRunning)
	transientErr := errors.New("transport api_key=super-secret-value temporarily unavailable")
	firstFailed := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseSecond := make(chan struct{})
	var calls atomic.Int64
	runner := &scriptedRunner{
		store:     store,
		retryable: func(err error) bool { return errors.Is(err, transientErr) },
	}
	runner.step = func(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
		switch calls.Add(1) {
		case 1:
			store.mutate(t, jobID, func(state *jobs.JobState) { state.Cycle = 3 })
			close(firstFailed)
			// Returning a zero state proves Manager does not trust the failed
			// call's observation and reloads the canonical state before retry.
			return jobs.JobState{}, false, transientErr
		case 2:
			close(secondEntered)
			select {
			case <-releaseSecond:
			case <-ctx.Done():
				state, _ := store.Load(context.Background(), jobID)
				return state, false, ctx.Err()
			}
			return store.mutate(t, jobID, func(state *jobs.JobState) {
				state.Status = jobs.JobStatusCompleted
				state.TerminalReason = jobs.TerminalReasonSuccess
			}), true, nil
		default:
			return jobs.JobState{}, false, fmt.Errorf("unexpected Step call %d", calls.Load())
		}
	}
	manager, err := New(store, runner, WithRetryBackoff(time.Millisecond, 2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	if _, err := manager.Start(context.Background(), "transient-retry"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, firstFailed, "transient Step error")
	waitClosed(t, secondEntered, "transient Step retry")
	view, err := manager.Start(context.Background(), "transient-retry")
	if err != nil {
		t.Fatalf("idempotent Start during retry: %v", err)
	}
	if !view.Active || view.State.Cycle != 3 {
		t.Fatalf("retry view did not use reloaded state: %+v", view)
	}
	if view.LastError == "" || contains(view.LastError, "super-secret-value") {
		t.Fatalf("retry LastError was not safely observable: %q", view.LastError)
	}
	close(releaseSecond)
	waitInactive(t, manager, "transient-retry")
	if got := calls.Load(); got != 2 {
		t.Fatalf("Step calls = %d, want 2", got)
	}
	if got := manager.LastError("transient-retry"); got != "" {
		t.Fatalf("successful retry retained LastError %q", got)
	}
}

func TestManagerShutdownInterruptsRetryBackoff(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "retry-shutdown", jobs.JobStatusRunning)
	transientErr := errors.New("temporary transport failure")
	failed := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{
		store:     store,
		retryable: func(err error) bool { return errors.Is(err, transientErr) },
	}
	runner.step = func(context.Context, string) (jobs.JobState, bool, error) {
		once.Do(func() { close(failed) })
		return jobs.JobState{}, false, transientErr
	}
	manager, err := New(store, runner, WithRetryBackoff(30*time.Second, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), "retry-shutdown"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, failed, "retry backoff admission")
	deadline := time.Now().Add(3 * time.Second)
	for manager.LastError("retry-shutdown") == "" {
		if time.Now().After(deadline) {
			t.Fatal("retry error was not observed")
		}
		time.Sleep(time.Millisecond)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown during retry backoff: %v", err)
	}
	if manager.Active("retry-shutdown") || runner.stepCount("retry-shutdown") != 1 {
		t.Fatalf("shutdown left active=%t step calls=%d", manager.Active("retry-shutdown"), runner.stepCount("retry-shutdown"))
	}
	if got := manager.LastError("retry-shutdown"); got != "" {
		t.Fatalf("shutdown retained transient LastError %q", got)
	}
}

func TestManagerRejectsInvalidRetryBackoff(t *testing.T) {
	store := newMemoryStore()
	runner := &scriptedRunner{store: store}
	for _, option := range []Option{
		WithRetryBackoff(0, time.Second),
		WithRetryBackoff(time.Second, 0),
		WithRetryBackoff(2*time.Second, time.Second),
	} {
		if _, err := New(store, runner, option); err == nil {
			t.Fatal("New() accepted invalid retry backoff")
		}
	}
}

func TestEffectiveRetryDelayHonorsProviderHintAndDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	err := delayedRetryTestError{delay: 10 * time.Second}
	if got := effectiveRetryDelay(err, time.Second, now.Add(time.Minute), now); got != 10*time.Second {
		t.Fatalf("provider retry delay = %s", got)
	}
	if got := effectiveRetryDelay(err, time.Second, now.Add(3*time.Second), now); got != 3*time.Second {
		t.Fatalf("deadline-capped retry delay = %s", got)
	}
	if got := effectiveRetryDelay(err, time.Second, now, now); got != 0 {
		t.Fatalf("expired retry delay = %s", got)
	}
}

type delayedRetryTestError struct{ delay time.Duration }

func (e delayedRetryTestError) Error() string             { return "retry later" }
func (e delayedRetryTestError) RetryDelay() time.Duration { return e.delay }

func TestManagerCancelIsDurableOperatorCancellation(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "operator-cancel", jobs.JobStatusRunning)
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	var enterOnce sync.Once
	var cancelOnce sync.Once
	runner := &scriptedRunner{store: store}
	runner.step = func(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
		enterOnce.Do(func() { close(entered) })
		select {
		case <-cancelled:
			state, _ := store.Load(context.Background(), jobID)
			return state, true, nil
		case <-ctx.Done():
			state, _ := store.Load(context.Background(), jobID)
			return state, false, ctx.Err()
		}
	}
	runner.cancel = func(_ context.Context, jobID string) (jobs.JobState, error) {
		state := store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCancelled
			state.TerminalReason = jobs.TerminalReasonOperatorCancellation
			state.CancelRequested = true
		})
		cancelOnce.Do(func() { close(cancelled) })
		return state, nil
	}
	manager := newTestManager(t, store, runner)
	if _, err := manager.Start(context.Background(), "operator-cancel"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "cancel target Step")
	view, err := manager.Cancel(context.Background(), "operator-cancel")
	if err != nil {
		t.Fatalf("Cancel(): %v", err)
	}
	if view.State.Status != jobs.JobStatusCancelled || !view.State.CancelRequested {
		t.Fatalf("cancel view = %+v", view)
	}
	waitInactive(t, manager, "operator-cancel")
	if runner.cancelCalls.Load() != 1 || manager.LastError("operator-cancel") != "" {
		t.Fatalf("cancel calls=%d last error=%q", runner.cancelCalls.Load(), manager.LastError("operator-cancel"))
	}
}

func TestManagerCancelWinsConcurrentPauseWithoutSpuriousLoopError(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "cancel-beats-pause", jobs.JobStatusRunning)
	pauseEntered := make(chan struct{})
	releasePause := make(chan struct{})
	runner := &scriptedRunner{store: store}
	runner.pause = func(_ context.Context, jobID string) (jobs.JobState, error) {
		before, _ := store.Load(context.Background(), jobID)
		close(pauseEntered)
		<-releasePause
		return before, jobstore.ErrConflict
	}
	runner.cancel = func(_ context.Context, jobID string) (jobs.JobState, error) {
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Revision++
			state.Status = jobs.JobStatusCancelled
			state.TerminalReason = jobs.TerminalReasonOperatorCancellation
			state.CancelRequested = true
		}), nil
	}
	manager := newTestManager(t, store, runner)
	pauseResult := make(chan struct {
		view View
		err  error
	}, 1)
	go func() {
		view, err := manager.Pause(context.Background(), "cancel-beats-pause")
		pauseResult <- struct {
			view View
			err  error
		}{view, err}
	}()
	waitClosed(t, pauseEntered, "pause control admission")
	if _, err := manager.Cancel(context.Background(), "cancel-beats-pause"); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}
	close(releasePause)
	result := waitValue(t, pauseResult, "superseded pause")
	if !errors.Is(result.err, ErrPauseFailed) || result.view.State.Status != jobs.JobStatusCancelled {
		t.Fatalf("Pause result after cancel = %+v, %v", result.view, result.err)
	}
	if got := manager.LastError("cancel-beats-pause"); got != "" {
		t.Fatalf("control race became loop error: %q", got)
	}
}

func TestManagerGracefulPauseThenResume(t *testing.T) {
	store := newMemoryStore()
	state := createMemoryJob(t, store, "pause-resume", jobs.JobStatusRunning)
	state.CurrentBatch = &jobs.WorkBatch{ID: "batch-active", StageID: state.Spec.Stages[0].ID, Cycle: 1, Barrier: jobs.BarrierAll, Items: []jobs.WorkItem{{
		ID: "item-active", RoleID: state.Spec.Stages[0].RoleIDs[0], Objective: "finish current batch", Authority: jobs.DenyAllAuthority(),
	}}}
	store.set(state)

	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		call := calls.Add(1)
		if call == 1 {
			close(entered)
			<-release
			return store.mutate(t, jobID, func(state *jobs.JobState) { state.CurrentBatch = nil }), true, nil
		}
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
		}), true, nil
	}
	manager := newTestManager(t, store, runner)
	if _, err := manager.Start(context.Background(), "pause-resume"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "active batch")

	pauseResult := make(chan struct {
		view View
		err  error
	}, 1)
	go func() {
		view, err := manager.Pause(context.Background(), "pause-resume")
		pauseResult <- struct {
			view View
			err  error
		}{view: view, err: err}
	}()
	select {
	case result := <-pauseResult:
		t.Fatalf("Pause returned before batch boundary: %+v", result)
	case <-time.After(20 * time.Millisecond):
	}
	if got := runner.pauseCalls.Load(); got != 0 {
		t.Fatalf("RequestPause calls before boundary = %d", got)
	}
	close(release)
	result := waitValue(t, pauseResult, "durable pause")
	if result.err != nil {
		t.Fatalf("Pause(): %v", result.err)
	}
	if result.view.Active || result.view.State.Status != jobs.JobStatusPaused || runner.pauseCalls.Load() != 1 {
		t.Fatalf("pause result=%+v calls=%d", result.view, runner.pauseCalls.Load())
	}

	resumed, err := manager.Resume(context.Background(), "pause-resume")
	if err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	if !resumed.Active || runner.resumeCalls.Load() != 1 {
		t.Fatalf("resume result=%+v calls=%d", resumed, runner.resumeCalls.Load())
	}
	waitInactive(t, manager, "pause-resume")
	final, _ := store.Load(context.Background(), "pause-resume")
	if final.Status != jobs.JobStatusCompleted || calls.Load() != 2 {
		t.Fatalf("final=%+v step calls=%d", final, calls.Load())
	}
}

func TestManagerClassifiesRunnerResumeCoordinationFailure(t *testing.T) {
	store := newMemoryStore()
	state := createMemoryJob(t, store, "resume-not-controllable", jobs.JobStatusPaused)
	coordinationErr := errors.New("runner is still draining")
	runner := &scriptedRunner{store: store}
	runner.resume = func(context.Context, string) (jobs.JobState, error) {
		// A coordination failure can happen before Runner.Load and therefore
		// legitimately return the zero state.
		return jobs.JobState{}, coordinationErr
	}
	manager, err := New(store, runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })

	view, err := manager.Resume(context.Background(), state.Spec.ID)
	if !errors.Is(err, ErrNotControllable) || !errors.Is(err, coordinationErr) {
		t.Fatalf("Resume() error = %v, want service classification and runner cause", err)
	}
	if view.State.Status != jobs.JobStatusPaused || view.Active {
		t.Fatalf("Resume() view = %#v, want dormant paused state", view)
	}
}

func TestManagerWithRuntimePausesAtDurableBatchBoundary(t *testing.T) {
	store, err := jobstore.NewFileStore(t.TempDir(), jobstore.Options{})
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	authority := jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{"qwen"}}
	spec := testSpec(t, "runtime-pause")
	spec.Deadline = time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	spec.Authority = authority
	for i := range spec.Roles {
		spec.Roles[i].Authority = authority
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	invoker := invokeFunc(func(ctx context.Context, invocation jobruntime.Invocation) (jobruntime.InvocationResult, error) {
		if invocation.Kind == jobruntime.InvocationKindWorker && invocation.Cycle == 1 {
			first.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return jobruntime.InvocationResult{}, ctx.Err()
			}
		}
		result := jobruntime.InvocationResult{
			Status: jobs.AttemptStatusSucceeded, Result: "bounded result", Fingerprint: "bounded-fingerprint",
			Usage: jobs.Usage{ModelCalls: 1, InputTokens: 2, OutputTokens: 1},
		}
		if invocation.Kind == jobruntime.InvocationKindSupervisor {
			result.Proposal = &jobruntime.SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "done"}
		}
		return result, nil
	})
	runtimeRunner, err := jobruntime.NewRunner(store, invoker, jobruntime.RunnerOptions{
		ServerAuthority: authority,
		Now:             func() time.Time { return time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewRunner(): %v", err)
	}
	manager := newTestManager(t, store, runtimeRunner)
	if _, err := manager.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if _, err := manager.Start(context.Background(), spec.ID); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	waitClosed(t, entered, "runtime invocation")
	pauseResult := make(chan struct {
		view View
		err  error
	}, 1)
	go func() {
		view, err := manager.Pause(context.Background(), spec.ID)
		pauseResult <- struct {
			view View
			err  error
		}{view, err}
	}()
	select {
	case result := <-pauseResult:
		t.Fatalf("runtime Pause returned before invocation drained: %+v", result)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	paused := waitValue(t, pauseResult, "runtime durable pause")
	if paused.err != nil {
		t.Fatalf("Pause(): %v", paused.err)
	}
	if paused.view.State.Status != jobs.JobStatusPaused || paused.view.State.CurrentBatch != nil || len(paused.view.State.CompletedBatches) != 1 {
		t.Fatalf("paused state = %+v", paused.view.State)
	}
	if _, err := manager.Resume(context.Background(), spec.ID); err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	waitInactive(t, manager, spec.ID)
	final, err := store.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != jobs.JobStatusCompleted || final.TerminalReason != jobs.TerminalReasonSuccess {
		t.Fatalf("final state = %+v", final)
	}
}

func TestManagerPauseCallerCancellationDoesNotRetractIntent(t *testing.T) {
	store := newMemoryStore()
	state := createMemoryJob(t, store, "pause-client-cancel", jobs.JobStatusRunning)
	state.CurrentBatch = &jobs.WorkBatch{ID: "batch-client", StageID: state.Spec.Stages[0].ID, Cycle: 1, Barrier: jobs.BarrierAll, Items: []jobs.WorkItem{{
		ID: "item-client", RoleID: state.Spec.Stages[0].RoleIDs[0], Objective: "drain after client leaves", Authority: jobs.DenyAllAuthority(),
	}}}
	store.set(state)
	entered := make(chan struct{})
	release := make(chan struct{})
	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		close(entered)
		<-release
		return store.mutate(t, jobID, func(state *jobs.JobState) { state.CurrentBatch = nil }), true, nil
	}
	manager := newTestManager(t, store, runner)
	if _, err := manager.Start(context.Background(), state.Spec.ID); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "pause client batch")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if view, err := manager.Pause(ctx, state.Spec.ID); !errors.Is(err, context.Canceled) || !view.Active {
		t.Fatalf("cancelled Pause view=%+v err=%v", view, err)
	}
	close(release)
	waitStatus(t, store, state.Spec.ID, jobs.JobStatusPaused)
	waitInactive(t, manager, state.Spec.ID)
	if runner.pauseCalls.Load() != 1 {
		t.Fatalf("RequestPause calls = %d, want 1", runner.pauseCalls.Load())
	}
}

func TestManagerTerminalCleanupAndRedactedLastError(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "terminal-cleanup", jobs.JobStatusRunning)
	createMemoryJob(t, store, "runtime-error", jobs.JobStatusRunning)
	runner := &scriptedRunner{store: store}
	runner.step = func(_ context.Context, jobID string) (jobs.JobState, bool, error) {
		state, _ := store.Load(context.Background(), jobID)
		if jobID == "runtime-error" {
			return state, false, errors.New("transport api_key=super-secret-value failed")
		}
		return store.mutate(t, jobID, func(state *jobs.JobState) {
			state.Status = jobs.JobStatusCompleted
			state.TerminalReason = jobs.TerminalReasonSuccess
		}), true, nil
	}
	manager := newTestManager(t, store, runner)
	if _, err := manager.Start(context.Background(), "terminal-cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), "runtime-error"); err != nil {
		t.Fatal(err)
	}
	waitInactive(t, manager, "terminal-cleanup")
	waitInactive(t, manager, "runtime-error")
	terminal, _ := manager.Get(context.Background(), "terminal-cleanup")
	if terminal.Active || terminal.State.Status != jobs.JobStatusCompleted || terminal.LastError != "" {
		t.Fatalf("terminal view = %+v", terminal)
	}
	errorView, _ := manager.Get(context.Background(), "runtime-error")
	if errorView.Active || errorView.LastError == "" || contains(errorView.LastError, "super-secret-value") {
		t.Fatalf("error view = %+v", errorView)
	}
	summaries, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, summary := range summaries {
		if summary.Job.ID == "runtime-error" {
			found = true
			if summary.Active || summary.LastError != errorView.LastError {
				t.Fatalf("error summary = %+v", summary)
			}
		}
	}
	if !found {
		t.Fatal("runtime-error summary missing")
	}
}

func TestManagerConcurrentViewsAndControls(t *testing.T) {
	store := newMemoryStore()
	createMemoryJob(t, store, "race-views", jobs.JobStatusRunning)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{store: store}
	runner.step = func(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return store.mutate(t, jobID, func(state *jobs.JobState) {
				state.Status = jobs.JobStatusCompleted
				state.TerminalReason = jobs.TerminalReasonSuccess
			}), true, nil
		case <-ctx.Done():
			state, _ := store.Load(context.Background(), jobID)
			return state, false, ctx.Err()
		}
	}
	manager := newTestManager(t, store, runner)
	if _, err := manager.Start(context.Background(), "race-views"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "race Step")

	const readers = 96
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := range readers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			switch index % 5 {
			case 0:
				_, err := manager.Start(context.Background(), "race-views")
				errs <- err
			case 1:
				_, err := manager.Get(context.Background(), "race-views")
				errs <- err
			case 2:
				_, err := manager.List(context.Background())
				errs <- err
			case 3:
				_ = manager.Active("race-views")
				errs <- nil
			default:
				_ = manager.LastError("race-views")
				errs <- nil
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if runner.stepCount("race-views") != 1 {
		t.Fatalf("Step calls = %d, want 1", runner.stepCount("race-views"))
	}
	close(release)
	waitInactive(t, manager, "race-views")
}

type scriptedRunner struct {
	store  *memoryStore
	step   func(context.Context, string) (jobs.JobState, bool, error)
	expire func(context.Context, string) (jobs.JobState, bool, error)

	retryable func(error) bool

	cancel func(context.Context, string) (jobs.JobState, error)
	pause  func(context.Context, string) (jobs.JobState, error)
	resume func(context.Context, string) (jobs.JobState, error)

	mu            sync.Mutex
	steps         map[string]int
	concurrent    atomic.Int64
	maxConcurrent atomic.Int64
	cancelCalls   atomic.Int64
	pauseCalls    atomic.Int64
	resumeCalls   atomic.Int64
	expireCalls   atomic.Int64
}

func (r *scriptedRunner) RetryableStepError(err error) bool {
	return r.retryable != nil && r.retryable(err)
}

type invokeFunc func(context.Context, jobruntime.Invocation) (jobruntime.InvocationResult, error)

func (f invokeFunc) Invoke(ctx context.Context, invocation jobruntime.Invocation) (jobruntime.InvocationResult, error) {
	return f(ctx, invocation)
}

func (r *scriptedRunner) Step(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
	r.mu.Lock()
	if r.steps == nil {
		r.steps = make(map[string]int)
	}
	r.steps[jobID]++
	r.mu.Unlock()
	current := r.concurrent.Add(1)
	for {
		maximum := r.maxConcurrent.Load()
		if current <= maximum || r.maxConcurrent.CompareAndSwap(maximum, current) {
			break
		}
	}
	defer r.concurrent.Add(-1)
	if r.step != nil {
		return r.step(ctx, jobID)
	}
	state, err := r.store.Load(ctx, jobID)
	return state, false, err
}

func (r *scriptedRunner) ExpireDue(ctx context.Context, jobID string) (jobs.JobState, bool, error) {
	r.expireCalls.Add(1)
	if r.expire != nil {
		return r.expire(ctx, jobID)
	}
	state, err := r.store.Load(ctx, jobID)
	return state, false, err
}

func (r *scriptedRunner) RequestCancel(ctx context.Context, jobID string) (jobs.JobState, error) {
	r.cancelCalls.Add(1)
	if r.cancel != nil {
		return r.cancel(ctx, jobID)
	}
	return r.store.mutate(nil, jobID, func(state *jobs.JobState) {
		state.Status = jobs.JobStatusCancelled
		state.TerminalReason = jobs.TerminalReasonOperatorCancellation
		state.CancelRequested = true
		state.NextWakeAt = time.Time{}
		state.WaitingReason = ""
	}), nil
}

func (r *scriptedRunner) RequestPause(ctx context.Context, jobID string) (jobs.JobState, error) {
	r.pauseCalls.Add(1)
	if r.pause != nil {
		return r.pause(ctx, jobID)
	}
	if err := ctx.Err(); err != nil {
		return jobs.JobState{}, err
	}
	state, err := r.store.Load(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if state.CurrentBatch != nil {
		return state, errors.New("batch is active")
	}
	return r.store.mutate(nil, jobID, func(state *jobs.JobState) { state.Status = jobs.JobStatusPaused }), nil
}

func (r *scriptedRunner) RequestResume(ctx context.Context, jobID string) (jobs.JobState, error) {
	r.resumeCalls.Add(1)
	if r.resume != nil {
		return r.resume(ctx, jobID)
	}
	if err := ctx.Err(); err != nil {
		return jobs.JobState{}, err
	}
	return r.store.mutate(nil, jobID, func(state *jobs.JobState) {
		state.Status = jobs.JobStatusRunning
		state.NextWakeAt = time.Time{}
		state.WaitingReason = ""
	}), nil
}

func (r *scriptedRunner) stepCount(jobID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.steps[jobID]
}

type memoryStore struct {
	mu          sync.Mutex
	states      map[string]jobs.JobState
	quarantined []jobstore.JobSummary
}

func newMemoryStore() *memoryStore {
	return &memoryStore{states: make(map[string]jobs.JobState)}
}

func (s *memoryStore) CoordinationKey() string {
	return fmt.Sprintf("memory:%p", s)
}

func (s *memoryStore) ProtectedRoots() []string { return []string{"/memory-job-store"} }

func (s *memoryStore) Create(ctx context.Context, spec jobs.JobSpec) (jobs.JobState, error) {
	if err := ctx.Err(); err != nil {
		return jobs.JobState{}, err
	}
	if err := spec.Validate(); err != nil {
		return jobs.JobState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[spec.ID]; ok {
		return jobs.JobState{}, jobstore.ErrAlreadyExists
	}
	state := jobs.JobState{Spec: spec, Status: jobs.JobStatusQueued}
	s.states[spec.ID] = cloneState(state)
	return cloneState(state), nil
}

func (s *memoryStore) Append(ctx context.Context, jobID string, revision uint64, event jobs.Event) (jobs.JobState, error) {
	if err := ctx.Err(); err != nil {
		return jobs.JobState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[jobID]
	if !ok {
		return jobs.JobState{}, jobstore.ErrNotFound
	}
	if state.Revision != revision {
		return cloneState(state), jobstore.ErrConflict
	}
	next, err := jobs.Reduce(state, event)
	if err != nil {
		return cloneState(state), err
	}
	s.states[jobID] = cloneState(next)
	return cloneState(next), nil
}

func (s *memoryStore) Load(ctx context.Context, jobID string) (jobs.JobState, error) {
	if err := ctx.Err(); err != nil {
		return jobs.JobState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[jobID]
	if !ok {
		return jobs.JobState{}, jobstore.ErrNotFound
	}
	return cloneState(state), nil
}

func (s *memoryStore) List(ctx context.Context) ([]jobstore.JobSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jobstore.JobSummary, 0, len(s.states))
	for _, state := range s.states {
		out = append(out, jobstore.JobSummary{
			ID: state.Spec.ID, Goal: state.Spec.Goal, Preset: state.Spec.Preset, Status: state.Status,
			TerminalReason: state.TerminalReason, Revision: state.Revision, Cycle: state.Cycle,
			Usage: state.Usage, AdmittedAt: state.Spec.AdmittedAt, Deadline: state.Spec.Deadline,
		})
	}
	out = append(out, s.quarantined...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (*memoryStore) PutArtifact(context.Context, string, string, string, string, io.Reader) (jobs.ArtifactRef, error) {
	return jobs.ArtifactRef{}, errors.New("artifacts are not implemented by memoryStore")
}

func (*memoryStore) OpenArtifact(context.Context, string, string) (io.ReadCloser, jobs.ArtifactRef, error) {
	return nil, jobs.ArtifactRef{}, errors.New("artifacts are not implemented by memoryStore")
}

func (*memoryStore) Close() error { return nil }

func (s *memoryStore) set(state jobs.JobState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state.Spec.ID] = cloneState(state)
}

func (s *memoryStore) mutate(t *testing.T, jobID string, mutate func(*jobs.JobState)) jobs.JobState {
	if t != nil {
		t.Helper()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[jobID]
	if !ok {
		if t != nil {
			t.Fatalf("missing memory job %q", jobID)
		}
		panic(fmt.Sprintf("missing memory job %q", jobID))
	}
	state = cloneState(state)
	mutate(&state)
	s.states[jobID] = cloneState(state)
	return cloneState(state)
}

func createMemoryJob(t *testing.T, store *memoryStore, id string, status jobs.JobStatus) jobs.JobState {
	t.Helper()
	state, err := store.Create(context.Background(), testSpec(t, id))
	if err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
	state.Status = status
	if status.IsTerminal() {
		state.TerminalReason = jobs.TerminalReasonSuccess
	}
	store.set(state)
	return state
}

func testSpec(t *testing.T, id string) jobs.JobSpec {
	t.Helper()
	workflow, err := jobs.CompilePreset(jobs.PresetGeneral, 1)
	if err != nil {
		t.Fatal(err)
	}
	return jobs.JobSpec{
		ID: id, Goal: "exercise durable service scheduling", Preset: workflow.Name, Workers: workflow.Workers,
		Deadline: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
		Budget:   jobs.Budget{MaxCycles: 4, MaxAttempts: 32, MaxModelCalls: 64, MaxTokens: 100_000},
		Route:    jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview"},
		Workflow: jobs.WorkflowControlFromWorkflow(workflow), Authority: jobs.DenyAllAuthority(),
		Roles: workflow.Roles, Stages: workflow.Stages,
	}
}

func cloneState(state jobs.JobState) jobs.JobState {
	body, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	var out jobs.JobState
	if err := json.Unmarshal(body, &out); err != nil {
		panic(err)
	}
	return out
}

func newTestManager(t *testing.T, store jobstore.Store, runner StepRunner) *Manager {
	t.Helper()
	manager, err := New(store, runner)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown(): %v", err)
		}
	})
	return manager
}

func waitInactive(t *testing.T, manager *Manager, jobID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for manager.Active(jobID) {
		if time.Now().After(deadline) {
			t.Fatalf("job %s remained active", jobID)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitStatus(t *testing.T, store *memoryStore, jobID string, want jobs.JobStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, err := store.Load(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s status = %s, want %s", jobID, state.Status, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitClosed(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitValue[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
