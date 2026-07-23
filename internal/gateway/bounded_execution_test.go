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

func TestBoundedExecutionRequestValidation(t *testing.T) {
	intPtr := func(value int) *int { return &value }
	tests := []struct {
		name string
		req  RunRequest
		want string
	}{
		{
			name: "isolated missing cap",
			req:  RunRequest{AccessMode: gatewayapi.AccessModeBoundedIsolatedPlanV1},
			want: "max_tool_calls is required",
		},
		{
			name: "isolated wrong cap",
			req: RunRequest{
				AccessMode:   gatewayapi.AccessModeBoundedIsolatedPlanV1,
				MaxToolCalls: intPtr(5),
			},
			want: "max_tool_calls must equal 4",
		},
		{
			name: "legacy wrong cap",
			req: RunRequest{
				AccessMode:   gatewayapi.AccessModeBoundedAutomationV1,
				MaxToolCalls: intPtr(4),
			},
			want: "max_tool_calls must equal 12",
		},
		{
			name: "ordinary zero cap",
			req:  RunRequest{MaxToolCalls: intPtr(0)},
			want: "max_tool_calls must be a positive integer",
		},
		{
			name: "ordinary negative cap",
			req:  RunRequest{MaxToolCalls: intPtr(-1)},
			want: "max_tool_calls must be a positive integer",
		},
		{
			name: "unknown contract version",
			req: RunRequest{
				AccessMode:   "bounded-automation-v2",
				MaxToolCalls: intPtr(12),
			},
			want: "unsupported access_mode",
		},
		{
			name: "bounded sentinel is case sensitive",
			req: RunRequest{
				AccessMode:   "BOUNDED-AUTOMATION-V1",
				MaxToolCalls: intPtr(12),
			},
			want: "unsupported access_mode",
		},
		{
			name: "bounded sentinel rejects whitespace",
			req: RunRequest{
				AccessMode:   " bounded-automation-v1",
				MaxToolCalls: intPtr(12),
			},
			want: "unsupported access_mode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRunExecutionLimits(tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}

	for _, req := range []RunRequest{
		{AccessMode: gatewayapi.AccessModeBoundedIsolatedPlanV1, MaxToolCalls: intPtr(4)},
		{AccessMode: gatewayapi.AccessModeBoundedAutomationV1, MaxToolCalls: intPtr(12)},
		{AccessMode: config.AccessModePlan, MaxToolCalls: intPtr(2)},
		{},
	} {
		if err := validateRunExecutionLimits(req); err != nil {
			t.Fatalf("valid request %#v rejected: %v", req, err)
		}
	}
}

func TestBoundedExecutionSettingsAreEffectiveAndScoped(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.AccessMode = config.AccessModeGuarded
	cfg.ProviderMaxRetries = 7
	cfg.ContextCompactStrategy = "model"
	cfg.WebSummaryMode = "model"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	legacyMax := 12
	legacy, err := server.runSettingsForRequest(context.Background(), RunRequest{
		AccessMode:   gatewayapi.AccessModeBoundedAutomationV1,
		MaxToolCalls: &legacyMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBoundedRunSettings(t, legacy, gatewayapi.AccessModeBoundedAutomationV1, 12)
	if legacy.toolPolicy.AccessMode != config.AccessModeGuarded {
		t.Fatalf("legacy access mode = %q, want configured guarded", legacy.toolPolicy.AccessMode)
	}

	isolatedMax := 4
	isolated, err := server.runSettingsForRequest(context.Background(), RunRequest{
		AccessMode:         gatewayapi.AccessModeBoundedIsolatedPlanV1,
		ContextMode:        gatewayapi.ContextModeIsolated,
		AllowedTools:       []string{"web_fetch"},
		AllowedURLPrefixes: []string{"https://example.com/api"},
		MaxToolCalls:       &isolatedMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBoundedRunSettings(t, isolated, gatewayapi.AccessModeBoundedIsolatedPlanV1, 4)
	if isolated.toolPolicy.AccessMode != config.AccessModePlan {
		t.Fatalf("isolated access mode = %q, want plan", isolated.toolPolicy.AccessMode)
	}

	ordinaryMax := 3
	ordinary, err := server.runSettingsForRequest(context.Background(), RunRequest{
		AccessMode:   config.AccessModePlan,
		MaxToolCalls: &ordinaryMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.runtime.MaxToolCalls != 3 {
		t.Fatalf("ordinary max tool calls = %d, want 3", ordinary.runtime.MaxToolCalls)
	}
	if ordinary.runtime.ProviderMaxRetries != 7 ||
		ordinary.provider.Limits.ProviderMaxRetries != 7 ||
		ordinary.runtime.ContextCompactStrategy != "model" ||
		ordinary.toolPolicy.WebSummaryMode != "model" ||
		ordinary.executionContract != nil {
		t.Fatalf("ordinary behavior changed: %#v", ordinary)
	}
}

func TestBoundedExecutionHTTPRejectsInvalidContractBeforeStreaming(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	for _, body := range []string{
		`{"prompt":"no cap","access_mode":"bounded-isolated-plan-v1"}`,
		`{"prompt":"wrong cap","access_mode":"bounded-automation-v1","max_tool_calls":4}`,
		`{"prompt":"unknown","access_mode":"bounded-automation-v2","max_tool_calls":12}`,
	} {
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewBufferString(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d body=%s request=%s", rec.Code, rec.Body.String(), body)
		}
		if strings.Contains(rec.Body.String(), string(protocol.EventRunStarted)) {
			t.Fatalf("invalid request started a run: %s", rec.Body.String())
		}
	}
}

func TestBoundedIsolatedCapabilitiesFailClosedAndDecodeStrictly(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	maxCalls := 4
	valid := RunRequest{
		Prompt:             "trail",
		AccessMode:         gatewayapi.AccessModeBoundedIsolatedPlanV1,
		ContextMode:        gatewayapi.ContextModeIsolated,
		AllowedTools:       []string{"web_fetch"},
		AllowedURLPrefixes: []string{"https://example.com/api"},
		MaxToolCalls:       &maxCalls,
	}
	tests := []struct {
		name string
		edit func(*RunRequest)
		want string
	}{
		{name: "missing context", edit: func(req *RunRequest) { req.ContextMode = "" }, want: "requires context_mode"},
		{name: "missing tools", edit: func(req *RunRequest) { req.AllowedTools = nil }, want: "non-empty allowed_tools"},
		{name: "missing URLs", edit: func(req *RunRequest) { req.AllowedURLPrefixes = nil }, want: "non-empty allowed_url_prefixes"},
		{name: "local tool", edit: func(req *RunRequest) { req.AllowedTools = []string{"fs_read_file", "web_fetch"} }, want: "not available in isolated scope"},
		{name: "profile override", edit: func(req *RunRequest) { req.Profile = "billy" }, want: "profile overrides"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			req.AllowedTools = append([]string(nil), valid.AllowedTools...)
			req.AllowedURLPrefixes = append([]string(nil), valid.AllowedURLPrefixes...)
			tt.edit(&req)
			if _, _, err := server.runCapabilitiesForRequest(req); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}

	_, _, err := decodeRunRequest(strings.NewReader(`{
		"prompt":"trail",
		"access_mode":"bounded-isolated-plan-v1",
		"context_mode":"isolated",
		"allowed_tools":["web_fetch"],
		"allowed_url_prefixes":["https://example.com/api"],
		"max_tool_calls":4,
		"future_field":true
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("constrained unknown-field error = %v", err)
	}
	if _, ok := config.ParseAccessMode(gatewayapi.AccessModeBoundedIsolatedPlanV1); ok {
		t.Fatalf("legacy access parser unexpectedly accepts %q", gatewayapi.AccessModeBoundedIsolatedPlanV1)
	}
}

func TestRunRequestBodyLimitAppliesOnlyToConstrainedRequests(t *testing.T) {
	cfg := config.BuiltIn()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	sessionID := createGatewaySessionForTest(t, server)
	padding := strings.Repeat("x", int(maxRunRequestBodyBytes))
	ordinaryBody := `{"padding":"` + padding + `"}`

	for _, path := range []string{
		"/v1/run",
		"/v1/sessions/" + sessionID + "/run",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(
				rec,
				httptest.NewRequest(http.MethodPost, path, strings.NewReader(ordinaryBody)),
			)
			if rec.Code != http.StatusBadRequest ||
				!strings.Contains(rec.Body.String(), "prompt or attachment required") {
				t.Fatalf("ordinary request status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	boundedBody := `{"prompt":"` + padding + `","access_mode":"` +
		gatewayapi.AccessModeBoundedAutomationV1 + `","max_tool_calls":12}`
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		rec,
		httptest.NewRequest(http.MethodPost, "/v1/run", strings.NewReader(boundedBody)),
	)
	if rec.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(rec.Body.String(), errRunRequestBodyTooLarge.Error()) {
		t.Fatalf("bounded request status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBoundedIsolatedCapabilitiesAreOneShotOnly(t *testing.T) {
	maxCalls := 4
	err := validateSessionRunCapabilityScopeWithPresence(RunRequest{
		AccessMode:         gatewayapi.AccessModeBoundedIsolatedPlanV1,
		ContextMode:        gatewayapi.ContextModeIsolated,
		AllowedTools:       []string{"web_fetch"},
		AllowedURLPrefixes: []string{"https://example.com/api"},
		MaxToolCalls:       &maxCalls,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "only by POST /v1/run") {
		t.Fatalf("session capability error = %v", err)
	}
}

func TestBoundedExecutionRunStartedWireAttestation(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.ProviderMaxRetries = 5
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	rec := httptest.NewRecorder()
	body := `{
		"prompt":"bounded",
		"access_mode":"bounded-isolated-plan-v1",
		"context_mode":"isolated",
		"allowed_tools":["web_fetch"],
		"allowed_url_prefixes":["https://example.com/api"],
		"max_tool_calls":4
	}`
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewBufferString(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var events []protocol.Event
	dec := json.NewDecoder(rec.Body)
	for dec.More() {
		var event protocol.Event
		if err := dec.Decode(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) == 0 || events[0].Type != protocol.EventRunStarted {
		t.Fatalf("first event = %#v", events)
	}
	data := mapFromEventData(events[0].Data)
	if len(data) != 7 ||
		data["submission_id"] == "" ||
		data["run_id"] == "" ||
		data["status"] == "" ||
		data["execution_contract"] != gatewayapi.AccessModeBoundedIsolatedPlanV1 ||
		data["provider_max_retries"] != float64(0) ||
		data["provider_failover_enabled"] != false ||
		data["max_tool_calls"] != float64(4) {
		t.Fatalf("run.started data = %#v", data)
	}
	modelIndex := -1
	for index, event := range events {
		if event.Type == protocol.EventModelCallStarted {
			modelIndex = index
			break
		}
	}
	if modelIndex <= 0 {
		t.Fatalf("model event index = %d events=%#v", modelIndex, events)
	}
	var started protocol.ModelCallEvent
	bodyBytes, err := json.Marshal(events[modelIndex].Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bodyBytes, &started); err != nil {
		t.Fatal(err)
	}
	if started.AccessMode != config.AccessModePlan ||
		started.CapabilityScope != gatewayapi.AccessModeBoundedIsolatedPlanV1 ||
		started.ContextMode != gatewayapi.ContextModeIsolated ||
		started.AllowedToolsCount != 1 ||
		started.AllowedToolsSHA256 == "" ||
		started.AllowedURLPrefixesCount != 1 ||
		started.AllowedURLPrefixesSHA256 == "" {
		t.Fatalf("model.call_started bounded capability = %#v", started)
	}
}

func assertBoundedRunSettings(t *testing.T, settings runSettings, contract string, maxToolCalls int) {
	t.Helper()
	if settings.runtime.ProviderMaxRetries != 0 ||
		settings.provider.Limits.ProviderMaxRetries != 0 ||
		settings.runtime.MaxToolCalls != maxToolCalls ||
		settings.runtime.ContextCompactStrategy != "deterministic" ||
		settings.toolPolicy.WebSummaryMode != "extractive" {
		t.Fatalf("bounded effective settings = %#v", settings)
	}
	if settings.executionContract == nil ||
		settings.executionContract.ExecutionContract != contract ||
		settings.executionContract.ProviderMaxRetries != 0 ||
		settings.executionContract.ProviderFailoverEnabled ||
		settings.executionContract.MaxToolCalls != maxToolCalls {
		t.Fatalf("execution contract = %#v", settings.executionContract)
	}
}
