package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

const (
	agentClubTriggerAuditJSONLName = "agentclub-trigger-audit.jsonl"
	agentClubTriggerAuditReceived  = "received"
	agentClubTriggerAuditAdmitted  = "admitted"
	agentClubTriggerAuditRejected  = "rejected"
	agentClubTriggerAuditDryRun    = "dry_registration"
)

type agentClubTriggerAuditRecord struct {
	SchemaVersion       int       `json:"schema_version"`
	Seq                 int64     `json:"seq"`
	Timestamp           time.Time `json:"ts"`
	Decision            string    `json:"decision"`
	Reason              string    `json:"reason,omitempty"`
	BindingID           string    `json:"binding_id,omitempty"`
	TriggerKind         string    `json:"trigger_kind,omitempty"`
	Source              string    `json:"source,omitempty"`
	Capability          string    `json:"capability,omitempty"`
	EventType           string    `json:"event_type,omitempty"`
	ExternalEventIDHash string    `json:"external_event_id_hash,omitempty"`
	PayloadSHA256       string    `json:"payload_sha256,omitempty"`
	TargetSessionID     string    `json:"target_session_id,omitempty"`
	InputID             string    `json:"input_id,omitempty"`
	Duplicate           bool      `json:"duplicate,omitempty"`
	ClientType          string    `json:"client_type,omitempty"`
	ClientIDHash        string    `json:"client_id_hash,omitempty"`
	MetadataKeys        []string  `json:"metadata_keys,omitempty"`
}

type replayedAgentClubTriggerAudit struct {
	lastSeq int64
	records []agentClubTriggerAuditRecord
}

func (s *Server) handleAgentClubTriggerDelivery(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("binding_id"))
	if s == nil || s.agentClub == nil {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContext{BindingID: bindingID}, gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, "agent-club registry is not configured")
		writeError(w, http.StatusNotFound, "agent-club trigger binding not found")
		return
	}
	binding, err := s.agentClub.TriggerBinding(bindingID)
	if err != nil {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContext{BindingID: bindingID}, gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, safeAgentClubTriggerReason(err))
		writeAgentClubTriggerError(w, err)
		return
	}
	rawBody, tooLarge, err := readAgentClubTriggerBody(r.Body, binding.MaxBodyBytes)
	if err != nil {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromBinding(binding), gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, "delivery body read failed")
		writeError(w, http.StatusBadRequest, "delivery body read failed")
		return
	}
	if tooLarge {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromBinding(binding), gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, "delivery body too large")
		writeError(w, http.StatusRequestEntityTooLarge, "delivery body too large")
		return
	}
	if binding.Kind == agentclub.TriggerKindWebhook && binding.AuthMethod == agentclub.TriggerAuthHMACSHA256 {
		if err := ingress.VerifyRawBodyHMACSHA256(ingress.HMACVerification{
			Secret:           binding.HMACSecret,
			Body:             rawBody,
			Signature:        r.Header.Get(binding.SignatureHeader),
			Timestamp:        headerValue(r, binding.TimestampHeader),
			MaxSkew:          triggerHMACMaxSkew(binding.TimestampHeader),
			IncludeTimestamp: strings.TrimSpace(binding.TimestampHeader) != "",
		}); err != nil {
			ctx := agentClubTriggerAuditContextFromBinding(binding)
			ctx.PayloadSHA256 = ingress.PayloadSHA256(rawBody)
			s.appendAgentClubTriggerAudit(ctx, gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, safeAgentClubTriggerReason(err))
			writeAgentClubTriggerError(w, err)
			return
		}
	}
	delivery, err := buildAgentClubTriggerDeliveryFromRequest(r, binding, rawBody)
	if err != nil {
		ctx := agentClubTriggerAuditContextFromBinding(binding)
		ctx.PayloadSHA256 = ingress.PayloadSHA256(rawBody)
		s.appendAgentClubTriggerAudit(ctx, gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, safeAgentClubTriggerReason(err))
		writeAgentClubTriggerError(w, err)
		return
	}
	if delivery.DryRunRegistration {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromDelivery(delivery), gatewayapi.SessionInputResponse{}, agentClubTriggerAuditDryRun, agentClubTriggerAuditDryRun)
		writeJSON(w, http.StatusAccepted, agentclub.ResponseFromTriggerDelivery(delivery, gatewayapi.SessionInputResponse{}, false))
		return
	}
	if _, err := s.agentClub.Match(delivery.Request, binding.Owner); err != nil {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromDelivery(delivery), gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, safeAgentClubTriggerReason(err))
		writeAgentClubTriggerError(w, err)
		return
	}
	mapping, err := agentclub.MapTriggerToIngress(delivery)
	if err != nil {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromDelivery(delivery), gatewayapi.SessionInputResponse{}, agentClubTriggerAuditRejected, safeAgentClubTriggerReason(err))
		writeAgentClubTriggerError(w, err)
		return
	}
	s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromDelivery(delivery), gatewayapi.SessionInputResponse{}, agentClubTriggerAuditReceived, agentClubTriggerAuditReceived)
	result, err := s.AdmitIngressEvent(r.Context(), mapping.Event, mapping.Rule)
	if err != nil {
		s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromDelivery(delivery), result.Input, agentClubTriggerAuditRejected, safeAgentClubTriggerReason(err))
		writeAgentClubTriggerError(w, err)
		return
	}
	s.appendAgentClubTriggerAudit(agentClubTriggerAuditContextFromDelivery(delivery), result.Input, agentClubTriggerAuditAdmitted, agentClubTriggerAuditAdmitted)
	status := http.StatusCreated
	if result.Input.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, agentclub.ResponseFromTriggerDelivery(delivery, result.Input, result.Decision.Admitted))
}

