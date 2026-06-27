package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewaySessionRunStreamsEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.ID == "" {
		t.Fatal("empty session id")
	}

	body := bytes.NewBufferString(`{"prompt":"through gateway"}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	var content strings.Builder
	scanner := bufio.NewScanner(run.Body)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == protocol.EventAssistantDelta {
			content.WriteString(event.Data.(string))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := content.String(); got != "mock: through gateway" {
		t.Fatalf("content = %q", got)
	}
}

func TestGatewaySessionRunPersistsHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"one", "two"} {
		run := httptest.NewRecorder()
		server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"`+prompt+`"}`)))
		if run.Code != http.StatusOK {
			t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
		}
	}

	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		MessageCount int                `json:"message_count"`
		Messages     []protocol.Message `json:"messages"`
		Running      bool               `json:"running"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Running {
		t.Fatal("session reports running after completed runs")
	}
	if got.MessageCount != len(got.Messages) || got.MessageCount < 5 {
		t.Fatalf("unexpected message count: %+v", got)
	}
	if got.Messages[len(got.Messages)-4].Content != "one" ||
		got.Messages[len(got.Messages)-3].Content != "mock: one" ||
		got.Messages[len(got.Messages)-2].Content != "two" ||
		got.Messages[len(got.Messages)-1].Content != "mock: two" {
		t.Fatalf("unexpected history tail: %+v", got.Messages)
	}
}

func TestGatewaySessionCancelEndpointCancelsActiveThread(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	thread := sessionpkg.New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	server.sessions["test-session"] = &Session{
		ID:      "test-session",
		Created: time.Now().UTC(),
		Thread:  thread,
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- thread.Run(context.Background(), sessionpkg.RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
			close(started)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "wait", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("thread did not start")
	}

	cancel := httptest.NewRecorder()
	server.Handler().ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/sessions/test-session/cancel", nil))
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancel.Code, cancel.Body.String())
	}
	if !strings.Contains(cancel.Body.String(), `"cancelled":true`) {
		t.Fatalf("cancel response = %s", cancel.Body.String())
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("thread error = %v, want context.Canceled", err)
	}
}

func TestGatewayRunAcceptsModelOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"prompt":"override","model":"mock","thinking":"disabled","reasoning_effort":"high","max_tool_rounds":3}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("unexpected body=%s", run.Body.String())
	}
}

func TestGatewayRunProviderOverrideWorksWithoutDefaultCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.APIKeyEnv = "BILLYHARNESS_TEST_MISSING_KEY"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"prompt":"override","provider":"mock","model":"mock","max_tool_rounds":2}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("override was not used, body=%s", run.Body.String())
	}
	if strings.Contains(run.Body.String(), string(protocol.EventRunFailed)) {
		t.Fatalf("run failed: %s", run.Body.String())
	}
}

func TestGatewayAuthEndpointsSaveDeepSeekAndImportCodex(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	deepseek := httptest.NewRecorder()
	server.Handler().ServeHTTP(deepseek, httptest.NewRequest(http.MethodPost, "/v1/auth/deepseek", bytes.NewBufferString(`{"api_key":"sk-test-secret"}`)))
	if deepseek.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", deepseek.Code, deepseek.Body.String())
	}
	if strings.Contains(deepseek.Body.String(), "sk-test-secret") {
		t.Fatalf("response leaked key: %s", deepseek.Body.String())
	}
	if body, err := os.ReadFile(filepath.Join(root, ".env")); err != nil || !strings.Contains(string(body), "DEEPSEEK_API_KEY=sk-test-secret") {
		t.Fatalf(".env body=%q err=%v", body, err)
	}

	sourceDir := filepath.Join(root, "codex")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "auth.json")
	if err := os.WriteFile(source, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"a.b.c","refresh_token":"rt-test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := httptest.NewRecorder()
	server.Handler().ServeHTTP(codex, httptest.NewRequest(http.MethodPost, "/v1/auth/codex/import", bytes.NewBufferString(`{"source_path":"`+source+`"}`)))
	if codex.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", codex.Code, codex.Body.String())
	}
	if strings.Contains(codex.Body.String(), "rt-test") {
		t.Fatalf("response leaked refresh token: %s", codex.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "auth", "codex.json")); err != nil {
		t.Fatal(err)
	}

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/auth/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"configured":true`) {
		t.Fatalf("status = %d body=%s", status.Code, status.Body.String())
	}
}

