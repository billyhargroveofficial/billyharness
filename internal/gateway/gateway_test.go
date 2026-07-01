package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestNewServerFromSettingsClonesProjectionSettings(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{"/repo"}
	cfg.ProjectDocFallbacks = []string{"README.md"}
	cfg.MCPEnabled = true
	cfg.MCPConfigFiles = []string{"mcp.toml"}
	cfg.MCPAllowedServers = []string{"github"}
	cfg.MCPServers = []config.MCPServer{{
		Name: "github",
		Args: []string{"serve"},
		Env:  map[string]string{"TOKEN": "secret"},
	}}
	cfg.HooksEnabled = true
	cfg.HookConfigFiles = []string{"hooks.toml"}
	cfg.Hooks = []config.Hook{{
		Name:    "audit",
		Event:   "before_tool",
		Command: "sh",
		Args:    []string{"-c", "true"},
		Env:     map[string]string{"HOOK_TOKEN": "secret"},
		Enabled: true,
	}}

	settings := ServerSettingsFromConfig(cfg)
	server := NewServerFromSettings(settings, provider.Mock{}, tools.NewRegistryFromSettings(tools.RegistrySettingsFromConfig(cfg)))

	settings.ToolPolicy.WorkspaceRoots[0] = "/mutated"
	settings.ToolPolicy.ProjectDocFallbacks[0] = "MUTATED.md"
	settings.MCP.ConfigFiles[0] = "mutated.toml"
	settings.MCP.AllowedServers[0] = "mutated"
	settings.MCP.Servers[0].Args[0] = "mutated"
	settings.MCP.Servers[0].Env["TOKEN"] = "mutated"
	settings.Hooks.ConfigFiles[0] = "mutated-hooks.toml"
	settings.Hooks.Hooks[0].Args[0] = "mutated"
	settings.Hooks.Hooks[0].Env["HOOK_TOKEN"] = "mutated"
	settings.Instructions.WorkspaceRoots[0] = "/mutated"
	settings.Instructions.ProjectDocFallbacks[0] = "MUTATED.md"

	if server.toolPolicy.WorkspaceRoots[0] != "/repo" ||
		server.toolPolicy.ProjectDocFallbacks[0] != "README.md" ||
		server.instructions.WorkspaceRoots[0] != "/repo" ||
		server.instructions.ProjectDocFallbacks[0] != "README.md" {
		t.Fatalf("server tool/instruction settings changed after caller mutation: %#v %#v", server.toolPolicy, server.instructions)
	}
	if server.mcpSettings.ConfigFiles[0] != "mcp.toml" ||
		server.mcpSettings.AllowedServers[0] != "github" ||
		server.mcpSettings.Servers[0].Args[0] != "serve" ||
		server.mcpSettings.Servers[0].Env["TOKEN"] != "secret" {
		t.Fatalf("server MCP settings changed after caller mutation: %#v", server.mcpSettings)
	}
	if server.hookSettings.ConfigFiles[0] != "hooks.toml" ||
		server.hookSettings.Hooks[0].Args[0] != "-c" ||
		server.hookSettings.Hooks[0].Env["HOOK_TOKEN"] != "secret" {
		t.Fatalf("server hook settings changed after caller mutation: %#v", server.hookSettings)
	}
}

