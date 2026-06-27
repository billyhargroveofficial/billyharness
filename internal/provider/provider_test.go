package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestDeepSeekBodyThinkingHigh(t *testing.T) {
	temp := 0.7
	d := &DeepSeek{Thinking: "enabled", ReasoningEffort: "high"}
	body, err := d.body(Request{
		Model:       "deepseek-v4-flash",
		Temperature: &temp,
		Messages: []protocol.Message{
			{Role: protocol.RoleUser, Content: "hello"},
		},
		Tools: []protocol.ToolSpec{
			{
				Name:        "time_now",
				Description: "Return time.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Risk:        protocol.RiskReadOnly,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "deepseek-v4-flash" {
		t.Fatalf("model = %v", payload["model"])
	}
	if _, ok := payload["temperature"]; ok {
		t.Fatalf("temperature must be omitted when thinking is enabled: %s", body)
	}
	if payload["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", payload["reasoning_effort"])
	}
	thinking, ok := payload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v", payload["thinking"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", payload["tools"])
	}
}

func TestIsCodexProviderReroutesOSeriesModels(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "o1-preview", "o3-mini", "o4-mini"} {
		cfg := config.Config{Provider: "deepseek", Model: model}
		if !isCodexProvider(cfg) {
			t.Fatalf("model %q should route to Codex provider", model)
		}
	}
}

