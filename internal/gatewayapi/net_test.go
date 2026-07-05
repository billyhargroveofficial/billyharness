package gatewayapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		" 127.0.0.1:8765/ ":        "http://127.0.0.1:8765",
		":8765/":                   "http://127.0.0.1:8765",
		"0.0.0.0:8765":             "http://127.0.0.1:8765",
		"[::]:8765":                "http://127.0.0.1:8765",
		"http://0.0.0.0:8765/":     "http://127.0.0.1:8765",
		"http://0.0.0.0:8765/path": "http://127.0.0.1:8765/path",
		"http://[::]:8765":         "http://127.0.0.1:8765",
		"http://localhost:80/":     "http://localhost:80",
		"https://example.com/api/": "https://example.com/api",
	}
	for input, want := range tests {
		if got := NormalizeBaseURL(input); got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAuthHeaderUsesPrimaryThenLegacyEnv(t *testing.T) {
	t.Setenv(GatewayAuthTokenEnv, " primary-token ")
	t.Setenv(LegacyGatewayAuthTokenEnv, "legacy-token")
	if got := AuthTokenFromEnv(); got != "primary-token" {
		t.Fatalf("AuthTokenFromEnv primary = %q", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	SetAuthHeader(req, " explicit-token ")
	if got := req.Header.Get("Authorization"); got != "Bearer explicit-token" {
		t.Fatalf("Authorization = %q", got)
	}
	SetAuthHeader(req, "replacement")
	if got := req.Header.Get("Authorization"); got != "Bearer explicit-token" {
		t.Fatalf("SetAuthHeader should not replace existing Authorization, got %q", got)
	}
}

func TestSetAuthHeaderFromEnv(t *testing.T) {
	t.Setenv(GatewayAuthTokenEnv, "test-token")

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	SetAuthHeaderFromEnv(req)

	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAuthTokenFromEnvFallsBackToLegacy(t *testing.T) {
	t.Setenv(GatewayAuthTokenEnv, " ")
	t.Setenv(LegacyGatewayAuthTokenEnv, " legacy-token ")
	if got := AuthTokenFromEnv(); got != "legacy-token" {
		t.Fatalf("AuthTokenFromEnv legacy = %q", got)
	}
}

func TestUnavailableHintIncludesRecoveryCommands(t *testing.T) {
	hint := UnavailableHint(":8765")
	for _, want := range []string{
		"gateway http://127.0.0.1:8765 is not reachable",
		"start it with ./bin/fast-agent-harness gateway",
		"systemctl restart billyharness-gateway.service",
		"systemctl --no-pager --full status billyharness-gateway.service",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint %q missing %q", hint, want)
		}
	}
}

func TestWaitForReadyProbesHealthEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	if !WaitForReady(context.Background(), server.URL, 20*time.Millisecond) {
		t.Fatal("WaitForReady should accept healthy server")
	}
	if len(paths) != 1 || paths[0] != "/health" {
		t.Fatalf("paths = %#v", paths)
	}
	if WaitForReady(context.Background(), "", 20*time.Millisecond) {
		t.Fatal("WaitForReady should reject empty base URL")
	}
}

func TestDoWithReadyRetryWrapsConnectionRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	client := &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "dial", URL: req.URL.String(), Err: syscall.ECONNREFUSED}
	})}

	_, err := DoWithReadyRetry(ctx, client, "127.0.0.1:1", func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/v1/mcp", nil)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %T %v, want UnavailableError", err, err)
	}
	if !strings.Contains(err.Error(), "./bin/fast-agent-harness gateway") ||
		!strings.Contains(err.Error(), "systemctl restart billyharness-gateway.service") {
		t.Fatalf("error does not include recovery commands: %v", err)
	}
}
