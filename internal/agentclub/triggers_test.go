package agentclub

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestBuildTriggerDeliveryWebhookUsesBindingAuthority(t *testing.T) {
	binding := testTriggerBinding("fixture-webhook", TriggerKindWebhook)
	delivery, err := BuildTriggerDelivery(TriggerDeliveryInput{
		Binding:    binding,
		RawBody:    []byte(`{"body":"external content"}`),
		DeliveryID: "delivery-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Request.Source != binding.Source ||
		delivery.Request.Capability != binding.Capability ||
		delivery.Request.EventType != binding.EventType ||
		delivery.Request.Prompt != binding.Prompt {
		t.Fatalf("request = %#v", delivery.Request)
	}
	if delivery.Request.ExternalEventID != "trigger:fixture-webhook:delivery:delivery-1" {
		t.Fatalf("external_event_id = %q", delivery.Request.ExternalEventID)
	}
	if delivery.PayloadSHA256 == "" || delivery.ExternalEventIDHash == "" {
		t.Fatalf("hashes missing: %#v", delivery)
	}
}

func TestBuildTriggerDeliveryScheduleRejectsFutureUnlessDryRegistration(t *testing.T) {
	binding := testTriggerBinding("fixture-schedule", TriggerKindSchedule)
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).Format(time.RFC3339)
	_, err := BuildTriggerDelivery(TriggerDeliveryInput{
		Binding:        binding,
		ScheduledAtUTC: future,
		Payload:        json.RawMessage(`{"tick":true}`),
		Now:            now,
	})
	if !errors.Is(err, ErrFutureTriggerDelivery) {
		t.Fatalf("future err = %v", err)
	}
	dry, err := BuildTriggerDelivery(TriggerDeliveryInput{
		Binding:            binding,
		ScheduledAtUTC:     future,
		Payload:            json.RawMessage(`{"tick":true}`),
		DryRunRegistration: true,
		Now:                now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRunRegistration || dry.Request.ExternalEventID != "trigger:fixture-schedule:scheduled:"+future {
		t.Fatalf("dry delivery = %#v", dry)
	}
}

func TestNewRegistryWithTriggersValidatesTrustedBinding(t *testing.T) {
	descriptor := testDescriptor("event.review")
	trusted := TrustedBinding{
		Capability: "event.review",
		ClientType: "ingress",
		ClientID:   "ingress:fixture:prod",
		Sources:    []string{"fixture"},
		EventTypes: []string{"event.created"},
		Enabled:    true,
	}
	trigger := testTriggerBinding("fixture-webhook", TriggerKindWebhook)
	registry, err := NewRegistryWithTriggers([]CapabilityDescriptor{descriptor}, []TrustedBinding{trusted}, []TriggerBinding{trigger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.TriggerBinding("fixture-webhook"); err != nil {
		t.Fatalf("trigger lookup err = %v", err)
	}

	badTrigger := trigger
	badTrigger.Source = "other"
	_, err = NewRegistryWithTriggers([]CapabilityDescriptor{descriptor}, []TrustedBinding{trusted}, []TriggerBinding{badTrigger})
	if !errors.Is(err, ErrInvalidTriggerBinding) {
		t.Fatalf("bad trigger err = %v", err)
	}

	disabledTrigger := trigger
	disabledTrigger.ID = "disabled-trigger"
	disabledTrigger.Enabled = false
	registry, err = NewRegistryWithTriggers([]CapabilityDescriptor{descriptor}, []TrustedBinding{trusted}, []TriggerBinding{disabledTrigger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.TriggerBinding("disabled-trigger"); !errors.Is(err, ErrTriggerDisabled) {
		t.Fatalf("disabled err = %v", err)
	}
}

func testTriggerBinding(id, kind string) TriggerBinding {
	return TriggerBinding{
		ID:               id,
		Kind:             kind,
		Source:           "fixture",
		Capability:       "event.review",
		EventType:        "event.created",
		Owner:            gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID:  "session-1",
		PromptTemplateID: "fixture-review",
		Prompt:           "Review this trigger delivery.",
		AuthMethod:       TriggerAuthNone,
		MaxBodyBytes:     4096,
		Enabled:          true,
	}
}
