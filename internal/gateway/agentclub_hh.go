package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub/hhapplicant"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

type hhApplicantReviewQueueAdapter interface {
	Capture(context.Context, hhapplicant.ReviewQueueRequest) (hhapplicant.ReviewQueueCapture, error)
}

func (s *Server) handleHHReviewQueueIngress(w http.ResponseWriter, r *http.Request) {
	var req gatewayapi.HHReviewQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	profile, err := hhapplicant.NormalizeProfile(req.Profile)
	if err != nil {
		writeHHReviewQueueError(w, err)
		return
	}
	owner, err := hhapplicant.OwnerForProfile(profile)
	if err != nil {
		writeHHReviewQueueError(w, err)
		return
	}
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	if err := authorizeSessionAccess(session, owner, sessionAccessMutate); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	capture, err := s.hhReviewQueueAdapter().Capture(r.Context(), hhapplicant.ReviewQueueRequest{
		SessionID: strings.TrimSpace(r.PathValue("id")),
		Profile:   profile,
		Limit:     req.Limit,
		RepoRoot:  req.RepoRoot,
	})
	if err != nil {
		writeHHReviewQueueError(w, err)
		return
	}
	result, err := s.AdmitIngressEvent(r.Context(), capture.Event, capture.Rule)
	if err != nil {
		writeHHReviewQueueError(w, err)
		return
	}
	resp := gatewayapi.HHReviewQueueResponse{
		Input:               result.Input,
		InputID:             result.Input.InputID,
		State:               result.Input.State,
		Duplicate:           result.Input.Duplicate,
		Admitted:            result.Decision.Admitted,
		AuditStatus:         result.Decision.Reason,
		TargetSessionID:     result.Decision.TargetSessionID,
		ClientID:            result.Decision.Request.ClientID,
		ClientType:          result.Decision.Request.ClientType,
		Profile:             capture.Profile,
		Limit:               capture.Limit,
		CommandName:         capture.CommandName,
		CommandArgs:         append([]string(nil), capture.CommandArgs...),
		OutputSHA256:        capture.OutputSHA256,
		PayloadSHA256:       result.Decision.PayloadSHA256,
		ExternalEventIDHash: hashReviewQueueExternalEventID(capture.ExternalEventID),
		ReviewItemCount:     capture.ReviewItemCount,
		MetadataKeys:        append([]string(nil), result.Decision.Metadata.Keys...),
		RunDispatched:       false,
	}
	if !result.Decision.Admitted {
		resp.AuditReason = result.Decision.Reason
	}
	status := http.StatusCreated
	if result.Input.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
}

func (s *Server) hhReviewQueueAdapter() hhApplicantReviewQueueAdapter {
	if s != nil && s.hhApplicantReviewQueue != nil {
		return s.hhApplicantReviewQueue
	}
	adapter := hhapplicant.DefaultAdapter()
	return adapter
}

func writeHHReviewQueueError(w http.ResponseWriter, err error) {
	var conflict *sessionInputConflictError
	var validation *sessionInputValidationError
	var commandErr *hhapplicant.CommandError
	switch {
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, hhapplicant.ErrInvalidLimit),
		errors.Is(err, hhapplicant.ErrInvalidProfile),
		errors.Is(err, hhapplicant.ErrInvalidRepoRoot):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, hhapplicant.ErrCommandTimeout):
		writeError(w, http.StatusGatewayTimeout, err.Error())
	case errors.Is(err, hhapplicant.ErrOutputLimitExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.As(err, &commandErr), errors.Is(err, hhapplicant.ErrReviewCommandFailed):
		writeError(w, http.StatusBadGateway, err.Error())
	case strings.Contains(err.Error(), "session not found"):
		writeError(w, http.StatusNotFound, err.Error())
	case strings.Contains(err.Error(), "session owner scope mismatch"),
		strings.Contains(err.Error(), "legacy unowned"):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func hashReviewQueueExternalEventID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
