package agentclub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

const (
	ProposalPolicyVersion = "agentclub.safe_output.v0"

	ProposalStatePending     = "pending"
	ProposalStateApproved    = "approved"
	ProposalStateRejected    = "rejected"
	ProposalStateExpired     = "expired"
	ProposalStateSuperseded  = "superseded"
	ProposalStateFailed      = "failed"
	ProposalStateApplied     = "applied"
	ProposalStateApplyFailed = "apply_failed"

	ProposalDecisionApprove = "approve"
	ProposalDecisionReject  = "reject"
	ProposalDecisionEdit    = "edit"

	ProposalActionRecordNote = "record_note"
	ProposalApplyStateDryRun = "dry_run"

	MaxProposalPreviewBytes = 4096
	MaxProposalPayloadBytes = 64 << 10
	MaxProposalCommentBytes = 1024
)

var (
	ErrInvalidProposal      = errors.New("invalid agentclub proposal")
	ErrInvalidDecision      = errors.New("invalid agentclub proposal decision")
	ErrProposalHashMismatch = errors.New("agentclub proposal hash mismatch")
	ErrProposalNotPending   = errors.New("agentclub proposal is not pending")
	ErrProposalNotApproved  = errors.New("agentclub proposal is not approved")
	ErrProposalNotFound     = errors.New("agentclub proposal not found")
	ErrInvalidApply         = errors.New("invalid agentclub proposal apply")
)

