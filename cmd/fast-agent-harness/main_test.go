package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewayRunSendsFullRunRequest(t *testing.T) {
	var captured gateway.RunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/v1/run" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{Type: protocol.EventRunCompleted})
	}))
	t.Cleanup(server.Close)

	err := gatewayRun(context.Background(), server.URL, "/v1/run", gateway.RunRequest{
		Prompt:          "ping",
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		Profile:         "billy",
		Thinking:        "enabled",
		ReasoningEffort: "xhigh",
		MaxToolRounds:   42,
		AccessMode:      "plan",
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Provider != "openai-codex" ||
		captured.Model != "gpt-5.5" ||
		captured.Profile != "billy" ||
		captured.ReasoningEffort != "xhigh" ||
		captured.AccessMode != "plan" ||
		captured.MaxToolRounds != 42 ||
		captured.Prompt != "ping" {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestGatewayRunReturnsStreamedFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{Type: protocol.EventRunFailed, Data: "boom"})
	}))
	t.Cleanup(server.Close)

	err := gatewayRun(context.Background(), server.URL, "/v1/run", gateway.RunRequest{Prompt: "ping"}, func(protocol.Event) {})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverGatewayURLUsesConfiguredHealthyGateway(t *testing.T) {
	t.Setenv("FAST_AGENT_GATEWAY_URL", "")
	t.Setenv("BILLYHARNESS_GATEWAY_URL", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	addr := strings.TrimPrefix(server.URL, "http://")
	got, ok := discoverGatewayURL(context.Background(), config.Config{GatewayAddr: addr})
	if !ok {
		t.Fatal("gateway was not discovered")
	}
	if got != server.URL {
		t.Fatalf("gateway URL = %q, want %q", got, server.URL)
	}
}

func TestNormalizeGatewayURL(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:8765":       "http://127.0.0.1:8765",
		":8765":                "http://127.0.0.1:8765",
		"0.0.0.0:8765":         "http://127.0.0.1:8765",
		"http://localhost:80/": "http://localhost:80",
	}
	for input, want := range tests {
		if got := normalizeGatewayURL(input); got != want {
			t.Fatalf("normalizeGatewayURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConfigInspectJSONDoesNotLeakDotenvSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("DEEPSEEK_API_KEY=sk-config-inspect-secret\nFAST_AGENT_MODEL=deepseek-v4-pro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := configCommand([]string{"inspect", "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sk-config-inspect-secret") {
		t.Fatalf("config inspect leaked secret: %s", out.String())
	}
	if !strings.Contains(out.String(), `"model"`) || !strings.Contains(out.String(), `deepseek-v4-pro`) {
		t.Fatalf("config inspect missing model: %s", out.String())
	}
	if !strings.Contains(out.String(), `"diagnostics"`) ||
		!strings.Contains(out.String(), `"provider_auth"`) ||
		!strings.Contains(out.String(), `"runtime_tool"`) {
		t.Fatalf("config inspect missing diagnostics: %s", out.String())
	}
}

func TestSessionsCommandListsAndInspectsStore(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := gateway.NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), gateway.ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created gateway.SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(`{"prompt":"inspect me"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}

	var listOut bytes.Buffer
	if err := sessionsCommand([]string{"list", "-dir", storeDir}, &listOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOut.String(), created.ID) || !strings.Contains(listOut.String(), "replay=true") {
		t.Fatalf("list output missing session/replay:\n%s", listOut.String())
	}

	var inspectOut bytes.Buffer
	if err := sessionsCommand([]string{"inspect", "-dir", storeDir, created.ID}, &inspectOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"offline replay: true", "config_snapshot exists=true", "model_provider_snapshot exists=true", "mcp_snapshot exists=true"} {
		if !strings.Contains(inspectOut.String(), want) {
			t.Fatalf("inspect output missing %q:\n%s", want, inspectOut.String())
		}
	}

	var jsonOut bytes.Buffer
	if err := sessionsCommand([]string{"inspect", "-dir", storeDir, "-json", created.ID}, &jsonOut); err != nil {
		t.Fatal(err)
	}
	var inspection gateway.StoredSessionInspection
	if err := json.Unmarshal(jsonOut.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.SessionID != created.ID || !inspection.OfflineReplayReady || inspection.Events.Records == 0 {
		t.Fatalf("inspection = %#v", inspection)
	}

	var indexOut bytes.Buffer
	if err := sessionsCommand([]string{"index", "rebuild", "-dir", storeDir}, &indexOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexOut.String(), "session index") || !strings.Contains(indexOut.String(), created.ID) {
		t.Fatalf("index rebuild output:\n%s", indexOut.String())
	}
	var indexJSON bytes.Buffer
	if err := sessionsCommand([]string{"index", "show", "-dir", storeDir, "-json"}, &indexJSON); err != nil {
		t.Fatal(err)
	}
	var index gateway.StoredSessionIndex
	if err := json.Unmarshal(indexJSON.Bytes(), &index); err != nil {
		t.Fatal(err)
	}
	if index.SessionCount != 1 || index.Sessions[0].ID != created.ID {
		t.Fatalf("index = %#v", index)
	}
	var deleteOut bytes.Buffer
	if err := sessionsCommand([]string{"index", "delete", "-dir", storeDir}, &deleteOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deleteOut.String(), "deleted") {
		t.Fatalf("index delete output: %s", deleteOut.String())
	}
}

func TestParseChatIDs(t *testing.T) {
	got, err := parseChatIDs("-100123, 42\n99")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{-100123, 42, 99} {
		if !got[id] {
			t.Fatalf("missing chat id %d in %#v", id, got)
		}
	}
	if _, err := parseChatIDs("bad"); err == nil {
		t.Fatal("expected invalid chat id error")
	}
}
