package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewayIngressAdmitsAuditsDuplicatesAndConflictsWithoutDispatch(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)

	event := ingress.IngressEvent{
		Source:          "fixture",
		ExternalEventID: "delivery-123",
		TargetSessionID: sessionID,
		Prompt:          "Summarize this fixture event.",
		RawBody:         []byte(`{"message":"SECRET fixture text"}`),
		Metadata: map[string]string{
			"project": "fixture-project",
		},
	}
	rule := ingress.IngressRule{
		ID:     "fixture-review",
		Source: "fixture",
		Owner:  owner,
		StaticMetadata: map[string]string{
			"ingress.policy": "review_only",
		},
	}
	result, err := server.AdmitIngressEvent(context.Background(), event, rule)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Decision.Admitted || result.Input.State != sessionInputAdmitted || result.Input.InputID == "" {
		t.Fatalf("result = %#v", result)
	}
	status := server.sessions[sessionID].Status()
	if status.Running || status.MessageCount == 0 || status.LastEvent != "" {
		t.Fatalf("ingress admission should not dispatch a run: %#v", status)
	}
	eventsPath := filepath.Join(storeDir, sessionID, sessionEventsJSONLName)
	if _, err := os.Stat(eventsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ingress admission should not create session events, stat err=%v", err)
	}
	inputsPath := filepath.Join(storeDir, sessionID, sessionInputsJSONLName)
	records := readSessionInputRecords(t, inputsPath)
	if len(records) != 1 || records[0].Kind != sessionInputAdmitted || records[0].InputID != result.Input.InputID {
		t.Fatalf("input records = %#v", records)
	}
	audit, err := server.store.ReplayIngressAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 ||
		audit[0].Decision != ingressAuditReceived ||
		audit[1].Decision != ingressAuditAdmitted ||
		audit[1].InputID != result.Input.InputID ||
		audit[1].ExternalEventIDHash == "" {
		t.Fatalf("audit = %#v", audit)
	}
	auditJSON, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRET fixture text", "Summarize this fixture event.", "delivery-123", owner.ClientID} {
		if strings.Contains(string(auditJSON), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, auditJSON)
		}
	}
	if !hasString(audit[1].MetadataKeys, "project") || !hasString(audit[1].MetadataKeys, "ingress.policy") {
		t.Fatalf("audit metadata keys = %#v", audit[1].MetadataKeys)
	}

	duplicate, err := server.AdmitIngressEvent(context.Background(), event, rule)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Input.Duplicate || duplicate.Input.InputID != result.Input.InputID {
		t.Fatalf("duplicate result = %#v", duplicate)
	}

	conflictingRule := rule
	conflictingRule.Prompt = "Changed prompt for same delivery."
	conflict, err := server.AdmitIngressEvent(context.Background(), event, conflictingRule)
	if err == nil || !strings.Contains(err.Error(), "already exists with different body") {
		t.Fatalf("conflict err = %v result=%#v", err, conflict)
	}
	audit, err = server.store.ReplayIngressAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 6 ||
		audit[2].Decision != ingressAuditReceived ||
		audit[3].Decision != ingressAuditAdmitted ||
		!audit[3].Duplicate ||
		audit[4].Decision != ingressAuditReceived ||
		audit[5].Decision != ingressAuditRejected ||
		!strings.Contains(audit[5].Reason, "input admission failed") {
		t.Fatalf("audit after duplicate/conflict = %#v", audit)
	}
}

