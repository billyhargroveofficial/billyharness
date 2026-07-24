package jobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestFileStoreCreateLoadAndListStableOrder(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, store)

	createdByID := make(map[string]jobs.JobState)
	for _, jobID := range []string{"job-z", "job-a", "job-m"} {
		spec := fileStoreTestSpec(t, jobID)
		created, err := store.Create(context.Background(), spec)
		if err != nil {
			t.Fatalf("Create(%q): %v", jobID, err)
		}
		if created.Status != jobs.JobStatusQueued || created.Revision != 0 || created.Spec.ID != jobID {
			t.Fatalf("Create(%q) state = %#v", jobID, created)
		}
		createdByID[jobID] = created

		loaded, err := store.Load(context.Background(), jobID)
		if err != nil {
			t.Fatalf("Load(%q): %v", jobID, err)
		}
		if !reflect.DeepEqual(loaded, created) {
			t.Fatalf("Load(%q) = %#v, want %#v", jobID, loaded, created)
		}
	}

	summaries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	wantOrder := []string{"job-a", "job-m", "job-z"}
	if len(summaries) != len(wantOrder) {
		t.Fatalf("List() returned %d summaries, want %d: %#v", len(summaries), len(wantOrder), summaries)
	}
	for index, wantID := range wantOrder {
		got := summaries[index]
		created := createdByID[wantID]
		if got.ID != wantID ||
			got.Goal != created.Spec.Goal ||
			got.Preset != created.Spec.Preset ||
			got.Status != jobs.JobStatusQueued ||
			got.Revision != 0 ||
			got.Deadline != created.Spec.Deadline {
			t.Fatalf("summary %d = %#v, want queued summary for %q", index, got, wantID)
		}
	}
}

func TestCloneStateForStoreDeepCopiesMutableDomainState(t *testing.T) {
	t.Parallel()

	spec := fileStoreTestSpec(t, "job-clone-state")
	batch := jobs.WorkBatch{
		ID: "batch-next", Cycle: 2, StageID: spec.Workflow.StageOrder[0], Barrier: jobs.BarrierAll,
		Items: []jobs.WorkItem{{
			ID: "work-next", RoleID: spec.Workflow.WorkerRoleIDs[0], Objective: "continue",
			Authority: jobs.Authority{Mode: jobs.AuthorityModeAllowList, Tools: []string{"fs_read_file"}, Providers: []string{"qwen"}},
		}},
	}
	decision := &jobs.Decision{Kind: jobs.DecisionContinue, Reason: "continue", Fingerprint: "fingerprint", NextBatch: &batch}
	state := jobs.JobState{
		Spec:             spec,
		Status:           jobs.JobStatusRunning,
		CompletedBatches: []jobs.CompletedBatch{{ID: "batch-done", StageID: spec.Workflow.StageOrder[0], Cycle: 1}},
		Attempts: []jobs.Attempt{{
			ID: "attempt-one", BatchID: "batch-done", WorkItemID: "work-one", RoleID: spec.Workflow.WorkerRoleIDs[0],
			AttemptNo: 1, Cycle: 1, StageID: spec.Workflow.StageOrder[0], Status: jobs.AttemptStatusSucceeded,
			Decision: decision,
		}},
		LastDecision: decision,
	}
	cloned := cloneStateForStore(state)

	cloned.Spec.Workflow.StageOrder[0] = "mutated-stage"
	cloned.Spec.Workflow.WorkerRoleIDs[0] = "mutated-role"
	cloned.CompletedBatches[0].ID = "mutated-batch"
	cloned.Attempts[0].Decision.NextBatch.Items[0].Authority.Tools[0] = "mutated-tool"
	cloned.LastDecision.NextBatch.Items[0].Objective = "mutated-objective"

	if state.Spec.Workflow.StageOrder[0] == "mutated-stage" ||
		state.Spec.Workflow.WorkerRoleIDs[0] == "mutated-role" ||
		state.CompletedBatches[0].ID == "mutated-batch" ||
		state.Attempts[0].Decision.NextBatch.Items[0].Authority.Tools[0] == "mutated-tool" ||
		state.LastDecision.NextBatch.Items[0].Objective == "mutated-objective" {
		t.Fatalf("clone mutation escaped into source state: %#v", state)
	}
}

