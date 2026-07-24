package jobservice

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func TestManagerCreateIdempotentConcurrentDurationAdmissionsHaveOneWinner(t *testing.T) {
	store := newMemoryStore()
	manager := newTestManager(t, store, &scriptedRunner{store: store})
	base := testSpec(t, "job-idempotent-race")
	base.CreateRequestHash = strings.Repeat("a", 64)
	base.AdmittedAt = base.Deadline.Add(-2 * time.Hour)
	base.NotBeforeComplete = base.Deadline.Add(-time.Hour)
	base.CycleCadenceSeconds = 60

	const callers = 64
	var wait sync.WaitGroup
	errorsByCaller := make(chan error, callers)
	views := make(chan View, callers)
	for index := range callers {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			candidate := base
			// Identical duration requests reach the server at distinct admission
			// instants and therefore compile to slightly different absolute floors.
			candidate.Deadline = candidate.Deadline.Add(time.Duration(offset) * time.Millisecond)
			candidate.NotBeforeComplete = candidate.NotBeforeComplete.Add(time.Duration(offset) * time.Millisecond)
			candidate.AdmittedAt = candidate.AdmittedAt.Add(time.Duration(offset) * time.Millisecond)
			view, err := manager.CreateIdempotent(context.Background(), candidate)
			errorsByCaller <- err
			views <- view
		}(index)
	}
	wait.Wait()
	close(errorsByCaller)
	close(views)
	for err := range errorsByCaller {
		if err != nil {
			t.Fatalf("CreateIdempotent() concurrent error = %v", err)
		}
	}
	for view := range views {
		if view.State.Spec.ID != base.ID || view.State.Spec.CreateRequestHash != base.CreateRequestHash || view.State.Spec.AdmittedAt.IsZero() {
			t.Fatalf("CreateIdempotent() view = %#v", view)
		}
	}
	summaries, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].ID != base.ID {
		t.Fatalf("durable jobs = %#v, want exactly %q", summaries, base.ID)
	}
}

func TestManagerCreateIdempotentRejectsMismatchesAndKeepsCreateStrict(t *testing.T) {
	store := newMemoryStore()
	manager := newTestManager(t, store, &scriptedRunner{store: store})
	spec := testSpec(t, "job-idempotent-conflict")
	spec.CreateRequestHash = strings.Repeat("b", 64)
	if _, err := manager.CreateIdempotent(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	sameHashDifferentSpec := spec
	sameHashDifferentSpec.Goal = "a different request cannot forge equivalence with the same hash field"
	if _, err := manager.CreateIdempotent(context.Background(), sameHashDifferentSpec); !errors.Is(err, ErrCreateConflict) || !errors.Is(err, jobstore.ErrAlreadyExists) {
		t.Fatalf("same-hash mismatch error = %v, want create conflict/already exists", err)
	}
	differentHash := spec
	differentHash.CreateRequestHash = strings.Repeat("c", 64)
	if _, err := manager.CreateIdempotent(context.Background(), differentHash); !errors.Is(err, ErrCreateConflict) {
		t.Fatalf("different-hash mismatch error = %v, want ErrCreateConflict", err)
	}
	if _, err := manager.Create(context.Background(), spec); !errors.Is(err, jobstore.ErrAlreadyExists) {
		t.Fatalf("strict Create() duplicate error = %v, want ErrAlreadyExists", err)
	}

	loaded, err := manager.Get(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State.Spec.Goal != spec.Goal || loaded.State.Status != jobs.JobStatusQueued {
		t.Fatalf("conflicting retry mutated durable winner: %#v", loaded.State)
	}
}

func TestManagerCreateIdempotentReconcilesPostCommitDurabilityWarning(t *testing.T) {
	underlying := newMemoryStore()
	store := &createCommitWarningStore{memoryStore: underlying}
	manager := newTestManager(t, store, &scriptedRunner{store: underlying})
	spec := testSpec(t, "job-idempotent-commit-warning")
	spec.CreateRequestHash = strings.Repeat("d", 64)

	view, err := manager.CreateIdempotent(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateIdempotent() durability reconciliation: %v", err)
	}
	if view.State.Spec.ID != spec.ID || view.State.Status != jobs.JobStatusQueued {
		t.Fatalf("reconciled view = %#v", view)
	}
	if _, err := underlying.Create(context.Background(), spec); !errors.Is(err, jobstore.ErrAlreadyExists) {
		t.Fatalf("durable winner missing after warning: %v", err)
	}
}

type createCommitWarningStore struct {
	*memoryStore
}

func (s *createCommitWarningStore) Create(ctx context.Context, spec jobs.JobSpec) (jobs.JobState, error) {
	state, err := s.memoryStore.Create(ctx, spec)
	if err != nil {
		return state, err
	}
	return state, &jobstore.CommitError{
		Operation: "create", JobID: spec.ID, Revision: state.Revision, Err: errors.New("injected root fsync warning"),
	}
}
