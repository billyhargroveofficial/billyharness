package config

import (
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
	if cfg.Profile != "billy" {
		t.Fatalf("Profile = %q, want billy", cfg.Profile)
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
