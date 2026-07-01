package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	gatewaySessionSchemaVersion = 1
	sessionManifestName         = "manifest.json"
	sessionHistoryJSONLName     = "history.jsonl"
	sessionEventsJSONLName      = "events.jsonl"
	sessionInputsJSONLName      = "inputs.jsonl"
	sessionConfigSnapshotName   = "config.snapshot.json"
	sessionModelSnapshotName    = "model_provider.snapshot.json"
	sessionMCPSnapshotName      = "mcp.snapshot.json"
	sessionHistoryCreated       = "session.created"
	sessionHistorySnapshot      = "history.snapshot"
)

type sessionStore struct {
	dir      string
	mu       sync.Mutex
	eventSeq map[string]int64
}

type storedSession struct {
	ID       string                  `json:"id"`
	Created  time.Time               `json:"created"`
	Updated  time.Time               `json:"updated"`
	Owner    gatewayapi.SessionOwner `json:"owner,omitempty"`
	Messages []protocol.Message      `json:"messages"`
}

type sessionManifest struct {
	SchemaVersion             int                     `json:"schema_version"`
	SessionID                 string                  `json:"session_id"`
	CreatedAt                 time.Time               `json:"created_at"`
	UpdatedAt                 time.Time               `json:"updated_at"`
	HistoryJSONL              string                  `json:"history_jsonl"`
	EventsJSONL               string                  `json:"events_jsonl,omitempty"`
	InputsJSONL               string                  `json:"inputs_jsonl,omitempty"`
	SnapshotJSON              string                  `json:"snapshot_json,omitempty"`
	ConfigSnapshotJSON        string                  `json:"config_snapshot_json,omitempty"`
	ModelProviderSnapshotJSON string                  `json:"model_provider_snapshot_json,omitempty"`
	MCPSnapshotJSON           string                  `json:"mcp_snapshot_json,omitempty"`
	HistorySeq                int64                   `json:"history_seq"`
	EventSeq                  int64                   `json:"event_seq,omitempty"`
	MessageCount              int                     `json:"message_count"`
	Owner                     gatewayapi.SessionOwner `json:"owner,omitempty"`
	HistorySHA256             string                  `json:"history_sha256,omitempty"`
}

type sessionStoreSnapshots struct {
	Config        map[string]any `json:"config,omitempty"`
	ModelProvider map[string]any `json:"model_provider,omitempty"`
	MCP           map[string]any `json:"mcp,omitempty"`
}

func (s *Session) setStoreSnapshots(snapshots sessionStoreSnapshots) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.storeSnapshots = sessionStoreSnapshots{
		Config:        cloneSnapshotMap(snapshots.Config),
		ModelProvider: cloneSnapshotMap(snapshots.ModelProvider),
		MCP:           cloneSnapshotMap(snapshots.MCP),
	}
	s.mu.Unlock()
}

func (s *Session) snapshotFiles() sessionStoreSnapshots {
	if s == nil {
		return sessionStoreSnapshots{}
	}
	s.mu.Lock()
	snapshots := s.storeSnapshots
	s.mu.Unlock()
	return sessionStoreSnapshots{
		Config:        cloneSnapshotMap(snapshots.Config),
		ModelProvider: cloneSnapshotMap(snapshots.ModelProvider),
		MCP:           cloneSnapshotMap(snapshots.MCP),
	}
}

func cloneSnapshotMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = item
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	return out
}

type sessionHistoryRecord struct {
	SchemaVersion int                `json:"schema_version"`
	Seq           int64              `json:"seq"`
	SessionID     string             `json:"session_id"`
	Timestamp     time.Time          `json:"ts"`
	Kind          string             `json:"kind"`
	CreatedAt     time.Time          `json:"created_at,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at,omitempty"`
	MessageCount  int                `json:"message_count"`
	HistorySHA256 string             `json:"history_sha256,omitempty"`
	Messages      []protocol.Message `json:"messages"`
}

type sessionEventRecord struct {
	SchemaVersion int            `json:"schema_version"`
	Seq           int64          `json:"seq"`
	SessionID     string         `json:"session_id"`
	RunSeq        int64          `json:"run_seq,omitempty"`
	Timestamp     time.Time      `json:"ts"`
	EventType     string         `json:"event_type"`
	Event         protocol.Event `json:"event"`
}

