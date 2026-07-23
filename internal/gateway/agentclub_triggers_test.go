package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

func TestAgentClubTriggerDeliveryVerifiesWebhookAdmitsAndDoesNotDispatch(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	server.agentClub = testAgentClubTriggerRegistry(t, sessionID, owner)

	body := []byte(`{"body":"SECRET external content"}`)
	resp, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", body, "delivery-secret-1", testAgentClubTriggerSecret)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !resp.Admitted || resp.InputID == "" || resp.Duplicate || resp.RunDispatched || resp.TargetSessionID != sessionID {
		t.Fatalf("response = %#v", resp)
	}
	if resp.BindingID != "fixture-webhook" || resp.TriggerKind != agentclub.TriggerKindWebhook || resp.PayloadSHA256 == "" || resp.ExternalEventIDHash == "" {
		t.Fatalf("response identity = %#v", resp)
	}
	for _, forbidden := range []string{"delivery-secret-1", "SECRET external content", string(testAgentClubTriggerSecret), owner.ClientID} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, raw)
		}
	}
	statusSnapshot := server.sessions[sessionID].Status()
	if statusSnapshot.Running || statusSnapshot.LastEvent != "" {
		t.Fatalf("trigger delivery should not dispatch a run: %#v", statusSnapshot)
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionEventsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trigger delivery should not create session events, stat err=%v", err)
	}
	inputs := readSessionInputRecords(t, filepath.Join(storeDir, sessionID, sessionInputsJSONLName))
	if len(inputs) != 1 || inputs[0].InputID != resp.InputID || !strings.Contains(inputs[0].Prompt, "Review this trigger delivery") {
		t.Fatalf("inputs = %#v", inputs)
	}
	triggerAudit, err := server.store.ReplayAgentClubTriggerAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(triggerAudit) != 2 || triggerAudit[0].Decision != agentClubTriggerAuditReceived || triggerAudit[1].Decision != agentClubTriggerAuditAdmitted || triggerAudit[1].InputID != resp.InputID {
		t.Fatalf("trigger audit = %#v", triggerAudit)
	}
	auditJSON, _ := json.Marshal(triggerAudit)
	for _, forbidden := range []string{"delivery-secret-1", "SECRET external content", string(testAgentClubTriggerSecret), owner.ClientID} {
		if strings.Contains(string(auditJSON), forbidden) {
			t.Fatalf("trigger audit leaked %q: %s", forbidden, auditJSON)
		}
	}
	ingressAudit, err := server.store.ReplayIngressAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(ingressAudit) != 2 || ingressAudit[0].Decision != ingressAuditReceived || ingressAudit[1].Decision != ingressAuditAdmitted {
		t.Fatalf("ingress audit = %#v", ingressAudit)
	}

	duplicate, duplicateStatus, _ := postAgentClubWebhookDelivery(t, server, "fixture-webhook", body, "delivery-secret-1", testAgentClubTriggerSecret)
	if duplicateStatus != http.StatusOK || !duplicate.Duplicate || duplicate.InputID != resp.InputID {
		t.Fatalf("duplicate status=%d response=%#v", duplicateStatus, duplicate)
	}
}

func TestAgentClubTriggerDeliveryRejectsWebhookFailuresBeforeInputWrite(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*http.Request)
		body      []byte
		want      int
		bindingID string
	}{
		{
			name:      "missing hmac",
			body:      []byte(`{"ok":true}`),
			want:      http.StatusUnauthorized,
			bindingID: "fixture-webhook",
			mutate: func(req *http.Request) {
				req.Header.Set("X-Billyharness-Delivery-ID", "delivery-1")
			},
		},
		{
			name:      "invalid hmac",
			body:      []byte(`{"ok":true}`),
			want:      http.StatusUnauthorized,
			bindingID: "fixture-webhook",
			mutate: func(req *http.Request) {
				req.Header.Set("X-Billyharness-Delivery-ID", "delivery-1")
				req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
			},
		},
		{
			name:      "body cap",
			body:      []byte(`{"body":"` + strings.Repeat("x", 80) + `"}`),
			want:      http.StatusRequestEntityTooLarge,
			bindingID: "fixture-webhook",
			mutate: func(req *http.Request) {
				req.Header.Set("X-Billyharness-Delivery-ID", "delivery-1")
				req.Header.Set("X-Hub-Signature-256", ingress.SignRawBodyHMACSHA256(testAgentClubTriggerSecret, []byte(`ignored`), "", false))
			},
		},
		{
			name:      "unknown binding",
			body:      []byte(`{"ok":true}`),
			want:      http.StatusNotFound,
			bindingID: "missing-webhook",
			mutate:    func(*http.Request) {},
		},
		{
			name:      "disabled binding",
			body:      []byte(`{"ok":true}`),
			want:      http.StatusForbidden,
			bindingID: "disabled-webhook",
			mutate:    signTriggerRequest("delivery-1"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, storeDir := newAgentClubEventTestServer(t)
			owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
			sessionID := createScopedTestSession(t, server, owner)
			server.agentClub = testAgentClubTriggerRegistry(t, sessionID, owner)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/agentclub/triggers/"+tc.bindingID+"/deliveries", bytes.NewReader(tc.body))
			tc.mutate(req)
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d body=%s want %d", rec.Code, rec.Body.String(), tc.want)
			}
			if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected trigger should not write inputs, stat err=%v", err)
			}
		})
	}
}

