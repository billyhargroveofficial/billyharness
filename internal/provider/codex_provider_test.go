package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestCodexStreamSendsResponsesHeadersAndParsesEvents(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		for header, want := range map[string]string{
			"Authorization":      "Bearer secret-token",
			"ChatGPT-Account-ID": "acct_123",
			"X-OpenAI-Fedramp":   "true",
			"originator":         "billy-test",
			"User-Agent":         "billy-test/0",
			"session-id":         "session-test",
			"Accept":             "text/event-stream",
			"Content-Type":       "application/json",
		} {
			if got := r.Header.Get(header); got != want {
				t.Fatalf("%s = %q, want %q", header, got, want)
			}
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-5.5" || payload["stream"] != true || payload["store"] != false {
			t.Fatalf("payload = %#v", payload)
		}
		if payload["prompt_cache_key"] != "billyharness" {
			t.Fatalf("prompt_cache_key = %#v", payload["prompt_cache_key"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("openai-request-id", "codex-req-1")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"ok"}`,
			``,
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(server.Close)

	c := &Codex{
		BaseURL:           server.URL,
		RequestTimeout:    time.Second,
		StreamIdleTimeout: time.Second,
		Originator:        "billy-test",
		UserAgent:         "billy-test/0",
		SessionID:         "session-test",
		Auth:              &codexAuth{AccessToken: "secret-token", AccountID: "acct_123", FedRAMP: true},
		Client:            server.Client(),
	}
	events, errs := c.Stream(context.Background(), Request{
		Model:    "gpt-5.5",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !sawRequest {
		t.Fatal("server did not see request")
	}
	if len(got) != 4 ||
		got[0].Kind != EventRequestMetadata ||
		got[0].Request.ProviderID != "openai-codex" ||
		got[0].Request.ModelID != "gpt-5.5" ||
		got[0].Request.ProviderRequestID != "codex-req-1" ||
		got[1].Kind != EventContent ||
		got[1].Text != "ok" ||
		got[2].Kind != EventUsage ||
		got[3].Kind != EventDone {
		t.Fatalf("events = %#v", got)
	}
}

func TestCodexStreamDoesNotApplyRequestTimeoutAfterHeaders(t *testing.T) {
	requestTimeout := 50 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"first"}` + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(2 * requestTimeout)
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"second"}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{}}` + "\n\n"))
	}))
	t.Cleanup(server.Close)

	c := &Codex{
		BaseURL:           server.URL,
		RequestTimeout:    requestTimeout,
		StreamIdleTimeout: time.Second,
		Auth:              &codexAuth{AccessToken: "secret-token"},
		Client:            server.Client(),
	}
	events, errs := c.Stream(context.Background(), Request{
		Model:    "gpt-5.5",
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

func TestCodexStreamRedactsAccessTokenOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad secret-token", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	c := &Codex{
		BaseURL:        server.URL,
		RequestTimeout: time.Second,
		Auth:           &codexAuth{AccessToken: "secret-token"},
		Client:         server.Client(),
	}
	events, errs := c.Stream(context.Background(), Request{
		Model:    "gpt-5.5",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	for range events {
	}
	err := <-errs
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redacted marker missing: %v", err)
	}
}

func TestCodexStreamRefreshesExpiredTokenBeforeRequest(t *testing.T) {
	var refreshCalled bool
	var sawAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  testJWT(t, map[string]any{"exp": time.Now().Add(2 * time.Hour).Unix(), "refreshed": true}),
				"refresh_token": "refresh-new",
			})
		case "/responses":
			sawAuthorization = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	c := &Codex{
		BaseURL:         server.URL,
		RequestTimeout:  time.Second,
		CodexRefreshURL: server.URL + "/oauth/token",
		CodexClientID:   "client-test",
		Auth:            &codexAuth{AccessToken: expired, RefreshToken: "refresh-old", ExpiresAt: time.Now().Add(-time.Hour)},
		Client:          server.Client(),
	}
	events, errs := c.Stream(context.Background(), Request{Model: "gpt-5.5", Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}}})
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !refreshCalled {
		t.Fatal("refresh endpoint was not called")
	}
	if sawAuthorization == "Bearer "+expired {
		t.Fatalf("sent expired token")
	}
	if !strings.HasPrefix(sawAuthorization, "Bearer ") || sawAuthorization == "Bearer " {
		t.Fatalf("Authorization = %q", sawAuthorization)
	}
}

func TestCodexStreamRefreshesAfterUnauthorizedResponse(t *testing.T) {
	var refreshCalled bool
	var responseCalls int
	var sawAuthorization []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
				"refresh_token": "refresh-new",
			})
		case "/responses":
			responseCalls++
			sawAuthorization = append(sawAuthorization, r.Header.Get("Authorization"))
			if responseCalls == 1 {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("openai-request-id", "codex-req-2")
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c := &Codex{
		BaseURL:         server.URL,
		RequestTimeout:  time.Second,
		CodexRefreshURL: server.URL + "/oauth/token",
		CodexClientID:   "client-test",
		Auth: &codexAuth{
			AccessToken:  testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "refreshed": false}),
			RefreshToken: "refresh-old",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
		Client: server.Client(),
	}
	events, errs := c.Stream(context.Background(), Request{RequestID: "local-codex-request", Model: "gpt-5.5", Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}}})
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
	if !refreshCalled || responseCalls != 2 || content != "ok" {
		t.Fatalf("refresh=%v responseCalls=%d content=%q", refreshCalled, responseCalls, content)
	}
	if len(sawAuthorization) != 2 || sawAuthorization[0] == sawAuthorization[1] {
		t.Fatalf("authorization headers = %#v", sawAuthorization)
	}
	if meta.RequestID != "local-codex-request" || meta.ProviderID != "openai-codex" || meta.ModelID != "gpt-5.5" ||
		meta.ProviderRequestID != "codex-req-2" || meta.Attempts != 2 || meta.Retries != 1 || meta.StatusCode != http.StatusOK {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestCodexConcurrentStreamsRefreshExpiredTokenOnce(t *testing.T) {
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	refreshed := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "refreshed": true})
	var mu sync.Mutex
	var refreshCalls int
	var responseAuthorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			mu.Lock()
			refreshCalls++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  refreshed,
				"refresh_token": "refresh-new",
			})
		case "/responses":
			mu.Lock()
			responseAuthorizations = append(responseAuthorizations, r.Header.Get("Authorization"))
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	c := &Codex{
		BaseURL:         server.URL,
		RequestTimeout:  time.Second,
		CodexRefreshURL: server.URL + "/oauth/token",
		CodexClientID:   "client-test",
		Auth: &codexAuth{
			AccessToken:  expired,
			RefreshToken: "refresh-old",
			ExpiresAt:    time.Now().Add(-time.Hour),
		},
		Client: server.Client(),
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			events, streamErrs := c.Stream(context.Background(), Request{
				Model:    "gpt-5.5",
				Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
			})
			for range events {
			}
			errs <- <-streamErrs
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", refreshCalls)
	}
	if len(responseAuthorizations) != 2 {
		t.Fatalf("response authorizations = %#v", responseAuthorizations)
	}
	for _, auth := range responseAuthorizations {
		if auth != "Bearer "+refreshed {
			t.Fatalf("Authorization = %q, want refreshed token", auth)
		}
	}
}

func TestCodexStreamMissingAuthReturnsError(t *testing.T) {
	c := &Codex{BaseURL: "http://127.0.0.1", Client: http.DefaultClient}
	events, errs := c.Stream(context.Background(), Request{
		Model:    "gpt-5.5",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}},
	})
	for range events {
	}
	err := <-errs
	if err == nil || !strings.Contains(err.Error(), "missing an access token") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexBodyBuildsResponsesRequest(t *testing.T) {
	c := &Codex{ReasoningEffort: "high"}
	body, err := c.body(Request{
		Model: "gpt-5.5",
		Messages: []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system one"},
			{Role: protocol.RoleSystem, Content: "system two"},
			{Role: protocol.RoleUser, Content: "hello"},
			{Role: protocol.RoleAssistant, Content: "hi", ToolCalls: []protocol.ToolCall{{
				ID:        "call_1",
				Name:      "fs_read_file",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}}},
			{Role: protocol.RoleTool, ToolCallID: "call_1", Content: "readme text"},
		},
		Tools: []protocol.ToolSpec{{
			Name:        "fs_read_file",
			Description: "Read a file.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			Risk:        protocol.RiskReadOnly,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "gpt-5.5" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if payload["instructions"] != "system one\n\nsystem two" {
		t.Fatalf("instructions = %#v", payload["instructions"])
	}
	if payload["stream"] != true || payload["store"] != false {
		t.Fatalf("stream/store = %#v", payload)
	}
	if payload["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %#v", payload["tool_choice"])
	}
	if payload["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls = %#v", payload["parallel_tool_calls"])
	}
	reasoning := payload["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	input := payload["input"].([]any)
	if input[0].(map[string]any)["role"] != "user" {
		t.Fatalf("input[0] = %#v", input[0])
	}
	if input[2].(map[string]any)["type"] != "function_call" {
		t.Fatalf("input[2] = %#v", input[2])
	}
	if input[3].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("input[3] = %#v", input[3])
	}
	tools := payload["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "fs_read_file" {
		t.Fatalf("tools = %#v", tools)
	}
	if tool["strict"] != false {
		t.Fatalf("strict = %#v", tool["strict"])
	}
	if _, ok := tool["function"]; ok {
		t.Fatalf("Responses tools must not use chat-completions function wrapper: %#v", tool)
	}
}

func TestCodexBodyDisablesParallelToolCallsWithoutTools(t *testing.T) {
	c := &Codex{}
	body, err := c.body(Request{
		Model:    "gpt-5.5",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["tools"]; ok {
		t.Fatalf("tools should be omitted: %#v", payload["tools"])
	}
	if payload["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v", payload["parallel_tool_calls"])
	}
}

func TestCodexBodyEdgeCases(t *testing.T) {
	c := &Codex{ReasoningEffort: "off"}
	body, err := c.body(Request{
		Model:    "gpt-5.5",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("reasoning should be omitted: %#v", payload["reasoning"])
	}

	_, err = c.body(Request{Tools: []protocol.ToolSpec{{Name: "bad", Parameters: json.RawMessage(`{bad`)}}})
	if err == nil || !strings.Contains(err.Error(), "invalid tool schema") {
		t.Fatalf("err = %v", err)
	}
	if got := codexReasoningEffort(" XHIGH "); got != "xhigh" {
		t.Fatalf("effort = %q", got)
	}
	if got := codexResponsesURL("https://example.test/responses"); got != "https://example.test/responses" {
		t.Fatalf("url = %q", got)
	}
}

func TestParseResponsesSSEEmitsTextReasoningToolUsageAndDone(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
		``,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"fs_read_file"}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\""}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":":\"README.md\"}"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":6},"output_tokens_details":{"reasoning_tokens":2}}}}`,
		``,
	}, "\n")
	events := parseTestResponsesSSE(t, sse)
	if len(events) != 7 {
		t.Fatalf("events len = %d: %#v", len(events), events)
	}
	if events[0].Kind != EventContent || events[0].Text != "hello" {
		t.Fatalf("events[0] = %#v", events[0])
	}
	if events[1].Kind != EventReasoning || events[1].Text != "thinking" {
		t.Fatalf("events[1] = %#v", events[1])
	}
	if events[2].Kind != EventToolCallDelta || events[2].ToolID != "call_1" || events[2].ToolName != "fs_read_file" {
		t.Fatalf("tool add = %#v", events[2])
	}
	if events[3].ArgsDelta != `{"path"` || events[4].ArgsDelta != `:"README.md"}` {
		t.Fatalf("arg deltas = %#v %#v", events[3], events[4])
	}
	wantUsage := Usage{InputTokens: 10, OutputTokens: 4, CacheHitTokens: 6, CacheMissTokens: 4, ReasoningTokens: 2}
	if events[5].Kind != EventUsage || events[5].Usage != wantUsage {
		t.Fatalf("usage = %#v", events[5])
	}
	if events[6].Kind != EventDone {
		t.Fatalf("done = %#v", events[6])
	}
}