type replayedSessionHistory struct {
	lastSeq       int64
	created       time.Time
	updated       time.Time
	messages      []protocol.Message
	historySHA256 string
}

func newSessionStore(dir string) *sessionStore {
	return &sessionStore{
		dir:      filepath.Clean(dir),
		eventSeq: map[string]int64{},
	}
}

func (s *sessionStore) LoadAll() ([]*Session, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []*Session
	loaded := map[string]struct{}{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		session, err := s.loadSessionDir(filepath.Join(s.dir, entry.Name()))
		if err == nil && session != nil {
			sessions = append(sessions, session)
			loaded[session.ID] = struct{}{}
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		session, err := s.loadLegacySnapshot(filepath.Join(s.dir, entry.Name()))
		if err == nil && session != nil {
			if _, ok := loaded[session.ID]; ok {
				continue
			}
			sessions = append(sessions, session)
			loaded[session.ID] = struct{}{}
		}
	}
	return sessions, nil
}

func (s *sessionStore) Save(session *Session) error {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(session)
}

func (s *sessionStore) AppendEvent(session *Session, event protocol.Event) (protocol.Event, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return event, nil
	}
	id, err := cleanSessionID(session.ID)
	if err != nil {
		return event, err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return event, err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return event, err
	}
	sessionDir := filepath.Join(s.dir, id)
	if err := ensurePrivateGatewayDir(sessionDir); err != nil {
		return event, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	manifest, err := readSessionManifest(manifestPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return event, err
	}
	if manifest.SessionID == "" {
		created := session.Created
		if created.IsZero() {
			created = time.Now().UTC()
		}
		manifest = sessionManifest{
			SchemaVersion:             gatewaySessionSchemaVersion,
			SessionID:                 id,
			CreatedAt:                 created,
			UpdatedAt:                 created,
			HistoryJSONL:              sessionHistoryJSONLName,
			EventsJSONL:               sessionEventsJSONLName,
			InputsJSONL:               sessionInputsJSONLName,
			SnapshotJSON:              id + ".json",
			ConfigSnapshotJSON:        sessionConfigSnapshotName,
			ModelProviderSnapshotJSON: sessionModelSnapshotName,
			MCPSnapshotJSON:           sessionMCPSnapshotName,
			MessageCount:              len(session.messages()),
			Owner:                     session.Owner,
		}
		if err := writeSessionManifest(manifestPath, manifest); err != nil {
			return event, err
		}
	}

	eventsPath := filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	seq := s.eventSeq[id]
	if seq == 0 {
		seq, err = lastSessionEventSeq(eventsPath, id)
		if err != nil {
			return event, err
		}
	}
	seq++
	status := session.Status()
	now := time.Now().UTC()
	storedEvent := protocol.EnrichEvent(event, protocol.EventEnvelope{
		Seq:    seq,
		Source: protocol.EventSourceGateway,
		RunID:  gatewaySessionRunID(id, status.RunSeq),
		TS:     now.Format(time.RFC3339Nano),
	})
	storedEvent.Seq = seq
	record := sessionEventRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           seq,
		SessionID:     id,
		RunSeq:        status.RunSeq,
		Timestamp:     now,
		EventType:     string(storedEvent.Type),
		Event:         storedEvent,
	}
	if err := eventlog.AppendJSONL(eventsPath, record); err != nil {
		return event, err
	}
	s.eventSeq[id] = seq
	return storedEvent, nil
}

