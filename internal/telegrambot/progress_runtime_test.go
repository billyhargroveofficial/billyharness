package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProgressEditsFakeClockCoalescesBurstAndFlushesFinal(t *testing.T) {
	fakeClock := newFakeClock()
	tickers := &fakeTelegramTickerFactory{}
	oldNow := telegramNow
	oldTicker := newTelegramTicker
	telegramNow = fakeClock.Now
	newTelegramTicker = tickers.NewTicker
	t.Cleanup(func() {
		telegramNow = oldNow
		newTelegramTicker = oldTicker
	})

	var mu sync.Mutex
	var edits []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/botbottoken/editMessageText" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if text, ok := payload["text"].(string); ok {
			mu.Lock()
			edits = append(edits, text)
			mu.Unlock()
		}
		writeTelegramResult(w, true)
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
	bot := &Bot{
		opts:   Options{SendEnabled: true},
		client: client,
	}
	stop := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0
	done := bot.startProgressEdits(context.Background(), 123, 11, time.Hour, stop, func(bool, int) string {
		callsMu.Lock()
		defer callsMu.Unlock()
		calls++
		switch calls {
		case 1:
			return "draft"
		case 2, 3:
			return "burst"
		default:
			return "final"
		}
	})
	ticker := tickers.WaitTicker(t, 0)
	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(edits) == 1
	})
	fakeClock.Advance(ticker.duration)
	ticker.Tick(fakeClock.Now())
	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(edits) == 2
	})
	fakeClock.Advance(ticker.duration)
	ticker.Tick(fakeClock.Now())
	waitForTestCondition(t, func() bool {
		callsMu.Lock()
		defer callsMu.Unlock()
		return calls >= 3
	})
	close(stop)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"draft", "burst", "final"}; !reflect.DeepEqual(edits, want) {
		t.Fatalf("edits = %#v, want %#v", edits, want)
	}
}

func TestProgressEditsSkipHeartbeatOnlyTicks(t *testing.T) {
	fakeClock := newFakeClock()
	tickers := &fakeTelegramTickerFactory{}
	oldNow := telegramNow
	oldTicker := newTelegramTicker
	telegramNow = fakeClock.Now
	newTelegramTicker = tickers.NewTicker
	t.Cleanup(func() {
		telegramNow = oldNow
		newTelegramTicker = oldTicker
	})

	var mu sync.Mutex
	var edits []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/botbottoken/editMessageText" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if text, ok := payload["text"].(string); ok {
			mu.Lock()
			edits = append(edits, text)
			mu.Unlock()
		}
		writeTelegramResult(w, true)
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
	bot := &Bot{
		opts:   Options{SendEnabled: true},
		client: client,
	}
	stop := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0
	dirty := true
	done := bot.startProgressEdits(context.Background(), 123, 11, time.Hour, stop, func(force bool, _ int) string {
		callsMu.Lock()
		defer callsMu.Unlock()
		calls++
		if !dirty && !force {
			return ""
		}
		if !dirty && force {
			return "final"
		}
		dirty = false
		return "first"
	})
	ticker := tickers.WaitTicker(t, 0)
	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(edits) == 1
	})
	fakeClock.Advance(ticker.duration)
	ticker.Tick(fakeClock.Now())
	waitForTestCondition(t, func() bool {
		callsMu.Lock()
		defer callsMu.Unlock()
		return calls >= 2
	})
	mu.Lock()
	gotAfterTick := append([]string(nil), edits...)
	mu.Unlock()
	if !reflect.DeepEqual(gotAfterTick, []string{"first"}) {
		t.Fatalf("heartbeat tick edits = %#v, want only initial edit", gotAfterTick)
	}
	close(stop)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"first", "final"}; !reflect.DeepEqual(edits, want) {
		t.Fatalf("edits = %#v, want %#v", edits, want)
	}
}

func TestProgressEditsFakeClockKeepsUTF16Limit(t *testing.T) {
	fakeClock := newFakeClock()
	tickers := &fakeTelegramTickerFactory{}
	oldNow := telegramNow
	oldTicker := newTelegramTicker
	telegramNow = fakeClock.Now
	newTelegramTicker = tickers.NewTicker
	t.Cleanup(func() {
		telegramNow = oldNow
		newTelegramTicker = oldTicker
	})

	var mu sync.Mutex
	var edits []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/botbottoken/editMessageText" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if text, ok := payload["text"].(string); ok {
			mu.Lock()
			edits = append(edits, text)
			mu.Unlock()
		}
		writeTelegramResult(w, true)
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{BaseURL: server.URL, Token: "bottoken", MinInterval: time.Nanosecond})
	bot := &Bot{
		opts:   Options{SendEnabled: true},
		client: client,
	}
	renderer := NewRenderer()
	renderer.Content.WriteString(strings.Repeat("😀", telegramLiveProgressLimit))
	tools := NewToolProgress()
	stop := make(chan struct{})
	done := bot.startProgressEdits(context.Background(), 123, 11, time.Hour, stop, func(bool, int) string {
		return renderer.StreamPlainText("deepseek-v4-flash", "high", tools)
	})
	tickers.WaitTicker(t, 0)
	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(edits) == 1
	})
	close(stop)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if got := telegramUTF16Len(edits[0]); got > telegramLiveProgressLimit {
		t.Fatalf("progress edit UTF-16 length = %d, want <= %d", got, telegramLiveProgressLimit)
	}
	if !strings.Contains(edits[0], "live tail") {
		t.Fatalf("long progress edit should show live-tail marker:\n%s", edits[0])
	}
}
