package jobstore

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

// SchemaVersion 2 introduces immutable execution routes/workflow cursors and
// the two-phase attempt lifecycle. Version 1 cannot be resumed safely because
// it did not persist a provider route or pre-dispatch reservations; readers
// reject it explicitly rather than silently selecting current configuration.
const SchemaVersion = 2

type SpecEnvelope struct {
	SchemaVersion int          `json:"schema_version"`
	JobID         string       `json:"job_id"`
	SpecHash      string       `json:"spec_hash"`
	Spec          jobs.JobSpec `json:"spec"`
}

type EventRecord struct {
	SchemaVersion    int        `json:"schema_version"`
	Seq              uint64     `json:"seq"`
	JobID            string     `json:"job_id"`
	ExpectedRevision uint64     `json:"expected_revision"`
	Revision         uint64     `json:"revision"`
	PreviousHash     string     `json:"previous_hash"`
	Hash             string     `json:"hash"`
	Event            jobs.Event `json:"event"`
}

type SnapshotEnvelope struct {
	SchemaVersion int           `json:"schema_version"`
	JobID         string        `json:"job_id"`
	Seq           uint64        `json:"seq"`
	LastHash      string        `json:"last_hash"`
	State         jobs.JobState `json:"state"`
}

func NewSpecEnvelope(spec jobs.JobSpec) (SpecEnvelope, error) {
	envelope := SpecEnvelope{SchemaVersion: SchemaVersion, JobID: spec.ID, Spec: spec}
	if err := spec.Validate(); err != nil {
		return SpecEnvelope{}, fmt.Errorf("invalid job spec: %w", err)
	}
	hash, err := ComputeSpecHash(envelope)
	if err != nil {
		return SpecEnvelope{}, err
	}
	envelope.SpecHash = hash
	return envelope, nil
}

func (e SpecEnvelope) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", e.SchemaVersion)
	}
	if err := ValidatePortableID(e.JobID); err != nil {
		return err
	}
	if err := e.Spec.Validate(); err != nil {
		return fmt.Errorf("invalid spec: %w", err)
	}
	if e.JobID != e.Spec.ID {
		return fmt.Errorf("envelope job_id %q does not match spec id %q", e.JobID, e.Spec.ID)
	}
	return VerifySpecHash(e)
}

func ComputeSpecHash(envelope SpecEnvelope) (string, error) {
	canonical := struct {
		SchemaVersion int          `json:"schema_version"`
		JobID         string       `json:"job_id"`
		Spec          jobs.JobSpec `json:"spec"`
	}{
		SchemaVersion: envelope.SchemaVersion,
		JobID:         envelope.JobID,
		Spec:          envelope.Spec,
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal canonical spec: %w", err)
	}
	return sha256Hex(data), nil
}

func VerifySpecHash(envelope SpecEnvelope) error {
	if !validSHA256Hex(envelope.SpecHash) {
		return fmt.Errorf("invalid spec hash encoding")
	}
	want, err := ComputeSpecHash(envelope)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(envelope.SpecHash), []byte(want)) != 1 {
		return fmt.Errorf("spec hash mismatch")
	}
	return nil
}

func NewEventRecord(jobID string, seq, expectedRevision, revision uint64, previousHash string, event jobs.Event) (EventRecord, error) {
	record := EventRecord{
		SchemaVersion:    SchemaVersion,
		Seq:              seq,
		JobID:            jobID,
		ExpectedRevision: expectedRevision,
		Revision:         revision,
		PreviousHash:     previousHash,
		Event:            event,
	}
	if err := record.validateShape(false); err != nil {
		return EventRecord{}, err
	}
	hash, err := ComputeEventHash(record)
	if err != nil {
		return EventRecord{}, err
	}
	record.Hash = hash
	return record, nil
}

func (r EventRecord) Validate() error {
	if err := r.validateShape(true); err != nil {
		return err
	}
	return VerifyEventHash(r)
}

func (r EventRecord) validateShape(requireHash bool) error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", r.SchemaVersion)
	}
	if r.Seq == 0 {
		return fmt.Errorf("event sequence must be greater than zero")
	}
	if err := ValidatePortableID(r.JobID); err != nil {
		return err
	}
	if r.ExpectedRevision == ^uint64(0) || r.Revision != r.ExpectedRevision+1 {
		return fmt.Errorf("revision %d must follow expected_revision %d", r.Revision, r.ExpectedRevision)
	}
	if r.Seq != r.Revision {
		return fmt.Errorf("event sequence %d must match revision %d", r.Seq, r.Revision)
	}
	if !validSHA256Hex(r.PreviousHash) {
		return fmt.Errorf("invalid previous hash encoding")
	}
	if requireHash && !validSHA256Hex(r.Hash) {
		return fmt.Errorf("invalid event hash encoding")
	}
	return nil
}

// ComputeEventHash hashes the canonical JSON representation of record with
// Hash set to the empty string. The input value is not mutated.
func ComputeEventHash(record EventRecord) (string, error) {
	record.Hash = ""
	data, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("marshal canonical event record: %w", err)
	}
	return sha256Hex(data), nil
}

func VerifyEventHash(record EventRecord) error {
	if !validSHA256Hex(record.Hash) {
		return fmt.Errorf("invalid event hash encoding")
	}
	want, err := ComputeEventHash(record)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(record.Hash), []byte(want)) != 1 {
		return fmt.Errorf("event hash mismatch")
	}
	return nil
}

func (e SnapshotEnvelope) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", e.SchemaVersion)
	}
	if err := ValidatePortableID(e.JobID); err != nil {
		return err
	}
	if err := e.State.Validate(); err != nil {
		return fmt.Errorf("invalid snapshot state: %w", err)
	}
	if e.JobID != e.State.Spec.ID {
		return fmt.Errorf("snapshot job_id %q does not match state job id %q", e.JobID, e.State.Spec.ID)
	}
	if e.Seq != e.State.Revision {
		return fmt.Errorf("snapshot seq %d does not match state revision %d", e.Seq, e.State.Revision)
	}
	if !validSHA256Hex(e.LastHash) {
		return fmt.Errorf("invalid snapshot last hash encoding")
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
