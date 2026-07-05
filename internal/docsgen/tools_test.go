package docsgen

import (
	"bytes"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestToolsReferenceCoversNativeRegistry(t *testing.T) {
	output, err := GenerateTools()
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(config.BuiltIn())
	for _, spec := range registry.Specs() {
		if count := bytes.Count(output, []byte(toolHeading(spec.Name))); count != 1 {
			t.Fatalf("tool %s appears %d times", spec.Name, count)
		}
	}
}

func TestRiskClassesParse(t *testing.T) {
	if len(protocol.RiskClasses()) != 11 {
		t.Fatalf("RiskClasses length = %d, want 11", len(protocol.RiskClasses()))
	}
	for _, risk := range protocol.RiskClasses() {
		if _, ok := protocol.ParseRisk(string(risk)); !ok {
			t.Fatalf("ParseRisk does not accept %s", risk)
		}
	}
	if risk, ok := protocol.ParseRisk("external"); !ok || risk != protocol.RiskExternalMutation {
		t.Fatalf("external MCP alias = %s, %v; want %s true", risk, ok, protocol.RiskExternalMutation)
	}
}
