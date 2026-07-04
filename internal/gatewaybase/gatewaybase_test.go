package gatewaybase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBaseURLCanonicalizesLocalBinds(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		":8765":                    "http://127.0.0.1:8765",
		"127.0.0.1:8765/":          "http://127.0.0.1:8765",
		"http://0.0.0.0:8765/path": "http://127.0.0.1:8765/path",
		"http://[::]:8765":         "http://127.0.0.1:8765",
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

func TestAuthTokenFromEnvFallsBackToLegacy(t *testing.T) {
	t.Setenv(GatewayAuthTokenEnv, " ")
	t.Setenv(LegacyGatewayAuthTokenEnv, " legacy-token ")
	if got := AuthTokenFromEnv(); got != "legacy-token" {
		t.Fatalf("AuthTokenFromEnv legacy = %q", got)
	}
}

func TestUnavailableHintNamesServiceAndNormalizedURL(t *testing.T) {
	hint := UnavailableHint(":8765")
	for _, want := range []string{
		"gateway http://127.0.0.1:8765 is not reachable",
		"start it with ./bin/fast-agent-harness gateway",
		"systemctl restart billyharness-gateway.service",
		"systemctl --no-pager --full status billyharness-gateway.service",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q:\n%s", want, hint)
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