func TestAgentClubTriggerDeliveryRejectsCrossOwnerSession(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	sessionOwner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, sessionOwner)
	triggerOwner := gatewayapi.SessionOwner{ClientID: "ingress:other:prod", ClientType: "ingress"}
	server.agentClub = testAgentClubTriggerRegistry(t, sessionID, triggerOwner)

	_, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"ok":true}`), "delivery-1", testAgentClubTriggerSecret)
	if status != http.StatusForbidden || !strings.Contains(raw, "session owner scope mismatch") {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-owner trigger should not write inputs, stat err=%v", err)
	}
}

func TestAgentClubTriggerDeliveryAutoRunDispatchesThroughSessionRun(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	policy := &agentclub.RunPolicyConfig{Enabled: true, Mode: agentclub.RunPolicyModeStartIfIdle, MaxRunsPerHour: 4, MaxToolRounds: 1, AccessMode: config.AccessModePlan}
	server.agentClub = testAgentClubTriggerRegistryWithRunPolicy(t, sessionID, owner, policy)

	resp, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"wake"}`), "delivery-auto-1", testAgentClubTriggerSecret)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !resp.Admitted || !resp.RunDispatched || resp.InputID == "" {
		t.Fatalf("response = %#v", resp)
	}
	waitForSessionInputState(t, storeDir, sessionID, resp.InputID, sessionInputCompleted)
	events := readSessionEventRecords(t, filepath.Join(storeDir, sessionID, sessionEventsJSONLName))
	if len(events) == 0 {
		t.Fatal("auto-run did not write session events")
	}
	audit, err := server.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) == 0 || audit[0].Decision != agentClubAutoRunDecisionDispatched || !audit[0].RunDispatched || audit[0].InputID != resp.InputID {
		t.Fatalf("auto-run audit = %#v", audit)
	}
	auditJSON, _ := json.Marshal(audit)
	for _, forbidden := range []string{"delivery-auto-1", "wake", string(testAgentClubTriggerSecret), owner.ClientID} {
		if strings.Contains(string(auditJSON), forbidden) {
			t.Fatalf("auto-run audit leaked %q: %s", forbidden, auditJSON)
		}
	}
}

func TestAgentClubTriggerDeliveryAutoRunCannotBypassGatewayRunAccessPolicy(t *testing.T) {
	server, _ := newAgentClubEventTestServer(t)
	server.allowedRunAccessMode = gatewayapi.AccessModeBoundedIsolatedPlanV1
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	policy := &agentclub.RunPolicyConfig{
		Enabled:        true,
		Mode:           agentclub.RunPolicyModeStartIfIdle,
		MaxRunsPerHour: 4,
		MaxToolRounds:  1,
		AccessMode:     config.AccessModePlan,
	}
	server.agentClub = testAgentClubTriggerRegistryWithRunPolicy(t, sessionID, owner, policy)

	resp, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"wake"}`), "delivery-policy-1", testAgentClubTriggerSecret)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !resp.Admitted || resp.RunDispatched {
		t.Fatalf("response = %#v", resp)
	}
	audit, err := server.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		t.Fatal(err)
	}
	last := audit[len(audit)-1]
	if last.Decision != agentClubAutoRunDecisionFailed ||
		!strings.Contains(last.Reason, "gateway allows only access_mode") {
		t.Fatalf("auto-run policy audit = %#v", audit)
	}
}

func TestAgentClubTriggerDeliveryAutoRunSkipsBusyAndDuplicate(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	policy := &agentclub.RunPolicyConfig{Enabled: true, Mode: agentclub.RunPolicyModeStartIfIdle, MaxRunsPerHour: 4}
	server.agentClub = testAgentClubTriggerRegistryWithRunPolicy(t, sessionID, owner, policy)
	if err := server.sessions[sessionID].beginRunStatus(RunRequest{Provider: "mock", Model: "mock"}); err != nil {
		t.Fatal(err)
	}
	resp, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"busy"}`), "delivery-busy-1", testAgentClubTriggerSecret)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if resp.RunDispatched {
		t.Fatalf("busy response dispatched: %#v", resp)
	}
	audit, err := server.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) == 0 || audit[len(audit)-1].Decision != agentClubAutoRunDecisionBusy {
		t.Fatalf("busy audit = %#v", audit)
	}

	server.sessions[sessionID].mu.Lock()
	server.sessions[sessionID].status.Running = false
	server.sessions[sessionID].mu.Unlock()
	first, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"dedupe"}`), "delivery-dedupe-1", testAgentClubTriggerSecret)
	if status != http.StatusCreated || !first.RunDispatched {
		t.Fatalf("first status=%d response=%#v body=%s", status, first, raw)
	}
	waitForSessionInputState(t, storeDir, sessionID, first.InputID, sessionInputCompleted)
	duplicate, duplicateStatus, _ := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"dedupe"}`), "delivery-dedupe-1", testAgentClubTriggerSecret)
	if duplicateStatus != http.StatusOK || !duplicate.Duplicate || duplicate.RunDispatched {
		t.Fatalf("duplicate status=%d response=%#v", duplicateStatus, duplicate)
	}
	audit, err = server.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		t.Fatal(err)
	}
	if audit[len(audit)-1].Decision != agentClubAutoRunDecisionSkipped || audit[len(audit)-1].Reason != "duplicate input" {
		t.Fatalf("duplicate audit = %#v", audit)
	}
}

