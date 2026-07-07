package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
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
}

func writeAgentclubTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