func TestSessionInputRequestFromRunIncludesMetadata(t *testing.T) {
	input := sessionInputRequestFromRun(RunRequest{
		InputID:         "input-1",
		Prompt:          "expanded prompt",
		InterruptPolicy: "interrupt",
		ClientID:        "tui",
		Metadata: map[string]string{
			"prompt_command":                 "review",
			"prompt_command_original":        "/review internal/tui",
			"prompt_command_expanded_sha256": "abc123",
		},
	})
	if input.InputID != "input-1" || input.Prompt != "expanded prompt" || input.ClientID != "tui" {
		t.Fatalf("input request = %#v", input)
	}
	if input.Metadata["prompt_command_original"] != "/review internal/tui" ||
		input.Metadata["prompt_command_expanded_sha256"] != "abc123" {
		t.Fatalf("metadata = %#v", input.Metadata)
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

func TestGatewayShutdownAbortRecordsActiveSessionFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	session := newGatewaySession("test-session", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	server.attachSessionStore(session)
	server.mu.Lock()
	server.sessions[session.ID] = session
	server.mu.Unlock()
	if err := server.saveSession(session); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	runID := "run-shutdown-abort"
	go func() {
		done <- session.Thread.Run(context.Background(), sessionpkg.RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
			emit(protocol.Event{Type: protocol.EventRunStarted, RunID: runID})
			close(started)
			<-ctx.Done()
			emit(protocol.Event{Type: protocol.EventRunFailed, RunID: runID, Data: ctx.Err().Error()})
			return messages, ctx.Err()
		}), "wait", func(event protocol.Event) {
			if event.Type == protocol.EventRunStarted {
				session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"})
			}
			session.observeRunEvent(event)
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("thread did not start")
	}

	if aborted := server.abortActiveSessions("gateway shutdown"); aborted != 1 {
		t.Fatalf("aborted = %d, want 1", aborted)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("thread error = %v, want context.Canceled", err)
	}
	status := session.Status()
	if status.Running || status.LastError != "gateway shutdown" {
		t.Fatalf("status after abort = %#v", status)
	}
	records := readSessionEventRecords(t, filepath.Join(storeDir, session.ID, sessionEventsJSONLName))
	if len(records) == 0 {
		t.Fatal("no event records written")
	}
	failedCount := 0
	for _, record := range records {
		if record.Event.Type != protocol.EventRunFailed {
			continue
		}
		failedCount++
		if record.Event.RunID != runID || fmt.Sprint(record.Event.Data) != "gateway shutdown" {
			t.Fatalf("run.failed record = %#v, want run_id %q data gateway shutdown", record, runID)
		}
	}
	if failedCount != 1 {
		t.Fatalf("run.failed count = %d, records=%#v", failedCount, records)
	}
	if _, err := server.store.ReplayEventsAfter(session.ID, 0); err != nil {
		t.Fatalf("shutdown abort replay failed: %v", err)
	}
}

func TestGatewayRunAcceptsModelOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
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

func TestGatewayConfigStatusIsSanitized(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DEEPSEEK_API_KEY=sk-gateway-config-secret\nFAST_AGENT_MODEL=deepseek-v4-pro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "sk-gateway-config-secret") {
		t.Fatalf("config endpoint leaked secret: %s", body)
	}
	if !strings.Contains(body, `"key":"model"`) || !strings.Contains(body, config.SourceGateway) {
		t.Fatalf("config endpoint missing runtime model provenance: %s", body)
	}
	if !strings.Contains(body, `"diagnostics"`) ||
		!strings.Contains(body, `"provider_auth"`) ||
		!strings.Contains(body, `"runtime_tool"`) {
		t.Fatalf("config endpoint missing diagnostics: %s", body)
	}
}

func TestGatewayAPIRedactsSecretsAcrossResponsesAndStreams(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	envSecret := "env-secret-value-123456789"
	t.Setenv("BILLY_TEST_TOKEN", envSecret)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DEEPSEEK_API_KEY=sk-gateway-boundary-secret-1234567890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	rawKey := "sk-session-boundary-secret-1234567890"
	rawPAT := "github_pat_" + strings.Repeat("A", 30)
	assertRedacted := func(label, body string) {
		t.Helper()
		for _, secret := range []string{rawKey, rawPAT, envSecret, "sk-gateway-boundary-secret-1234567890"} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s leaked %q in body: %s", label, secret, body)
			}
		}
		if strings.Contains(body, "github_pat_") || strings.Contains(body, "sk-session-boundary-secret") {
			t.Fatalf("%s retained token-looking material: %s", label, body)
		}
	}
	assertJSON := func(label, body string, stream bool) {
		t.Helper()
		if stream {
			requireLines := !strings.Contains(label, "event replay")
			scanner := bufio.NewScanner(strings.NewReader(body))
			lines := 0
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				lines++
				if !json.Valid([]byte(line)) {
					t.Fatalf("%s emitted invalid NDJSON line %d: %s", label, lines, line)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("%s scan NDJSON: %v", label, err)
			}
			if requireLines && lines == 0 {
				t.Fatalf("%s emitted no NDJSON lines", label)
			}
			return
		}
		if !json.Valid([]byte(body)) {
			t.Fatalf("%s emitted invalid JSON: %s", label, body)
		}
	}

	createPayload, err := json.Marshal(CreateSessionRequest{Messages: []protocol.Message{
		{Role: protocol.RoleUser, Content: "user pasted " + rawKey},
		{Role: protocol.RoleAssistant, Content: "tool output had " + rawPAT + " and " + envSecret},
	}})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(createPayload)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("empty session id")
	}
	assertRedacted("create session", create.Body.String())
	assertJSON("create session", create.Body.String(), false)

	for _, tc := range []struct {
		label  string
		method string
		path   string
		body   string
	}{
		{label: "config", method: http.MethodGet, path: "/v1/config"},
		{label: "auth status", method: http.MethodGet, path: "/v1/auth/status"},
		{label: "session", method: http.MethodGet, path: "/v1/sessions/" + created.ID},
		{label: "session context", method: http.MethodGet, path: "/v1/sessions/" + created.ID + "/context"},
		{label: "global run stream", method: http.MethodPost, path: "/v1/run", body: `{"prompt":"repeat ` + rawKey + ` and task_api_key = \"literal\" safely"}`},
		{label: "session run stream", method: http.MethodPost, path: "/v1/sessions/" + created.ID + "/run", body: `{"prompt":"repeat ` + rawPAT + ` and explicit_api_key=abc123"}`},
		{label: "event replay stream", method: http.MethodGet, path: "/v1/sessions/" + created.ID + "/events?after_seq=0&follow=false"},
	} {
		rec := httptest.NewRecorder()
		var reader io.Reader
		if tc.body != "" {
			reader = strings.NewReader(tc.body)
		}
		server.Handler().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, reader))
		if rec.Code < 200 || rec.Code >= 300 {
			t.Fatalf("%s status = %d body=%s", tc.label, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		assertRedacted(tc.label, body)
		assertJSON(tc.label, body, strings.Contains(tc.label, "stream"))
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
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
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
	if !strings.Contains(rec.Body.String(), `"name":"fake"`) ||
		!strings.Contains(rec.Body.String(), `"connected":true`) ||
		!strings.Contains(rec.Body.String(), `"state":"connected"`) ||
		!strings.Contains(rec.Body.String(), `"retry_count":0`) ||
		!strings.Contains(rec.Body.String(), `"restart_count":0`) {
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

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func readSessionHistoryRecords(t *testing.T, path string) []sessionHistoryRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []sessionHistoryRecord
	decoder := json.NewDecoder(file)
	for {
		var record sessionHistoryRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func readSessionEventRecords(t *testing.T, path string) []sessionEventRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []sessionEventRecord
	decoder := json.NewDecoder(file)
	for {
		var record sessionEventRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func readProtocolEvents(t *testing.T, r io.Reader) []protocol.Event {
	t.Helper()
	var events []protocol.Event
	decoder := json.NewDecoder(r)
	for {
		var event protocol.Event
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func sawSessionEvent(events []sessionEventRecord, typ protocol.EventType) bool {
	for _, event := range events {
		if event.EventType == string(typ) {
			return true
		}
	}
	return false
}

func decodeProtocolEvents(r io.Reader) <-chan protocol.Event {
	out := make(chan protocol.Event, 64)
	go func() {
		defer close(out)
		dec := json.NewDecoder(r)
		for {
			var event protocol.Event
			if err := dec.Decode(&event); err != nil {
				return
			}
			out <- event
		}
	}()
	return out
}

func waitProtocolEvent(t *testing.T, events <-chan protocol.Event) protocol.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event stream closed")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return protocol.Event{}
	}
}

func eventStatus(t *testing.T, event protocol.Event) SessionStatus {
	t.Helper()
	bytes, err := json.Marshal(event.Data)
	if err != nil {
		t.Fatal(err)
	}
	var status SessionStatus
	if err := json.Unmarshal(bytes, &status); err != nil {
		t.Fatal(err)
	}
	return status
}
