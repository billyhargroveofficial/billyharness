package gateway

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	agentClubAutoRunAuditJSONLName = "agentclub-autorun-audit.jsonl"

	agentClubAutoRunDecisionSkipped     = "skipped"
	agentClubAutoRunDecisionDispatched  = "dispatched"
	agentClubAutoRunDecisionRateLimited = "rate_limited"
	agentClubAutoRunDecisionBusy        = "busy"
	agentClubAutoRunDecisionFailed      = "failed"
)

type agentClubAutoRunAuditRecord struct {
	SchemaVersion   int       `json:"schema_version"`
	Seq             int64     `json:"seq"`
	Timestamp       time.Time `json:"ts"`
	Decision        string    `json:"decision"`
	Reason          string    `json:"reason,omitempty"`
	BindingID       string    `json:"binding_id,omitempty"`
	TriggerKind     string    `json:"trigger_kind,omitempty"`
	Source          string    `json:"source,omitempty"`
	Capability      string    `json:"capability,omitempty"`
	EventType       string    `json:"event_type,omitempty"`
	TargetSessionID string    `json:"target_session_id,omitempty"`
	InputID         string    `json:"input_id,omitempty"`
	Duplicate       bool      `json:"duplicate,omitempty"`
	RunDispatched   bool      `json:"run_dispatched"`
	Mode            string    `json:"mode,omitempty"`
	ClientType      string    `json:"client_type,omitempty"`
	ClientIDHash    string    `json:"client_id_hash,omitempty"`
}

type replayedAgentClubAutoRunAudit struct {
	lastSeq int64
	records []agentClubAutoRunAuditRecord
}

func (s *Server) maybeDispatchAgentClubAutoRun(ctx context.Context, delivery agentclub.TriggerDelivery, input gatewayapi.SessionInputResponse) bool {
	binding := delivery.Binding
	if binding.RunPolicy == nil || !binding.RunPolicy.Enabled {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionSkipped, "run policy disabled", false, "")
		return false
	}
	policy, err := agentclub.NormalizeRunPolicyConfig(*binding.RunPolicy)
	if err != nil || !policy.Enabled {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionFailed, safeAgentClubAutoRunReason(err), false, "")
		return false
	}
	if input.Duplicate {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionSkipped, "duplicate input", false, policy.Mode)
		return false
	}
	session, ok, err := s.sessionWithError(binding.TargetSessionID)
	if err != nil {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionFailed, "session load failed", false, policy.Mode)
		return false
	}
	if !ok || session == nil {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionFailed, "session not found", false, policy.Mode)
		return false
	}
	if err := authorizeSessionAccess(session, binding.Owner, sessionAccessMutate); err != nil {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionFailed, safeAgentClubAutoRunReason(err), false, policy.Mode)
		return false
	}
	if policy.Mode == agentclub.RunPolicyModeStartIfIdle && agentClubSessionRunning(session) {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionBusy, "session run already active", false, policy.Mode)
		return false
	}
	if err := s.validateAgentClubRunPolicyRuntime(policy); err != nil {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionFailed, safeAgentClubAutoRunReason(err), false, policy.Mode)
		return false
	}
	if ok, reason := s.agentClubAutoRunRateAllowed(binding.ID, policy, time.Now().UTC()); !ok {
		s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionRateLimited, reason, false, policy.Mode)
		return false
	}
	req := RunRequest{
		Prompt:          delivery.Request.Prompt,
		InputID:         input.InputID,
		ClientID:        binding.Owner.ClientID,
		ClientType:      binding.Owner.ClientType,
		InterruptPolicy: policy.InterruptPolicy,
		MaxToolRounds:   policy.MaxToolRounds,
		AccessMode:      policy.AccessMode,
	}
	s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionDispatched, agentClubAutoRunDecisionDispatched, true, policy.Mode)
	go func() {
		if err := s.runAdmittedSessionInput(context.Background(), session, req, input, "agentclub_auto_run", func(protocol.Event) {}); err != nil {
			s.appendAgentClubAutoRunAudit(delivery, input, agentClubAutoRunDecisionFailed, safeAgentClubAutoRunReason(err), false, policy.Mode)
		}
	}()
	return true
}

func (s *Server) validateAgentClubRunPolicyRuntime(policy agentclub.RunPolicy) error {
	if s != nil {
		if err := s.validateRunAccessPolicy(RunRequest{AccessMode: policy.AccessMode}); err != nil {
			return err
		}
	}
	if policy.MaxToolRounds > 0 && s != nil && s.runtime.MaxToolRounds > 0 && policy.MaxToolRounds > s.runtime.MaxToolRounds {
		return fmt.Errorf("run_policy max_tool_rounds exceeds configured runtime limit")
	}
	if strings.TrimSpace(policy.AccessMode) != "" && s != nil && !agentClubAccessModeWithin(config.NormalizeAccessMode(s.toolPolicy.AccessMode), policy.AccessMode) {
		return fmt.Errorf("run_policy access_mode exceeds configured access mode")
	}
	return nil
}

func agentClubAccessModeWithin(configured, requested string) bool {
	return agentClubAccessModeRank(config.NormalizeAccessMode(requested)) <= agentClubAccessModeRank(config.NormalizeAccessMode(configured))
}

func agentClubAccessModeRank(mode string) int {
	switch config.NormalizeAccessMode(mode) {
	case config.AccessModePlan:
		return 1
	case config.AccessModeGuarded:
		return 2
	default:
		return 3
	}
}

func agentClubSessionRunning(session *Session) bool {
	if session == nil {
		return false
	}
	session.mu.Lock()
	statusRunning := session.status.Running
	session.mu.Unlock()
	return statusRunning || (session.Thread != nil && session.Thread.Running())
}

