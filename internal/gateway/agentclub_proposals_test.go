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
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestAgentClubProposalCreateListApproveRedactsAndDoesNotDispatch(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)

	req := validProposalCreateRequest()
	resp, status, raw := postAgentClubProposal(t, server, sessionID, owner, req)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if resp.Proposal.ProposalID == "" || resp.Proposal.ProposalHash == "" || resp.Proposal.State != agentclub.ProposalStatePending {
		t.Fatalf("proposal = %#v", resp)
	}
	for _, forbidden := range []string{"SECRET payload", "SECRET metadata"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("proposal response leaked %q: %s", forbidden, raw)
		}
	}
	duplicate, duplicateStatus, _ := postAgentClubProposal(t, server, sessionID, owner, req)
	if duplicateStatus != http.StatusOK || !duplicate.Duplicate || duplicate.Proposal.ProposalID != resp.Proposal.ProposalID {
		t.Fatalf("duplicate status=%d response=%#v", duplicateStatus, duplicate)
	}

	list := listAgentClubProposals(t, server, sessionID, owner)
	if len(list.Proposals) != 1 || list.Proposals[0].ProposalID != resp.Proposal.ProposalID {
		t.Fatalf("list = %#v", list)
	}
	decision := decideAgentClubProposal(t, server, sessionID, owner, resp.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionApprove,
		ExpectedProposalHash: resp.Proposal.ProposalHash,
		Comment:              "looks good but do not apply",
	}, http.StatusOK)
	if decision.Proposal.State != agentclub.ProposalStateApproved || decision.DecisionID == "" {
		t.Fatalf("decision = %#v", decision)
	}
	decideAgentClubProposal(t, server, sessionID, owner, resp.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionApprove,
		ExpectedProposalHash: resp.Proposal.ProposalHash,
	}, http.StatusConflict)

	statusSnapshot := server.sessions[sessionID].Status()
	if statusSnapshot.Running || statusSnapshot.LastEvent != "" {
		t.Fatalf("proposal approval should not dispatch a run: %#v", statusSnapshot)
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionEventsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proposal route should not create session events, stat err=%v", err)
	}
	ledger, err := os.ReadFile(filepath.Join(storeDir, sessionID, agentClubProposalsJSONLName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ledger), "proposal_applied") {
		t.Fatalf("approval alone should not apply proposal:\n%s", string(ledger))
	}
}

func TestAgentClubProposalRejectsStaleHashAndCrossOwner(t *testing.T) {
	server, _ := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	resp, status, raw := postAgentClubProposal(t, server, sessionID, owner, validProposalCreateRequest())
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	decideAgentClubProposal(t, server, sessionID, owner, resp.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionReject,
		ExpectedProposalHash: strings.Repeat("b", 64),
	}, http.StatusConflict)

	other := gatewayapi.SessionOwner{ClientID: "ingress:other:prod", ClientType: "ingress"}
	body, _ := json.Marshal(agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionApprove,
		ExpectedProposalHash: resp.Proposal.ProposalHash,
	})
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/proposals/"+resp.Proposal.ProposalID+"/decision", bytes.NewReader(body))
	setScopedTestOwnerHeaders(httpReq, other)
	server.Handler().ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-owner status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgentClubProposalExpirationAndEditAsNew(t *testing.T) {
	server, _ := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	past := time.Now().UTC().Add(-time.Minute)
	expiring := validProposalCreateRequest()
	expiring.ExpiresAt = &past
	expired, status, raw := postAgentClubProposal(t, server, sessionID, owner, expiring)
	if status != http.StatusCreated {
		t.Fatalf("create expired status=%d body=%s", status, raw)
	}
	list := listAgentClubProposals(t, server, sessionID, owner)
	if len(list.Proposals) != 1 || list.Proposals[0].State != agentclub.ProposalStateExpired {
		t.Fatalf("expired list = %#v", list)
	}
	decideAgentClubProposal(t, server, sessionID, owner, expired.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionApprove,
		ExpectedProposalHash: expired.Proposal.ProposalHash,
	}, http.StatusConflict)

	freshReq := validProposalCreateRequest()
	freshReq.Preview = "Fresh safe preview"
	fresh, status, raw := postAgentClubProposal(t, server, sessionID, owner, freshReq)
	if status != http.StatusCreated {
		t.Fatalf("fresh status=%d body=%s", status, raw)
	}
	editReq := validProposalCreateRequest()
	editReq.Preview = "Edited safe preview"
	edit := decideAgentClubProposal(t, server, sessionID, owner, fresh.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionEdit,
		ExpectedProposalHash: fresh.Proposal.ProposalHash,
		Edit:                 &editReq,
	}, http.StatusOK)
	if edit.Proposal.State != agentclub.ProposalStateSuperseded || edit.NewProposal == nil || edit.NewProposal.State != agentclub.ProposalStatePending {
		t.Fatalf("edit response = %#v", edit)
	}
}

