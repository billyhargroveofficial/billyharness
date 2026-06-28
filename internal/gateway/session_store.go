package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type sessionStore struct {
	dir string
}

type storedSession struct {
	ID       string             `json:"id"`
	Created  time.Time          `json:"created"`
	Updated  time.Time          `json:"updated"`
	Messages []protocol.Message `json:"messages"`
}

func newSessionStore(dir string) *sessionStore {
	return &sessionStore{dir: filepath.Clean(dir)}
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
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		session, err := s.load(filepath.Join(s.dir, entry.Name()))
		if err == nil && session != nil {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func (s *sessionStore) Save(session *Session) error {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return nil
	}
	if strings.TrimSpace(session.ID) == "" {
		return fmt.Errorf("session id required")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return err
	}
	record := storedSession{
		ID:       session.ID,
		Created:  session.Created,
		Updated:  time.Now().UTC(),
		Messages: session.Thread.Messages(),
	}
	if record.Created.IsZero() {
		record.Created = record.Updated
	}
	bytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, session.ID+".json")
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

func (s *sessionStore) load(path string) (*Session, error) {
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