type ProposalCreateRequest struct {
	SchemaVersion int               `json:"schema_version"`
	Source        string            `json:"source"`
	Capability    string            `json:"capability"`
	ActionKind    string            `json:"action_kind"`
	Risk          string            `json:"risk"`
	Preview       string            `json:"preview,omitempty"`
	Payload       json.RawMessage   `json:"payload,omitempty"`
	OutputRef     string            `json:"output_ref,omitempty"`
	TargetScope   string            `json:"target_scope,omitempty"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type Proposal struct {
	SchemaVersion          int                     `json:"schema_version"`
	ProposalID             string                  `json:"proposal_id"`
	SessionID              string                  `json:"session_id"`
	Owner                  gatewayapi.SessionOwner `json:"owner,omitempty"`
	Source                 string                  `json:"source"`
	Capability             string                  `json:"capability"`
	ActionKind             string                  `json:"action_kind"`
	Risk                   string                  `json:"risk"`
	State                  string                  `json:"state"`
	Preview                string                  `json:"preview,omitempty"`
	PayloadSHA256          string                  `json:"payload_sha256"`
	OutputRef              string                  `json:"output_ref,omitempty"`
	OutputRefHash          string                  `json:"output_ref_hash,omitempty"`
	TargetScope            string                  `json:"target_scope,omitempty"`
	PolicyVersion          string                  `json:"policy_version"`
	ProposalHash           string                  `json:"proposal_hash"`
	SupersedesProposalID   string                  `json:"supersedes_proposal_id,omitempty"`
	SupersededByProposalID string                  `json:"superseded_by_proposal_id,omitempty"`
	CreatedAt              time.Time               `json:"created_at"`
	UpdatedAt              time.Time               `json:"updated_at"`
	ExpiresAt              *time.Time              `json:"expires_at,omitempty"`
	MetadataKeys           []string                `json:"metadata_keys,omitempty"`
}

type ProposalCreateResponse struct {
	SchemaVersion int      `json:"schema_version"`
	Proposal      Proposal `json:"proposal"`
	Duplicate     bool     `json:"duplicate,omitempty"`
}

type ProposalListResponse struct {
	SchemaVersion int        `json:"schema_version"`
	Proposals     []Proposal `json:"proposals"`
}

type ProposalDecisionRequest struct {
	SchemaVersion        int                    `json:"schema_version"`
	Action               string                 `json:"action"`
	ExpectedProposalHash string                 `json:"expected_proposal_hash"`
	Comment              string                 `json:"comment,omitempty"`
	Edit                 *ProposalCreateRequest `json:"edit,omitempty"`
}

type ProposalDecisionResponse struct {
	SchemaVersion int       `json:"schema_version"`
	DecisionID    string    `json:"decision_id"`
	Action        string    `json:"action"`
	Proposal      Proposal  `json:"proposal"`
	NewProposal   *Proposal `json:"new_proposal,omitempty"`
}

type ProposalApplyRequest struct {
	SchemaVersion        int    `json:"schema_version"`
	ExpectedProposalHash string `json:"expected_proposal_hash"`
	IdempotencyKey       string `json:"idempotency_key"`
	DryRun               bool   `json:"dry_run,omitempty"`
}

type ProposalApplyResponse struct {
	SchemaVersion int    `json:"schema_version"`
	ProposalID    string `json:"proposal_id"`
	ProposalHash  string `json:"proposal_hash"`
	ApplyID       string `json:"apply_id"`
	State         string `json:"state"`
	ActionKind    string `json:"action_kind"`
	DryRun        bool   `json:"dry_run,omitempty"`
	OutputRef     string `json:"output_ref,omitempty"`
	PayloadSHA256 string `json:"payload_sha256,omitempty"`
	RunDispatched bool   `json:"run_dispatched"`
	Duplicate     bool   `json:"duplicate,omitempty"`
}

func NewProposal(req ProposalCreateRequest, sessionID string, owner gatewayapi.SessionOwner, now time.Time, supersedes string) (Proposal, []byte, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Proposal{}, nil, fmt.Errorf("%w: session_id required", ErrInvalidProposal)
	}
	if req.SchemaVersion != SchemaVersion {
		return Proposal{}, nil, fmt.Errorf("%w: got schema_version %d want %d", ErrUnsupportedSchemaVersion, req.SchemaVersion, SchemaVersion)
	}
	var err error
	if req.Source, err = normalizeIdentifier("source", req.Source); err != nil {
		return Proposal{}, nil, err
	}
	if req.Capability, err = normalizeIdentifier("capability", req.Capability); err != nil {
		return Proposal{}, nil, err
	}
	if req.ActionKind, err = normalizeIdentifier("action_kind", req.ActionKind); err != nil {
		return Proposal{}, nil, err
	}
	req.Risk = strings.TrimSpace(req.Risk)
	if req.Risk == "" {
		req.Risk = RiskUnknown
	}
	if !allowedRiskClasses[req.Risk] {
		return Proposal{}, nil, fmt.Errorf("%w: unsupported risk %q", ErrInvalidProposal, req.Risk)
	}
	req.Preview = strings.TrimSpace(req.Preview)
	if len([]byte(req.Preview)) > MaxProposalPreviewBytes {
		return Proposal{}, nil, fmt.Errorf("%w: preview too large", ErrInvalidProposal)
	}
	req.OutputRef = strings.TrimSpace(req.OutputRef)
	if req.Preview == "" && req.OutputRef == "" {
		return Proposal{}, nil, fmt.Errorf("%w: preview or output_ref required", ErrInvalidProposal)
	}
	req.TargetScope = strings.TrimSpace(req.TargetScope)
	if len(req.TargetScope) > 512 {
		return Proposal{}, nil, fmt.Errorf("%w: target_scope too long", ErrInvalidProposal)
	}
	payload, err := normalizeProposalPayload(req.Payload)
	if err != nil {
		return Proposal{}, nil, err
	}
	metadata, err := ingress.SanitizeMetadata(req.Metadata)
	if err != nil {
		return Proposal{}, nil, fmt.Errorf("%w: %v", ErrInvalidProposal, err)
	}
	payloadHash := ingress.PayloadSHA256(payload)
	outputRefHash := hashString(req.OutputRef)
	previewHash := hashString(req.Preview)
	proposalHash := proposalHashFor(proposalHashParts{
		SessionID:     sessionID,
		Source:        req.Source,
		Capability:    req.Capability,
		ActionKind:    req.ActionKind,
		Risk:          req.Risk,
		TargetScope:   req.TargetScope,
		PayloadSHA256: payloadHash,
		PreviewSHA256: previewHash,
		OutputRefHash: outputRefHash,
		PolicyVersion: ProposalPolicyVersion,
	})
	proposalID := "proposal-" + proposalHash[:32]
	if supersedes, err = normalizeOptionalIdentifier("supersedes_proposal_id", supersedes); err != nil {
		return Proposal{}, nil, err
	}
	metadataKeys := metadataKeys(metadata)
	return Proposal{
		SchemaVersion:        SchemaVersion,
		ProposalID:           proposalID,
		SessionID:            sessionID,
		Owner:                normalizeOwner(owner),
		Source:               req.Source,
		Capability:           req.Capability,
		ActionKind:           req.ActionKind,
		Risk:                 req.Risk,
		State:                ProposalStatePending,
		Preview:              req.Preview,
		PayloadSHA256:        payloadHash,
		OutputRef:            req.OutputRef,
		OutputRefHash:        outputRefHash,
		TargetScope:          req.TargetScope,
		PolicyVersion:        ProposalPolicyVersion,
		ProposalHash:         proposalHash,
		SupersedesProposalID: supersedes,
		CreatedAt:            now.UTC(),
		UpdatedAt:            now.UTC(),
		ExpiresAt:            cloneTimePtr(req.ExpiresAt),
		MetadataKeys:         metadataKeys,
	}, payload, nil
}

func NormalizeProposalDecision(req ProposalDecisionRequest) (ProposalDecisionRequest, error) {
	if req.SchemaVersion != SchemaVersion {
		return ProposalDecisionRequest{}, fmt.Errorf("%w: got schema_version %d want %d", ErrUnsupportedSchemaVersion, req.SchemaVersion, SchemaVersion)
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	switch req.Action {
	case ProposalDecisionApprove, ProposalDecisionReject, ProposalDecisionEdit:
	default:
		return ProposalDecisionRequest{}, fmt.Errorf("%w: unsupported action %q", ErrInvalidDecision, req.Action)
	}
	req.ExpectedProposalHash = strings.ToLower(strings.TrimSpace(req.ExpectedProposalHash))
	if !isSHA256Hex(req.ExpectedProposalHash) {
		return ProposalDecisionRequest{}, fmt.Errorf("%w: expected_proposal_hash required", ErrInvalidDecision)
	}
	req.Comment = strings.TrimSpace(req.Comment)
	if len([]byte(req.Comment)) > MaxProposalCommentBytes {
		return ProposalDecisionRequest{}, fmt.Errorf("%w: comment too large", ErrInvalidDecision)
	}
	if req.Action == ProposalDecisionEdit && req.Edit == nil {
		return ProposalDecisionRequest{}, fmt.Errorf("%w: edit proposal required", ErrInvalidDecision)
	}
	if req.Action != ProposalDecisionEdit {
		req.Edit = nil
	}
	return req, nil
}

func NormalizeProposalApply(req ProposalApplyRequest) (ProposalApplyRequest, error) {
	if req.SchemaVersion != SchemaVersion {
		return ProposalApplyRequest{}, fmt.Errorf("%w: got schema_version %d want %d", ErrUnsupportedSchemaVersion, req.SchemaVersion, SchemaVersion)
	}
	req.ExpectedProposalHash = strings.ToLower(strings.TrimSpace(req.ExpectedProposalHash))
	if !isSHA256Hex(req.ExpectedProposalHash) {
		return ProposalApplyRequest{}, fmt.Errorf("%w: expected_proposal_hash required", ErrInvalidApply)
	}
	var err error
	if req.IdempotencyKey, err = normalizeIdentifier("idempotency_key", req.IdempotencyKey); err != nil {
		return ProposalApplyRequest{}, fmt.Errorf("%w: %v", ErrInvalidApply, err)
	}
	return req, nil
}

func ProposalExpired(proposal Proposal, now time.Time) bool {
	if proposal.ExpiresAt == nil || proposal.State != ProposalStatePending {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !proposal.ExpiresAt.After(now.UTC())
}

func DecisionID(proposalID, action, proposalHash string, now time.Time) string {
	now = now.UTC()
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(proposalID),
		strings.TrimSpace(action),
		strings.TrimSpace(proposalHash),
		now.Format(time.RFC3339Nano),
	}, "\x00")))
	return "decision-" + hex.EncodeToString(sum[:])[:32]
}

func ProposalApplyID(proposalID, proposalHash, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(proposalID),
		strings.ToLower(strings.TrimSpace(proposalHash)),
		strings.TrimSpace(idempotencyKey),
	}, "\x00")))
	return "apply-" + hex.EncodeToString(sum[:])[:32]
}

func normalizeProposalPayload(raw json.RawMessage) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	if len(raw) > MaxProposalPayloadBytes {
		return nil, fmt.Errorf("%w: payload too large", ErrInvalidProposal)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("%w: payload must be valid JSON", ErrInvalidProposal)
	}
	return append([]byte(nil), compact.Bytes()...), nil
}

type proposalHashParts struct {
	SessionID     string `json:"session_id"`
	Source        string `json:"source"`
	Capability    string `json:"capability"`
	ActionKind    string `json:"action_kind"`
	Risk          string `json:"risk"`
	TargetScope   string `json:"target_scope"`
	PayloadSHA256 string `json:"payload_sha256"`
	PreviewSHA256 string `json:"preview_sha256"`
	OutputRefHash string `json:"output_ref_hash"`
	PolicyVersion string `json:"policy_version"`
}

func proposalHashFor(parts proposalHashParts) string {
	body, _ := json.Marshal(parts)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func hashString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func isSHA256Hex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func SortProposals(proposals []Proposal) {
	sort.Slice(proposals, func(i, j int) bool {
		if proposals[i].CreatedAt.Equal(proposals[j].CreatedAt) {
			return proposals[i].ProposalID < proposals[j].ProposalID
		}
		return proposals[i].CreatedAt.Before(proposals[j].CreatedAt)
	})
}
