package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

func TestAPIKeyFallsBackToDotenv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TEST_API_KEY=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_API_KEY", "")
	t.Setenv("FAST_AGENT_ENV_FILE", "")

	cfg := Default()
	cfg.APIKeyEnv = "TEST_API_KEY"
	if got := cfg.APIKey(); got != "from-dotenv" {
		t.Fatalf("APIKey() = %q", got)
	}
}

func TestDefaultRuntimeLimits(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	cfg := Default()
	if cfg.MaxToolRounds != 0 {
		t.Fatalf("MaxToolRounds = %d, want unlimited default 0", cfg.MaxToolRounds)
	}
	if cfg.ContextCompactTokens != 600_000 {
		t.Fatalf("ContextCompactTokens = %d, want 600000", cfg.ContextCompactTokens)
	}
	if cfg.ContextWindowTokens != 1_000_000 {
		t.Fatalf("ContextWindowTokens = %d, want 1000000", cfg.ContextWindowTokens)
	}
	if cfg.ContextCompactKeep != 32 {
		t.Fatalf("ContextCompactKeep = %d, want 32", cfg.ContextCompactKeep)
	}
	if cfg.ProjectContextMaxBytes != 4*1024 {
		t.Fatalf("ProjectContextMaxBytes = %d, want 4096", cfg.ProjectContextMaxBytes)
	}
	if !cfg.MemoryEnabled ||
		cfg.MemorySummaryMaxBytes != 2*1024 ||
		cfg.MemoryIndexMaxBytes != 25*1024 ||
		cfg.MemoryTopicMaxBytes != 64*1024 {
		t.Fatalf("memory defaults = enabled:%v summary:%d index:%d topic:%d", cfg.MemoryEnabled, cfg.MemorySummaryMaxBytes, cfg.MemoryIndexMaxBytes, cfg.MemoryTopicMaxBytes)
	}
	if cfg.MemoryAutoExtractEnabled || cfg.DiagnosticSnapshot().RuntimeTool.MemoryAutoExtractEnabled {
		t.Fatalf("memory auto extraction must default disabled: cfg=%v diagnostics=%v", cfg.MemoryAutoExtractEnabled, cfg.DiagnosticSnapshot().RuntimeTool.MemoryAutoExtractEnabled)
	}
	if !cfg.AutoApproveDangerous {
		t.Fatalf("AutoApproveDangerous should be enabled by default")
	}
	if got := strings.Join(cfg.MCPAllowedServers, ","); got != "telegram,telegram-parilka,github,context7" {
		t.Fatalf("MCPAllowedServers = %q", got)
	}
	if cfg.CodexAuthFile != filepath.Join(os.Getenv("BILLYHARNESS_HOME"), "auth", "codex.json") {
		t.Fatalf("CodexAuthFile = %q", cfg.CodexAuthFile)
	}
	if cfg.CredentialFile != filepath.Join(os.Getenv("BILLYHARNESS_HOME"), "auth", "credentials.json") {
		t.Fatalf("CredentialFile = %q", cfg.CredentialFile)
	}
	if cfg.Profile != "billy" {
		t.Fatalf("Profile = %q, want billy", cfg.Profile)
	}
	if !cfg.DisableSpark {
		t.Fatalf("DisableSpark should be enabled by default billy profile")
	}
	if cfg.WebSummaryMode != "extractive" || cfg.WebSummaryModel != "deepseek-v4-flash" || cfg.WebSummaryProvider != "deepseek" {
		t.Fatalf("web summary defaults = mode:%q provider:%q model:%q", cfg.WebSummaryMode, cfg.WebSummaryProvider, cfg.WebSummaryModel)
	}
	if cfg.WebSummaryMaxInputTokens != 12_000 || cfg.WebSummaryMaxOutputTokens != 700 || cfg.WebSummaryTimeout != time.Minute {
		t.Fatalf("web summary budgets = in:%d out:%d timeout:%s", cfg.WebSummaryMaxInputTokens, cfg.WebSummaryMaxOutputTokens, cfg.WebSummaryTimeout)
	}
	if !cfg.WebCacheEnabled || cfg.WebCacheTTL != 10*time.Minute || cfg.WebCacheMaxBytes != 128*1024*1024 {
		t.Fatalf("web cache defaults = enabled:%v ttl:%s max:%d", cfg.WebCacheEnabled, cfg.WebCacheTTL, cfg.WebCacheMaxBytes)
	}
	if cfg.WebSearchBackend != "native" || cfg.WebExtractBackend != "native" ||
		cfg.WebTavilyAPIKeyEnv != "TAVILY_API_KEY" || cfg.WebExaAPIKeyEnv != "EXA_API_KEY" ||
		len(cfg.WebHermesEnvFiles) != 0 {
		t.Fatalf("web backend defaults = search:%q extract:%q tavily_env:%q exa_env:%q hermes:%v",
			cfg.WebSearchBackend, cfg.WebExtractBackend, cfg.WebTavilyAPIKeyEnv, cfg.WebExaAPIKeyEnv, cfg.WebHermesEnvFiles)
	}
}

func TestMCPAllowedServersEnvOverridesDefault(t *testing.T) {
	t.Setenv("FAST_AGENT_MCP_ALLOWED_SERVERS", "github, custom, github")
	cfg := Default()
	if got := strings.Join(cfg.MCPAllowedServers, ","); got != "github,custom" {
		t.Fatalf("MCPAllowedServers = %q", got)
	}
}