func TestFileStoreCreateRejectsDuplicateAndPreservesOriginal(t *testing.T) {
	t.Parallel()

	store := openFileStoreForTest(t, filepath.Join(t.TempDir(), "jobs"), Options{})
	defer closeFileStoreForTest(t, store)

	original := fileStoreTestSpec(t, "job-duplicate")
	created, err := store.Create(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	replacement := original
	replacement.Goal = "This replacement must never overwrite the original spec."
	if _, err := store.Create(context.Background(), replacement); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Create() error = %v, want ErrAlreadyExists", err)
	}
	loaded, err := store.Load(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, created) {
		t.Fatalf("duplicate create changed state: got %#v, want %#v", loaded, created)
	}
}

func TestFileStoreAppendCASAndExactLastEventRetry(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, store)
	spec := fileStoreTestSpec(t, "job-cas")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	event := jobs.Event{
		ID:   "event-start",
		Type: jobs.EventJobStarted,
		At:   fileStoreTestTime(),
	}
	committed, err := store.Append(context.Background(), spec.ID, 0, event)
	if err != nil {
		t.Fatalf("first Append(): %v", err)
	}
	if committed.Revision != 1 || committed.Status != jobs.JobStatusRunning || committed.LastEventID != event.ID {
		t.Fatalf("committed state = %#v", committed)
	}

	retried, err := store.Append(context.Background(), spec.ID, 0, event)
	if err != nil {
		t.Fatalf("exact retry at original expected revision: %v", err)
	}
	if !reflect.DeepEqual(retried, committed) {
		t.Fatalf("exact retry state = %#v, want %#v", retried, committed)
	}
	assertEventLogLineCount(t, root, spec.ID, 1)

	for _, test := range []struct {
		name             string
		expectedRevision uint64
		event            jobs.Event
	}{
		{
			name:             "same operation at current revision",
			expectedRevision: 1,
			event:            event,
		},
		{
			name:             "same ID different payload at original revision",
			expectedRevision: 0,
			event: jobs.Event{
				ID: "event-start", Type: jobs.EventJobStarted, At: event.At.Add(time.Second),
			},
		},
		{
			name:             "same ID different payload at current revision",
			expectedRevision: 1,
			event: jobs.Event{
				ID: "event-start", Type: jobs.EventJobPaused, At: event.At.Add(time.Second),
			},
		},
		{
			name:             "new event at stale revision",
			expectedRevision: 0,
			event: jobs.Event{
				ID: "event-pause", Type: jobs.EventJobPaused, At: event.At.Add(time.Second),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.Append(context.Background(), spec.ID, test.expectedRevision, test.event)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Append() error = %v, want ErrConflict", err)
			}
			var conflict *ConflictError
			if !errors.As(err, &conflict) ||
				conflict.JobID != spec.ID ||
				conflict.ExpectedRevision != test.expectedRevision ||
				conflict.ActualRevision != 1 {
				t.Fatalf("conflict metadata = %#v, error %v", conflict, err)
			}
		})
	}
	assertEventLogLineCount(t, root, spec.ID, 1)
}

func TestFileStoreAppendExactEarlierEventRetryAfterLaterEvent(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	spec := fileStoreTestSpec(t, "job-earlier-retry")
	started := jobs.Event{
		ID:   "event-start",
		Type: jobs.EventJobStarted,
		At:   fileStoreTestTime(),
	}
	paused := jobs.Event{
		ID:   "event-pause",
		Type: jobs.EventJobPaused,
		At:   fileStoreTestTime().Add(time.Minute),
	}

	first := openFileStoreForTest(t, root, Options{})
	if _, err := first.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Append(context.Background(), spec.ID, 0, started); err != nil {
		t.Fatal(err)
	}
	want, err := first.Append(context.Background(), spec.ID, 1, paused)
	if err != nil {
		t.Fatal(err)
	}
	closeFileStoreForTest(t, first)

	second := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, second)
	got, err := second.Append(context.Background(), spec.ID, 0, started)
	if err != nil {
		t.Fatalf("exact earlier event retry: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exact earlier retry state = %#v, want current state %#v", got, want)
	}
	assertEventLogLineCount(t, root, spec.ID, 2)

	cancelled, err := second.Append(context.Background(), spec.ID, want.Revision, jobs.Event{
		ID: "event-cancel", Type: jobs.EventJobCancelled, At: fileStoreTestTime().Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = second.Append(context.Background(), spec.ID, 0, started)
	if err != nil {
		t.Fatalf("exact earlier event retry after terminal event: %v", err)
	}
	if !reflect.DeepEqual(got, cancelled) {
		t.Fatalf("exact earlier retry after terminal = %#v, want %#v", got, cancelled)
	}
	assertEventLogLineCount(t, root, spec.ID, 3)

	for _, test := range []struct {
		name             string
		expectedRevision uint64
		event            jobs.Event
	}{
		{
			name:             "same payload wrong original revision",
			expectedRevision: 1,
			event:            started,
		},
		{
			name:             "same ID different payload original revision",
			expectedRevision: 0,
			event: jobs.Event{
				ID: "event-start", Type: jobs.EventJobStarted, At: started.At.Add(time.Nanosecond),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := second.Append(context.Background(), spec.ID, test.expectedRevision, test.event)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Append() error = %v, want ErrConflict", err)
			}
			var conflict *ConflictError
			if !errors.As(err, &conflict) ||
				conflict.ExpectedRevision != test.expectedRevision ||
				conflict.ActualRevision != cancelled.Revision {
				t.Fatalf("conflict = %#v, err = %v", conflict, err)
			}
		})
	}
	assertEventLogLineCount(t, root, spec.ID, 3)
}

