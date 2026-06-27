package telegrambot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type blockingHarness struct {
	startOnce  sync.Once
	cancelOnce sync.Once
	runStarted chan struct{}
	cancelled  chan struct{}
}

func newBlockingHarness() *blockingHarness {
	return &blockingHarness{
		runStarted: make(chan struct{}),
		cancelled:  make(chan struct{}),
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
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("run handler did not finish after cancel")
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
	if bot.allowed(123) {
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
	if !allowAll.allowed(123) {
		t.Fatal("allow-all-chats should explicitly permit unknown chats")
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
