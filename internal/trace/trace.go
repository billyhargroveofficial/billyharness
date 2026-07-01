package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const CurrentManifestVersion = 1

type Manifest struct {
	SchemaVersion         int            `json:"schema_version"`
	RunID                 string         `json:"run_id"`
	CreatedAt             time.Time      `json:"created_at"`
	StartedAtMS           int64          `json:"started_at_unix_ms"`
	Harness               string         `json:"harness"`
	ProfileHash           string         `json:"profile_hash,omitempty"`
	TasksPath             string         `json:"tasks_path,omitempty"`
	TaskCount             int            `json:"task_count,omitempty"`
	ResultsJSONL          string         `json:"results_jsonl"`
	EventsJSONL           string         `json:"events_jsonl"`
	PayloadsDir           string         `json:"payloads_dir,omitempty"`
	ConfigSnapshot        map[string]any `json:"config_snapshot,omitempty"`
	ProviderModelMetadata map[string]any `json:"provider_model_metadata,omitempty"`
	MCPStatus             map[string]any `json:"mcp_status,omitempty"`
}

type EventRecord struct {
	SchemaVersion int          `json:"schema_version"`
	Seq           int64        `json:"seq"`
	RunID         string       `json:"run_id"`
	TaskID        string       `json:"task_id,omitempty"`
	Timestamp     time.Time    `json:"ts"`
	EventType     string       `json:"event_type,omitempty"`
	ProfileHash   string       `json:"profile_hash,omitempty"`
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
	mu            sync.Mutex
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
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	now := w.now()
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{
		Seq:    w.seq,
		Source: protocol.EventSourceBench,
		RunID:  w.runID,
		TS:     now.UTC().Format(time.RFC3339Nano),
	})
	event.Seq = w.seq
	if err := eventlog.ValidateEnvelope(event); err != nil {
		return EventRecord{}, err
	}
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
		slim := event
		slim.Data = nil
		recordEvent = slim
	}
	record := EventRecord{
		SchemaVersion: CurrentManifestVersion,
		Seq:           w.seq,
		RunID:         w.runID,
		TaskID:        taskID,
		Timestamp:     now,
		EventType:     string(event.Type),
		ProfileHash:   event.ProfileHash,
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
	RunID                  string                       `json:"run_id"`
	Records                int                          `json:"records"`
	FirstSeq               int64                        `json:"first_seq,omitempty"`
	LastSeq                int64                        `json:"last_seq,omitempty"`
	PayloadRefs            int                          `json:"payload_refs,omitempty"`
	PayloadBytes           int64                        `json:"payload_bytes,omitempty"`
	EventTypes             map[string]int               `json:"event_types"`
	Tasks                  map[string]int               `json:"tasks"`
	RunStarted             int                          `json:"run_started,omitempty"`
	RunCompleted           int                          `json:"run_completed,omitempty"`
	RunFailed              int                          `json:"run_failed,omitempty"`
	TurnsStarted           int                          `json:"turns_started,omitempty"`
	TurnsCompleted         int                          `json:"turns_completed,omitempty"`
	TurnsFailed            int                          `json:"turns_failed,omitempty"`
	StepsStarted           int                          `json:"steps_started,omitempty"`
	StepsCompleted         int                          `json:"steps_completed,omitempty"`
	StepsFailed            int                          `json:"steps_failed,omitempty"`
	ParallelBatches        int                          `json:"parallel_batches,omitempty"`
	FirstDeltaSamples      int                          `json:"first_delta_samples,omitempty"`
	FirstDeltaTotalMS      int64                        `json:"first_delta_total_ms,omitempty"`
	ModelLatencyMS         int64                        `json:"model_latency_ms,omitempty"`
	ToolLatencyMS          int64                        `json:"tool_latency_ms,omitempty"`
	ParallelBatchLatencyMS int64                        `json:"parallel_batch_latency_ms,omitempty"`
	ModelCallsStarted      int                          `json:"model_calls_started,omitempty"`
	ModelCallsFinished     int                          `json:"model_calls_finished,omitempty"`
	PromptInventories      int                          `json:"prompt_inventories,omitempty"`
	PromptCacheInitial     int                          `json:"prompt_cache_initial,omitempty"`
	PromptCacheUnchanged   int                          `json:"prompt_cache_unchanged,omitempty"`
	PromptCacheBreaks      int                          `json:"prompt_cache_breaks,omitempty"`
	ToolCallsStarted       int                          `json:"tool_calls_started,omitempty"`
	ToolCallProgress       int                          `json:"tool_call_progress,omitempty"`
	ToolCallsFinished      int                          `json:"tool_calls_finished,omitempty"`
	ContextThresholds      int                          `json:"context_thresholds,omitempty"`
	ContextCompactions     int                          `json:"context_compactions,omitempty"`
	OutputRefs             int                          `json:"output_refs,omitempty"`
	OutputRefBytes         int64                        `json:"output_ref_bytes,omitempty"`
	MissingOutputRefs      int                          `json:"missing_output_refs,omitempty"`
	OutputRefHashMismatch  int                          `json:"output_ref_hash_mismatch,omitempty"`
	OutputRefWarnings      []ReplayOutputRefWarning     `json:"output_ref_warnings,omitempty"`
	InputTokens            int64                        `json:"input_tokens,omitempty"`
	OutputTokens           int64                        `json:"output_tokens,omitempty"`
	CacheHitTokens         int64                        `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens        int64                        `json:"cache_miss_tokens,omitempty"`
	HelperModelCalls       int                          `json:"helper_model_calls,omitempty"`
	HelperInputTokens      int64                        `json:"helper_input_tokens,omitempty"`
	HelperOutputTokens     int64                        `json:"helper_output_tokens,omitempty"`
	HelperCacheHitTokens   int64                        `json:"helper_cache_hit_tokens,omitempty"`
	HelperCacheMissTokens  int64                        `json:"helper_cache_miss_tokens,omitempty"`
	HelperAPITokens        int64                        `json:"helper_api_tokens,omitempty"`
	HelperUsageByKind      map[string]ReplayHelperUsage `json:"helper_usage_by_kind,omitempty"`
	ProfileHashes          []string                     `json:"profile_hashes,omitempty"`
	Timeline               []ReplayTimelineItem         `json:"timeline,omitempty"`
	usage                  replayUsageAccumulator
}

type ReplayHelperUsage struct {
	Calls           int   `json:"calls,omitempty"`
	InputTokens     int64 `json:"input_tokens,omitempty"`
	OutputTokens    int64 `json:"output_tokens,omitempty"`
	CacheHitTokens  int64 `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens int64 `json:"cache_miss_tokens,omitempty"`
	APITokens       int64 `json:"api_tokens,omitempty"`
}