func TestFileStoreVerifiedCacheAvoidsReplayPerAppendAndExactRetry(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, store)
	spec := fileStoreTestSpec(t, "job-cache")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	started := jobs.Event{ID: "event-start", Type: jobs.EventJobStarted, At: fileStoreTestTime()}
	running, err := store.Append(context.Background(), spec.ID, 0, started)
	if err != nil {
		t.Fatal(err)
	}
	if store.fullReplayCount != 1 {
		t.Fatalf("full replay count after cache warmup = %d, want 1", store.fullReplayCount)
	}

	for index := 0; index < 64; index++ {
		got, err := store.Append(context.Background(), spec.ID, 0, started)
		if err != nil {
			t.Fatalf("exact retry %d: %v", index, err)
		}
		if !reflect.DeepEqual(got, running) {
			t.Fatalf("exact retry %d state differs", index)
		}
		if _, err := store.Load(context.Background(), spec.ID); err != nil {
			t.Fatalf("cached Load %d: %v", index, err)
		}
	}
	paused, err := store.Append(context.Background(), spec.ID, running.Revision, jobs.Event{
		ID: "event-pause", Type: jobs.EventJobPaused, At: fileStoreTestTime().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Append(context.Background(), spec.ID, 0, started)
	if err != nil {
		t.Fatalf("exact earlier retry: %v", err)
	}
	if !reflect.DeepEqual(got, paused) {
		t.Fatalf("exact earlier retry = %#v, want current state %#v", got, paused)
	}
	if store.fullReplayCount != 1 {
		t.Fatalf("full replay count after cached operations = %d, want 1", store.fullReplayCount)
	}
	assertEventLogLineCount(t, root, spec.ID, 2)
}

func TestFileStoreVerifiedCacheInvalidatesOnCanonicalLogChange(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, store)
	spec := fileStoreTestSpec(t, "job-cache-invalidated")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	started := jobs.Event{ID: "event-start", Type: jobs.EventJobStarted, At: fileStoreTestTime()}
	if _, err := store.Append(context.Background(), spec.ID, 0, started); err != nil {
		t.Fatal(err)
	}
	if store.fullReplayCount != 1 {
		t.Fatalf("warmup replay count = %d, want 1", store.fullReplayCount)
	}
	appendRecoveryBytes(t, filepath.Join(root, spec.ID, eventsFileName), []byte("{]\n"))

	_, err := store.Append(context.Background(), spec.ID, 0, started)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Append after out-of-band log change error = %v, want ErrCorrupt", err)
	}
	if store.fullReplayCount != 2 {
		t.Fatalf("replay count after cache invalidation = %d, want 2", store.fullReplayCount)
	}
}

