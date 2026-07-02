package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestDeepSeekStreamRejectsImageInputBeforeHTTP(t *testing.T) {
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called.Add(1)
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
		Model: "deepseek-v4-flash",
		Messages: []protocol.Message{{
			Role:    protocol.RoleUser,
			Content: "look",
			Parts: []protocol.MessagePart{
				protocol.TextPart("look"),
				protocol.AttachmentPart(protocol.AttachmentRef{ID: "att_test", Kind: protocol.AttachmentKindImage}),
			},
		}},
	})
	for range events {
	}
	err := <-errs
	if err == nil || !strings.Contains(err.Error(), "image input is required") {
		t.Fatalf("err = %v", err)
	}
	if called.Load() != 0 {
		t.Fatalf("DeepSeek HTTP server was called %d time(s)", called.Load())
	}
}

func TestIsCodexProviderReroutesOSeriesModels(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "o1-preview", "o3-mini", "o4-mini"} {
		cfg := config.Config{Provider: "deepseek", Model: model}
		if !isCodexBinding(cfg.ProviderBinding()) {
			t.Fatalf("model %q should route to Codex provider", model)
		}
	}
}

func TestNewFromBindingRejectsUnsupportedCapabilityPolicyBeforeCredentials(t *testing.T) {
	_, err := NewFromBinding(config.ProviderBinding{
		Provider: config.ProviderSelection{Provider: "deepseek", BaseURL: "https://api.deepseek.example"},
		Model: config.ModelSelection{
			Model:           "deepseek-v4-flash",
			Thinking:        "enabled",
			ReasoningEffort: "warp",
			MaxTokens:       512,
		},
		Auth: config.AuthSettings{APIKeyEnv: "MISSING_DEEPSEEK_KEY"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported reasoning_effort") {
		t.Fatalf("err = %v", err)
	}

	_, err = NewFromBinding(config.ProviderBinding{
		Provider: config.ProviderSelection{Provider: "deepseek", BaseURL: "https://api.deepseek.example"},
		Model: config.ModelSelection{
			Model:     "deepseek-v4-flash",
			MaxTokens: 9000,
		},
		Auth: config.AuthSettings{APIKeyEnv: "MISSING_DEEPSEEK_KEY"},
	})
	if err == nil || !strings.Contains(err.Error(), "max_output_tokens=9000") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewDeepSeekProviderUsesCredentialsManager(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "1")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CUSTOM_DEEPSEEK_KEY=sk-provider-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Provider:          "deepseek",
		Model:             "deepseek-v4-flash",
		BaseURL:           "https://api.deepseek.com",
		APIKeyEnv:         "CUSTOM_DEEPSEEK_KEY",
		RequestTimeout:    time.Second,
		StreamIdleTimeout: time.Second,
	}
	prov, err := NewFromBinding(cfg.ProviderBinding())
	if err != nil {
		t.Fatal(err)
	}
	deepseek, ok := prov.(*DeepSeek)
	if !ok {
		t.Fatalf("provider = %T, want *DeepSeek", prov)
	}
	if deepseek.APIKey != "sk-provider-value" {
		t.Fatalf("APIKey = %q", deepseek.APIKey)
	}
}

func TestNewFromBindingDeepSeekProviderUsesProjection(t *testing.T) {
	t.Setenv("BINDING_DEEPSEEK_KEY", "sk-binding-value")

	prov, err := NewFromBinding(config.ProviderBinding{
		Provider: config.ProviderSelection{
			Provider: "deepseek",
			BaseURL:  "https://api.deepseek.example",
		},
		Model: config.ModelSelection{
			Model:           "deepseek-v4-flash",
			Thinking:        "disabled",
			ReasoningEffort: "low",
			MaxTokens:       512,
		},
		Auth: config.AuthSettings{
			APIKeyEnv: "BINDING_DEEPSEEK_KEY",
		},
		Limits: config.RuntimeLimits{
			RequestTimeout:     time.Second,
			StreamIdleTimeout:  2 * time.Second,
			ProviderMaxRetries: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deepseek, ok := prov.(*DeepSeek)
	if !ok {
		t.Fatalf("provider = %T, want *DeepSeek", prov)
	}
	if deepseek.BaseURL != "https://api.deepseek.example" ||
		deepseek.APIKey != "sk-binding-value" ||
		deepseek.Model != "deepseek-v4-flash" ||
		deepseek.Thinking != "disabled" ||
		deepseek.ReasoningEffort != "low" ||
		deepseek.MaxTokens != 512 ||
		deepseek.RequestTimeout != time.Second ||
		deepseek.StreamIdleTimeout != 2*time.Second ||
		deepseek.MaxRetries != 3 {
		t.Fatalf("deepseek provider = %#v", deepseek)
	}
}

func TestNewFromBindingDeepSeekCredentialsIgnoreCodexAuthPath(t *testing.T) {
	t.Setenv("ISOLATED_DEEPSEEK_KEY", "sk-isolated-value")
	t.Setenv("CODEX_ACCESS_TOKEN", "")

	prov, err := NewFromBinding(config.ProviderBinding{
		Provider: config.ProviderSelection{
			Provider: "deepseek",
			BaseURL:  "https://api.deepseek.example",
		},
		Model: config.ModelSelection{
			Model: "deepseek-v4-flash",
		},
		Auth: config.AuthSettings{
			APIKeyEnv:       "ISOLATED_DEEPSEEK_KEY",
			CodexAuthFile:   filepath.Join(t.TempDir(), "missing-codex.json"),
			CodexRefreshURL: "https://auth.invalid/token",
			CodexClientID:   "codex-client",
		},
		Limits: config.RuntimeLimits{
			RequestTimeout:    time.Second,
			StreamIdleTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prov.(*DeepSeek); !ok {
		t.Fatalf("provider = %T, want *DeepSeek", prov)
	}
}

func TestNewFromBindingCodexIgnoresDeepSeekCredentials(t *testing.T) {
	t.Setenv("CODEX_ACCESS_TOKEN", "codex-env-token")
	t.Setenv("MISSING_DEEPSEEK_KEY", "")

	prov, err := NewFromBinding(config.ProviderBinding{
		Provider: config.ProviderSelection{
			Provider:     "deepseek",
			BaseURL:      "https://api.deepseek.invalid",
			CodexBaseURL: "https://codex.example/backend",
		},
		Model: config.ModelSelection{
			Model: "gpt-5.5",
		},
		Auth: config.AuthSettings{
			APIKeyEnv:       "MISSING_DEEPSEEK_KEY",
			CodexAuthFile:   filepath.Join(t.TempDir(), "missing-codex.json"),
			CodexRefreshURL: "https://auth.example/token",
			CodexClientID:   "codex-client",
			CodexOriginator: "test-originator",
		},
		Limits: config.RuntimeLimits{
			RequestTimeout:    time.Second,
			StreamIdleTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	codex, ok := prov.(*Codex)
	if !ok {
		t.Fatalf("provider = %T, want *Codex", prov)
	}
	if codex.BaseURL != "https://codex.example/backend" || codex.Auth.AccessToken != "codex-env-token" {
		t.Fatalf("codex provider = %#v", codex)
	}
}

func TestProviderStreamRunnerClosesEventsBeforeErrs(t *testing.T) {
	events := make(chan Event)
	errs := make(chan error)
	wantErr := errors.New("provider boom")
	done := make(chan struct{})
	go func() {
		defer close(done)
		runProviderStream(events, errs, func() error {
			return wantErr
		})
	}()

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("event channel yielded an event")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for events to close")
	}
	select {
	case err, ok := <-errs:
		if !ok || !errors.Is(err, wantErr) {
			t.Fatalf("err = %v ok=%v, want %v", err, ok, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal error")
	}
	if _, ok := <-errs; ok {
		t.Fatal("error channel yielded more than one value")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream runner did not return")
	}
}

func TestProviderStreamRunnerClosesErrsWithoutValueOnSuccess(t *testing.T) {
	events := make(chan Event, 1)
	errs := make(chan error)
	go runProviderStream(events, errs, func() error {
		events <- Event{Kind: EventDone}
		return nil
	})

	if event, ok := <-events; !ok || event.Kind != EventDone {
		t.Fatalf("event = %#v ok=%v", event, ok)
	}
	if _, ok := <-events; ok {
		t.Fatal("events channel remained open")
	}
	if err, ok := <-errs; ok || err != nil {
		t.Fatalf("err = %v ok=%v, want closed channel", err, ok)
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
		w.Header().Set("x-request-id", "deepseek-req-2")
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
		RequestID: "local-request",
		Model:     "deepseek-v4-flash",
		Messages:  []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	var content string
	var meta RequestMetadata
	for event := range events {
		if event.Kind == EventRequestMetadata {
			meta = event.Request
		}
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
	if meta.RequestID != "local-request" || meta.ProviderID != "deepseek" || meta.ModelID != "deepseek-v4-flash" ||
		meta.ProviderRequestID != "deepseek-req-2" || meta.Attempts != 2 || meta.Retries != 1 || meta.StatusCode != http.StatusOK {
		t.Fatalf("metadata = %#v", meta)
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
		w.Header().Set("x-request-id", "deepseek-error-request")
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
		RequestID: "local-deepseek-request",
		Model:     "deepseek-v4-flash",
		Messages:  []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
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
	metadata, ok := RequestMetadataFromError(err)
	if !ok ||
		metadata.RequestID != "local-deepseek-request" ||
		metadata.ProviderID != "deepseek" ||
		metadata.ModelID != "deepseek-v4-flash" ||
		metadata.ProviderRequestID != "deepseek-error-request" ||
		metadata.Attempts != 1 ||
		metadata.Retries != 0 ||
		metadata.StatusCode != http.StatusBadRequest {
		t.Fatalf("metadata = %#v ok=%v", metadata, ok)
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

func TestToolAccumulatorSanitizesInvalidJSONArguments(t *testing.T) {
	var acc ToolAccumulator
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ToolID: "call_bad", ToolName: "shell_exec", ArgsDelta: `{bad`})

	calls, err := acc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v", calls)
	}
	if string(calls[0].Arguments) != "{}" ||
		calls[0].InvalidArguments != "{bad" ||
		!strings.Contains(calls[0].InvalidArgumentError, "invalid JSON args") {
		t.Fatalf("call = %#v", calls[0])
	}
}

func TestToolAccumulatorErrorsOnMissingNameWithArguments(t *testing.T) {
	var acc ToolAccumulator
	acc.Push(Event{Kind: EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ArgsDelta: `{"path":"."}`})
	if _, err := acc.Finish(); err == nil || !strings.Contains(err.Error(), "missing name") {
		t.Fatalf("err = %v", err)
	}
}
