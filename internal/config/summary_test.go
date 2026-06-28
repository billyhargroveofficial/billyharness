package config

import (
	"strings"
	"testing"
)

func TestFormatSummaryUsesSanitizedValuesAndProvenance(t *testing.T) {
	text := FormatSummary([]ResolvedValue{
		{Key: "provider", Value: "deepseek", Source: SourceEnvironment, SourceKey: "FAST_AGENT_PROVIDER"},
		{Key: "model", Value: "deepseek-v4-flash", Source: SourceGateway, SourceKey: "model"},
		{Key: "api_key", Value: "sk-secret", Redacted: true, Source: SourceEnvironment, SourceKey: "DEEPSEEK_API_KEY"},
	}, []string{"sample warning"})
	for _, want := range []string{"billyharness config", "provider:", "deepseek", "FAST_AGENT_PROVIDER", "model:", "deepseek-v4-flash", "sample warning"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "sk-secret") {
		t.Fatalf("summary leaked secret:\n%s", text)
	}
}
