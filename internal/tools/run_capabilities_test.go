package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestBoundedIsolatedCapabilitiesFilterToolsAndDenyCalls(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeBoundedIsolatedPlanV1,
		[]string{"web_fetch", "time_now"},
		[]string{"https://example.com/api"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	specs := snapshot.Specs()
	if len(specs) != 2 || specs[0].Name != "time_now" || specs[1].Name != "web_fetch" {
		t.Fatalf("bounded specs = %#v", specs)
	}

	result, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: []byte(`{"path":"README.md"}`),
	})
	if err == nil || result.ErrorCode != "permission_denied" ||
		result.Metadata["permission_reason"] != "tool_not_allowlisted_for_run" {
		t.Fatalf("disallowed call result=%#v err=%v", result, err)
	}
}

func TestBoundedIsolatedCapabilitiesRejectURLBeforeHandler(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeBoundedIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.com/api"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	result, err := snapshot.Call(t.Context(), protocol.ToolCall{
		Name:      "web_fetch",
		Arguments: []byte(`{"url":"https://example.com/private"}`),
	})
	if err == nil || result.ErrorCode != "permission_denied" ||
		result.Metadata["permission_reason"] != "url_not_allowlisted_for_run" ||
		!strings.Contains(err.Error(), "outside allowed_url_prefixes") {
		t.Fatalf("URL denial result=%#v err=%v", result, err)
	}
}

func TestRunCapabilitiesAttestationCanonicalHashesAndVersion(t *testing.T) {
	registry := NewRegistry(config.Default())
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeBoundedIsolatedPlanV1,
		[]string{"web_fetch", "time_now"},
		[]string{"https://b.example:443/api/", "https://a.example/"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := capabilities.Attestation()
	toolsHash := sha256.Sum256([]byte(`["time_now","web_fetch"]`))
	prefixHash := sha256.Sum256([]byte(`["https://a.example/","https://b.example/api"]`))
	if got.Scope != protocol.CapabilityScopeBoundedIsolatedPlanV1 ||
		got.AllowedToolsCount != 2 ||
		got.AllowedToolsSHA256 != hex.EncodeToString(toolsHash[:]) ||
		got.AllowedURLPrefixesCount != 2 ||
		got.AllowedURLPrefixesSHA256 != hex.EncodeToString(prefixHash[:]) {
		t.Fatalf("attestation = %#v", got)
	}
}

func TestRunCapabilitiesAcceptsLegacyIsolatedScope(t *testing.T) {
	registry := NewRegistry(config.Default())
	if _, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.com/api"},
	); err != nil {
		t.Fatalf("legacy isolated scope regressed: %v", err)
	}
}
