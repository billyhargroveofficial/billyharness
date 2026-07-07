package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

const (
	agentClubProposalsJSONLName = "agentclub-proposals.jsonl"

	agentClubProposalCreated     = "proposal_created"
	agentClubDecisionRecorded    = "decision_recorded"
	agentClubProposalExpired     = "proposal_expired"
	agentClubProposalSuperseded  = "proposal_superseded"
	agentClubProposalFailed      = "proposal_failed"
	agentClubProposalApplied     = "proposal_applied"
	agentClubProposalApplyFailed = "proposal_apply_failed"
)

var errAgentClubProposalStoreUnavailable = errors.New("agentclub proposal store unavailable")

type agentClubProposalRecord struct {
	SchemaVersion          int                `json:"schema_version"`
	Seq                    int64              `json:"seq"`
	SessionID              string             `json:"session_id"`
	Timestamp              time.Time          `json:"ts"`
	Kind                   string             `json:"kind"`
	Proposal               agentclub.Proposal `json:"proposal,omitempty"`
	ProposalID             string             `json:"proposal_id,omitempty"`
	ProposalHash           string             `json:"proposal_hash,omitempty"`
	DecisionID             string             `json:"decision_id,omitempty"`
	Decision               string             `json:"decision,omitempty"`
	ExpectedProposalHash   string             `json:"expected_proposal_hash,omitempty"`
	CommentSHA256          string             `json:"comment_sha256,omitempty"`
	NewProposalID          string             `json:"new_proposal_id,omitempty"`
	SupersededByProposalID string             `json:"superseded_by_proposal_id,omitempty"`
	ApplyID                string             `json:"apply_id,omitempty"`
	IdempotencyKeyHash     string             `json:"idempotency_key_hash,omitempty"`
	ApplyState             string             `json:"apply_state,omitempty"`
	ActionKind             string             `json:"action_kind,omitempty"`
	OutputRef              string             `json:"output_ref,omitempty"`
	PayloadSHA256          string             `json:"payload_sha256,omitempty"`
	DryRun                 bool               `json:"dry_run,omitempty"`
	Reason                 string             `json:"reason,omitempty"`
}

type replayedAgentClubProposals struct {
	lastSeq   int64
	proposals map[string]agentclub.Proposal
	records   []agentClubProposalRecord
}

