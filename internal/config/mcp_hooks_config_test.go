package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
default_tool_risk = "network-read"
tool_risks = { echo = "local-read", env = "secret-access" }
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
	if server.DefaultToolRisk != "network_read" || server.ToolRisks["echo"] != "local_read" || server.ToolRisks["env"] != "secret_access" {
		t.Fatalf("tool risks = %#v", server)
	}
}

func TestLoadMCPServersRejectsUnknownToolRisk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad-risk.toml")
	if err := os.WriteFile(path, []byte(`
[mcp_servers.fake]
command = "python3"
tool_risks = { echo = "whatever_the_server_said" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMCPServers([]string{path}); err == nil || !strings.Contains(err.Error(), "unsupported tool_risks.echo value") {
		t.Fatalf("expected unsupported tool risk error, got %v", err)
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

[hooks.user_prompt_submit.guard]
command = "sh"
args = ["-c", "printf '{}'"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks, err := LoadHooks([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 2 {
		t.Fatalf("hooks = %#v", hooks)
	}
	hook, ok := hookByName(hooks, "capture")
	if !ok {
		t.Fatalf("missing capture hook: %#v", hooks)
	}
	if hook.Event != "before_tool" || hook.Name != "capture" || hook.Command != "sh" ||
		strings.Join(hook.Args, " ") != "-c cat" || hook.Env["STATIC_VALUE"] != "literal" ||
		strings.Join(hook.EnvVars, ",") != "PATH" || hook.Timeout != 1500*time.Millisecond ||
		hook.MaxOutputBytes != 123 || !hook.Fatal || !hook.Enabled {
		t.Fatalf("hook = %#v", hook)
	}
	promptHook, ok := hookByName(hooks, "guard")
	if !ok {
		t.Fatalf("missing prompt hook: %#v", hooks)
	}
	if promptHook.Event != "user_prompt_submit" || promptHook.Name != "guard" || promptHook.Command != "sh" ||
		strings.Join(promptHook.Args, " ") != "-c printf '{}'" || !promptHook.Enabled {
		t.Fatalf("prompt hook = %#v", promptHook)
	}
}

func hookByName(hooks []Hook, name string) (Hook, bool) {
	for _, hook := range hooks {
		if hook.Name == name {
			return hook, true
		}
	}
	return Hook{}, false
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
	if count := strings.Count(text, "[mcp_servers."); count != 4 {
		t.Fatalf("default MCP config server count = %d, want 4: %s", count, text)
	}
	if !strings.Contains(text, "web_extract") {
		t.Fatalf("default MCP config should document native web_extract: %s", text)
	}
	if strings.Contains(text, "codex_only") {
		t.Fatalf("default MCP config should not copy Codex MCP servers: %s", text)
	}
}