func (s *sessionStore) ReplayEventsAfter(sessionID string, afterSeq int64) ([]protocol.Event, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	id, err := cleanSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	manifest, err := readSessionManifest(filepath.Join(s.dir, id, sessionManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	eventsPath := filepath.Join(s.dir, id, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	return replaySessionEventsAfter(eventsPath, id, afterSeq)
}

func gatewaySessionRunID(sessionID string, runSeq int64) string {
	if strings.TrimSpace(sessionID) == "" || runSeq <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:run-%d", sessionID, runSeq)
}

func (s *sessionStore) saveLocked(session *Session) error {
	id, err := cleanSessionID(session.ID)
	if err != nil {
		return err
	}
	if err := ensurePrivateGatewayDir(s.dir); err != nil {
		return err
	}
	sessionDir := filepath.Join(s.dir, id)
	if err := ensurePrivateGatewayDir(sessionDir); err != nil {
		return err
	}

	now := time.Now().UTC()
	created := session.Created
	if created.IsZero() {
		created = now
	}
	messages := session.messages()
	historySHA256, err := hashSessionMessages(messages)
	if err != nil {
		return err
	}

	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	manifest, err := readSessionManifest(manifestPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	historyPath := filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName))
	history, err := replaySessionHistory(historyPath, id)
	if err != nil {
		return err
	}
	if history.lastSeq == 0 || history.historySHA256 != historySHA256 {
		kind := sessionHistorySnapshot
		if history.lastSeq == 0 {
			kind = sessionHistoryCreated
		}
		record := sessionHistoryRecord{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           history.lastSeq + 1,
			SessionID:     id,
			Timestamp:     now,
			Kind:          kind,
			CreatedAt:     created,
			UpdatedAt:     now,
			MessageCount:  len(messages),
			HistorySHA256: historySHA256,
			Messages:      messages,
		}
		if err := eventlog.AppendJSONL(historyPath, record); err != nil {
			return err
		}
		history.lastSeq = record.Seq
		history.created = created
		history.updated = now
		history.messages = messages
		history.historySHA256 = historySHA256
	}

	eventsPath := filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	eventSeq := s.eventSeq[id]
	if eventSeq == 0 {
		eventSeq, err = lastSessionEventSeq(eventsPath, id)
		if err != nil {
			return err
		}
	}
	snapshots := session.snapshotFiles()
	configSnapshotJSON := manifest.ConfigSnapshotJSON
	modelSnapshotJSON := manifest.ModelProviderSnapshotJSON
	mcpSnapshotJSON := manifest.MCPSnapshotJSON
	if len(snapshots.Config) > 0 {
		configSnapshotJSON = sessionConfigSnapshotName
		if err := writeSessionSnapshot(filepath.Join(sessionDir, configSnapshotJSON), snapshots.Config); err != nil {
			return err
		}
	}
	if len(snapshots.ModelProvider) > 0 {
		modelSnapshotJSON = sessionModelSnapshotName
		if err := writeSessionSnapshot(filepath.Join(sessionDir, modelSnapshotJSON), snapshots.ModelProvider); err != nil {
			return err
		}
	}
	if len(snapshots.MCP) > 0 {
		mcpSnapshotJSON = sessionMCPSnapshotName
		if err := writeSessionSnapshot(filepath.Join(sessionDir, mcpSnapshotJSON), snapshots.MCP); err != nil {
			return err
		}
	}

	manifest = sessionManifest{
		SchemaVersion:             gatewaySessionSchemaVersion,
		SessionID:                 id,
		CreatedAt:                 created,
		UpdatedAt:                 now,
		HistoryJSONL:              sessionHistoryJSONLName,
		EventsJSONL:               sessionEventsJSONLName,
		InputsJSONL:               sessionInputsJSONLName,
		SnapshotJSON:              id + ".json",
		ConfigSnapshotJSON:        configSnapshotJSON,
		ModelProviderSnapshotJSON: modelSnapshotJSON,
		MCPSnapshotJSON:           mcpSnapshotJSON,
		HistorySeq:                history.lastSeq,
		EventSeq:                  eventSeq,
		MessageCount:              len(messages),
		Owner:                     session.Owner,
		HistorySHA256:             history.historySHA256,
	}
	if err := writeSessionManifest(manifestPath, manifest); err != nil {
		return err
	}

	return writeLegacySnapshot(filepath.Join(s.dir, id+".json"), storedSession{
		ID:       session.ID,
		Created:  created,
		Updated:  now,
		Owner:    session.Owner,
		Messages: messages,
	})
}

func (s *sessionStore) loadSessionDir(dir string) (*Session, error) {
	manifest, err := readSessionManifest(filepath.Join(dir, sessionManifestName))
	if err != nil {
		return nil, err
	}
	id, err := cleanSessionID(manifest.SessionID)
	if err != nil {
		return nil, err
	}
	history, err := replaySessionHistory(filepath.Join(dir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName)), id)
	if err != nil {
		return nil, err
	}
	if history.lastSeq == 0 {
		return nil, fmt.Errorf("stored session %s has empty history", id)
	}
	created := manifest.CreatedAt
	if created.IsZero() {
		created = history.created
	}
	if created.IsZero() {
		created = manifest.UpdatedAt
	}
	if created.IsZero() {
		created = time.Now().UTC()
	}
	session := newGatewaySessionWithOwner(id, created, history.messages, manifest.Owner)
	eventsPath := filepath.Join(dir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	status, ok, err := replaySessionStatus(eventsPath, id)
	if err != nil {
		return nil, err
	}
	if ok {
		session.restoreStatus(status)
	}
	inputsPath := filepath.Join(dir, sessionFileName(manifest.InputsJSONL, sessionInputsJSONLName))
	if err := markPromotedSessionInputsAmbiguous(inputsPath, id); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *sessionStore) loadLegacySnapshot(path string) (*Session, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record storedSession
	if err := json.Unmarshal(bytes, &record); err != nil {
		return nil, err
	}
	if strings.TrimSpace(record.ID) == "" {
		return nil, fmt.Errorf("stored session missing id")
	}
	if record.Created.IsZero() {
		record.Created = record.Updated
	}
	if record.Created.IsZero() {
		record.Created = time.Now().UTC()
	}
	return newGatewaySessionWithOwner(record.ID, record.Created, record.Messages, record.Owner), nil
}

func replaySessionHistory(path, sessionID string) (replayedSessionHistory, error) {
	var history replayedSessionHistory
	expectedSeq := int64(1)
	err := eventlog.ReplayJSONL[sessionHistoryRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[sessionHistoryRecord]) error {
		record := item.Value
		recordNo := expectedSeq
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != expectedSeq {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq))
		}
		if record.SessionID != sessionID {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("session_id = %q, want %q", record.SessionID, sessionID))
		}
		if record.Kind != sessionHistoryCreated && record.Kind != sessionHistorySnapshot {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported kind %q", record.Kind))
		}
		if record.MessageCount != len(record.Messages) {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("message_count = %d, want %d", record.MessageCount, len(record.Messages)))
		}
		historySHA256, err := hashSessionMessages(record.Messages)
		if err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "invalid messages", err)
		}
		if record.HistorySHA256 != "" && record.HistorySHA256 != historySHA256 {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("history_sha256 mismatch: got %s want %s", record.HistorySHA256, historySHA256))
		}
		history.lastSeq = record.Seq
		history.created = record.CreatedAt
		history.updated = record.UpdatedAt
		history.messages = record.Messages
		history.historySHA256 = historySHA256
		expectedSeq++
		return nil
	})
	if err != nil {
		return history, err
	}
	return history, nil
}

