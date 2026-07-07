package agentclub

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/ingress"
)

var (
	ErrInvalidBinding     = errors.New("invalid agentclub binding")
	ErrUnknownCapability  = errors.New("unknown agentclub capability")
	ErrCapabilityDisabled = errors.New("disabled agentclub capability")
)

type TrustedBinding struct {
	Capability   string   `json:"capability"`
	ClientType   string   `json:"client_type"`
	ClientID     string   `json:"client_id"`
	Sources      []string `json:"sources,omitempty"`
	EventTypes   []string `json:"event_types,omitempty"`
	MetadataKeys []string `json:"metadata_keys,omitempty"`
	Enabled      bool     `json:"enabled"`
}

type BindingView struct {
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

type Registry struct {
	capabilities map[string]CapabilityDescriptor
	bindings     []TrustedBinding
}

type BindingMatch struct {
	Descriptor CapabilityDescriptor
	Binding    TrustedBinding
}

func NewRegistry(descriptors []CapabilityDescriptor, bindings []TrustedBinding) (*Registry, error) {
	r := &Registry{
		capabilities: make(map[string]CapabilityDescriptor, len(descriptors)),
	}
	for _, descriptor := range descriptors {
		normalized, err := NormalizeCapabilityDescriptor(descriptor)
		if err != nil {
			return nil, err
		}
		if _, exists := r.capabilities[normalized.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate capability %q", ErrInvalidEvent, normalized.ID)
		}
		r.capabilities[normalized.ID] = normalized
	}
	for _, binding := range bindings {
		normalized, err := NormalizeTrustedBinding(binding)
		if err != nil {
			return nil, err
		}
		if _, ok := r.capabilities[normalized.Capability]; !ok {
			return nil, fmt.Errorf("%w: binding references %q", ErrUnknownCapability, normalized.Capability)
		}
		r.bindings = append(r.bindings, normalized)
	}
	sort.Slice(r.bindings, func(i, j int) bool {
		return bindingSortKey(r.bindings[i]) < bindingSortKey(r.bindings[j])
	})
	return r, nil
}

func NormalizeTrustedBinding(binding TrustedBinding) (TrustedBinding, error) {
	var err error
	if binding.Capability, err = normalizeIdentifier("capability", binding.Capability); err != nil {
		return TrustedBinding{}, err
	}
	binding.ClientType = strings.ToLower(strings.TrimSpace(binding.ClientType))
	if binding.ClientType != "ingress" {
		return TrustedBinding{}, fmt.Errorf("%w: binding client_type must be ingress", ErrInvalidBinding)
	}
	binding.ClientID = strings.TrimSpace(binding.ClientID)
	if binding.ClientID == "" {
		return TrustedBinding{}, fmt.Errorf("%w: binding client_id required", ErrInvalidBinding)
	}
	if len(binding.ClientID) > 256 {
		return TrustedBinding{}, fmt.Errorf("%w: binding client_id too long", ErrInvalidBinding)
	}
	if binding.Sources, err = normalizeIdentifierList("source", binding.Sources); err != nil {
		return TrustedBinding{}, err
	}
	if binding.EventTypes, err = normalizeIdentifierList("event_type", binding.EventTypes); err != nil {
		return TrustedBinding{}, err
	}
	if binding.MetadataKeys, err = normalizeMetadataKeys(binding.MetadataKeys); err != nil {
		return TrustedBinding{}, err
	}
	return binding, nil
}

func (r *Registry) Match(req EventRequest, actor gatewayapi.SessionOwner) (BindingMatch, error) {
	if r == nil {
		return BindingMatch{}, nil
	}
	normalized, _, err := NormalizeEventRequest(req)
	if err != nil {
		return BindingMatch{}, err
	}
	actor = normalizeOwner(actor)
	descriptor, ok := r.capabilities[normalized.Capability]
	if !ok {
		return BindingMatch{}, fmt.Errorf("%w: %s", ErrUnknownCapability, normalized.Capability)
	}
	for _, binding := range r.bindings {
		if !binding.Enabled ||
			binding.Capability != normalized.Capability ||
			binding.ClientType != actor.ClientType ||
			binding.ClientID != actor.ClientID ||
			!matchesOptionalSet(binding.Sources, normalized.Source) ||
			!matchesOptionalSet(binding.EventTypes, normalized.EventType) ||
			!metadataAllowed(binding.MetadataKeys, normalized.Metadata) {
			continue
		}
		return BindingMatch{Descriptor: descriptor, Binding: binding}, nil
	}
	return BindingMatch{}, fmt.Errorf("%w: %s for %s/%s", ErrCapabilityDisabled, normalized.Capability, normalized.Source, normalized.EventType)
}

func (r *Registry) CapabilitiesForActor(actor gatewayapi.SessionOwner) CapabilityListResponse {
	resp := CapabilityListResponse{SchemaVersion: SchemaVersion}
	if r == nil {
		return resp
	}
	actor = normalizeOwner(actor)
	viewsByID := map[string]*CapabilityView{}
	for _, binding := range r.bindings {
		if !binding.Enabled {
			continue
		}
		if !sessionOwnerEmpty(actor) && (binding.ClientType != actor.ClientType || binding.ClientID != actor.ClientID) {
			continue
		}
		descriptor, ok := r.capabilities[binding.Capability]
		if !ok {
			continue
		}
		view := viewsByID[descriptor.ID]
		if view == nil {
			resp.Capabilities = append(resp.Capabilities, CapabilityView{Descriptor: descriptor})
			view = &resp.Capabilities[len(resp.Capabilities)-1]
			viewsByID[descriptor.ID] = view
		}
		view.Bindings = append(view.Bindings, BindingView{
			Capability:   binding.Capability,
			ClientType:   binding.ClientType,
			ClientID:     binding.ClientID,
			Sources:      append([]string(nil), binding.Sources...),
			EventTypes:   append([]string(nil), binding.EventTypes...),
			MetadataKeys: append([]string(nil), binding.MetadataKeys...),
			Enabled:      binding.Enabled,
		})
	}
	sort.Slice(resp.Capabilities, func(i, j int) bool {
		return resp.Capabilities[i].Descriptor.ID < resp.Capabilities[j].Descriptor.ID
	})
	for i := range resp.Capabilities {
		sort.Slice(resp.Capabilities[i].Bindings, func(a, b int) bool {
			return bindingViewSortKey(resp.Capabilities[i].Bindings[a]) < bindingViewSortKey(resp.Capabilities[i].Bindings[b])
		})
	}
	return resp
}

func normalizeIdentifierList(field string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeIdentifier(field, value)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeMetadataKeys(keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, err := ingress.SanitizeMetadata(map[string]string{key: "allowed"}); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidBinding, err)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

func matchesOptionalSet(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}

func metadataAllowed(allowedKeys []string, metadata map[string]string) bool {
	if len(allowedKeys) == 0 || len(metadata) == 0 {
		return true
	}
	allowed := map[string]bool{}
	for _, key := range allowedKeys {
		allowed[key] = true
	}
	for key, value := range metadata {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if !allowed[strings.TrimSpace(key)] {
			return false
		}
	}
	return true
}

func sessionOwnerEmpty(owner gatewayapi.SessionOwner) bool {
	owner = normalizeOwner(owner)
	return owner.ClientID == "" && owner.ClientType == ""
}

func bindingSortKey(binding TrustedBinding) string {
	return strings.Join([]string{
		binding.Capability,
		binding.ClientType,
		binding.ClientID,
		strings.Join(binding.Sources, ","),
		strings.Join(binding.EventTypes, ","),
	}, "\x00")
}

func bindingViewSortKey(binding BindingView) string {
	return strings.Join([]string{
		binding.Capability,
		binding.ClientType,
		binding.ClientID,
		strings.Join(binding.Sources, ","),
		strings.Join(binding.EventTypes, ","),
	}, "\x00")
}
