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
	"unicode"
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

	DefaultTriggerSignatureHeader = "X-Hub-Signature-256"
	DefaultTriggerDeliveryHeader  = "X-Billyharness-Delivery-ID"

	HeaderSessionClientID         = "X-Billyharness-Session-Client-ID"
	HeaderSessionClientType       = "X-Billyharness-Session-Client-Type"
	HeaderSessionTelegramChatID   = "X-Billyharness-Session-Telegram-Chat-ID"
	HeaderSessionTelegramThreadID = "X-Billyharness-Session-Telegram-Thread-ID"
	HeaderSessionTelegramUserID   = "X-Billyharness-Session-Telegram-User-ID"
	HeaderSessionTUIChatID        = "X-Billyharness-Session-TUI-Chat-ID"
)

var (
	ErrUnsupportedSchemaVersion = errors.New("unsupported agentclub schema_version")
	ErrInvalidIdentifier        = errors.New("invalid agentclub identifier")
	ErrInvalidEvent             = errors.New("invalid agentclub event")
	ErrInvalidTriggerDelivery   = errors.New("invalid agentclub trigger delivery")
	ErrInvalidOwner             = errors.New("invalid agentclub owner")
)

type Owner struct {
	ClientID         string `json:"client_id,omitempty"`
	ClientType       string `json:"client_type,omitempty"`
	TelegramChatID   int64  `json:"telegram_chat_id,omitempty"`
	TelegramThreadID int    `json:"telegram_thread_id,omitempty"`
	TelegramUserID   int64  `json:"telegram_user_id,omitempty"`
	TUIChatID        string `json:"tui_chat_id,omitempty"`
	Profile          string `json:"profile,omitempty"`
	Model            string `json:"model,omitempty"`
}

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

