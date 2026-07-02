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

func TestLiveRunViewHeartbeatEditsDuringToolOnlyWait(t *testing.T) {
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
	view := &telegramLiveRunView{
		renderer:    NewRenderer(),
		tools:       NewToolProgress(),
		model:       "deepseek-v4-flash",
		reasoning:   "high",
		answerDirty: true,
	}
	if !view.tools.Add(RenderEvent{Kind: "tool", Body: "🌐 web_fetch example.com/forecast", Key: "fetch"}) {
		t.Fatal("expected tool progress to be added")
	}

	stop := make(chan struct{})
	done := bot.startProgressEdits(context.Background(), 123, 11, time.Hour, stop, view.progressText)
	ticker := tickers.WaitTicker(t, 0)
	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(edits) == 1
	})
	fakeClock.Advance(5 * time.Second)
	ticker.Tick(fakeClock.Now())
	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(edits) >= 2
	})
	close(stop)
	<-done

	mu.Lock()
	first := edits[0]
	second := edits[1]
	mu.Unlock()
	for _, want := range []string{"Tools running · 0s", "🌐 web_fetch example.com/forecast"} {
		if !strings.Contains(first, want) {
			t.Fatalf("initial progress missing %q:\n%s", want, first)
		}
	}
	for _, want := range []string{"⏱ 5s", "Tools running · 5s", "🌐 web_fetch example.com/forecast"} {
		if !strings.Contains(second, want) {
			t.Fatalf("heartbeat progress missing %q:\n%s", want, second)
		}
	}
	if first == second {
		t.Fatalf("heartbeat progress did not change:\n%s", second)
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
