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

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	tuiruntime "github.com/billyhargroveofficial/billyharness/internal/tui/runtimeclient"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
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
	GatewayEventSeq  int64              `json:"gateway_event_seq,omitempty"`
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

type savedBlock = transcript.PersistedCell

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

func (m *Model) resetFreshChatState(title string) {
	m.localChatID = newChatID()
	m.chatTitle = title
	if m.chatTitle == "" {
		m.chatTitle = "new chat"
	}
	m.chatCreated = time.Now().UTC()
	m.messages = tuiruntime.InitialMessages(m.instructions)
	m.blocks = nil
	m.clearRichRenderCache()
	m.collapsed = map[int]bool{}
	m.selected = 0
	m.modelCalls = 0
	m.toolCalls = 0
	m.inputTok = 0
	m.outputTok = 0
	m.cacheHitTok = 0
	m.cacheMissTok = 0
	m.reasoningTok = 0
	m.lastInputTok = 0
	m.lastOutputTok = 0
	m.lastCacheHitTok = 0
	m.lastCacheMissTok = 0
	m.toolSummaryInTok = 0
	m.toolSummaryOutTok = 0
	m.toolSummaryAPITok = 0
	m.helperModelInTok = 0
	m.helperModelOutTok = 0
	m.helperModelCacheHit = 0
	m.helperModelCacheMiss = 0
	m.helperModelAPITok = 0
	m.resetProjectedAccounting()
	m.sessionID = ""
	m.lastGatewayEventSeq = 0
	m.followOutput = true
	m.resetTranscriptProjector()
}