func lastSessionEventSeq(path, sessionID string) (int64, error) {
	validator := eventlog.NewRecordValidator(eventlog.RecordValidatorOptions{
		SchemaVersion:   gatewaySessionSchemaVersion,
		ScopeName:       "session_id",
		ExpectedScopeID: sessionID,
	})
	err := eventlog.ReplayJSONL[sessionEventRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[sessionEventRecord]) error {
		record := item.Value
		recordNo := validator.NextSeq()
		if err := validator.Validate(eventlog.Record{
			SchemaVersion: record.SchemaVersion,
			Seq:           record.Seq,
			ScopeID:       record.SessionID,
			EventType:     record.EventType,
			Event:         record.Event,
			HasEvent:      record.Event.Type != "",
		}); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		return nil
	})
	if err != nil {
		return validator.LastSeq(), err
	}
	return validator.LastSeq(), nil
}

func replaySessionStatus(path, sessionID string) (SessionStatus, bool, error) {
	var status SessionStatus
	var found bool
	var maxRunSeq int64
	validator := eventlog.NewRecordValidator(eventlog.RecordValidatorOptions{
		SchemaVersion:   gatewaySessionSchemaVersion,
		ScopeName:       "session_id",
		ExpectedScopeID: sessionID,
	})
	err := eventlog.ReplayJSONL[sessionEventRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[sessionEventRecord]) error {
		record := item.Value
		recordNo := validator.NextSeq()
		if err := validator.Validate(eventlog.Record{
			SchemaVersion: record.SchemaVersion,
			Seq:           record.Seq,
			ScopeID:       record.SessionID,
			EventType:     record.EventType,
			Event:         record.Event,
			HasEvent:      record.Event.Type != "",
		}); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		if record.RunSeq > maxRunSeq {
			maxRunSeq = record.RunSeq
		}
		if record.Event.Type != protocol.EventSessionStatus {
			return nil
		}
		decoded, ok := decodeSessionStatus(record.Event.Data)
		if !ok {
			return nil
		}
		status = decoded
		found = true
		if record.RunSeq > status.RunSeq {
			status.RunSeq = record.RunSeq
		}
		return nil
	})
	if err != nil {
		return SessionStatus{}, false, err
	}
	if found {
		if maxRunSeq > status.RunSeq {
			status.RunSeq = maxRunSeq
		}
		return status, true, nil
	}
	if maxRunSeq > 0 {
		return SessionStatus{ID: sessionID, RunSeq: maxRunSeq}, true, nil
	}
	return SessionStatus{}, false, nil
}

