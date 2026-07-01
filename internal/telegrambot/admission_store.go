package telegrambot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

const telegramAdmissionSchemaVersion = 1

type telegramAdmissionStore struct {
	path string
	mu   sync.Mutex
}

type telegramAdmissionRecord struct {
	SchemaVersion int       `json:"schema_version"`
	Seq           int64     `json:"seq"`
	Timestamp     time.Time `json:"ts"`
	Kind          string    `json:"kind"`
	UpdateID      int       `json:"update_id"`
	MessageID     int       `json:"message_id,omitempty"`
	ChatID        int64     `json:"chat_id,omitempty"`
	ThreadID      int       `json:"thread_id,omitempty"`
	UserID        int64     `json:"user_id,omitempty"`
	Key           string    `json:"key,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	InputID       string    `json:"input_id,omitempty"`
	PromptSHA256  string    `json:"prompt_sha256,omitempty"`
	GatewayState  string    `json:"gateway_state,omitempty"`
	Duplicate     bool      `json:"duplicate,omitempty"`
}

func newTelegramAdmissionStore(statePath string) telegramAdmissionStore {
	statePath = strings.TrimSpace(statePath)
	if statePath == "" {
		return telegramAdmissionStore{}
	}
	dir := filepath.Dir(statePath)
	base := strings.TrimSuffix(filepath.Base(statePath), filepath.Ext(statePath))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "telegram-state"
	}
	return telegramAdmissionStore{path: filepath.Join(dir, base+".admissions.jsonl")}
}

func (s *telegramAdmissionStore) RecordIgnored(update Update, reason string) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	record := telegramAdmissionRecord{
		SchemaVersion: telegramAdmissionSchemaVersion,
		Timestamp:     time.Now().UTC(),
		Kind:          "ignored",
		UpdateID:      update.UpdateID,
		Reason:        strings.TrimSpace(reason),
	}
	if update.Message != nil {
		record.MessageID = update.Message.MessageID
		record.ChatID = update.Message.Chat.ID
		record.ThreadID = update.Message.ThreadID
		record.UserID = messageUserID(*update.Message)
		record.Key = messageChatKey(*update.Message)
		record.PromptSHA256 = telegramPromptHash(update.Message.Text)
	}
	return s.append(record)
}

func (s *telegramAdmissionStore) RecordAdmitted(updateID int, msg Message, sessionID string, resp gatewayapi.SessionInputResponse) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	record := telegramAdmissionRecord{
		SchemaVersion: telegramAdmissionSchemaVersion,
		Timestamp:     time.Now().UTC(),
		Kind:          "admitted",
		UpdateID:      updateID,
		MessageID:     msg.MessageID,
		ChatID:        msg.Chat.ID,
		ThreadID:      msg.ThreadID,
		UserID:        messageUserID(msg),
		Key:           messageChatKey(msg),
		SessionID:     sessionID,
		InputID:       resp.InputID,
		PromptSHA256:  telegramPromptHash(msg.Text),
		GatewayState:  resp.State,
		Duplicate:     resp.Duplicate,
	}
	return s.append(record)
}

func (s *telegramAdmissionStore) append(record telegramAdmissionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lastSeq, err := s.lastSeqLocked()
	if err != nil {
		return err
	}
	record.Seq = lastSeq + 1
	return eventlog.AppendJSONL(s.path, record)
}

func (s *telegramAdmissionStore) lastSeqLocked() (int64, error) {
	var lastSeq int64
	err := eventlog.ReplayJSONL[telegramAdmissionRecord](s.path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[telegramAdmissionRecord]) error {
		record := item.Value
		want := lastSeq + 1
		if record.SchemaVersion != 0 && record.SchemaVersion != telegramAdmissionSchemaVersion {
			return eventlog.NewCorruptionError(s.path, item.Line, want, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != want {
			return eventlog.NewCorruptionError(s.path, item.Line, want, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, want))
		}
		lastSeq = record.Seq
		return nil
	})
	return lastSeq, err
}

func telegramPromptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}
