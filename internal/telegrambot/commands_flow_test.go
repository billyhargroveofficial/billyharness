package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestTelegramConfigCommandSendsSanitizedSummary(t *testing.T) {
	var sentText string
	var parseMode string
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			parseMode, _ = payload["parse_mode"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, scriptedHarness{configStatus: "billyharness config\nprovider: deepseek\napi_key: [redacted]"})
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "/config"})
	if parseMode != "HTML" || !strings.Contains(sentText, "<b>Config</b>") || !strings.Contains(sentText, "provider: deepseek") {
		t.Fatalf("config message parse=%q text=%q", parseMode, sentText)
	}
	if strings.Contains(sentText, "sk-secret") {
		t.Fatalf("config leaked secret: %q", sentText)
	}
}

func TestTelegramContextCommandShowsSessionContext(t *testing.T) {
	var sentText string
	var parseMode string
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			parseMode, _ = payload["parse_mode"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, scriptedHarness{contextStatus: "active context: 580.0k / 1.00M\nsources:\n  web_summaries: 320.0k"})
	if err != nil {
		t.Fatal(err)
	}
	state := bot.chatState(chatKey(123, 0))
	state.SessionID = "session-1"
	bot.setChatState(chatKey(123, 0), state)

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "/context"})
	if parseMode != "HTML" || !strings.Contains(sentText, "<b>Context</b>") || !strings.Contains(sentText, "web_summaries") {
		t.Fatalf("context message parse=%q text=%q", parseMode, sentText)
	}
}

func TestTelegramToolViewShowsCompactLastRunTools(t *testing.T) {
	var sentText string
	var parseMode string
	harness := &replayScriptedHarness{
		replayFrom: []protocol.Event{
			{RunID: "old-run", Type: protocol.EventRunStarted},
			{RunID: "old-run", Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "old-search", Name: "web_search", Arguments: json.RawMessage(`{"query":"old query"}`)}},
			{RunID: "new-run", Type: protocol.EventRunStarted},
			{RunID: "new-run", Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "search-1", Name: "web_search", Arguments: json.RawMessage(`{"query":"telegram bot api"}`)}},
			{RunID: "new-run", Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
				CallID:  "search-1",
				Name:    "web_search",
				Content: "raw output that should stay hidden",
				Metadata: map[string]any{
					"duration_ms":           int64(42),
					"web_cache_hit":         true,
					"estimated_text_tokens": int64(1200),
				},
			}},
			{RunID: "new-run", CallID: "shell-1", Type: protocol.EventToolCallFailed, Data: protocol.ToolProgressEvent{CallID: "shell-1", Name: "shell_exec", Message: "exit status 1"}},
		},
	}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			parseMode, _ = payload["parse_mode"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}
	bot.setChatState(userChatKey(123, 0, 1001), ChatState{SessionID: "session-1"})

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "/toolview"})

	if parseMode != "HTML" || !strings.Contains(sentText, "<b>Toolview</b>") {
		t.Fatalf("toolview parse=%q text=%q", parseMode, sentText)
	}
	for _, want := range []string{"web_search", "telegram bot api", "cache hit", "~1.2k tok", "shell_exec failed"} {
		if !strings.Contains(sentText, want) {
			t.Fatalf("toolview missing %q: %q", want, sentText)
		}
	}
	for _, notWant := range []string{"old query", "raw output that should stay hidden"} {
		if strings.Contains(sentText, notWant) {
			t.Fatalf("toolview leaked %q: %q", notWant, sentText)
		}
	}
	harness.mu.Lock()
	replaySeq := harness.replaySeq
	harness.mu.Unlock()
	if replaySeq != 0 {
		t.Fatalf("toolview should replay current session from start, got after_seq=%d", replaySeq)
	}
}

func TestTelegramResumeListsAndSelectsGatewaySession(t *testing.T) {
	var sentTexts []string
	statePath := t.TempDir() + "/state.json"
	harness := &telegramSessionHarness{
		sessions: []gatewayapi.SessionSummary{
			{ID: "abc123456789", MessageCount: 9, RunSeq: 3, Profile: "billy", Model: "deepseek-v4-pro", ReasoningEffort: "max"},
			{ID: "def123456789", MessageCount: 2, Profile: "billy", Model: "deepseek-v4-flash"},
		},
	}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			text, _ := payload["text"].(string)
			sentTexts = append(sentTexts, text)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      statePath,
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}}

	msg.Text = "/resume"
	bot.handleMessage(context.Background(), msg)
	msg.Text = "/resume abc123"
	bot.handleMessage(context.Background(), msg)

	if len(sentTexts) != 2 || !strings.Contains(sentTexts[0], "<b>Sessions</b>") || !strings.Contains(sentTexts[0], "abc123456789") {
		t.Fatalf("resume list messages = %#v", sentTexts)
	}
	if !strings.Contains(sentTexts[1], "Resumed Billyharness session") {
		t.Fatalf("resume response = %#v", sentTexts)
	}
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats[userChatKey(123, 0, 1001)]
	if chat.SessionID != "abc123456789" || chat.Model != "deepseek-v4-pro" || chat.Profile != "billy" || chat.ReasoningEffort != "max" || chat.AgentTurns != 3 {
		t.Fatalf("resumed chat state = %#v", chat)
	}
}

