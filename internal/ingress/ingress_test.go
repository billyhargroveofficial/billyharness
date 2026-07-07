package ingress

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestAdmitBuildsDeterministicSessionInput(t *testing.T) {
	event := IngressEvent{
		Source:          "github",
		ExternalEventID: "delivery-1",
		TargetSessionID: "session-1",
		Prompt:          "review this delivery",
		RawBody:         []byte(`{"action":"opened"}`),
		Metadata: map[string]string{
			"repository": "owner/repo",
		},
	}
	rule := IngressRule{
		ID:     "github-pr",
		Source: "github",
		Owner: gatewayapi.SessionOwner{
			ClientID:   "ingress:github:prod",
			ClientType: "ingress",
		},
		StaticMetadata: map[string]string{
			"ingress.policy": "read_only_review",
		},
	}

	first, err := Admit(event, rule)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Admit(event, rule)
	if err != nil {
		t.Fatal(err)
	}
	if first.InputID == "" || first.InputID != second.InputID || !strings.HasPrefix(first.InputID, inputIDPrefix) {
		t.Fatalf("input ids = %q %q", first.InputID, second.InputID)
	}
	if !first.Admitted || first.TargetSessionID != "session-1" || first.Request.InputID != first.InputID {
		t.Fatalf("decision = %#v", first)
	}
	if first.Request.ClientID != "ingress:github:prod" || first.Request.ClientType != "ingress" || first.Request.Prompt != "review this delivery" {
		t.Fatalf("request = %#v", first.Request)
	}
	for _, key := range []string{"repository", "ingress.policy", "ingress.rule_id", "ingress.source", "ingress.payload_sha256"} {
		if first.Request.Metadata[key] == "" {
			t.Fatalf("metadata missing %q: %#v", key, first.Request.Metadata)
		}
	}
	wantKeys := []string{"ingress.payload_sha256", "ingress.policy", "ingress.rule_id", "ingress.source", "repository"}
	if !reflect.DeepEqual(first.Metadata.Keys, wantKeys) {
		t.Fatalf("metadata keys = %#v, want %#v", first.Metadata.Keys, wantKeys)
	}
}

func TestAdmitRejectsUnallowlistedSourceAndTargetMismatch(t *testing.T) {
	_, err := Admit(
		IngressEvent{Source: "gitlab", TargetSessionID: "session-1", Prompt: "hello"},
		IngressRule{ID: "github-pr", Source: "github"},
	)
	if err == nil || !strings.Contains(err.Error(), "source not allowed") {
		t.Fatalf("source error = %v", err)
	}

	_, err = Admit(
		IngressEvent{Source: "github", TargetSessionID: "session-2", Prompt: "hello"},
		IngressRule{ID: "github-pr", Source: "github", TargetSessionID: "session-1"},
	)
	if err == nil || !strings.Contains(err.Error(), "target session mismatch") {
		t.Fatalf("target error = %v", err)
	}
}

func TestAdmitRejectsUnsafePayloadMetadataOverrides(t *testing.T) {
	for _, key := range []string{"provider", "model", "access_mode", "tool", "mcp_server", "cmd"} {
		t.Run(key, func(t *testing.T) {
			_, err := Admit(
				IngressEvent{
					Source:          "github",
					TargetSessionID: "session-1",
					Prompt:          "hello",
					Metadata:        map[string]string{key: "override"},
				},
				IngressRule{ID: "github-pr", Source: "github"},
			)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("metadata override error = %v", err)
			}
		})
	}
}

func TestAdmitAllowsRuleStaticMetadataForTrustedPolicyLabels(t *testing.T) {
	decision, err := Admit(
		IngressEvent{Source: "github", TargetSessionID: "session-1", Prompt: "hello"},
		IngressRule{
			ID:     "github-pr",
			Source: "github",
			StaticMetadata: map[string]string{
				"provider":    "gateway-default",
				"access_mode": "configured",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Request.Metadata["provider"] != "gateway-default" || decision.Request.Metadata["access_mode"] != "configured" {
		t.Fatalf("static metadata = %#v", decision.Request.Metadata)
	}
}

func TestDeterministicInputIDChangesForPayloadOrTarget(t *testing.T) {
	base := DeterministicInputIDParts{
		RuleID:          "rule",
		Source:          "source",
		ExternalEventID: "event",
		PayloadSHA256:   PayloadSHA256([]byte("one")),
		TargetSessionID: "session-1",
	}
	first := DeterministicInputID(base)
	base.PayloadSHA256 = PayloadSHA256([]byte("two"))
	if got := DeterministicInputID(base); got == first {
		t.Fatalf("input id did not change after payload hash changed: %q", got)
	}
	base.PayloadSHA256 = PayloadSHA256([]byte("one"))
	base.TargetSessionID = "session-2"
	if got := DeterministicInputID(base); got == first {
		t.Fatalf("input id did not change after target session changed: %q", got)
	}
}

func TestVerifyRawBodyHMACSHA256(t *testing.T) {
	secret := []byte("top-secret")
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1700000000, 0).UTC()
	timestamp := now.Format(time.RFC3339Nano)
	signature := SignRawBodyHMACSHA256(secret, body, timestamp, true)

	if err := VerifyRawBodyHMACSHA256(HMACVerification{
		Secret:           secret,
		Body:             body,
		Signature:        signature,
		Timestamp:        timestamp,
		Now:              now,
		MaxSkew:          time.Minute,
		IncludeTimestamp: true,
	}); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	if err := VerifyRawBodyHMACSHA256(HMACVerification{
		Secret:           secret,
		Body:             []byte(`{"hello":"mutated"}`),
		Signature:        signature,
		Timestamp:        timestamp,
		Now:              now,
		MaxSkew:          time.Minute,
		IncludeTimestamp: true,
	}); !errors.Is(err, ErrInvalidHMACSignature) {
		t.Fatalf("mutated body error = %v", err)
	}

	if err := VerifyRawBodyHMACSHA256(HMACVerification{
		Secret: secret,
		Body:   body,
	}); !errors.Is(err, ErrMissingHMACSignature) {
		t.Fatalf("missing signature error = %v", err)
	}

	if err := VerifyRawBodyHMACSHA256(HMACVerification{
		Secret:           secret,
		Body:             body,
		Signature:        "sha256=deadbeef",
		Timestamp:        timestamp,
		Now:              now,
		MaxSkew:          time.Minute,
		IncludeTimestamp: true,
	}); !errors.Is(err, ErrInvalidHMACSignature) {
		t.Fatalf("invalid signature error = %v", err)
	}

	if err := VerifyRawBodyHMACSHA256(HMACVerification{
		Secret:           secret,
		Body:             body,
		Signature:        signature,
		Timestamp:        now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
		Now:              now,
		MaxSkew:          time.Minute,
		IncludeTimestamp: true,
	}); !errors.Is(err, ErrStaleHMACTimestamp) {
		t.Fatalf("stale timestamp error = %v", err)
	}
}