func TestContextCompactionEnvOverridesPolicyControls(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_TOKENS", "12345")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_KEEP", "17")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_MAX_CHARS", "54321")
	cfg := Default()
	if cfg.ContextCompactTokens != 12345 ||
		cfg.ContextCompactKeep != 17 ||
		cfg.ContextCompactMaxChars != 54321 {
		t.Fatalf("context compaction policy = tokens:%d keep:%d max_chars:%d", cfg.ContextCompactTokens, cfg.ContextCompactKeep, cfg.ContextCompactMaxChars)
	}
	if !cfg.ContextCompactExplicitOverride() || cfg.ContextCompactSourceLabel() != "override" {
		t.Fatalf("context compact override provenance not preserved")
	}
}

func TestContextCompactionOverrideAboveWindowIsClampedToDerived(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_TOKENS", "600000")
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Config.ContextCompactTokens; got != 230_400 {
		t.Fatalf("ContextCompactTokens = %d, want model-derived 230400", got)
	}
	if resolved.Config.ContextCompactExplicitOverride() || resolved.Config.ContextCompactSourceLabel() != "derived" {
		t.Fatalf("clamped compact threshold should be derived, got source %q", resolved.Config.ContextCompactSourceLabel())
	}
}

func TestContextCompactionExplicitCodexSixtyPercentOverrideIsPreserved(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_TOKENS", "153600")
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Config.ContextCompactTokens; got != 153_600 {
		t.Fatalf("ContextCompactTokens = %d, want explicit override 153600", got)
	}
	if !resolved.Config.ContextCompactExplicitOverride() || resolved.Config.ContextCompactSourceLabel() != "override" {
		t.Fatalf("explicit compact threshold should stay override, got source %q", resolved.Config.ContextCompactSourceLabel())
	}
}

func TestResolveStrictRejectsInvalidTypedValue(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MAX_TOOL_ROUNDS", "not-an-int")

	resolved, err := ResolveStrict()
	if err == nil {
		t.Fatal("ResolveStrict accepted invalid typed value")
	}
	text := err.Error()
	for _, want := range []string{"invalid runtime config", "max_tool_rounds", "FAST_AGENT_MAX_TOOL_ROUNDS"} {
		if !strings.Contains(text, want) {
			t.Fatalf("strict error missing %q: %v", want, err)
		}
	}
	if value, ok := resolved.Value("max_tool_rounds"); !ok || value.Error == "" {
		t.Fatalf("resolved invalid value missing error detail: %#v ok=%v", value, ok)
	}
}

func TestResolveStrictRejectsMalformedHomeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("max_tool_rounds = [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveStrict()
	if err == nil {
		t.Fatal("ResolveStrict accepted malformed config TOML")
	}
	text := err.Error()
	if !strings.Contains(text, "resolve runtime config") || !strings.Contains(text, "config.toml") {
		t.Fatalf("strict TOML error = %v", err)
	}
}

func TestProjectContextMaxBytesEnvOverride(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_PROJECT_CONTEXT_MAX_BYTES", "1234")
	cfg := Default()
	if cfg.ProjectContextMaxBytes != 1234 || cfg.InstructionSettings().ProjectContextMaxBytes != 1234 {
		t.Fatalf("project context cap = cfg:%d projection:%d", cfg.ProjectContextMaxBytes, cfg.InstructionSettings().ProjectContextMaxBytes)
	}
}

func TestMemoryConfigEnvOverridesInstructionSettings(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("BILLYHARNESS_MEMORY_ENABLED", "false")
	t.Setenv("BILLYHARNESS_MEMORY_SUMMARY_MAX_BYTES", "123")
	t.Setenv("BILLYHARNESS_MEMORY_INDEX_MAX_BYTES", "456")
	t.Setenv("BILLYHARNESS_MEMORY_TOPIC_MAX_BYTES", "789")
	cfg := Default()
	instructions := cfg.InstructionSettings()
	if cfg.MemoryEnabled || instructions.MemoryEnabled {
		t.Fatalf("memory enabled = cfg:%v projection:%v", cfg.MemoryEnabled, instructions.MemoryEnabled)
	}
	if cfg.MemorySummaryMaxBytes != 123 || instructions.MemorySummaryMaxBytes != 123 ||
		cfg.MemoryIndexMaxBytes != 456 || instructions.MemoryIndexMaxBytes != 456 ||
		cfg.MemoryTopicMaxBytes != 789 || instructions.MemoryTopicMaxBytes != 789 {
		t.Fatalf("memory caps = cfg:%#v instructions:%#v", cfg, instructions)
	}
}

func TestMemoryAutoExtractionRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("BILLYHARNESS_MEMORY_AUTO_EXTRACT_ENABLED", "")
	cfg := Default()
	if cfg.MemoryAutoExtractEnabled {
		t.Fatalf("memory auto extraction default = true")
	}
	t.Setenv("BILLYHARNESS_MEMORY_AUTO_EXTRACT_ENABLED", "true")
	cfg = Default()
	if !cfg.MemoryAutoExtractEnabled || !cfg.DiagnosticSnapshot().RuntimeTool.MemoryAutoExtractEnabled {
		t.Fatalf("memory auto extraction override not visible: cfg=%v diagnostics=%v", cfg.MemoryAutoExtractEnabled, cfg.DiagnosticSnapshot().RuntimeTool.MemoryAutoExtractEnabled)
	}
}