func TestTelegramResumeFiltersOtherUserOwnedSessions(t *testing.T) {
	var sentTexts []string
	harness := &telegramSessionHarness{
		sessions: []gatewayapi.SessionSummary{
			{ID: "own-session", MessageCount: 1, Owner: gatewayapi.SessionOwner{ClientType: "telegram", TelegramChatID: 123, TelegramUserID: 1001}},
			{ID: "other-session", MessageCount: 1, Owner: gatewayapi.SessionOwner{ClientType: "telegram", TelegramChatID: 123, TelegramUserID: 2002}},
			{ID: "legacy-session", MessageCount: 1},
		},
	}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			text, _ := payload["text"].(string)
			sentTexts = append(sentTexts, text)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}}

	msg.Text = "/resume"
	bot.handleMessage(context.Background(), msg)
	msg.Text = "/resume other"
	bot.handleMessage(context.Background(), msg)

	if len(sentTexts) != 2 {
		t.Fatalf("resume messages = %#v", sentTexts)
	}
	if !strings.Contains(sentTexts[0], "own-session") || !strings.Contains(sentTexts[0], short("legacy-session")) {
		t.Fatalf("resume list should include own and legacy sessions: %q", sentTexts[0])
	}
	if strings.Contains(sentTexts[0], "other-session") {
		t.Fatalf("resume list leaked another user's session: %q", sentTexts[0])
	}
	if !strings.Contains(sentTexts[1], "not found") {
		t.Fatalf("explicit other-user resume should fail, got %q", sentTexts[1])
	}
}

func TestTelegramNewSessionStampsOwnerMetadata(t *testing.T) {
	harness := &telegramSessionHarness{createdID: "new-session"}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-pro",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, ThreadID: 7, From: &User{ID: 1001}, Text: "/new"})

	harness.mu.Lock()
	createdOwner := harness.createdOwner
	harness.mu.Unlock()
	want := gatewayapi.SessionOwner{
		ClientType:       "telegram",
		TelegramChatID:   123,
		TelegramThreadID: 7,
		TelegramUserID:   1001,
		Profile:          "billy",
		Model:            "deepseek-v4-pro",
	}
	if createdOwner != want {
		t.Fatalf("created owner = %#v, want %#v", createdOwner, want)
	}
}

func TestTelegramForkClonesGatewaySessionMessages(t *testing.T) {
	var sentText string
	statePath := t.TempDir() + "/state.json"
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
		{Role: protocol.RoleAssistant, Content: "world"},
	}
	harness := &telegramSessionHarness{
		sessions: []gatewayapi.SessionSummary{{ID: "source-session", MessageCount: len(messages), Profile: "billy"}},
		full: map[string]gatewayapi.SessionResponse{
			"source-session": {ID: "source-session", Messages: messages, MessageCount: len(messages)},
		},
		createdID: "forked-session",
	}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      statePath,
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}
	bot.setChatState(userChatKey(123, 0, 1001), ChatState{
		SessionID:    "source-session",
		Profile:      "billy",
		AgentTurns:   4,
		ToolCalls:    11,
		LastEventSeq: 99,
	})

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "/fork current"})

	if !strings.Contains(sentText, "Forked source-sessi into forked-sessi") {
		t.Fatalf("fork response = %q", sentText)
	}
	harness.mu.Lock()
	createdProfile := harness.createdProfile
	createdMessages := append([]protocol.Message(nil), harness.createdMessages...)
	createdOwner := harness.createdOwner
	harness.mu.Unlock()
	if createdProfile != "billy" || len(createdMessages) != len(messages) || createdMessages[1].Content != "hello" {
		t.Fatalf("created profile=%q messages=%#v", createdProfile, createdMessages)
	}
	if createdOwner.ClientType != "telegram" || createdOwner.TelegramChatID != 123 || createdOwner.TelegramUserID != 1001 || createdOwner.Profile != "billy" {
		t.Fatalf("created owner = %#v", createdOwner)
	}
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats[userChatKey(123, 0, 1001)]
	if chat.SessionID != "forked-session" || chat.AgentTurns != 0 || chat.ToolCalls != 0 || chat.LastEventSeq != 0 {
		t.Fatalf("forked chat state = %#v", chat)
	}
}

func TestTelegramForkRejectsOtherUserOwnedSession(t *testing.T) {
	var sentText string
	harness := &telegramSessionHarness{
		sessions: []gatewayapi.SessionSummary{
			{ID: "other-session", MessageCount: 1, Owner: gatewayapi.SessionOwner{ClientType: "telegram", TelegramChatID: 123, TelegramUserID: 2002}},
		},
		full: map[string]gatewayapi.SessionResponse{
			"other-session": {ID: "other-session", Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "private"}}},
		},
	}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "/fork other"})

	if !strings.Contains(sentText, "not found") {
		t.Fatalf("fork response = %q", sentText)
	}
	harness.mu.Lock()
	createdMessages := append([]protocol.Message(nil), harness.createdMessages...)
	harness.mu.Unlock()
	if len(createdMessages) != 0 {
		t.Fatalf("fork should not clone another user's session: %#v", createdMessages)
	}
}