func (s *Server) handleAgentClubProposalCreate(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	var req agentclub.ProposalCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	owner := proposalOwnerForRequest(session, r)
	resp, err := s.createAgentClubProposal(session, req, owner, "")
	if err != nil {
		writeAgentClubProposalError(w, err)
		return
	}
	status := http.StatusCreated
	if resp.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleAgentClubProposalList(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessRead)
	if !ok {
		return
	}
	resp, err := s.listAgentClubProposals(session)
	if err != nil {
		writeAgentClubProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentClubProposalDecision(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	proposalID, err := url.PathUnescape(strings.TrimSpace(r.PathValue("proposal_id")))
	if err != nil || strings.TrimSpace(proposalID) == "" {
		writeError(w, http.StatusBadRequest, "proposal_id required")
		return
	}
	var req agentclub.ProposalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	owner := proposalOwnerForRequest(session, r)
	resp, err := s.recordAgentClubProposalDecision(session, proposalID, req, owner)
	if err != nil {
		writeAgentClubProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentClubProposalApply(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	proposalID, err := url.PathUnescape(strings.TrimSpace(r.PathValue("proposal_id")))
	if err != nil || strings.TrimSpace(proposalID) == "" {
		writeError(w, http.StatusBadRequest, "proposal_id required")
		return
	}
	var req agentclub.ProposalApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	owner := proposalOwnerForRequest(session, r)
	resp, err := s.applyAgentClubProposal(session, proposalID, req, owner)
	if err != nil {
		writeAgentClubProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createAgentClubProposal(session *Session, req agentclub.ProposalCreateRequest, owner gatewayapi.SessionOwner, supersedes string) (agentclub.ProposalCreateResponse, error) {
	if s == nil || s.store == nil {
		return agentclub.ProposalCreateResponse{}, errAgentClubProposalStoreUnavailable
	}
	return s.store.CreateAgentClubProposal(session, req, owner, supersedes, time.Now().UTC())
}

func (s *Server) listAgentClubProposals(session *Session) (agentclub.ProposalListResponse, error) {
	if s == nil || s.store == nil {
		return agentclub.ProposalListResponse{}, errAgentClubProposalStoreUnavailable
	}
	return s.store.ListAgentClubProposals(session, time.Now().UTC())
}

func (s *Server) recordAgentClubProposalDecision(session *Session, proposalID string, req agentclub.ProposalDecisionRequest, owner gatewayapi.SessionOwner) (agentclub.ProposalDecisionResponse, error) {
	if s == nil || s.store == nil {
		return agentclub.ProposalDecisionResponse{}, errAgentClubProposalStoreUnavailable
	}
	return s.store.RecordAgentClubProposalDecision(session, proposalID, req, owner, time.Now().UTC())
}

func (s *Server) applyAgentClubProposal(session *Session, proposalID string, req agentclub.ProposalApplyRequest, owner gatewayapi.SessionOwner) (agentclub.ProposalApplyResponse, error) {
	if s == nil || s.store == nil {
		return agentclub.ProposalApplyResponse{}, errAgentClubProposalStoreUnavailable
	}
	return s.store.ApplyAgentClubProposal(session, proposalID, req, owner, time.Now().UTC())
}

func (s *sessionStore) CreateAgentClubProposal(session *Session, req agentclub.ProposalCreateRequest, owner gatewayapi.SessionOwner, supersedes string, now time.Time) (agentclub.ProposalCreateResponse, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return agentclub.ProposalCreateResponse{}, errAgentClubProposalStoreUnavailable
	}
	proposal, _, err := agentclub.NewProposal(req, session.ID, owner, now, supersedes)
	if err != nil {
		return agentclub.ProposalCreateResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.agentClubProposalsPathLocked(session)
	if err != nil {
		return agentclub.ProposalCreateResponse{}, err
	}
	replayed, err := replayAgentClubProposals(path, session.ID)
	if err != nil {
		return agentclub.ProposalCreateResponse{}, err
	}
	if existing, ok := replayed.proposals[proposal.ProposalID]; ok {
		if existing.ProposalHash != proposal.ProposalHash {
			return agentclub.ProposalCreateResponse{}, fmt.Errorf("%w: proposal id collision", agentclub.ErrInvalidProposal)
		}
		return agentclub.ProposalCreateResponse{SchemaVersion: agentclub.SchemaVersion, Proposal: existing, Duplicate: true}, nil
	}
	record := agentClubProposalRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           replayed.lastSeq + 1,
		SessionID:     session.ID,
		Timestamp:     now.UTC(),
		Kind:          agentClubProposalCreated,
		Proposal:      proposal,
		ProposalID:    proposal.ProposalID,
		ProposalHash:  proposal.ProposalHash,
	}
	if err := validateAgentClubProposalRecordForAppend(record); err != nil {
		return agentclub.ProposalCreateResponse{}, err
	}
	if err := eventlog.AppendJSONL(path, record); err != nil {
		return agentclub.ProposalCreateResponse{}, err
	}
	return agentclub.ProposalCreateResponse{SchemaVersion: agentclub.SchemaVersion, Proposal: proposal}, nil
}

func (s *sessionStore) ListAgentClubProposals(session *Session, now time.Time) (agentclub.ProposalListResponse, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return agentclub.ProposalListResponse{}, errAgentClubProposalStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.agentClubProposalsPathLocked(session)
	if err != nil {
		return agentclub.ProposalListResponse{}, err
	}
	replayed, err := replayAgentClubProposals(path, session.ID)
	if err != nil {
		return agentclub.ProposalListResponse{}, err
	}
	replayed, err = s.expireAgentClubProposalsLocked(path, session.ID, replayed, now)
	if err != nil {
		return agentclub.ProposalListResponse{}, err
	}
	proposals := make([]agentclub.Proposal, 0, len(replayed.proposals))
	for _, proposal := range replayed.proposals {
		proposals = append(proposals, proposal)
	}
	agentclub.SortProposals(proposals)
	return agentclub.ProposalListResponse{SchemaVersion: agentclub.SchemaVersion, Proposals: proposals}, nil
}

func (s *sessionStore) RecordAgentClubProposalDecision(session *Session, proposalID string, req agentclub.ProposalDecisionRequest, owner gatewayapi.SessionOwner, now time.Time) (agentclub.ProposalDecisionResponse, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return agentclub.ProposalDecisionResponse{}, errAgentClubProposalStoreUnavailable
	}
	normalized, err := agentclub.NormalizeProposalDecision(req)
	if err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.agentClubProposalsPathLocked(session)
	if err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	replayed, err := replayAgentClubProposals(path, session.ID)
	if err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	replayed, err = s.expireAgentClubProposalsLocked(path, session.ID, replayed, now)
	if err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	proposalID = strings.TrimSpace(proposalID)
	proposal, ok := replayed.proposals[proposalID]
	if !ok {
		return agentclub.ProposalDecisionResponse{}, fmt.Errorf("%w: %s", agentclub.ErrProposalNotFound, proposalID)
	}
	if proposal.State != agentclub.ProposalStatePending {
		return agentclub.ProposalDecisionResponse{}, fmt.Errorf("%w: %s is %s", agentclub.ErrProposalNotPending, proposalID, proposal.State)
	}
	if proposal.ProposalHash != normalized.ExpectedProposalHash {
		return agentclub.ProposalDecisionResponse{}, agentclub.ErrProposalHashMismatch
	}
	if proposalOwner := normalizeSessionOwner(proposal.Owner); proposalOwner != (gatewayapi.SessionOwner{}) && normalizeSessionOwner(owner) != proposalOwner {
		return agentclub.ProposalDecisionResponse{}, fmt.Errorf("session owner scope mismatch")
	}
	decisionID := agentclub.DecisionID(proposal.ProposalID, normalized.Action, proposal.ProposalHash, now)
	var newProposal *agentclub.Proposal
	if normalized.Action == agentclub.ProposalDecisionEdit {
		created, _, err := agentclub.NewProposal(*normalized.Edit, session.ID, owner, now, proposal.ProposalID)
		if err != nil {
			return agentclub.ProposalDecisionResponse{}, err
		}
		if existing, ok := replayed.proposals[created.ProposalID]; ok {
			if existing.ProposalHash != created.ProposalHash {
				return agentclub.ProposalDecisionResponse{}, fmt.Errorf("%w: proposal id collision", agentclub.ErrInvalidProposal)
			}
			created = existing
		} else {
			createRecord := agentClubProposalRecord{
				SchemaVersion: gatewaySessionSchemaVersion,
				Seq:           replayed.lastSeq + 1,
				SessionID:     session.ID,
				Timestamp:     now.UTC(),
				Kind:          agentClubProposalCreated,
				Proposal:      created,
				ProposalID:    created.ProposalID,
				ProposalHash:  created.ProposalHash,
			}
			if err := validateAgentClubProposalRecordForAppend(createRecord); err != nil {
				return agentclub.ProposalDecisionResponse{}, err
			}
			if err := eventlog.AppendJSONL(path, createRecord); err != nil {
				return agentclub.ProposalDecisionResponse{}, err
			}
			replayed.lastSeq = createRecord.Seq
			replayed.proposals[created.ProposalID] = created
		}
		newProposal = &created
	}
	state := decisionState(normalized.Action)
	record := agentClubProposalRecord{
		SchemaVersion:          gatewaySessionSchemaVersion,
		Seq:                    replayed.lastSeq + 1,
		SessionID:              session.ID,
		Timestamp:              now.UTC(),
		Kind:                   agentClubDecisionRecorded,
		ProposalID:             proposal.ProposalID,
		ProposalHash:           proposal.ProposalHash,
		DecisionID:             decisionID,
		Decision:               normalized.Action,
		ExpectedProposalHash:   normalized.ExpectedProposalHash,
		CommentSHA256:          hashComment(normalized.Comment),
		NewProposalID:          newProposalID(newProposal),
		SupersededByProposalID: newProposalID(newProposal),
	}
	if err := validateAgentClubProposalRecordForAppend(record); err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	if err := eventlog.AppendJSONL(path, record); err != nil {
		return agentclub.ProposalDecisionResponse{}, err
	}
	if normalized.Action == agentclub.ProposalDecisionEdit {
		supersedeRecord := agentClubProposalRecord{
			SchemaVersion:          gatewaySessionSchemaVersion,
			Seq:                    record.Seq + 1,
			SessionID:              session.ID,
			Timestamp:              now.UTC(),
			Kind:                   agentClubProposalSuperseded,
			ProposalID:             proposal.ProposalID,
			ProposalHash:           proposal.ProposalHash,
			DecisionID:             decisionID,
			Decision:               normalized.Action,
			ExpectedProposalHash:   normalized.ExpectedProposalHash,
			NewProposalID:          newProposalID(newProposal),
			SupersededByProposalID: newProposalID(newProposal),
		}
		if err := validateAgentClubProposalRecordForAppend(supersedeRecord); err != nil {
			return agentclub.ProposalDecisionResponse{}, err
		}
		if err := eventlog.AppendJSONL(path, supersedeRecord); err != nil {
			return agentclub.ProposalDecisionResponse{}, err
		}
	}
	proposal.State = state
	proposal.UpdatedAt = now.UTC()
	if newProposal != nil {
		proposal.SupersededByProposalID = newProposal.ProposalID
	}
	return agentclub.ProposalDecisionResponse{
		SchemaVersion: agentclub.SchemaVersion,
		DecisionID:    decisionID,
		Action:        normalized.Action,
		Proposal:      proposal,
		NewProposal:   newProposal,
	}, nil
}

func (s *sessionStore) ApplyAgentClubProposal(session *Session, proposalID string, req agentclub.ProposalApplyRequest, owner gatewayapi.SessionOwner, now time.Time) (agentclub.ProposalApplyResponse, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return agentclub.ProposalApplyResponse{}, errAgentClubProposalStoreUnavailable
	}
	normalized, err := agentclub.NormalizeProposalApply(req)
	if err != nil {
		return agentclub.ProposalApplyResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.agentClubProposalsPathLocked(session)
	if err != nil {
		return agentclub.ProposalApplyResponse{}, err
	}
	replayed, err := replayAgentClubProposals(path, session.ID)
	if err != nil {
		return agentclub.ProposalApplyResponse{}, err
	}
	replayed, err = s.expireAgentClubProposalsLocked(path, session.ID, replayed, now)
	if err != nil {
		return agentclub.ProposalApplyResponse{}, err
	}
	proposalID = strings.TrimSpace(proposalID)
	proposal, ok := replayed.proposals[proposalID]
	if !ok {
		return agentclub.ProposalApplyResponse{}, fmt.Errorf("%w: %s", agentclub.ErrProposalNotFound, proposalID)
	}
	if proposal.ProposalHash != normalized.ExpectedProposalHash {
		return agentclub.ProposalApplyResponse{}, agentclub.ErrProposalHashMismatch
	}
	if proposalOwner := normalizeSessionOwner(proposal.Owner); proposalOwner != (gatewayapi.SessionOwner{}) && normalizeSessionOwner(owner) != proposalOwner {
		return agentclub.ProposalApplyResponse{}, fmt.Errorf("session owner scope mismatch")
	}
	applyID := agentclub.ProposalApplyID(proposal.ProposalID, proposal.ProposalHash, normalized.IdempotencyKey)
	if existing, ok := latestAgentClubApplyRecord(replayed.records, proposal.ProposalID, proposal.ProposalHash, applyID); ok {
		return agentClubApplyResponseFromRecord(existing, true), nil
	}
	if existing, ok := latestSuccessfulAgentClubApplyRecord(replayed.records, proposal.ProposalID, proposal.ProposalHash); ok {
		return agentClubApplyResponseFromRecord(existing, true), nil
	}
	if proposal.State != agentclub.ProposalStateApproved {
		return agentclub.ProposalApplyResponse{}, fmt.Errorf("%w: %s is %s", agentclub.ErrProposalNotApproved, proposalID, proposal.State)
	}
	decisionID := latestApprovalDecisionID(replayed.records, proposal.ProposalID, proposal.ProposalHash)
	if decisionID == "" {
		return agentclub.ProposalApplyResponse{}, fmt.Errorf("%w: approval evidence missing", agentclub.ErrProposalNotApproved)
	}
	outputRef := agentClubApplyOutputRef(applyID)
	base := agentClubProposalRecord{
		SchemaVersion:        gatewaySessionSchemaVersion,
		Seq:                  replayed.lastSeq + 1,
		SessionID:            session.ID,
		Timestamp:            now.UTC(),
		ProposalID:           proposal.ProposalID,
		ProposalHash:         proposal.ProposalHash,
		DecisionID:           decisionID,
		ExpectedProposalHash: normalized.ExpectedProposalHash,
		ApplyID:              applyID,
		IdempotencyKeyHash:   hashIngressAuditValue(normalized.IdempotencyKey),
		ActionKind:           proposal.ActionKind,
		OutputRef:            outputRef,
		PayloadSHA256:        proposal.PayloadSHA256,
		DryRun:               normalized.DryRun,
	}
	if normalized.DryRun {
		return agentclub.ProposalApplyResponse{
			SchemaVersion: agentclub.SchemaVersion,
			ProposalID:    proposal.ProposalID,
			ProposalHash:  proposal.ProposalHash,
			ApplyID:       applyID,
			State:         agentclub.ProposalApplyStateDryRun,
			ActionKind:    proposal.ActionKind,
			DryRun:        true,
			OutputRef:     outputRef,
			PayloadSHA256: proposal.PayloadSHA256,
			RunDispatched: false,
		}, nil
	}
	record := base
	switch proposal.ActionKind {
	case agentclub.ProposalActionRecordNote:
		record.Kind = agentClubProposalApplied
		record.ApplyState = agentclub.ProposalStateApplied
	default:
		record.Kind = agentClubProposalApplyFailed
		record.ApplyState = agentclub.ProposalStateApplyFailed
		record.Reason = "unsupported action_kind"
	}
	if err := validateAgentClubProposalRecordForAppend(record); err != nil {
		return agentclub.ProposalApplyResponse{}, err
	}
	if err := eventlog.AppendJSONL(path, record); err != nil {
		return agentclub.ProposalApplyResponse{}, err
	}
	return agentClubApplyResponseFromRecord(record, false), nil
}

func (s *sessionStore) expireAgentClubProposalsLocked(path, sessionID string, replayed replayedAgentClubProposals, now time.Time) (replayedAgentClubProposals, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var expired []agentclub.Proposal
	for _, proposal := range replayed.proposals {
		if agentclub.ProposalExpired(proposal, now) {
			expired = append(expired, proposal)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].ProposalID < expired[j].ProposalID })
	for _, proposal := range expired {
		record := agentClubProposalRecord{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           replayed.lastSeq + 1,
			SessionID:     sessionID,
			Timestamp:     now.UTC(),
			Kind:          agentClubProposalExpired,
			ProposalID:    proposal.ProposalID,
			ProposalHash:  proposal.ProposalHash,
			Reason:        "expires_at reached",
		}
		if err := validateAgentClubProposalRecordForAppend(record); err != nil {
			return replayed, err
		}
		if err := eventlog.AppendJSONL(path, record); err != nil {
			return replayed, err
		}
		replayed.lastSeq = record.Seq
		proposal.State = agentclub.ProposalStateExpired
		proposal.UpdatedAt = now.UTC()
		replayed.proposals[proposal.ProposalID] = proposal
	}
	return replayed, nil
}

func (s *sessionStore) agentClubProposalsPathLocked(session *Session) (string, error) {
	id, err := cleanSessionID(session.ID)
	if err != nil {
		return "", err
	}
	sessionDir := filepath.Join(s.dir, id)
	if err := ensurePrivateGatewayDir(sessionDir); err != nil {
		return "", err
	}
	return filepath.Join(sessionDir, agentClubProposalsJSONLName), nil
}

func replayAgentClubProposals(path, sessionID string) (replayedAgentClubProposals, error) {
	out := replayedAgentClubProposals{proposals: map[string]agentclub.Proposal{}}
	expectedSeq := int64(1)
	err := eventlog.ReplayJSONL[agentClubProposalRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[agentClubProposalRecord]) error {
		record := item.Value
		recordNo := expectedSeq
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != expectedSeq {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq))
		}
		if err := validateAgentClubProposalRecord(record, sessionID); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		applyAgentClubProposalRecord(out.proposals, record)
		out.records = append(out.records, record)
		out.lastSeq = record.Seq
		expectedSeq++
		return nil
	})
	return out, err
}

