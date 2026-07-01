package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if cfg.MaxToolRounds != 100 {
		t.Fatalf("MaxToolRounds = %d, want 100", cfg.MaxToolRounds)
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

	resolved, err := Resolve(ResolveOverride{Key: "profile", Value: "teacher", Source: SourceCLI, SourceKey: "-profile"})
	if err != nil {
		t.Fatal(err)
	}
	assertResolvedSource(t, resolved, "model", SourceProfile, "model")
	assertResolvedSource(t, resolved, "web_summary_mode", SourceProfile, "web_summary_mode")
	if value, ok := resolved.Value("profile_tool_policy"); !ok || value.Value != "solo-full-access" {
		t.Fatalf("missing profile tool policy: %#v", resolved.Values)
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

func TestLoadMCPServersParsesCodexStyleTOML(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte(`
[mcp_servers.fake]
command = "python3"
args = ["server.py"]
env = { MCP_TOKEN = "secret-value" }
env_vars = ["FROM_PARENT", { name = "FROM_OBJECT", source = "local" }]
cwd = "subdir"
startup_timeout_sec = 1.5
tool_timeout_sec = 2.0
required = true
enabled_tools = ["echo", "env"]
disabled_tools = ["env"]
default_tools_approval_mode = "prompt"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, err := LoadMCPServers([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %#v", servers)
	}
	server := servers[0]
	if server.Name != "fake" || server.Command != "python3" || len(server.Args) != 1 || server.Args[0] != "server.py" {
		t.Fatalf("server command = %#v", server)
	}
	if server.Env["MCP_TOKEN"] != "secret-value" {
		t.Fatalf("env = %#v", server.Env)
	}
	if strings.Join(server.EnvVars, ",") != "FROM_PARENT,FROM_OBJECT" {
		t.Fatalf("env_vars = %#v", server.EnvVars)
	}
	if server.StartupTimeout != 1500*time.Millisecond || server.ToolTimeout != 2*time.Second {
		t.Fatalf("timeouts = %s %s", server.StartupTimeout, server.ToolTimeout)
	}
	if !server.Required || strings.Join(server.EnabledTools, ",") != "echo,env" || strings.Join(server.DisabledTools, ",") != "env" {
		t.Fatalf("filters = %#v", server)
	}
}

func TestLoadMCPServersParsesRemoteAsUnsupportedDiagnostic(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "remote.toml")
	if err := os.WriteFile(path, []byte(`
[mcp_servers.remote]
url = "https://example.com/mcp"
bearer_token_env_var = "REMOTE_MCP_TOKEN"
http_headers = { X_Client = "billyharness" }
env_http_headers = { Authorization = "REMOTE_MCP_AUTH_HEADER" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadMCPServers([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %#v", servers)
	}
	server := servers[0]
	if server.Name != "remote" || server.URL != "https://example.com/mcp" || server.Command != "" {
		t.Fatalf("remote server = %#v", server)
	}
	if server.BearerTokenEnvVar != "REMOTE_MCP_TOKEN" ||
		server.HTTPHeaders["X_Client"] != "billyharness" ||
		server.EnvHTTPHeaders["Authorization"] != "REMOTE_MCP_AUTH_HEADER" {
		t.Fatalf("remote headers = %#v", server)
	}
	if !strings.Contains(server.UnsupportedReason, "streamable HTTP MCP is not implemented") {
		t.Fatalf("unsupported reason = %q", server.UnsupportedReason)
	}
}

func TestLoadHooksParsesCommandHooks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hooks.toml")
	if err := os.WriteFile(path, []byte(`
[hooks.before_tool.capture]
command = "sh"
args = ["-c", "cat"]
env = { STATIC_VALUE = "literal" }
env_vars = ["PATH"]
cwd = "."
timeout_sec = 1.5
max_output_bytes = 123
fatal = true

[hooks.after_tool.disabled]
enabled = false
command = "sh"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks, err := LoadHooks([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %#v", hooks)
	}
	hook := hooks[0]
	if hook.Event != "before_tool" || hook.Name != "capture" || hook.Command != "sh" ||
		strings.Join(hook.Args, " ") != "-c cat" || hook.Env["STATIC_VALUE"] != "literal" ||
		strings.Join(hook.EnvVars, ",") != "PATH" || hook.Timeout != 1500*time.Millisecond ||
		hook.MaxOutputBytes != 123 || !hook.Fatal || !hook.Enabled {
		t.Fatalf("hook = %#v", hook)
	}
}

func TestFilterMCPServersKeepsOnlyAllowedNames(t *testing.T) {
	servers := []MCPServer{
		{Name: "context7"},
		{Name: "github"},
		{Name: "hermes-tools"},
		{Name: "telegram"},
		{Name: "telegram-parilka"},
		{Name: "yandex-disk"},
	}
	filtered := filterMCPServers(servers, []string{"Telegram", "telegram-parilka", "github", "context7"})
	var names []string
	for _, server := range filtered {
		names = append(names, server.Name)
	}
	if got := strings.Join(names, ","); got != "telegram,telegram-parilka,github,context7" {
		t.Fatalf("filtered = %q", got)
	}
}

func TestLoadDefaultMCPServersSkipsInvalidDisallowedServers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte(`
[mcp_servers.github]
command = "npx"

[mcp_servers.bad]
command = "python3"
url = "https://example.com/mcp"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		MCPEnabled:        true,
		MCPConfigFiles:    []string{path},
		MCPAllowedServers: []string{"github"},
	}
	if err := cfg.LoadDefaultMCPServers(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "github" {
		t.Fatalf("servers = %#v", cfg.MCPServers)
	}
}

func TestLoadDefaultMCPServersDoesNotCreateDefaultConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	cfg := Config{
		MCPEnabled:        true,
		MCPAllowedServers: []string{"github"},
	}

	if err := cfg.LoadDefaultMCPServers(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPConfigFiles) != 0 || len(cfg.MCPServers) != 0 {
		t.Fatalf("default MCP load should be empty without a config file: %#v", cfg)
	}
	if _, err := os.Stat(filepath.Join(root, "mcp.config.toml")); !os.IsNotExist(err) {
		t.Fatalf("LoadDefaultMCPServers should not create default MCP config (err=%v)", err)
	}
}

func TestLoadMCPServersOverlayDisablesAndRejectsInvalidTransport(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base.toml")
	override := filepath.Join(root, "override.toml")
	if err := os.WriteFile(base, []byte(`[mcp_servers.fake]
command = "python3"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`[mcp_servers.fake]
enabled = false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadMCPServers([]string{base, override})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %#v", servers)
	}

	invalid := filepath.Join(root, "invalid.toml")
	if err := os.WriteFile(invalid, []byte(`[mcp_servers.bad]
command = "python3"
url = "https://example.com/mcp"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMCPServers([]string{invalid}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadMCPServersDisableThenReenableDoesNotDuplicate(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base.toml")
	disabled := filepath.Join(root, "disabled.toml")
	reenabled := filepath.Join(root, "reenabled.toml")
	if err := os.WriteFile(base, []byte(`[mcp_servers.fake]
command = "python3"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disabled, []byte(`[mcp_servers.fake]
enabled = false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reenabled, []byte(`[mcp_servers.fake]
command = "node"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadMCPServers([]string{base, disabled, reenabled})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "fake" || servers[0].Command != "node" {
		t.Fatalf("servers = %#v", servers)
	}
}

func TestDefaultMCPConfigFilesUsesBillyharnessHomeOnly(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	codexHome := filepath.Join(root, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(`[mcp_servers.codex_only]
command = "nope"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("CODEX_HOME", codexHome)

	if files := DefaultMCPConfigFiles(); len(files) != 0 {
		t.Fatalf("files before ensure = %#v", files)
	}
	path, err := EnsureDefaultMCPConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(billyHome, "mcp.config.toml")
	if path != want {
		t.Fatalf("path = %q want %q", path, want)
	}
	files := DefaultMCPConfigFiles()
	if len(files) != 1 || files[0] != want {
		t.Fatalf("files = %#v want %q", files, want)
	}
	bytes, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	text := string(bytes)
	for _, wantServer := range []string{"[mcp_servers.telegram]", "[mcp_servers.telegram-parilka]", "[mcp_servers.github]", "[mcp_servers.context7]"} {
		if !strings.Contains(text, wantServer) {
			t.Fatalf("default MCP config missing %s: %s", wantServer, text)
		}
	}
	if strings.Contains(text, "codex_only") {
		t.Fatalf("default MCP config should not copy Codex MCP servers: %s", text)
	}
}

func TestLookupEnvOrDotenvFallsBackToBillyharnessHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("FROM_BILLY_ENV=dotenv-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := LookupEnvOrDotenv("FROM_BILLY_ENV")
	if !ok || got != "dotenv-value" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
}

func TestLookupEnvOrDotenvPrefersBillyharnessHomeOverCWD(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	workdir := filepath.Join(root, "work")
	if err := os.MkdirAll(billyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billyHome, ".env"), []byte("PREFERRED_ENV=from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("PREFERRED_ENV=from-cwd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")

	got, ok := LookupEnvOrDotenv("PREFERRED_ENV")
	if !ok || got != "from-home" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
}

func TestLookupEnvOrDotenvExplicitFileOverridesByDefault(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	explicit := filepath.Join(root, "explicit.env")
	if err := os.MkdirAll(billyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billyHome, ".env"), []byte("EXPLICIT_ENV=from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, []byte("EXPLICIT_ENV=from-explicit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_ENV_FILE", explicit)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")

	got, ok := LookupEnvOrDotenv("EXPLICIT_ENV")
	if !ok || got != "from-explicit" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
}

func TestLookupEnvOrDotenvCanRestrictToBillyharnessHome(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	workdir := filepath.Join(root, "work")
	outsideEnv := filepath.Join(root, "outside.env")
	if err := os.MkdirAll(billyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billyHome, ".env"), []byte("RESTRICTED_ENV=from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("RESTRICTED_ENV=from-cwd\nCWD_ONLY=blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideEnv, []byte("RESTRICTED_ENV=from-explicit\nEXPLICIT_ONLY=blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_ENV_FILE", outsideEnv)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "true")

	got, ok := LookupEnvOrDotenv("RESTRICTED_ENV")
	if !ok || got != "from-home" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
	if got, ok := LookupEnvOrDotenv("CWD_ONLY"); ok || got != "" {
		t.Fatalf("CWD dotenv should be blocked in home-only mode, got %q %v", got, ok)
	}
	if got, ok := LookupEnvOrDotenv("EXPLICIT_ONLY"); ok || got != "" {
		t.Fatalf("explicit dotenv should be blocked in home-only mode, got %q %v", got, ok)
	}
}