func TestTelegramAuthDeepSeekDeletesSecretMessageAndDoesNotRenderKey(t *testing.T) {
	var (
		mu             sync.Mutex
		sentText       string
		parseMode      string
		deleteCalls    int
		deletedMessage int
		deletedChatID  int64
	)
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			mu.Lock()
			sentText, _ = payload["text"].(string)
			parseMode, _ = payload["parse_mode"].(string)
			mu.Unlock()
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		},
		"deleteMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			mu.Lock()
			deleteCalls++
			deletedChatID = int64(payload["chat_id"].(float64))
			deletedMessage = int(payload["message_id"].(float64))
			mu.Unlock()
			writeTelegramResult(w, true)
		},
	})

	harness := &telegramAuthHarness{}
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}

	const secret = "sk-test-telegram-secret"
	bot.handleMessage(context.Background(), Message{MessageID: 77, Chat: Chat{ID: 123}, Text: "/auth deepseek " + secret})

	harness.mu.Lock()
	saved := harness.savedDeepSeekKey
	harness.mu.Unlock()
	if saved != secret {
		t.Fatalf("saved DeepSeek key = %q, want %q", saved, secret)
	}
	mu.Lock()
	defer mu.Unlock()
	if deleteCalls != 1 || deletedChatID != 123 || deletedMessage != 77 {
		t.Fatalf("delete call = count %d chat %d message %d", deleteCalls, deletedChatID, deletedMessage)
	}
	if parseMode != "HTML" || !strings.Contains(sentText, "<b>Auth updated</b>") || !strings.Contains(sentText, "deepseek") {
		t.Fatalf("auth response parse=%q text=%q", parseMode, sentText)
	}
	if strings.Contains(sentText, secret) || strings.Contains(sentText, "sk-test") {
		t.Fatalf("auth response leaked secret: %q", sentText)
	}
}

func TestTelegramAuthCodexImportAndStatus(t *testing.T) {
	var sentTexts []string
	harness := &telegramAuthHarness{
		scriptedHarness: scriptedHarness{authStatus: credentials.Status{
			DeepSeek: credentials.ProviderStatus{Configured: true, Source: ".env", Path: "/root/billyharness/.env"},
			Codex:    credentials.ProviderStatus{Configured: true, Source: "imported", Path: "/root/billyharness/auth/codex.json", Mode: "chatgpt", Refresh: "fresh"},
		}},
	}
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			text, _ := payload["text"].(string)
			sentTexts = append(sentTexts, text)
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:       "bottoken",
		StatePath:      t.TempDir() + "/state.json",
		Model:          "deepseek-v4-flash",
		Profile:        "billy",
		AllowedChatIDs: map[int64]bool{123: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{MessageID: 78, Chat: Chat{ID: 123}, Text: "/auth"})
	bot.handleMessage(context.Background(), Message{MessageID: 79, Chat: Chat{ID: 123}, Text: "/auth codex"})

	harness.mu.Lock()
	imported := harness.importedCodex
	harness.mu.Unlock()
	if !imported {
		t.Fatal("codex import was not called")
	}
	if len(sentTexts) != 2 {
		t.Fatalf("sent %d auth messages, want 2: %#v", len(sentTexts), sentTexts)
	}
	if !strings.Contains(sentTexts[0], "<b>Auth</b>") || !strings.Contains(sentTexts[0], "refresh=fresh") {
		t.Fatalf("auth status text = %q", sentTexts[0])
	}
	if !strings.Contains(sentTexts[1], "<b>Auth updated</b>") || !strings.Contains(sentTexts[1], "acct_123") {
		t.Fatalf("auth import text = %q", sentTexts[1])
	}
	if strings.Contains(strings.Join(sentTexts, "\n"), "refresh_token") {
		t.Fatalf("auth text leaked token-ish payload: %#v", sentTexts)
	}
}

func TestTelegramChatStateAccumulatesTurnsAndTools(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	harness := scriptedHarness{
		events: []protocol.Event{
			{Type: protocol.EventRunStarted},
			{Type: protocol.EventModelCallStarted},
			{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{Name: "web_search", Arguments: json.RawMessage(`{"query":"one"}`)}},
			{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com"}`)}},
			{Type: protocol.EventAssistantDelta, Data: "done"},
		},
	}
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       statePath,
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     false,
		DryRunDefault:   true,
	}, nil, harness)
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "first"})
	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "second"})

	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.AgentTurns != 2 || chat.ToolCalls != 4 {
		t.Fatalf("chat totals = turns:%d tools:%d", chat.AgentTurns, chat.ToolCalls)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "/new"})
	state, err = (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat = state.Chats["123"]
	if chat.AgentTurns != 0 || chat.ToolCalls != 0 {
		t.Fatalf("/new should reset chat totals, got turns:%d tools:%d", chat.AgentTurns, chat.ToolCalls)
	}
}
