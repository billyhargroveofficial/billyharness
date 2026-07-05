package protocol

import "testing"

func TestParseRiskUsesConservativeMCPAliases(t *testing.T) {
	cases := map[string]Risk{
		"read":       RiskLocalRead,
		"read-only":  RiskLocalRead,
		"network":    RiskNetworkRead,
		"write":      RiskLocalWrite,
		"net_write":  RiskNetworkWrite,
		"exec":       RiskExecute,
		"external":   RiskExternalMutation,
		"secret":     RiskSecretAccess,
		"local_read": RiskLocalRead,
	}
	for input, want := range cases {
		got, ok := ParseRisk(input)
		if !ok || got != want {
			t.Fatalf("ParseRisk(%q) = %q, %v; want %q true", input, got, ok, want)
		}
	}
}

func TestRiskClassMapsCoarseNativeRisks(t *testing.T) {
	cases := map[Risk]Risk{
		RiskReadOnly:     RiskLocalRead,
		RiskNetwork:      RiskNetworkRead,
		RiskWrite:        RiskLocalWrite,
		RiskExecute:      RiskExecute,
		RiskExternal:     RiskExternal,
		RiskSecretAccess: RiskSecretAccess,
	}
	for input, want := range cases {
		if got := RiskClass(input); got != want {
			t.Fatalf("RiskClass(%q) = %q, want %q", input, got, want)
		}
	}
}
