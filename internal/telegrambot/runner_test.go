package telegrambot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

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