func TestFileStoreListQuarantinesCorruptJobWithoutHidingHealthyJobs(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, store)
	for _, id := range []string{"healthy-job", "corrupt-job"} {
		if _, err := store.Create(context.Background(), fileStoreTestSpec(t, id)); err != nil {
			t.Fatal(err)
		}
	}
	corruptEvents := filepath.Join(root, "corrupt-job", eventsFileName)
	if err := os.WriteFile(corruptEvents, []byte("{]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List with one corrupt job: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("List returned %d entries, want healthy plus quarantine: %#v", len(summaries), summaries)
	}
	if summaries[0].ID != "corrupt-job" || summaries[0].Quarantine == nil ||
		summaries[0].Quarantine.Kind != CorruptionMalformedJSON {
		t.Fatalf("corrupt summary = %#v", summaries[0])
	}
	if summaries[0].Status != "" || summaries[0].Quarantine.String() == "" {
		t.Fatalf("quarantine was not fail-closed/reportable: %#v", summaries[0])
	}
	if summaries[1].ID != "healthy-job" || summaries[1].Quarantine != nil ||
		summaries[1].Status != jobs.JobStatusQueued {
		t.Fatalf("healthy summary = %#v", summaries[1])
	}
	if _, err := store.Load(context.Background(), "corrupt-job"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("direct Load of quarantined job error = %v, want ErrCorrupt", err)
	}

	// Quarantine is a fail-closed observation rather than a destructive move;
	// an operator can repair the canonical log and the next scan re-admits it.
	if err := os.WriteFile(corruptEvents, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	summaries, err = store.List(context.Background())
	if err != nil {
		t.Fatalf("List after repair: %v", err)
	}
	if summaries[0].ID != "corrupt-job" || summaries[0].Quarantine != nil ||
		summaries[0].Status != jobs.JobStatusQueued {
		t.Fatalf("repaired summary = %#v", summaries[0])
	}
}

func TestFileStoreAppendPersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	spec := fileStoreTestSpec(t, "job-reopen")
	now := fileStoreTestTime()

	first := openFileStoreForTest(t, root, Options{})
	if _, err := first.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	running, err := first.Append(context.Background(), spec.ID, 0, jobs.Event{
		ID: "event-start", Type: jobs.EventJobStarted, At: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	closeFileStoreForTest(t, first)

	second := openFileStoreForTest(t, root, Options{})
	reloaded, err := second.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded, running) {
		t.Fatalf("state after first reopen = %#v, want %#v", reloaded, running)
	}
	paused, err := second.Append(context.Background(), spec.ID, reloaded.Revision, jobs.Event{
		ID: "event-pause", Type: jobs.EventJobPaused, At: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	closeFileStoreForTest(t, second)

	third := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, third)
	reloaded, err = third.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded, paused) || reloaded.Status != jobs.JobStatusPaused || reloaded.Revision != 2 {
		t.Fatalf("state after second reopen = %#v, want paused revision 2 %#v", reloaded, paused)
	}
	assertEventLogLineCount(t, root, spec.ID, 2)
}

func TestFileStoreClosedOperationsFail(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	spec := fileStoreTestSpec(t, "job-closed")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "create",
			run: func() error {
				_, err := store.Create(context.Background(), fileStoreTestSpec(t, "job-after-close"))
				return err
			},
		},
		{
			name: "append",
			run: func() error {
				_, err := store.Append(context.Background(), spec.ID, 0, jobs.Event{
					ID: "event-start", Type: jobs.EventJobStarted, At: fileStoreTestTime(),
				})
				return err
			},
		},
		{
			name: "load",
			run: func() error {
				_, err := store.Load(context.Background(), spec.ID)
				return err
			},
		},
		{
			name: "list",
			run: func() error {
				_, err := store.List(context.Background())
				return err
			},
		},
		{
			name: "put artifact",
			run: func() error {
				_, err := store.PutArtifact(context.Background(), spec.ID, "artifact", "text/plain", "", strings.NewReader("x"))
				return err
			},
		},
		{
			name: "open artifact",
			run: func() error {
				reader, _, err := store.OpenArtifact(context.Background(), spec.ID, "artifact")
				if reader != nil {
					_ = reader.Close()
				}
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrClosed) {
				t.Fatalf("operation error = %v, want ErrClosed", err)
			}
		})
	}
}

func TestFileStoreSecondOwnerRejectedUntilClose(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	first := openFileStoreForTest(t, root, Options{})
	second, err := NewFileStore(root, Options{})
	if second != nil {
		_ = second.Close()
		t.Fatal("second NewFileStore unexpectedly succeeded")
	}
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("second NewFileStore error = %v, want ErrOwnership", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, reopened)
}