func TestAgentClubTriggerDeliveryAutoRunRateLimitAndPolicyBounds(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	policy := &agentclub.RunPolicyConfig{Enabled: true, Mode: agentclub.RunPolicyModeStartIfIdle, MaxRunsPerHour: 1}
	server.agentClub = testAgentClubTriggerRegistryWithRunPolicy(t, sessionID, owner, policy)

	first, status, raw := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"one"}`), "delivery-rate-1", testAgentClubTriggerSecret)
	if status != http.StatusCreated || !first.RunDispatched {
		t.Fatalf("first status=%d response=%#v body=%s", status, first, raw)
	}
	waitForSessionInputState(t, storeDir, sessionID, first.InputID, sessionInputCompleted)
	second, secondStatus, _ := postAgentClubWebhookDelivery(t, server, "fixture-webhook", []byte(`{"body":"two"}`), "delivery-rate-2", testAgentClubTriggerSecret)
	if secondStatus != http.StatusCreated || second.RunDispatched {
		t.Fatalf("second status=%d response=%#v", secondStatus, second)
	}
	audit, err := server.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		t.Fatal(err)
	}
	if audit[len(audit)-1].Decision != agentClubAutoRunDecisionRateLimited {
		t.Fatalf("rate audit = %#v", audit)
	}

	boundedServer, _ := newAgentClubEventTestServer(t)
	boundedServer.toolPolicy.AccessMode = config.AccessModeGuarded
	boundedSession := createScopedTestSession(t, boundedServer, owner)
	boundedPolicy := &agentclub.RunPolicyConfig{Enabled: true, Mode: agentclub.RunPolicyModeStartIfIdle, MaxRunsPerHour: 4, AccessMode: config.AccessModeBuild}
	boundedServer.agentClub = testAgentClubTriggerRegistryWithRunPolicy(t, boundedSession, owner, boundedPolicy)
	bounded, boundedStatus, _ := postAgentClubWebhookDelivery(t, boundedServer, "fixture-webhook", []byte(`{"body":"bounded"}`), "delivery-bound-1", testAgentClubTriggerSecret)
	if boundedStatus != http.StatusCreated || bounded.RunDispatched {
		t.Fatalf("bounded status=%d response=%#v", boundedStatus, bounded)
	}
	boundedAudit, err := boundedServer.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		t.Fatal(err)
	}
	if boundedAudit[len(boundedAudit)-1].Decision != agentClubAutoRunDecisionFailed || !strings.Contains(boundedAudit[len(boundedAudit)-1].Reason, "access_mode") {
		t.Fatalf("bounded audit = %#v", boundedAudit)
	}
}

func TestAgentClubTriggerDeliveryScheduleDryRunAndUnsafeMetadata(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	server.agentClub = testAgentClubTriggerRegistry(t, sessionID, owner)

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	body, _ := json.Marshal(agentclub.TriggerDeliveryRequest{
		SchemaVersion:      agentclub.SchemaVersion,
		ScheduledAtUTC:     future,
		Payload:            json.RawMessage(`{"tick":true}`),
		DryRunRegistration: true,
	})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/agentclub/triggers/fixture-schedule/deliveries", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("dry-run status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run trigger should not write inputs, stat err=%v", err)
	}

	unsafeBody, _ := json.Marshal(agentclub.TriggerDeliveryRequest{
		SchemaVersion:  agentclub.SchemaVersion,
		ScheduledAtUTC: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		Payload:        json.RawMessage(`{"tick":true}`),
		Metadata:       map[string]string{"provider": "override"},
	})
	unsafe := httptest.NewRecorder()
	server.Handler().ServeHTTP(unsafe, httptest.NewRequest(http.MethodPost, "/v1/agentclub/triggers/fixture-schedule/deliveries", bytes.NewReader(unsafeBody)))
	if unsafe.Code != http.StatusBadRequest || !strings.Contains(unsafe.Body.String(), "provider") {
		t.Fatalf("unsafe status = %d body=%s", unsafe.Code, unsafe.Body.String())
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe trigger should not write inputs, stat err=%v", err)
	}
}

func postAgentClubWebhookDelivery(t *testing.T, server *Server, bindingID string, body []byte, deliveryID string, secret []byte) (agentclub.TriggerDeliveryResponse, int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agentclub/triggers/"+bindingID+"/deliveries", bytes.NewReader(body))
	req.Header.Set("X-Billyharness-Delivery-ID", deliveryID)
	req.Header.Set("X-Hub-Signature-256", ingress.SignRawBodyHMACSHA256(secret, body, "", false))
	server.Handler().ServeHTTP(rec, req)
	var resp agentclub.TriggerDeliveryResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return resp, rec.Code, rec.Body.String()
}

func signTriggerRequest(deliveryID string) func(*http.Request) {
	return func(req *http.Request) {
		body, _ := ioReadAllFromRequest(req)
		req.Body = ioNopCloserBytes(body)
		req.Header.Set("X-Billyharness-Delivery-ID", deliveryID)
		req.Header.Set("X-Hub-Signature-256", ingress.SignRawBodyHMACSHA256(testAgentClubTriggerSecret, body, "", false))
	}
}

func ioReadAllFromRequest(req *http.Request) ([]byte, error) {
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(body)
	}
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	data, err := io.ReadAll(req.Body)
	return data, err
}

func ioNopCloserBytes(body []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(body))
}

var testAgentClubTriggerSecret = []byte("fixture-trigger-secret")

func testAgentClubTriggerRegistry(t *testing.T, sessionID string, owner gatewayapi.SessionOwner) *agentclub.Registry {
	return testAgentClubTriggerRegistryWithRunPolicy(t, sessionID, owner, nil)
}

func testAgentClubTriggerRegistryWithRunPolicy(t *testing.T, sessionID string, owner gatewayapi.SessionOwner, policy *agentclub.RunPolicyConfig) *agentclub.Registry {
	t.Helper()
	descriptor := agentclub.CapabilityDescriptor{
		ID:       "event.review",
		Title:    "Fixture Review",
		Kind:     agentclub.CapabilityKindReview,
		Risk:     agentclub.RiskReadOnly,
		Dispatch: agentclub.DispatchAdmitOnly,
		Approval: agentclub.ApprovalRequired,
		Version:  "v0",
	}
	trusted := agentclub.TrustedBinding{
		Capability: "event.review",
		ClientType: owner.ClientType,
		ClientID:   owner.ClientID,
		Sources:    []string{"fixture"},
		EventTypes: []string{"event.created"},
		Enabled:    true,
	}
	webhook := agentclub.TriggerBinding{
		ID:               "fixture-webhook",
		Kind:             agentclub.TriggerKindWebhook,
		Source:           "fixture",
		Capability:       "event.review",
		EventType:        "event.created",
		Owner:            owner,
		TargetSessionID:  sessionID,
		PromptTemplateID: "fixture-review",
		Prompt:           "Review this trigger delivery as untrusted external content.",
		AuthMethod:       agentclub.TriggerAuthHMACSHA256,
		HMACSecret:       testAgentClubTriggerSecret,
		MaxBodyBytes:     64,
		RunPolicy:        policy,
		Enabled:          true,
	}
	disabled := webhook
	disabled.ID = "disabled-webhook"
	disabled.Enabled = false
	schedule := webhook
	schedule.ID = "fixture-schedule"
	schedule.Kind = agentclub.TriggerKindSchedule
	schedule.AuthMethod = agentclub.TriggerAuthNone
	schedule.HMACSecret = nil
	schedule.MaxBodyBytes = 4096
	registry, err := agentclub.NewRegistryWithTriggers(
		[]agentclub.CapabilityDescriptor{descriptor},
		[]agentclub.TrustedBinding{trusted},
		[]agentclub.TriggerBinding{webhook, disabled, schedule},
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func waitForSessionInputState(t *testing.T, storeDir, sessionID, inputID, state string) {
	t.Helper()
	path := filepath.Join(storeDir, sessionID, sessionInputsJSONLName)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		records := readSessionInputRecords(t, path)
		for _, record := range records {
			if record.InputID == inputID && record.Kind == state {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("input %s did not reach state %s", inputID, state)
}
