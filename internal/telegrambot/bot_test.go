package telegrambot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type blockingHarness struct {
	startOnce           sync.Once
	cancelOnce          sync.Once
	gatewayCancelOnce   sync.Once
	runStarted          chan struct{}
	cancelled           chan struct{}
	gatewayCancelCalled chan struct{}
}

type scriptedHarness struct {
	events        []protocol.Event
	delay         time.Duration
	configStatus  string
	contextStatus string
	authStatus    credentials.Status
}

type replayScriptedHarness struct {
	scriptedHarness

	mu         sync.Mutex
	replaySeq  int64
	replayID   string
	replayed   int
	replayFrom []protocol.Event
}

func newBlockingHarness() *blockingHarness {
	return &blockingHarness{
		runStarted:          make(chan struct{}),
		cancelled:           make(chan struct{}),
		gatewayCancelCalled: make(chan struct{}),
	}
}

func (h *blockingHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h *blockingHarness) RunSession(ctx context.Context, _ string, _ gateway.RunRequest, _ func(protocol.Event)) error {
	h.startOnce.Do(func() { close(h.runStarted) })
	<-ctx.Done()
	h.cancelOnce.Do(func() { close(h.cancelled) })
	return ctx.Err()
}

func (h *blockingHarness) ReplaySessionEvents(context.Context, string, int64, func(protocol.Event)) error {
	return nil
}

func (h *blockingHarness) MCPStatus(context.Context) (string, error) {
	return "{}", nil
}

func (h *blockingHarness) ConfigStatus(context.Context) (string, error) {
	return "billyharness config", nil
}

func (h *blockingHarness) ContextStatus(context.Context, string) (string, error) {
	return "active context: 0", nil
}

func (h *blockingHarness) AuthStatus(context.Context) (credentials.Status, error) {
	return credentials.Status{}, nil
}

func (h *blockingHarness) SaveDeepSeekAPIKey(context.Context, string) (credentials.ProviderStatus, error) {
	return credentials.ProviderStatus{Configured: true, Source: ".env", Path: ".env"}, nil
}

func (h *blockingHarness) ImportCodexAuth(context.Context) (credentials.ProviderStatus, error) {
	return credentials.ProviderStatus{Configured: true, Source: "imported", Path: "auth/codex.json"}, nil
}

func (h *blockingHarness) CancelSession(context.Context, string) (bool, error) {
	h.gatewayCancelOnce.Do(func() { close(h.gatewayCancelCalled) })
	return true, nil
}

func (h scriptedHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h scriptedHarness) RunSession(ctx context.Context, _ string, _ gateway.RunRequest, emit func(protocol.Event)) error {
	for _, event := range h.events {
		emit(event)
	}
	if h.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.delay):
		}
	}
	emit(protocol.Event{Type: protocol.EventRunCompleted})
	return nil
}

func (h scriptedHarness) ReplaySessionEvents(context.Context, string, int64, func(protocol.Event)) error {
	return nil
}

func (h *replayScriptedHarness) ReplaySessionEvents(_ context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	h.mu.Lock()
	h.replayID = sessionID
	h.replaySeq = afterSeq
	h.replayed++
	events := append([]protocol.Event(nil), h.replayFrom...)
	h.mu.Unlock()
	for _, event := range events {
		emit(event)
	}
	return nil
}

func (h scriptedHarness) MCPStatus(context.Context) (string, error) {
	return "{}", nil
}

func (h scriptedHarness) ConfigStatus(context.Context) (string, error) {
	if h.configStatus != "" {
		return h.configStatus, nil
	}
	return "billyharness config", nil
}

func (h scriptedHarness) ContextStatus(context.Context, string) (string, error) {
	if h.contextStatus != "" {
		return h.contextStatus, nil
	}
	return "active context: 0", nil
}

func (h scriptedHarness) AuthStatus(context.Context) (credentials.Status, error) {
	return h.authStatus, nil
}

func (h scriptedHarness) SaveDeepSeekAPIKey(context.Context, string) (credentials.ProviderStatus, error) {
	return credentials.ProviderStatus{Configured: true, Source: ".env", Path: ".env"}, nil
}