func TestGatewayAuthMiddlewareProtectsNonLoopbackClients(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{AuthToken: "secret"})

	health := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthReq.RemoteAddr = "203.0.113.10:4444"
	server.Handler().ServeHTTP(health, healthReq)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", health.Code, health.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	unauthorizedReq.RemoteAddr = "203.0.113.10:4444"
	server.Handler().ServeHTTP(unauthorized, unauthorizedReq)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	if unauthorized.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate header")
	}

	authorized := httptest.NewRecorder()
	authorizedReq := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	authorizedReq.RemoteAddr = "203.0.113.10:4444"
	authorizedReq.Header.Set("Authorization", "Bearer secret")
	server.Handler().ServeHTTP(authorized, authorizedReq)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", authorized.Code, authorized.Body.String())
	}

	local := httptest.NewRecorder()
	localReq := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	localReq.RemoteAddr = "127.0.0.1:4444"
	server.Handler().ServeHTTP(local, localReq)
	if local.Code != http.StatusOK {
		t.Fatalf("local status = %d body=%s", local.Code, local.Body.String())
	}
}

func TestGatewayServeUsesPreboundListener(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(ctx, listener)
	}()
	if !WaitForReady(context.Background(), NormalizeBaseURL(listener.Addr().String()), time.Second) {
		cancel()
		select {
		case <-errs:
		case <-time.After(time.Second):
		}
		t.Fatal("gateway did not become ready on prebound listener")
	}
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
}

func TestGatewaySessionRunProviderOverrideWorksWithoutDefaultCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.APIKeyEnv = "BILLYHARNESS_TEST_MISSING_KEY"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"prompt":"override","provider":"mock","model":"mock","max_tool_rounds":2}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("override was not used, body=%s", run.Body.String())
	}
}

func TestGatewayCreateSessionAcceptsMessages(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"messages":[{"role":"system","content":"system"},{"role":"user","content":"old"}]}`)
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", body))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		MessageCount int                `json:"message_count"`
		Messages     []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MessageCount != 2 || len(got.Messages) != 2 || got.Messages[1].Content != "old" {
		t.Fatalf("expected restored messages, got=%+v body=%s", got, get.Body.String())
	}
}

func TestGatewayCreateSessionUsesRequestedProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"profile":"billy"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), "# Billyharness profile: billy") ||
		!strings.Contains(get.Body.String(), "Формулы пиши в LaTeX") {
		t.Fatalf("profile not injected: %s", get.Body.String())
	}
}

func TestGatewayToolsExposeMCPRegistry(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestGatewayFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_GATEWAY_MCP_HELPER": "1"},
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		Enabled:        true,
	}}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	server := NewServer(cfg, provider.Mock{}, registry)

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `"name":"mcp__fake__echo"`) {
		t.Fatalf("tools body should expose lazy MCP gateway, not raw MCP tools: %s", body)
	}
	if !strings.Contains(body, `"name":"mcp_list_tools"`) || !strings.Contains(body, `"name":"mcp_call"`) || !strings.Contains(body, `"name":"time_now"`) {
		t.Fatalf("tools body missing lazy MCP/native tools: %s", body)
	}

	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"fake"`) || !strings.Contains(rec.Body.String(), `"connected":true`) {
		t.Fatalf("mcp body missing connected server status: %s", rec.Body.String())
	}
}

func TestGatewayFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_GATEWAY_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
			}})
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo text",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			}}}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		}
	}
	os.Exit(0)
}
