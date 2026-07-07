package agentclub

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestNewProposalHashesExactArtifactAndRedactsMetadataValues(t *testing.T) {
	owner := gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"}
	req := ProposalCreateRequest{
		SchemaVersion: SchemaVersion,
		Source:        "fixture",
		Capability:    "reply.draft",
		ActionKind:    "hh.reply",
		Risk:          RiskExternalMutation,
		Preview:       "Safe preview",
		Payload:       json.RawMessage(`{"body":"SECRET payload"}`),
		TargetScope:   "hh:negotiation:1",
		Metadata:      map[string]string{"project": "SECRET project"},
	}
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	proposal, payload, err := NewProposal(req, "session-1", owner, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.ProposalHash == "" || proposal.PayloadSHA256 == "" || len(payload) == 0 {
		t.Fatalf("proposal = %#v payload=%s", proposal, payload)
	}
	if proposal.Owner != owner || proposal.State != ProposalStatePending || proposal.PolicyVersion != ProposalPolicyVersion {
		t.Fatalf("proposal scope = %#v", proposal)
	}
	if len(proposal.MetadataKeys) != 1 || proposal.MetadataKeys[0] != "project" {
		t.Fatalf("metadata keys = %#v", proposal.MetadataKeys)
	}
	again, _, err := NewProposal(req, "session-1", owner, now.Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ProposalID != proposal.ProposalID || again.ProposalHash != proposal.ProposalHash {
		t.Fatalf("hash changed: %#v vs %#v", proposal, again)
	}
	req.Payload = json.RawMessage(`{"body":"different"}`)
	changed, _, err := NewProposal(req, "session-1", owner, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ProposalHash == proposal.ProposalHash {
		t.Fatalf("hash did not change for payload change")
	}
}

func TestNewProposalValidationAndDecisionNormalization(t *testing.T) {
	_, _, err := NewProposal(ProposalCreateRequest{
		SchemaVersion: SchemaVersion,
		Source:        "fixture",
		Capability:    "reply.draft",
		ActionKind:    "hh.reply",
		Risk:          "network",
		Preview:       "preview",
	}, "session-1", gatewayapi.SessionOwner{}, time.Time{}, "")
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("risk err = %v", err)
	}
	_, _, err = NewProposal(ProposalCreateRequest{
		SchemaVersion: SchemaVersion,
		Source:        "fixture",
		Capability:    "reply.draft",
		ActionKind:    "hh.reply",
		Risk:          RiskExternalMutation,
		Preview:       "preview",
		Metadata:      map[string]string{"provider": "override"},
	}, "session-1", gatewayapi.SessionOwner{}, time.Time{}, "")
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("metadata err = %v", err)
	}
	_, err = NormalizeProposalDecision(ProposalDecisionRequest{
		SchemaVersion:        SchemaVersion,
		Action:               ProposalDecisionApprove,
		ExpectedProposalHash: "not-a-hash",
	})
	if !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("decision hash err = %v", err)
	}
	_, err = NormalizeProposalApply(ProposalApplyRequest{
		SchemaVersion:        SchemaVersion,
		ExpectedProposalHash: "not-a-hash",
		IdempotencyKey:       "operator-key",
	})
	if !errors.Is(err, ErrInvalidApply) {
		t.Fatalf("apply hash err = %v", err)
	}
	apply, err := NormalizeProposalApply(ProposalApplyRequest{
		SchemaVersion:        SchemaVersion,
		ExpectedProposalHash: strings.Repeat("A", 64),
		IdempotencyKey:       "operator:key-1",
		DryRun:               true,
	})
	if err != nil {
		t.Fatalf("apply normalize err = %v", err)
	}
	if apply.ExpectedProposalHash != strings.Repeat("a", 64) || !apply.DryRun {
		t.Fatalf("apply = %#v", apply)
	}
	if got := ProposalApplyID("proposal-1", strings.Repeat("a", 64), "operator:key-1"); got == "" || got != ProposalApplyID("proposal-1", strings.Repeat("A", 64), "operator:key-1") {
		t.Fatalf("apply id not deterministic: %q", got)
	}
}

func TestProposalExpired(t *testing.T) {
	expires := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	proposal := Proposal{State: ProposalStatePending, ExpiresAt: &expires}
	if !ProposalExpired(proposal, expires) {
		t.Fatal("proposal should expire at expires_at")
	}
	proposal.State = ProposalStateApproved
	if ProposalExpired(proposal, expires.Add(time.Hour)) {
		t.Fatal("approved proposal should not expire")
	}
}
