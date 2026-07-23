package jobstore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

// RecoverableTail describes bytes that follow the final complete newline.
// The parser never truncates its input; a store may use TruncateOffset after
// obtaining its ownership lock and revalidating the file.
type RecoverableTail struct {
	TruncateOffset int64 `json:"truncate_offset"`
	ByteCount      int64 `json:"byte_count"`
}

// ParsedEventRecords is the syntactically and canonically validated JSONL
// prefix plus an optional crash tail. Record semantics and the hash chain are
// validated by Replay.
type ParsedEventRecords struct {
	Records []EventRecord
	Tail    *RecoverableTail
	offsets []int64
}

// ReplayResult is reconstructed exclusively from the immutable spec and the
// canonical, complete JSONL prefix.
type ReplayResult struct {
	State      jobs.JobState
	Seq        uint64
	LastHash   string
	Records    []EventRecord
	LastRecord *EventRecord
	Tail       *RecoverableTail
}

// ParseEventRecords parses complete JSONL records without mutating the input.
// A non-empty final byte sequence without a newline is returned as a
// recoverable tail and is never decoded or applied, even when it looks like
// valid JSON. Empty, malformed, non-canonical, and oversized completed lines
// fail closed.
func ParseEventRecords(reader io.Reader, maxRecordBytes int) (ParsedEventRecords, error) {
	if reader == nil {
		return ParsedEventRecords{}, fmt.Errorf("event reader is required")
	}
	if maxRecordBytes <= 0 {
		maxRecordBytes = DefaultMaxRecordBytes
	}

	bufferSize := min(maxRecordBytes, 64*1024)
	if bufferSize < 16 {
		bufferSize = 16
	}
	buffered := bufio.NewReaderSize(reader, bufferSize)
	parsed := ParsedEventRecords{}
	var line []byte
	var offset int64

	for {
		fragment, readErr := buffered.ReadSlice('\n')
		if len(fragment) > 0 {
			contentBytes := len(fragment)
			if fragment[len(fragment)-1] == '\n' {
				contentBytes--
			}
			if contentBytes > maxRecordBytes || len(line) > maxRecordBytes-contentBytes {
				return ParsedEventRecords{}, NewCorruptionError(CorruptionMetadata{
					Line:   len(parsed.Records) + 1,
					Offset: offset,
					Kind:   CorruptionRecordTooLarge,
				}, &TooLargeError{Resource: "event record", Limit: int64(maxRecordBytes)})
			}
			line = append(line, fragment...)
		}

		switch {
		case readErr == nil:
			content := line[:len(line)-1]
			if len(content) == 0 {
				return ParsedEventRecords{}, NewCorruptionError(CorruptionMetadata{
					Line:   len(parsed.Records) + 1,
					Offset: offset,
					Kind:   CorruptionMalformedJSON,
				}, errors.New("empty completed JSONL record"))
			}
			record, kind, err := decodeCanonicalEventRecord(content)
			if err != nil {
				return ParsedEventRecords{}, NewCorruptionError(CorruptionMetadata{
					JobID:  record.JobID,
					Line:   len(parsed.Records) + 1,
					Seq:    record.Seq,
					Offset: offset,
					Kind:   kind,
				}, err)
			}
			parsed.Records = append(parsed.Records, record)
			parsed.offsets = append(parsed.offsets, offset)
			offset += int64(len(line))
			line = line[:0]
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if len(line) == 0 {
				return parsed, nil
			}
			if len(line) > maxRecordBytes {
				return ParsedEventRecords{}, NewCorruptionError(CorruptionMetadata{
					Line:   len(parsed.Records) + 1,
					Offset: offset,
					Kind:   CorruptionRecordTooLarge,
				}, &TooLargeError{Resource: "unterminated event record", Limit: int64(maxRecordBytes)})
			}
			parsed.Tail = &RecoverableTail{
				TruncateOffset: offset,
				ByteCount:      int64(len(line)),
			}
			return parsed, nil
		default:
			return ParsedEventRecords{}, fmt.Errorf("read events JSONL at offset %d: %w", offset, readErr)
		}
	}
}

