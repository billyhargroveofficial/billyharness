package jobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestFileStoreRecoversOnlyUnterminatedFinalTail(t *testing.T) {
	t.Parallel()

	store, root, spec := newRecoveryFileStore(t, Options{}, "tail-job")
	started := recoveryEvent("event-started", jobs.EventJobStarted, 1)
	want, err := store.Append(context.Background(), spec.ID, 0, started)
	if err != nil {
		t.Fatalf("Append(started): %v", err)
	}

	eventsPath := recoveryEventsPath(root, spec.ID)
	canonical, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	partial := []byte(`{"schema_version":1,"seq":2`)
	appendRecoveryBytes(t, eventsPath, partial)

	got, err := store.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Load with recoverable tail: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered state = %#v, want %#v", got, want)
	}
	after, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, canonical) {
		t.Fatalf("recovered log = %q, want exact complete prefix %q", after, canonical)
	}
}

func TestFileStoreCompletedMalformedRecordFailsClosedWithoutTruncation(t *testing.T) {
	t.Parallel()

	store, root, spec := newRecoveryFileStore(t, Options{}, "completed-corrupt-job")
	if _, err := store.Append(
		context.Background(),
		spec.ID,
		0,
		recoveryEvent("event-started", jobs.EventJobStarted, 1),
	); err != nil {
		t.Fatalf("Append(started): %v", err)
	}

	eventsPath := recoveryEventsPath(root, spec.ID)
	appendRecoveryBytes(t, eventsPath, []byte("{]\n"))
	before, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Load(context.Background(), spec.ID)
	assertRecoveryCorruption(t, err, CorruptionMalformedJSON)
	after, readErr := os.ReadFile(eventsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("completed corrupt log was changed:\nbefore %q\nafter  %q", before, after)
	}
}

func TestFileStoreRejectsOnDiskSequenceRevisionHashAndSpecTampering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantKind CorruptionKind
		tamper   func(*testing.T, string, string)
	}{
		{
			name:     "sequence",
			wantKind: CorruptionSequenceGap,
			tamper: func(t *testing.T, root, jobID string) {
				records := readRecoveryRecords(t, recoveryEventsPath(root, jobID))
				records[0].Seq = 2
				rehashRecoveryRecord(t, &records[0])
				writeRecoveryRecords(t, recoveryEventsPath(root, jobID), records)
			},
		},
		{
			name:     "revision",
			wantKind: CorruptionRevisionMismatch,
			tamper: func(t *testing.T, root, jobID string) {
				records := readRecoveryRecords(t, recoveryEventsPath(root, jobID))
				records[0].ExpectedRevision = 1
				records[0].Revision = 2
				rehashRecoveryRecord(t, &records[0])
				writeRecoveryRecords(t, recoveryEventsPath(root, jobID), records)
			},
		},
		{
			name:     "event hash",
			wantKind: CorruptionHashMismatch,
			tamper: func(t *testing.T, root, jobID string) {
				records := readRecoveryRecords(t, recoveryEventsPath(root, jobID))
				records[0].Hash = strings.Repeat("0", 64)
				writeRecoveryRecords(t, recoveryEventsPath(root, jobID), records)
			},
		},
		{
			name:     "previous hash chain",
			wantKind: CorruptionHashMismatch,
			tamper: func(t *testing.T, root, jobID string) {
				records := readRecoveryRecords(t, recoveryEventsPath(root, jobID))
				records[0].PreviousHash = strings.Repeat("0", 64)
				rehashRecoveryRecord(t, &records[0])
				writeRecoveryRecords(t, recoveryEventsPath(root, jobID), records)
			},
		},
		{
			name:     "immutable spec",
			wantKind: CorruptionHashMismatch,
			tamper: func(t *testing.T, root, jobID string) {
				specPath := filepath.Join(root, jobID, specFileName)
				var envelope SpecEnvelope
				readRecoveryJSON(t, specPath, &envelope)
				envelope.Spec.Goal = "tampered goal"
				writeRecoveryJSON(t, specPath, envelope)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			jobID := "tamper-" + strings.ReplaceAll(test.name, " ", "-")
			store, root, spec := newRecoveryFileStore(t, Options{}, jobID)
			if _, err := store.Append(
				context.Background(),
				spec.ID,
				0,
				recoveryEvent("event-started", jobs.EventJobStarted, 1),
			); err != nil {
				t.Fatalf("Append(started): %v", err)
			}
			test.tamper(t, root, spec.ID)
			specPath := filepath.Join(root, spec.ID, specFileName)
			eventsPath := recoveryEventsPath(root, spec.ID)
			beforeSpec := readRecoveryBytes(t, specPath)
			beforeEvents := readRecoveryBytes(t, eventsPath)

			_, err := store.Load(context.Background(), spec.ID)
			assertRecoveryCorruption(t, err, test.wantKind)
			if test.wantKind == CorruptionHashMismatch && !errors.Is(err, ErrTampered) {
				t.Fatalf("tampering error = %v, want errors.Is(ErrTampered)", err)
			}
			if after := readRecoveryBytes(t, specPath); !bytes.Equal(after, beforeSpec) {
				t.Fatal("fail-closed load rewrote tampered spec")
			}
			if after := readRecoveryBytes(t, eventsPath); !bytes.Equal(after, beforeEvents) {
				t.Fatal("fail-closed load rewrote tampered event log")
			}
		})
	}
}

