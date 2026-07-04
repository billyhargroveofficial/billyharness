package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	clientprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type StoredSessionList struct {
	Dir      string                 `json:"dir"`
	Sessions []StoredSessionSummary `json:"sessions"`
	Warnings []string               `json:"warnings,omitempty"`
}

type StoredSessionSummary struct {
	ID                 string                  `json:"id"`
	CreatedAt          time.Time               `json:"created_at,omitempty"`
	UpdatedAt          time.Time               `json:"updated_at,omitempty"`
	MessageCount       int                     `json:"message_count"`
	HistorySeq         int64                   `json:"history_seq,omitempty"`
	EventSeq           int64                   `json:"event_seq,omitempty"`
	LastEvent          string                  `json:"last_event,omitempty"`
	Owner              gatewayapi.SessionOwner `json:"owner,omitempty"`
	Legacy             bool                    `json:"legacy,omitempty"`
	OfflineReplayReady bool                    `json:"offline_replay_ready"`
}

type StoredSessionInspection struct {
	Dir                string                         `json:"dir"`
	SessionID          string                         `json:"session_id"`
	SessionDir         string                         `json:"session_dir,omitempty"`
	Legacy             bool                           `json:"legacy,omitempty"`
	Manifest           StoredSessionManifest          `json:"manifest,omitempty"`
	Files              []StoredSessionFile            `json:"files,omitempty"`
	History            StoredSessionHistoryInspection `json:"history,omitempty"`
	Events             StoredSessionEventsInspection  `json:"events,omitempty"`
	MessageCount       int                            `json:"message_count,omitempty"`
	OfflineReplayReady bool                           `json:"offline_replay_ready"`
	Warnings           []string                       `json:"warnings,omitempty"`
}

type StoredSessionManifest struct {
	SchemaVersion             int                     `json:"schema_version,omitempty"`
	SessionID                 string                  `json:"session_id,omitempty"`
	CreatedAt                 time.Time               `json:"created_at,omitempty"`
	UpdatedAt                 time.Time               `json:"updated_at,omitempty"`
	HistoryJSONL              string                  `json:"history_jsonl,omitempty"`
	EventsJSONL               string                  `json:"events_jsonl,omitempty"`
	SnapshotJSON              string                  `json:"snapshot_json,omitempty"`
	ConfigSnapshotJSON        string                  `json:"config_snapshot_json,omitempty"`
	ModelProviderSnapshotJSON string                  `json:"model_provider_snapshot_json,omitempty"`
	MCPSnapshotJSON           string                  `json:"mcp_snapshot_json,omitempty"`
	HistorySeq                int64                   `json:"history_seq,omitempty"`
	EventSeq                  int64                   `json:"event_seq,omitempty"`
	MessageCount              int                     `json:"message_count,omitempty"`
	Owner                     gatewayapi.SessionOwner `json:"owner,omitempty"`
	HistorySHA256             string                  `json:"history_sha256,omitempty"`
}

type StoredSessionFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type StoredSessionHistoryInspection struct {
	Path          string `json:"path,omitempty"`
	Exists        bool   `json:"exists"`
	Records       int64  `json:"records,omitempty"`
	LastSeq       int64  `json:"last_seq,omitempty"`
	LastKind      string `json:"last_kind,omitempty"`
	MessageCount  int    `json:"message_count,omitempty"`
	HistorySHA256 string `json:"history_sha256,omitempty"`
}

type StoredSessionEventsInspection struct {
	Path                  string                              `json:"path,omitempty"`
	Exists                bool                                `json:"exists"`
	Records               int                                 `json:"records,omitempty"`
	LastSeq               int64                               `json:"last_seq,omitempty"`
	LastEvent             string                              `json:"last_event,omitempty"`
	Validation            StoredSessionEventValidation        `json:"validation"`
	Terminal              StoredSessionTerminalInspection     `json:"terminal"`
	Projector             StoredSessionProjectorInspection    `json:"projector"`
	OutputRefs            int                                 `json:"output_refs,omitempty"`
	OutputRefsVerified    bool                                `json:"output_refs_verified,omitempty"`
	OutputRefBytes        int64                               `json:"output_ref_bytes,omitempty"`
	MissingOutputRefs     int                                 `json:"missing_output_refs,omitempty"`
	OutputRefHashMismatch int                                 `json:"output_ref_hash_mismatch,omitempty"`
	OutputRefWarnings     []StoredSessionOutputRefWarning     `json:"output_ref_warnings,omitempty"`
	EventTypes            map[string]int                      `json:"event_types,omitempty"`
	TurnChanges           []StoredSessionTurnChangeInspection `json:"turn_changes,omitempty"`
	RedoAvailable         bool                                `json:"redo_available,omitempty"`
	RedoChangeID          string                              `json:"redo_change_id,omitempty"`
}

