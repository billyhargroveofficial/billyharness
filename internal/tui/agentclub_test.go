package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestAgentClubStatusTextFetchesCapabilitiesAndScopedProposals(t *testing.T) {
	var proposalClientType, proposalTUIChatID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agentclub/capabilities":
			writeTUITestJSON(t, w, agentclub.CapabilityListResponse{
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
						Enabled:    true,
					}},
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions/session-1/agentclub/proposals":
			proposalClientType = r.Header.Get(gatewayapi.HeaderSessionClientType)
			proposalTUIChatID = r.Header.Get(gatewayapi.HeaderSessionTUIChatID)
			writeTUITestJSON(t, w, agentclub.ProposalListResponse{
				SchemaVersion: agentclub.SchemaVersion,
				Proposals: []agentclub.Proposal{{
					SchemaVersion: agentclub.SchemaVersion,
					ProposalID:    "proposal-1",
					SessionID:     "session-1",
					Source:        "hh_applicant_tool",
					Capability:    "safe_output.reply",
					ActionKind:    "reply",
					Risk:          agentclub.RiskExternalMutation,
					State:         agentclub.ProposalStatePending,
					ProposalHash:  strings.Repeat("a", 64),
					PayloadSHA256: strings.Repeat("b", 64),
					Preview:       "draft reply",
					CreatedAt:     time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC),
				}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	m := NewModel(config.Default(), Options{})
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	m.localChatID = "local-chat-1"

	text, err := m.agentClubStatusText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if proposalClientType != "tui" || proposalTUIChatID != "local-chat-1" {
		t.Fatalf("proposal owner headers type=%q tui=%q", proposalClientType, proposalTUIChatID)
	}
	for _, want := range []string{"agent-club capabilities: 1", "event.review", "agent-club proposals: 1 pending=1", "draft reply"} {
		if !strings.Contains(text, want) {
			t.Fatalf("agent-club text missing %q:\n%s", want, text)
		}
	}
}

func writeTUITestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