func TestDefaultReadsBillySettingsModelWhenEnvIsUnset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_MODEL", "")
	t.Setenv("FAST_AGENT_CONTEXT_WINDOW_TOKENS", "")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{
  "last_selected_model": "gpt-5.5",
  "last_reasoning_kind": "enabled",
  "last_reasoning_effort": "xhigh",
  "context_window_tokens": 777000
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if cfg.Model != "gpt-5.5" || cfg.Provider != "openai-codex" || cfg.ReasoningEffort != "xhigh" {
		t.Fatalf("cfg = %#v", cfg)
	}
	if cfg.ContextWindowTokens != 777000 {
		t.Fatalf("ContextWindowTokens = %d, want 777000", cfg.ContextWindowTokens)
	}
}

func TestDefaultReadsBillySettingsProfileWhenEnvIsUnset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("BILLYHARNESS_PROFILE", "")
	t.Setenv("FAST_AGENT_PROFILE", "")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"last_profile":"teacher.profile"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if cfg.Profile != "teacher.profile" {
		t.Fatalf("Profile = %q", cfg.Profile)
	}
}

func TestProfileEnvOverridesBillySettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("BILLYHARNESS_PROFILE", "Env/Profile")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"last_profile":"settings-profile"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if cfg.Profile != "envprofile" {
		t.Fatalf("Profile = %q", cfg.Profile)
	}
}

func TestEnsureDefaultProfileFileCreatesBillySoul(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	path, err := EnsureDefaultProfileFile("billy")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "profiles", "billy", "SOUL.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Формулы пиши в LaTeX") {
		t.Fatalf("profile body = %s", body)
	}
	if _, err := os.Stat(filepath.Join(root, "profiles", "billy", "profile.toml")); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDoesNotCreateDefaultProfileFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Config.DisableSpark || resolved.Config.Profile != "billy" {
		t.Fatalf("default billy profile settings = %#v", resolved.Config)
	}
	if value, ok := resolved.Value("profile_tool_policy"); !ok || value.Value != "solo-full-access" {
		t.Fatalf("missing built-in profile metadata: %#v", resolved.Values)
	}
	for _, path := range []string{
		filepath.Join(root, "profiles", "billy", "profile.toml"),
		filepath.Join(root, "profiles", "billy", "SOUL.md"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Resolve should not create %s (err=%v)", path, err)
		}
	}
}

