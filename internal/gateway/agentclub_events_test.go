package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestAgentClubEventRouteAdmitsInputAuditsAndDoesNotDispatch(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)

	req := agentclub.EventRequest{
		SchemaVersion:   agentclub.SchemaVersion,
		Source:          "fixture",
		Capability:      "pull_request.review",
		EventType:       "pull_request.opened",
		ExternalEventID: "delivery-secret-1",
		Prompt:          "Review this fixture event containing SECRET prompt text.",
		Payload:         json.RawMessage(`{"body":"SECRET payload text"}`),
		Metadata: map[string]string{
			"project": "fixture-secret-project",
		},
	}
	resp, status, raw := postAgentClubEvent(t, server, sessionID, owner, req)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !resp.Admitted || resp.InputID == "" || resp.State != sessionInputAdmitted || resp.Duplicate || resp.RunDispatched {
		t.Fatalf("response = %#v", resp)
	}
	if resp.TargetSessionID != sessionID || resp.Source != "fixture" || resp.Capability != "pull_request.review" || resp.EventType != "pull_request.opened" {
		t.Fatalf("response identity = %#v", resp)
	}
	if resp.PayloadSHA256 == "" || resp.ExternalEventIDHash == "" || !hasString(resp.MetadataKeys, "project") || !hasString(resp.MetadataKeys, "agentclub.capability") {
		t.Fatalf("response hashes/metadata = %#v", resp)
	}
	for _, forbidden := range []string{owner.ClientID, "delivery-secret-1", "SECRET prompt text", "SECRET payload text", "fixture-secret-project"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, raw)
		}
	}

	statusSnapshot := server.sessions[sessionID].Status()
	if statusSnapshot.Running || statusSnapshot.LastEvent != "" {
		t.Fatalf("agentclub event admission should not dispatch a run: %#v", statusSnapshot)
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionEventsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agentclub event admission should not create session events, stat err=%v", err)
	}
	inputs := readSessionInputRecords(t, filepath.Join(storeDir, sessionID, sessionInputsJSONLName))
	if len(inputs) != 1 || inputs[0].InputID != resp.InputID || !strings.Contains(inputs[0].Prompt, "SECRET prompt text") {
		t.Fatalf("inputs = %#v", inputs)
	}
	audit, err := server.store.ReplayIngressAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 || audit[0].Decision != ingressAuditReceived || audit[1].Decision != ingressAuditAdmitted || audit[1].InputID != resp.InputID {
		t.Fatalf("audit = %#v", audit)
	}
	auditJSON, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{owner.ClientID, "delivery-secret-1", "SECRET prompt text", "SECRET payload text", "fixture-secret-project"} {
		if strings.Contains(string(auditJSON), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, auditJSON)
		}
	}

	duplicate, duplicateStatus, _ := postAgentClubEvent(t, server, sessionID, owner, req)
	if duplicateStatus != http.StatusOK || !duplicate.Duplicate || duplicate.InputID != resp.InputID {
		t.Fatalf("duplicate status=%d response=%#v", duplicateStatus, duplicate)
	}
}

func TestAgentClubEventRouteRequiresIngressOwnerHeaders(t *testing.T) {
	server, _ := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	req := validAgentClubEventRequest()
	body, _ := json.Marshal(req)

	missing := httptest.NewRecorder()
	server.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/events", bytes.NewReader(body)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing owner status = %d body=%s", missing.Code, missing.Body.String())
	}

	telegram := httptest.NewRecorder()
	telegramReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/events", bytes.NewReader(body))
	setScopedTestOwnerHeaders(telegramReq, gatewayapi.SessionOwner{ClientID: "telegram:1", ClientType: "telegram"})
	server.Handler().ServeHTTP(telegram, telegramReq)
	if telegram.Code != http.StatusForbidden {
		t.Fatalf("non-ingress owner status = %d body=%s", telegram.Code, telegram.Body.String())
	}
}

func TestAgentClubEventRouteDeniesCrossOwnerBeforeInputWrite(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	sessionOwner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, sessionOwner)
	actor := gatewayapi.SessionOwner{ClientID: "ingress:other:prod", ClientType: "ingress"}

	body, _ := json.Marshal(validAgentClubEventRequest())
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/events", bytes.NewReader(body))
	setScopedTestOwnerHeaders(httpReq, actor)
	server.Handler().ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross owner status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-owner route should not write inputs, stat err=%v", err)
	}
}

func TestAgentClubEventRouteRejectsUnsafeMetadata(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	req := validAgentClubEventRequest()
	req.Metadata = map[string]string{"provider": "override"}

	_, status, raw := postAgentClubEvent(t, server, sessionID, owner, req)
	if status != http.StatusBadRequest || !strings.Contains(raw, "provider") {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe metadata should not write inputs, stat err=%v", err)
	}
}

func validAgentClubEventRequest() agentclub.EventRequest {
	return agentclub.EventRequest{
		SchemaVersion:   agentclub.SchemaVersion,
		Source:          "fixture",
		Capability:      "event.review",
		EventType:       "event.created",
		ExternalEventID: "delivery-1",
		Prompt:          "Review this fixture event.",
		Payload:         json.RawMessage(`{"ok":true}`),
		Metadata: map[string]string{
			"project": "fixture-project",
		},
	}
}

func postAgentClubEvent(t *testing.T, server *Server, sessionID string, owner gatewayapi.SessionOwner, req agentclub.EventRequest) (agentclub.EventAdmissionResponse, int, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/events", bytes.NewReader(body))
	setScopedTestOwnerHeaders(httpReq, owner)
	server.Handler().ServeHTTP(rec, httpReq)
	var resp agentclub.EventAdmissionResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return resp, rec.Code, rec.Body.String()
}

func newAgentClubEventTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	return server, storeDir
}