func TestParseChunkEmitsReasoningContentToolAndUsage(t *testing.T) {
	events, err := parseChunk([]byte(`{
		"choices":[{
			"delta":{
				"reasoning_content":"think",
				"content":"answer",
				"tool_calls":[{
					"index":0,
					"id":"call_1",
					"function":{"name":"time_now","arguments":"{}"}
				}]
			}
		}],
		"usage":{
			"prompt_tokens":30,
			"completion_tokens":5,
			"prompt_cache_hit_tokens":20,
			"prompt_cache_miss_tokens":10,
			"completion_tokens_details":{"reasoning_tokens":2}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events len = %d: %#v", len(events), events)
	}
	if events[0].Kind != EventUsage ||
		events[0].Usage.InputTokens != 30 ||
		events[0].Usage.OutputTokens != 5 ||
		events[0].Usage.CacheHitTokens != 20 ||
		events[0].Usage.CacheMissTokens != 10 ||
		events[0].Usage.ReasoningTokens != 2 {
		t.Fatalf("usage event = %#v", events[0])
	}
	if events[1].Kind != EventContent || events[1].Text != "answer" {
		t.Fatalf("content event = %#v", events[1])
	}
	if events[2].Kind != EventReasoning || events[2].Text != "think" {
		t.Fatalf("reasoning event = %#v", events[2])
	}
	if events[3].Kind != EventToolCallDelta || events[3].ToolID != "call_1" || events[3].ToolName != "time_now" || events[3].ArgsDelta != "{}" {
		t.Fatalf("tool event = %#v", events[3])
	}
}

func TestParseChunkNormalizesOpenAICachedTokens(t *testing.T) {
	events, err := parseChunk([]byte(`{
		"choices":[],
		"usage":{
			"prompt_tokens":100,
			"completion_tokens":7,
			"prompt_tokens_details":{"cached_tokens":64}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != EventUsage {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Usage.CacheHitTokens != 64 || events[0].Usage.CacheMissTokens != 36 {
		t.Fatalf("usage = %#v", events[0].Usage)
	}
}

func TestParseChunkCountsPromptTokensAsMissWhenCacheFieldsMissing(t *testing.T) {
	events, err := parseChunk([]byte(`{
		"choices":[],
		"usage":{
			"prompt_tokens":100,
			"completion_tokens":7
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != EventUsage {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Usage.CacheHitTokens != 0 || events[0].Usage.CacheMissTokens != 100 {
		t.Fatalf("usage = %#v", events[0].Usage)
	}
}

func TestParseSSEReturnsWhenEventConsumerBlockedAndContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event)
	done := make(chan error, 1)
	go func() {
		done <- parseSSE(ctx, strings.NewReader(`data: {"choices":[{"delta":{"content":"blocked"}}]}`+"\n\n"), time.Minute, events)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parseSSE did not return after context cancellation")
	}
}

func TestParseSSEReturnsErrorWhenStreamClosesBeforeDone(t *testing.T) {
	events := make(chan Event, 4)
	err := parseSSE(context.Background(), strings.NewReader(`data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n"), time.Minute, events)
	if err == nil || !strings.Contains(err.Error(), "before [DONE]") {
		t.Fatalf("err = %v, want truncated stream error", err)
	}
}

func TestParseSSEReturnsImmediatelyAfterDone(t *testing.T) {
	events := make(chan Event, 4)
	err := parseSSE(context.Background(), strings.NewReader("data: [DONE]\n\n"), time.Minute, events)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Kind != EventDone {
			t.Fatalf("event = %#v", event)
		}
	default:
		t.Fatal("missing done event")
	}
}

func TestDeepSeekStreamRetriesRateLimitBeforeStreaming(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	d := &DeepSeek{
		BaseURL:           server.URL,
		APIKey:            "secret",
		RequestTimeout:    time.Second,
		StreamIdleTimeout: time.Second,
		MaxRetries:        1,
		Client:            server.Client(),
	}
	events, errs := d.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	var content string
	for event := range events {
		if event.Kind == EventContent {
			content += event.Text
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if calls != 2 || content != "ok" {
		t.Fatalf("calls=%d content=%q", calls, content)
	}
}

func TestDeepSeekStreamDoesNotApplyRequestTimeoutAfterHeaders(t *testing.T) {
	requestTimeout := 50 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"first"}}]}` + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(2 * requestTimeout)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"second"}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	d := &DeepSeek{
		BaseURL:           server.URL,
		APIKey:            "secret",
		RequestTimeout:    requestTimeout,
		StreamIdleTimeout: time.Second,
		Client:            server.Client(),
	}
	events, errs := d.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	var content string
	for event := range events {
		if event.Kind == EventContent {
			content += event.Text
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if content != "firstsecond" {
		t.Fatalf("content = %q", content)
	}
}

func TestDeepSeekStreamBuffersEventsForSlowConsumer(t *testing.T) {
	const eventCount = 128
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < eventCount; i++ {
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	d := &DeepSeek{
		BaseURL:           server.URL,
		APIKey:            "secret",
		RequestTimeout:    time.Second,
		StreamIdleTimeout: time.Second,
		Client:            server.Client(),
	}
	events, errs := d.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider stream blocked behind slow event consumer")
	}

	var contentEvents int
	var sawDone bool
	for event := range events {
		switch event.Kind {
		case EventContent:
			contentEvents++
		case EventDone:
			sawDone = true
		}
	}
	if contentEvents != eventCount || !sawDone {
		t.Fatalf("contentEvents=%d sawDone=%v", contentEvents, sawDone)
	}
}

func TestRetryAfterParsingAndDelay(t *testing.T) {
	if got := parseRetryAfter("0.25", time.Now()); got != 250*time.Millisecond {
		t.Fatalf("fractional Retry-After = %s", got)
	}
	retryAfter := 30 * time.Second
	err := &ProviderError{Kind: ErrorRateLimit, RetryAfter: retryAfter}
	if got := providerRetryDelay(err, 0); got != retryAfter {
		t.Fatalf("retry delay = %s, want %s", got, retryAfter)
	}
}

func TestDeepSeekHTTPErrorIsTypedAndRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad secret-token", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	d := &DeepSeek{
		BaseURL:        server.URL,
		APIKey:         "secret-token",
		RequestTimeout: time.Second,
		MaxRetries:     1,
		Client:         server.Client(),
	}
	events, errs := d.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	for range events {
	}
	err := <-errs
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("err = %T %v, want ProviderError", err, err)
	}
	if providerErr.Kind != ErrorBadRequest || providerErr.Status != http.StatusBadRequest {
		t.Fatalf("provider error = %#v", providerErr)
	}
	if strings.Contains(err.Error(), "secret-token") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error redaction failed: %v", err)
	}
}

func TestToolAccumulatorAssemblesArgumentDeltas(t *testing.T) {
	var acc ToolAccumulator
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "fs_list", ArgsDelta: `{"path"`})
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ArgsDelta: `:"."}`})

	calls, err := acc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls len = %d", len(calls))
	}
	if calls[0].ID != "call_a" || calls[0].Name != "fs_list" || string(calls[0].Arguments) != `{"path":"."}` {
		t.Fatalf("call = %#v", calls[0])
	}
}

func TestToolAccumulatorParallelCallsAndGaps(t *testing.T) {
	var acc ToolAccumulator
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 1, ToolID: "call_b", ToolName: "fs_read_file", ArgsDelta: `{"path":"README.md"}`})
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "time_now", ArgsDelta: `{}`})
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: -1, ToolID: "bad", ToolName: "bad", ArgsDelta: `{}`})

	calls, err := acc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
	if calls[0].Name != "time_now" || calls[1].Name != "fs_read_file" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestToolAccumulatorErrorsOnMissingNameWithArguments(t *testing.T) {
	var acc ToolAccumulator
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ArgsDelta: `{"path":"."}`})
	if _, err := acc.Finish(); err == nil || !strings.Contains(err.Error(), "missing name") {
		t.Fatalf("err = %v", err)
	}
}