func (m *Model) newChat() tea.Cmd {
	if m.busy {
		m.status = "busy"
		return nil
	}
	_ = m.saveCurrentSession()
	m.resetFreshChatState("new chat")
	m.status = "new chat " + shortID(m.localChatID)
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) resumeChat(prefix string) tea.Cmd {
	if m.busy {
		m.status = "busy"
		return nil
	}
	prefix = strings.TrimSpace(strings.ToLower(prefix))
	if prefix == "list" || prefix == "all" {
		prefix = ""
	}
	if strings.TrimSpace(prefix) == "" {
		m.addInfoBlock("CHATS", m.sessionsText())
		m.status = "chats listed"
		return nil
	}
	session, err := loadChatSession(m.sessionsDir, prefix)
	if err != nil {
		m.status = err.Error()
		m.addBlock("error", "ERROR", err.Error())
		return nil
	}
	m.applyChatSession(session)
	m.status = "resumed " + shortID(m.localChatID)
	if m.gatewayURL != "" {
		if strings.TrimSpace(m.sessionID) != "" {
			return m.replayGatewayEventsCmd(true)
		}
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) forkChat(prefix string) tea.Cmd {
	if m.busy {
		m.status = "busy"
		return nil
	}
	prefix = strings.TrimSpace(strings.ToLower(prefix))
	if prefix == "current" {
		prefix = ""
	}
	if strings.TrimSpace(prefix) != "" {
		session, err := loadChatSession(m.sessionsDir, prefix)
		if err != nil {
			m.status = err.Error()
			m.addBlock("error", "ERROR", err.Error())
			return nil
		}
		m.applyChatSession(session)
	}
	old := m.localChatID
	m.localChatID = newChatID()
	m.chatTitle = "fork of " + shortID(old)
	m.chatCreated = time.Now().UTC()
	m.sessionID = ""
	m.lastGatewayEventSeq = 0
	m.status = "forked " + shortID(old) + " -> " + shortID(m.localChatID)
	m.addInfoBlock("FORK", m.status)
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) applyChatSession(session chatSession) {
	m.localChatID = session.ID
	m.chatTitle = session.Title
	if m.chatTitle == "" {
		m.chatTitle = "untitled"
	}
	m.chatCreated = session.CreatedAt
	if m.chatCreated.IsZero() {
		m.chatCreated = time.Now().UTC()
	}
	if session.Profile != "" {
		m.cfg.Profile = config.NormalizeProfileName(session.Profile)
	}
	m.refreshConfigProjections()
	if len(session.Messages) > 0 {
		m.messages = append([]protocol.Message(nil), session.Messages...)
	} else {
		m.messages = tuiruntime.InitialMessages(m.instructions)
	}
	m.blocks = decodeBlocks(session.Blocks)
	m.clearRichRenderCache()
	m.ensureBlockMetadata()
	m.resetTranscriptProjector()
	m.collapsed = map[int]bool{}
	m.selected = max(0, len(m.blocks)-1)
	m.inputTok = session.InputTokens
	m.outputTok = session.OutputTokens
	m.cacheHitTok = session.CacheHitTokens
	m.cacheMissTok = session.CacheMissTokens
	m.reasoningTok = session.ReasoningTokens
	m.lastInputTok = 0
	m.lastOutputTok = 0
	m.lastCacheHitTok = 0
	m.lastCacheMissTok = 0
	m.toolSummaryInTok = 0
	m.toolSummaryOutTok = 0
	m.toolSummaryAPITok = 0
	m.helperModelInTok = 0
	m.helperModelOutTok = 0
	m.helperModelCacheHit = 0
	m.helperModelCacheMiss = 0
	m.helperModelAPITok = 0
	m.toolCalls = session.ToolCalls
	m.modelCalls = session.ModelCalls
	m.resetProjectedAccounting()
	m.sessionID = session.GatewaySessionID
	m.lastGatewayEventSeq = session.GatewayEventSeq
	if session.Model != "" {
		m.models = appendIfMissing(m.models, session.Model)
		for i, model := range m.models {
			if model == session.Model {
				m.modelIndex = i
				break
			}
		}
	}
	for i, mode := range m.thinking {
		if mode.kind == session.ReasoningKind && mode.effort == session.ReasoningEffort {
			m.thinkingIdx = i
			break
		}
	}
	m.refreshConfigProjections()
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	m.reflow(true)
}

func (m Model) sessionsText() string {
	sessions, err := listChatSessions(m.sessionsDir)
	if err != nil {
		return err.Error()
	}
	if len(sessions) == 0 {
		return "no saved chats"
	}
	var lines []string
	for i, session := range sessions {
		if i >= 20 {
			lines = append(lines, fmt.Sprintf("... %d more", len(sessions)-i))
			break
		}
		title := session.Title
		if title == "" {
			title = "untitled"
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  tok:%d/%d tools:%d",
			shortID(session.ID),
			session.UpdatedAt.Local().Format("2006-01-02 15:04"),
			title,
			session.InputTokens,
			session.OutputTokens,
			session.ToolCalls,
		))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) saveCurrentSession() error {
	if m.sessionsDir == "" || m.localChatID == "" {
		return nil
	}
	created := m.chatCreated
	if created.IsZero() {
		created = time.Now().UTC()
	}
	title := m.chatTitle
	if title == "" {
		title = "untitled"
	}
	return saveChatSession(m.sessionsDir, chatSession{
		ID:               m.localChatID,
		Title:            title,
		CreatedAt:        created,
		UpdatedAt:        time.Now().UTC(),
		Model:            m.currentModel(),
		Profile:          m.currentProfile(),
		ReasoningKind:    m.currentThinking().kind,
		ReasoningEffort:  m.currentThinking().effort,
		GatewaySessionID: m.sessionID,
		GatewayEventSeq:  m.lastGatewayEventSeq,
		Messages:         append([]protocol.Message(nil), m.messages...),
		Blocks:           encodeBlocks(m.blocks),
		InputTokens:      m.inputTok,
		OutputTokens:     m.outputTok,
		CacheHitTokens:   m.cacheHitTok,
		CacheMissTokens:  m.cacheMissTok,
		ReasoningTokens:  m.reasoningTok,
		ToolCalls:        m.toolCalls,
		ModelCalls:       m.modelCalls,
	})
}