func TestParseResponsesSSEUsesOutputItemDoneTextFallback(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer"}]}}`,
		``,
		`data: {"type":"response.completed","response":{}}`,
		``,
	}, "\n")
	events := parseTestResponsesSSE(t, sse)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Kind != EventContent || events[0].Text != "final answer" {
		t.Fatalf("content = %#v", events[0])
	}
	if events[1].Kind != EventDone {
		t.Fatalf("done = %#v", events[1])
	}
}

func TestParseResponsesSSEUsesOutputItemDoneFunctionArgumentsFallback(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"fs_list","arguments":"{\"path\":\".\"}"}}`,
		``,
		`data: {"type":"response.completed","response":{}}`,
		``,
	}, "\n")
	events := parseTestResponsesSSE(t, sse)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Kind != EventToolCallDelta || events[0].ToolID != "call_1" || events[0].ToolName != "fs_list" || events[0].ArgsDelta != `{"path":"."}` {
		t.Fatalf("tool event = %#v", events[0])
	}
}

func TestParseResponsesSSEAliasesArgsBeforeOutputItemAdded(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\""}`,
		``,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"fs_read_file"}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":":\"README.md\"}"}`,
		``,
		`data: {"type":"response.completed","response":{}}`,
		``,
	}, "\n")
	events := parseTestResponsesSSE(t, sse)
	if events[0].ToolID != "fc_1" || events[0].ArgsDelta != `{"path"` {
		t.Fatalf("first delta = %#v", events[0])
	}
	if events[1].ToolID != "call_1" || events[1].ToolName != "fs_read_file" {
		t.Fatalf("added event = %#v", events[1])
	}
	if events[2].ToolID != "call_1" || events[2].ArgsDelta != `:"README.md"}` {
		t.Fatalf("aliased delta = %#v", events[2])
	}
}

func TestParseResponsesSSEDoesNotDuplicateDoneTextAfterTextDelta(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"streamed"}`,
		``,
		`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fallback"}]}}`,
		``,
		`data: {"type":"response.completed","response":{}}`,
		``,
	}, "\n")
	events := parseTestResponsesSSE(t, sse)
	var content []string
	for _, event := range events {
		if event.Kind == EventContent {
			content = append(content, event.Text)
		}
	}
	if strings.Join(content, "") != "streamed" {
		t.Fatalf("content = %#v", content)
	}
}