type StoredSessionEventValidation struct {
	Valid                bool   `json:"valid"`
	EnvelopeValid        bool   `json:"envelope_valid"`
	SequenceValid        bool   `json:"sequence_valid"`
	LifecycleValid       bool   `json:"lifecycle_valid"`
	SequenceGaps         int    `json:"sequence_gaps,omitempty"`
	DuplicateSeqs        int    `json:"duplicate_seqs,omitempty"`
	UnmatchedProgress    int    `json:"unmatched_progress,omitempty"`
	UnmatchedOutputRefs  int    `json:"unmatched_output_refs,omitempty"`
	UnmatchedPermissions int    `json:"unmatched_permissions,omitempty"`
	CorruptionKind       string `json:"corruption_kind,omitempty"`
	Line                 int    `json:"line,omitempty"`
	RecordNo             int64  `json:"record_no,omitempty"`
	Error                string `json:"error,omitempty"`
}

type StoredSessionTerminalInspection struct {
	State     string `json:"state"`
	Event     string `json:"event,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Seq       int64  `json:"seq,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type StoredSessionProjectorInspection struct {
	ParityOK           bool                          `json:"parity_ok"`
	SessionID          string                        `json:"session_id,omitempty"`
	SeqRange           string                        `json:"seq_range,omitempty"`
	LastSeq            int64                         `json:"last_seq,omitempty"`
	SnapshotLastSeq    int64                         `json:"snapshot_last_seq,omitempty"`
	RunState           string                        `json:"run_state,omitempty"`
	TranscriptEvents   int                           `json:"transcript_events,omitempty"`
	ToolCallsRaw       int                           `json:"tool_calls_raw,omitempty"`
	ToolCallsProjected int                           `json:"tool_calls_projected,omitempty"`
	AssistantBytes     int                           `json:"assistant_bytes,omitempty"`
	ReasoningBytes     int                           `json:"reasoning_bytes,omitempty"`
	ProjectionHash     string                        `json:"projection_hash,omitempty"`
	LastEventID        string                        `json:"last_event_id,omitempty"`
	MismatchReasons    []string                      `json:"mismatch_reasons,omitempty"`
	SeqGap             *StoredSessionProjectorSeqGap `json:"seq_gap,omitempty"`
}

type StoredSessionProjectorSeqGap struct {
	AfterSeq int64 `json:"after_seq"`
	GotSeq   int64 `json:"got_seq"`
}

const (
	storedSessionRunStateIdle      = "idle"
	storedSessionRunStateRunning   = "running"
	storedSessionRunStateCompleted = "completed"
	storedSessionRunStateFailed    = "failed"
)

type StoredSessionOutputRefWarning struct {
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

type StoredSessionTurnChangeInspection struct {
	Seq            int64                     `json:"seq,omitempty"`
	EventType      string                    `json:"event_type,omitempty"`
	ChangeID       string                    `json:"change_id,omitempty"`
	Status         string                    `json:"status,omitempty"`
	ToolName       string                    `json:"tool_name,omitempty"`
	FileCount      int                       `json:"file_count,omitempty"`
	Added          int                       `json:"added,omitempty"`
	Modified       int                       `json:"modified,omitempty"`
	Deleted        int                       `json:"deleted,omitempty"`
	Additions      int                       `json:"additions,omitempty"`
	Deletions      int                       `json:"deletions,omitempty"`
	PatchOutputRef string                    `json:"patch_output_ref,omitempty"`
	Files          []protocol.TurnChangeFile `json:"files,omitempty"`
}

func ListStoredSessions(dir string) (StoredSessionList, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = DefaultSessionStoreDir()
	}
	out := StoredSessionList{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == sessionIndexDirName {
			continue
		}
		id := entry.Name()
		inspection, err := InspectStoredSession(dir, id)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		out.Sessions = append(out.Sessions, inspectionSummary(inspection))
		seen[inspection.SessionID] = struct{}{}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if _, ok := seen[id]; ok {
			continue
		}
		inspection, err := InspectStoredSession(dir, id)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		out.Sessions = append(out.Sessions, inspectionSummary(inspection))
		seen[inspection.SessionID] = struct{}{}
	}
	sort.Slice(out.Sessions, func(i, j int) bool {
		return out.Sessions[i].UpdatedAt.After(out.Sessions[j].UpdatedAt)
	})
	return out, nil
}