func TestAgentClubProposalApplyRecordNoteIsHashBoundIdempotentAndReplayable(t *testing.T) {
	server, storeDir := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	req := validRecordNoteProposalCreateRequest()
	created, status, raw := postAgentClubProposal(t, server, sessionID, owner, req)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:pending",
	}, http.StatusConflict)
	decideAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionApprove,
		ExpectedProposalHash: created.Proposal.ProposalHash,
	}, http.StatusOK)
	dryRun := applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:dry-run",
		DryRun:               true,
	}, http.StatusOK)
	if dryRun.State != agentclub.ProposalApplyStateDryRun || !dryRun.DryRun || dryRun.RunDispatched {
		t.Fatalf("dry run response = %#v", dryRun)
	}
	listAfterDryRun := listAgentClubProposals(t, server, sessionID, owner)
	if len(listAfterDryRun.Proposals) != 1 || listAfterDryRun.Proposals[0].State != agentclub.ProposalStateApproved {
		t.Fatalf("dry run should not mark applied: %#v", listAfterDryRun)
	}
	applied := applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:apply-1",
	}, http.StatusOK)
	if applied.State != agentclub.ProposalStateApplied || applied.ActionKind != agentclub.ProposalActionRecordNote || applied.OutputRef == "" || applied.RunDispatched {
		t.Fatalf("applied response = %#v", applied)
	}
	duplicate := applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:apply-1",
	}, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ApplyID != applied.ApplyID || duplicate.OutputRef != applied.OutputRef {
		t.Fatalf("duplicate response = %#v applied=%#v", duplicate, applied)
	}
	duplicateDifferentKey := applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:apply-2",
	}, http.StatusOK)
	if !duplicateDifferentKey.Duplicate || duplicateDifferentKey.ApplyID != applied.ApplyID {
		t.Fatalf("different-key duplicate should return existing apply: %#v applied=%#v", duplicateDifferentKey, applied)
	}
	list := listAgentClubProposals(t, server, sessionID, owner)
	if len(list.Proposals) != 1 || list.Proposals[0].State != agentclub.ProposalStateApplied {
		t.Fatalf("applied list = %#v", list)
	}
	ledger, err := os.ReadFile(filepath.Join(storeDir, sessionID, agentClubProposalsJSONLName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ledger), "SECRET payload") || strings.Contains(string(ledger), "SECRET metadata") || strings.Contains(string(ledger), "operator:apply") {
		t.Fatalf("apply ledger leaked raw values:\n%s", string(ledger))
	}
	if strings.Count(string(ledger), agentClubProposalApplied) != 1 {
		t.Fatalf("apply ledger should contain one applied record:\n%s", string(ledger))
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionEventsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("apply route should not create session events, stat err=%v", err)
	}
}

