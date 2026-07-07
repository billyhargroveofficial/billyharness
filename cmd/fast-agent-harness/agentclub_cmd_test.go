package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

func TestAgentclubCapabilitiesCommandListsScopedBindings(t *testing.T) {
	var gotClientType, gotClientID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agentclub/capabilities" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotClientType = r.Header.Get(gatewayapi.HeaderSessionClientType)
		gotClientID = r.Header.Get(gatewayapi.HeaderSessionClientID)
		writeAgentclubTestJSON(t, w, agentclub.CapabilityListResponse{
			SchemaVersion: agentclub.SchemaVersion,
			Capabilities: []agentclub.CapabilityView{{
				Descriptor: agentclub.CapabilityDescriptor{
					ID:       "event.review",
					Title:    "Review queue",
					Kind:     agentclub.CapabilityKindReview,
					Risk:     agentclub.RiskReadOnly,
					Dispatch: agentclub.DispatchAdmitOnly,
					Approval: agentclub.ApprovalRequired,
				},
				Bindings: []agentclub.BindingView{{
					Capability: "event.review",
					ClientType: "ingress",
					ClientID:   "ingress:hh-applicant-tool:prod",
					Sources:    []string{"hh_applicant_tool"},
					EventTypes: []string{"review_queue"},
					Enabled:    true,
				}},
			}},
		})
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := agentclubCommand([]string{
		"capabilities",
		"-gateway", server.URL,
		"-client-type", "ingress",
		"-client-id", "ingress:hh-applicant-tool:prod",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotClientType != "ingress" || gotClientID != "ingress:hh-applicant-tool:prod" {
		t.Fatalf("owner headers type=%q id=%q", gotClientType, gotClientID)
	}
	for _, want := range []string{"agent-club capabilities: 1", "event.review", "binding ingress/ingress:hh-applicant-tool:prod"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestAgentclubProposalListAndDecisionCommandsUseExplicitHashOnly(t *testing.T) {
	proposalHash := strings.Repeat("a", 64)
	proposal := agentclub.Proposal{
		SchemaVersion: agentclub.SchemaVersion,
		ProposalID:    "proposal-1",
		SessionID:     "session-1",
		Source:        "hh_applicant_tool",
		Capability:    "safe_output.reply",
		ActionKind:    "reply",
		Risk:          agentclub.RiskExternalMutation,
		State:         agentclub.ProposalStatePending,
		ProposalHash:  proposalHash,
		PayloadSHA256: strings.Repeat("b", 64),
		Preview:       "candidate reply",
		CreatedAt:     time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC),
	}
	var gotDecision agentclub.ProposalDecisionRequest
	var gotApply agentclub.ProposalApplyRequest
	var sawRun bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions/session-1/agentclub/proposals":
			writeAgentclubTestJSON(t, w, agentclub.ProposalListResponse{
				SchemaVersion: agentclub.SchemaVersion,
				Proposals:     []agentclub.Proposal{proposal},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions/session-1/agentclub/proposals/proposal-1/decision":
			if err := json.NewDecoder(r.Body).Decode(&gotDecision); err != nil {
				t.Fatal(err)
			}
			approved := proposal
			approved.State = agentclub.ProposalStateApproved
			writeAgentclubTestJSON(t, w, agentclub.ProposalDecisionResponse{
				SchemaVersion: agentclub.SchemaVersion,
				DecisionID:    "decision-1",
				Action:        agentclub.ProposalDecisionApprove,
				Proposal:      approved,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions/session-1/agentclub/proposals/proposal-1/apply":
			if err := json.NewDecoder(r.Body).Decode(&gotApply); err != nil {
				t.Fatal(err)
			}
			writeAgentclubTestJSON(t, w, agentclub.ProposalApplyResponse{
				SchemaVersion: agentclub.SchemaVersion,
				ProposalID:    "proposal-1",
				ProposalHash:  proposalHash,
				ApplyID:       "apply-1",
				State:         agentclub.ProposalStateApplied,
				ActionKind:    agentclub.ProposalActionRecordNote,
				OutputRef:     "agentclub:apply:apply-1",
				PayloadSHA256: strings.Repeat("b", 64),
				RunDispatched: false,
			})
		case strings.Contains(r.URL.Path, "/run"):
			sawRun = true
			t.Fatalf("agentclub command must not dispatch runs")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	var listOut bytes.Buffer
	if err := agentclubCommand([]string{"proposals", "-gateway", server.URL, "-session", "session-1"}, &listOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOut.String(), "agent-club proposals: 1 pending=1") || !strings.Contains(listOut.String(), "candidate reply") {
		t.Fatalf("proposal list output:\n%s", listOut.String())
	}

	var approveOut bytes.Buffer
	if err := agentclubCommand([]string{
		"approve",
		"-gateway", server.URL,
		"-session", "session-1",
		"-proposal", "proposal-1",
		"-hash", proposalHash,
		"-comment", "looks good",
	}, &approveOut); err != nil {
		t.Fatal(err)
	}
	if sawRun {
		t.Fatal("run dispatched")
	}
	if gotDecision.Action != agentclub.ProposalDecisionApprove || gotDecision.ExpectedProposalHash != proposalHash || gotDecision.Comment != "looks good" {
		t.Fatalf("decision request = %#v", gotDecision)
	}
	if !strings.Contains(approveOut.String(), "approve proposal proposal-1 decision=decision-1 state=approved") {
		t.Fatalf("approve output:\n%s", approveOut.String())
	}
	var applyOut bytes.Buffer
	if err := agentclubCommand([]string{
		"apply",
		"-gateway", server.URL,
		"-session", "session-1",
		"-proposal", "proposal-1",
		"-hash", proposalHash,
	}, &applyOut); err != nil {
		t.Fatal(err)
	}
	if sawRun {
		t.Fatal("run dispatched")
	}
	if gotApply.ExpectedProposalHash != proposalHash || gotApply.IdempotencyKey == "" || gotApply.DryRun {
		t.Fatalf("apply request = %#v", gotApply)
	}
	if !strings.Contains(applyOut.String(), "apply proposal proposal-1 apply=apply-1 state=applied action=record_note") || !strings.Contains(applyOut.String(), "run_dispatched=false") {
		t.Fatalf("apply output:\n%s", applyOut.String())
	}
}

func TestAgentclubLocalConfigCommandsValidateListAndToggle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentclub.config.json")
	cfg := agentclub.FileConfig{
		SchemaVersion: agentclub.SchemaVersion,
		Capabilities: []agentclub.CapabilityDescriptor{{
			ID:       "fixture.review",
			Title:    "Fixture review",
			Kind:     agentclub.CapabilityKindReview,
			Risk:     agentclub.RiskReadOnly,
			Dispatch: agentclub.DispatchAdmitOnly,
			Approval: agentclub.ApprovalNone,
		}},
		TrustedBindings: []agentclub.TrustedBindingConfig{{
			ID:         "fixture.binding",
			Capability: "fixture.review",
			ClientType: "ingress",
			ClientID:   "ingress:fixture:prod",
			Sources:    []string{"fixture"},
			EventTypes: []string{"review_queue"},
			Enabled:    true,
		}},
	}
	if err := agentclub.WriteConfigFile(path, cfg); err != nil {
		t.Fatal(err)
	}

	var validateOut bytes.Buffer
	if err := agentclubCommand([]string{"config", "validate", "-path", path}, &validateOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(validateOut.String(), "agent-club config: files=1 capabilities=1 bindings=1") {
		t.Fatalf("validate output:\n%s", validateOut.String())
	}

	var bindingsOut bytes.Buffer
	if err := agentclubCommand([]string{"bindings", "-path", path}, &bindingsOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bindingsOut.String(), "fixture.binding") || !strings.Contains(bindingsOut.String(), "enabled=true") {
		t.Fatalf("bindings output:\n%s", bindingsOut.String())
	}

	var disableOut bytes.Buffer
	if err := agentclubCommand([]string{"disable", "binding", "fixture.binding", "-path", path}, &disableOut); err != nil {
		t.Fatal(err)
	}
	updated, err := agentclub.ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.TrustedBindings) != 1 || updated.TrustedBindings[0].Enabled {
		t.Fatalf("updated config = %#v", updated.TrustedBindings)
	}
}

func TestAgentclubTriggerDeliverManualQueuesInputWithoutRun(t *testing.T) {
	path := writeAgentclubTriggerConfig(t, agentclub.TriggerBindingConfig{
		ID:              "fixture.manual",
		Kind:            agentclub.TriggerKindManual,
		Source:          "fixture",
		Capability:      "fixture.review",
		EventType:       "review_queue",
		Owner:           gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID: "session-1",
		Prompt:          "Review the queued fixture snapshot.",
		AuthMethod:      agentclub.TriggerAuthNone,
		Enabled:         true,
	})
	var got agentclub.TriggerDeliveryRequest
	var sawRun bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agentclub/triggers/fixture.manual/deliveries":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			writeAgentclubTestJSON(t, w, agentclub.TriggerDeliveryResponse{
				SchemaVersion:       agentclub.SchemaVersion,
				Admitted:            true,
				InputID:             "input-1",
				State:               "admitted",
				TargetSessionID:     "session-1",
				BindingID:           "fixture.manual",
				TriggerKind:         agentclub.TriggerKindManual,
				Source:              "fixture",
				Capability:          "fixture.review",
				EventType:           "review_queue",
				PayloadSHA256:       strings.Repeat("a", 64),
				ExternalEventIDHash: strings.Repeat("b", 64),
				RunDispatched:       false,
			})
		case strings.Contains(r.URL.Path, "/run"):
			sawRun = true
			t.Fatalf("trigger delivery command must not dispatch runs")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := agentclubCommand([]string{
		"trigger", "deliver", "fixture.manual",
		"-path", path,
		"-gateway", server.URL,
		"-payload", `{"candidate":"candidate@example.com"}`,
		"-scheduled-at", "now",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if sawRun {
		t.Fatal("run dispatched")
	}
	if got.SchemaVersion != agentclub.SchemaVersion || got.ScheduledAtUTC == "" || string(got.Payload) != `{"candidate":"candidate@example.com"}` {
		t.Fatalf("delivery request = %#v payload=%s", got, string(got.Payload))
	}
	for _, want := range []string{"agent-club trigger delivery: admitted=true", "input-1", "target_session=session-1", "run_dispatched=false", "next: queued input input-1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "candidate@example.com") {
		t.Fatalf("output leaked raw payload:\n%s", out.String())
	}
}

func TestAgentclubTriggerDeliverWebhookSignsAndRedacts(t *testing.T) {
	secretEnv := "AGENTCLUB_TEST_SECRET_SIGN"
	secret := "super-secret-signing-value"
	t.Setenv(secretEnv, secret)
	path := writeAgentclubTriggerConfig(t, agentclub.TriggerBindingConfig{
		ID:               "fixture.webhook",
		Kind:             agentclub.TriggerKindWebhook,
		Source:           "fixture",
		Capability:       "fixture.review",
		EventType:        "review_queue",
		Owner:            gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID:  "session-1",
		Prompt:           "Review the queued fixture snapshot.",
		AuthMethod:       agentclub.TriggerAuthHMACSHA256,
		HMACSecretEnv:    secretEnv,
		SignatureHeader:  "X-Test-Signature",
		TimestampHeader:  "X-Test-Timestamp",
		DeliveryIDHeader: "X-Test-Delivery-ID",
		Enabled:          true,
	})
	const deliveryID = "delivery-secret-id"
	const payload = `{"candidate":"candidate@example.com"}`
	var gotBody []byte
	var gotSignature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/agentclub/triggers/fixture.webhook/deliveries" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Test-Delivery-ID") != deliveryID {
			t.Fatalf("delivery header = %q", r.Header.Get("X-Test-Delivery-ID"))
		}
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotSignature = r.Header.Get("X-Test-Signature")
		if err := ingress.VerifyRawBodyHMACSHA256(ingress.HMACVerification{
			Secret:           []byte(secret),
			Body:             gotBody,
			Signature:        gotSignature,
			Timestamp:        r.Header.Get("X-Test-Timestamp"),
			Now:              time.Now().UTC(),
			MaxSkew:          time.Minute,
			IncludeTimestamp: true,
		}); err != nil {
			t.Fatal(err)
		}
		writeAgentclubTestJSON(t, w, agentclub.TriggerDeliveryResponse{
			SchemaVersion:       agentclub.SchemaVersion,
			Admitted:            true,
			InputID:             "input-1",
			State:               "admitted",
			TargetSessionID:     "session-1",
			BindingID:           "fixture.webhook",
			TriggerKind:         agentclub.TriggerKindWebhook,
			Source:              "fixture",
			Capability:          "fixture.review",
			EventType:           "review_queue",
			PayloadSHA256:       strings.Repeat("a", 64),
			ExternalEventIDHash: strings.Repeat("b", 64),
			RunDispatched:       false,
		})
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := agentclubCommand([]string{
		"trigger", "deliver", "fixture.webhook",
		"-path", path,
		"-gateway", server.URL,
		"-payload", payload,
		"-delivery-id", deliveryID,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != payload {
		t.Fatalf("body = %s", string(gotBody))
	}
	for _, forbidden := range []string{secret, deliveryID, payload, "candidate@example.com", gotSignature} {
		if forbidden != "" && strings.Contains(out.String(), forbidden) {
			t.Fatalf("output leaked %q:\n%s", forbidden, out.String())
		}
	}
	if !strings.Contains(out.String(), "run_dispatched=false") || !strings.Contains(out.String(), "fixture.webhook") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestAgentclubTriggerDeliverRejectsLocalMistakes(t *testing.T) {
	base := agentclub.TriggerBindingConfig{
		ID:              "fixture.manual",
		Kind:            agentclub.TriggerKindManual,
		Source:          "fixture",
		Capability:      "fixture.review",
		EventType:       "review_queue",
		Owner:           gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID: "session-1",
		Prompt:          "Review the queued fixture snapshot.",
		AuthMethod:      agentclub.TriggerAuthNone,
		Enabled:         true,
	}
	path := writeAgentclubTriggerConfig(t, base)
	disabled := base
	disabled.ID = "fixture.disabled"
	disabled.Enabled = false
	disabledPath := writeAgentclubTriggerConfig(t, disabled)
	webhook := base
	webhook.ID = "fixture.webhook"
	webhook.Kind = agentclub.TriggerKindWebhook
	webhook.DeliveryIDHeader = "X-Test-Delivery-ID"
	webhookPath := writeAgentclubTriggerConfig(t, webhook)
	hmac := webhook
	hmac.ID = "fixture.hmac"
	hmac.AuthMethod = agentclub.TriggerAuthHMACSHA256
	hmac.HMACSecretEnv = "AGENTCLUB_TEST_SECRET_MISSING_016"
	hmacPath := writeAgentclubTriggerConfig(t, hmac)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing config", args: []string{"trigger", "deliver", "fixture.manual", "-path", filepath.Join(t.TempDir(), "missing.json")}, want: "load agentclub config"},
		{name: "unknown trigger", args: []string{"trigger", "deliver", "fixture.unknown", "-path", path}, want: "unknown agentclub trigger binding"},
		{name: "disabled trigger", args: []string{"trigger", "deliver", "fixture.disabled", "-path", disabledPath}, want: "disabled agentclub trigger binding"},
		{name: "bad json", args: []string{"trigger", "deliver", "fixture.manual", "-path", path, "-payload", `{bad`}, want: "payload must be valid JSON"},
		{name: "missing delivery id", args: []string{"trigger", "deliver", "fixture.webhook", "-path", webhookPath}, want: "-delivery-id is required"},
		{name: "missing hmac env", args: []string{"trigger", "deliver", "fixture.hmac", "-path", hmacPath, "-delivery-id", "delivery-1"}, want: "secret env AGENTCLUB_TEST_SECRET_MISSING_016 is not set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := agentclubCommand(tc.args, &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("unexpected output:\n%s", out.String())
			}
		})
	}
}

func TestAgentclubTriggerDeliverStatusErrorsStayRedacted(t *testing.T) {
	path := writeAgentclubTriggerConfig(t, agentclub.TriggerBindingConfig{
		ID:              "fixture.manual",
		Kind:            agentclub.TriggerKindManual,
		Source:          "fixture",
		Capability:      "fixture.review",
		EventType:       "review_queue",
		Owner:           gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID: "session-1",
		Prompt:          "Review the queued fixture snapshot.",
		AuthMethod:      agentclub.TriggerAuthNone,
		Enabled:         true,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`raw candidate@example.com delivery-secret-id secret should not echo`))
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := agentclubCommand([]string{
		"trigger", "deliver", "fixture.manual",
		"-path", path,
		"-gateway", server.URL,
		"-payload", `{"candidate":"candidate@example.com"}`,
	}, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "gateway HTTP 413") {
		t.Fatalf("err = %v", err)
	}
	for _, forbidden := range []string{"candidate@example.com", "delivery-secret-id", "secret should not echo"} {
		if strings.Contains(err.Error(), forbidden) || strings.Contains(out.String(), forbidden) {
			t.Fatalf("leaked %q err=%v out=%s", forbidden, err, out.String())
		}
	}
}

func TestAgentclubTriggerDeliverDuplicatePrintsDuplicateState(t *testing.T) {
	path := writeAgentclubTriggerConfig(t, agentclub.TriggerBindingConfig{
		ID:              "fixture.manual",
		Kind:            agentclub.TriggerKindManual,
		Source:          "fixture",
		Capability:      "fixture.review",
		EventType:       "review_queue",
		Owner:           gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID: "session-1",
		Prompt:          "Review the queued fixture snapshot.",
		AuthMethod:      agentclub.TriggerAuthNone,
		Enabled:         true,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAgentclubTestJSON(t, w, agentclub.TriggerDeliveryResponse{
			SchemaVersion:   agentclub.SchemaVersion,
			Admitted:        true,
			InputID:         "input-1",
			State:           "duplicate",
			Duplicate:       true,
			TargetSessionID: "session-1",
			BindingID:       "fixture.manual",
			TriggerKind:     agentclub.TriggerKindManual,
			Source:          "fixture",
			Capability:      "fixture.review",
			EventType:       "review_queue",
			RunDispatched:   false,
		})
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	if err := agentclubCommand([]string{"trigger", "deliver", "fixture.manual", "-path", path, "-gateway", server.URL}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "duplicate=true") || !strings.Contains(out.String(), "state=duplicate") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func writeAgentclubTriggerConfig(t *testing.T, trigger agentclub.TriggerBindingConfig) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agentclub.config.json")
	cfg := agentclub.FileConfig{
		SchemaVersion: agentclub.SchemaVersion,
		Capabilities: []agentclub.CapabilityDescriptor{{
			ID:       "fixture.review",
			Title:    "Fixture review",
			Kind:     agentclub.CapabilityKindReview,
			Risk:     agentclub.RiskReadOnly,
			Dispatch: agentclub.DispatchAdmitOnly,
			Approval: agentclub.ApprovalNone,
		}},
		TrustedBindings: []agentclub.TrustedBindingConfig{{
			ID:         "fixture.binding",
			Capability: "fixture.review",
			ClientType: "ingress",
			ClientID:   "ingress:fixture:prod",
			Sources:    []string{"fixture"},
			EventTypes: []string{"review_queue"},
			Enabled:    true,
		}},
		Triggers: []agentclub.TriggerBindingConfig{trigger},
	}
	if err := agentclub.WriteConfigFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeAgentclubTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