func applyAgentClubProposalRecord(proposals map[string]agentclub.Proposal, record agentClubProposalRecord) {
	switch record.Kind {
	case agentClubProposalCreated:
		proposals[record.Proposal.ProposalID] = record.Proposal
	case agentClubDecisionRecorded:
		proposal := proposals[record.ProposalID]
		proposal.State = decisionState(record.Decision)
		proposal.UpdatedAt = record.Timestamp
		proposals[record.ProposalID] = proposal
	case agentClubProposalSuperseded:
		proposal := proposals[record.ProposalID]
		proposal.State = agentclub.ProposalStateSuperseded
		proposal.UpdatedAt = record.Timestamp
		proposal.SupersededByProposalID = record.SupersededByProposalID
		proposals[record.ProposalID] = proposal
	case agentClubProposalExpired:
		proposal := proposals[record.ProposalID]
		proposal.State = agentclub.ProposalStateExpired
		proposal.UpdatedAt = record.Timestamp
		proposals[record.ProposalID] = proposal
	case agentClubProposalFailed:
		proposal := proposals[record.ProposalID]
		proposal.State = agentclub.ProposalStateFailed
		proposal.UpdatedAt = record.Timestamp
		proposals[record.ProposalID] = proposal
	case agentClubProposalApplied, agentClubProposalApplyFailed:
		proposal := proposals[record.ProposalID]
		proposal.State = record.ApplyState
		proposal.UpdatedAt = record.Timestamp
		proposals[record.ProposalID] = proposal
	}
}