func (s *Server) agentClubAutoRunRateAllowed(bindingID string, policy agentclub.RunPolicy, now time.Time) (bool, string) {
	if s == nil || s.store == nil {
		return true, ""
	}
	records, err := s.store.ReplayAgentClubAutoRunAudit()
	if err != nil {
		return false, "auto-run audit replay failed"
	}
	now = now.UTC()
	var recent int
	var last time.Time
	for _, record := range records {
		if record.BindingID != bindingID || record.Decision != agentClubAutoRunDecisionDispatched || !record.RunDispatched {
			continue
		}
		if record.Timestamp.After(now.Add(-time.Hour)) {
			recent++
		}
		if record.Timestamp.After(last) {
			last = record.Timestamp
		}
	}
	if policy.MaxRunsPerHour > 0 && recent >= policy.MaxRunsPerHour {
		return false, "max_runs_per_hour exceeded"
	}
	if policy.Cooldown > 0 && !last.IsZero() && now.Sub(last) < policy.Cooldown {
		return false, "cooldown active"
	}
	return true, ""
}

func (s *Server) appendAgentClubAutoRunAudit(delivery agentclub.TriggerDelivery, input gatewayapi.SessionInputResponse, decision, reason string, runDispatched bool, mode string) {
	if s == nil || s.store == nil {
		return
	}
	if _, err := s.store.AppendAgentClubAutoRunAudit(delivery, input, decision, reason, runDispatched, mode); err != nil {
		logAgentClubAutoRunAuditError(err)
	}
}

func (s *sessionStore) AppendAgentClubAutoRunAudit(delivery agentclub.TriggerDelivery, input gatewayapi.SessionInputResponse, decision, reason string, runDispatched bool, mode string) (agentClubAutoRunAuditRecord, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return agentClubAutoRunAuditRecord{}, nil
	}
	decision = strings.TrimSpace(decision)
	if decision == "" {
		decision = agentClubAutoRunDecisionSkipped
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = decision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensurePrivateGatewayDir(s.dir); err != nil {
		return agentClubAutoRunAuditRecord{}, err
	}
	path := filepath.Join(s.dir, agentClubAutoRunAuditJSONLName)
	replayed, err := replayAgentClubAutoRunAudit(path)
	if err != nil {
		return agentClubAutoRunAuditRecord{}, err
	}
	record := agentClubAutoRunAuditRecord{
		SchemaVersion:   gatewaySessionSchemaVersion,
		Seq:             replayed.lastSeq + 1,
		Timestamp:       time.Now().UTC(),
		Decision:        decision,
		Reason:          reason,
		BindingID:       delivery.Binding.ID,
		TriggerKind:     delivery.Binding.Kind,
		Source:          delivery.Request.Source,
		Capability:      delivery.Request.Capability,
		EventType:       delivery.Request.EventType,
		TargetSessionID: delivery.Binding.TargetSessionID,
		InputID:         input.InputID,
		Duplicate:       input.Duplicate,
		RunDispatched:   runDispatched,
		Mode:            strings.TrimSpace(mode),
		ClientType:      delivery.Binding.Owner.ClientType,
		ClientIDHash:    hashIngressAuditValue(delivery.Binding.Owner.ClientID),
	}
	if err := validateAgentClubAutoRunAuditRecordForAppend(record); err != nil {
		return agentClubAutoRunAuditRecord{}, err
	}
	if err := eventlog.AppendJSONL(path, record); err != nil {
		return agentClubAutoRunAuditRecord{}, err
	}
	return record, nil
}

func (s *sessionStore) ReplayAgentClubAutoRunAudit() ([]agentClubAutoRunAuditRecord, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed, err := replayAgentClubAutoRunAudit(filepath.Join(s.dir, agentClubAutoRunAuditJSONLName))
	if err != nil {
		return nil, err
	}
	return replayed.records, nil
}

func replayAgentClubAutoRunAudit(path string) (replayedAgentClubAutoRunAudit, error) {
	var out replayedAgentClubAutoRunAudit
	expectedSeq := int64(1)
	err := eventlog.ReplayJSONL[agentClubAutoRunAuditRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[agentClubAutoRunAuditRecord]) error {
		record := item.Value
		recordNo := expectedSeq
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != expectedSeq {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq))
		}
		if err := validateAgentClubAutoRunAuditRecord(record); err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		out.records = append(out.records, record)
		out.lastSeq = record.Seq
		expectedSeq++
		return nil
	})
	return out, err
}

func validateAgentClubAutoRunAuditRecordForAppend(record agentClubAutoRunAuditRecord) error {
	if record.SchemaVersion == 0 {
		return fmt.Errorf("missing schema_version")
	}
	return validateAgentClubAutoRunAuditRecord(record)
}

func validateAgentClubAutoRunAuditRecord(record agentClubAutoRunAuditRecord) error {
	switch record.Decision {
	case agentClubAutoRunDecisionSkipped, agentClubAutoRunDecisionDispatched, agentClubAutoRunDecisionRateLimited, agentClubAutoRunDecisionBusy, agentClubAutoRunDecisionFailed:
	default:
		return fmt.Errorf("unsupported decision %q", record.Decision)
	}
	if strings.TrimSpace(record.BindingID) == "" {
		return fmt.Errorf("missing binding_id")
	}
	if record.ClientIDHash != "" && !isHexSHA256(record.ClientIDHash) {
		return fmt.Errorf("invalid client_id_hash")
	}
	return nil
}

func safeAgentClubAutoRunReason(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func logAgentClubAutoRunAuditError(err error) {
	if err != nil {
		log.Printf("agentclub auto-run audit failed: %v", err)
	}
}
