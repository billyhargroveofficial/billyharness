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

func TestNewTelegramMessageInterruptsActiveRunAndRunsLatestPrompt(t *testing.T) {
	harness := newInterruptHarness()
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

	firstDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{
			Chat: Chat{ID: 123},
			Text: "old long task",
		})
		close(firstDone)
	}()

	select {
	case <-harness.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first run did not start")
	}

	secondDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{
			Chat: Chat{ID: 123},
			Text: "new instruction",
		})
		close(secondDone)
	}()

	select {
	case <-harness.firstCancelled:
	case <-time.After(time.Second):
		t.Fatal("new message did not cancel active local run")
	}
	select {
	case <-harness.gatewayCancelCalled:
	case <-time.After(time.Second):
		t.Fatal("new message did not request gateway cancel")
	}
	select {
	case <-harness.secondCompleted:
	case <-time.After(time.Second):
		t.Fatal("new message did not run after interrupt")
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first handler did not finish")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second handler did not finish")
	}

	harness.mu.Lock()
	prompts := append([]string(nil), harness.prompts...)
	harness.mu.Unlock()
	if len(prompts) != 2 || prompts[0] != "old long task" || prompts[1] != "new instruction" {
		t.Fatalf("prompts = %#v", prompts)
	}
}

func TestSupersededTelegramRunDoesNotRenderLateOldAnswer(t *testing.T) {
	harness := newLateSuccessInterruptHarness()
	var mu sync.Mutex
	var edits []string
	nextMessageID := 10
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
			nextMessageID++
			id := nextMessageID
			mu.Unlock()
			writeTelegramResult(w, SentMessage{MessageID: id, Chat: Chat{ID: 123}})
		case "/botbottoken/sendChatAction":
			writeTelegramResult(w, true)
		case "/botbottoken/editMessageText":
			mu.Lock()
			if text, ok := payload["text"].(string); ok {
				edits = append(edits, text)
			}
			if rich, ok := payload["rich_message"].(map[string]any); ok {
				if markdown, ok := rich["markdown"].(string); ok {
					edits = append(edits, markdown)
				}
			}
			mu.Unlock()
			writeTelegramResult(w, true)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
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

	firstDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "old prompt"})
		close(firstDone)
	}()
	select {
	case <-harness.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first run did not start")
	}

	secondDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "new prompt"})
		close(secondDone)
	}()

	select {
	case <-harness.firstCancelled:
	case <-time.After(time.Second):
		t.Fatal("new message did not cancel old run")
	}
	select {
	case <-harness.secondCompleted:
	case <-time.After(time.Second):
		t.Fatal("new run did not complete")
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("old handler did not finish")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("new handler did not finish")
	}

	mu.Lock()
	joined := strings.Join(edits, "\n---\n")
	mu.Unlock()
	if strings.Contains(joined, "old answer should not render") {
		t.Fatalf("superseded old answer leaked into telegram edits:\n%s", joined)
	}
	if !strings.Contains(joined, "Interrupted by newer message.") {
		t.Fatalf("old placeholder was not marked interrupted:\n%s", joined)
	}
	if !strings.Contains(joined, "new answer should render") {
		t.Fatalf("new answer did not render:\n%s", joined)
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

func TestTelegramChatScopeKeysPreserveLegacyFormat(t *testing.T) {
	scope := ChatScope{ChatID: 123, ThreadID: 45, UserID: 1001}
	if scope.LegacyKey() != "123:45" {
		t.Fatalf("legacy key = %q", scope.LegacyKey())
	}
	if scope.Key() != "123:45:u1001" {
		t.Fatalf("scoped key = %q", scope.Key())
	}
	msg := Message{Chat: Chat{ID: 123}, ThreadID: 45, From: &User{ID: 1001}}
	if messageChatScope(msg) != scope {
		t.Fatalf("message scope = %#v, want %#v", messageChatScope(msg), scope)
	}
	if chatKey(123, 45) != scope.LegacyKey() || userChatKey(123, 45, 1001) != scope.Key() {
		t.Fatalf("compat key helpers diverged: chat=%q user=%q", chatKey(123, 45), userChatKey(123, 45, 1001))
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
		case "/botbottoken/sendChatAction":
			writeTelegramResult(w, true)
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

func TestTelegramReplayCatchupDoesNotLeakOldRunIntoNewProgress(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	if err := (Store{Path: statePath}).Save(State{Chats: map[string]ChatState{
		"123": {
			SessionID:       "session-1",
			Model:           "deepseek-v4-flash",
			Profile:         "billy",
			ReasoningEffort: "high",
			LastEventSeq:    3,
			UpdatedAt:       time.Now().UTC(),
		},
	}}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var renderedTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/botbottoken/sendMessage":
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		case "/botbottoken/sendChatAction":
			writeTelegramResult(w, true)
		case "/botbottoken/editMessageText":
			mu.Lock()
			if text, ok := payload["text"].(string); ok {
				renderedTexts = append(renderedTexts, text)
			}
			if rich, ok := payload["rich_message"].(map[string]any); ok {
				if markdown, ok := rich["markdown"].(string); ok {
					renderedTexts = append(renderedTexts, markdown)
				}
			}
			mu.Unlock()
			writeTelegramResult(w, true)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	harness := &replayScriptedHarness{
		scriptedHarness: scriptedHarness{
			delay: 25 * time.Millisecond,
			events: []protocol.Event{
				{Seq: 9, Type: protocol.EventRunStarted},
				{Seq: 10, Type: protocol.EventModelCallStarted},
				{Seq: 11, Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
					ID:        "new-search",
					Name:      "web_search",
					Arguments: json.RawMessage(`{"query":"new query"}`),
				}},
				{Seq: 12, Type: protocol.EventAssistantDelta, Data: "new answer"},
			},
		},
		replayFrom: []protocol.Event{
			{Seq: 4, Type: protocol.EventRunStarted},
			{Seq: 5, Type: protocol.EventModelCallStarted},
			{Seq: 6, Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
				ID:        "old-search",
				Name:      "web_search",
				Arguments: json.RawMessage(`{"query":"old query"}`),
			}},
			{Seq: 7, Type: protocol.EventAssistantDelta, Data: "old answer"},
			{Seq: 8, Type: protocol.EventRunCompleted},
		},
	}
	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
	bot, err := New(Options{
		BotToken:        "bottoken",
		StatePath:       statePath,
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

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "continue"})

	mu.Lock()
	joined := strings.Join(renderedTexts, "\n---\n")
	mu.Unlock()
	if joined == "" {
		t.Fatal("expected telegram progress/final edits")
	}
	for _, notWant := range []string{"old query", "old answer"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("new progress leaked replayed old run %q:\n%s", notWant, joined)
		}
	}
	for _, want := range []string{"new query", "new answer"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("new progress missing %q:\n%s", want, joined)
		}
	}

	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.LastEventSeq != 12 {
		t.Fatalf("LastEventSeq = %d, want 12", chat.LastEventSeq)
	}
	if chat.AgentTurns != 2 || chat.ToolCalls != 2 {
		t.Fatalf("chat totals should include silent catch-up plus live run, got turns=%d tools=%d", chat.AgentTurns, chat.ToolCalls)
	}
}