func validateAgentClubProposalRecordForAppend(record agentClubProposalRecord) error {
	if record.SchemaVersion == 0 {
		return fmt.Errorf("missing schema_version")
	}
	return validateAgentClubProposalRecord(record, record.SessionID)
}

func validateAgentClubProposalRecord(record agentClubProposalRecord, sessionID string) error {
	switch record.Kind {
	case agentClubProposalCreated, agentClubDecisionRecorded, agentClubProposalExpired, agentClubProposalSuperseded, agentClubProposalFailed, agentClubProposalApplied, agentClubProposalApplyFailed:
	default:
		return fmt.Errorf("unsupported proposal record kind %q", record.Kind)
	}
	if strings.TrimSpace(record.SessionID) == "" || strings.TrimSpace(record.SessionID) != strings.TrimSpace(sessionID) {
		return fmt.Errorf("proposal record session mismatch")
	}
	if record.Kind == agentClubProposalCreated {
		if record.Proposal.ProposalID == "" || record.Proposal.ProposalHash == "" || record.Proposal.SessionID != sessionID {
			return fmt.Errorf("invalid proposal_created record")
		}
	}
	if record.Kind != agentClubProposalCreated && strings.TrimSpace(record.ProposalID) == "" {
		return fmt.Errorf("proposal record missing proposal_id")
	}
	if record.Kind == agentClubProposalApplied || record.Kind == agentClubProposalApplyFailed {
		if strings.TrimSpace(record.ApplyID) == "" || strings.TrimSpace(record.ActionKind) == "" || strings.TrimSpace(record.PayloadSHA256) == "" || strings.TrimSpace(record.ProposalHash) == "" {
			return fmt.Errorf("invalid proposal apply record")
		}
		if record.Kind == agentClubProposalApplied && record.ApplyState != agentclub.ProposalStateApplied {
			return fmt.Errorf("invalid proposal applied state")
		}
		if record.Kind == agentClubProposalApplyFailed && record.ApplyState != agentclub.ProposalStateApplyFailed {
			return fmt.Errorf("invalid proposal apply_failed state")
		}
	}
	return nil
}