func TestProfileMetadataAppliesRuntimeDefaults(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	profileDir := filepath.Join(root, "profiles", "teacher")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "profile.toml"), []byte(`
name = "teacher"
provider = "deepseek"
model = "deepseek-v4-pro"
thinking = "enabled"
reasoning_effort = "max"
context_window_tokens = 700000
web_summary_mode = "model"
mcp_allowlist = ["context7"]
tool_policy = "solo-full-access"
instruction_fragments = ["SOUL.md"]
cost_budget_hints = ["prefer flash summaries"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Profile: "teacher"}
	if err := cfg.ApplyProfileMetadata(); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "deepseek-v4-pro" || cfg.Provider != "deepseek" || cfg.ReasoningEffort != "max" ||
		cfg.ContextWindowTokens != 700000 || cfg.WebSummaryMode != "model" || cfg.WebSummaryModel != "deepseek-v4-flash" ||
		len(cfg.MCPAllowedServers) != 1 || cfg.MCPAllowedServers[0] != "context7" {
		t.Fatalf("profile metadata config = %#v", cfg)
	}
	if !cfg.ContextWindowExplicitOverride() {
		t.Fatalf("profile context window should be explicit override")
	}

	resolved, err := Resolve(ResolveOverride{Key: "profile", Value: "teacher", Source: SourceCLI, SourceKey: "-profile"})
	if err != nil {
		t.Fatal(err)
	}
	assertResolvedSource(t, resolved, "model", SourceProfile, "model")
	assertResolvedSource(t, resolved, "web_summary_mode", SourceProfile, "web_summary_mode")
	contextValue, ok := resolved.Value("context_window_tokens")
	if !ok || contextValue.Source != SourceProfile || !strings.Contains(contextValue.Warning, "explicit override") {
		t.Fatalf("profile context window value = %#v", contextValue)
	}
	if value, ok := resolved.Value("profile_tool_policy"); !ok || value.Value != "solo-full-access" {
		t.Fatalf("missing profile tool policy: %#v", resolved.Values)
	}
}

func TestProfileContextWindowOverridesAreExplicitUnlessLegacyCodex(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		context      int64
		wantWindow   int64
		wantOverride bool
		wantSource   string
		wantWarning  string
	}{
		{
			name:       "codex no context uses model metadata",
			model:      "gpt-5.5",
			wantWindow: 256_000,
			wantSource: SourceDerived,
		},
		{
			name:         "codex profile override is loud",
			model:        "gpt-5.5",
			context:      700_000,
			wantWindow:   700_000,
			wantOverride: true,
			wantSource:   SourceProfile,
			wantWarning:  "explicit override; model gpt-5.5 default is 256000",
		},
		{
			name:       "codex legacy million still derives",
			model:      "gpt-5.5",
			context:    1_000_000,
			wantWindow: 256_000,
			wantSource: SourceDerived,
		},
		{
			name:       "deepseek no context uses model metadata",
			model:      "deepseek-v4-pro",
			wantWindow: 1_000_000,
			wantSource: SourceBuiltIn,
		},
		{
			name:         "deepseek profile override is loud",
			model:        "deepseek-v4-pro",
			context:      700_000,
			wantWindow:   700_000,
			wantOverride: true,
			wantSource:   SourceProfile,
			wantWarning:  "explicit override; model deepseek-v4-pro default is 1000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("BILLYHARNESS_HOME", home)
			t.Setenv("FAST_AGENT_ENV_FILE", "")
			profileDir := filepath.Join(home, "profiles", "switcher")
			if err := os.MkdirAll(profileDir, 0o700); err != nil {
				t.Fatal(err)
			}
			var body strings.Builder
			body.WriteString("name = \"switcher\"\n")
			body.WriteString("model = \"" + tt.model + "\"\n")
			if tt.context > 0 {
				body.WriteString(fmt.Sprintf("context_window_tokens = %d\n", tt.context))
			}
			if err := os.WriteFile(filepath.Join(profileDir, "profile.toml"), []byte(body.String()), 0o600); err != nil {
				t.Fatal(err)
			}

			resolved, err := Resolve(ResolveOverride{Key: "profile", Value: "switcher", Source: SourceCLI, SourceKey: "-profile"})
			if err != nil {
				t.Fatal(err)
			}
			if got := resolved.Config.ContextWindowTokens; got != tt.wantWindow {
				t.Fatalf("ContextWindowTokens = %d, want %d", got, tt.wantWindow)
			}
			if got := resolved.Config.ContextWindowExplicitOverride(); got != tt.wantOverride {
				t.Fatalf("ContextWindowExplicitOverride = %v, want %v", got, tt.wantOverride)
			}
			value, ok := resolved.Value("context_window_tokens")
			if !ok || value.Source != tt.wantSource {
				t.Fatalf("context_window_tokens value = %#v, want source %s", value, tt.wantSource)
			}
			if tt.wantWarning != "" && !strings.Contains(value.Warning, tt.wantWarning) {
				t.Fatalf("context warning = %q, want %q", value.Warning, tt.wantWarning)
			}
			if tt.wantWarning == "" && strings.Contains(value.Warning, "explicit override") {
				t.Fatalf("unexpected explicit override warning: %#v", value)
			}
		})
	}
}

func TestFastAgentModelEnvOverridesBillySettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_MODEL", "deepseek-v4-pro")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"last_selected_model":"gpt-5.5"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if cfg.Model != "deepseek-v4-pro" || cfg.Provider != "deepseek" {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestAutoApproveDangerousEnvCanDisableDefault(t *testing.T) {
	t.Setenv("FAST_AGENT_AUTO_APPROVE_DANGEROUS", "false")
	cfg := Default()
	if cfg.AutoApproveDangerous {
		t.Fatalf("AutoApproveDangerous should be disabled by env")
	}
}

func TestApplyModelProviderDefaultsSelectsProviderFromModel(t *testing.T) {
	cfg := Config{Provider: "deepseek", Model: "gpt-5.5"}
	cfg.ApplyModelProviderDefaults()
	if cfg.Provider != "openai-codex" {
		t.Fatalf("Provider = %q", cfg.Provider)
	}

	cfg = Config{Provider: "openai-codex", Model: "deepseek-v4-flash"}
	cfg.ApplyModelProviderDefaults()
	if cfg.Provider != "deepseek" {
		t.Fatalf("Provider = %q", cfg.Provider)
	}
}

func TestApplyModelProviderDefaultsUsesCodexModelContextWindows(t *testing.T) {
	cfg := Config{Provider: "deepseek", Model: "gpt-5.5", ContextWindowTokens: 1_000_000, ContextCompactTokens: 600_000}
	cfg.ApplyModelProviderDefaults()
	if cfg.Provider != "openai-codex" || cfg.ContextWindowTokens != 256_000 || cfg.ContextCompactTokens != 230_400 {
		t.Fatalf("gpt-5.5 defaults = provider:%q context:%d compact:%d", cfg.Provider, cfg.ContextWindowTokens, cfg.ContextCompactTokens)
	}

	cfg = Config{Provider: "deepseek", Model: "gpt-5.4-mini", ContextWindowTokens: 1_000_000, ContextCompactTokens: 600_000}
	cfg.ApplyModelProviderDefaults()
	if cfg.Provider != "openai-codex" || cfg.ContextWindowTokens != 256_000 || cfg.ContextCompactTokens != 230_400 {
		t.Fatalf("gpt-5.4-mini defaults = provider:%q context:%d compact:%d", cfg.Provider, cfg.ContextWindowTokens, cfg.ContextCompactTokens)
	}

	cfg = Config{Provider: "deepseek", Model: "gpt-5.4-mini", ContextWindowTokens: 777_000, ContextCompactTokens: 600_000}
	cfg.ApplyModelProviderDefaults()
	if cfg.Provider != "openai-codex" || cfg.ContextWindowTokens != 777_000 {
		t.Fatalf("custom context should be preserved: provider:%q context:%d", cfg.Provider, cfg.ContextWindowTokens)
	}
}

func TestResolveModelContextWindowsFollowModelInfo(t *testing.T) {
	tests := map[string]int64{
		"gpt-5.5":             256_000,
		"gpt-5.4-mini":        256_000,
		"gpt-5.3-codex-spark": 128_000,
		"deepseek-v4-flash":   1_000_000,
		"deepseek-v4-pro":     1_000_000,
		"qwen3.8-max-preview": 983_616,
	}
	for model, want := range tests {
		t.Run(model, func(t *testing.T) {
			t.Setenv("BILLYHARNESS_HOME", t.TempDir())
			t.Setenv("FAST_AGENT_ENV_FILE", "")
			t.Setenv("FAST_AGENT_MODEL", model)
			resolved, err := Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if got := resolved.Config.ContextWindowTokens; got != want {
				t.Fatalf("ContextWindowTokens = %d, want %d", got, want)
			}
			if got := modelinfo.Lookup(model).ContextWindowTokens; got != want {
				t.Fatalf("modelinfo context = %d, want %d", got, want)
			}
			if resolved.Config.ContextWindowSourceLabel() == "override" {
				t.Fatalf("model-derived context should not be labeled override")
			}
		})
	}
}

func TestResolvePreservesExplicitContextWindowOverride(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	t.Setenv("FAST_AGENT_CONTEXT_WINDOW_TOKENS", "1000000")

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Config.ContextWindowTokens; got != 1_000_000 {
		t.Fatalf("ContextWindowTokens = %d, want explicit override 1000000", got)
	}
	if !resolved.Config.ContextWindowExplicitOverride() || resolved.Config.ContextWindowSourceLabel() != "override" {
		t.Fatalf("context override provenance not preserved")
	}
	value, ok := resolved.Value("context_window_tokens")
	if !ok {
		t.Fatalf("missing context_window_tokens resolved value")
	}
	if value.Source != SourceEnvironment || !strings.Contains(value.Warning, "explicit override") || !strings.Contains(value.Warning, "256000") {
		t.Fatalf("context override value = %#v", value)
	}
}

func TestResolvedDiagnosticSnapshotCarriesRuntimeSources(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	t.Setenv("FAST_AGENT_CONTEXT_WINDOW_TOKENS", "1000000")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_TOKENS", "200000")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MODEL", "gpt-5.4-mini")
	t.Setenv("FAST_AGENT_WEB_SEARCH_BACKEND", "exa")
	t.Setenv("FAST_AGENT_WEB_EXTRACT_BACKEND", "tavily")

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := resolved.DiagnosticSnapshot()
	if diagnostics.ProviderAuth.ProviderSource == nil || diagnostics.ProviderAuth.ProviderSource.Source != SourceDerived {
		t.Fatalf("provider source = %#v", diagnostics.ProviderAuth.ProviderSource)
	}
	if diagnostics.ProviderAuth.ModelSource == nil ||
		diagnostics.ProviderAuth.ModelSource.Source != SourceEnvironment ||
		diagnostics.ProviderAuth.ModelSource.SourceKey != "FAST_AGENT_MODEL" {
		t.Fatalf("model source = %#v", diagnostics.ProviderAuth.ModelSource)
	}
	contextSource := diagnostics.RuntimeTool.ContextWindowTokensSource
	if contextSource == nil ||
		contextSource.Source != SourceEnvironment ||
		!strings.Contains(contextSource.Warning, "explicit override") ||
		!strings.Contains(contextSource.Warning, "256000") {
		t.Fatalf("context source = %#v", contextSource)
	}
	if diagnostics.RuntimeTool.ContextCompactTokensSource == nil ||
		diagnostics.RuntimeTool.ContextCompactTokensSource.Source != SourceEnvironment {
		t.Fatalf("compact source = %#v", diagnostics.RuntimeTool.ContextCompactTokensSource)
	}
	if diagnostics.RuntimeTool.WebSummaryModelSource == nil ||
		diagnostics.RuntimeTool.WebSummaryModelSource.SourceKey != "FAST_AGENT_WEB_SUMMARY_MODEL" {
		t.Fatalf("web summary model source = %#v", diagnostics.RuntimeTool.WebSummaryModelSource)
	}
	if diagnostics.RuntimeTool.WebSearchBackendSource == nil ||
		diagnostics.RuntimeTool.WebSearchBackendSource.SourceKey != "FAST_AGENT_WEB_SEARCH_BACKEND" ||
		diagnostics.RuntimeTool.WebExtractBackendSource == nil ||
		diagnostics.RuntimeTool.WebExtractBackendSource.SourceKey != "FAST_AGENT_WEB_EXTRACT_BACKEND" {
		t.Fatalf("web backend sources = search:%#v extract:%#v",
			diagnostics.RuntimeTool.WebSearchBackendSource,
			diagnostics.RuntimeTool.WebExtractBackendSource,
		)
	}
}

func TestResolveWarnsWhenSettingsContextWindowOverridesModelDefaultWithoutExplicitSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	body := []byte(`{"last_selected_model":"deepseek-v4-pro","context_window_tokens":777000}`)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Config.ContextWindowTokens; got != 777_000 {
		t.Fatalf("ContextWindowTokens = %d, want saved setting", got)
	}
	warning := "saved setting overrides model deepseek-v4-pro default 1000000 without explicit config source"
	if !hasWarning(resolved.Warnings, warning) {
		t.Fatalf("missing saved setting warning in %#v", resolved.Warnings)
	}
	value, ok := resolved.Value("context_window_tokens")
	if !ok || value.Source != SourceSettings || !strings.Contains(value.Warning, warning) {
		t.Fatalf("context value = %#v, warning %q", value, warning)
	}
	source := resolved.DiagnosticSnapshot().RuntimeTool.ContextWindowTokensSource
	if source == nil || !strings.Contains(source.Warning, warning) {
		t.Fatalf("diagnostic context source = %#v", source)
	}
}

func TestResolveIgnoresStaleSettingsContextWindowForCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	body := []byte(`{"last_selected_model":"gpt-5.5","context_window_tokens":1000000,"context_compact_tokens":600000}`)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Config.ContextWindowTokens; got != 256_000 {
		t.Fatalf("ContextWindowTokens = %d, want model-derived 256000", got)
	}
	if got := resolved.Config.ContextCompactTokens; got != 230_400 {
		t.Fatalf("ContextCompactTokens = %d, want model-derived 230400", got)
	}
	if resolved.Config.ContextCompactExplicitOverride() || resolved.Config.ContextCompactSourceLabel() != "derived" {
		t.Fatalf("stale settings compact threshold should be derived, got source %q", resolved.Config.ContextCompactSourceLabel())
	}
	value, ok := resolved.Value("context_window_tokens")
	if !ok || value.Source != SourceDerived {
		t.Fatalf("context_window_tokens value = %#v, want derived", value)
	}
	value, ok = resolved.Value("context_compact_tokens")
	if !ok || value.Source != SourceDerived {
		t.Fatalf("context_compact_tokens value = %#v, want derived", value)
	}
}

func TestResolveUpgradesStaleCodexSixtyPercentCompactSetting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	body := []byte(`{"last_selected_model":"gpt-5.5","context_window_tokens":256000,"context_compact_tokens":153600}`)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Config.ContextWindowTokens; got != 256_000 {
		t.Fatalf("ContextWindowTokens = %d, want model-derived 256000", got)
	}
	if got := resolved.Config.ContextCompactTokens; got != 230_400 {
		t.Fatalf("ContextCompactTokens = %d, want model-derived 230400", got)
	}
	if resolved.Config.ContextCompactExplicitOverride() || resolved.Config.ContextCompactSourceLabel() != "derived" {
		t.Fatalf("stale 60%% compact threshold should be derived, got source %q", resolved.Config.ContextCompactSourceLabel())
	}
}

func TestWebSummaryModelDefaultsFollowProviderWithoutSpark(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MODE", "model")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	cfg := Default()
	if cfg.WebSummaryMode != "model" || cfg.WebSummaryProvider != "openai-codex" || cfg.WebSummaryModel != "gpt-5.4-mini" {
		t.Fatalf("codex web summary defaults = mode:%q provider:%q model:%q", cfg.WebSummaryMode, cfg.WebSummaryProvider, cfg.WebSummaryModel)
	}
	if strings.Contains(cfg.WebSummaryModel, "spark") {
		t.Fatalf("web summary should not default to spark: %q", cfg.WebSummaryModel)
	}

	t.Setenv("FAST_AGENT_MODEL", "deepseek-v4-pro")
	cfg = Default()
	if cfg.WebSummaryMode != "model" || cfg.WebSummaryProvider != "deepseek" || cfg.WebSummaryModel != "deepseek-v4-flash" {
		t.Fatalf("deepseek web summary defaults = mode:%q provider:%q model:%q", cfg.WebSummaryMode, cfg.WebSummaryProvider, cfg.WebSummaryModel)
	}
}

func TestProfileDisableSparkRewritesSparkUnlessOverridden(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_MODEL", "spark")
	cfg := Default()
	if !cfg.DisableSpark || cfg.Model != "gpt-5.4-mini" || cfg.Provider != "openai-codex" {
		t.Fatalf("spark should be disabled by billy profile: disable=%v model=%q provider=%q", cfg.DisableSpark, cfg.Model, cfg.Provider)
	}

	t.Setenv("FAST_AGENT_MODEL", "gpt-5.3-codex-spark")
	cfg = Default()
	if !cfg.DisableSpark || cfg.Model != "gpt-5.3-codex-spark" || cfg.ContextWindowTokens != 128_000 {
		t.Fatalf("explicit spark id should keep spark metadata: disable=%v model=%q context=%d", cfg.DisableSpark, cfg.Model, cfg.ContextWindowTokens)
	}

	t.Setenv("FAST_AGENT_MODEL", "spark")
	t.Setenv("FAST_AGENT_DISABLE_SPARK", "false")
	cfg = Default()
	if cfg.DisableSpark || cfg.Model != "gpt-5.3-codex-spark" || cfg.Provider != "openai-codex" {
		t.Fatalf("explicit disable_spark=false should allow spark: disable=%v model=%q provider=%q", cfg.DisableSpark, cfg.Model, cfg.Provider)
	}

	t.Setenv("FAST_AGENT_DISABLE_SPARK", "true")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MODEL", "spark")
	cfg = Default()
	if cfg.WebSummaryModel == "gpt-5.3-codex-spark" {
		t.Fatalf("web summary model should not remain spark when disabled: %#v", cfg)
	}
}

func TestWebSummaryEnvOverridesDefaults(t *testing.T) {
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MODE", "llm")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_PROVIDER", "mock")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MODEL", "custom-mini")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MAX_INPUT_TOKENS", "333")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_MAX_OUTPUT_TOKENS", "44")
	t.Setenv("FAST_AGENT_WEB_SUMMARY_TIMEOUT_SEC", "7")
	cfg := Default()
	if cfg.WebSummaryMode != "model" || cfg.WebSummaryProvider != "mock" || cfg.WebSummaryModel != "custom-mini" {
		t.Fatalf("web summary env = mode:%q provider:%q model:%q", cfg.WebSummaryMode, cfg.WebSummaryProvider, cfg.WebSummaryModel)
	}
	if cfg.WebSummaryMaxInputTokens != 333 || cfg.WebSummaryMaxOutputTokens != 44 || cfg.WebSummaryTimeout != 7*time.Second {
		t.Fatalf("web summary env budgets = in:%d out:%d timeout:%s", cfg.WebSummaryMaxInputTokens, cfg.WebSummaryMaxOutputTokens, cfg.WebSummaryTimeout)
	}
}

func TestContextCompactModelStrategyEnvOverridesDefaults(t *testing.T) {
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_STRATEGY", "llm")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_SUMMARY_PROVIDER", "mock")
	t.Setenv("FAST_AGENT_CONTEXT_COMPACT_SUMMARY_MODEL", "custom-compact-mini")
	cfg := Default()
	if cfg.ContextCompactStrategy != "model" ||
		cfg.ContextCompactSummaryProvider != "mock" ||
		cfg.ContextCompactSummaryModel != "custom-compact-mini" {
		t.Fatalf("context compact strategy = strategy:%q provider:%q model:%q", cfg.ContextCompactStrategy, cfg.ContextCompactSummaryProvider, cfg.ContextCompactSummaryModel)
	}
}

func TestWebCacheEnvOverridesDefaults(t *testing.T) {
	t.Setenv("FAST_AGENT_WEB_CACHE_ENABLED", "false")
	t.Setenv("FAST_AGENT_WEB_CACHE_TTL_SEC", "123")
	t.Setenv("FAST_AGENT_WEB_CACHE_MAX_BYTES", "456789")
	cfg := Default()
	if cfg.WebCacheEnabled || cfg.WebCacheTTL != 123*time.Second || cfg.WebCacheMaxBytes != 456789 {
		t.Fatalf("web cache env = enabled:%v ttl:%s max:%d", cfg.WebCacheEnabled, cfg.WebCacheTTL, cfg.WebCacheMaxBytes)
	}
}

func TestResolveConfigRecordsPrecedenceAndDoesNotLeakSecrets(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	t.Setenv("DEEPSEEK_REASONING_EFFORT", "xhigh")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
model = "deepseek-v4-flash"
profile = "home-profile"
max_tool_rounds = 55
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".billyharness"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".billyharness", "config.toml"), []byte(`
profile = "project-profile"
max_tool_rounds = 77
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte(`
FAST_AGENT_WEB_SUMMARY_MODE=model
DEEPSEEK_REASONING_EFFORT=medium
DEEPSEEK_API_KEY=sk-secret-should-not-appear
`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(ResolveOverride{Key: "model", Value: "deepseek-v4-pro", Source: SourceCLI, SourceKey: "-model"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Model != "deepseek-v4-pro" || resolved.Config.Profile != "project-profile" ||
		resolved.Config.ReasoningEffort != "xhigh" || resolved.Config.MaxToolRounds != 77 ||
		resolved.Config.WebSummaryMode != "model" {
		t.Fatalf("resolved config = %#v", resolved.Config)
	}
	assertResolvedSource(t, resolved, "model", SourceCLI, "-model")
	assertResolvedSource(t, resolved, "profile", SourceProject, "profile")
	assertResolvedSource(t, resolved, "reasoning_effort", SourceEnvironment, "DEEPSEEK_REASONING_EFFORT")
	assertResolvedSource(t, resolved, "max_tool_rounds", SourceProject, "max_tool_rounds")
	assertResolvedSource(t, resolved, "web_summary_mode", SourceDotenv, "FAST_AGENT_WEB_SUMMARY_MODE")
	body, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sk-secret-should-not-appear") {
		t.Fatalf("resolved config leaked secret: %s", string(body))
	}

	cfg := Default()
	if cfg.Model != "gpt-5.5" || cfg.Profile != "project-profile" || cfg.WebSummaryMode != "model" {
		t.Fatalf("Default() should use resolved config layers, got %#v", cfg)
	}
}

func TestResolveEffectiveKeepsBaseAndOverridesIndependent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "deepseek-v4-flash")

	builderCalled := false
	base, effective, err := ResolveEffectiveFromBase(func(base Config) []ResolveOverride {
		builderCalled = true
		if base.Model != "deepseek-v4-flash" {
			t.Fatalf("builder saw base model %q", base.Model)
		}
		return []ResolveOverride{{
			Key:       "model",
			Value:     "gpt-5.5",
			Source:    SourceGateway,
			SourceKey: "model",
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !builderCalled {
		t.Fatal("override builder was not called")
	}
	if base.Config.Model != "deepseek-v4-flash" || effective.Config.Model != "gpt-5.5" {
		t.Fatalf("base/effective models = %q/%q", base.Config.Model, effective.Config.Model)
	}
	assertResolvedSource(t, base, "model", SourceEnvironment, "FAST_AGENT_MODEL")
	assertResolvedSource(t, effective, "model", SourceGateway, "model")
}

func TestProjectConfigDenylistBlocksProviderAuthOverrides(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
base_url = "https://trusted.deepseek.example"
api_key_env = "TRUSTED_DEEPSEEK_KEY"
credential_file = "/trusted/credentials.json"
codex_base_url = "https://trusted.codex.example"
codex_auth_file = "/trusted/codex.json"
codex_refresh_url = "https://trusted.refresh.example/token"
codex_auth_api_base_url = "https://trusted.auth.example/api"
codex_client_id = "trusted-client"
codex_originator = "trusted-originator"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	projectConfigDir := filepath.Join(project, ".billyharness")
	if err := os.MkdirAll(projectConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectConfigPath := filepath.Join(projectConfigDir, "config.toml")
	if err := os.WriteFile(projectConfigPath, []byte(`
base_url = "https://project.deepseek.invalid"
api_key_env = "PROJECT_DEEPSEEK_KEY"
credential_file = "/project/credentials.json"
codex_base_url = "https://project.codex.invalid"
codex_auth_file = "/project/codex.json"
codex_refresh_url = "https://project.refresh.invalid/token"
codex_auth_api_base_url = "https://project.auth.invalid/api"
codex_client_id = "project-client"
codex_originator = "project-originator"
max_tool_rounds = 77
`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cfg := resolved.Config
	if cfg.BaseURL != "https://trusted.deepseek.example" ||
		cfg.APIKeyEnv != "TRUSTED_DEEPSEEK_KEY" ||
		cfg.CredentialFile != "/trusted/credentials.json" ||
		cfg.CodexBaseURL != "https://trusted.codex.example" ||
		cfg.CodexAuthFile != "/trusted/codex.json" ||
		cfg.CodexRefreshURL != "https://trusted.refresh.example/token" ||
		cfg.CodexAuthAPIBaseURL != "https://trusted.auth.example/api" ||
		cfg.CodexClientID != "trusted-client" ||
		cfg.CodexOriginator != "trusted-originator" {
		t.Fatalf("project config changed provider/auth authority: %#v", cfg)
	}
	if cfg.MaxToolRounds != 77 {
		t.Fatalf("non-sensitive project runtime setting was not applied: %d", cfg.MaxToolRounds)
	}
	for _, key := range []string{
		"base_url",
		"api_key_env",
		"credential_file",
		"codex_base_url",
		"codex_auth_file",
		"codex_refresh_url",
		"codex_auth_api_base_url",
		"codex_client_id",
		"codex_originator",
	} {
		assertResolvedSource(t, resolved, key, SourceHomeConfig, key)
		if !hasWarning(resolved.Warnings, `project config key "`+key+`"`, projectConfigPath) {
			t.Fatalf("missing project denylist warning for %s in %#v", key, resolved.Warnings)
		}
	}
	assertResolvedSource(t, resolved, "max_tool_rounds", SourceProject, "max_tool_rounds")
}

func TestProjectConfigDenylistAllowsTrustedRuntimeOverrides(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("DEEPSEEK_BASE_URL", "https://env.deepseek.example")
	projectConfigDir := filepath.Join(project, ".billyharness")
	if err := os.MkdirAll(projectConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectConfigDir, "config.toml"), []byte(`
base_url = "https://project.deepseek.invalid"
credential_file = "/project/credentials.json"
codex_auth_file = "/project/codex.json"
max_tool_rounds = 77
`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(
		ResolveOverride{Key: "credential_file", Value: "/gateway/credentials.json", Source: SourceGateway, SourceKey: "credential_file"},
		ResolveOverride{Key: "codex_auth_file", Value: "/cli/codex.json", Source: SourceCLI, SourceKey: "-codex-auth-file"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.BaseURL != "https://env.deepseek.example" ||
		resolved.Config.CredentialFile != "/gateway/credentials.json" ||
		resolved.Config.CodexAuthFile != "/cli/codex.json" ||
		resolved.Config.MaxToolRounds != 77 {
		t.Fatalf("resolved config = %#v", resolved.Config)
	}
	binding := resolved.Config.ProviderBinding()
	if binding.Provider.BaseURL != "https://env.deepseek.example" ||
		binding.Auth.CredentialFile != "/gateway/credentials.json" ||
		binding.Auth.CodexAuthFile != "/cli/codex.json" {
		t.Fatalf("provider binding = %#v", binding)
	}
	assertResolvedSource(t, resolved, "base_url", SourceEnvironment, "DEEPSEEK_BASE_URL")
	assertResolvedSource(t, resolved, "credential_file", SourceGateway, "credential_file")
	assertResolvedSource(t, resolved, "codex_auth_file", SourceCLI, "-codex-auth-file")
}

func TestResolveProviderBindingWarnsWhenExplicitProviderConflictsWithModelRouting(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_PROVIDER", "deepseek")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Provider != "openai-codex" || resolved.Config.Model != "gpt-5.5" {
		t.Fatalf("resolved provider/model = %q/%q", resolved.Config.Provider, resolved.Config.Model)
	}
	value, ok := resolved.Value("provider")
	if !ok || value.Source != SourceDerived || !strings.Contains(value.Warning, `provider "deepseek"`) {
		t.Fatalf("provider value = %#v", value)
	}
	if !hasWarning(resolved.Warnings, `provider "deepseek" from environment ignored`, `model "gpt-5.5" routes to "openai-codex"`) {
		t.Fatalf("missing provider conflict warning: %#v", resolved.Warnings)
	}
}

func assertResolvedSource(t *testing.T, resolved ResolvedConfig, key, source, sourceKey string) {
	t.Helper()
	value, ok := resolved.Value(key)
	if !ok {
		t.Fatalf("missing resolved value %q in %#v", key, resolved.Values)
	}
	if value.Source != source || value.SourceKey != sourceKey {
		t.Fatalf("%s source = %q/%q, want %q/%q; value=%#v", key, value.Source, value.SourceKey, source, sourceKey, value)
	}
}

func hasWarning(warnings []string, parts ...string) bool {
	for _, warning := range warnings {
		matched := true
		for _, part := range parts {
			if !strings.Contains(warning, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
