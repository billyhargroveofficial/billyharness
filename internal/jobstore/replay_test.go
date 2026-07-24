package jobstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestReplayReconstructsCanonicalStateAndChain(t *testing.T) {
	spec, records := replayTestFixture(t)
	data := marshalEventRecords(t, records...)

	result, err := Replay(spec, bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if result.State.Status != jobs.JobStatusPaused ||
		result.State.TerminalReason != "" ||
		result.State.Revision != 2 {
		t.Fatalf("replayed state = %#v", result.State)
	}
	if result.Seq != 2 || result.LastHash != records[1].Hash || result.Tail != nil {
		t.Fatalf("replay metadata = %#v", result)
	}
	if !reflect.DeepEqual(result.Records, records) {
		t.Fatalf("replay records = %#v, want %#v", result.Records, records)
	}
	if result.LastRecord == nil || !reflect.DeepEqual(*result.LastRecord, records[1]) {
		t.Fatalf("last record = %#v, want %#v", result.LastRecord, records[1])
	}
}

func TestReplayEmptyLogStartsQueuedFromSpecHash(t *testing.T) {
	spec, _ := replayTestFixture(t)
	result, err := Replay(spec, bytes.NewReader(nil), DefaultMaxRecordBytes)
	if err != nil {
		t.Fatalf("Replay empty log: %v", err)
	}
	if result.State.Status != jobs.JobStatusQueued || result.State.Revision != 0 {
		t.Fatalf("empty replay state = %#v", result.State)
	}
	if result.Seq != 0 || result.LastHash != spec.SpecHash || result.LastRecord != nil || result.Tail != nil {
		t.Fatalf("empty replay metadata = %#v", result)
	}
}

func TestReplayAcceptsLegacySpecWithoutAdmittedAt(t *testing.T) {
	t.Parallel()

	legacy, _ := replayTestFixture(t)
	if !legacy.Spec.AdmittedAt.IsZero() {
		t.Fatalf("fixture admitted_at = %s, want legacy zero", legacy.Spec.AdmittedAt)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"admitted_at"`)) {
		t.Fatalf("legacy envelope unexpectedly contains admitted_at: %s", encoded)
	}

	var decoded SpecEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SpecHash != legacy.SpecHash {
		t.Fatalf("decoded legacy hash = %q, want unchanged %q", decoded.SpecHash, legacy.SpecHash)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("validate legacy envelope/hash: %v", err)
	}
	recomputed, err := ComputeSpecHash(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if recomputed != legacy.SpecHash {
		t.Fatalf("recomputed legacy hash = %q, want unchanged %q", recomputed, legacy.SpecHash)
	}
	result, err := Replay(decoded, bytes.NewReader(nil), DefaultMaxRecordBytes)
	if err != nil {
		t.Fatalf("Replay legacy envelope: %v", err)
	}
	if !result.State.Spec.AdmittedAt.IsZero() || result.State.Status != jobs.JobStatusQueued {
		t.Fatalf("legacy replay state = %#v", result.State)
	}
}

func TestParseAndReplayReportOnlyUnterminatedTailAsRecoverable(t *testing.T) {
	spec, records := replayTestFixture(t)
	first := marshalEventRecords(t, records[0])
	second, err := json.Marshal(records[1])
	if err != nil {
		t.Fatal(err)
	}
	data := append(append([]byte(nil), first...), second...)

	parsed, err := ParseEventRecords(bytes.NewReader(data), DefaultMaxRecordBytes)
	if err != nil {
		t.Fatalf("ParseEventRecords: %v", err)
	}
	if len(parsed.Records) != 1 || parsed.Records[0].Seq != 1 {
		t.Fatalf("parsed records = %#v", parsed.Records)
	}
	if parsed.Tail == nil ||
		parsed.Tail.TruncateOffset != int64(len(first)) ||
		parsed.Tail.ByteCount != int64(len(second)) {
		t.Fatalf("recoverable tail = %#v", parsed.Tail)
	}

	result, err := Replay(spec, bytes.NewReader(data), DefaultMaxRecordBytes)
	if err != nil {
		t.Fatalf("Replay with tail: %v", err)
	}
	if result.State.Status != jobs.JobStatusRunning || result.State.Revision != 1 || result.Seq != 1 {
		t.Fatalf("tail was applied instead of ignored: %#v", result)
	}
	if !reflect.DeepEqual(result.Tail, parsed.Tail) {
		t.Fatalf("replay tail = %#v, parsed tail = %#v", result.Tail, parsed.Tail)
	}

	for _, partial := range [][]byte{[]byte("{"), second, []byte("   ")} {
		parsed, err := ParseEventRecords(bytes.NewReader(partial), DefaultMaxRecordBytes)
		if err != nil {
			t.Fatalf("unterminated %q: %v", partial, err)
		}
		if len(parsed.Records) != 0 || parsed.Tail == nil || parsed.Tail.TruncateOffset != 0 || parsed.Tail.ByteCount != int64(len(partial)) {
			t.Fatalf("unterminated %q parsed as %#v", partial, parsed)
		}
	}
}

func TestParseEventRecordsFailsClosedOnCompletedCorruption(t *testing.T) {
	_, records := replayTestFixture(t)
	canonical, err := json.Marshal(records[0])
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), canonical[:len(canonical)-1]...), []byte(`,"unknown":true}`)...)

	tests := []struct {
		name string
		data []byte
		kind CorruptionKind
	}{
		{name: "malformed", data: []byte("{]\n"), kind: CorruptionMalformedJSON},
		{name: "empty completed line", data: []byte("\n"), kind: CorruptionMalformedJSON},
		{name: "trailing empty completed line", data: append(marshalEventRecords(t, records[0]), '\n'), kind: CorruptionMalformedJSON},
		{name: "unknown field", data: append(unknown, '\n'), kind: CorruptionMalformedJSON},
		{name: "non canonical whitespace", data: append(append([]byte(" "), canonical...), '\n'), kind: CorruptionNonCanonical},
		{name: "crlf", data: append(append([]byte(nil), canonical...), '\r', '\n'), kind: CorruptionNonCanonical},
		{name: "multiple values", data: append(append(append([]byte(nil), canonical...), canonical...), '\n'), kind: CorruptionMalformedJSON},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseEventRecords(bytes.NewReader(test.data), DefaultMaxRecordBytes)
			assertCorruptionKind(t, err, test.kind)
		})
	}
}

func TestParseEventRecordsReportsCompletedMidLogCorruptionOffset(t *testing.T) {
	_, records := replayTestFixture(t)
	first := marshalEventRecords(t, records[0])
	data := append(append([]byte(nil), first...), []byte("{]\n")...)

	_, err := ParseEventRecords(bytes.NewReader(data), DefaultMaxRecordBytes)
	assertCorruptionKind(t, err, CorruptionMalformedJSON)
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("error type = %T, want *CorruptionError", err)
	}
	if corruption.Line != 2 || corruption.Offset != int64(len(first)) {
		t.Fatalf("corruption location = line %d offset %d, want line 2 offset %d", corruption.Line, corruption.Offset, len(first))
	}
}

func TestParseEventRecordsEnforcesRecordLimitForCompleteAndPartialLines(t *testing.T) {
	const limit = 8
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "completed", data: []byte(strings.Repeat("x", limit+1) + "\n")},
		{name: "unterminated", data: []byte(strings.Repeat("x", limit+1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseEventRecords(bytes.NewReader(test.data), limit)
			assertCorruptionKind(t, err, CorruptionRecordTooLarge)
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("error = %v, want errors.Is(ErrTooLarge)", err)
			}
		})
	}

	parsed, err := ParseEventRecords(bytes.NewReader([]byte(strings.Repeat("x", limit))), limit)
	if err != nil {
		t.Fatalf("exact-limit partial tail: %v", err)
	}
	if parsed.Tail == nil || parsed.Tail.ByteCount != limit {
		t.Fatalf("exact-limit tail = %#v", parsed.Tail)
	}
}

func TestReplayRejectsRecordInvariantViolations(t *testing.T) {
	spec, valid := replayTestFixture(t)
	zeroHash := strings.Repeat("0", 64)

	tests := []struct {
		name     string
		mutate   func([]EventRecord) []EventRecord
		wantKind CorruptionKind
		tampered bool
	}{
		{
			name: "unsupported version",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].SchemaVersion = SchemaVersion + 1
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionUnsupportedVersion,
		},
		{
			name: "sequence gap",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].Seq = 2
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionSequenceGap,
		},
		{
			name: "job identity",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].JobID = "other-job"
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionIdentityMismatch,
		},
		{
			name: "expected revision",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].ExpectedRevision = 1
				records[0].Revision = 2
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionRevisionMismatch,
		},
		{
			name: "declared revision",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].Revision = 2
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionRevisionMismatch,
		},
		{
			name: "first record not chained to spec",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].PreviousHash = zeroHash
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionHashMismatch,
			tampered: true,
		},
		{
			name: "event hash",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].Hash = zeroHash
				return records[:1]
			},
			wantKind: CorruptionHashMismatch,
			tampered: true,
		},
		{
			name: "invalid reducer transition",
			mutate: func(records []EventRecord) []EventRecord {
				records[0].Event = jobs.Event{
					ID: "invalid-resume", Type: jobs.EventJobResumed, At: replayTestTime().Add(time.Minute),
				}
				rehashEventRecord(t, &records[0])
				return records[:1]
			},
			wantKind: CorruptionReducerTransition,
		},
		{
			name: "duplicate event id cannot fake revision",
			mutate: func(records []EventRecord) []EventRecord {
				records[1].Event = records[0].Event
				rehashEventRecord(t, &records[1])
				return records
			},
			wantKind: CorruptionDuplicateEvent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := append([]EventRecord(nil), valid...)
			records = test.mutate(records)
			_, err := Replay(spec, bytes.NewReader(marshalEventRecords(t, records...)), DefaultMaxRecordBytes)
			assertCorruptionKind(t, err, test.wantKind)
			if test.tampered && !errors.Is(err, ErrTampered) {
				t.Fatalf("error = %v, want errors.Is(ErrTampered)", err)
			}
		})
	}
}

func TestReplayVerifiesSpecBeforeReadingEvents(t *testing.T) {
	valid, _ := replayTestFixture(t)
	zeroHash := strings.Repeat("0", 64)

	tests := []struct {
		name     string
		mutate   func(SpecEnvelope) SpecEnvelope
		wantKind CorruptionKind
		tampered bool
	}{
		{
			name: "hash",
			mutate: func(spec SpecEnvelope) SpecEnvelope {
				spec.SpecHash = zeroHash
				return spec
			},
			wantKind: CorruptionHashMismatch,
			tampered: true,
		},
		{
			name: "version",
			mutate: func(spec SpecEnvelope) SpecEnvelope {
				spec.SchemaVersion++
				rehashSpecEnvelope(t, &spec)
				return spec
			},
			wantKind: CorruptionUnsupportedVersion,
		},
		{
			name: "invalid spec",
			mutate: func(spec SpecEnvelope) SpecEnvelope {
				spec.Spec.Goal = ""
				rehashSpecEnvelope(t, &spec)
				return spec
			},
			wantKind: CorruptionInvalidSpec,
		},
		{
			name: "identity",
			mutate: func(spec SpecEnvelope) SpecEnvelope {
				spec.JobID = "other-job"
				rehashSpecEnvelope(t, &spec)
				return spec
			},
			wantKind: CorruptionIdentityMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &panicReader{}
			_, err := Replay(test.mutate(valid), reader, DefaultMaxRecordBytes)
			assertCorruptionKind(t, err, test.wantKind)
			if test.tampered && !errors.Is(err, ErrTampered) {
				t.Fatalf("error = %v, want errors.Is(ErrTampered)", err)
			}
		})
	}
}

func replayTestFixture(t *testing.T) (SpecEnvelope, []EventRecord) {
	t.Helper()
	now := replayTestTime()
	workflow, err := jobs.CompilePreset(jobs.PresetGeneral, 1)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NewSpecEnvelope(jobs.JobSpec{
		ID:       "job-replay-test",
		Goal:     "Replay a bounded durable job deterministically.",
		Preset:   jobs.PresetGeneral,
		Workers:  1,
		Deadline: now.Add(24 * time.Hour),
		Budget:   jobs.Budget{MaxCycles: 4, MaxAttempts: 4, MaxModelCalls: 8, MaxTokens: 10_000},
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen",
			ModelID:    "qwen3.8-max-preview",
		},
		Workflow:  jobs.WorkflowControlFromWorkflow(workflow),
		Authority: jobs.DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	})
	if err != nil {
		t.Fatal(err)
	}

	state := jobs.JobState{Spec: spec.Spec, Status: jobs.JobStatusQueued}
	previousHash := spec.SpecHash
	events := []jobs.Event{
		{ID: "event-start", Type: jobs.EventJobStarted, At: now},
		{ID: "event-pause", Type: jobs.EventJobPaused, At: now.Add(time.Minute)},
	}
	records := make([]EventRecord, 0, len(events))
	for index, event := range events {
		next, err := jobs.Reduce(state, event)
		if err != nil {
			t.Fatalf("reduce fixture event %d: %v", index+1, err)
		}
		record, err := NewEventRecord(spec.JobID, uint64(index+1), state.Revision, next.Revision, previousHash, event)
		if err != nil {
			t.Fatalf("new fixture record %d: %v", index+1, err)
		}
		records = append(records, record)
		state = next
		previousHash = record.Hash
	}
	return spec, records
}

func marshalEventRecords(t *testing.T, records ...EventRecord) []byte {
	t.Helper()
	var data []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	return data
}

func rehashEventRecord(t *testing.T, record *EventRecord) {
	t.Helper()
	hash, err := ComputeEventHash(*record)
	if err != nil {
		t.Fatal(err)
	}
	record.Hash = hash
}

func rehashSpecEnvelope(t *testing.T, spec *SpecEnvelope) {
	t.Helper()
	hash, err := ComputeSpecHash(*spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.SpecHash = hash
}

func assertCorruptionKind(t *testing.T, err error, kind CorruptionKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want corruption kind %q", kind)
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error = %v, want errors.Is(ErrCorrupt)", err)
	}
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("error type = %T, want *CorruptionError", err)
	}
	if corruption.Kind != kind {
		t.Fatalf("corruption kind = %q, want %q: %v", corruption.Kind, kind, err)
	}
}

func replayTestTime() time.Time {
	return time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
}

type panicReader struct{}

func (*panicReader) Read([]byte) (int, error) {
	panic(fmt.Errorf("event stream must not be read before spec verification"))
}