func latestAgentClubApplyRecord(records []agentClubProposalRecord, proposalID, proposalHash, applyID string) (agentClubProposalRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.ProposalID == proposalID && record.ProposalHash == proposalHash && record.ApplyID == applyID && isAgentClubApplyRecord(record) {
			return record, true
		}
	}
	return agentClubProposalRecord{}, false
}

func latestSuccessfulAgentClubApplyRecord(records []agentClubProposalRecord, proposalID, proposalHash string) (agentClubProposalRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.ProposalID == proposalID && record.ProposalHash == proposalHash && record.Kind == agentClubProposalApplied && record.ApplyState == agentclub.ProposalStateApplied {
			return record, true
		}
	}
	return agentClubProposalRecord{}, false
}

func latestApprovalDecisionID(records []agentClubProposalRecord, proposalID, proposalHash string) string {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.Kind == agentClubDecisionRecorded && record.ProposalID == proposalID && record.ProposalHash == proposalHash && record.Decision == agentclub.ProposalDecisionApprove {
			return record.DecisionID
		}
	}
	return ""
}

func isAgentClubApplyRecord(record agentClubProposalRecord) bool {
	return record.Kind == agentClubProposalApplied || record.Kind == agentClubProposalApplyFailed
}

func agentClubApplyResponseFromRecord(record agentClubProposalRecord, duplicate bool) agentclub.ProposalApplyResponse {
	return agentclub.ProposalApplyResponse{
		SchemaVersion: agentclub.SchemaVersion,
		ProposalID:    record.ProposalID,
		ProposalHash:  record.ProposalHash,
		ApplyID:       record.ApplyID,
		State:         record.ApplyState,
		ActionKind:    record.ActionKind,
		DryRun:        record.DryRun,
		OutputRef:     record.OutputRef,
		PayloadSHA256: record.PayloadSHA256,
		RunDispatched: false,
		Duplicate:     duplicate,
	}
}

