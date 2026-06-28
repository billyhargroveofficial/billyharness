package telegrambot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	events []protocol.Event
	delay  time.Duration
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

func (h *blockingHarness) MCPStatus(context.Context) (string, error) {
	return "{}", nil
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

func (h scriptedHarness) MCPStatus(context.Context) (string, error) {
	return "{}", nil
}

func (h scriptedHarness) CancelSession(context.Context, string) (bool, error) {
	return false, nil
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