func InspectStoredSession(dir, id string) (StoredSessionInspection, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = DefaultSessionStoreDir()
	}
	cleanID, err := cleanSessionID(id)
	if err != nil {
		return StoredSessionInspection{}, err
	}
	sessionDir := filepath.Join(dir, cleanID)
	out := StoredSessionInspection{
		Dir:        dir,
		SessionID:  cleanID,
		SessionDir: sessionDir,
	}
	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	manifest, err := readSessionManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inspectLegacyStoredSession(dir, cleanID)
		}
		return out, err
	}
	out.Manifest = storedSessionManifest(manifest)
	out.Files = append(out.Files,
		inspectStoredSessionFile("manifest", manifestPath),
		inspectStoredSessionFile("history", filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName))),
		inspectStoredSessionFile("events", filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))),
		inspectStoredSessionFile("legacy_snapshot", filepath.Join(dir, sessionFileName(manifest.SnapshotJSON, cleanID+".json"))),
		inspectStoredSessionFile("config_snapshot", filepath.Join(sessionDir, sessionFileName(manifest.ConfigSnapshotJSON, sessionConfigSnapshotName))),
		inspectStoredSessionFile("model_provider_snapshot", filepath.Join(sessionDir, sessionFileName(manifest.ModelProviderSnapshotJSON, sessionModelSnapshotName))),
		inspectStoredSessionFile("mcp_snapshot", filepath.Join(sessionDir, sessionFileName(manifest.MCPSnapshotJSON, sessionMCPSnapshotName))),
	)
	historyPath := filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName))
	history, err := replaySessionHistory(historyPath, cleanID)
	if err != nil {
		return out, err
	}
	out.History = StoredSessionHistoryInspection{
		Path:          historyPath,
		Exists:        fileExists(historyPath),
		Records:       history.lastSeq,
		LastSeq:       history.lastSeq,
		MessageCount:  len(history.messages),
		HistorySHA256: history.historySHA256,
	}
	if history.lastSeq == 1 {
		out.History.LastKind = sessionHistoryCreated
	} else if history.lastSeq > 1 {
		out.History.LastKind = sessionHistorySnapshot
	}
	eventsPath := filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	events, err := replaySessionEventsAfter(eventsPath, cleanID, 0)
	if err != nil {
		out.Events = inspectSessionEvents(cleanID, eventsPath, nil)
		out.Events.Validation = storedSessionEventValidationFromError(dir, err)
		out.Warnings = append(out.Warnings, "event replay invalid: "+out.Events.Validation.Error)
	} else {
		out.Events = inspectSessionEvents(cleanID, eventsPath, events)
		for _, warning := range out.Events.OutputRefWarnings {
			out.Warnings = append(out.Warnings, formatStoredOutputRefWarning(warning))
		}
	}
	out.MessageCount = len(history.messages)
	out.OfflineReplayReady = out.History.Exists && out.History.Records > 0 && out.Events.Validation.Valid
	if !hasExistingFile(out.Files, "config_snapshot") {
		out.Warnings = append(out.Warnings, "config snapshot missing")
	}
	if !hasExistingFile(out.Files, "model_provider_snapshot") {
		out.Warnings = append(out.Warnings, "model/provider snapshot missing")
	}
	if !hasExistingFile(out.Files, "mcp_snapshot") {
		out.Warnings = append(out.Warnings, "MCP snapshot missing")
	}
	return out, nil
}

func StoredSessionContext(dir, id string, limits config.RuntimeLimits) (gatewayapi.SessionContextResponse, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = DefaultSessionStoreDir()
	}
	cleanID, err := cleanSessionID(id)
	if err != nil {
		return gatewayapi.SessionContextResponse{}, err
	}
	sessionDir := filepath.Join(dir, cleanID)
	manifest, err := readSessionManifest(filepath.Join(sessionDir, sessionManifestName))
	if err != nil {
		return gatewayapi.SessionContextResponse{}, err
	}
	history, err := replaySessionHistory(filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName)), cleanID)
	if err != nil {
		return gatewayapi.SessionContextResponse{}, err
	}
	var warnings []string
	limits, snapshotWarnings := storedSessionRuntimeLimits(sessionDir, manifest.ConfigSnapshotJSON, limits)
	warnings = append(warnings, snapshotWarnings...)
	events, err := replaySessionEventsAfter(filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName)), cleanID, 0)
	if err != nil {
		warnings = append(warnings, "event replay unavailable: "+err.Error())
	}
	return clientux.BuildContextResponseWithOptions(limits, cleanID, history.messages, clientux.ContextReportOptions{
		Memory:   lockedMemoryContextStatus(history.messages),
		Events:   events,
		Warnings: warnings,
	}), nil
}

