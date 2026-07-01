package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sessionIndexDirName  = "index"
	sessionIndexFileName = "sessions.json"
)

type StoredSessionIndex struct {
	SchemaVersion int                    `json:"schema_version"`
	BuiltAt       time.Time              `json:"built_at"`
	Dir           string                 `json:"dir"`
	SessionCount  int                    `json:"session_count"`
	Sessions      []StoredSessionSummary `json:"sessions"`
	Warnings      []string               `json:"warnings,omitempty"`
}

func RebuildStoredSessionIndex(dir string) (StoredSessionIndex, error) {
	dir = cleanSessionStoreDir(dir)
	list, err := ListStoredSessions(dir)
	if err != nil {
		return StoredSessionIndex{}, err
	}
	index := StoredSessionIndex{
		SchemaVersion: gatewaySessionSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		Dir:           list.Dir,
		SessionCount:  len(list.Sessions),
		Sessions:      list.Sessions,
		Warnings:      list.Warnings,
	}
	if err := writeStoredSessionIndex(indexPath(dir), index); err != nil {
		return StoredSessionIndex{}, err
	}
	return index, nil
}

func ReadStoredSessionIndex(dir string) (StoredSessionIndex, error) {
	dir = cleanSessionStoreDir(dir)
	body, err := os.ReadFile(indexPath(dir))
	if err != nil {
		return StoredSessionIndex{}, err
	}
	var index StoredSessionIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return StoredSessionIndex{}, err
	}
	if index.SchemaVersion != 0 && index.SchemaVersion != gatewaySessionSchemaVersion {
		return StoredSessionIndex{}, errors.New("unsupported session index schema_version")
	}
	return index, nil
}

func DeleteStoredSessionIndex(dir string) error {
	dir = cleanSessionStoreDir(dir)
	for _, path := range []string{indexPath(dir), diagnosticsIndexPath(dir)} {
		err := os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			continue
		}
		return err
	}
	return nil
}

func writeStoredSessionIndex(path string, index StoredSessionIndex) error {
	if index.SchemaVersion == 0 {
		index.SchemaVersion = gatewaySessionSchemaVersion
	}
	if index.BuiltAt.IsZero() {
		index.BuiltAt = time.Now().UTC()
	}
	index.SessionCount = len(index.Sessions)
	return writeJSONIndex(path, index)
}

func writeJSONIndex(path string, value any) error {
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
	encodeErr := encoder.Encode(value)
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

func indexPath(dir string) string {
	return filepath.Join(cleanSessionStoreDir(dir), sessionIndexDirName, sessionIndexFileName)
}

func cleanSessionStoreDir(dir string) string {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return DefaultSessionStoreDir()
	}
	return dir
}
