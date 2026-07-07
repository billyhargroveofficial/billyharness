package agentclub

import (
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
	TriggerKindWebhook  = "webhook"
	TriggerKindSchedule = "schedule"
	TriggerKindManual   = "manual"

	TriggerAuthNone       = "none"
	TriggerAuthHMACSHA256 = "hmac_sha256"

	DefaultTriggerMaxBodyBytes int64 = 1 << 20
	MaxTriggerBodyBytes        int64 = 8 << 20

	DefaultTriggerSignatureHeader = "X-Hub-Signature-256"
	DefaultTriggerDeliveryHeader  = "X-Billyharness-Delivery-ID"
)

var (
	ErrInvalidTriggerBinding = errors.New("invalid agentclub trigger binding")
	ErrUnknownTriggerBinding = errors.New("unknown agentclub trigger binding")
	ErrTriggerDisabled       = errors.New("disabled agentclub trigger binding")
	ErrInvalidTrigger        = errors.New("invalid agentclub trigger delivery")
	ErrFutureTriggerDelivery = errors.New("agentclub trigger delivery is in the future")
)

type TriggerBinding struct {
	ID                string                  `json:"id"`
	Kind              string                  `json:"kind"`
	Source            string                  `json:"source"`
	Capability        string                  `json:"capability"`
	EventType         string                  `json:"event_type"`
	Owner             gatewayapi.SessionOwner `json:"owner"`
	TargetSessionID   string                  `json:"target_session_id"`
	PromptTemplateID  string                  `json:"prompt_template_id,omitempty"`
	Prompt            string                  `json:"prompt"`
	AuthMethod        string                  `json:"auth_method"`
	HMACSecret        []byte                  `json:"-"`
	SignatureHeader   string                  `json:"signature_header,omitempty"`
	TimestampHeader   string                  `json:"timestamp_header,omitempty"`
	DeliveryIDHeader  string                  `json:"delivery_id_header,omitempty"`
	Metadata          map[string]string       `json:"metadata,omitempty"`
	MaxBodyBytes      int64                   `json:"max_body_bytes,omitempty"`
	Schedule          *ScheduleConfig         `json:"schedule,omitempty"`
	RunPolicy         *RunPolicyConfig        `json:"run_policy,omitempty"`
	Enabled           bool                    `json:"enabled"`
	AllowFutureDryRun bool                    `json:"allow_future_dry_run,omitempty"`
}