func agentClubApplyOutputRef(applyID string) string {
	return "agentclub:apply:" + strings.TrimSpace(applyID)
}

func proposalOwnerForRequest(session *Session, r *http.Request) gatewayapi.SessionOwner {
	actor := sessionOwnerFromRequest(r)
	if !sessionOwnerEmpty(actor) {
		return actor
	}
	if session != nil {
		return normalizeSessionOwner(session.Owner)
	}
	return gatewayapi.SessionOwner{}
}

func writeAgentClubProposalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAgentClubProposalStoreUnavailable):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agentclub.ErrUnsupportedSchemaVersion),
		errors.Is(err, agentclub.ErrInvalidIdentifier),
		errors.Is(err, agentclub.ErrInvalidProposal),
		errors.Is(err, agentclub.ErrInvalidDecision),
		errors.Is(err, agentclub.ErrInvalidApply):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agentclub.ErrProposalHashMismatch),
		errors.Is(err, agentclub.ErrProposalNotPending),
		errors.Is(err, agentclub.ErrProposalNotApproved):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agentclub.ErrProposalNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case strings.Contains(err.Error(), "session owner scope mismatch"):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func decisionState(action string) string {
	switch action {
	case agentclub.ProposalDecisionApprove:
		return agentclub.ProposalStateApproved
	case agentclub.ProposalDecisionReject:
		return agentclub.ProposalStateRejected
	case agentclub.ProposalDecisionEdit:
		return agentclub.ProposalStateSuperseded
	default:
		return agentclub.ProposalStateFailed
	}
}

func hashComment(comment string) string {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return ""
	}
	return hashIngressAuditValue(comment)
}

func newProposalID(proposal *agentclub.Proposal) string {
	if proposal == nil {
		return ""
	}
	return proposal.ProposalID
}