func storedSessionRuntimeLimits(sessionDir string, configSnapshotJSON string, fallback config.RuntimeLimits) (config.RuntimeLimits, []string) {
	name := sessionFileName(configSnapshotJSON, sessionConfigSnapshotName)
	path := filepath.Join(sessionDir, name)
	body, err := os.ReadFile(path)
	if err != nil {
		return fallback, []string{"config snapshot unavailable: " + err.Error()}
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return fallback, []string{"config snapshot invalid: " + err.Error()}
	}
	limits := fallback
	if value, ok := int64FromSnapshot(raw["context_window_tokens"]); ok {
		limits.ContextWindowTokens = value
	}
	if value, ok := int64FromSnapshot(raw["context_compact_tokens"]); ok {
		limits.ContextCompactTokens = int(value)
	}
	if value, ok := int64FromSnapshot(raw["context_compact_keep"]); ok {
		limits.ContextCompactKeep = int(value)
	}
	if value, ok := int64FromSnapshot(raw["context_compact_max_chars"]); ok {
		limits.ContextCompactMaxChars = int(value)
	}
	if value, ok := raw["context_window_source"].(string); ok {
		limits.ContextWindowSource = strings.TrimSpace(value)
	}
	if value, ok := raw["context_compact_source"].(string); ok {
		limits.ContextCompactSource = strings.TrimSpace(value)
	}
	if value, ok := raw["context_compact_strategy"].(string); ok {
		limits.ContextCompactStrategy = strings.TrimSpace(value)
	}
	if value, ok := raw["context_compact_summary_provider"].(string); ok {
		limits.ContextCompactSummaryProvider = strings.TrimSpace(value)
	}
	if value, ok := raw["context_compact_summary_model"].(string); ok {
		limits.ContextCompactSummaryModel = strings.TrimSpace(value)
	}
	return limits, nil
}