func TestTelegramRunRecreatesMissingGatewaySessionAndRetries(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	if err := (Store{Path: statePath}).Save(State{Chats: map[string]ChatState{
		"123": {
			SessionID:       "old-session",
			Model:           "deepseek-v4-flash",
			Profile:         "billy",
			ReasoningEffort: "high",
			UpdatedAt:       time.Now().UTC(),
		},
	}}); err != nil {
		t.Fatal(err)
	}
	harness := &missingSessionRetryHarness{}
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       statePath,
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		AllowedChatIDs:  map[int64]bool{123: true},
		DryRunDefault:   true,
	}, nil, harness)
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "retry me"})

	harness.mu.Lock()
	runIDs := append([]string(nil), harness.runIDs...)
	prompts := append([]string(nil), harness.prompts...)
	created := append([]string(nil), harness.created...)
	harness.mu.Unlock()
	if len(runIDs) != 2 || runIDs[0] != "old-session" || runIDs[1] != "new-session" {
		t.Fatalf("run session IDs = %#v", runIDs)
	}
	if len(prompts) != 2 || prompts[0] != "retry me" || prompts[1] != "retry me" {
		t.Fatalf("prompts = %#v", prompts)
	}
	if len(created) != 1 || created[0] != "billy" {
		t.Fatalf("created profiles = %#v", created)
	}
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.SessionID != "new-session" || chat.LastEventSeq != 3 {
		t.Fatalf("chat state after retry = %#v", chat)
	}
}