func (h scriptedHarness) ImportCodexAuth(context.Context) (credentials.ProviderStatus, error) {
	return credentials.ProviderStatus{Configured: true, Source: "imported", Path: "auth/codex.json"}, nil
}

func (h scriptedHarness) CancelSession(context.Context, string) (bool, error) {
	return false, nil
}

type telegramAuthHarness struct {
	scriptedHarness

	mu               sync.Mutex
	savedDeepSeekKey string
	importedCodex    bool
}

type uniqueSessionHarness struct {
	scriptedHarness

	mu   sync.Mutex
	next int
}

func (h *uniqueSessionHarness) CreateSession(context.Context, string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	return fmt.Sprintf("session-%d", h.next), nil
}

func (h *telegramAuthHarness) SaveDeepSeekAPIKey(_ context.Context, apiKey string) (credentials.ProviderStatus, error) {
	h.mu.Lock()
	h.savedDeepSeekKey = apiKey
	h.mu.Unlock()
	return credentials.ProviderStatus{Configured: true, Source: "/root/billyharness/.env", Path: "/root/billyharness/.env"}, nil
}

func (h *telegramAuthHarness) ImportCodexAuth(context.Context) (credentials.ProviderStatus, error) {
	h.mu.Lock()
	h.importedCodex = true
	h.mu.Unlock()
	return credentials.ProviderStatus{
		Configured: true,
		Source:     "imported",
		Path:       "/root/billyharness/auth/codex.json",
		AccountID:  "acct_123",
		Mode:       "chatgpt",
		Refresh:    "fresh",
	}, nil
}

func TestCancelCommandBypassesActiveRunLock(t *testing.T) {
	harness := newBlockingHarness()
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       t.TempDir() + "/state.json",
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

	runDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{
			Chat: Chat{ID: 123},
			Text: "run a long task",
		})
		close(runDone)
	}()

	select {
	case <-harness.runStarted:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}

	cancelDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{
			Chat: Chat{ID: 123},
			Text: "/cancel",
		})
		close(cancelDone)
	}()

	select {
	case <-cancelDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("/cancel was blocked by the active run lock")
	}
	select {
	case <-harness.cancelled:
	case <-time.After(time.Second):
		t.Fatal("active run was not cancelled")
	}
	select {
	case <-harness.gatewayCancelCalled:
	case <-time.After(time.Second):
		t.Fatal("gateway cancel was not requested")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("run handler did not finish after cancel")
	}
}

func TestTelegramStateSeparatesUsersInSameChat(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	harness := &uniqueSessionHarness{
		scriptedHarness: scriptedHarness{
			events: []protocol.Event{
				{Type: protocol.EventRunStarted},
				{Type: protocol.EventAssistantDelta, Data: "ok"},
			},
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

	userOne := Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "first user"}
	userTwo := Message{Chat: Chat{ID: 123}, From: &User{ID: 1002}, Text: "second user"}
	bot.handleMessage(context.Background(), userOne)
	bot.handleMessage(context.Background(), userTwo)
	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "/model pro"})
	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1002}, Text: "/reasoning low"})

	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	first := state.Chats[userChatKey(123, 0, 1001)]
	second := state.Chats[userChatKey(123, 0, 1002)]
	if first.SessionID == "" || second.SessionID == "" || first.SessionID == second.SessionID {
		t.Fatalf("sessions were not separated: first=%#v second=%#v", first, second)
	}
	if first.Model != "deepseek-v4-pro" {
		t.Fatalf("first user model = %q", first.Model)
	}
	if second.Model != "deepseek-v4-flash" {
		t.Fatalf("second user model changed unexpectedly: %q", second.Model)
	}
	if first.ReasoningEffort != "high" || second.ReasoningEffort != "low" {
		t.Fatalf("reasoning defaults crossed users: first=%q second=%q", first.ReasoningEffort, second.ReasoningEffort)
	}
}

func TestTelegramCancelIsScopedToUser(t *testing.T) {
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       t.TempDir() + "/state.json",
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     false,
		DryRunDefault:   true,
	}, nil, scriptedHarness{})
	if err != nil {
		t.Fatal(err)
	}
	firstCancelled := make(chan struct{})
	secondCancelled := make(chan struct{})
	bot.setCancel(userChatKey(123, 0, 1001), func() { close(firstCancelled) })
	bot.setCancel(userChatKey(123, 0, 1002), func() { close(secondCancelled) })

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "/cancel"})

	select {
	case <-firstCancelled:
	case <-time.After(time.Second):
		t.Fatal("first user's run was not cancelled")
	}
	select {
	case <-secondCancelled:
		t.Fatal("second user's run was cancelled by first user's /cancel")
	default:
	}
}