func TestFileStoreRejectsDuplicateEventIDsInCanonicalLog(t *testing.T) {
	t.Parallel()

	store, root, spec := newRecoveryFileStore(t, Options{}, "duplicate-event-job")
	started := recoveryEvent("same-event-id", jobs.EventJobStarted, 1)
	if _, err := store.Append(context.Background(), spec.ID, 0, started); err != nil {
		t.Fatalf("Append(started): %v", err)
	}

	records := readRecoveryRecords(t, recoveryEventsPath(root, spec.ID))
	duplicate := recoveryEvent("same-event-id", jobs.EventJobPaused, 2)
	second, err := NewEventRecord(spec.ID, 2, 1, 2, records[0].Hash, duplicate)
	if err != nil {
		t.Fatalf("NewEventRecord(duplicate): %v", err)
	}
	writeRecoveryRecords(t, recoveryEventsPath(root, spec.ID), append(records, second))
	before := readRecoveryBytes(t, recoveryEventsPath(root, spec.ID))

	_, err = store.Load(context.Background(), spec.ID)
	assertRecoveryCorruption(t, err, CorruptionDuplicateEvent)
	if after := readRecoveryBytes(t, recoveryEventsPath(root, spec.ID)); !bytes.Equal(after, before) {
		t.Fatal("duplicate-event corruption was truncated or rewritten")
	}
}

func TestFileStoreEnforcesEventRecordSizeOnAppendAndReplay(t *testing.T) {
	t.Parallel()

	const maxRecordBytes = 16 << 10
	options := Options{MaxRecordBytes: maxRecordBytes}
	store, root, spec := newRecoveryFileStore(t, options, "record-limit-job")
	oversized := recoveryEvent(strings.Repeat("e", maxRecordBytes*2), jobs.EventJobStarted, 1)

	if _, err := store.Append(context.Background(), spec.ID, 0, oversized); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Append(oversized) error = %v, want ErrTooLarge", err)
	}
	eventsPath := recoveryEventsPath(root, spec.ID)
	body, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("oversized append changed canonical log: %d bytes", len(body))
	}

	completedOversized := []byte(strings.Repeat("x", maxRecordBytes+1) + "\n")
	if err := os.WriteFile(eventsPath, completedOversized, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background(), spec.ID)
	assertRecoveryCorruption(t, err, CorruptionRecordTooLarge)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized replay error = %v, want errors.Is(ErrTooLarge)", err)
	}
	after, readErr := os.ReadFile(eventsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, completedOversized) {
		t.Fatal("completed oversized record was truncated or rewritten")
	}
}