type TriggerDeliveryRequest struct {
	SchemaVersion      int               `json:"schema_version"`
	ScheduledAtUTC     string            `json:"scheduled_at_utc"`
	Payload            json.RawMessage   `json:"payload,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	DryRunRegistration bool              `json:"dry_run_registration,omitempty"`
}

type TriggerDeliveryInput struct {
	Binding            TriggerBinding
	RawBody            []byte
	DeliveryID         string
	ScheduledAtUTC     string
	Payload            json.RawMessage
	Metadata           map[string]string
	DryRunRegistration bool
	Now                time.Time
}

type TriggerDelivery struct {
	Request             EventRequest
	Binding             TriggerBinding
	PayloadSHA256       string
	ExternalEventIDHash string
	MetadataKeys        []string
	DryRunRegistration  bool
	ScheduledAtUTC      string
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

func NormalizeTriggerBinding(binding TriggerBinding) (TriggerBinding, error) {
	var err error
	if binding.ID, err = normalizeIdentifier("binding_id", binding.ID); err != nil {
		return TriggerBinding{}, err
	}
	binding.Kind = strings.ToLower(strings.TrimSpace(binding.Kind))
	if !allowedTriggerKinds[binding.Kind] {
		return TriggerBinding{}, fmt.Errorf("%w: unsupported kind %q", ErrInvalidTriggerBinding, binding.Kind)
	}
	if binding.Source, err = normalizeIdentifier("source", binding.Source); err != nil {
		return TriggerBinding{}, err
	}
	if binding.Capability, err = normalizeIdentifier("capability", binding.Capability); err != nil {
		return TriggerBinding{}, err
	}
	if binding.EventType, err = normalizeIdentifier("event_type", binding.EventType); err != nil {
		return TriggerBinding{}, err
	}
	binding.Owner = normalizeOwner(binding.Owner)
	if binding.Owner.ClientType != "ingress" || binding.Owner.ClientID == "" {
		return TriggerBinding{}, fmt.Errorf("%w: owner must be client_type=ingress with client_id", ErrInvalidTriggerBinding)
	}
	binding.TargetSessionID = strings.TrimSpace(binding.TargetSessionID)
	if binding.TargetSessionID == "" {
		return TriggerBinding{}, fmt.Errorf("%w: target_session_id required", ErrInvalidTriggerBinding)
	}
	if binding.PromptTemplateID, err = normalizeOptionalIdentifier("prompt_template_id", binding.PromptTemplateID); err != nil {
		return TriggerBinding{}, err
	}
	binding.Prompt = strings.TrimSpace(binding.Prompt)
	if binding.Prompt == "" {
		return TriggerBinding{}, fmt.Errorf("%w: prompt required", ErrInvalidTriggerBinding)
	}
	binding.AuthMethod = strings.ToLower(strings.TrimSpace(binding.AuthMethod))
	if binding.AuthMethod == "" {
		binding.AuthMethod = TriggerAuthNone
	}
	if !allowedTriggerAuthMethods[binding.AuthMethod] {
		return TriggerBinding{}, fmt.Errorf("%w: unsupported auth_method %q", ErrInvalidTriggerBinding, binding.AuthMethod)
	}
	if binding.AuthMethod == TriggerAuthHMACSHA256 && binding.Enabled && len(binding.HMACSecret) == 0 {
		return TriggerBinding{}, fmt.Errorf("%w: hmac secret required", ErrInvalidTriggerBinding)
	}
	if binding.Kind != TriggerKindWebhook && binding.AuthMethod != TriggerAuthNone {
		return TriggerBinding{}, fmt.Errorf("%w: non-webhook triggers must use auth_method=none", ErrInvalidTriggerBinding)
	}
	binding.SignatureHeader = normalizeHeaderName(binding.SignatureHeader)
	if binding.SignatureHeader == "" && binding.AuthMethod == TriggerAuthHMACSHA256 {
		binding.SignatureHeader = DefaultTriggerSignatureHeader
	}
	binding.TimestampHeader = normalizeHeaderName(binding.TimestampHeader)
	binding.DeliveryIDHeader = normalizeHeaderName(binding.DeliveryIDHeader)
	if binding.DeliveryIDHeader == "" && binding.Kind == TriggerKindWebhook {
		binding.DeliveryIDHeader = DefaultTriggerDeliveryHeader
	}
	if binding.MaxBodyBytes <= 0 {
		binding.MaxBodyBytes = DefaultTriggerMaxBodyBytes
	}
	if binding.MaxBodyBytes > MaxTriggerBodyBytes {
		return TriggerBinding{}, fmt.Errorf("%w: max_body_bytes exceeds %d", ErrInvalidTriggerBinding, MaxTriggerBodyBytes)
	}
	if binding.Metadata, err = ingress.SanitizeMetadata(binding.Metadata); err != nil {
		return TriggerBinding{}, fmt.Errorf("%w: %v", ErrInvalidTriggerBinding, err)
	}
	binding.HMACSecret = append([]byte(nil), binding.HMACSecret...)
	return binding, nil
}

func BuildTriggerDelivery(input TriggerDeliveryInput) (TriggerDelivery, error) {
	binding, err := NormalizeTriggerBinding(input.Binding)
	if err != nil {
		return TriggerDelivery{}, err
	}
	if !binding.Enabled {
		return TriggerDelivery{}, fmt.Errorf("%w: %s", ErrTriggerDisabled, binding.ID)
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var payload []byte
	var externalEventID string
	var scheduledAt string
	switch binding.Kind {
	case TriggerKindWebhook:
		payload, err = canonicalPayload(input.RawBody)
		if err != nil {
			return TriggerDelivery{}, err
		}
		deliveryID := strings.TrimSpace(input.DeliveryID)
		if deliveryID == "" {
			return TriggerDelivery{}, fmt.Errorf("%w: delivery id required", ErrInvalidTrigger)
		}
		if len(deliveryID) > 256 {
			return TriggerDelivery{}, fmt.Errorf("%w: delivery id too long", ErrInvalidTrigger)
		}
		externalEventID = "trigger:" + binding.ID + ":delivery:" + deliveryID
	case TriggerKindSchedule, TriggerKindManual:
		scheduled, err := parseScheduledAtUTC(input.ScheduledAtUTC)
		if err != nil {
			return TriggerDelivery{}, err
		}
		if scheduled.After(now.UTC()) && !input.DryRunRegistration {
			return TriggerDelivery{}, ErrFutureTriggerDelivery
		}
		scheduledAt = scheduled.Format(time.RFC3339Nano)
		payload, err = canonicalPayload(input.Payload)
		if err != nil {
			return TriggerDelivery{}, err
		}
		externalEventID = "trigger:" + binding.ID + ":scheduled:" + scheduledAt
	default:
		return TriggerDelivery{}, fmt.Errorf("%w: unsupported kind %q", ErrInvalidTriggerBinding, binding.Kind)
	}
	metadata := mergeTriggerMetadata(input.Metadata, binding.Metadata)
	normalizedReq := EventRequest{
		SchemaVersion:   SchemaVersion,
		Source:          binding.Source,
		Capability:      binding.Capability,
		EventType:       binding.EventType,
		ExternalEventID: externalEventID,
		Prompt:          binding.Prompt,
		Payload:         append(json.RawMessage(nil), payload...),
		Metadata:        metadata,
	}
	normalizedReq, payload, err = NormalizeEventRequest(normalizedReq)
	if err != nil {
		return TriggerDelivery{}, err
	}
	return TriggerDelivery{
		Request:             normalizedReq,
		Binding:             binding,
		PayloadSHA256:       ingress.PayloadSHA256(payload),
		ExternalEventIDHash: HashExternalEventID(normalizedReq.ExternalEventID),
		MetadataKeys:        metadataKeys(metadata),
		DryRunRegistration:  input.DryRunRegistration,
		ScheduledAtUTC:      scheduledAt,
	}, nil
}

func SignTriggerWebhookHMAC(binding TriggerBinding, body []byte, now time.Time) (signatureHeader, signature, timestampHeader, timestamp string, err error) {
	binding, err = NormalizeTriggerBinding(binding)
	if err != nil {
		return "", "", "", "", err
	}
	if binding.Kind != TriggerKindWebhook {
		return "", "", "", "", fmt.Errorf("%w: hmac signing is only valid for webhook triggers", ErrInvalidTriggerBinding)
	}
	if binding.AuthMethod != TriggerAuthHMACSHA256 {
		return "", "", "", "", nil
	}
	if len(binding.HMACSecret) == 0 {
		return "", "", "", "", fmt.Errorf("%w: hmac secret required", ErrInvalidTriggerBinding)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	timestampHeader = binding.TimestampHeader
	includeTimestamp := timestampHeader != ""
	if includeTimestamp {
		timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	signatureHeader = binding.SignatureHeader
	signature = ingress.SignRawBodyHMACSHA256(binding.HMACSecret, body, timestamp, includeTimestamp)
	return signatureHeader, signature, timestampHeader, timestamp, nil
}

func MapTriggerToIngress(delivery TriggerDelivery) (AdmissionMapping, error) {
	req, payload, err := NormalizeEventRequest(delivery.Request)
	if err != nil {
		return AdmissionMapping{}, err
	}
	binding, err := NormalizeTriggerBinding(delivery.Binding)
	if err != nil {
		return AdmissionMapping{}, err
	}
	if !binding.Enabled {
		return AdmissionMapping{}, fmt.Errorf("%w: %s", ErrTriggerDisabled, binding.ID)
	}
	if req.Source != binding.Source || req.Capability != binding.Capability || req.EventType != binding.EventType {
		return AdmissionMapping{}, fmt.Errorf("%w: trigger request does not match binding", ErrInvalidTrigger)
	}
	eventMetadata, err := ingress.SanitizeMetadata(req.Metadata)
	if err != nil {
		return AdmissionMapping{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	staticMetadata := map[string]string{
		"agentclub.capability":         req.Capability,
		"agentclub.dispatch":           DispatchAdmitOnly,
		"agentclub.event_type":         req.EventType,
		"agentclub.schema_version":     "1",
		"agentclub.trigger_binding_id": binding.ID,
		"agentclub.trigger_kind":       binding.Kind,
		"ingress.policy":               "agentclub_trigger_admit_only",
	}
	if binding.PromptTemplateID != "" {
		staticMetadata["agentclub.prompt_template_id"] = binding.PromptTemplateID
	}
	keys := metadataKeys(eventMetadata)
	keys = append(keys, metadataKeys(staticMetadata)...)
	sort.Strings(keys)
	return AdmissionMapping{
		Request: req,
		Event: ingress.IngressEvent{
			Source:          req.Source,
			ExternalEventID: req.ExternalEventID,
			TargetSessionID: binding.TargetSessionID,
			Prompt:          req.Prompt,
			RawBody:         payload,
			Metadata:        eventMetadata,
		},
		Rule: ingress.IngressRule{
			ID:              binding.ID,
			Source:          req.Source,
			TargetSessionID: binding.TargetSessionID,
			Owner:           binding.Owner,
			StaticMetadata:  staticMetadata,
		},
		PayloadSHA256:       ingress.PayloadSHA256(payload),
		ExternalEventIDHash: HashExternalEventID(req.ExternalEventID),
		MetadataKeys:        keys,
	}, nil
}

func ResponseFromTriggerDelivery(delivery TriggerDelivery, input gatewayapi.SessionInputResponse, admitted bool) TriggerDeliveryResponse {
	state := input.State
	if state == "" && delivery.DryRunRegistration {
		state = "dry_registration"
	}
	return TriggerDeliveryResponse{
		SchemaVersion:       SchemaVersion,
		Admitted:            admitted,
		InputID:             input.InputID,
		State:               state,
		Duplicate:           input.Duplicate,
		TargetSessionID:     delivery.Binding.TargetSessionID,
		BindingID:           delivery.Binding.ID,
		TriggerKind:         delivery.Binding.Kind,
		Source:              delivery.Request.Source,
		Capability:          delivery.Request.Capability,
		EventType:           delivery.Request.EventType,
		PayloadSHA256:       delivery.PayloadSHA256,
		ExternalEventIDHash: delivery.ExternalEventIDHash,
		MetadataKeys:        append([]string(nil), delivery.MetadataKeys...),
		RunDispatched:       false,
	}
}

func cloneTriggerBinding(binding TriggerBinding) TriggerBinding {
	binding.HMACSecret = append([]byte(nil), binding.HMACSecret...)
	binding.Metadata = copyStringMap(binding.Metadata)
	binding.Schedule = cloneScheduleConfig(binding.Schedule)
	binding.RunPolicy = cloneRunPolicyConfig(binding.RunPolicy)
	return binding
}

func parseScheduledAtUTC(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("%w: scheduled_at_utc required", ErrInvalidTrigger)
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: scheduled_at_utc must be RFC3339", ErrInvalidTrigger)
	}
	return ts.UTC(), nil
}

func mergeTriggerMetadata(request, trusted map[string]string) map[string]string {
	out := copyStringMap(request)
	for key, value := range trusted {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[key] = value
	}
	return out
}

func normalizeHeaderName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, "\r\n:") {
		return ""
	}
	return value
}

var allowedTriggerKinds = map[string]bool{
	TriggerKindWebhook:  true,
	TriggerKindSchedule: true,
	TriggerKindManual:   true,
}

var allowedTriggerAuthMethods = map[string]bool{
	TriggerAuthNone:       true,
	TriggerAuthHMACSHA256: true,
}