func TestTelegramLegacyChatStateMigratesToUserKey(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	legacy := ChatState{
		SessionID:       "legacy-session",
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		LastEventSeq:    7,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := (Store{Path: statePath}).Save(State{Chats: map[string]ChatState{
		chatKey(123, 0): legacy,
	}}); err != nil {
		t.Fatal(err)
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
	}, nil, scriptedHarness{})
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, From: &User{ID: 1001}, Text: "/model pro"})

	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	migrated := state.Chats[userChatKey(123, 0, 1001)]
	if migrated.SessionID != "legacy-session" || migrated.LastEventSeq != 7 {
		t.Fatalf("legacy session fields were not preserved: %#v", migrated)
	}
	if migrated.Model != "deepseek-v4-pro" {
		t.Fatalf("legacy state command update did not land on user key: %#v", migrated)
	}
	if state.Chats[chatKey(123, 0)].SessionID != "legacy-session" {
		t.Fatalf("legacy key should remain readable for older clients: %#v", state.Chats)
	}
}

func TestProfileCommandAppliesProfileMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	dir := filepath.Join(home, "profiles", "pro")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.toml"), []byte(`
name = "pro"
model = "deepseek-v4-pro"
reasoning_effort = "max"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(home, "state.json")
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
	}, nil, scriptedHarness{})
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "/profile pro"})
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.Profile != "pro" || chat.Model != "deepseek-v4-pro" || chat.ReasoningEffort != "max" {
		t.Fatalf("chat profile state = %#v", chat)
	}
}

func TestTelegramRunUsesSingleProgressMessageWithInlineTools(t *testing.T) {
	var mu sync.Mutex
	sendMessageCalls := 0
	var editTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/botbottoken/sendMessage":
			mu.Lock()
			sendMessageCalls++
			mu.Unlock()
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		case "/botbottoken/editMessageText":
			if text, ok := payload["text"].(string); ok {
				mu.Lock()
				editTexts = append(editTexts, text)
				mu.Unlock()
			}
			writeTelegramResult(w, true)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(ClientOptions{
		BaseURL:     server.URL,
		Token:       "bottoken",
		MinInterval: time.Nanosecond,
	})
	harness := scriptedHarness{
		delay: 25 * time.Millisecond,
		events: []protocol.Event{
			{Type: protocol.EventRunStarted},
			{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
				ID:        "search-1",
				Name:      "web_search",
				Arguments: json.RawMessage(`{"query":"Moscow weather"}`),
			}},
			{Type: protocol.EventAssistantDelta, Data: "Checking weather..."},
		},
	}
	bot, err := New(Options{
		BotToken:        "bottoken",
		StatePath:       t.TempDir() + "/state.json",
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     true,
		DryRunDefault:   false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "weather"})

	mu.Lock()
	defer mu.Unlock()
	if sendMessageCalls != 1 {
		t.Fatalf("sendMessageCalls = %d, want only placeholder send", sendMessageCalls)
	}
	foundInlineTools := false
	foundDoneTools := false
	for _, text := range editTexts {
		if strings.Contains(text, "Tools running") && strings.Contains(text, "web_search") && strings.Contains(text, "Moscow weather") {
			foundInlineTools = true
		}
		if strings.Contains(text, "Tools done") && strings.Contains(text, "web_search") && strings.Contains(text, "Moscow weather") {
			foundDoneTools = true
		}
	}
	if !foundInlineTools {
		t.Fatalf("stream edits did not include inline tool progress: %#v", editTexts)
	}
	if !foundDoneTools {
		t.Fatalf("final stream edit did not finalize tool progress: %#v", editTexts)
	}
}