func TestGatewayIngressAuditsRejectedAdmission(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	_, err := server.AdmitIngressEvent(context.Background(),
		ingress.IngressEvent{Source: "gitlab", TargetSessionID: "session-1", Prompt: "hello"},
		ingress.IngressRule{ID: "github-only", Source: "github"},
	)
	if err == nil || !strings.Contains(err.Error(), "source not allowed") {
		t.Fatalf("err = %v", err)
	}
	audit, replayErr := server.store.ReplayIngressAudit()
	if replayErr != nil {
		t.Fatal(replayErr)
	}
	if len(audit) != 1 ||
		audit[0].Decision != ingressAuditRejected ||
		audit[0].Reason != "source not allowed" ||
		audit[0].TargetSessionID != "session-1" {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestGatewayIngressDeniesUnscopedRuleBeforeInputWrite(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createScopedTestSession(t, server, gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"})

	_, err := server.AdmitIngressEvent(context.Background(),
		ingress.IngressEvent{Source: "fixture", TargetSessionID: sessionID, Prompt: "hello"},
		ingress.IngressRule{ID: "fixture-review", Source: "fixture"},
	)
	if err == nil || !strings.Contains(err.Error(), "client_id required") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unscoped ingress should not write inputs, stat err=%v", statErr)
	}
	audit, replayErr := server.store.ReplayIngressAudit()
	if replayErr != nil {
		t.Fatal(replayErr)
	}
	if len(audit) != 1 || audit[0].Decision != ingressAuditRejected || !strings.Contains(audit[0].Reason, "client_id required") {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestGatewayIngressDeniesCrossOwnerBeforeInputWrite(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createScopedTestSession(t, server, gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"})

	_, err := server.AdmitIngressEvent(context.Background(),
		ingress.IngressEvent{Source: "fixture", TargetSessionID: sessionID, Prompt: "hello"},
		ingress.IngressRule{ID: "fixture-review", Source: "fixture", Owner: gatewayapi.SessionOwner{ClientID: "ingress:other:prod", ClientType: "ingress"}},
	)
	if err == nil || !strings.Contains(err.Error(), "session owner scope mismatch") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cross-owner ingress should not write inputs, stat err=%v", statErr)
	}
	audit, replayErr := server.store.ReplayIngressAudit()
	if replayErr != nil {
		t.Fatal(replayErr)
	}
	if len(audit) != 1 || audit[0].Decision != ingressAuditRejected || !strings.Contains(audit[0].Reason, "session owner scope mismatch") {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestGatewayIngressAuditFailurePreventsInputWrite(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	if err := os.WriteFile(filepath.Join(storeDir, ingressAuditJSONLName), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := server.AdmitIngressEvent(context.Background(),
		ingress.IngressEvent{Source: "fixture", TargetSessionID: sessionID, Prompt: "hello"},
		ingress.IngressRule{ID: "fixture-review", Source: "fixture", Owner: owner},
	)
	if err == nil {
		t.Fatalf("expected audit replay error")
	}
	if _, statErr := os.Stat(filepath.Join(storeDir, sessionID, sessionInputsJSONLName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("audit failure should not write inputs, stat err=%v", statErr)
	}
}

func TestGatewayIngressAuditReplayRejectsCorruption(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	store := newSessionStore(storeDir)
	decision := ingress.AdmissionDecision{
		Admitted:        true,
		RuleID:          "rule",
		Source:          "source",
		PayloadSHA256:   ingress.PayloadSHA256([]byte("body")),
		TargetSessionID: "session-1",
		InputID:         "input-1",
		Request: gatewayapi.SessionInputRequest{
			InputID:  "input-1",
			ClientID: "ingress:test",
		},
	}
	if _, err := store.AppendIngressAudit(decision, gatewayapi.SessionInputResponse{InputID: "input-1"}, ingressAuditAdmitted, "admitted"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storeDir, ingressAuditJSONLName)
	if err := eventlog.AppendJSONL(path, ingressAuditRecord{
		SchemaVersion:   gatewaySessionSchemaVersion,
		Seq:             3,
		Decision:        ingressAuditAdmitted,
		PayloadSHA256:   decision.PayloadSHA256,
		TargetSessionID: "session-1",
		InputID:         "input-2",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := store.ReplayIngressAudit()
	var corrupt *eventlog.CorruptionError
	if err == nil || !errors.As(err, &corrupt) || !strings.Contains(err.Error(), "sequence gap") {
		t.Fatalf("replay err = %T %[1]v", err)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