type ReplayOutputRefWarning struct {
	Seq            int64  `json:"seq,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	StepID         string `json:"step_id,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	AttemptID      string `json:"attempt_id,omitempty"`
	Name           string `json:"name,omitempty"`
	OutputRef      string `json:"output_ref,omitempty"`
	OutputRefID    string `json:"output_ref_id,omitempty"`
	ExpectedBytes  int64  `json:"expected_bytes,omitempty"`
	ActualBytes    int64  `json:"actual_bytes,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	Reason         string `json:"reason"`
	Error          string `json:"error,omitempty"`
}

type ReplayTimelineItem struct {
	Seq                 int64  `json:"seq"`
	TaskID              string `json:"task_id,omitempty"`
	EventType           string `json:"event_type"`
	Source              string `json:"source,omitempty"`
	SubmissionID        string `json:"submission_id,omitempty"`
	RunID               string `json:"run_id,omitempty"`
	TurnID              string `json:"turn_id,omitempty"`
	StepID              string `json:"step_id,omitempty"`
	ParentStepID        string `json:"parent_step_id,omitempty"`
	CallID              string `json:"call_id,omitempty"`
	AttemptID           string `json:"attempt_id,omitempty"`
	ProfileHash         string `json:"profile_hash,omitempty"`
	Round               int    `json:"round,omitempty"`
	Index               int    `json:"index,omitempty"`
	Kind                string `json:"kind,omitempty"`
	Phase               string `json:"phase,omitempty"`
	Status              string `json:"status,omitempty"`
	Name                string `json:"name,omitempty"`
	StopReason          string `json:"stop_reason,omitempty"`
	BatchID             string `json:"batch_id,omitempty"`
	BatchSize           int    `json:"batch_size,omitempty"`
	Parallel            bool   `json:"parallel,omitempty"`
	DurationMS          int64  `json:"duration_ms,omitempty"`
	PromptInventoryHash string `json:"prompt_inventory_hash,omitempty"`
	PromptCacheStatus   string `json:"prompt_cache_status,omitempty"`
	PromptCacheReason   string `json:"prompt_cache_reason,omitempty"`
}

func ReplayEvents(path string) (ReplaySummary, error) {
	summary := ReplaySummary{
		EventTypes:        map[string]int{},
		Tasks:             map[string]int{},
		HelperUsageByKind: map[string]ReplayHelperUsage{},
	}
	validator := eventlog.NewRecordValidator(eventlog.RecordValidatorOptions{
		SchemaVersion:    CurrentManifestVersion,
		ScopeName:        "run_id",
		ValidateEnvelope: true,
	})
	lifecycle := eventlog.NewLifecycleValidator()
	err := eventlog.ReplayJSONL[EventRecord](path, eventlog.JSONLOptions{}, func(item eventlog.JSONLRecord[EventRecord]) error {
		record := item.Value
		event, ok, err := protocolEventFromRecord(record.Event)
		if err != nil {
			return eventlog.NewCorruptionError(path, item.Line, item.RecordNo, "", err)
		}
		recordNo := validator.NextSeq()
		if err := validator.Validate(eventlog.Record{
			SchemaVersion: record.SchemaVersion,
			Seq:           record.Seq,
			ScopeID:       record.RunID,
			EventType:     record.EventType,
			Event:         event,
			HasEvent:      ok,
		}); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}

		if summary.RunID == "" {
			summary.RunID = validator.ScopeID()
			summary.FirstSeq = validator.FirstSeq()
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
			return eventlog.NewCorruptionError(path, item.Line, item.RecordNo, "", err)
		}
		summary.PayloadBytes += bytes
		if ok {
			if err := lifecycle.Observe(event); err != nil {
				return eventlog.NewCorruptionError(path, item.Line, item.RecordNo, "lifecycle", err)
			}
		}
		if err := summary.observe(record, event, ok); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, item.RecordNo, "", err)
		}
		return nil
	})
	if err != nil {
		return summary, err
	}
	return summary, nil
}

func protocolEventFromRecord(value any) (protocol.Event, bool, error) {
	if value == nil {
		return protocol.Event{}, false, nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return protocol.Event{}, false, fmt.Errorf("invalid protocol event: %w", err)
	}
	var event protocol.Event
	if err := json.Unmarshal(bytes, &event); err != nil {
		return protocol.Event{}, false, fmt.Errorf("invalid protocol event: %w", err)
	}
	if event.Type == "" {
		return protocol.Event{}, false, nil
	}
	return event, true, nil
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

func (s *ReplaySummary) observe(record EventRecord, event protocol.Event, hasEvent bool) error {
	if hasEvent {
		s.addProfileHash(firstString(event.ProfileHash, record.ProfileHash))
		if err := s.appendTimeline(record, event); err != nil {
			return err
		}
	} else {
		s.addProfileHash(record.ProfileHash)
	}
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
		switch step.Kind {
		case protocol.StepKindModelCall:
			s.ModelLatencyMS += step.DurationMS
			if firstDelta := metadataInt64(step.Metadata, "first_delta_ms"); firstDelta > 0 {
				s.FirstDeltaSamples++
				s.FirstDeltaTotalMS += firstDelta
			}
		case protocol.StepKindToolCall:
			s.ToolLatencyMS += step.DurationMS
		case protocol.StepKindToolBatch:
			s.ParallelBatchLatencyMS += step.DurationMS
		}
		if step.Status == protocol.StepStatusFailed {
			s.StepsFailed++
		}
	case protocol.EventModelCallStarted:
		s.ModelCallsStarted++
		s.usage.Reset()
		if err := s.observePromptDiagnostics(event); err != nil {
			return err
		}
	case protocol.EventModelCallFinished:
		s.ModelCallsFinished++
	case protocol.EventToolCallStarted:
		s.ToolCallsStarted++
	case protocol.EventToolCallProgress:
		s.ToolCallProgress++
	case protocol.EventToolCallFinished:
		s.ToolCallsFinished++
	case protocol.EventToolOutputRefCreated:
		s.observeOutputRef(event)
	case protocol.EventContextThreshold:
		s.ContextThresholds++
	case protocol.EventContextCompacted:
		s.ContextCompactions++
	case protocol.EventProviderUsageUpdate:
		usage, err := usageFromEvent(record.Event)
		if err != nil {
			return err
		}
		delta := s.usage.Apply(usage)
		s.InputTokens += delta.InputTokens
		s.OutputTokens += delta.OutputTokens
		s.CacheHitTokens += delta.CacheHitTokens
		s.CacheMissTokens += delta.CacheMissTokens
	case protocol.EventProviderHelperUsage:
		usage, err := helperUsageFromEvent(record.Event)
		if err != nil {
			return err
		}
		s.observeHelperUsage(usage)
	}
	return nil
}

func (s *ReplaySummary) observeOutputRef(event protocol.Event) {
	if s == nil {
		return
	}
	s.OutputRefs++
	ref, err := toolOutputRefFromEvent(event)
	if err != nil {
		s.MissingOutputRefs++
		s.OutputRefWarnings = append(s.OutputRefWarnings, ReplayOutputRefWarning{
			Seq:    event.Seq,
			RunID:  event.RunID,
			Reason: "invalid_event",
			Error:  err.Error(),
		})
		return
	}
	warning := ReplayOutputRefWarning{
		Seq:            event.Seq,
		RunID:          event.RunID,
		TurnID:         event.TurnID,
		StepID:         event.StepID,
		CallID:         firstString(ref.CallID, event.CallID),
		AttemptID:      firstString(ref.AttemptID, event.AttemptID),
		Name:           ref.Name,
		OutputRef:      ref.OutputRef,
		OutputRefID:    ref.OutputRefID,
		ExpectedBytes:  ref.OutputRefBytes,
		ExpectedSHA256: ref.OutputRefSHA256,
	}
	if strings.TrimSpace(ref.OutputRef) == "" {
		warning.Reason = "missing_path"
		s.MissingOutputRefs++
		s.OutputRefWarnings = append(s.OutputRefWarnings, warning)
		return
	}
	info, err := os.Stat(ref.OutputRef)
	if err != nil {
		warning.Reason = "missing"
		warning.Error = err.Error()
		s.MissingOutputRefs++
		s.OutputRefWarnings = append(s.OutputRefWarnings, warning)
		return
	}
	if info.IsDir() {
		warning.Reason = "is_directory"
		s.MissingOutputRefs++
		s.OutputRefWarnings = append(s.OutputRefWarnings, warning)
		return
	}
	warning.ActualBytes = info.Size()
	s.OutputRefBytes += info.Size()
	if ref.OutputRefBytes > 0 && info.Size() != ref.OutputRefBytes {
		warning.Reason = "size_mismatch"
		s.OutputRefHashMismatch++
		s.OutputRefWarnings = append(s.OutputRefWarnings, warning)
		return
	}
	if ref.OutputRefSHA256 == "" {
		return
	}
	ok, err := fileSHA256Matches(ref.OutputRef, ref.OutputRefSHA256)
	if err != nil {
		warning.Reason = "hash_error"
		warning.Error = err.Error()
		s.OutputRefHashMismatch++
		s.OutputRefWarnings = append(s.OutputRefWarnings, warning)
		return
	}
	if !ok {
		warning.Reason = "sha256_mismatch"
		s.OutputRefHashMismatch++
		s.OutputRefWarnings = append(s.OutputRefWarnings, warning)
	}
}

func (s *ReplaySummary) observeHelperUsage(usage protocol.ProviderHelperUsageEvent) {
	inputTokens := nonNegativeInt64(usage.InputTokens)
	outputTokens := nonNegativeInt64(usage.OutputTokens)
	apiTokens := nonNegativeInt64(usage.APITokens)
	if apiTokens == 0 {
		apiTokens = inputTokens + outputTokens
	}
	cacheHit := nonNegativeInt64(usage.CacheHitTokens)
	cacheMiss := nonNegativeInt64(usage.CacheMissTokens)
	s.HelperModelCalls++
	s.HelperInputTokens += inputTokens
	s.HelperOutputTokens += outputTokens
	s.HelperCacheHitTokens += cacheHit
	s.HelperCacheMissTokens += cacheMiss
	s.HelperAPITokens += apiTokens
	kind := strings.TrimSpace(usage.Kind)
	if kind == "" {
		kind = "unknown"
	}
	if s.HelperUsageByKind == nil {
		s.HelperUsageByKind = map[string]ReplayHelperUsage{}
	}
	byKind := s.HelperUsageByKind[kind]
	byKind.Calls++
	byKind.InputTokens += inputTokens
	byKind.OutputTokens += outputTokens
	byKind.CacheHitTokens += cacheHit
	byKind.CacheMissTokens += cacheMiss
	byKind.APITokens += apiTokens
	s.HelperUsageByKind[kind] = byKind
}

func (s *ReplaySummary) observePromptDiagnostics(event protocol.Event) error {
	model, err := modelCallFromEvent(event)
	if err != nil {
		return err
	}
	if model.PromptInventory != nil {
		s.PromptInventories++
	}
	if model.PromptCacheBreak == nil {
		return nil
	}
	switch model.PromptCacheBreak.Status {
	case "initial":
		s.PromptCacheInitial++
	case "unchanged":
		s.PromptCacheUnchanged++
	case "changed":
		s.PromptCacheBreaks++
	}
	return nil
}

func (s *ReplaySummary) appendTimeline(record EventRecord, event protocol.Event) error {
	if !isReplayTimelineEvent(event.Type) {
		return nil
	}
	item := ReplayTimelineItem{
		Seq:          firstInt64(event.Seq, record.Seq),
		TaskID:       record.TaskID,
		EventType:    string(event.Type),
		Source:       string(event.Source),
		SubmissionID: event.SubmissionID,
		RunID:        firstString(event.RunID, record.RunID),
		TurnID:       event.TurnID,
		StepID:       event.StepID,
		ParentStepID: event.ParentStepID,
		CallID:       event.CallID,
		AttemptID:    event.AttemptID,
		ProfileHash:  firstString(event.ProfileHash, record.ProfileHash),
		DurationMS:   event.DurationMS,
	}

	switch event.Type {
	case protocol.EventTurnStarted, protocol.EventTurnCompleted:
		turn, err := turnFromEvent(event)
		if err != nil {
			return err
		}
		item.TurnID = firstString(item.TurnID, turn.TurnID)
		item.Round = turn.Round
		item.Status = turn.Status
		item.Name = turn.Model
		item.StopReason = turn.StopReason
		item.DurationMS = firstInt64(item.DurationMS, turn.DurationMS)
	case protocol.EventStepStarted, protocol.EventStepCompleted:
		step, err := stepFromEvent(event)
		if err != nil {
			return err
		}
		item.TurnID = firstString(item.TurnID, step.TurnID)
		item.StepID = firstString(item.StepID, step.StepID)
		item.CallID = firstString(item.CallID, step.ToolCallID)
		item.ParentStepID = firstString(item.ParentStepID, step.BatchID)
		item.Round = step.Round
		item.Index = step.Index
		item.Kind = step.Kind
		item.Status = step.Status
		item.Name = step.Name
		item.BatchID = step.BatchID
		item.BatchSize = step.BatchSize
		item.Parallel = step.Parallel
		item.DurationMS = firstInt64(item.DurationMS, step.DurationMS)
	case protocol.EventModelCallStarted:
		item.Kind = protocol.StepKindModelCall
		item.Status = protocol.StepStatusStarted
		applyTimelineMapData(&item, event.Data)
	case protocol.EventModelCallFinished:
		item.Kind = protocol.StepKindModelCall
		item.Status = protocol.StepStatusCompleted
		applyTimelineMapData(&item, event.Data)
	case protocol.EventToolCallRequested:
		item.Kind = protocol.StepKindToolCall
		item.Status = "requested"
		applyTimelineToolCallData(&item, event.Data)
	case protocol.EventToolPermissionRequested:
		item.Kind = protocol.StepKindToolCall
		item.Status = "permission_requested"
		applyTimelineMapData(&item, event.Data)
	case protocol.EventToolPermissionDecided:
		item.Kind = protocol.StepKindToolCall
		item.Status = "permission_decided"
		applyTimelineMapData(&item, event.Data)
	case protocol.EventToolCallStarted:
		item.Kind = protocol.StepKindToolCall
		item.Status = protocol.StepStatusStarted
		applyTimelineToolNameData(&item, event.Data)
	case protocol.EventToolCallProgress:
		item.Kind = protocol.StepKindToolCall
		applyTimelineToolProgressData(&item, event.Data)
	case protocol.EventToolCallFinished:
		item.Kind = protocol.StepKindToolCall
		item.Status = protocol.StepStatusCompleted
		applyTimelineToolResultData(&item, event.Data)
	case protocol.EventToolCallFailed:
		item.Kind = protocol.StepKindToolCall
		item.Status = protocol.StepStatusFailed
		applyTimelineToolResultData(&item, event.Data)
	case protocol.EventToolCallAborted:
		item.Kind = protocol.StepKindToolCall
		item.Status = "aborted"
		applyTimelineToolResultData(&item, event.Data)
	case protocol.EventToolOutputRefCreated:
		item.Kind = protocol.StepKindToolCall
		item.Status = "output_ref_created"
		applyTimelineMapData(&item, event.Data)
	case protocol.EventContextThreshold:
		item.Kind = "context_threshold"
		item.Status = protocol.StepStatusCompleted
		applyTimelineMapData(&item, event.Data)
	case protocol.EventContextCompacted:
		item.Kind = "context_compaction"
		item.Status = protocol.StepStatusCompleted
	case protocol.EventProviderHelperUsage:
		item.Kind = "helper_usage"
		item.Status = protocol.StepStatusCompleted
		if usage, err := helperUsageFromEvent(event); err == nil {
			item.Name = usage.Kind
			item.CallID = firstString(item.CallID, usage.CallID)
			item.AttemptID = firstString(item.AttemptID, usage.AttemptID)
		}
	case protocol.EventHookStarted:
		item.Kind = "hook"
		item.Status = protocol.StepStatusStarted
		applyTimelineMapData(&item, event.Data)
	case protocol.EventHookFinished:
		item.Kind = "hook"
		item.Status = protocol.StepStatusCompleted
		applyTimelineMapData(&item, event.Data)
	case protocol.EventHookFailed:
		item.Kind = "hook"
		item.Status = protocol.StepStatusFailed
		applyTimelineMapData(&item, event.Data)
	}
	s.Timeline = append(s.Timeline, item)
	return nil
}

func isReplayTimelineEvent(eventType protocol.EventType) bool {
	switch eventType {
	case protocol.EventRunStarted,
		protocol.EventRunCompleted,
		protocol.EventRunFailed,
		protocol.EventTurnStarted,
		protocol.EventTurnCompleted,
		protocol.EventStepStarted,
		protocol.EventStepCompleted,
		protocol.EventModelCallStarted,
		protocol.EventModelCallFinished,
		protocol.EventToolCallRequested,
		protocol.EventToolPermissionRequested,
		protocol.EventToolPermissionDecided,
		protocol.EventToolCallStarted,
		protocol.EventToolCallProgress,
		protocol.EventToolCallFinished,
		protocol.EventToolCallFailed,
		protocol.EventToolCallAborted,
		protocol.EventToolOutputRefCreated,
		protocol.EventContextThreshold,
		protocol.EventContextCompacted,
		protocol.EventProviderHelperUsage,
		protocol.EventHookStarted,
		protocol.EventHookFinished,
		protocol.EventHookFailed:
		return true
	default:
		return false
	}
}

func (s *ReplaySummary) addProfileHash(hash string) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return
	}
	for _, existing := range s.ProfileHashes {
		if existing == hash {
			return
		}
	}
	s.ProfileHashes = append(s.ProfileHashes, hash)
}

func applyTimelineToolCallData(item *ReplayTimelineItem, data any) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	var call protocol.ToolCall
	if err := json.Unmarshal(bytes, &call); err == nil {
		item.CallID = firstString(item.CallID, call.ID)
		item.Name = firstString(item.Name, call.Name)
		return
	}
	applyTimelineMapData(item, data)
}

func applyTimelineToolResultData(item *ReplayTimelineItem, data any) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err == nil {
		item.CallID = firstString(item.CallID, result.CallID)
		item.Name = firstString(item.Name, result.Name)
		if result.IsError && item.Status == protocol.StepStatusCompleted {
			item.Status = protocol.StepStatusFailed
		}
		applyTimelineMap(item, result.Metadata)
		return
	}
	applyTimelineMapData(item, data)
}

func applyTimelineToolProgressData(item *ReplayTimelineItem, data any) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	var progress protocol.ToolProgressEvent
	if err := json.Unmarshal(bytes, &progress); err == nil {
		item.CallID = firstString(item.CallID, progress.CallID)
		item.AttemptID = firstString(item.AttemptID, progress.AttemptID)
		item.Name = firstString(item.Name, progress.Name)
		item.Phase = firstString(item.Phase, progress.Phase)
		item.Status = firstString(progress.Status, item.Status)
		applyTimelineMap(item, progress.Metadata)
		return
	}
	applyTimelineMapData(item, data)
}

func applyTimelineToolNameData(item *ReplayTimelineItem, data any) {
	switch value := data.(type) {
	case string:
		item.Name = firstString(item.Name, value)
	default:
		applyTimelineToolCallData(item, data)
	}
}

func applyTimelineMapData(item *ReplayTimelineItem, data any) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(bytes, &m); err != nil {
		return
	}
	applyTimelineMap(item, m)
}

func applyTimelineMap(item *ReplayTimelineItem, m map[string]any) {
	if len(m) == 0 {
		return
	}
	item.SubmissionID = firstString(item.SubmissionID, mapString(m, "submission_id"))
	item.RunID = firstString(item.RunID, mapString(m, "run_id"))
	item.TurnID = firstString(item.TurnID, mapString(m, "turn_id"))
	item.StepID = firstString(item.StepID, mapString(m, "step_id"))
	item.ParentStepID = firstString(item.ParentStepID, firstString(mapString(m, "parent_step_id"), mapString(m, "batch_id")))
	item.CallID = firstString(item.CallID, firstString(mapString(m, "call_id"), mapString(m, "tool_call_id")))
	item.AttemptID = firstString(item.AttemptID, mapString(m, "attempt_id"))
	item.Kind = firstString(item.Kind, mapString(m, "kind"))
	item.Status = firstString(mapString(m, "status"), item.Status)
	item.Name = firstString(item.Name, firstString(mapString(m, "name"), mapString(m, "tool_name"), mapString(m, "model")))
	item.DurationMS = firstInt64(item.DurationMS, mapInt64(m, "duration_ms"))
	item.PromptInventoryHash = firstString(item.PromptInventoryHash, mapString(m, "prompt_inventory_hash"))
	item.PromptCacheStatus = firstString(item.PromptCacheStatus, mapString(m, "prompt_cache_status"))
	item.PromptCacheReason = firstString(item.PromptCacheReason, mapString(m, "prompt_cache_reason"))
	if nested, ok := m["prompt_cache_break"].(map[string]any); ok {
		item.PromptCacheStatus = firstString(item.PromptCacheStatus, mapString(nested, "status"))
		item.PromptCacheReason = firstString(item.PromptCacheReason, mapString(nested, "reason"))
	}
	if nested, ok := m["prompt_inventory"].(map[string]any); ok {
		item.PromptInventoryHash = firstString(item.PromptInventoryHash, mapString(nested, "hash"))
	}
}

func modelCallFromEvent(event protocol.Event) (protocol.ModelCallEvent, error) {
	bytes, err := json.Marshal(event.Data)
	if err != nil {
		return protocol.ModelCallEvent{}, fmt.Errorf("invalid model call event: %w", err)
	}
	var model protocol.ModelCallEvent
	if err := json.Unmarshal(bytes, &model); err != nil {
		return protocol.ModelCallEvent{}, fmt.Errorf("invalid model call event: %w", err)
	}
	return model, nil
}

func toolOutputRefFromEvent(event protocol.Event) (protocol.ToolOutputRefEvent, error) {
	bytes, err := json.Marshal(event.Data)
	if err != nil {
		return protocol.ToolOutputRefEvent{}, fmt.Errorf("invalid output ref event: %w", err)
	}
	var ref protocol.ToolOutputRefEvent
	if err := json.Unmarshal(bytes, &ref); err != nil {
		return protocol.ToolOutputRefEvent{}, fmt.Errorf("invalid output ref event: %w", err)
	}
	return ref, nil
}

func fileSHA256Matches(path, want string) (bool, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(bytes)
	return strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(want)), nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func mapString(m map[string]any, key string) string {
	switch value := m[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func mapInt64(m map[string]any, key string) int64 {
	switch value := m[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
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

func metadataInt64(metadata map[string]any, key string) int64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	}
	return 0
}

type replayUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheHitTokens  int64 `json:"cache_hit_tokens"`
	CacheMissTokens int64 `json:"cache_miss_tokens"`
}

type replayUsageAccumulator struct {
	last    replayUsage
	hasLast bool
}

func (a *replayUsageAccumulator) Reset() {
	a.last = replayUsage{}
	a.hasLast = false
}

func (a *replayUsageAccumulator) Apply(update replayUsage) replayUsage {
	if update == (replayUsage{}) {
		return replayUsage{}
	}
	if !a.hasLast {
		a.last = update
		a.hasLast = true
		return update
	}
	if update == a.last {
		return replayUsage{}
	}
	if update.atLeast(a.last) {
		delta := update.minus(a.last)
		a.last = update
		return delta
	}
	a.last = update
	return update
}

func (u replayUsage) atLeast(other replayUsage) bool {
	return u.InputTokens >= other.InputTokens &&
		u.OutputTokens >= other.OutputTokens &&
		u.CacheHitTokens >= other.CacheHitTokens &&
		u.CacheMissTokens >= other.CacheMissTokens
}

func (u replayUsage) minus(other replayUsage) replayUsage {
	return replayUsage{
		InputTokens:     u.InputTokens - other.InputTokens,
		OutputTokens:    u.OutputTokens - other.OutputTokens,
		CacheHitTokens:  u.CacheHitTokens - other.CacheHitTokens,
		CacheMissTokens: u.CacheMissTokens - other.CacheMissTokens,
	}
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

func helperUsageFromEvent(value any) (protocol.ProviderHelperUsageEvent, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return protocol.ProviderHelperUsageEvent{}, fmt.Errorf("invalid provider helper usage event: %w", err)
	}
	var event struct {
		Type protocol.EventType `json:"type"`
		Data json.RawMessage    `json:"data"`
	}
	if err := json.Unmarshal(bytes, &event); err != nil {
		return protocol.ProviderHelperUsageEvent{}, fmt.Errorf("invalid provider helper usage event: %w", err)
	}
	if event.Type != "" && event.Type != protocol.EventProviderHelperUsage {
		return protocol.ProviderHelperUsageEvent{}, fmt.Errorf("invalid provider helper usage event type %q", event.Type)
	}
	if len(event.Data) == 0 {
		return protocol.ProviderHelperUsageEvent{}, fmt.Errorf("provider helper usage event missing data")
	}
	var usage protocol.ProviderHelperUsageEvent
	if err := json.Unmarshal(event.Data, &usage); err != nil {
		return protocol.ProviderHelperUsageEvent{}, fmt.Errorf("invalid provider helper usage data: %w", err)
	}
	return usage, nil
}