func buildAgentClubTriggerDeliveryFromRequest(r *http.Request, binding agentclub.TriggerBinding, rawBody []byte) (agentclub.TriggerDelivery, error) {
	switch binding.Kind {
	case agentclub.TriggerKindWebhook:
		return agentclub.BuildTriggerDelivery(agentclub.TriggerDeliveryInput{
			Binding:    binding,
			RawBody:    rawBody,
			DeliveryID: headerValue(r, binding.DeliveryIDHeader),
		})
	case agentclub.TriggerKindSchedule, agentclub.TriggerKindManual:
		var req agentclub.TriggerDeliveryRequest
		if err := json.Unmarshal(rawBody, &req); err != nil {
			return agentclub.TriggerDelivery{}, fmt.Errorf("%w: invalid JSON", agentclub.ErrInvalidTrigger)
		}
		if req.SchemaVersion != agentclub.SchemaVersion {
			return agentclub.TriggerDelivery{}, fmt.Errorf("%w: got schema_version %d want %d", agentclub.ErrUnsupportedSchemaVersion, req.SchemaVersion, agentclub.SchemaVersion)
		}
		return agentclub.BuildTriggerDelivery(agentclub.TriggerDeliveryInput{
			Binding:            binding,
			ScheduledAtUTC:     req.ScheduledAtUTC,
			Payload:            req.Payload,
			Metadata:           req.Metadata,
			DryRunRegistration: req.DryRunRegistration,
		})
	default:
		return agentclub.TriggerDelivery{}, fmt.Errorf("%w: unsupported trigger kind %q", agentclub.ErrInvalidTriggerBinding, binding.Kind)
	}
}

func readAgentClubTriggerBody(reader io.Reader, maxBytes int64) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = agentclub.DefaultTriggerMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > maxBytes {
		return nil, true, nil
	}
	return body, false, nil
}

func triggerHMACMaxSkew(timestampHeader string) time.Duration {
	if strings.TrimSpace(timestampHeader) == "" {
		return 0
	}
	return 5 * time.Minute
}

func headerValue(r *http.Request, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(name))
}

func writeAgentClubTriggerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentclub.ErrUnknownTriggerBinding):
		writeError(w, http.StatusNotFound, "agent-club trigger binding not found")
	case errors.Is(err, agentclub.ErrTriggerDisabled),
		errors.Is(err, agentclub.ErrUnknownCapability),
		errors.Is(err, agentclub.ErrCapabilityDisabled):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ingress.ErrMissingHMACSignature),
		errors.Is(err, ingress.ErrInvalidHMACSignature),
		errors.Is(err, ingress.ErrMissingHMACSecret),
		errors.Is(err, ingress.ErrMissingHMACTimestamp),
		errors.Is(err, ingress.ErrInvalidHMACTimestamp),
		errors.Is(err, ingress.ErrStaleHMACTimestamp),
		errors.Is(err, ingress.ErrUnsignedHMACTimestamp):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, agentclub.ErrFutureTriggerDelivery):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agentclub.ErrUnsupportedSchemaVersion),
		errors.Is(err, agentclub.ErrInvalidIdentifier),
		errors.Is(err, agentclub.ErrInvalidEvent),
		errors.Is(err, agentclub.ErrInvalidOwner),
		errors.Is(err, agentclub.ErrInvalidBinding),
		errors.Is(err, agentclub.ErrInvalidTriggerBinding),
		errors.Is(err, agentclub.ErrInvalidTrigger):
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

