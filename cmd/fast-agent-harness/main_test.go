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
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Provider != "openai-codex" ||
		captured.Model != "gpt-5.5" ||
		captured.Profile != "billy" ||
		captured.ReasoningEffort != "xhigh" ||
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
