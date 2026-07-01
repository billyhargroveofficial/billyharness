package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type StoredSessionTranscript struct {
	Dir        string             `json:"dir"`
	SessionID  string             `json:"session_id"`
	SessionDir string             `json:"session_dir,omitempty"`
	Legacy     bool               `json:"legacy,omitempty"`
	Messages   []protocol.Message `json:"messages,omitempty"`
	Events     []protocol.Event   `json:"events,omitempty"`
	Warnings   []string           `json:"warnings,omitempty"`
}

func LoadStoredSessionTranscript(dir, id string) (StoredSessionTranscript, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = DefaultSessionStoreDir()
	}
	cleanID, err := cleanSessionID(id)
	if err != nil {
		return StoredSessionTranscript{}, err
	}
	sessionDir := filepath.Join(dir, cleanID)
	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	manifest, err := readSessionManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return loadLegacyStoredSessionTranscript(dir, cleanID)
		}
		return StoredSessionTranscript{}, err
	}
	history, err := replaySessionHistory(filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName)), cleanID)
	if err != nil {
		return StoredSessionTranscript{}, err
	}
	events, err := replaySessionEventsAfter(filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName)), cleanID, 0)
	if err != nil {
		return StoredSessionTranscript{}, err
	}
	out := StoredSessionTranscript{
		Dir:        dir,
		SessionID:  cleanID,
		SessionDir: sessionDir,
		Messages:   append([]protocol.Message(nil), history.messages...),
		Events:     append([]protocol.Event(nil), events...),
	}
	if len(out.Events) == 0 {
		out.Warnings = append(out.Warnings, "events JSONL is empty; export will use stored messages")
	}
	return out, nil
}

func loadLegacyStoredSessionTranscript(dir, id string) (StoredSessionTranscript, error) {
	path := filepath.Join(dir, id+".json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return StoredSessionTranscript{}, err
	}
	var record storedSession
	if err := json.Unmarshal(bytes, &record); err != nil {
		return StoredSessionTranscript{}, err
	}
	return StoredSessionTranscript{
		Dir:       dir,
		SessionID: id,
		Legacy:    true,
		Messages:  append([]protocol.Message(nil), record.Messages...),
		Warnings:  []string{"legacy snapshot only; events JSONL missing"},
	}, nil
}