func int64FromSnapshot(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

func inspectLegacyStoredSession(dir, id string) (StoredSessionInspection, error) {
	path := filepath.Join(dir, id+".json")
	file := inspectStoredSessionFile("legacy_snapshot", path)
	if !file.Exists {
		return StoredSessionInspection{Dir: dir, SessionID: id, Files: []StoredSessionFile{file}}, os.ErrNotExist
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return StoredSessionInspection{}, err
	}
	var record storedSession
	if err := json.Unmarshal(bytes, &record); err != nil {
		return StoredSessionInspection{}, err
	}
	out := StoredSessionInspection{
		Dir:                dir,
		SessionID:          id,
		Legacy:             true,
		Files:              []StoredSessionFile{file},
		MessageCount:       len(record.Messages),
		OfflineReplayReady: len(record.Messages) > 0,
		Warnings:           []string{"legacy snapshot only; JSONL manifest/history/events missing"},
	}
	out.History = StoredSessionHistoryInspection{
		Path:         path,
		Exists:       true,
		Records:      1,
		LastSeq:      1,
		LastKind:     "legacy.snapshot",
		MessageCount: len(record.Messages),
	}
	out.Events = inspectSessionEvents(id, "", nil)
	out.Manifest = StoredSessionManifest{
		SessionID:    id,
		CreatedAt:    record.Created,
		UpdatedAt:    record.Updated,
		MessageCount: len(record.Messages),
		SnapshotJSON: filepath.Base(path),
	}
	return out, nil
}

func inspectStoredSessionFile(name, path string) StoredSessionFile {
	file := StoredSessionFile{Name: name, Path: filepath.Clean(path)}
	info, err := os.Stat(file.Path)
	if err != nil || info.IsDir() {
		return file
	}
	file.Exists = true
	file.Bytes = info.Size()
	return file
}

func inspectSessionEvents(sessionID, path string, events []protocol.Event) StoredSessionEventsInspection {
	out := StoredSessionEventsInspection{
		Path:       path,
		Exists:     fileExists(path),
		Records:    len(events),
		Validation: storedSessionEventValidationOK(),
		Terminal:   StoredSessionTerminalInspection{State: storedSessionRunStateIdle},
		EventTypes: map[string]int{},
	}
	var redoChangeID string
	seenSeq := map[int64]struct{}{}
	lastSeq := int64(0)
	requestedCalls := map[string]struct{}{}
	startedAttempts := map[string]struct{}{}
	terminalAttempts := map[string]struct{}{}
	for _, event := range events {
		if event.Seq > 0 {
			if _, ok := seenSeq[event.Seq]; ok {
				out.Validation.DuplicateSeqs++
				out.Validation.SequenceValid = false
				out.Validation.Valid = false
			}
			seenSeq[event.Seq] = struct{}{}
			if lastSeq > 0 && event.Seq > lastSeq+1 {
				out.Validation.SequenceGaps++
				out.Validation.SequenceValid = false
				out.Validation.Valid = false
			}
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
		}
		if event.Seq > out.LastSeq {
			out.LastSeq = event.Seq
			out.LastEvent = string(event.Type)
		}
		if event.Type != "" {
			out.EventTypes[string(event.Type)]++
		}
		switch event.Type {
		case protocol.EventRunStarted:
			out.Terminal = StoredSessionTerminalInspection{State: storedSessionRunStateRunning, Event: string(event.Type), RunID: event.RunID, Seq: event.Seq}
		case protocol.EventRunCompleted:
			out.Terminal = StoredSessionTerminalInspection{State: storedSessionRunStateCompleted, Event: string(event.Type), RunID: event.RunID, Seq: event.Seq}
		case protocol.EventRunFailed:
			out.Terminal = StoredSessionTerminalInspection{State: storedSessionRunStateFailed, Event: string(event.Type), RunID: event.RunID, Seq: event.Seq, LastError: fmt.Sprint(event.Data)}
		}
		inspectSessionLifecycleReferences(&out, event, requestedCalls, startedAttempts, terminalAttempts)
		if event.Type == protocol.EventToolOutputRefCreated {
			ref := outputRefFromEvent(event)
			out.OutputRefs++
			warning, bytes, ok := inspectOutputRefEvent(event, ref)
			if bytes > 0 {
				out.OutputRefBytes += bytes
			}
			if ok {
				continue
			}
			out.OutputRefWarnings = append(out.OutputRefWarnings, warning)
			switch warning.Reason {
			case "missing_path", "missing", "is_directory":
				out.MissingOutputRefs++
			case "size_mismatch", "sha256_mismatch", "hash_error":
				out.OutputRefHashMismatch++
			}
		}
		switch event.Type {
		case protocol.EventTurnChangeRecorded, protocol.EventTurnChangeReverted:
			stored, ok := storedTurnChangeFromEvent(event)
			if !ok {
				continue
			}
			out.TurnChanges = append(out.TurnChanges, inspectTurnChange(event, stored.Data))
			if event.Type == protocol.EventTurnChangeRecorded {
				redoChangeID = ""
			} else {
				redoChangeID = stored.Data.ChangeID
			}
		}
	}
	if redoChangeID != "" {
		out.RedoAvailable = true
		out.RedoChangeID = redoChangeID
	}
	if out.Validation.UnmatchedProgress > 0 || out.Validation.UnmatchedOutputRefs > 0 || out.Validation.UnmatchedPermissions > 0 {
		out.Validation.LifecycleValid = false
		out.Validation.Valid = false
	}
	out.Projector = inspectSessionProjector(sessionID, events, out.LastSeq)
	out.OutputRefsVerified = out.OutputRefs > 0 && out.MissingOutputRefs == 0 && out.OutputRefHashMismatch == 0
	if len(out.EventTypes) == 0 {
		out.EventTypes = nil
	}
	return out
}

func storedSessionEventValidationOK() StoredSessionEventValidation {
	return StoredSessionEventValidation{
		Valid:          true,
		EnvelopeValid:  true,
		SequenceValid:  true,
		LifecycleValid: true,
	}
}

func storedSessionEventValidationFromError(storeDir string, err error) StoredSessionEventValidation {
	out := storedSessionEventValidationOK()
	out.Valid = false
	out.Error = sanitizeSessionStoreLoadError(storeDir, err.Error())
	var corrupt *eventlog.CorruptionError
	if errors.As(err, &corrupt) {
		out.CorruptionKind = corrupt.Kind
		out.Line = corrupt.Line
		out.RecordNo = corrupt.RecordNo
		if corrupt.Err != nil {
			out.Error = sanitizeSessionStoreLoadError(storeDir, corrupt.Err.Error())
		}
	}
	combined := strings.ToLower(out.CorruptionKind + " " + out.Error)
	switch {
	case strings.Contains(combined, "lifecycle"):
		out.LifecycleValid = false
	case strings.Contains(combined, "sequence") || strings.Contains(combined, "seq"):
		out.SequenceValid = false
	default:
		out.EnvelopeValid = false
	}
	return out
}

func inspectSessionLifecycleReferences(out *StoredSessionEventsInspection, event protocol.Event, requestedCalls, startedAttempts, terminalAttempts map[string]struct{}) {
	callID := inspectEventCallID(event)
	attemptID := inspectEventAttemptID(event)
	switch event.Type {
	case protocol.EventToolCallRequested:
		if callID != "" {
			requestedCalls[callID] = struct{}{}
		}
	case protocol.EventToolCallStarted:
		if callID != "" {
			requestedCalls[callID] = struct{}{}
		}
		if attemptID != "" {
			startedAttempts[attemptID] = struct{}{}
		}
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		if attemptID != "" {
			terminalAttempts[attemptID] = struct{}{}
		}
	case protocol.EventToolCallProgress:
		if !inspectKnownCallOrAttempt(callID, attemptID, requestedCalls, startedAttempts) && !inspectTerminalCleanupProgress(event) {
			out.Validation.UnmatchedProgress++
		}
	case protocol.EventToolOutputRefCreated:
		if !inspectKnownCallOrAttempt(callID, attemptID, requestedCalls, startedAttempts) {
			out.Validation.UnmatchedOutputRefs++
		}
	case protocol.EventToolPermissionRequested, protocol.EventToolPermissionDecided:
		if !inspectKnownCallOrAttempt(callID, attemptID, requestedCalls, startedAttempts) {
			out.Validation.UnmatchedPermissions++
		}
	}
	if attemptID != "" {
		if _, ok := terminalAttempts[attemptID]; ok && event.Type == protocol.EventToolCallProgress && !inspectTerminalCleanupProgress(event) {
			out.Validation.UnmatchedProgress++
		}
	}
}

func inspectKnownCallOrAttempt(callID, attemptID string, requestedCalls, startedAttempts map[string]struct{}) bool {
	if callID != "" {
		if _, ok := requestedCalls[callID]; ok {
			return true
		}
	}
	if attemptID != "" {
		if _, ok := startedAttempts[attemptID]; ok {
			return true
		}
	}
	return false
}

func inspectTerminalCleanupProgress(event protocol.Event) bool {
	phase := strings.ToLower(strings.TrimSpace(inspectEventDataString(event, "phase")))
	switch phase {
	case "attempt_finished", "cancel_abort", "retry_decision", "finalize":
		return true
	default:
		return false
	}
}

func inspectSessionProjector(sessionID string, events []protocol.Event, lastSeq int64) StoredSessionProjectorInspection {
	transcriptEvents := 0
	rawToolCalls := 0
	var firstSeq int64
	var lastEvent protocol.Event
	uxProjector := clientprojector.New()
	snapshot := uxProjector.Snapshot()
	for _, event := range events {
		if storedSessionTranscriptEvent(event.Type) {
			transcriptEvents++
		}
		if event.Seq > 0 {
			if firstSeq == 0 || event.Seq < firstSeq {
				firstSeq = event.Seq
			}
			if event.Seq >= lastEvent.Seq {
				lastEvent = event
			}
		}
		if event.Type == protocol.EventToolCallRequested {
			rawToolCalls++
		}
		snapshot = uxProjector.Apply(event)
	}
	out := StoredSessionProjectorInspection{
		LastSeq:            lastSeq,
		SessionID:          strings.TrimSpace(sessionID),
		SeqRange:           storedSessionProjectorSeqRange(firstSeq, lastSeq),
		SnapshotLastSeq:    snapshot.LastSeq,
		RunState:           storedSessionProjectorRunState(snapshot.RunState),
		TranscriptEvents:   transcriptEvents,
		ToolCallsRaw:       rawToolCalls,
		ToolCallsProjected: snapshot.ToolCalls,
		AssistantBytes:     len(snapshot.AssistantText),
		ReasoningBytes:     len(snapshot.ReasoningText),
		ProjectionHash:     storedSessionProjectorHash(snapshot),
		LastEventID:        storedSessionProjectorEventID(lastEvent),
		SeqGap:             storedSessionProjectorSeqGap(snapshot.SeqGap),
	}
	if out.LastSeq != out.SnapshotLastSeq {
		out.MismatchReasons = append(out.MismatchReasons, fmt.Sprintf("last_seq=%d snapshot_last_seq=%d", out.LastSeq, out.SnapshotLastSeq))
	}
	if out.ToolCallsRaw != out.ToolCallsProjected {
		out.MismatchReasons = append(out.MismatchReasons, fmt.Sprintf("tool_calls_raw=%d tool_calls_projected=%d", out.ToolCallsRaw, out.ToolCallsProjected))
	}
	if out.SeqGap != nil {
		out.MismatchReasons = append(out.MismatchReasons, fmt.Sprintf("sequence_gap_after=%d got=%d", out.SeqGap.AfterSeq, out.SeqGap.GotSeq))
	}
	out.ParityOK = len(out.MismatchReasons) == 0
	return out
}

func storedSessionProjectorRunState(state clientprojector.RunState) string {
	switch state {
	case clientprojector.RunStateRunning:
		return storedSessionRunStateRunning
	case clientprojector.RunStateCompleted:
		return storedSessionRunStateCompleted
	case clientprojector.RunStateFailed:
		return storedSessionRunStateFailed
	default:
		return storedSessionRunStateIdle
	}
}

func storedSessionProjectorSeqGap(gap *clientprojector.EventSeqGap) *StoredSessionProjectorSeqGap {
	if gap == nil {
		return nil
	}
	return &StoredSessionProjectorSeqGap{AfterSeq: gap.AfterSeq, GotSeq: gap.GotSeq}
}

func storedSessionProjectorSeqRange(firstSeq, lastSeq int64) string {
	if firstSeq == 0 && lastSeq == 0 {
		return ""
	}
	return fmt.Sprintf("%d-%d", firstSeq, lastSeq)
}

func storedSessionProjectorEventID(event protocol.Event) string {
	if event.Seq == 0 && event.Type == "" {
		return ""
	}
	parts := []string{fmt.Sprintf("seq=%d", event.Seq), "type=" + string(event.Type)}
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "run_id", value: event.RunID},
		{name: "turn_id", value: event.TurnID},
		{name: "step_id", value: event.StepID},
		{name: "call_id", value: event.CallID},
		{name: "attempt_id", value: event.AttemptID},
	} {
		if strings.TrimSpace(item.value) == "" {
			continue
		}
		parts = append(parts, item.name+"="+strings.TrimSpace(item.value))
	}
	return strings.Join(parts, " ")
}

