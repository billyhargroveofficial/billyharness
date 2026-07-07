package ingress

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

const inputIDPrefix = "ingress-"

type IngressEvent struct {
	Source          string
	ExternalEventID string
	TargetSessionID string
	Prompt          string
	RawBody         []byte
	Metadata        map[string]string
}

type IngressRule struct {
	ID              string
	Source          string
	TargetSessionID string
	Owner           gatewayapi.SessionOwner
	ClientID        string
	ClientType      string
	Prompt          string
	StaticMetadata  map[string]string
}

type AdmissionDecision struct {
	Admitted        bool
	Reason          string
	RuleID          string
	Source          string
	ExternalEventID string
	PayloadSHA256   string
	TargetSessionID string
	InputID         string
	Request         gatewayapi.SessionInputRequest
	Metadata        SanitizedMetadata
}

type SanitizedMetadata struct {
	Request map[string]string
	Keys    []string
}

type DeterministicInputIDParts struct {
	RuleID          string
	Source          string
	ExternalEventID string
	PayloadSHA256   string
	TargetSessionID string
}

func Admit(event IngressEvent, rule IngressRule) (AdmissionDecision, error) {
	event = normalizeEvent(event)
	rule = normalizeRule(rule)
	payloadSHA := PayloadSHA256(event.RawBody)
	decision := AdmissionDecision{
		RuleID:          rule.ID,
		Source:          event.Source,
		ExternalEventID: event.ExternalEventID,
		PayloadSHA256:   payloadSHA,
	}
	if rule.ID == "" {
		return reject(decision, "rule id required")
	}
	if event.Source == "" {
		return reject(decision, "source required")
	}
	if rule.Source != "" && event.Source != rule.Source {
		return reject(decision, "source not allowed")
	}
	targetSessionID, ok := targetSessionForAdmission(event, rule)
	if !ok {
		return reject(decision, "target session mismatch")
	}
	if targetSessionID == "" {
		return reject(decision, "target session required")
	}
	decision.TargetSessionID = targetSessionID
	prompt := strings.TrimSpace(rule.Prompt)
	if prompt == "" {
		prompt = event.Prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return reject(decision, "prompt required")
	}
	metadata, err := SanitizeMetadata(event.Metadata)
	if err != nil {
		return reject(decision, err.Error())
	}
	metadata = mergeStaticMetadata(metadata, rule.StaticMetadata)
	metadata = mergeStaticMetadata(metadata, map[string]string{
		"ingress.rule_id":        rule.ID,
		"ingress.source":         event.Source,
		"ingress.payload_sha256": payloadSHA,
	})
	inputID := DeterministicInputID(DeterministicInputIDParts{
		RuleID:          rule.ID,
		Source:          event.Source,
		ExternalEventID: event.ExternalEventID,
		PayloadSHA256:   payloadSHA,
		TargetSessionID: targetSessionID,
	})
	clientID := firstNonEmpty(rule.ClientID, rule.Owner.ClientID)
	clientType := firstNonEmpty(rule.ClientType, rule.Owner.ClientType)
	req := gatewayapi.SessionInputRequest{
		InputID:    inputID,
		Prompt:     prompt,
		ClientID:   clientID,
		ClientType: clientType,
		Metadata:   metadata,
	}
	decision.Admitted = true
	decision.Reason = "admitted"
	decision.InputID = inputID
	decision.Request = req
	decision.Metadata = SanitizedMetadata{
		Request: copyStringMap(metadata),
		Keys:    metadataKeys(metadata),
	}
	return decision, nil
}

func PayloadSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func DeterministicInputID(parts DeterministicInputIDParts) string {
	parts.RuleID = strings.TrimSpace(parts.RuleID)
	parts.Source = strings.TrimSpace(parts.Source)
	parts.ExternalEventID = strings.TrimSpace(parts.ExternalEventID)
	parts.PayloadSHA256 = strings.ToLower(strings.TrimSpace(parts.PayloadSHA256))
	parts.TargetSessionID = strings.TrimSpace(parts.TargetSessionID)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		parts.RuleID,
		parts.Source,
		parts.ExternalEventID,
		parts.PayloadSHA256,
		parts.TargetSessionID,
	}, "\x00")))
	return inputIDPrefix + hex.EncodeToString(sum[:])
}

func SanitizeMetadata(metadata map[string]string) (map[string]string, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if unsafePayloadMetadataKey(key) {
			return nil, fmt.Errorf("payload metadata key %q is not allowed", key)
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func UnsafePayloadMetadataKeys() []string {
	out := make([]string, 0, len(unsafePayloadMetadata))
	for key := range unsafePayloadMetadata {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func reject(decision AdmissionDecision, reason string) (AdmissionDecision, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "rejected"
	}
	decision.Admitted = false
	decision.Reason = reason
	return decision, errors.New(reason)
}

func normalizeEvent(event IngressEvent) IngressEvent {
	event.Source = strings.TrimSpace(event.Source)
	event.ExternalEventID = strings.TrimSpace(event.ExternalEventID)
	event.TargetSessionID = strings.TrimSpace(event.TargetSessionID)
	event.Prompt = strings.TrimSpace(event.Prompt)
	event.Metadata = copyStringMap(event.Metadata)
	event.RawBody = append([]byte(nil), event.RawBody...)
	return event
}

func normalizeRule(rule IngressRule) IngressRule {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Source = strings.TrimSpace(rule.Source)
	rule.TargetSessionID = strings.TrimSpace(rule.TargetSessionID)
	rule.ClientID = strings.TrimSpace(rule.ClientID)
	rule.ClientType = strings.TrimSpace(rule.ClientType)
	rule.Prompt = strings.TrimSpace(rule.Prompt)
	rule.StaticMetadata = copyStringMap(rule.StaticMetadata)
	rule.Owner.ClientID = strings.TrimSpace(rule.Owner.ClientID)
	rule.Owner.ClientType = strings.TrimSpace(rule.Owner.ClientType)
	return rule
}

func targetSessionForAdmission(event IngressEvent, rule IngressRule) (string, bool) {
	if rule.TargetSessionID != "" && event.TargetSessionID != "" && rule.TargetSessionID != event.TargetSessionID {
		return "", false
	}
	if rule.TargetSessionID != "" {
		return rule.TargetSessionID, true
	}
	return event.TargetSessionID, true
}

func mergeStaticMetadata(metadata, static map[string]string) map[string]string {
	if len(static) == 0 {
		return metadata
	}
	out := copyStringMap(metadata)
	for key, value := range static {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func unsafePayloadMetadataKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	return unsafePayloadMetadata[key]
}

var unsafePayloadMetadata = map[string]bool{
	"access_mode":               true,
	"argv":                      true,
	"cmd":                       true,
	"command":                   true,
	"dangerous_permission_mode": true,
	"exec":                      true,
	"max_tool_rounds":           true,
	"mcp":                       true,
	"mcp_server":                true,
	"mcp_servers":               true,
	"model":                     true,
	"provider":                  true,
	"reasoning_effort":          true,
	"shell":                     true,
	"thinking":                  true,
	"tool":                      true,
	"tool_name":                 true,
	"tools":                     true,
}
