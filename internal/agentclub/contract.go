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
	"unicode"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

const (
	SchemaVersion = 1

	DispatchAdmitOnly = "admit_only"

	CapabilityKindEvent    = "event"
	CapabilityKindReview   = "review"
	CapabilityKindDecision = "decision"

	RiskReadOnly         = "read_only"
	RiskLocalRead        = "local_read"
	RiskNetworkRead      = "network_read"
	RiskLocalWrite       = "local_write"
	RiskNetworkWrite     = "network_write"
	RiskExternalMutation = "external_mutation"
	RiskExecute          = "execute"
	RiskSecretAccess     = "secret_access"
	RiskUnknown          = "unknown"

	ApprovalNone     = "none"
	ApprovalRequired = "required"
)

var (
	ErrUnsupportedSchemaVersion = errors.New("unsupported agentclub schema_version")
	ErrInvalidIdentifier        = errors.New("invalid agentclub identifier")
	ErrInvalidEvent             = errors.New("invalid agentclub event")
	ErrInvalidOwner             = errors.New("invalid agentclub owner")
)

type EventRequest struct {
	SchemaVersion   int               `json:"schema_version"`
	Source          string            `json:"source"`
	Capability      string            `json:"capability"`
	EventType       string            `json:"event_type"`
	ExternalEventID string            `json:"external_event_id"`
	Prompt          string            `json:"prompt"`
	Payload         json.RawMessage   `json:"payload,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type EventAdmissionResponse struct {
	SchemaVersion       int      `json:"schema_version"`
	Admitted            bool     `json:"admitted"`
	InputID             string   `json:"input_id"`
	State               string   `json:"state"`
	Duplicate           bool     `json:"duplicate,omitempty"`
	TargetSessionID     string   `json:"target_session_id"`
	Source              string   `json:"source"`
	Capability          string   `json:"capability"`
	EventType           string   `json:"event_type"`
	PayloadSHA256       string   `json:"payload_sha256"`
	ExternalEventIDHash string   `json:"external_event_id_hash"`
	MetadataKeys        []string `json:"metadata_keys,omitempty"`
	RunDispatched       bool     `json:"run_dispatched"`
}

type CapabilityDescriptor struct {
	ID           string          `json:"id"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	Kind         string          `json:"kind"`
	Risk         string          `json:"risk"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Dispatch     string          `json:"dispatch"`
	Approval     string          `json:"approval,omitempty"`
	Version      string          `json:"version,omitempty"`
}

type AdmissionMapping struct {
	Request             EventRequest
	Event               ingress.IngressEvent
	Rule                ingress.IngressRule
	PayloadSHA256       string
	ExternalEventIDHash string
	MetadataKeys        []string
}

func MapToIngress(req EventRequest, targetSessionID string, owner gatewayapi.SessionOwner) (AdmissionMapping, error) {
	normalized, payload, err := NormalizeEventRequest(req)
	if err != nil {
		return AdmissionMapping{}, err
	}
	targetSessionID = strings.TrimSpace(targetSessionID)
	if targetSessionID == "" {
		return AdmissionMapping{}, fmt.Errorf("%w: target session id required", ErrInvalidEvent)
	}
	owner = normalizeOwner(owner)
	if owner.ClientType != "ingress" || owner.ClientID == "" {
		return AdmissionMapping{}, fmt.Errorf("%w: ingress client owner required", ErrInvalidOwner)
	}
	payloadHash := ingress.PayloadSHA256(payload)
	externalHash := HashExternalEventID(normalized.ExternalEventID)
	eventMetadata, err := ingress.SanitizeMetadata(normalized.Metadata)
	if err != nil {
		return AdmissionMapping{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	rule := ingress.IngressRule{
		ID:     normalized.Capability,
		Source: normalized.Source,
		Owner:  owner,
		StaticMetadata: map[string]string{
			"agentclub.capability":     normalized.Capability,
			"agentclub.dispatch":       DispatchAdmitOnly,
			"agentclub.event_type":     normalized.EventType,
			"agentclub.schema_version": "1",
			"ingress.policy":           "agentclub_admit_only",
		},
	}
	keys := metadataKeys(eventMetadata)
	keys = append(keys, metadataKeys(rule.StaticMetadata)...)
	sort.Strings(keys)
	return AdmissionMapping{
		Request: normalized,
		Event: ingress.IngressEvent{
			Source:          normalized.Source,
			ExternalEventID: normalized.ExternalEventID,
			TargetSessionID: targetSessionID,
			Prompt:          normalized.Prompt,
			RawBody:         payload,
			Metadata:        eventMetadata,
		},
		Rule:                rule,
		PayloadSHA256:       payloadHash,
		ExternalEventIDHash: externalHash,
		MetadataKeys:        keys,
	}, nil
}

func NormalizeEventRequest(req EventRequest) (EventRequest, []byte, error) {
	if req.SchemaVersion != SchemaVersion {
		return EventRequest{}, nil, fmt.Errorf("%w: got %d want %d", ErrUnsupportedSchemaVersion, req.SchemaVersion, SchemaVersion)
	}
	var err error
	if req.Source, err = normalizeIdentifier("source", req.Source); err != nil {
		return EventRequest{}, nil, err
	}
	if req.Capability, err = normalizeIdentifier("capability", req.Capability); err != nil {
		return EventRequest{}, nil, err
	}
	if req.EventType, err = normalizeIdentifier("event_type", req.EventType); err != nil {
		return EventRequest{}, nil, err
	}
	req.ExternalEventID = strings.TrimSpace(req.ExternalEventID)
	if req.ExternalEventID == "" {
		return EventRequest{}, nil, fmt.Errorf("%w: external_event_id required", ErrInvalidEvent)
	}
	if len(req.ExternalEventID) > 512 {
		return EventRequest{}, nil, fmt.Errorf("%w: external_event_id too long", ErrInvalidEvent)
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return EventRequest{}, nil, fmt.Errorf("%w: prompt required", ErrInvalidEvent)
	}
	payload, err := canonicalPayload(req.Payload)
	if err != nil {
		return EventRequest{}, nil, err
	}
	req.Payload = append(json.RawMessage(nil), payload...)
	req.Metadata = copyStringMap(req.Metadata)
	return req, payload, nil
}

func ValidateCapabilityDescriptor(desc CapabilityDescriptor) error {
	_, err := NormalizeCapabilityDescriptor(desc)
	return err
}

func NormalizeCapabilityDescriptor(desc CapabilityDescriptor) (CapabilityDescriptor, error) {
	var err error
	if desc.ID, err = normalizeIdentifier("id", desc.ID); err != nil {
		return CapabilityDescriptor{}, err
	}
	desc.Title = strings.TrimSpace(desc.Title)
	desc.Description = strings.TrimSpace(desc.Description)
	if desc.Kind = strings.TrimSpace(desc.Kind); !allowedCapabilityKinds[desc.Kind] {
		return CapabilityDescriptor{}, fmt.Errorf("%w: unsupported descriptor kind %q", ErrInvalidEvent, desc.Kind)
	}
	if desc.Risk = strings.TrimSpace(desc.Risk); !allowedRiskClasses[desc.Risk] {
		return CapabilityDescriptor{}, fmt.Errorf("%w: unsupported descriptor risk %q", ErrInvalidEvent, desc.Risk)
	}
	if desc.Dispatch = strings.TrimSpace(desc.Dispatch); desc.Dispatch != DispatchAdmitOnly {
		return CapabilityDescriptor{}, fmt.Errorf("%w: descriptor dispatch must be %q", ErrInvalidEvent, DispatchAdmitOnly)
	}
	desc.Approval = strings.TrimSpace(desc.Approval)
	if desc.Approval == "" {
		desc.Approval = ApprovalNone
	}
	if !allowedApprovalModes[desc.Approval] {
		return CapabilityDescriptor{}, fmt.Errorf("%w: unsupported descriptor approval %q", ErrInvalidEvent, desc.Approval)
	}
	if desc.Version, err = normalizeOptionalIdentifier("version", desc.Version); err != nil {
		return CapabilityDescriptor{}, err
	}
	if desc.InputSchema, err = normalizeOptionalJSONObject("input_schema", desc.InputSchema); err != nil {
		return CapabilityDescriptor{}, err
	}
	if desc.OutputSchema, err = normalizeOptionalJSONObject("output_schema", desc.OutputSchema); err != nil {
		return CapabilityDescriptor{}, err
	}
	return desc, nil
}

func ResponseFromAdmission(mapping AdmissionMapping, input gatewayapi.SessionInputResponse, admitted bool) EventAdmissionResponse {
	return EventAdmissionResponse{
		SchemaVersion:       SchemaVersion,
		Admitted:            admitted,
		InputID:             input.InputID,
		State:               input.State,
		Duplicate:           input.Duplicate,
		TargetSessionID:     mapping.Event.TargetSessionID,
		Source:              mapping.Request.Source,
		Capability:          mapping.Request.Capability,
		EventType:           mapping.Request.EventType,
		PayloadSHA256:       mapping.PayloadSHA256,
		ExternalEventIDHash: mapping.ExternalEventIDHash,
		MetadataKeys:        append([]string(nil), mapping.MetadataKeys...),
		RunDispatched:       false,
	}
}

func HashExternalEventID(externalEventID string) string {
	externalEventID = strings.TrimSpace(externalEventID)
	if externalEventID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(externalEventID))
	return hex.EncodeToString(sum[:])
}

func canonicalPayload(payload json.RawMessage) ([]byte, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return []byte("{}"), nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return nil, fmt.Errorf("%w: payload must be valid JSON", ErrInvalidEvent)
	}
	return append([]byte(nil), compact.Bytes()...), nil
}

func normalizeIdentifier(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s required", ErrInvalidIdentifier, field)
	}
	if len(value) > 128 {
		return "", fmt.Errorf("%w: %s too long", ErrInvalidIdentifier, field)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return "", fmt.Errorf("%w: %s contains unsupported character %q", ErrInvalidIdentifier, field, r)
	}
	return value, nil
}

func normalizeOptionalIdentifier(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return normalizeIdentifier(field, value)
}

func normalizeOptionalJSONObject(field string, raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: descriptor %s must be a JSON object", ErrInvalidEvent, field)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("%w: descriptor %s must be valid JSON", ErrInvalidEvent, field)
	}
	return append(json.RawMessage(nil), compact.Bytes()...), nil
}

func normalizeOwner(owner gatewayapi.SessionOwner) gatewayapi.SessionOwner {
	owner.ClientID = strings.TrimSpace(owner.ClientID)
	owner.ClientType = strings.ToLower(strings.TrimSpace(owner.ClientType))
	return owner
}

func metadataKeys(metadata map[string]string) []string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

var allowedCapabilityKinds = map[string]bool{
	CapabilityKindEvent:    true,
	CapabilityKindReview:   true,
	CapabilityKindDecision: true,
}

var allowedRiskClasses = map[string]bool{
	RiskReadOnly:         true,
	RiskLocalRead:        true,
	RiskNetworkRead:      true,
	RiskLocalWrite:       true,
	RiskNetworkWrite:     true,
	RiskExternalMutation: true,
	RiskExecute:          true,
	RiskSecretAccess:     true,
	RiskUnknown:          true,
}

var allowedApprovalModes = map[string]bool{
	ApprovalNone:     true,
	ApprovalRequired: true,
}