func safeAgentClubTriggerReason(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

type agentClubTriggerAuditContext struct {
	BindingID           string
	TriggerKind         string
	Source              string
	Capability          string
	EventType           string
	ExternalEventIDHash string
	PayloadSHA256       string
	TargetSessionID     string
	Owner               gatewayapi.SessionOwner
	MetadataKeys        []string
}

func agentClubTriggerAuditContextFromBinding(binding agentclub.TriggerBinding) agentClubTriggerAuditContext {
	return agentClubTriggerAuditContext{
		BindingID:       binding.ID,
		TriggerKind:     binding.Kind,
		Source:          binding.Source,
		Capability:      binding.Capability,
		EventType:       binding.EventType,
		TargetSessionID: binding.TargetSessionID,
		Owner:           binding.Owner,
		MetadataKeys:    metadataMapKeys(binding.Metadata),
	}
}

func agentClubTriggerAuditContextFromDelivery(delivery agentclub.TriggerDelivery) agentClubTriggerAuditContext {
	return agentClubTriggerAuditContext{
		BindingID:           delivery.Binding.ID,
		TriggerKind:         delivery.Binding.Kind,
		Source:              delivery.Request.Source,
		Capability:          delivery.Request.Capability,
		EventType:           delivery.Request.EventType,
		ExternalEventIDHash: delivery.ExternalEventIDHash,
		PayloadSHA256:       delivery.PayloadSHA256,
		TargetSessionID:     delivery.Binding.TargetSessionID,
		Owner:               delivery.Binding.Owner,
		MetadataKeys:        append([]string(nil), delivery.MetadataKeys...),
	}
}

func (s *Server) appendAgentClubTriggerAudit(ctx agentClubTriggerAuditContext, input gatewayapi.SessionInputResponse, status, reason string) {
	if s == nil || s.store == nil {
		return
	}
	_, _ = s.store.AppendAgentClubTriggerAudit(ctx, input, status, reason)
}

func (s *sessionStore) AppendAgentClubTriggerAudit(ctx agentClubTriggerAuditContext, input gatewayapi.SessionInputResponse, status, reason string) (agentClubTriggerAuditRecord, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return agentClubTriggerAuditRecord{}, nil
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = agentClubTriggerAuditRejected
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = status
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensurePrivateGatewayDir(s.dir); err != nil {
		return agentClubTriggerAuditRecord{}, err
	}
	path := filepath.Join(s.dir, agentClubTriggerAuditJSONLName)
	replayed, err := replayAgentClubTriggerAudit(path)
	if err != nil {
		return agentClubTriggerAuditRecord{}, err
	}
	record := agentClubTriggerAuditRecord{
		SchemaVersion:       gatewaySessionSchemaVersion,
		Seq:                 replayed.lastSeq + 1,
		Timestamp:           time.Now().UTC(),
		Decision:            status,
		Reason:              reason,
		BindingID:           ctx.BindingID,
		TriggerKind:         ctx.TriggerKind,
		Source:              ctx.Source,
		Capability:          ctx.Capability,
		EventType:           ctx.EventType,
		ExternalEventIDHash: ctx.ExternalEventIDHash,
		PayloadSHA256:       ctx.PayloadSHA256,
		TargetSessionID:     ctx.TargetSessionID,
		InputID:             input.InputID,
		Duplicate:           input.Duplicate,
		ClientType:          ctx.Owner.ClientType,
		ClientIDHash:        hashIngressAuditValue(ctx.Owner.ClientID),
		MetadataKeys:        append([]string(nil), ctx.MetadataKeys...),
	}
	sort.Strings(record.MetadataKeys)
	if err := validateAgentClubTriggerAuditRecordForAppend(record); err != nil {
		return agentClubTriggerAuditRecord{}, err
	}
	if err := eventlog.AppendJSONL(path, record); err != nil {
		return agentClubTriggerAuditRecord{}, err
	}
	return record, nil
}

func (s *sessionStore) ReplayAgentClubTriggerAudit() ([]agentClubTriggerAuditRecord, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	replayed, err := replayAgentClubTriggerAudit(filepath.Join(s.dir, agentClubTriggerAuditJSONLName))
	if err != nil {
		return nil, err
	}
	return replayed.records, nil
}

func replayAgentClubTriggerAudit(path string) (replayedAgentClubTriggerAudit, error) {
	var out replayedAgentClubTriggerAudit
	expectedSeq := int64(1)
	err := eventlog.ReplayJSONL[agentClubTriggerAuditRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[agentClubTriggerAuditRecord]) error {
		record := item.Value
		recordNo := expectedSeq
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != expectedSeq {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq))
		}
		if err := validateAgentClubTriggerAuditRecord(record); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		out.records = append(out.records, record)
		out.lastSeq = record.Seq
		expectedSeq++
		return nil
	})
	return out, err
}

func validateAgentClubTriggerAuditRecordForAppend(record agentClubTriggerAuditRecord) error {
	if record.SchemaVersion == 0 {
		return fmt.Errorf("missing schema_version")
	}
	return validateAgentClubTriggerAuditRecord(record)
}

func validateAgentClubTriggerAuditRecord(record agentClubTriggerAuditRecord) error {
	switch record.Decision {
	case agentClubTriggerAuditReceived, agentClubTriggerAuditAdmitted, agentClubTriggerAuditRejected, agentClubTriggerAuditDryRun:
	default:
		return fmt.Errorf("unsupported decision %q", record.Decision)
	}
	if strings.TrimSpace(record.BindingID) == "" {
		return fmt.Errorf("missing binding_id")
	}
	if record.PayloadSHA256 != "" && !isHexSHA256(record.PayloadSHA256) {
		return fmt.Errorf("invalid payload_sha256")
	}
	if record.ExternalEventIDHash != "" && !isHexSHA256(record.ExternalEventIDHash) {
		return fmt.Errorf("invalid external_event_id_hash")
	}
	if record.ClientIDHash != "" && !isHexSHA256(record.ClientIDHash) {
		return fmt.Errorf("invalid client_id_hash")
	}
	return nil
}

func metadataMapKeys(metadata map[string]string) []string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