func TestFileStoreRejectsSymlinkAndNonRegularCanonicalPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation generally requires elevated Windows privileges")
	}
	t.Parallel()

	tests := []struct {
		name   string
		tamper func(*testing.T, string, string)
	}{
		{
			name: "symlink event log",
			tamper: func(t *testing.T, root, jobID string) {
				eventsPath := recoveryEventsPath(root, jobID)
				target := filepath.Join(t.TempDir(), "target.jsonl")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(eventsPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, eventsPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory event log",
			tamper: func(t *testing.T, root, jobID string) {
				eventsPath := recoveryEventsPath(root, jobID)
				if err := os.Remove(eventsPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(eventsPath, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink job directory",
			tamper: func(t *testing.T, root, jobID string) {
				jobDir := filepath.Join(root, jobID)
				target := filepath.Join(root, ".real-"+jobID)
				if err := os.Rename(jobDir, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, jobDir); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			jobID := "path-" + strings.ReplaceAll(test.name, " ", "-")
			store, root, spec := newRecoveryFileStore(t, Options{}, jobID)
			test.tamper(t, root, spec.ID)
			if _, err := store.Load(context.Background(), spec.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Load() error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestFileStoreMissingEventLogIsCorruptionAndIsNotRecreated(t *testing.T) {
	t.Parallel()

	store, root, spec := newRecoveryFileStore(t, Options{}, "missing-log-job")
	eventsPath := recoveryEventsPath(root, spec.ID)
	if err := os.Remove(eventsPath); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(context.Background(), spec.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load() error = %v, want ErrCorrupt", err)
	}
	if _, err := os.Lstat(eventsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing canonical log was recreated: %v", err)
	}
}

func TestFileStoreRebuildsMissingStaleAndInvalidSnapshotsFromEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tamper func(*testing.T, string, SpecEnvelope)
	}{
		{
			name: "missing",
			tamper: func(t *testing.T, path string, _ SpecEnvelope) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale",
			tamper: func(t *testing.T, path string, envelope SpecEnvelope) {
				writeRecoveryJSON(t, path, SnapshotEnvelope{
					SchemaVersion: SchemaVersion,
					JobID:         envelope.JobID,
					Seq:           0,
					LastHash:      envelope.SpecHash,
					State: jobs.JobState{
						Spec:   envelope.Spec,
						Status: jobs.JobStatusQueued,
					},
				})
			},
		},
		{
			name: "malformed",
			tamper: func(t *testing.T, path string, _ SpecEnvelope) {
				if err := os.WriteFile(path, []byte("{]\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unsupported version",
			tamper: func(t *testing.T, path string, envelope SpecEnvelope) {
				writeRecoveryJSON(t, path, SnapshotEnvelope{
					SchemaVersion: SchemaVersion + 1,
					JobID:         envelope.JobID,
					Seq:           0,
					LastHash:      envelope.SpecHash,
					State: jobs.JobState{
						Spec:   envelope.Spec,
						Status: jobs.JobStatusQueued,
					},
				})
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			jobID := "snapshot-" + strings.ReplaceAll(test.name, " ", "-")
			store, root, spec := newRecoveryFileStore(t, Options{}, jobID)
			want, err := store.Append(
				context.Background(),
				spec.ID,
				0,
				recoveryEvent("event-started", jobs.EventJobStarted, 1),
			)
			if err != nil {
				t.Fatalf("Append(started): %v", err)
			}
			eventsPath := recoveryEventsPath(root, spec.ID)
			canonicalEvents, err := os.ReadFile(eventsPath)
			if err != nil {
				t.Fatal(err)
			}
			var envelope SpecEnvelope
			readRecoveryJSON(t, filepath.Join(root, spec.ID, specFileName), &envelope)
			snapshotPath := filepath.Join(root, spec.ID, snapshotFileName)
			test.tamper(t, snapshotPath, envelope)

			got, err := store.Load(context.Background(), spec.ID)
			if err != nil {
				t.Fatalf("Load after %s snapshot: %v", test.name, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("state after snapshot rebuild = %#v, want %#v", got, want)
			}
			records := readRecoveryRecords(t, eventsPath)
			if len(records) != 1 {
				t.Fatalf("event records after snapshot rebuild = %d, want 1", len(records))
			}
			var rebuilt SnapshotEnvelope
			readRecoveryJSON(t, snapshotPath, &rebuilt)
			wantSnapshot := SnapshotEnvelope{
				SchemaVersion: SchemaVersion,
				JobID:         spec.ID,
				Seq:           1,
				LastHash:      records[0].Hash,
				State:         want,
			}
			if !recoveryJSONEqual(t, rebuilt, wantSnapshot) {
				t.Fatalf("rebuilt snapshot = %#v, want %#v", rebuilt, wantSnapshot)
			}
			afterEvents, err := os.ReadFile(eventsPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(afterEvents, canonicalEvents) {
				t.Fatal("snapshot rebuild changed canonical events")
			}
		})
	}
}

func TestFileStoreReplaysAfterCloseAndReopenAndCanContinue(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	spec := recoverySpec(t, "reopen-job")
	first, err := NewFileStore(root, Options{})
	if err != nil {
		t.Fatalf("NewFileStore(first): %v", err)
	}
	if _, err := first.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := first.Append(
		context.Background(),
		spec.ID,
		0,
		recoveryEvent("event-started", jobs.EventJobStarted, 1),
	); err != nil {
		t.Fatalf("Append(started): %v", err)
	}
	wantPaused, err := first.Append(
		context.Background(),
		spec.ID,
		1,
		recoveryEvent("event-paused", jobs.EventJobPaused, 2),
	)
	if err != nil {
		t.Fatalf("Append(paused): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	second, err := NewFileStore(root, Options{})
	if err != nil {
		t.Fatalf("NewFileStore(second): %v", err)
	}
	gotPaused, err := second.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Load(second): %v", err)
	}
	if !reflect.DeepEqual(gotPaused, wantPaused) {
		t.Fatalf("reopened state = %#v, want %#v", gotPaused, wantPaused)
	}
	wantResumed, err := second.Append(
		context.Background(),
		spec.ID,
		gotPaused.Revision,
		recoveryEvent("event-resumed", jobs.EventJobResumed, 3),
	)
	if err != nil {
		t.Fatalf("Append(resumed): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close(second): %v", err)
	}

	third, err := NewFileStore(root, Options{})
	if err != nil {
		t.Fatalf("NewFileStore(third): %v", err)
	}
	t.Cleanup(func() { _ = third.Close() })
	gotResumed, err := third.Load(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("Load(third): %v", err)
	}
	if !reflect.DeepEqual(gotResumed, wantResumed) {
		t.Fatalf("twice-reopened state = %#v, want %#v", gotResumed, wantResumed)
	}
	if gotResumed.Status != jobs.JobStatusRunning || gotResumed.Revision != 3 {
		t.Fatalf("continued state = status %q revision %d", gotResumed.Status, gotResumed.Revision)
	}
	if records := readRecoveryRecords(t, recoveryEventsPath(root, spec.ID)); len(records) != 3 {
		t.Fatalf("event record count = %d, want 3", len(records))
	}
}

func newRecoveryFileStore(
	t *testing.T,
	options Options,
	jobID string,
) (*FileStore, string, jobs.JobSpec) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "jobs")
	store, err := NewFileStore(root, options)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec := recoverySpec(t, jobID)
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store, root, spec
}

func recoverySpec(t *testing.T, jobID string) jobs.JobSpec {
	t.Helper()
	workflow, err := jobs.CompilePreset(jobs.PresetGeneral, 2)
	if err != nil {
		t.Fatalf("CompilePreset: %v", err)
	}
	return jobs.JobSpec{
		ID:       jobID,
		Goal:     "Exercise durable crash recovery and fail-closed replay.",
		Preset:   workflow.Name,
		Workers:  workflow.Workers,
		Deadline: time.Date(2026, time.July, 24, 18, 0, 0, 0, time.UTC),
		Budget: jobs.Budget{
			MaxCycles: 8, MaxAttempts: 32, MaxModelCalls: 128, MaxTokens: 1_000_000,
		},
		Authority: jobs.DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}
}

func recoveryEvent(id string, eventType jobs.EventType, minute int) jobs.Event {
	return jobs.Event{
		ID:   id,
		Type: eventType,
		At:   time.Date(2026, time.July, 24, 10, minute, 0, 0, time.UTC),
	}
}

func recoveryEventsPath(root, jobID string) string {
	return filepath.Join(root, jobID, eventsFileName)
}

func appendRecoveryBytes(t *testing.T, path string, body []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readRecoveryRecords(t *testing.T, path string) []EventRecord {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	if len(lines) == 1 && len(lines[0]) == 0 {
		return nil
	}
	records := make([]EventRecord, len(lines))
	for index, line := range lines {
		if err := json.Unmarshal(line, &records[index]); err != nil {
			t.Fatalf("decode event record %d: %v", index+1, err)
		}
	}
	return records
}

func writeRecoveryRecords(t *testing.T, path string, records []EventRecord) {
	t.Helper()
	var body bytes.Buffer
	for index, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode event record %d: %v", index+1, err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rehashRecoveryRecord(t *testing.T, record *EventRecord) {
	t.Helper()
	hash, err := ComputeEventHash(*record)
	if err != nil {
		t.Fatal(err)
	}
	record.Hash = hash
}

func readRecoveryJSON(t *testing.T, path string, destination any) {
	t.Helper()
	body := readRecoveryBytes(t, path)
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func readRecoveryBytes(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeRecoveryJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func recoveryJSONEqual(t *testing.T, left, right any) bool {
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

func assertRecoveryCorruption(t *testing.T, err error, want CorruptionKind) {
	t.Helper()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error = %v, want errors.Is(ErrCorrupt)", err)
	}
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("error type = %T, want *CorruptionError", err)
	}
	if corruption.Kind != want {
		t.Fatalf("corruption kind = %q, want %q: %v", corruption.Kind, want, err)
	}
}
