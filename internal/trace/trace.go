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
	"strings"
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
	RunID              string         `json:"run_id"`
	Records            int            `json:"records"`
	FirstSeq           int64          `json:"first_seq,omitempty"`
	LastSeq            int64          `json:"last_seq,omitempty"`
	PayloadRefs        int            `json:"payload_refs,omitempty"`
	PayloadBytes       int64          `json:"payload_bytes,omitempty"`
	EventTypes         map[string]int `json:"event_types"`
	Tasks              map[string]int `json:"tasks"`
	RunStarted         int            `json:"run_started,omitempty"`
	RunCompleted       int            `json:"run_completed,omitempty"`
	RunFailed          int            `json:"run_failed,omitempty"`
	TurnsStarted       int            `json:"turns_started,omitempty"`
	TurnsCompleted     int            `json:"turns_completed,omitempty"`
	TurnsFailed        int            `json:"turns_failed,omitempty"`
	StepsStarted       int            `json:"steps_started,omitempty"`
	StepsCompleted     int            `json:"steps_completed,omitempty"`
	StepsFailed        int            `json:"steps_failed,omitempty"`
	ParallelBatches    int            `json:"parallel_batches,omitempty"`
	ModelCallsStarted  int            `json:"model_calls_started,omitempty"`
	ModelCallsFinished int            `json:"model_calls_finished,omitempty"`
	ToolCallsStarted   int            `json:"tool_calls_started,omitempty"`
	ToolCallsFinished  int            `json:"tool_calls_finished,omitempty"`
	ContextCompactions int            `json:"context_compactions,omitempty"`
	InputTokens        int64          `json:"input_tokens,omitempty"`
	OutputTokens       int64          `json:"output_tokens,omitempty"`
	CacheHitTokens     int64          `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens    int64          `json:"cache_miss_tokens,omitempty"`
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
		bytes, err := verifyPayloadRefs(path, record.PayloadRefs)
		if err != nil {
			return summary, fmt.Errorf("%s:%d %w", path, lineNo, err)
		}
		summary.PayloadBytes += bytes
		if err := summary.observe(record); err != nil {
			return summary, fmt.Errorf("%s:%d %w", path, lineNo, err)
		}
		expectedSeq++
	}
	if err := scanner.Err(); err != nil {
		return summary, err
	}
	return summary, nil
}

func verifyPayloadRefs(eventsPath string, refs []PayloadRef) (int64, error) {
	var total int64
	for _, ref := range refs {
		if strings.TrimSpace(ref.Path) == "" {
			return total, fmt.Errorf("payload ref %q missing path", ref.PayloadID)
		}
		path := resolvePayloadPath(eventsPath, ref.Path)
		bytes, err := os.ReadFile(path)
		if err != nil {
			return total, fmt.Errorf("payload ref %q unreadable: %w", ref.PayloadID, err)
		}
		if ref.Bytes > 0 && int64(len(bytes)) != ref.Bytes {
			return total, fmt.Errorf("payload ref %q byte mismatch: got %d want %d", ref.PayloadID, len(bytes), ref.Bytes)
		}
		if ref.SHA256 != "" {
			sum := sha256.Sum256(bytes)
			if got := hex.EncodeToString(sum[:]); got != ref.SHA256 {
				return total, fmt.Errorf("payload ref %q sha256 mismatch: got %s want %s", ref.PayloadID, got, ref.SHA256)
			}
		}
		total += int64(len(bytes))
	}
	return total, nil
}

func resolvePayloadPath(eventsPath, payloadPath string) string {
	if filepath.IsAbs(payloadPath) {
		return payloadPath
	}
	if _, err := os.Stat(payloadPath); err == nil {
		return payloadPath
	}
	return filepath.Join(filepath.Dir(eventsPath), payloadPath)
}

func (s *ReplaySummary) observe(record EventRecord) error {
	switch protocol.EventType(record.EventType) {
	case protocol.EventRunStarted:
		s.RunStarted++
	case protocol.EventRunCompleted:
		s.RunCompleted++
	case protocol.EventRunFailed:
		s.RunFailed++
	case protocol.EventTurnStarted:
		s.TurnsStarted++
	case protocol.EventTurnCompleted:
		turn, err := turnFromEvent(record.Event)
		if err != nil {
			return err
		}
		s.TurnsCompleted++
		if turn.Status == protocol.TurnStatusFailed {
			s.TurnsFailed++
		}
	case protocol.EventStepStarted:
		step, err := stepFromEvent(record.Event)
		if err != nil {
			return err
		}
		s.StepsStarted++
		if step.Kind == protocol.StepKindToolBatch && step.Parallel {
			s.ParallelBatches++
		}
	case protocol.EventStepCompleted:
		step, err := stepFromEvent(record.Event)
		if err != nil {
			return err
		}
		s.StepsCompleted++
		if step.Status == protocol.StepStatusFailed {
			s.StepsFailed++
		}
	case protocol.EventModelCallStarted:
		s.ModelCallsStarted++
	case protocol.EventModelCallFinished:
		s.ModelCallsFinished++
	case protocol.EventToolCallStarted:
		s.ToolCallsStarted++
	case protocol.EventToolCallFinished:
		s.ToolCallsFinished++
	case protocol.EventContextCompacted:
		s.ContextCompactions++
	case protocol.EventProviderUsageUpdate:
		usage, err := usageFromEvent(record.Event)
		if err != nil {
			return err
		}
		s.InputTokens += usage.InputTokens
		s.OutputTokens += usage.OutputTokens
		s.CacheHitTokens += usage.CacheHitTokens
		s.CacheMissTokens += usage.CacheMissTokens
	}
	return nil
}

func turnFromEvent(value any) (protocol.TurnEvent, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return protocol.TurnEvent{}, fmt.Errorf("invalid turn event: %w", err)
	}
	var event struct {
		Type protocol.EventType `json:"type"`
		Data json.RawMessage    `json:"data"`
	}
	if err := json.Unmarshal(bytes, &event); err != nil {
		return protocol.TurnEvent{}, fmt.Errorf("invalid turn event: %w", err)
	}
	if event.Type != "" && event.Type != protocol.EventTurnStarted && event.Type != protocol.EventTurnCompleted {
		return protocol.TurnEvent{}, fmt.Errorf("invalid turn event type %q", event.Type)
	}
	if len(event.Data) == 0 {
		return protocol.TurnEvent{}, fmt.Errorf("turn event missing data")
	}
	var turn protocol.TurnEvent
	if err := json.Unmarshal(event.Data, &turn); err != nil {
		return protocol.TurnEvent{}, fmt.Errorf("invalid turn event data: %w", err)
	}
	return turn, nil
}

func stepFromEvent(value any) (protocol.StepEvent, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return protocol.StepEvent{}, fmt.Errorf("invalid step event: %w", err)
	}
	var event struct {
		Type protocol.EventType `json:"type"`
		Data json.RawMessage    `json:"data"`
	}
	if err := json.Unmarshal(bytes, &event); err != nil {
		return protocol.StepEvent{}, fmt.Errorf("invalid step event: %w", err)
	}
	if event.Type != "" && event.Type != protocol.EventStepStarted && event.Type != protocol.EventStepCompleted {
		return protocol.StepEvent{}, fmt.Errorf("invalid step event type %q", event.Type)
	}
	if len(event.Data) == 0 {
		return protocol.StepEvent{}, fmt.Errorf("step event missing data")
	}
	var step protocol.StepEvent
	if err := json.Unmarshal(event.Data, &step); err != nil {
		return protocol.StepEvent{}, fmt.Errorf("invalid step event data: %w", err)
	}
	return step, nil
}

type replayUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheHitTokens  int64 `json:"cache_hit_tokens"`
	CacheMissTokens int64 `json:"cache_miss_tokens"`
}

func usageFromEvent(value any) (replayUsage, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return replayUsage{}, fmt.Errorf("invalid provider usage event: %w", err)
	}
	var event struct {
		Type protocol.EventType `json:"type"`
		Data json.RawMessage    `json:"data"`
	}
	if err := json.Unmarshal(bytes, &event); err != nil {
		return replayUsage{}, fmt.Errorf("invalid provider usage event: %w", err)
	}
	if event.Type != "" && event.Type != protocol.EventProviderUsageUpdate {
		return replayUsage{}, fmt.Errorf("invalid provider usage event type %q", event.Type)
	}
	if len(event.Data) == 0 {
		return replayUsage{}, fmt.Errorf("provider usage event missing data")
	}
	var usage replayUsage
	if err := json.Unmarshal(event.Data, &usage); err != nil {
		return replayUsage{}, fmt.Errorf("invalid provider usage data: %w", err)
	}
	return usage, nil
}
