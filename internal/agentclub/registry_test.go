package agentclub

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestRegistryMatchesEnabledTrustedBinding(t *testing.T) {
	registry := testRegistry(t)
	match, err := registry.Match(testRegistryEvent("fixture", "event.created", map[string]string{"project": "fixture-project"}), gatewayapi.SessionOwner{
		ClientType: "ingress",
		ClientID:   "ingress:fixture:prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if match.Descriptor.ID != "event.review" || match.Binding.ClientID != "ingress:fixture:prod" {
		t.Fatalf("match = %#v", match)
	}
}

func TestRegistryRejectsUnknownDisabledMismatchedAndMetadata(t *testing.T) {
	registry := testRegistry(t)
	actor := gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"}
	cases := []struct {
		name string
		req  EventRequest
		want error
	}{
		{name: "unknown capability", req: testRegistryEventWithCapability("missing.review", "fixture", "event.created", nil), want: ErrUnknownCapability},
		{name: "disabled owner", req: testRegistryEventWithCapability("disabled.review", "fixture", "event.created", nil), want: ErrCapabilityDisabled},
		{name: "source mismatch", req: testRegistryEventWithCapability("event.review", "other", "event.created", nil), want: ErrCapabilityDisabled},
		{name: "event mismatch", req: testRegistryEventWithCapability("event.review", "fixture", "event.deleted", nil), want: ErrCapabilityDisabled},
		{name: "metadata key mismatch", req: testRegistryEventWithCapability("event.review", "fixture", "event.created", map[string]string{"actor": "me"}), want: ErrCapabilityDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.Match(tc.req, actor)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRegistryCapabilitiesForActorFiltersEnabledBindings(t *testing.T) {
	registry := testRegistry(t)
	scoped := registry.CapabilitiesForActor(gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"})
	if scoped.SchemaVersion != SchemaVersion || len(scoped.Capabilities) != 1 {
		t.Fatalf("scoped response = %#v", scoped)
	}
	if scoped.Capabilities[0].Descriptor.ID != "event.review" || len(scoped.Capabilities[0].Bindings) != 1 {
		t.Fatalf("scoped capability = %#v", scoped.Capabilities[0])
	}
	if scoped.Capabilities[0].Bindings[0].ClientID != "ingress:fixture:prod" || !scoped.Capabilities[0].Bindings[0].Enabled {
		t.Fatalf("scoped binding = %#v", scoped.Capabilities[0].Bindings[0])
	}

	unscoped := registry.CapabilitiesForActor(gatewayapi.SessionOwner{})
	if len(unscoped.Capabilities) != 1 || len(unscoped.Capabilities[0].Bindings) != 2 {
		t.Fatalf("unscoped response = %#v", unscoped)
	}
}

func TestNewRegistryValidatesDescriptorsAndBindings(t *testing.T) {
	_, err := NewRegistry([]CapabilityDescriptor{testDescriptor("event.review")}, []TrustedBinding{{
		Capability: "missing.review",
		ClientType: "ingress",
		ClientID:   "ingress:fixture:prod",
		Enabled:    true,
	}})
	if !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("missing capability err = %v", err)
	}
	_, err = NewRegistry([]CapabilityDescriptor{testDescriptor("event.review")}, []TrustedBinding{{
		Capability: "event.review",
		ClientType: "telegram",
		ClientID:   "telegram:1",
		Enabled:    true,
	}})
	if !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("client type err = %v", err)
	}
	_, err = NewRegistry([]CapabilityDescriptor{{
		ID:       "event.review",
		Kind:     CapabilityKindReview,
		Risk:     "network",
		Dispatch: DispatchAdmitOnly,
	}}, nil)
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("risk err = %v", err)
	}
	_, err = NewRegistry([]CapabilityDescriptor{{
		ID:          "event.review",
		Kind:        CapabilityKindReview,
		Risk:        RiskReadOnly,
		Dispatch:    DispatchAdmitOnly,
		InputSchema: json.RawMessage(`[]`),
	}}, nil)
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("schema err = %v", err)
	}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(
		[]CapabilityDescriptor{
			testDescriptor("event.review"),
			testDescriptor("disabled.review"),
		},
		[]TrustedBinding{
			{
				Capability:   "event.review",
				ClientType:   "ingress",
				ClientID:     "ingress:fixture:prod",
				Sources:      []string{"fixture"},
				EventTypes:   []string{"event.created"},
				MetadataKeys: []string{"project"},
				Enabled:      true,
			},
			{
				Capability: "event.review",
				ClientType: "ingress",
				ClientID:   "ingress:fixture:stage",
				Sources:    []string{"fixture"},
				EventTypes: []string{"event.created"},
				Enabled:    true,
			},
			{
				Capability: "disabled.review",
				ClientType: "ingress",
				ClientID:   "ingress:fixture:prod",
				Sources:    []string{"fixture"},
				EventTypes: []string{"event.created"},
				Enabled:    false,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testDescriptor(id string) CapabilityDescriptor {
	return CapabilityDescriptor{
		ID:           id,
		Title:        "Fixture Review",
		Description:  "Review fixture events.",
		Kind:         CapabilityKindReview,
		Risk:         RiskReadOnly,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Dispatch:     DispatchAdmitOnly,
		Approval:     ApprovalRequired,
		Version:      "v0",
	}
}

func testRegistryEvent(source, eventType string, metadata map[string]string) EventRequest {
	return testRegistryEventWithCapability("event.review", source, eventType, metadata)
}

func testRegistryEventWithCapability(capability, source, eventType string, metadata map[string]string) EventRequest {
	return EventRequest{
		SchemaVersion:   SchemaVersion,
		Source:          source,
		Capability:      capability,
		EventType:       eventType,
		ExternalEventID: "delivery-1",
		Prompt:          "Review this fixture event.",
		Payload:         json.RawMessage(`{"ok":true}`),
		Metadata:        metadata,
	}
}
