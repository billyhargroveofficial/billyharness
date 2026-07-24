package jobstore

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound      = errors.New("job not found")
	ErrAlreadyExists = errors.New("job already exists")
	ErrConflict      = errors.New("job revision conflict")
	ErrCorrupt       = errors.New("job store corruption")
	ErrClosed        = errors.New("job store closed")
	ErrOwnership     = errors.New("job store ownership unavailable")
	ErrTooLarge      = errors.New("job store value too large")
	ErrTampered      = errors.New("job store content tampered")
	ErrInvalidID     = errors.New("invalid portable id")
	ErrCommitted     = errors.New("operation committed with durability warning")
)

// CommitError means the operation's canonical target is visible and must be
// reconciled rather than blindly retried, but a post-publication durability
// step (normally a parent-directory fsync) failed.
type CommitError struct {
	Operation string
	JobID     string
	Revision  uint64
	Err       error
}

func (e *CommitError) Error() string {
	if e == nil {
		return ErrCommitted.Error()
	}
	return fmt.Sprintf("%s: %s job %q revision %d: %v", ErrCommitted, e.Operation, e.JobID, e.Revision, e.Err)
}

func (e *CommitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *CommitError) Is(target error) bool { return target == ErrCommitted }

type ConflictError struct {
	JobID            string
	ExpectedRevision uint64
	ActualRevision   uint64
}

func (e *ConflictError) Error() string {
	if e == nil {
		return ErrConflict.Error()
	}
	return fmt.Sprintf("%s: job %q expected revision %d, actual %d", ErrConflict, e.JobID, e.ExpectedRevision, e.ActualRevision)
}

func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

type CorruptionKind string

const (
	CorruptionMalformedJSON      CorruptionKind = "malformed_json"
	CorruptionNonCanonical       CorruptionKind = "non_canonical"
	CorruptionRecordTooLarge     CorruptionKind = "record_too_large"
	CorruptionUnsupportedVersion CorruptionKind = "unsupported_version"
	CorruptionInvalidSpec        CorruptionKind = "invalid_spec"
	CorruptionIdentityMismatch   CorruptionKind = "identity_mismatch"
	CorruptionDuplicateEvent     CorruptionKind = "duplicate_event"
	CorruptionSequenceGap        CorruptionKind = "sequence_gap"
	CorruptionRevisionMismatch   CorruptionKind = "revision_mismatch"
	CorruptionHashMismatch       CorruptionKind = "hash_mismatch"
	CorruptionReducerTransition  CorruptionKind = "reducer_transition"
	CorruptionCompletedTail      CorruptionKind = "completed_tail"
	CorruptionSnapshotMismatch   CorruptionKind = "snapshot_mismatch"
	CorruptionArtifactMismatch   CorruptionKind = "artifact_mismatch"
	CorruptionUnreadable         CorruptionKind = "unreadable"
)

type CorruptionMetadata struct {
	JobID  string         `json:"job_id,omitempty"`
	Path   string         `json:"path,omitempty"`
	Line   int            `json:"line,omitempty"`
	Seq    uint64         `json:"seq,omitempty"`
	Offset int64          `json:"offset,omitempty"`
	Kind   CorruptionKind `json:"kind,omitempty"`
}

type CorruptionError struct {
	CorruptionMetadata
	Err error
}

func NewCorruptionError(metadata CorruptionMetadata, err error) *CorruptionError {
	return &CorruptionError{CorruptionMetadata: metadata, Err: err}
}

func (e *CorruptionError) Error() string {
	if e == nil {
		return ErrCorrupt.Error()
	}
	detail := ""
	if e.Err != nil {
		detail = ": " + e.Err.Error()
	}
	return fmt.Sprintf("%s: kind=%s job=%q path=%q line=%d seq=%d offset=%d%s",
		ErrCorrupt, e.Kind, e.JobID, e.Path, e.Line, e.Seq, e.Offset, detail)
}

func (e *CorruptionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *CorruptionError) Is(target error) bool {
	if target == ErrCorrupt {
		return true
	}
	return target == ErrTampered && e != nil &&
		(e.Kind == CorruptionHashMismatch || e.Kind == CorruptionArtifactMismatch)
}

type OwnershipError struct {
	Root string
	Err  error
}

func (e *OwnershipError) Error() string {
	if e == nil || e.Err == nil {
		return ErrOwnership.Error()
	}
	return fmt.Sprintf("%s for %q: %v", ErrOwnership, e.Root, e.Err)
}

func (e *OwnershipError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *OwnershipError) Is(target error) bool { return target == ErrOwnership }

type TooLargeError struct {
	Resource string
	Limit    int64
	Actual   int64
}

func (e *TooLargeError) Error() string {
	if e == nil {
		return ErrTooLarge.Error()
	}
	if e.Actual > 0 {
		return fmt.Sprintf("%s: %s is %d bytes, limit %d", ErrTooLarge, e.Resource, e.Actual, e.Limit)
	}
	return fmt.Sprintf("%s: %s exceeds limit %d", ErrTooLarge, e.Resource, e.Limit)
}

func (e *TooLargeError) Is(target error) bool { return target == ErrTooLarge }

type InvalidIDError struct {
	Value string
}

func (e *InvalidIDError) Error() string {
	if e == nil {
		return ErrInvalidID.Error()
	}
	return fmt.Sprintf("%s %q", ErrInvalidID, e.Value)
}

func (e *InvalidIDError) Is(target error) bool { return target == ErrInvalidID }
