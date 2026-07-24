package jobstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestSpecEnvelopeHashBindsImmutableSpec(t *testing.T) {
	t.Parallel()

	envelope, err := NewSpecEnvelope(contractTestSpec(t))
	if err != nil {
		t.Fatalf("NewSpecEnvelope(): %v", err)
	}
	if envelope.SchemaVersion != SchemaVersion || envelope.JobID != envelope.Spec.ID {
		t.Fatalf("envelope identity = %#v", envelope)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}

	recomputed, err := ComputeSpecHash(envelope)
	if err != nil {
		t.Fatalf("ComputeSpecHash(): %v", err)
	}
	if recomputed != envelope.SpecHash {
		t.Fatalf("recomputed spec hash = %q, want %q", recomputed, envelope.SpecHash)
	}
	copyWithIrrelevantHash := envelope
	copyWithIrrelevantHash.SpecHash = strings.Repeat("f", 64)
	if got, err := ComputeSpecHash(copyWithIrrelevantHash); err != nil || got != envelope.SpecHash {
		t.Fatalf("spec hash included SpecHash field: got %q err %v", got, err)
	}

	tampered := envelope
	tampered.Spec.Goal = "silently changed goal"
	if err := VerifySpecHash(tampered); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("VerifySpecHash(tampered) error = %v", err)
	}
}

func TestSchemaVersionOneIsRejectedExplicitly(t *testing.T) {
	t.Parallel()

	spec, err := NewSpecEnvelope(contractTestSpec(t))
	if err != nil {
		t.Fatalf("NewSpecEnvelope(): %v", err)
	}
	legacySpec := spec
	legacySpec.SchemaVersion = 1
	if err := legacySpec.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported schema_version 1") {
		t.Fatalf("legacy spec error = %v", err)
	}

	event, err := NewEventRecord(spec.JobID, 1, 0, 1, spec.SpecHash, jobs.Event{
		ID: "event-1", Type: jobs.EventJobStarted, At: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NewEventRecord(): %v", err)
	}
	legacyEvent := event
	legacyEvent.SchemaVersion = 1
	if err := legacyEvent.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported schema_version 1") {
		t.Fatalf("legacy event error = %v", err)
	}

	legacySnapshot := SnapshotEnvelope{
		SchemaVersion: 1,
		JobID:         spec.JobID,
		Seq:           0,
		LastHash:      spec.SpecHash,
		State:         jobs.JobState{Spec: spec.Spec, Status: jobs.JobStatusQueued},
	}
	if err := legacySnapshot.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported schema_version 1") {
		t.Fatalf("legacy snapshot error = %v", err)
	}
}

func TestEventRecordCanonicalHashAndRequiredFields(t *testing.T) {
	t.Parallel()

	spec, err := NewSpecEnvelope(contractTestSpec(t))
	if err != nil {
		t.Fatalf("NewSpecEnvelope(): %v", err)
	}
	event := jobs.Event{
		ID:   "event-1",
		Type: jobs.EventJobStarted,
		At:   time.Date(2026, time.July, 23, 12, 1, 0, 0, time.UTC),
	}
	record, err := NewEventRecord(spec.JobID, 1, 0, 1, spec.SpecHash, event)
	if err != nil {
		t.Fatalf("NewEventRecord(): %v", err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("record.Validate(): %v", err)
	}
	if record.PreviousHash != spec.SpecHash {
		t.Fatalf("first previous hash = %q, want spec hash %q", record.PreviousHash, spec.SpecHash)
	}

	recomputed, err := ComputeEventHash(record)
	if err != nil {
		t.Fatalf("ComputeEventHash(): %v", err)
	}
	if recomputed != record.Hash {
		t.Fatalf("recomputed event hash = %q, want %q", recomputed, record.Hash)
	}
	copyWithDifferentHash := record
	copyWithDifferentHash.Hash = strings.Repeat("f", 64)
	if got, err := ComputeEventHash(copyWithDifferentHash); err != nil || got != record.Hash {
		t.Fatalf("event hash included Hash field: got %q err %v", got, err)
	}

	tampered := record
	tampered.Event.ID = "event-2"
	if err := VerifyEventHash(tampered); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("VerifyEventHash(tampered) error = %v", err)
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal(): %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal(): %v", err)
	}
	wantFields := []string{
		"schema_version", "seq", "job_id", "expected_revision", "revision",
		"previous_hash", "hash", "event",
	}
	for _, field := range wantFields {
		if _, exists := fields[field]; !exists {
			t.Errorf("event record JSON missing %q: %s", field, encoded)
		}
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("event record JSON fields = %v, want exactly %v", reflect.ValueOf(fields).MapKeys(), wantFields)
	}
}

func TestEventRecordRejectsNonContiguousRevisionAndMalformedHashes(t *testing.T) {
	t.Parallel()

	event := jobs.Event{ID: "event-1", Type: jobs.EventJobStarted, At: time.Now().UTC()}
	if _, err := NewEventRecord("job-1", 1, 1, 3, strings.Repeat("a", 64), event); err == nil {
		t.Fatal("NewEventRecord() accepted non-contiguous revision")
	}
	if _, err := NewEventRecord("job-1", 2, 0, 1, strings.Repeat("a", 64), event); err == nil {
		t.Fatal("NewEventRecord() accepted sequence/revision mismatch")
	}
	if _, err := NewEventRecord("job-1", 1, 0, 1, "not-a-hash", event); err == nil {
		t.Fatal("NewEventRecord() accepted malformed previous hash")
	}
}

func TestSnapshotEnvelopeValidatesIdentityRevisionAndSpecAnchor(t *testing.T) {
	t.Parallel()

	spec, err := NewSpecEnvelope(contractTestSpec(t))
	if err != nil {
		t.Fatalf("NewSpecEnvelope(): %v", err)
	}
	snapshot := SnapshotEnvelope{
		SchemaVersion: SchemaVersion,
		JobID:         spec.JobID,
		Seq:           0,
		LastHash:      spec.SpecHash,
		State: jobs.JobState{
			Spec:   spec.Spec,
			Status: jobs.JobStatusQueued,
		},
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("snapshot.Validate(): %v", err)
	}

	badRevision := snapshot
	badRevision.Seq = 1
	if err := badRevision.Validate(); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("bad snapshot revision error = %v", err)
	}
	badIdentity := snapshot
	badIdentity.JobID = "other-job"
	if err := badIdentity.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("bad snapshot identity error = %v", err)
	}
}

func contractTestSpec(t *testing.T) jobs.JobSpec {
	t.Helper()
	workflow, err := jobs.CompilePreset(jobs.PresetGeneral, 2)
	if err != nil {
		t.Fatalf("CompilePreset(): %v", err)
	}
	return jobs.JobSpec{
		ID:       "job-1",
		Goal:     "Produce a durable provider-neutral result.",
		Preset:   workflow.Name,
		Workers:  workflow.Workers,
		Deadline: time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		Budget: jobs.Budget{
			MaxCycles: 8, MaxAttempts: 32, MaxModelCalls: 128, MaxTokens: 1_000_000,
		},
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
