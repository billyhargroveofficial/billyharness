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

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

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

func TestTelegramStreamGapReplaysBeforeFinalDelivery(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	if err := (Store{Path: statePath}).Save(State{Chats: map[string]ChatState{
		"123": {
			SessionID:       "session-1",
			Model:           "deepseek-v4-flash",
			Profile:         "billy",
			ReasoningEffort: "high",
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
			writeTelegramResult(w, SentMessage{MessageID: 21, Chat: Chat{ID: 123}})
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
			events: []protocol.Event{
				{Seq: 1, Type: protocol.EventRunStarted},
				{Seq: 2, Type: protocol.EventAssistantDelta, Data: "first "},
				{Type: protocol.EventGatewayStreamGap, Data: protocol.GatewayStreamGapEvent{DroppedEvents: 1, ReplayAfterSeq: 2}},
			},
		},
		replayFrom: []protocol.Event{
			{Seq: 3, Type: protocol.EventAssistantDelta, Data: "missed"},
			{Seq: 4, Type: protocol.EventRunCompleted},
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

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "recover stream"})

	harness.mu.Lock()
	replaySeq := harness.replaySeq
	replayed := harness.replayed
	harness.mu.Unlock()
	if replayed != 1 || replaySeq != 2 {
		t.Fatalf("replay called %d times from seq %d, want once from 2", replayed, replaySeq)
	}
	mu.Lock()
	joined := strings.Join(renderedTexts, "\n---\n")
	mu.Unlock()
	if !strings.Contains(joined, "first missed") {
		t.Fatalf("final telegram text did not include replayed delta:\n%s", joined)
	}
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Chats["123"].LastEventSeq; got != 4 {
		t.Fatalf("LastEventSeq = %d, want 4", got)
	}
}

func TestTelegramSecondMessageStartsFreshToolProgress(t *testing.T) {
	var mu sync.Mutex
	nextMessageID := 20
	var sentIDs []int
	editsByMessage := map[int][]string{}
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
			sentIDs = append(sentIDs, id)
			mu.Unlock()
			writeTelegramResult(w, SentMessage{MessageID: id, Chat: Chat{ID: 123}})
		case "/botbottoken/sendChatAction":
			writeTelegramResult(w, true)
		case "/botbottoken/editMessageText":
			id := intFromPayload(payload["message_id"])
			mu.Lock()
			if text, ok := payload["text"].(string); ok {
				editsByMessage[id] = append(editsByMessage[id], text)
			}
			if rich, ok := payload["rich_message"].(map[string]any); ok {
				if markdown, ok := rich["markdown"].(string); ok {
					editsByMessage[id] = append(editsByMessage[id], markdown)
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

	harness := &sequenceRunHarness{
		delay: 35 * time.Millisecond,
		runs: [][]protocol.Event{
			{
				{Type: protocol.EventRunStarted},
				{Type: protocol.EventModelCallStarted},
				{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
					ID:        "old-search",
					Name:      "web_search",
					Arguments: json.RawMessage(`{"query":"old query"}`),
				}},
				{Type: protocol.EventAssistantDelta, Data: "first answer"},
			},
			{
				{Type: protocol.EventRunStarted},
				{Type: protocol.EventModelCallStarted},
				{Type: protocol.EventAssistantDelta, Data: "second answer no tools"},
			},
		},
	}
	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
	statePath := t.TempDir() + "/state.json"
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

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "first"})
	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123}, Text: "second"})

	mu.Lock()
	ids := append([]int(nil), sentIDs...)
	if len(ids) < 2 {
		mu.Unlock()
		t.Fatalf("sent placeholder ids = %#v", ids)
	}
	firstJoined := strings.Join(editsByMessage[ids[0]], "\n---\n")
	secondJoined := strings.Join(editsByMessage[ids[1]], "\n---\n")
	mu.Unlock()
	if !strings.Contains(firstJoined, "old query") || !strings.Contains(firstJoined, "first answer") {
		t.Fatalf("first run did not render expected tool and answer:\n%s", firstJoined)
	}
	for _, notWant := range []string{"old query", "first answer", "web_search", "Tools running", "Tools done"} {
		if strings.Contains(secondJoined, notWant) {
			t.Fatalf("second run leaked previous run content %q:\n%s", notWant, secondJoined)
		}
	}
	for _, want := range []string{"second answer no tools", "tools 1"} {
		if !strings.Contains(secondJoined, want) {
			t.Fatalf("second run missing %q:\n%s", want, secondJoined)
		}
	}

	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	chat := state.Chats["123"]
	if chat.AgentTurns != 2 || chat.ToolCalls != 1 {
		t.Fatalf("chat totals = turns:%d tools:%d, want cumulative turns=2 tools=1", chat.AgentTurns, chat.ToolCalls)
	}
}

type sequenceRunHarness struct {
	scriptedHarness

	mu     sync.Mutex
	calls  int
	runs   [][]protocol.Event
	delay  time.Duration
	runErr error
}

func (h *sequenceRunHarness) RunSession(ctx context.Context, _ string, _ gatewayapi.RunRequest, emit func(protocol.Event)) error {
	h.mu.Lock()
	call := h.calls
	h.calls++
	if call >= len(h.runs) {
		call = len(h.runs) - 1
	}
	events := append([]protocol.Event(nil), h.runs[call]...)
	delay := h.delay
	runErr := h.runErr
	h.mu.Unlock()

	for _, event := range events {
		emit(event)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	emit(protocol.Event{Type: protocol.EventRunCompleted})
	return runErr
}

func intFromPayload(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}
