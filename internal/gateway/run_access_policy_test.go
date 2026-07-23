package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestParseAllowedRunAccessMode(t *testing.T) {
	for _, input := range []string{"", " \t\n"} {
		got, err := ParseAllowedRunAccessMode(input)
		if err != nil || got != "" {
			t.Fatalf("ParseAllowedRunAccessMode(%q) = %q, %v; want empty", input, got, err)
		}
	}

	got, err := ParseAllowedRunAccessMode("  " + gatewayapi.AccessModeBoundedIsolatedPlanV1 + " ")
	if err != nil || got != gatewayapi.AccessModeBoundedIsolatedPlanV1 {
		t.Fatalf("isolated policy = %q, %v", got, err)
	}

	for _, input := range []string{
		gatewayapi.AccessModeBoundedAutomationV1,
		gatewayapi.AccessModeIsolatedPlanV1,
		config.AccessModePlan,
		"BOUNDED-ISOLATED-PLAN-V1",
	} {
		if _, err := ParseAllowedRunAccessMode(input); err == nil ||
			!strings.Contains(err.Error(), GatewayAllowedRunAccessModeEnv) {
			t.Fatalf("ParseAllowedRunAccessMode(%q) error = %v", input, err)
		}
	}
}

func TestGatewayAllowedRunAccessModeRejectsAuthenticatedBroaderRunsBeforeStreaming(t *testing.T) {
	server := newRestrictedRunTestServer(t, gatewayapi.AccessModeBoundedIsolatedPlanV1)

	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "ordinary unbounded",
			body: `{"prompt":"do work","access_mode":"plan"}`,
		},
		{
			name: "bounded automation",
			body: `{"prompt":"do work","access_mode":"bounded-automation-v1","max_tool_calls":12}`,
		},
		{
			name: "missing access mode",
			body: `{"prompt":"do work"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := authenticatedRunRequest(server, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `gateway allows only access_mode \"bounded-isolated-plan-v1\"`) {
				t.Fatalf("policy denial body = %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), string(protocol.EventRunStarted)) ||
				strings.Contains(rec.Body.String(), string(protocol.EventModelCallStarted)) {
				t.Fatalf("rejected request started provider execution: %s", rec.Body.String())
			}
		})
	}
}

func TestGatewayAllowedRunAccessModeAcceptsAuthenticatedBoundedIsolatedRun(t *testing.T) {
	server := newRestrictedRunTestServer(t, gatewayapi.AccessModeBoundedIsolatedPlanV1)
	rec := authenticatedRunRequest(server, `{
		"prompt":"summarize the allowed page",
		"access_mode":"bounded-isolated-plan-v1",
		"context_mode":"isolated",
		"allowed_tools":["web_fetch"],
		"allowed_url_prefixes":["https://example.com/reference"],
		"max_tool_calls":4
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), string(protocol.EventRunStarted)) ||
		!strings.Contains(rec.Body.String(), `"execution_contract":"bounded-isolated-plan-v1"`) {
		t.Fatalf("accepted run missing isolated attestation: %s", rec.Body.String())
	}
}

func TestGatewayAllowedRunAccessModeIsEnforcedByInternalRunSettings(t *testing.T) {
	server := newRestrictedRunTestServer(t, gatewayapi.AccessModeBoundedIsolatedPlanV1)
	if _, err := server.runSettingsForRequest(context.Background(), RunRequest{AccessMode: config.AccessModePlan}); err == nil ||
		!strings.Contains(err.Error(), "gateway allows only access_mode") {
		t.Fatalf("internal run policy error = %v", err)
	}
}

func TestGatewayRunAccessPolicyIsPublicAndInvalidOptionsFailReadiness(t *testing.T) {
	restricted := newRestrictedRunTestServer(t, gatewayapi.AccessModeBoundedIsolatedPlanV1)
	health := httptest.NewRecorder()
	restricted.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", health.Code, health.Body.String())
	}
	var healthResponse HealthResponse
	if err := json.Unmarshal(health.Body.Bytes(), &healthResponse); err != nil {
		t.Fatal(err)
	}
	if healthResponse.AllowedRunAccessMode != gatewayapi.AccessModeBoundedIsolatedPlanV1 {
		t.Fatalf("health policy = %#v", healthResponse)
	}

	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	invalid := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{
		AllowedRunAccessMode: "bounded-automation-v1",
	})
	ready := httptest.NewRecorder()
	invalid.Handler().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid policy readiness status = %d body=%s", ready.Code, ready.Body.String())
	}
	if !strings.Contains(ready.Body.String(), `"name":"run_access_policy"`) ||
		!strings.Contains(ready.Body.String(), `"status":"fail"`) {
		t.Fatalf("invalid policy readiness = %s", ready.Body.String())
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewBufferString(`{"prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	invalid.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "gateway run access policy is invalid") {
		t.Fatalf("invalid policy run status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func newRestrictedRunTestServer(t *testing.T, allowedAccessMode string) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	return NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{
		AuthToken:            "secret",
		RequireMutationAuth:  true,
		AllowedRunAccessMode: allowedAccessMode,
	})
}

func authenticatedRunRequest(server *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)
	return rec
}