type TriggerDeliveryRequest struct {
	SchemaVersion      int               `json:"schema_version"`
	ScheduledAtUTC     string            `json:"scheduled_at_utc"`
	Payload            json.RawMessage   `json:"payload,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	DryRunRegistration bool              `json:"dry_run_registration,omitempty"`
}

type TriggerDeliveryResponse struct {
	SchemaVersion       int      `json:"schema_version"`
	Admitted            bool     `json:"admitted"`
	InputID             string   `json:"input_id,omitempty"`
	State               string   `json:"state"`
	Duplicate           bool     `json:"duplicate,omitempty"`
	TargetSessionID     string   `json:"target_session_id,omitempty"`
	BindingID           string   `json:"binding_id"`
	TriggerKind         string   `json:"trigger_kind"`
	Source              string   `json:"source"`
	Capability          string   `json:"capability"`
	EventType           string   `json:"event_type"`
	PayloadSHA256       string   `json:"payload_sha256,omitempty"`
	ExternalEventIDHash string   `json:"external_event_id_hash,omitempty"`
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

type BindingView struct {
	ID           string   `json:"id,omitempty"`
	Capability   string   `json:"capability"`
	ClientType   string   `json:"client_type"`
	ClientID     string   `json:"client_id"`
	Sources      []string `json:"sources,omitempty"`
	EventTypes   []string `json:"event_types,omitempty"`
	MetadataKeys []string `json:"metadata_keys,omitempty"`
	Enabled      bool     `json:"enabled"`
}

type CapabilityView struct {
	Descriptor CapabilityDescriptor `json:"descriptor"`
	Bindings   []BindingView        `json:"bindings,omitempty"`
}

type CapabilityListResponse struct {
	SchemaVersion int              `json:"schema_version"`
	Capabilities  []CapabilityView `json:"capabilities"`
}

type TriggerDelivery struct {
	Body    []byte
	Headers map[string]string
}

func NewEventRequest(source, capability, eventType, externalEventID, prompt string, payload json.RawMessage, metadata map[string]string) (EventRequest, error) {
	return NormalizeEventRequest(EventRequest{
		SchemaVersion:   SchemaVersion,
		Source:          source,
		Capability:      capability,
		EventType:       eventType,
		ExternalEventID: externalEventID,
		Prompt:          prompt,
		Payload:         payload,
		Metadata:        metadata,
	})
}

func NormalizeEventRequest(req EventRequest) (EventRequest, error) {
	if req.SchemaVersion != SchemaVersion {
		return EventRequest{}, fmt.Errorf("%w: got %d want %d", ErrUnsupportedSchemaVersion, req.SchemaVersion, SchemaVersion)
	}
	var err error
	if req.Source, err = normalizeIdentifier("source", req.Source); err != nil {
		return EventRequest{}, err
	}
	if req.Capability, err = normalizeIdentifier("capability", req.Capability); err != nil {
		return EventRequest{}, err
	}
	if req.EventType, err = normalizeIdentifier("event_type", req.EventType); err != nil {
		return EventRequest{}, err
	}
	req.ExternalEventID = strings.TrimSpace(req.ExternalEventID)
	if req.ExternalEventID == "" {
		return EventRequest{}, fmt.Errorf("%w: external_event_id required", ErrInvalidEvent)
	}
	if len(req.ExternalEventID) > 512 {
		return EventRequest{}, fmt.Errorf("%w: external_event_id too long", ErrInvalidEvent)
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return EventRequest{}, fmt.Errorf("%w: prompt required", ErrInvalidEvent)
	}
	payload, err := CanonicalJSON(req.Payload)
	if err != nil {
		return EventRequest{}, fmt.Errorf("%w: payload must be valid JSON", ErrInvalidEvent)
	}
	req.Payload = payload
	req.Metadata = copyStringMap(req.Metadata)
	return req, nil
}

func NewTriggerDeliveryRequest(scheduledAt time.Time, payload json.RawMessage, dryRunRegistration bool) (TriggerDeliveryRequest, error) {
	if scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC()
	}
	return NormalizeTriggerDeliveryRequest(TriggerDeliveryRequest{
		SchemaVersion:      SchemaVersion,
		ScheduledAtUTC:     scheduledAt.UTC().Format(time.RFC3339Nano),
		Payload:            payload,
		DryRunRegistration: dryRunRegistration,
	})
}

func NormalizeTriggerDeliveryRequest(req TriggerDeliveryRequest) (TriggerDeliveryRequest, error) {
	if req.SchemaVersion != SchemaVersion {
		return TriggerDeliveryRequest{}, fmt.Errorf("%w: got %d want %d", ErrUnsupportedSchemaVersion, req.SchemaVersion, SchemaVersion)
	}
	scheduledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.ScheduledAtUTC))
	if err != nil {
		return TriggerDeliveryRequest{}, fmt.Errorf("%w: scheduled_at_utc must be RFC3339", ErrInvalidTriggerDelivery)
	}
	payload, err := CanonicalJSON(req.Payload)
	if err != nil {
		return TriggerDeliveryRequest{}, fmt.Errorf("%w: payload must be valid JSON", ErrInvalidTriggerDelivery)
	}
	req.ScheduledAtUTC = scheduledAt.UTC().Format(time.RFC3339Nano)
	req.Payload = payload
	req.Metadata = copyStringMap(req.Metadata)
	return req, nil
}

func JSONTriggerDelivery(req TriggerDeliveryRequest) (TriggerDelivery, error) {
	normalized, err := NormalizeTriggerDeliveryRequest(req)
	if err != nil {
		return TriggerDelivery{}, err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return TriggerDelivery{}, err
	}
	return TriggerDelivery{Body: body}, nil
}

func WebhookDelivery(body []byte, deliveryIDHeader, deliveryID string) (TriggerDelivery, error) {
	payload, err := CanonicalJSON(body)
	if err != nil {
		return TriggerDelivery{}, fmt.Errorf("%w: webhook body must be valid JSON", ErrInvalidTriggerDelivery)
	}
	deliveryIDHeader = strings.TrimSpace(deliveryIDHeader)
	if deliveryIDHeader == "" {
		deliveryIDHeader = DefaultTriggerDeliveryHeader
	}
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" {
		return TriggerDelivery{}, fmt.Errorf("%w: delivery id required", ErrInvalidTriggerDelivery)
	}
	return TriggerDelivery{
		Body: payload,
		Headers: map[string]string{
			deliveryIDHeader: deliveryID,
		},
	}, nil
}

func (d TriggerDelivery) WithHMACSHA256(secret []byte, signatureHeader, timestampHeader, timestamp string) (TriggerDelivery, error) {
	signatureHeader = strings.TrimSpace(signatureHeader)
	if signatureHeader == "" {
		signatureHeader = DefaultTriggerSignatureHeader
	}
	timestampHeader = strings.TrimSpace(timestampHeader)
	includeTimestamp := timestampHeader != ""
	if includeTimestamp && strings.TrimSpace(timestamp) == "" {
		timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	out := TriggerDelivery{
		Body:    append([]byte(nil), d.Body...),
		Headers: copyStringMap(d.Headers),
	}
	if out.Headers == nil {
		out.Headers = map[string]string{}
	}
	if includeTimestamp {
		out.Headers[timestampHeader] = strings.TrimSpace(timestamp)
	}
	out.Headers[signatureHeader] = SignWebhookHMACSHA256(secret, out.Body, timestamp, includeTimestamp)
	return out, nil
}

func CanonicalJSON(payload json.RawMessage) (json.RawMessage, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), compact.Bytes()...), nil
}

func PayloadSHA256(payload json.RawMessage) (string, error) {
	canonical, err := CanonicalJSON(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func HashExternalEventID(externalEventID string) string {
	externalEventID = strings.TrimSpace(externalEventID)
	if externalEventID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(externalEventID))
	return hex.EncodeToString(sum[:])
}

func MetadataKeys(metadata map[string]string) []string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
