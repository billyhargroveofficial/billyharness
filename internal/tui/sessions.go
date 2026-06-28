package tui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type chatSession struct {
	ID               string             `json:"id"`
	Title            string             `json:"title"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	Model            string             `json:"model"`
	Profile          string             `json:"profile,omitempty"`
	ReasoningKind    string             `json:"reasoning_kind"`
	ReasoningEffort  string             `json:"reasoning_effort"`
	GatewaySessionID string             `json:"gateway_session_id,omitempty"`
	Messages         []protocol.Message `json:"messages,omitempty"`
	Blocks           []savedBlock       `json:"blocks,omitempty"`
	InputTokens      int64              `json:"input_tokens"`
	OutputTokens     int64              `json:"output_tokens"`
	CacheHitTokens   int64              `json:"cache_hit_tokens"`
	CacheMissTokens  int64              `json:"cache_miss_tokens"`
	ReasoningTokens  int64              `json:"reasoning_tokens"`
	ToolCalls        int                `json:"tool_calls"`
	ModelCalls       int                `json:"model_calls"`
}

type savedBlock struct {
	ID           string `json:"id,omitempty"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	EventType    string `json:"event_type,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
	StepID       string `json:"step_id,omitempty"`
	CallID       string `json:"call_id,omitempty"`
	AttemptID    string `json:"attempt_id,omitempty"`
	ParentStepID string `json:"parent_step_id,omitempty"`
	RawCopy      string `json:"raw_copy,omitempty"`
}

func newChatID() string {
	var bytes [6]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func saveChatSession(dir string, session chatSession) error {
	if session.ID == "" {
		return fmt.Errorf("session id required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, session.ID+".json")
	bytes, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(bytes, '\n'), 0o600)
}

func loadChatSession(dir, prefix string) (chatSession, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return chatSession{}, fmt.Errorf("session id required")
	}
	sessions, err := listChatSessions(dir)
	if err != nil {
		return chatSession{}, err
	}
	var matches []chatSession
	for _, session := range sessions {
		if session.ID == prefix || strings.HasPrefix(session.ID, prefix) {
			matches = append(matches, session)
		}
	}
	if len(matches) == 0 {
		return chatSession{}, fmt.Errorf("session not found: %s", prefix)
	}
	if len(matches) > 1 {
		return chatSession{}, fmt.Errorf("session id prefix is ambiguous: %s", prefix)
	}
	return matches[0], nil
}

func listChatSessions(dir string) ([]chatSession, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []chatSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		bytes, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var session chatSession
		if err := json.Unmarshal(bytes, &session); err == nil && session.ID != "" {
			sessions = append(sessions, session)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func sessionTitle(prompt string) string {
	text := oneLinePreview(prompt, 54)
	if text == "" {
		return "untitled"
	}
	return text
}
