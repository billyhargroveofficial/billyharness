package telegrambot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

type blockingHarness struct {
	startOnce           sync.Once
	cancelOnce          sync.Once
	gatewayCancelOnce   sync.Once
	runStarted          chan struct{}
	cancelled           chan struct{}
	gatewayCancelCalled chan struct{}
}

type interruptHarness struct {
	mu                  sync.Mutex
	cancelOnce          sync.Once
	gatewayCancelOnce   sync.Once
	runCalls            int
	prompts             []string
	interruptPolicies   []string
	firstStarted        chan struct{}
	firstCancelled      chan struct{}
	secondCompleted     chan struct{}
	gatewayCancelCalled chan struct{}
}

type lateSuccessInterruptHarness struct {
	*interruptHarness
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

type missingSessionRetryHarness struct {
	scriptedHarness

	mu       sync.Mutex
	created  []string
	runIDs   []string
	prompts  []string
	runCalls int
}

func newBlockingHarness() *blockingHarness {
	return &blockingHarness{
		runStarted:          make(chan struct{}),
		cancelled:           make(chan struct{}),
		gatewayCancelCalled: make(chan struct{}),
	}
}

func newInterruptHarness() *interruptHarness {
	return &interruptHarness{
		firstStarted:        make(chan struct{}),
		firstCancelled:      make(chan struct{}),
		secondCompleted:     make(chan struct{}),
		gatewayCancelCalled: make(chan struct{}),
	}
}

func newLateSuccessInterruptHarness() *lateSuccessInterruptHarness {
	return &lateSuccessInterruptHarness{interruptHarness: newInterruptHarness()}
}

func (h *blockingHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h *blockingHarness) RunSession(ctx context.Context, _ string, _ gatewayapi.RunRequest, _ func(protocol.Event)) error {
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

func (h *interruptHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h *interruptHarness) RunSession(ctx context.Context, _ string, req gatewayapi.RunRequest, emit func(protocol.Event)) error {
	h.mu.Lock()
	h.runCalls++
	call := h.runCalls
	h.prompts = append(h.prompts, req.Prompt)
	h.interruptPolicies = append(h.interruptPolicies, req.InterruptPolicy)
	h.mu.Unlock()

	switch call {
	case 1:
		close(h.firstStarted)
		<-ctx.Done()
		h.cancelOnce.Do(func() { close(h.firstCancelled) })
		return ctx.Err()
	default:
		emit(protocol.Event{Type: protocol.EventRunStarted})
		emit(protocol.Event{Type: protocol.EventAssistantDelta, Data: "handled: " + req.Prompt})
		close(h.secondCompleted)
		return nil
	}
}

func (h *lateSuccessInterruptHarness) RunSession(ctx context.Context, _ string, req gatewayapi.RunRequest, emit func(protocol.Event)) error {
	h.mu.Lock()
	h.runCalls++
	call := h.runCalls
	h.prompts = append(h.prompts, req.Prompt)
	h.interruptPolicies = append(h.interruptPolicies, req.InterruptPolicy)
	h.mu.Unlock()

	switch call {
	case 1:
		close(h.firstStarted)
		<-ctx.Done()
		h.cancelOnce.Do(func() { close(h.firstCancelled) })
		emit(protocol.Event{Type: protocol.EventAssistantDelta, Data: "old answer should not render"})
		return nil
	default:
		emit(protocol.Event{Type: protocol.EventRunStarted})
		emit(protocol.Event{Type: protocol.EventAssistantDelta, Data: "new answer should render"})
		close(h.secondCompleted)
		return nil
	}
}

func (h *interruptHarness) ReplaySessionEvents(context.Context, string, int64, func(protocol.Event)) error {
	return nil
}

func (h *interruptHarness) MCPStatus(context.Context) (string, error) {
	return "{}", nil
}

func (h *interruptHarness) ConfigStatus(context.Context) (string, error) {
	return "billyharness config", nil
}

func (h *interruptHarness) ContextStatus(context.Context, string) (string, error) {
	return "active context: 0", nil
}

func (h *interruptHarness) AuthStatus(context.Context) (credentials.Status, error) {
	return credentials.Status{}, nil
}

func (h *interruptHarness) SaveDeepSeekAPIKey(context.Context, string) (credentials.ProviderStatus, error) {
	return credentials.ProviderStatus{Configured: true, Source: ".env", Path: ".env"}, nil
}

func (h *interruptHarness) ImportCodexAuth(context.Context) (credentials.ProviderStatus, error) {
	return credentials.ProviderStatus{Configured: true, Source: "imported", Path: "auth/codex.json"}, nil
}

func (h *interruptHarness) CancelSession(context.Context, string) (bool, error) {
	h.gatewayCancelOnce.Do(func() { close(h.gatewayCancelCalled) })
	return true, nil
}

func (h scriptedHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h scriptedHarness) RunSession(ctx context.Context, _ string, _ gatewayapi.RunRequest, emit func(protocol.Event)) error {
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

func (h *missingSessionRetryHarness) CreateSession(_ context.Context, profile string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.created = append(h.created, profile)
	return "new-session", nil
}

func (h *missingSessionRetryHarness) RunSession(_ context.Context, sessionID string, req gatewayapi.RunRequest, emit func(protocol.Event)) error {
	h.mu.Lock()
	h.runCalls++
	call := h.runCalls
	h.runIDs = append(h.runIDs, sessionID)
	h.prompts = append(h.prompts, req.Prompt)
	h.mu.Unlock()
	if call == 1 {
		return fmt.Errorf("gateway run http 404: session not found")
	}
	emit(protocol.Event{Seq: 1, Type: protocol.EventRunStarted})
	emit(protocol.Event{Seq: 2, Type: protocol.EventAssistantDelta, Data: "retried answer"})
	emit(protocol.Event{Seq: 3, Type: protocol.EventRunCompleted})
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

type telegramSessionHarness struct {
	scriptedHarness

	mu              sync.Mutex
	sessions        []gatewayapi.SessionSummary
	full            map[string]gatewayapi.SessionResponse
	createdProfile  string
	createdMessages []protocol.Message
	createdOwner    gatewayapi.SessionOwner
	createdID       string
}

func (h *uniqueSessionHarness) CreateSession(context.Context, string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	return fmt.Sprintf("session-%d", h.next), nil
}

func (h *telegramSessionHarness) ListSessions(context.Context) ([]gatewayapi.SessionSummary, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gatewayapi.SessionSummary(nil), h.sessions...), nil
}

func (h *telegramSessionHarness) GetSession(_ context.Context, sessionID string) (gatewayapi.SessionResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	session, ok := h.full[sessionID]
	if !ok {
		return gatewayapi.SessionResponse{}, fmt.Errorf("session %s missing", sessionID)
	}
	return session, nil
}

func (h *telegramSessionHarness) CreateSessionFromMessages(_ context.Context, profile string, messages []protocol.Message) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.createdProfile = profile
	h.createdMessages = append([]protocol.Message(nil), messages...)
	if h.createdID == "" {
		h.createdID = "forked-session"
	}
	return h.createdID, nil
}

func (h *telegramSessionHarness) CreateSessionWithOwner(_ context.Context, profile string, owner gatewayapi.SessionOwner) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.createdProfile = profile
	h.createdOwner = owner
	if h.createdID == "" {
		h.createdID = "session-1"
	}
	return h.createdID, nil
}

func (h *telegramSessionHarness) CreateSessionFromMessagesWithOwner(_ context.Context, profile string, messages []protocol.Message, owner gatewayapi.SessionOwner) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.createdProfile = profile
	h.createdMessages = append([]protocol.Message(nil), messages...)
	h.createdOwner = owner
	if h.createdID == "" {
		h.createdID = "forked-session"
	}
	return h.createdID, nil
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

func TestTelegramDropsReplayAndLiveEventsAtOrBeforeCursor(t *testing.T) {
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
				{Seq: 8, Type: protocol.EventModelCallStarted},
				{Seq: 9, Type: protocol.EventModelCallStarted},
			},
		},
		replayFrom: []protocol.Event{
			{Seq: 7, Type: protocol.EventModelCallStarted},
			{Seq: 8, Type: protocol.EventModelCallStarted},
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

	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.LastEventSeq != 9 {
		t.Fatalf("LastEventSeq = %d, want 9", chat.LastEventSeq)
	}
	if chat.AgentTurns != 2 {
		t.Fatalf("AgentTurns = %d, want only fresh replay/live model calls", chat.AgentTurns)
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
		HTTPClient: &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
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
