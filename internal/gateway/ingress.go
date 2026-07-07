package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

const (
	ingressAuditJSONLName = "ingress-audit.jsonl"
	ingressAuditReceived  = "received"
	ingressAuditAdmitted  = "admitted"
	ingressAuditRejected  = "rejected"
)

type IngressAdmissionResult struct {
	Decision ingress.AdmissionDecision
	Input    gatewayapi.SessionInputResponse
}

type ingressAuditRecord struct {
	SchemaVersion       int       `json:"schema_version"`
	Seq                 int64     `json:"seq"`
	Timestamp           time.Time `json:"ts"`
	Decision            string    `json:"decision"`
	Reason              string    `json:"reason,omitempty"`
	RuleID              string    `json:"rule_id,omitempty"`
	Source              string    `json:"source,omitempty"`
	ExternalEventIDHash string    `json:"external_event_id_hash,omitempty"`
	PayloadSHA256       string    `json:"payload_sha256,omitempty"`
	TargetSessionID     string    `json:"target_session_id,omitempty"`
	InputID             string    `json:"input_id,omitempty"`
	Duplicate           bool      `json:"duplicate,omitempty"`
	ClientType          string    `json:"client_type,omitempty"`
	ClientIDHash        string    `json:"client_id_hash,omitempty"`
	MetadataKeys        []string  `json:"metadata_keys,omitempty"`
}

type replayedIngressAudit struct {
	lastSeq int64
	records []ingressAuditRecord
}

func (s *Server) AdmitIngressEvent(ctx context.Context, event ingress.IngressEvent, rule ingress.IngressRule) (IngressAdmissionResult, error) {
	var result IngressAdmissionResult
	if ctx != nil {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
	}
	decision, err := ingress.Admit(event, rule)
	result.Decision = decision
	if err != nil {
		if auditErr := s.appendIngressAudit(decision, result.Input, ingressAuditRejected, err.Error()); auditErr != nil {
			return result, auditErr
		}
		return result, err
	}
	session, ok, err := s.sessionWithError(decision.TargetSessionID)
	if err != nil {
		reason := "session load failed: " + err.Error()
		result.Decision.Reason = reason
		if auditErr := s.appendIngressAudit(result.Decision, result.Input, ingressAuditRejected, reason); auditErr != nil {
			return result, auditErr
		}
		return result, err
	}
	if !ok {
		err := fmt.Errorf("session not found")
		result.Decision.Reason = err.Error()
		if auditErr := s.appendIngressAudit(result.Decision, result.Input, ingressAuditRejected, err.Error()); auditErr != nil {
			return result, auditErr
		}
		return result, err
	}
	actor := ingressRuleActor(rule, decision)
	if err := authorizeSessionAccess(session, actor, sessionAccessMutate); err != nil {
		result.Decision.Reason = err.Error()
		if auditErr := s.appendIngressAudit(result.Decision, result.Input, ingressAuditRejected, err.Error()); auditErr != nil {
			return result, auditErr
		}
		return result, err
	}
	result.Decision.Reason = ingressAuditReceived
	if auditErr := s.appendIngressAudit(result.Decision, result.Input, ingressAuditReceived, ingressAuditReceived); auditErr != nil {
		return result, auditErr
	}
	input, err := s.admitSessionInput(session, decision.Request)
	result.Input = input
	if err != nil {
		reason := "input admission failed: " + err.Error()
		result.Decision.Reason = reason
		if auditErr := s.appendIngressAudit(result.Decision, result.Input, ingressAuditRejected, reason); auditErr != nil {
			return result, auditErr
		}
		return result, err
	}
	result.Decision.Reason = ingressAuditAdmitted
	if auditErr := s.appendIngressAudit(result.Decision, input, ingressAuditAdmitted, ingressAuditAdmitted); auditErr != nil {
		return result, auditErr
	}
	return result, nil
}

func ingressRuleActor(rule ingress.IngressRule, decision ingress.AdmissionDecision) gatewayapi.SessionOwner {
	actor := rule.Owner
	if strings.TrimSpace(actor.ClientID) == "" {
		actor.ClientID = decision.Request.ClientID
	}
	if strings.TrimSpace(actor.ClientType) == "" {
		actor.ClientType = decision.Request.ClientType
	}
	return normalizeSessionOwner(actor)
}

