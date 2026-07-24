package gatewayapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	isolateGatewayAuth(t)
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
	isolateGatewayAuth(t)
	t.Setenv(GatewayAuthTokenEnv, "test-token")

	req := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/mcp", nil)
	SetAuthHeaderFromEnv(req)

	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAuthTokenFromEnvFallsBackToLegacy(t *testing.T) {
	isolateGatewayAuth(t)
	t.Setenv(GatewayAuthTokenEnv, " ")
	t.Setenv(LegacyGatewayAuthTokenEnv, " legacy-token ")
	if got := AuthTokenFromEnv(); got != "legacy-token" {
		t.Fatalf("AuthTokenFromEnv legacy = %q", got)
	}
}

func TestAuthTokenFromEnvLoadsSharedBillyharnessDotenv(t *testing.T) {
	home := isolateGatewayAuth(t)
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte(GatewayAuthTokenEnv+"=shared-dotenv-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := AuthTokenFromEnv(); got != "shared-dotenv-token" {
		t.Fatalf("AuthTokenFromEnv did not load the shared Billyharness dotenv token")
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8765/v1/jobs", nil)
	SetAuthHeaderFromEnv(req)
	if got := req.Header.Get("Authorization"); got != "Bearer shared-dotenv-token" {
		t.Fatalf("Authorization header did not use the shared Billyharness dotenv token")
	}
}

func TestSetAuthHeaderFromDefaultUsesDedicatedTokenForLoopbackURLs(t *testing.T) {
	home := isolateGatewayAuth(t)
	writeDedicatedGatewayToken(t, home, "local-dedicated-token")

	for _, rawURL := range []string{
		"http://localhost:8765/v1/jobs",
		"http://127.42.8.9:8765/v1/jobs",
		"http://[::1]:8765/v1/jobs",
	} {
		t.Run(rawURL, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, rawURL, nil)
			if err := SetAuthHeaderFromDefault(req); err != nil {
				t.Fatalf("SetAuthHeaderFromDefault: %v", err)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer local-dedicated-token" {
				t.Fatalf("Authorization = %q", got)
			}
		})
	}
}

func TestSetAuthHeaderFromDefaultDoesNotUseDedicatedTokenForRemoteURL(t *testing.T) {
	home := isolateGatewayAuth(t)
	writeDedicatedGatewayToken(t, home, "local-only-token")

	req := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/jobs", nil)
	if err := SetAuthHeaderFromDefault(req); err != nil {
		t.Fatalf("SetAuthHeaderFromDefault: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("remote Authorization = %q, want empty", got)
	}
}

func TestSetAuthHeaderFromDefaultUsesPrimaryProcessEnvForRemoteURL(t *testing.T) {
	isolateGatewayAuth(t)
	t.Setenv(GatewayAuthTokenEnv, " remote-explicit-token ")

	req := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/jobs", nil)
	if err := SetAuthHeaderFromDefault(req); err != nil {
		t.Fatalf("SetAuthHeaderFromDefault: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer remote-explicit-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestSetAuthHeaderFromDefaultUsesLegacyProcessEnvForRemoteURL(t *testing.T) {
	isolateGatewayAuth(t)
	t.Setenv(LegacyGatewayAuthTokenEnv, " remote-legacy-token ")

	req := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/jobs", nil)
	if err := SetAuthHeaderFromDefault(req); err != nil {
		t.Fatalf("SetAuthHeaderFromDefault: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer remote-legacy-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestSetAuthHeaderFromDefaultPreservesExistingHeaderWithoutResolvingStore(t *testing.T) {
	home := isolateGatewayAuth(t)
	writeDedicatedGatewayToken(t, home, "secret\ncorrupt")

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/v1/jobs", nil)
	req.Header.Set("Authorization", "Basic caller-owned")
	if err := SetAuthHeaderFromDefault(req); err != nil {
		t.Fatalf("pre-set Authorization should bypass token resolution: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Basic caller-owned" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestSetAuthHeaderFromDefaultRejectsInvalidProcessEnvWithoutLeakingIt(t *testing.T) {
	isolateGatewayAuth(t)
	const secretPrefix = "do-not-leak-this-secret"
	t.Setenv(GatewayAuthTokenEnv, secretPrefix+"\ncorrupt")

	req := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/jobs", nil)
	err := SetAuthHeaderFromDefault(req)
	if err == nil {
		t.Fatal("expected invalid process token error")
	}
	if strings.Contains(err.Error(), secretPrefix) {
		t.Fatalf("error leaked token material: %v", err)
	}
	if !strings.Contains(err.Error(), GatewayAuthTokenEnv) {
		t.Fatalf("error does not identify the invalid source: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
}

func TestSetAuthHeaderFromDefaultIgnoresProjectAndExplicitDotenvFiles(t *testing.T) {
	isolateGatewayAuth(t)
	project := t.TempDir()
	projectDotenv := filepath.Join(project, ".env")
	explicitDotenv := filepath.Join(t.TempDir(), "gateway.env")
	for path, token := range map[string]string{
		projectDotenv:  "project-token",
		explicitDotenv: "explicit-file-token",
	} {
		if err := os.WriteFile(path, []byte(GatewayAuthTokenEnv+"="+token+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAST_AGENT_ENV_FILE", explicitDotenv)
	t.Chdir(project)

	for _, rawURL := range []string{
		"http://127.0.0.1:8765/v1/jobs",
		"https://gateway.example/v1/jobs",
	} {
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		if err := SetAuthHeaderFromDefault(req); err != nil {
			t.Fatalf("SetAuthHeaderFromDefault(%s): %v", rawURL, err)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization for %s = %q, want empty", rawURL, got)
		}
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

func isolateGatewayAuth(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "1")
	t.Setenv(GatewayAuthTokenEnv, "")
	t.Setenv(LegacyGatewayAuthTokenEnv, "")
	return home
}

func writeDedicatedGatewayToken(t *testing.T, home, token string) {
	t.Helper()
	authDir := filepath.Join(home, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "gateway.token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