func TestParseResponsesSSEEmitsReasoningOutputItemSummary(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"reasoning","summary":[{"type":"summary_text","text":"summary"}]}}`,
		``,
		`data: {"type":"response.completed","response":{}}`,
		``,
	}, "\n")
	events := parseTestResponsesSSE(t, sse)
	if events[0].Kind != EventReasoning || events[0].Text != "summary" {
		t.Fatalf("events = %#v", events)
	}
}

func TestParseResponsesSSEReturnsFailedResponseError(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.failed","response":{"error":{"code":"rate_limit","message":"slow down"}}}`,
		``,
	}, "\n")
	events := make(chan Event, 8)
	err := parseResponsesSSE(context.Background(), strings.NewReader(sse), 0, events)
	if err == nil || !strings.Contains(err.Error(), "Codex rate_limit: slow down") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseResponsesSSEReturnsIncompleteDetailsAndErrorEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		sse  string
		want string
	}{
		{
			name: "incomplete",
			sse:  "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n",
			want: "Codex incomplete response: max_output_tokens",
		},
		{
			name: "error",
			sse:  "data: {\"type\":\"error\",\"error\":{\"code\":\"bad\",\"message\":\"wrong\"}}\n\n",
			want: "Codex error bad: wrong",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan Event, 8)
			err := parseResponsesSSE(context.Background(), strings.NewReader(tc.sse), 0, events)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseResponsesSSEHandlesMultilineDataAndCRLF(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\r\n" +
		"data: \"delta\":\"hi\"}\r\n\r\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\r\n\r\n"
	events := parseTestResponsesSSE(t, sse)
	if events[0].Kind != EventContent || events[0].Text != "hi" {
		t.Fatalf("events = %#v", events)
	}
}

func TestParseResponsesSSEReturnsErrorWhenStreamEndsBeforeCompleted(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	events := make(chan Event, 8)
	err := parseResponsesSSE(context.Background(), strings.NewReader(sse), 0, events)
	if err == nil || !strings.Contains(err.Error(), "before response.completed") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseResponsesSSEReturnsWhenEventConsumerBlockedAndContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event)
	done := make(chan error, 1)
	go func() {
		done <- parseResponsesSSE(ctx, strings.NewReader(`data: {"type":"response.output_text.delta","delta":"blocked"}`+"\n\n"), time.Minute, events)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parseResponsesSSE did not return after context cancellation")
	}
}

func parseTestResponsesSSE(t *testing.T, sse string) []Event {
	t.Helper()
	events := make(chan Event, 32)
	if err := parseResponsesSSE(context.Background(), strings.NewReader(sse), 0, events); err != nil {
		t.Fatal(err)
	}
	close(events)
	var out []Event
	for event := range events {
		out = append(out, event)
	}
	return out
}
