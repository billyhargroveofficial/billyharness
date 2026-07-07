package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
)

func (s *Server) handleAgentClubEventIngress(w http.ResponseWriter, r *http.Request) {
	actor := sessionOwnerFromRequest(r)
	if sessionOwnerEmpty(actor) {
		writeError(w, http.StatusBadRequest, "agentclub ingress owner headers required")
		return
	}
	if actor.ClientType != "ingress" {
		writeError(w, http.StatusForbidden, "agentclub ingress requires client_type=ingress")
		return
	}
	session, ok := s.sessionForRequest(w, r, sessionAccessMutate)
	if !ok {
		return
	}
	var req agentclub.EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	mapping, err := agentclub.MapToIngress(req, session.ID, actor)
	if err != nil {
		writeAgentClubEventError(w, err)
		return
	}
	result, err := s.AdmitIngressEvent(r.Context(), mapping.Event, mapping.Rule)
	if err != nil {
		writeAgentClubEventError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Input.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, agentclub.ResponseFromAdmission(mapping, result.Input, result.Decision.Admitted))
}

func writeAgentClubEventError(w http.ResponseWriter, err error) {
	var conflict *sessionInputConflictError
	var validation *sessionInputValidationError
	switch {
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agentclub.ErrUnsupportedSchemaVersion),
		errors.Is(err, agentclub.ErrInvalidIdentifier),
		errors.Is(err, agentclub.ErrInvalidEvent),
		errors.Is(err, agentclub.ErrInvalidOwner):
		writeError(w, http.StatusBadRequest, err.Error())
	case strings.Contains(err.Error(), "session not found"):
		writeError(w, http.StatusNotFound, err.Error())
	case strings.Contains(err.Error(), "session owner scope mismatch"),
		strings.Contains(err.Error(), "legacy unowned"):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