func TestFileStoreCreatesPrivateFilesAndDirectories(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{MaxArtifactBytes: 1024})
	defer closeFileStoreForTest(t, store)
	spec := fileStoreTestSpec(t, "job-modes")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutArtifact(
		context.Background(), spec.ID, "report", "text/plain", "attempt-1", strings.NewReader("private"),
	); err != nil {
		t.Fatal(err)
	}

	jobDir := filepath.Join(root, spec.ID)
	artifactDir := filepath.Join(jobDir, artifactsDirectory, "report")
	for _, path := range []string{root, jobDir, filepath.Join(jobDir, artifactsDirectory), artifactDir} {
		assertMode(t, path, 0o700)
	}
	for _, path := range []string{
		filepath.Join(root, ownershipFileName),
		filepath.Join(jobDir, specFileName),
		filepath.Join(jobDir, eventsFileName),
		filepath.Join(jobDir, snapshotFileName),
		filepath.Join(artifactDir, artifactDataName),
		filepath.Join(artifactDir, artifactMetadataName),
	} {
		assertMode(t, path, 0o600)
	}
}

func TestFileStoreRebuildsMissingStaleAndCorruptSnapshots(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, snapshotPath string, initialSnapshot []byte)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, snapshotPath string, _ []byte) {
				t.Helper()
				if err := os.Remove(snapshotPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale",
			mutate: func(t *testing.T, snapshotPath string, initialSnapshot []byte) {
				t.Helper()
				if err := os.WriteFile(snapshotPath, initialSnapshot, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt",
			mutate: func(t *testing.T, snapshotPath string, _ []byte) {
				t.Helper()
				if err := os.WriteFile(snapshotPath, []byte("{not-json\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "jobs")
			store := openFileStoreForTest(t, root, Options{})
			defer closeFileStoreForTest(t, store)
			spec := fileStoreTestSpec(t, "job-snapshot")
			if _, err := store.Create(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
			snapshotPath := filepath.Join(root, spec.ID, snapshotFileName)
			initialSnapshot, err := os.ReadFile(snapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			want, err := store.Append(context.Background(), spec.ID, 0, jobs.Event{
				ID: "event-start", Type: jobs.EventJobStarted, At: fileStoreTestTime(),
			})
			if err != nil {
				t.Fatal(err)
			}
			eventsPath := filepath.Join(root, spec.ID, eventsFileName)
			eventsBefore, err := os.ReadFile(eventsPath)
			if err != nil {
				t.Fatal(err)
			}

			test.mutate(t, snapshotPath, initialSnapshot)
			got, err := store.Load(context.Background(), spec.ID)
			if err != nil {
				t.Fatalf("Load() after %s snapshot: %v", test.name, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Load() state = %#v, want %#v", got, want)
			}
			eventsAfter, err := os.ReadFile(eventsPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(eventsAfter, eventsBefore) {
				t.Fatal("snapshot recovery changed canonical event log")
			}

			var rebuilt SnapshotEnvelope
			if err := readStrictJSONFile(snapshotPath, maxSnapshotBytes, &rebuilt); err != nil {
				t.Fatalf("read rebuilt snapshot: %v", err)
			}
			if err := rebuilt.Validate(); err != nil {
				t.Fatalf("rebuilt snapshot validation: %v", err)
			}
			if rebuilt.Seq != want.Revision || !jsonValuesEqual(t, rebuilt.State, want) {
				t.Fatalf("rebuilt snapshot = %#v, want state %#v", rebuilt, want)
			}
		})
	}
}

func TestFileStoreArtifactsRoundTripAcrossReopen(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	spec := fileStoreTestSpec(t, "job-artifacts")
	body := []byte("durable result through FileStore\n")

	first := openFileStoreForTest(t, root, Options{MaxArtifactBytes: 1024})
	if _, err := first.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ref, err := first.PutArtifact(
		context.Background(), spec.ID, "result", "text/plain; charset=utf-8", "attempt-1", bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("PutArtifact(): %v", err)
	}
	digest := sha256.Sum256(body)
	if ref.ID != "result" ||
		ref.URI != "job://job-artifacts/artifacts/result" ||
		ref.SHA256 != hex.EncodeToString(digest[:]) ||
		ref.CreatedByAttemptID != "attempt-1" {
		t.Fatalf("artifact ref = %#v", ref)
	}
	if _, err := first.PutArtifact(
		context.Background(), spec.ID, "result", "text/plain", "", strings.NewReader("replacement"),
	); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate PutArtifact() error = %v, want ErrAlreadyExists", err)
	}
	closeFileStoreForTest(t, first)

	second := openFileStoreForTest(t, root, Options{MaxArtifactBytes: 1024})
	defer closeFileStoreForTest(t, second)
	reader, reopenedRef, err := second.OpenArtifact(context.Background(), spec.ID, "result")
	if err != nil {
		t.Fatalf("OpenArtifact(): %v", err)
	}
	openedBody, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close artifact: %v / %v", readErr, closeErr)
	}
	if !bytes.Equal(openedBody, body) || reopenedRef != ref {
		t.Fatalf("reopened artifact = %q %#v, want %q %#v", openedBody, reopenedRef, body, ref)
	}
	if _, err := second.PutArtifact(
		context.Background(), "job-missing", "result", "", "", strings.NewReader("x"),
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PutArtifact() missing job error = %v, want ErrNotFound", err)
	}
}

func TestFileStoreConcurrentAppendCASHasExactlyOneWinner(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, store)
	spec := fileStoreTestSpec(t, "job-concurrent")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	const contenders = 12
	type result struct {
		eventID string
		state   jobs.JobState
		err     error
	}
	results := make(chan result, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			eventID := "event-start-" + string(rune('a'+index))
			state, err := store.Append(context.Background(), spec.ID, 0, jobs.Event{
				ID: eventID, Type: jobs.EventJobStarted, At: fileStoreTestTime().Add(time.Duration(index) * time.Nanosecond),
			})
			results <- result{eventID: eventID, state: state, err: err}
		}()
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	winner := ""
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.eventID
			if result.state.Revision != 1 || result.state.LastEventID != result.eventID {
				t.Fatalf("winning state = %#v for %q", result.state, result.eventID)
			}
		case errors.Is(result.err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("Append(%q) error = %v, want nil or ErrConflict", result.eventID, result.err)
		}
	}
	if successes != 1 || conflicts != contenders-1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/%d", successes, conflicts, contenders-1)
	}
	loaded, err := store.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 || loaded.LastEventID != winner || loaded.Status != jobs.JobStatusRunning {
		t.Fatalf("loaded winner state = %#v, winner %q", loaded, winner)
	}
	assertEventLogLineCount(t, root, spec.ID, 1)
}

func fileStoreTestSpec(t *testing.T, jobID string) jobs.JobSpec {
	t.Helper()
	workflow, err := jobs.CompilePreset(jobs.PresetGeneral, 2)
	if err != nil {
		t.Fatalf("CompilePreset(): %v", err)
	}
	return jobs.JobSpec{
		ID:       jobID,
		Goal:     "Produce and verify a durable provider-neutral result.",
		Preset:   workflow.Name,
		Workers:  workflow.Workers,
		Deadline: fileStoreTestTime().Add(24 * time.Hour),
		Budget:   jobs.Budget{MaxCycles: 8, MaxAttempts: 32, MaxModelCalls: 128, MaxTokens: 1_000_000},
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen",
			ModelID:    "qwen3.8-max-preview",
		},
		Workflow:  jobs.WorkflowControlFromWorkflow(workflow),
		Authority: jobs.DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}
}

func fileStoreTestTime() time.Time {
	return time.Date(2026, time.July, 24, 9, 0, 0, 0, time.UTC)
}

func openFileStoreForTest(t *testing.T, root string, options Options) *FileStore {
	t.Helper()
	store, err := NewFileStore(root, options)
	if err != nil {
		t.Fatalf("NewFileStore(%q): %v", root, err)
	}
	return store
}

func closeFileStoreForTest(t *testing.T, store *FileStore) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func assertEventLogLineCount(t *testing.T, root, jobID string, want int) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, jobID, eventsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		t.Fatalf("event log does not end in newline: %q", body)
	}
	if got := bytes.Count(body, []byte{'\n'}); got != want {
		t.Fatalf("event log lines = %d, want %d: %q", got, want, body)
	}
	for lineNumber, line := range bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record EventRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode event log line %d: %v", lineNumber+1, err)
		}
		canonical, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(line, canonical) {
			t.Fatalf("event log line %d is not canonical", lineNumber+1)
		}
	}
}

func jsonValuesEqual(t *testing.T, left, right any) bool {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(leftJSON, rightJSON)
}
