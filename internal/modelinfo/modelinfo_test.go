package modelinfo

import (
	"strings"
	"testing"
)

func TestNormalizeAlias(t *testing.T) {
	tests := map[string]string{
		"flash":       "deepseek-v4-flash",
		"v4 pro":      "deepseek-v4-pro",
		"gpt":         "gpt-5.5",
		"gpt mini":    "gpt-5.4-mini",
		"codex spark": "gpt-5.3-codex-spark",
	}
	for input, want := range tests {
		if got := NormalizeAlias(input); got != want {
			t.Fatalf("NormalizeAlias(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProviderForModelFollowsKnownFamilies(t *testing.T) {
	if got := ProviderForModel("gpt-5.5", "deepseek"); got != ProviderOpenAICodex {
		t.Fatalf("provider = %q", got)
	}
	if got := ProviderForModel("deepseek-v4-flash", "openai-codex"); got != ProviderDeepSeek {
		t.Fatalf("provider = %q", got)
	}
	if got := ProviderForModel("custom-model", "mock"); got != ProviderMock {
		t.Fatalf("provider = %q", got)
	}
}

func TestIsSparkModelUsesAliases(t *testing.T) {
	if !IsSparkModel("spark") || !IsSparkModel("gpt-5.3-codex-spark") {
		t.Fatalf("spark aliases were not detected")
	}
	if IsSparkModel("gpt-5.4-mini") {
		t.Fatalf("mini should not be detected as spark")
	}
}

func TestLookupIncludesBillingHints(t *testing.T) {
	flash := Lookup("deepseek-v4-flash")
	if flash.Provider != ProviderDeepSeek || flash.Pricing.CacheMissPer1M <= 0 || flash.Subscription {
		t.Fatalf("flash = %#v", flash)
	}
	gpt := Lookup("gpt-5.5")
	if gpt.Provider != ProviderOpenAICodex || !gpt.Subscription || gpt.Pricing.OutputPer1M != 0 {
		t.Fatalf("gpt = %#v", gpt)
	}
}

func TestLookupIncludesCapabilityMetadata(t *testing.T) {
	flash := Lookup("deepseek-v4-flash")
	if flash.ContextWindowTokens != 1_000_000 || !flash.ToolCalls || !flash.ParallelToolCalls || !flash.Streaming ||
		flash.MaxOutputTokens != 8192 ||
		flash.HelperModels.WebSummary != "deepseek-v4-flash" ||
		flash.HelperModels.Memory != "deepseek-v4-flash" ||
		flash.CostMode != "metered" ||
		!flash.Reasoning ||
		!hasString(flash.ReasoningModes, "max") ||
		!hasString(flash.TokenAccountingFields, "reasoning_tokens") ||
		!hasString(flash.CacheAccountingFields, "cache_hit_tokens") {
		t.Fatalf("flash capabilities = %#v", flash)
	}
	gpt := Lookup("gpt-5.5")
	if gpt.ContextWindowTokens != 1_000_000 || !gpt.ToolCalls || !gpt.Streaming ||
		gpt.MaxOutputTokens != 8192 ||
		gpt.HelperModels.WebSummary != "gpt-5.4-mini" ||
		gpt.HelperModels.Memory != "gpt-5.4-mini" ||
		gpt.CostMode != "subscription" ||
		!hasString(gpt.ReasoningModes, "minimal") ||
		!hasString(gpt.CacheAccountingFields, "cache_miss_tokens") {
		t.Fatalf("gpt capabilities = %#v", gpt)
	}
}

func TestValidateCapabilityPolicyRejectsUnsupportedSettings(t *testing.T) {
	if err := ValidateCapabilityPolicy(CapabilityPolicyRequest{
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		Thinking:        "enabled",
		ReasoningEffort: "warp",
	}); err == nil || !strings.Contains(err.Error(), "unsupported reasoning_effort") {
		t.Fatalf("reasoning error = %v", err)
	}
	if err := ValidateCapabilityPolicy(CapabilityPolicyRequest{
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		MaxOutputTokens: 9000,
	}); err == nil || !strings.Contains(err.Error(), "max_output_tokens=9000") {
		t.Fatalf("max output error = %v", err)
	}
	if err := ValidateCapabilityPolicy(CapabilityPolicyRequest{
		Provider: "deepseek",
		Model:    "unknown-model",
	}); err == nil || !strings.Contains(err.Error(), "capabilities are unknown") {
		t.Fatalf("unknown model error = %v", err)
	}
	if err := ValidateCapabilityPolicy(CapabilityPolicyRequest{
		Provider:        "openai-codex",
		Model:           "o3-mini",
		ReasoningEffort: "minimal",
	}); err != nil {
		t.Fatalf("codex-family model should use inferred capabilities: %v", err)
	}
	if err := ValidateCapabilityPolicy(CapabilityPolicyRequest{
		Provider:           "my-openai-compatible",
		Model:              "unknown-model",
		AllowUnknownModels: true,
	}); err != nil {
		t.Fatalf("custom unknown model should be allowed: %v", err)
	}
}

func TestProviderCatalogIncludesCoreAndCustomProviders(t *testing.T) {
	deepseek := Provider("deepseek")
	if !deepseek.OpenAICompatible || deepseek.Auth != "api-key" || len(deepseek.Models) == 0 {
		t.Fatalf("deepseek provider = %#v", deepseek)
	}
	codex := Provider("codex")
	if codex.ID != ProviderOpenAICodex || !codex.Subscription || codex.Auth != "codex-oauth" {
		t.Fatalf("codex provider = %#v", codex)
	}
	custom := Provider("my-openai-compatible")
	if !custom.Custom || !custom.OpenAICompatible || custom.Transport != "openai-compatible-chat-completions" {
		t.Fatalf("custom provider = %#v", custom)
	}
	if len(Providers()) < 3 {
		t.Fatalf("providers = %#v", Providers())
	}
}

func TestDefaultSummaryModelUsesCatalog(t *testing.T) {
	if got := DefaultSummaryModel("gpt-5.5", "deepseek"); got != "gpt-5.4-mini" {
		t.Fatalf("codex summary model = %q", got)
	}
	if got := DefaultSummaryModel("deepseek-v4-pro", "openai-codex"); got != "deepseek-v4-flash" {
		t.Fatalf("deepseek summary model = %q", got)
	}
}
