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
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSendMessageUsesThreadAndParseMode(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 42,
				"chat":       map[string]any{"id": -100},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: time.Nanosecond})
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			if payload["parse_mode"] != "HTML" {
				t.Fatalf("first payload missing parse mode: %#v", payload)
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  400,
				"description": "Bad Request: can't parse entities",
			})
			return
		}
		if _, ok := payload["parse_mode"]; ok {
			t.Fatalf("fallback payload kept parse mode: %#v", payload)
		}
		if payload["text"] != "broken" {
			t.Fatalf("fallback text = %#v, want plain text", payload["text"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: time.Nanosecond})
	if err := client.EditMessageText(context.Background(), 10, 20, "<b>broken", "HTML"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestClientDoesNotFallbackWhenEditErrorIsNotParseError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"error_code":  400,
			"description": "Bad Request: message is not modified",
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: time.Nanosecond})
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
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, time.Now())
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 1,
				"chat":       map[string]any{"id": 10},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: 20 * time.Millisecond})
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

func TestClientSendRichMessageMarkdown(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendRichMessage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 99,
				"chat":       map[string]any{"id": -100},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: time.Nanosecond})
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestClientEditMessageRichMarkdown(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/editMessageText" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 77,
				"chat":       map[string]any{"id": -100},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: time.Nanosecond})
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/deleteMessage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "token", MinInterval: time.Nanosecond})
	if err := client.DeleteMessage(context.Background(), -100, 77); err != nil {
		t.Fatal(err)
	}
	if captured["chat_id"] != float64(-100) || captured["message_id"] != float64(77) {
		t.Fatalf("captured = %#v", captured)
	}
}