func storedSessionProjectorHash(snapshot clientprojector.Snapshot) string {
	body, err := json.Marshal(struct {
		RunState       clientprojector.RunState `json:"run_state"`
		LastSeq        int64                    `json:"last_seq"`
		ToolCalls      int                      `json:"tool_calls"`
		AssistantBytes int                      `json:"assistant_bytes"`
		ReasoningBytes int                      `json:"reasoning_bytes"`
		ErrorCount     int                      `json:"error_count"`
	}{
		RunState:       snapshot.RunState,
		LastSeq:        snapshot.LastSeq,
		ToolCalls:      snapshot.ToolCalls,
		AssistantBytes: len(snapshot.AssistantText),
		ReasoningBytes: len(snapshot.ReasoningText),
		ErrorCount:     len(snapshot.Errors),
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func storedSessionTranscriptEvent(eventType protocol.EventType) bool {
	switch eventType {
	case protocol.EventRunStarted,
		protocol.EventRunCompleted,
		protocol.EventRunFailed,
		protocol.EventAssistantReasoning,
		protocol.EventAssistantDelta,
		protocol.EventToolAudit,
		protocol.EventToolCallRequested,
		protocol.EventToolCallFinished,
		protocol.EventToolCallFailed,
		protocol.EventToolCallAborted,
		protocol.EventStepStarted,
		protocol.EventStepCompleted,
		protocol.EventContextCompacted,
		protocol.EventContextThreshold,
		protocol.EventTurnChangeRecorded,
		protocol.EventTurnChangeReverted,
		protocol.EventUserInputRequested,
		protocol.EventUserInputAnswered,
		protocol.EventUserInputRejected:
		return true
	default:
		return false
	}
}

func inspectEventCallID(event protocol.Event) string {
	if strings.TrimSpace(event.CallID) != "" {
		return strings.TrimSpace(event.CallID)
	}
	return inspectEventDataString(event, "call_id")
}

func inspectEventAttemptID(event protocol.Event) string {
	if strings.TrimSpace(event.AttemptID) != "" {
		return strings.TrimSpace(event.AttemptID)
	}
	return inspectEventDataString(event, "attempt_id")
}

func inspectEventDataString(event protocol.Event, key string) string {
	if strings.TrimSpace(key) == "" || event.Data == nil {
		return ""
	}
	body, err := json.Marshal(event.Data)
	if err != nil {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

func inspectOutputRefEvent(event protocol.Event, ref outputRefEventData) (StoredSessionOutputRefWarning, int64, bool) {
	warning := StoredSessionOutputRefWarning{
		Seq:            event.Seq,
		RunID:          event.RunID,
		TurnID:         event.TurnID,
		StepID:         event.StepID,
		CallID:         firstNonEmpty(ref.CallID, event.CallID),
		AttemptID:      firstNonEmpty(ref.AttemptID, event.AttemptID),
		Name:           ref.Name,
		OutputRef:      ref.OutputRef,
		OutputRefID:    ref.OutputRefID,
		ExpectedBytes:  ref.Bytes,
		ExpectedSHA256: ref.SHA256,
	}
	if strings.TrimSpace(ref.OutputRef) == "" {
		warning.Reason = "missing_path"
		return warning, 0, false
	}
	info, err := os.Stat(ref.OutputRef)
	if err != nil {
		warning.Reason = "missing"
		warning.Error = err.Error()
		return warning, 0, false
	}
	if info.IsDir() {
		warning.Reason = "is_directory"
		return warning, 0, false
	}
	warning.ActualBytes = info.Size()
	if ref.Bytes > 0 && info.Size() != ref.Bytes {
		warning.Reason = "size_mismatch"
		return warning, info.Size(), false
	}
	if ref.SHA256 != "" {
		ok, err := fileSHA256Matches(ref.OutputRef, ref.SHA256)
		if err != nil {
			warning.Reason = "hash_error"
			warning.Error = err.Error()
			return warning, info.Size(), false
		}
		if !ok {
			warning.Reason = "sha256_mismatch"
			return warning, info.Size(), false
		}
	}
	return warning, info.Size(), true
}

func formatStoredOutputRefWarning(warning StoredSessionOutputRefWarning) string {
	var parts []string
	parts = append(parts, "output_ref")
	if warning.Seq > 0 {
		parts = append(parts, fmt.Sprintf("seq=%d", warning.Seq))
	}
	if warning.CallID != "" {
		parts = append(parts, "call_id="+warning.CallID)
	}
	if warning.Name != "" {
		parts = append(parts, "name="+warning.Name)
	}
	if warning.OutputRef != "" {
		parts = append(parts, "path="+warning.OutputRef)
	}
	if warning.Reason != "" {
		parts = append(parts, "reason="+warning.Reason)
	}
	if warning.Error != "" {
		parts = append(parts, "error="+warning.Error)
	}
	return strings.Join(parts, " ")
}

func inspectTurnChange(event protocol.Event, change protocol.TurnChangeEvent) StoredSessionTurnChangeInspection {
	status := strings.TrimSpace(change.Status)
	if status == "" {
		if event.Type == protocol.EventTurnChangeReverted {
			status = "reverted"
		} else {
			status = "recorded"
		}
	}
	return StoredSessionTurnChangeInspection{
		Seq:            event.Seq,
		EventType:      string(event.Type),
		ChangeID:       change.ChangeID,
		Status:         status,
		ToolName:       change.ToolName,
		FileCount:      change.FileCount,
		Added:          change.Added,
		Modified:       change.Modified,
		Deleted:        change.Deleted,
		Additions:      change.Additions,
		Deletions:      change.Deletions,
		PatchOutputRef: change.PatchOutputRef,
		Files:          append([]protocol.TurnChangeFile(nil), change.Files...),
	}
}

type outputRefEventData struct {
	CallID      string `json:"call_id,omitempty"`
	AttemptID   string `json:"attempt_id,omitempty"`
	Name        string `json:"name,omitempty"`
	OutputRef   string `json:"output_ref"`
	OutputRefID string `json:"output_ref_id,omitempty"`
	SHA256      string `json:"output_ref_sha256"`
	Bytes       int64  `json:"output_ref_bytes"`
}

func outputRefFromEvent(event protocol.Event) outputRefEventData {
	body, err := json.Marshal(event.Data)
	if err != nil {
		return outputRefEventData{}
	}
	var out outputRefEventData
	_ = json.Unmarshal(body, &out)
	return out
}

func fileSHA256Matches(path, want string) (bool, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(bytes)
	return strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(want)), nil
}

func storedSessionManifest(manifest sessionManifest) StoredSessionManifest {
	return StoredSessionManifest{
		SchemaVersion:             manifest.SchemaVersion,
		SessionID:                 manifest.SessionID,
		CreatedAt:                 manifest.CreatedAt,
		UpdatedAt:                 manifest.UpdatedAt,
		HistoryJSONL:              manifest.HistoryJSONL,
		EventsJSONL:               manifest.EventsJSONL,
		SnapshotJSON:              manifest.SnapshotJSON,
		ConfigSnapshotJSON:        manifest.ConfigSnapshotJSON,
		ModelProviderSnapshotJSON: manifest.ModelProviderSnapshotJSON,
		MCPSnapshotJSON:           manifest.MCPSnapshotJSON,
		HistorySeq:                manifest.HistorySeq,
		EventSeq:                  manifest.EventSeq,
		MessageCount:              manifest.MessageCount,
		Owner:                     manifest.Owner,
		HistorySHA256:             manifest.HistorySHA256,
	}
}

func inspectionSummary(inspection StoredSessionInspection) StoredSessionSummary {
	summary := StoredSessionSummary{
		ID:                 inspection.SessionID,
		CreatedAt:          inspection.Manifest.CreatedAt,
		UpdatedAt:          inspection.Manifest.UpdatedAt,
		MessageCount:       inspection.MessageCount,
		HistorySeq:         inspection.History.LastSeq,
		EventSeq:           inspection.Events.LastSeq,
		LastEvent:          inspection.Events.LastEvent,
		Owner:              inspection.Manifest.Owner,
		Legacy:             inspection.Legacy,
		OfflineReplayReady: inspection.OfflineReplayReady,
	}
	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = summary.CreatedAt
	}
	return summary
}

func hasExistingFile(files []StoredSessionFile, name string) bool {
	for _, file := range files {
		if file.Name == name {
			return file.Exists
		}
	}
	return false
}
