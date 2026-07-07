package agentclub

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestMapToIngressBuildsNeutralAdmission(t *testing.T) {
	req := EventRequest{
		SchemaVersion:   SchemaVersion,
		Source:          "fixture",
		Capability:      "pull_request.review",
		EventType:       "pull_request.opened",
		ExternalEventID: "delivery-1",
		Prompt:          "Review this event.",
		Payload:         json.RawMessage(`{ "number": 7, "title": "hello" }`),
		Metadata: map[string]string{
			"project": "fixture-project",
		},
	}
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}

	mapping, err := MapToIngress(req, "session-1", owner)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Event.Source != "fixture" || mapping.Event.TargetSessionID != "session-1" || mapping.Event.ExternalEventID != "delivery-1" {
		t.Fatalf("event = %#v", mapping.Event)
	}
	if string(mapping.Event.RawBody) != `{"number":7,"title":"hello"}` {
		t.Fatalf("raw body = %s", mapping.Event.RawBody)
	}
	if mapping.Rule.ID != "pull_request.review" || mapping.Rule.Owner.ClientID != owner.ClientID || mapping.Rule.Owner.ClientType != "ingress" {
		t.Fatalf("rule = %#v", mapping.Rule)
	}
	for _, key := range []string{"agentclub.capability", "agentclub.event_type", "agentclub.dispatch", "agentclub.schema_version", "ingress.policy"} {
		if mapping.Rule.StaticMetadata[key] == "" {
			t.Fatalf("static metadata missing %q: %#v", key, mapping.Rule.StaticMetadata)
		}
	}
	wantKeys := []string{"agentclub.capability", "agentclub.dispatch", "agentclub.event_type", "agentclub.schema_version", "ingress.policy", "project"}
	if !reflect.DeepEqual(mapping.MetadataKeys, wantKeys) {
		t.Fatalf("metadata keys = %#v, want %#v", mapping.MetadataKeys, wantKeys)
	}
	if mapping.PayloadSHA256 == "" || mapping.ExternalEventIDHash == "" || strings.Contains(mapping.ExternalEventIDHash, "delivery") {
		t.Fatalf("hashes = payload:%q external:%q", mapping.PayloadSHA256, mapping.ExternalEventIDHash)
	}

	response := ResponseFromAdmission(mapping, gatewayapi.SessionInputResponse{InputID: "input-1", State: "admitted"}, true)
	if response.SchemaVersion != SchemaVersion || !response.Admitted || response.InputID != "input-1" || response.RunDispatched {
		t.Fatalf("response = %#v", response)
	}
	if response.Source != req.Source || response.Capability != req.Capability || response.EventType != req.EventType {
		t.Fatalf("response identity = %#v", response)
	}
}

func TestMapToIngressRejectsUnsafeAuthorityAndInvalidOwner(t *testing.T) {
	base := EventRequest{
		SchemaVersion:   SchemaVersion,
		Source:          "fixture",
		Capability:      "event.review",
		EventType:       "event.created",
		ExternalEventID: "delivery-1",
		Prompt:          "Review this event.",
		Payload:         json.RawMessage(`{"ok":true}`),
	}
	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}

	unsafe := base
	unsafe.Metadata = map[string]string{"provider": "override"}
	if _, err := MapToIngress(unsafe, "session-1", owner); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("unsafe metadata err = %v", err)
	}

	if _, err := MapToIngress(base, "session-1", gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "telegram"}); !errors.Is(err, ErrInvalidOwner) {
		t.Fatalf("owner err = %v", err)
	}
}

func TestNormalizeEventRequestValidatesIdentifiersSchemaAndPayload(t *testing.T) {
	cases := []struct {
		name string
		req  EventRequest
		want error
	}{
		{name: "schema", req: EventRequest{SchemaVersion: 2}, want: ErrUnsupportedSchemaVersion},
		{name: "source", req: EventRequest{SchemaVersion: 1, Source: "bad/source"}, want: ErrInvalidIdentifier},
		{name: "external id", req: EventRequest{SchemaVersion: 1, Source: "fixture", Capability: "capability", EventType: "event"}, want: ErrInvalidEvent},
		{name: "prompt", req: EventRequest{SchemaVersion: 1, Source: "fixture", Capability: "capability", EventType: "event", ExternalEventID: "id"}, want: ErrInvalidEvent},
		{name: "payload", req: EventRequest{SchemaVersion: 1, Source: "fixture", Capability: "capability", EventType: "event", ExternalEventID: "id", Prompt: "hello", Payload: json.RawMessage(`{bad`)}, want: ErrInvalidEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := NormalizeEventRequest(tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateCapabilityDescriptorRequiresAdmitOnlyDispatch(t *testing.T) {
	err := ValidateCapabilityDescriptor(CapabilityDescriptor{
		ID:       "fixture.review",
		Kind:     CapabilityKindReview,
		Risk:     RiskReadOnly,
		Dispatch: DispatchAdmitOnly,
		Approval: ApprovalRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateCapabilityDescriptor(CapabilityDescriptor{
		ID:       "fixture.review",
		Kind:     CapabilityKindReview,
		Risk:     RiskReadOnly,
		Dispatch: "run",
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err = %v, want invalid event", err)
	}
}