func decodeSessionStatus(value any) (SessionStatus, bool) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return SessionStatus{}, false
	}
	var status SessionStatus
	if err := json.Unmarshal(bytes, &status); err != nil {
		return SessionStatus{}, false
	}
	return status, status.ID != "" || status.RunSeq > 0
}

func replaySessionEventsAfter(path, sessionID string, afterSeq int64) ([]protocol.Event, error) {
	var events []protocol.Event
	validator := eventlog.NewRecordValidator(eventlog.RecordValidatorOptions{
		SchemaVersion:   gatewaySessionSchemaVersion,
		ScopeName:       "session_id",
		ExpectedScopeID: sessionID,
	})
	lifecycle := eventlog.NewLifecycleValidator()
	err := eventlog.ReplayJSONL[sessionEventRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[sessionEventRecord]) error {
		record := item.Value
		recordNo := validator.NextSeq()
		if err := validator.Validate(eventlog.Record{
			SchemaVersion: record.SchemaVersion,
			Seq:           record.Seq,
			ScopeID:       record.SessionID,
			EventType:     record.EventType,
			Event:         record.Event,
			HasEvent:      record.Event.Type != "",
		}); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		if record.Event.Type != "" {
			if err := lifecycle.Observe(record.Event); err != nil {
				return eventlog.NewCorruptionError(path, item.Line, recordNo, "lifecycle", err)
			}
		}
		if record.Seq > afterSeq {
			events = append(events, record.Event)
		}
		return nil
	})
	if err != nil {
		return events, err
	}
	return events, nil
}

func readSessionManifest(path string) (sessionManifest, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return sessionManifest{}, err
	}
	var manifest sessionManifest
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		return sessionManifest{}, err
	}
	if manifest.SchemaVersion != 0 && manifest.SchemaVersion != gatewaySessionSchemaVersion {
		return sessionManifest{}, fmt.Errorf("%s unsupported schema_version %d", path, manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.SessionID) == "" {
		return sessionManifest{}, fmt.Errorf("%s missing session_id", path)
	}
	return manifest, nil
}

func writeSessionManifest(path string, manifest sessionManifest) error {
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = gatewaySessionSchemaVersion
	}
	if err := ensurePrivateGatewayDir(filepath.Dir(path)); err != nil {
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
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(tmp)
		return encodeErr
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return syncErr
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

func writeLegacySnapshot(path string, record storedSession) error {
	bytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := ensurePrivateGatewayDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(bytes, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func writeSessionSnapshot(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := ensurePrivateGatewayDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(bytes, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func ensurePrivateGatewayDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func hashSessionMessages(messages []protocol.Message) (string, error) {
	bytes, err := json.Marshal(messages)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return fmt.Sprintf("%x", sum[:]), nil
}

func cleanSessionID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("session id required")
	}
	if id == "." || id == ".." || strings.Contains(id, "/") || strings.Contains(id, "\\") || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	return id, nil
}

func sessionFileName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	name = filepath.Base(filepath.Clean(name))
	if name == "." || name == string(filepath.Separator) {
		return fallback
	}
	return name
}
