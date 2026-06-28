package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	gatewaySessionSchemaVersion = 1
	sessionManifestName         = "manifest.json"
	sessionHistoryJSONLName     = "history.jsonl"
	sessionEventsJSONLName      = "events.jsonl"
	sessionHistoryCreated       = "session.created"
	sessionHistorySnapshot      = "history.snapshot"
)

type sessionStore struct {
	dir      string
	mu       sync.Mutex
	eventSeq map[string]int64
}

type storedSession struct {
	ID       string             `json:"id"`
	Created  time.Time          `json:"created"`
	Updated  time.Time          `json:"updated"`
	Messages []protocol.Message `json:"messages"`
}

type sessionManifest struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	HistoryJSONL  string    `json:"history_jsonl"`
	EventsJSONL   string    `json:"events_jsonl,omitempty"`
	SnapshotJSON  string    `json:"snapshot_json,omitempty"`
	HistorySeq    int64     `json:"history_seq"`
	EventSeq      int64     `json:"event_seq,omitempty"`
	MessageCount  int       `json:"message_count"`
	HistorySHA256 string    `json:"history_sha256,omitempty"`
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
			SchemaVersion: gatewaySessionSchemaVersion,
			SessionID:     id,
			CreatedAt:     created,
			UpdatedAt:     created,
			HistoryJSONL:  sessionHistoryJSONLName,
			EventsJSONL:   sessionEventsJSONLName,
			SnapshotJSON:  id + ".json",
			MessageCount:  len(session.messages()),
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
	if err := appendJSONL(eventsPath, record); err != nil {
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
		if err := appendJSONL(historyPath, record); err != nil {
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

	manifest = sessionManifest{
		SchemaVersion: gatewaySessionSchemaVersion,
		SessionID:     id,
		CreatedAt:     created,
		UpdatedAt:     now,
		HistoryJSONL:  sessionHistoryJSONLName,
		EventsJSONL:   sessionEventsJSONLName,
		SnapshotJSON:  id + ".json",
		HistorySeq:    history.lastSeq,
		EventSeq:      eventSeq,
		MessageCount:  len(messages),
		HistorySHA256: history.historySHA256,
	}
	if err := writeSessionManifest(manifestPath, manifest); err != nil {
		return err
	}

	return writeLegacySnapshot(filepath.Join(s.dir, id+".json"), storedSession{
		ID:       session.ID,
		Created:  created,
		Updated:  now,
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
	return newGatewaySession(id, created, history.messages), nil
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
	return newGatewaySession(record.ID, record.Created, record.Messages), nil
}

func replaySessionHistory(path, sessionID string) (replayedSessionHistory, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return replayedSessionHistory{}, nil
		}
		return replayedSessionHistory{}, err
	}
	defer file.Close()

	var history replayedSessionHistory
	decoder := json.NewDecoder(file)
	expectedSeq := int64(1)
	for {
		var record sessionHistoryRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return history, fmt.Errorf("%s record %d invalid: %w", path, expectedSeq, err)
		}
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return history, fmt.Errorf("%s record %d unsupported schema_version %d", path, expectedSeq, record.SchemaVersion)
		}
		if record.Seq != expectedSeq {
			return history, fmt.Errorf("%s sequence gap: got %d want %d", path, record.Seq, expectedSeq)
		}
		if record.SessionID != sessionID {
			return history, fmt.Errorf("%s record %d session_id = %q, want %q", path, expectedSeq, record.SessionID, sessionID)
		}
		if record.Kind != sessionHistoryCreated && record.Kind != sessionHistorySnapshot {
			return history, fmt.Errorf("%s record %d unsupported kind %q", path, expectedSeq, record.Kind)
		}
		if record.MessageCount != len(record.Messages) {
			return history, fmt.Errorf("%s record %d message_count = %d, want %d", path, expectedSeq, record.MessageCount, len(record.Messages))
		}
		historySHA256, err := hashSessionMessages(record.Messages)
		if err != nil {
			return history, fmt.Errorf("%s record %d invalid messages: %w", path, expectedSeq, err)
		}
		if record.HistorySHA256 != "" && record.HistorySHA256 != historySHA256 {
			return history, fmt.Errorf("%s record %d history_sha256 mismatch: got %s want %s", path, expectedSeq, record.HistorySHA256, historySHA256)
		}
		history.lastSeq = record.Seq
		history.created = record.CreatedAt
		history.updated = record.UpdatedAt
		history.messages = record.Messages
		history.historySHA256 = historySHA256
		expectedSeq++
	}
	return history, nil
}

func lastSessionEventSeq(path, sessionID string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	var lastSeq int64
	decoder := json.NewDecoder(file)
	expectedSeq := int64(1)
	for {
		var record sessionEventRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return lastSeq, fmt.Errorf("%s record %d invalid: %w", path, expectedSeq, err)
		}
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return lastSeq, fmt.Errorf("%s record %d unsupported schema_version %d", path, expectedSeq, record.SchemaVersion)
		}
		if record.Seq != expectedSeq {
			return lastSeq, fmt.Errorf("%s sequence gap: got %d want %d", path, record.Seq, expectedSeq)
		}
		if record.SessionID != sessionID {
			return lastSeq, fmt.Errorf("%s record %d session_id = %q, want %q", path, expectedSeq, record.SessionID, sessionID)
		}
		if record.EventType != "" && record.Event.Type != "" && record.EventType != string(record.Event.Type) {
			return lastSeq, fmt.Errorf("%s record %d event_type = %q, event.type = %q", path, expectedSeq, record.EventType, record.Event.Type)
		}
		lastSeq = record.Seq
		expectedSeq++
	}
	return lastSeq, nil
}

func replaySessionEventsAfter(path, sessionID string, afterSeq int64) ([]protocol.Event, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var events []protocol.Event
	decoder := json.NewDecoder(file)
	expectedSeq := int64(1)
	for {
		var record sessionEventRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return events, fmt.Errorf("%s record %d invalid: %w", path, expectedSeq, err)
		}
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return events, fmt.Errorf("%s record %d unsupported schema_version %d", path, expectedSeq, record.SchemaVersion)
		}
		if record.Seq != expectedSeq {
			return events, fmt.Errorf("%s sequence gap: got %d want %d", path, record.Seq, expectedSeq)
		}
		if record.SessionID != sessionID {
			return events, fmt.Errorf("%s record %d session_id = %q, want %q", path, expectedSeq, record.SessionID, sessionID)
		}
		if record.EventType != "" && record.Event.Type != "" && record.EventType != string(record.Event.Type) {
			return events, fmt.Errorf("%s record %d event_type = %q, event.type = %q", path, expectedSeq, record.EventType, record.Event.Type)
		}
		if record.Seq > afterSeq {
			events = append(events, record.Event)
		}
		expectedSeq++
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

func appendJSONL(path string, value any) error {
	if err := ensurePrivateGatewayDir(filepath.Dir(path)); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encodeErr := encoder.Encode(value)
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, 0o600)
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