func TestTelegramRunShowsTypingAndAnimatedWorkingPulse(t *testing.T) {
	var mu sync.Mutex
	typingActions := 0
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
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		case "/botbottoken/sendChatAction":
			if action, _ := payload["action"].(string); action != "typing" {
				t.Errorf("chat action = %q, want typing", action)
			}
			mu.Lock()
			typingActions++
			mu.Unlock()
			writeTelegramResult(w, true)
		case "/botbottoken/editMessageText":
			mu.Lock()
			if text, ok := payload["text"].(string); ok {
				editTexts = append(editTexts, text)
			}
			if rich, ok := payload["rich_message"].(map[string]any); ok {
				if markdown, ok := rich["markdown"].(string); ok {
					editTexts = append(editTexts, markdown)
				}
			}
			mu.Unlock()
			writeTelegramResult(w, true)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
	bot, err := New(Options{
		BotToken:        "bottoken",
		StatePath:       t.TempDir() + "/state.json",
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    10 * time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     true,
		DryRunDefault:   false,
	}, client, scriptedHarness{delay: 65 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "slow"})

	mu.Lock()
	typing := typingActions
	joined := strings.Join(editTexts, "\n---\n")
	mu.Unlock()
	if typing == 0 {
		t.Fatal("expected telegram typing action during run")
	}
	for _, want := range []string{"Working.", "Working..", "Working..."} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress never showed %q pulse:\n%s", want, joined)
		}
	}
}

func TestTelegramRunThrottlesBurstProgressEdits(t *testing.T) {
	var mu sync.Mutex
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
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		case "/botbottoken/sendChatAction":
			writeTelegramResult(w, true)
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

	events := []protocol.Event{{Type: protocol.EventRunStarted}}
	for i := 0; i < 80; i++ {
		events = append(events, protocol.Event{Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("token-%02d ", i)})
	}
	client := NewClient(ClientOptions{
		BaseURL:     server.URL,
		Token:       "bottoken",
		MinInterval: time.Nanosecond,
	})
	bot, err := New(Options{
		BotToken:        "bottoken",
		StatePath:       t.TempDir() + "/state.json",
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    20 * time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     true,
		DryRunDefault:   false,
	}, client, scriptedHarness{
		delay:  90 * time.Millisecond,
		events: events,
	})
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "burst"})

	mu.Lock()
	defer mu.Unlock()
	if len(editTexts) == 0 {
		t.Fatal("expected progress edits")
	}
	if len(editTexts) >= 12 {
		t.Fatalf("burst should be coalesced into a small number of edits, got %d edits", len(editTexts))
	}
	var sawFreshTail bool
	for _, text := range editTexts {
		if strings.Contains(text, "token-79") {
			sawFreshTail = true
			break
		}
	}
	if !sawFreshTail {
		t.Fatalf("progress edits never showed freshest delta tail: %#v", editTexts)
	}
}

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