func TestAgentClubProposalApplyRejectsStaleCrossOwnerAndUnsupportedAction(t *testing.T) {
	server, _ := newAgentClubEventTestServer(t)
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)
	created, status, raw := postAgentClubProposal(t, server, sessionID, owner, validProposalCreateRequest())
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	decideAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               agentclub.ProposalDecisionApprove,
		ExpectedProposalHash: created.Proposal.ProposalHash,
	}, http.StatusOK)
	applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: strings.Repeat("c", 64),
		IdempotencyKey:       "operator:stale",
	}, http.StatusConflict)
	other := gatewayapi.SessionOwner{ClientID: "ingress:other:prod", ClientType: "ingress"}
	applyAgentClubProposal(t, server, sessionID, other, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:other",
	}, http.StatusForbidden)
	failed := applyAgentClubProposal(t, server, sessionID, owner, created.Proposal.ProposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: created.Proposal.ProposalHash,
		IdempotencyKey:       "operator:unsupported",
	}, http.StatusOK)
	if failed.State != agentclub.ProposalStateApplyFailed || failed.ActionKind != "hh.reply" || failed.RunDispatched {
		t.Fatalf("unsupported apply response = %#v", failed)
	}
	list := listAgentClubProposals(t, server, sessionID, owner)
	if len(list.Proposals) != 1 || list.Proposals[0].State != agentclub.ProposalStateApplyFailed {
		t.Fatalf("failed list = %#v", list)
	}
}

func validProposalCreateRequest() agentclub.ProposalCreateRequest {
	return agentclub.ProposalCreateRequest{
		SchemaVersion: agentclub.SchemaVersion,
		Source:        "fixture",
		Capability:    "reply.draft",
		ActionKind:    "hh.reply",
		Risk:          agentclub.RiskExternalMutation,
		Preview:       "Safe preview text for an untrusted external draft.",
		Payload:       json.RawMessage(`{"body":"SECRET payload"}`),
		TargetScope:   "fixture:target:1",
		Metadata:      map[string]string{"project": "SECRET metadata"},
	}
}

func validRecordNoteProposalCreateRequest() agentclub.ProposalCreateRequest {
	req := validProposalCreateRequest()
	req.Capability = "note.record"
	req.ActionKind = agentclub.ProposalActionRecordNote
	req.Risk = agentclub.RiskLocalWrite
	req.Preview = "Record reviewed queue note."
	req.Payload = json.RawMessage(`{"message":"SECRET payload","labels":["fixture"]}`)
	req.OutputRef = ""
	return req
}

func postAgentClubProposal(t *testing.T, server *Server, sessionID string, owner gatewayapi.SessionOwner, req agentclub.ProposalCreateRequest) (agentclub.ProposalCreateResponse, int, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/proposals", bytes.NewReader(body))
	setScopedTestOwnerHeaders(httpReq, owner)
	server.Handler().ServeHTTP(rec, httpReq)
	var resp agentclub.ProposalCreateResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return resp, rec.Code, rec.Body.String()
}

func listAgentClubProposals(t *testing.T, server *Server, sessionID string, owner gatewayapi.SessionOwner) agentclub.ProposalListResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/agentclub/proposals", nil)
	setScopedTestOwnerHeaders(httpReq, owner)
	server.Handler().ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp agentclub.ProposalListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func decideAgentClubProposal(t *testing.T, server *Server, sessionID string, owner gatewayapi.SessionOwner, proposalID string, req agentclub.ProposalDecisionRequest, wantStatus int) agentclub.ProposalDecisionResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/proposals/"+proposalID+"/decision", bytes.NewReader(body))
	setScopedTestOwnerHeaders(httpReq, owner)
	server.Handler().ServeHTTP(rec, httpReq)
	if rec.Code != wantStatus {
		t.Fatalf("decision status = %d body=%s want %d", rec.Code, rec.Body.String(), wantStatus)
	}
	var resp agentclub.ProposalDecisionResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return resp
}

func applyAgentClubProposal(t *testing.T, server *Server, sessionID string, owner gatewayapi.SessionOwner, proposalID string, req agentclub.ProposalApplyRequest, wantStatus int) agentclub.ProposalApplyResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/proposals/"+proposalID+"/apply", bytes.NewReader(body))
	setScopedTestOwnerHeaders(httpReq, owner)
	server.Handler().ServeHTTP(rec, httpReq)
	if rec.Code != wantStatus {
		t.Fatalf("apply status = %d body=%s want %d", rec.Code, rec.Body.String(), wantStatus)
	}
	var resp agentclub.ProposalApplyResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return resp
}