func (s *Server) appendIngressAudit(decision ingress.AdmissionDecision, input gatewayapi.SessionInputResponse, status, reason string) error {
	if s == nil || s.store == nil {
		return nil
	}
	_, err := s.store.AppendIngressAudit(decision, input, status, reason)
	return err
}

func (s *sessionStore) AppendIngressAudit(decision ingress.AdmissionDecision, input gatewayapi.SessionInputResponse, status, reason string) (ingressAuditRecord, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return ingressAuditRecord{}, nil
	}
	status = strings.TrimSpace(status)
	if status == "" {
		if decision.Admitted {
			status = ingressAuditAdmitted
		} else {
			status = ingressAuditRejected
		}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = strings.TrimSpace(decision.Reason)
	}
	if reason == "" {
		reason = status
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensurePrivateGatewayDir(s.dir); err != nil {
		return ingressAuditRecord{}, err
	}
	path := filepath.Join(s.dir, ingressAuditJSONLName)
	replayed, err := replayIngressAudit(path)
	if err != nil {
		return ingressAuditRecord{}, err
	}
	inputID := firstNonEmpty(input.InputID, decision.InputID)
	record := ingressAuditRecord{
		SchemaVersion:       gatewaySessionSchemaVersion,
		Seq:                 replayed.lastSeq + 1,
		Timestamp:           time.Now().UTC(),
		Decision:            status,
		Reason:              reason,
		RuleID:              decision.RuleID,
		Source:              decision.Source,
		ExternalEventIDHash: hashIngressAuditValue(decision.ExternalEventID),
		PayloadSHA256:       decision.PayloadSHA256,
		TargetSessionID:     decision.TargetSessionID,
		InputID:             inputID,
		Duplicate:           input.Duplicate,
		ClientType:          decision.Request.ClientType,
		ClientIDHash:        hashIngressAuditValue(decision.Request.ClientID),
		MetadataKeys:        ingressAuditMetadataKeys(decision),
	}
	if err := validateIngressAuditRecordForAppend(record); err != nil {
		return ingressAuditRecord{}, err
	}
	if err := eventlog.AppendJSONL(path, record); err != nil {
		return ingressAuditRecord{}, err
	}
	return record, nil
}

func (s *sessionStore) ReplayIngressAudit() ([]ingressAuditRecord, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	replayed, err := replayIngressAudit(filepath.Join(s.dir, ingressAuditJSONLName))
	if err != nil {
		return nil, err
	}
	return replayed.records, nil
}

func replayIngressAudit(path string) (replayedIngressAudit, error) {
	var out replayedIngressAudit
	expectedSeq := int64(1)
	err := eventlog.ReplayJSONL[ingressAuditRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[ingressAuditRecord]) error {
		record := item.Value
		recordNo := expectedSeq
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != expectedSeq {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq))
		}
		if err := validateIngressAuditRecord(record); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		out.records = append(out.records, record)
		out.lastSeq = record.Seq
		expectedSeq++
		return nil
	})
	return out, err
}

func validateIngressAuditRecordForAppend(record ingressAuditRecord) error {
	if record.SchemaVersion == 0 {
		return fmt.Errorf("missing schema_version")
	}
	return validateIngressAuditRecord(record)
}

func validateIngressAuditRecord(record ingressAuditRecord) error {
	switch record.Decision {
	case ingressAuditReceived, ingressAuditAdmitted, ingressAuditRejected:
	default:
		return fmt.Errorf("unsupported decision %q", record.Decision)
	}
	if record.Decision == ingressAuditReceived || record.Decision == ingressAuditAdmitted {
		if strings.TrimSpace(record.InputID) == "" {
			return fmt.Errorf("%s ingress audit record missing input_id", record.Decision)
		}
		if strings.TrimSpace(record.TargetSessionID) == "" {
			return fmt.Errorf("%s ingress audit record missing target_session_id", record.Decision)
		}
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

func hashIngressAuditValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func ingressAuditMetadataKeys(decision ingress.AdmissionDecision) []string {
	keys := append([]string(nil), decision.Metadata.Keys...)
	if len(keys) == 0 {
		for key := range decision.Request.Metadata {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func isHexSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
