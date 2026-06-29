package telegrambot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestClientSendMessageUsesThreadAndParseMode(t *testing.T) {
	var captured map[string]any
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			captured = payload
			writeTelegramResult(w, SentMessage{MessageID: 42, Chat: Chat{ID: -100}})
		},
	})
	msg, err := client.SendMessage(context.Background(), -100, "<b>hi</b>", "HTML", 7)
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageID != 42 {
		t.Fatalf("message id = %d", msg.MessageID)
	}
	if captured["parse_mode"] != "HTML" || captured["message_thread_id"] != float64(7) {
		t.Fatalf("captured payload = %#v", captured)
	}
}

func TestClientFallsBackWhenParseModeFails(t *testing.T) {
	var calls int32
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"editMessageText": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				if payload["parse_mode"] != "HTML" {
					t.Fatalf("first payload missing parse mode: %#v", payload)
				}
				writeTelegramError(w, http.StatusBadRequest, "Bad Request: can't parse entities")
				return
			}
			if _, ok := payload["parse_mode"]; ok {
				t.Fatalf("fallback payload kept parse mode: %#v", payload)
			}
			if payload["text"] != "broken" {
				t.Fatalf("fallback text = %#v, want plain text", payload["text"])
			}
			writeTelegramResult(w, true)
		},
	})

	if err := client.EditMessageText(context.Background(), 10, 20, "<b>broken", "HTML"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestClientDoesNotFallbackWhenEditErrorIsNotParseError(t *testing.T) {
	var calls int32
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"editMessageText": func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			atomic.AddInt32(&calls, 1)
			writeTelegramError(w, http.StatusBadRequest, "Bad Request: message is not modified")
		},
	})

	err := client.EditMessageText(context.Background(), 10, 20, "<b>same</b>", "HTML")
	if err == nil || !strings.Contains(err.Error(), "message is not modified") {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want no fallback retry", calls)
	}
}

func TestClientTransportErrorRedactsBotToken(t *testing.T) {
	token := "123456789:secret-token"
	client := NewClient(ClientOptions{
		BaseURL:     "https://api.telegram.test",
		Token:       token,
		MinInterval: time.Nanosecond,
		HTTPClient: &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed for %s", req.URL.String())
		})},
	})
	_, err := client.SendMessage(context.Background(), 1, "hello", "", 0)
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("token was not redacted: %v", err)
	}
}

func TestClientSerializesSameChatRateLimitReservations(t *testing.T) {
	var mu sync.Mutex
	var seen []time.Time
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			mu.Lock()
			seen = append(seen, time.Now())
			mu.Unlock()
			writeTelegramResult(w, SentMessage{MessageID: 1, Chat: Chat{ID: 10}})
		},
	})

	client.minInterval = 20 * time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.SendMessage(context.Background(), 10, "hello", "", 0)
			if err != nil {
				t.Errorf("send failed: %v", err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("seen = %d, want 3", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if delta := seen[i].Sub(seen[i-1]); delta < 15*time.Millisecond {
			t.Fatalf("request %d arrived after %s, want serialized pacing; seen=%v", i, delta, seen)
		}
	}
}

func TestClientRetryAfterUsesFakeTimer(t *testing.T) {
	fakeClock := newFakeClock()
	timers := &fakeTelegramTimerFactory{}
	oldNow := telegramNow
	oldTimer := newTelegramTimer
	telegramNow = fakeClock.Now
	newTelegramTimer = timers.NewTimer
	t.Cleanup(func() {
		telegramNow = oldNow
		newTelegramTimer = oldTimer
	})

	var mu sync.Mutex
	requests := 0
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			mu.Lock()
			requests++
			count := requests
			mu.Unlock()
			if count == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":          false,
					"error_code":  http.StatusTooManyRequests,
					"description": "Too Many Requests",
					"parameters":  map[string]any{"retry_after": 2},
				})
				return
			}
			writeTelegramResult(w, SentMessage{MessageID: 1, Chat: Chat{ID: 10}})
		},
	})

	done := make(chan error, 1)
	go func() {
		_, err := client.SendMessage(context.Background(), 10, "hello", "", 0)
		done <- err
	}()

	retryTimer := timers.WaitTimer(t, 0)
	if retryTimer.duration != 2250*time.Millisecond {
		t.Fatalf("retry timer = %s, want 2.25s", retryTimer.duration)
	}
	fakeClock.Advance(retryTimer.duration)
	retryTimer.Fire(fakeClock.Now())
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("send failed after retry: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retry")
	}

	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestClientSendRichMessageMarkdown(t *testing.T) {
	var captured map[string]any
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"sendRichMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			captured = payload
			writeTelegramResult(w, SentMessage{MessageID: 99, Chat: Chat{ID: -100}})
		},
	})
	msg, err := client.SendRichMessageMarkdown(context.Background(), -100, "## Title\n\n| A | B |\n|---|---|", 5)
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageID != 99 {
		t.Fatalf("message id = %d", msg.MessageID)
	}
	rich, ok := captured["rich_message"].(map[string]any)
	if !ok || rich["markdown"] == "" {
		t.Fatalf("missing rich markdown payload: %#v", captured)
	}
	if captured["message_thread_id"] != float64(5) {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestClientEditMessageRichMarkdown(t *testing.T) {
	var captured map[string]any
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"editMessageText": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			captured = payload
			writeTelegramResult(w, SentMessage{MessageID: 77, Chat: Chat{ID: -100}})
		},
	})
	if err := client.EditMessageRichMarkdown(context.Background(), -100, 77, "**done**"); err != nil {
		t.Fatal(err)
	}
	rich, ok := captured["rich_message"].(map[string]any)
	if !ok || rich["markdown"] != "**done**" {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestClientDeleteMessage(t *testing.T) {
	var captured map[string]any
	client := newTelegramAPIClient(t, "token", map[string]telegramAPIHandler{
		"deleteMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			captured = payload
			writeTelegramResult(w, true)
		},
	})
	if err := client.DeleteMessage(context.Background(), -100, 77); err != nil {
		t.Fatal(err)
	}
	if captured["chat_id"] != float64(-100) || captured["message_id"] != float64(77) {
		t.Fatalf("captured = %#v", captured)
	}
}
