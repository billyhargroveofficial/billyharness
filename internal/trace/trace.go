package trace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const CurrentManifestVersion = 1

type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	RunID         string    `json:"run_id"`
	CreatedAt     time.Time `json:"created_at"`
	StartedAtMS   int64     `json:"started_at_unix_ms"`
	Harness       string    `json:"harness"`
	TasksPath     string    `json:"tasks_path,omitempty"`
	TaskCount     int       `json:"task_count,omitempty"`
	ResultsJSONL  string    `json:"results_jsonl"`
	EventsJSONL   string    `json:"events_jsonl"`
	PayloadsDir   string    `json:"payloads_dir,omitempty"`
}

type EventRecord struct {
	SchemaVersion int          `json:"schema_version"`
	Seq           int64        `json:"seq"`
	RunID         string       `json:"run_id"`
	TaskID        string       `json:"task_id,omitempty"`
	Timestamp     time.Time    `json:"ts"`
	EventType     string       `json:"event_type,omitempty"`
	Event         any          `json:"event"`
	PayloadRefs   []PayloadRef `json:"payload_refs,omitempty"`
}

type PayloadRef struct {
	PayloadID string `json:"payload_id"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
}

type EventWriter struct {
	runID         string
	encoder       *json.Encoder
	now           func() time.Time
	sanitize      func(any) any
	seq           int64
	payloadDir    string
	payloadPolicy func(protocol.Event) bool
}

type EventWriterOption func(*EventWriter)

func WithNow(now func() time.Time) EventWriterOption {
	return func(writer *EventWriter) {
		if now != nil {
			writer.now = now
		}
	}
}

func WithSanitizer(sanitize func(any) any) EventWriterOption {
	return func(writer *EventWriter) {
		writer.sanitize = sanitize
	}
}

func WithPayloadDir(dir string, policy func(protocol.Event) bool) EventWriterOption {
	return func(writer *EventWriter) {
		writer.payloadDir = dir
		writer.payloadPolicy = policy
	}
}

func NewEventWriter(runID string, writer io.Writer, opts ...EventWriterOption) *EventWriter {
	eventWriter := &EventWriter{
		runID:   runID,
		encoder: json.NewEncoder(writer),
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, opt := range opts {
		opt(eventWriter)
	}
	return eventWriter
}

func (w *EventWriter) Record(taskID string, event protocol.Event) (EventRecord, error) {
	if w == nil {
		return EventRecord{}, fmt.Errorf("nil event writer")
	}
	w.seq++
	value := any(event)
	if w.sanitize != nil {
		value = w.sanitize(event)
	}
	payloadRefs, err := w.writePayloads(event, value)
	if err != nil {
		return EventRecord{}, err
	}
	recordEvent := value
	if len(payloadRefs) > 0 {
		recordEvent = protocol.Event{Type: event.Type}
	}
	record := EventRecord{
		SchemaVersion: CurrentManifestVersion,
		Seq:           w.seq,
		RunID:         w.runID,
		TaskID:        taskID,
		Timestamp:     w.now(),
		EventType:     string(event.Type),
		Event:         recordEvent,
		PayloadRefs:   payloadRefs,
	}
	return record, w.encoder.Encode(record)
}

func (w *EventWriter) writePayloads(event protocol.Event, value any) ([]PayloadRef, error) {
	if w.payloadDir == "" || w.payloadPolicy == nil || !w.payloadPolicy(event) {
		return nil, nil
	}
	if err := os.MkdirAll(w.payloadDir, 0o700); err != nil {
		return nil, err
	}
	payload := map[string]any{"event": value}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bytes)
	name := fmt.Sprintf("%06d.json", w.seq)
	path := filepath.Join(w.payloadDir, name)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return []PayloadRef{{
		PayloadID: fmt.Sprintf("payload:%d", w.seq),
		Kind:      "protocol_event",
		Path:      path,
		SHA256:    hex.EncodeToString(sum[:]),
		Bytes:     int64(len(bytes)),
	}}, nil
}

func WriteManifest(path string, manifest Manifest) error {
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = CurrentManifestVersion
	}
	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = time.Now().UTC()
	}
	if manifest.StartedAtMS == 0 {
		manifest.StartedAtMS = manifest.CreatedAt.UnixMilli()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(manifest)
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(tmp)
		return encodeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

type ReplaySummary struct {
	RunID       string         `json:"run_id"`
	Records     int            `json:"records"`
	FirstSeq    int64          `json:"first_seq,omitempty"`
	LastSeq     int64          `json:"last_seq,omitempty"`
	PayloadRefs int            `json:"payload_refs,omitempty"`
	EventTypes  map[string]int `json:"event_types"`
	Tasks       map[string]int `json:"tasks"`
}

func ReplayEvents(path string) (ReplaySummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return ReplaySummary{}, err
	}
	defer file.Close()

	summary := ReplaySummary{
		EventTypes: map[string]int{},
		Tasks:      map[string]int{},
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var expectedSeq int64 = 1
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		var record EventRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return summary, fmt.Errorf("%s:%d invalid event record: %w", path, lineNo, err)
		}
		if record.SchemaVersion != 0 && record.SchemaVersion != CurrentManifestVersion {
			return summary, fmt.Errorf("%s:%d unsupported schema_version %d", path, lineNo, record.SchemaVersion)
		}
		if record.Seq != expectedSeq {
			return summary, fmt.Errorf("%s:%d sequence gap: got %d want %d", path, lineNo, record.Seq, expectedSeq)
		}
		if summary.RunID == "" {
			summary.RunID = record.RunID
			summary.FirstSeq = record.Seq
		} else if record.RunID != summary.RunID {
			return summary, fmt.Errorf("%s:%d run_id changed from %q to %q", path, lineNo, summary.RunID, record.RunID)
		}
		summary.LastSeq = record.Seq
		summary.Records++
		if record.EventType != "" {
			summary.EventTypes[record.EventType]++
		}
		if record.TaskID != "" {
			summary.Tasks[record.TaskID]++
		}
		summary.PayloadRefs += len(record.PayloadRefs)
		expectedSeq++
	}
	if err := scanner.Err(); err != nil {
		return summary, err
	}
	return summary, nil
}