// Replay reconstructs a job from a verified spec envelope and the complete
// prefix of its canonical event stream. It rejects all ambiguity other than a
// bounded, unterminated final record reported through ReplayResult.Tail.
func Replay(spec SpecEnvelope, reader io.Reader, maxRecordBytes int) (ReplayResult, error) {
	if err := VerifySpecHash(spec); err != nil {
		return ReplayResult{}, replayCorruption(spec.JobID, 0, 0, 0, CorruptionHashMismatch, fmt.Errorf("verify spec hash: %w", err))
	}
	if spec.SchemaVersion != SchemaVersion {
		return ReplayResult{}, replayCorruption(spec.JobID, 0, 0, 0, CorruptionUnsupportedVersion, fmt.Errorf(
			"unsupported spec schema version %d", spec.SchemaVersion,
		))
	}
	if err := spec.Spec.Validate(); err != nil {
		return ReplayResult{}, replayCorruption(spec.JobID, 0, 0, 0, CorruptionInvalidSpec, fmt.Errorf("invalid job spec: %w", err))
	}
	if spec.JobID != spec.Spec.ID {
		return ReplayResult{}, replayCorruption(spec.JobID, 0, 0, 0, CorruptionIdentityMismatch, fmt.Errorf(
			"spec job ID %q does not match embedded job ID %q", spec.JobID, spec.Spec.ID,
		))
	}

	parsed, err := ParseEventRecords(reader, maxRecordBytes)
	if err != nil {
		return ReplayResult{}, err
	}
	state := jobs.JobState{Spec: spec.Spec, Status: jobs.JobStatusQueued}
	result := ReplayResult{
		State:    state,
		LastHash: spec.SpecHash,
		Tail:     cloneRecoverableTail(parsed.Tail),
	}
	seenEventIDs := make(map[string]uint64, len(parsed.Records))

	for index, record := range parsed.Records {
		line := index + 1
		offset := parsed.offsets[index]
		expectedSeq := uint64(line)
		corrupt := func(kind CorruptionKind, cause error) (ReplayResult, error) {
			return ReplayResult{}, replayCorruption(spec.JobID, line, record.Seq, offset, kind, cause)
		}

		if record.SchemaVersion != SchemaVersion {
			return corrupt(CorruptionUnsupportedVersion, fmt.Errorf(
				"event seq %d has unsupported schema version %d", record.Seq, record.SchemaVersion,
			))
		}
		if record.Seq != expectedSeq {
			return corrupt(CorruptionSequenceGap, fmt.Errorf(
				"event sequence is %d, want contiguous %d", record.Seq, expectedSeq,
			))
		}
		if record.JobID != spec.JobID {
			return corrupt(CorruptionIdentityMismatch, fmt.Errorf(
				"event job ID %q does not match spec job ID %q", record.JobID, spec.JobID,
			))
		}
		if record.ExpectedRevision != state.Revision {
			return corrupt(CorruptionRevisionMismatch, fmt.Errorf(
				"event expected revision %d, replay state is %d", record.ExpectedRevision, state.Revision,
			))
		}
		if record.ExpectedRevision == ^uint64(0) || record.Revision != record.ExpectedRevision+1 {
			return corrupt(CorruptionRevisionMismatch, fmt.Errorf(
				"event revision %d must immediately follow expected revision %d", record.Revision, record.ExpectedRevision,
			))
		}
		if record.PreviousHash != result.LastHash {
			return corrupt(CorruptionHashMismatch, fmt.Errorf(
				"event previous hash %q does not match %q", record.PreviousHash, result.LastHash,
			))
		}
		if err := VerifyEventHash(record); err != nil {
			return corrupt(CorruptionHashMismatch, fmt.Errorf("verify event hash: %w", err))
		}
		if previousSeq, duplicate := seenEventIDs[record.Event.ID]; duplicate {
			return corrupt(CorruptionDuplicateEvent, fmt.Errorf(
				"event ID %q was already recorded at seq %d", record.Event.ID, previousSeq,
			))
		}

		next, err := jobs.Reduce(state, record.Event)
		if err != nil {
			return corrupt(CorruptionReducerTransition, fmt.Errorf("reduce event: %w", err))
		}
		if next.Revision != record.Revision {
			return corrupt(CorruptionRevisionMismatch, fmt.Errorf(
				"reducer produced revision %d, record declares %d", next.Revision, record.Revision,
			))
		}

		state = next
		result.State = state
		result.Seq = record.Seq
		result.LastHash = record.Hash
		result.Records = append(result.Records, record)
		seenEventIDs[record.Event.ID] = record.Seq
		last := record
		result.LastRecord = &last
	}

	return result, nil
}

func decodeCanonicalEventRecord(content []byte) (EventRecord, CorruptionKind, error) {
	var record EventRecord
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, CorruptionMalformedJSON, fmt.Errorf("decode completed event record: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return record, CorruptionMalformedJSON, fmt.Errorf("decode completed event record: %w", err)
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		return record, CorruptionMalformedJSON, fmt.Errorf("canonicalize event record: %w", err)
	}
	if !bytes.Equal(content, canonical) {
		return record, CorruptionNonCanonical, errors.New("completed event record is not canonical JSON")
	}
	return record, "", nil
}

func replayCorruption(jobID string, line int, seq uint64, offset int64, kind CorruptionKind, cause error) error {
	return NewCorruptionError(CorruptionMetadata{
		JobID:  jobID,
		Line:   line,
		Seq:    seq,
		Offset: offset,
		Kind:   kind,
	}, cause)
}

func cloneRecoverableTail(tail *RecoverableTail) *RecoverableTail {
	if tail == nil {
		return nil
	}
	return &RecoverableTail{
		TruncateOffset: tail.TruncateOffset,
		ByteCount:      tail.ByteCount,
	}
}