func TestTelegramConfigCommandSendsSanitizedSummary(t *testing.T) {
	var sentText string
	var parseMode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botbottoken/sendMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sentText, _ = payload["text"].(string)
		parseMode, _ = payload["parse_mode"].(string)
		writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
	}))
	defer server.Close()

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botbottoken/sendMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sentText, _ = payload["text"].(string)
		parseMode, _ = payload["parse_mode"].(string)
		writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
	}))
	defer server.Close()

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botbottoken/sendMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sentText, _ = payload["text"].(string)
		parseMode, _ = payload["parse_mode"].(string)
		writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
	}))
	defer server.Close()

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
	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
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

func TestTelegramAuthDeepSeekDeletesSecretMessageAndDoesNotRenderKey(t *testing.T) {
	var (
		mu             sync.Mutex
		sentText       string
		parseMode      string
		deleteCalls    int
		deletedMessage int
		deletedChatID  int64
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/botbottoken/sendMessage":
			mu.Lock()
			sentText, _ = payload["text"].(string)
			parseMode, _ = payload["parse_mode"].(string)
			mu.Unlock()
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		case "/botbottoken/deleteMessage":
			mu.Lock()
			deleteCalls++
			deletedChatID = int64(payload["chat_id"].(float64))
			deletedMessage = int(payload["message_id"].(float64))
			mu.Unlock()
			writeTelegramResult(w, true)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	harness := &telegramAuthHarness{}
	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botbottoken/sendMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		text, _ := payload["text"].(string)
		sentTexts = append(sentTexts, text)
		writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
	}))
	defer server.Close()

	harness := &telegramAuthHarness{
		scriptedHarness: scriptedHarness{authStatus: credentials.Status{
			DeepSeek: credentials.ProviderStatus{Configured: true, Source: ".env", Path: "/root/billyharness/.env"},
			Codex:    credentials.ProviderStatus{Configured: true, Source: "imported", Path: "/root/billyharness/auth/codex.json", Mode: "chatgpt", Refresh: "fresh"},
		}},
	}
	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
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

func TestTelegramReplaysMissedGatewayEventsBeforeRun(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	if err := (Store{Path: statePath}).Save(State{Chats: map[string]ChatState{
		"123": {
			SessionID:       "session-1",
			Model:           "deepseek-v4-flash",
			Profile:         "billy",
			ReasoningEffort: "high",
			LastEventSeq:    7,
			UpdatedAt:       time.Now().UTC(),
		},
	}}); err != nil {
		t.Fatal(err)
	}
	harness := &replayScriptedHarness{
		scriptedHarness: scriptedHarness{
			events: []protocol.Event{
				{Seq: 10, Type: protocol.EventModelCallStarted},
				{Seq: 11, Type: protocol.EventAssistantDelta, Data: "new"},
			},
		},
		replayFrom: []protocol.Event{
			{Seq: 8, Type: protocol.EventModelCallStarted},
			{Seq: 9, Type: protocol.EventAssistantDelta, Data: "missed"},
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

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "continue"})

	harness.mu.Lock()
	replayed := harness.replayed
	replayID := harness.replayID
	replaySeq := harness.replaySeq
	harness.mu.Unlock()
	if replayed != 1 || replayID != "session-1" || replaySeq != 7 {
		t.Fatalf("replay = count:%d id:%q seq:%d", replayed, replayID, replaySeq)
	}
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.LastEventSeq != 11 {
		t.Fatalf("LastEventSeq = %d, want 11", chat.LastEventSeq)
	}
	if chat.AgentTurns != 2 {
		t.Fatalf("AgentTurns = %d, want replayed+new model calls", chat.AgentTurns)
	}
}

func TestLiveTelegramRequiresAllowlistUnlessExplicitlyAllowed(t *testing.T) {
	bot, err := New(Options{
		BotToken:      "token",
		StatePath:     t.TempDir() + "/state.json",
		SendEnabled:   true,
		DryRunDefault: false,
	}, nil, newBlockingHarness())
	if err != nil {
		t.Fatal(err)
	}
	if bot.allowed(Message{Chat: Chat{ID: 123}}) {
		t.Fatal("live bot without allowlist should be fail-closed")
	}
	if !bot.opts.RequireAllowlist {
		t.Fatal("live bot should force require-allowlist unless allow-all-chats is explicit")
	}

	allowAll, err := New(Options{
		BotToken:      "token",
		StatePath:     t.TempDir() + "/state.json",
		SendEnabled:   true,
		DryRunDefault: false,
		AllowAllChats: true,
	}, nil, newBlockingHarness())
	if err != nil {
		t.Fatal(err)
	}
	if !allowAll.allowed(Message{Chat: Chat{ID: 123}}) {
		t.Fatal("allow-all-chats should explicitly permit unknown chats")
	}
}

