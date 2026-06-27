package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