func TestTelegramAllowsExplicitUserID(t *testing.T) {
	bot, err := New(Options{
		BotToken:       "token",
		StatePath:      t.TempDir() + "/state.json",
		AllowedUserIDs: map[int64]bool{8226987886: true},
		SendEnabled:    true,
		DryRunDefault:  false,
	}, nil, newBlockingHarness())
	if err != nil {
		t.Fatal(err)
	}
	if !bot.allowed(Message{Chat: Chat{ID: 999}, From: &User{ID: 8226987886}}) {
		t.Fatal("explicit allowed user should be accepted even when chat id differs")
	}
	if bot.allowed(Message{Chat: Chat{ID: 999}, From: &User{ID: 111}}) {
		t.Fatal("unknown user in unknown chat should be rejected")
	}
}

func TestFinishRichCleansFreshRichMessageBeforeHTMLFallback(t *testing.T) {
	var mu sync.Mutex
	sendRichCalls := 0
	var deleted []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/bottoken/editMessageText":
			writeTelegramError(w, http.StatusBadRequest, "Bad Request: rich edit failed")
		case "/bottoken/sendRichMessage":
			mu.Lock()
			sendRichCalls++
			call := sendRichCalls
			mu.Unlock()
			if call == 1 {
				writeTelegramResult(w, SentMessage{MessageID: 101, Chat: Chat{ID: 123}})
				return
			}
			writeTelegramError(w, http.StatusBadRequest, "Bad Request: rich chunk failed")
		case "/bottoken/deleteMessage":
			if id, ok := payload["message_id"].(float64); ok {
				mu.Lock()
				deleted = append(deleted, int(id))
				mu.Unlock()
			}
			writeTelegramResult(w, true)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(ClientOptions{
		BaseURL:     server.URL,
		Token:       "token",
		MinInterval: time.Nanosecond,
	})
	bot := &Bot{
		opts:   Options{SendEnabled: true},
		client: client,
	}
	renderer := NewRenderer()
	renderer.Content.WriteString(strings.Repeat("rich fallback cleanup 🙂\n", 4000))

	ok := bot.finishRich(context.Background(), Message{Chat: Chat{ID: 123}}, SentMessage{MessageID: 11, Chat: Chat{ID: 123}}, renderer, "model", "high")
	if ok {
		t.Fatal("finishRich returned true after a later rich chunk failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if sendRichCalls != 2 {
		t.Fatalf("sendRichCalls = %d, want 2", sendRichCalls)
	}
	if len(deleted) != 1 || deleted[0] != 101 {
		t.Fatalf("deleted = %#v, want only fresh rich message 101", deleted)
	}
}

func TestEditUsesBoundedContext(t *testing.T) {
	sawDeadline := false
	client := NewClient(ClientOptions{
		BaseURL: "https://api.telegram.test",
		Token:   "token",
		HTTPClient: &http.Client{Transport: botRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			_, sawDeadline = req.Context().Deadline()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
			}, nil
		})},
		MinInterval: time.Nanosecond,
	})
	bot := &Bot{
		opts:   Options{SendEnabled: true},
		client: client,
	}

	if err := bot.edit(context.Background(), 123, 11, "done", ""); err != nil {
		t.Fatal(err)
	}
	if !sawDeadline {
		t.Fatal("edit request context did not include a deadline")
	}
}

type botRoundTripFunc func(*http.Request) (*http.Response, error)

func (f botRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func writeTelegramResult(tw http.ResponseWriter, result any) {
	tw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(tw).Encode(map[string]any{
		"ok":     true,
		"result": result,
	})
}

func writeTelegramError(tw http.ResponseWriter, status int, description string) {
	tw.Header().Set("Content-Type", "application/json")
	tw.WriteHeader(status)
	_ = json.NewEncoder(tw).Encode(map[string]any{
		"ok":          false,
		"error_code":  status,
		"description": description,
	})
}
