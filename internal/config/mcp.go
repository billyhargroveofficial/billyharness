package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type MCPServer struct {
	Name                     string
	Command                  string
	Args                     []string
	Env                      map[string]string
	EnvVars                  []string
	CWD                      string
	URL                      string
	UnsupportedReason        string
	BearerTokenEnvVar        string
	HTTPHeaders              map[string]string
	EnvHTTPHeaders           map[string]string
	StartupTimeout           time.Duration
	ToolTimeout              time.Duration
	Enabled                  bool
	Required                 bool
	EnabledTools             []string
	DisabledTools            []string
	DefaultToolsApprovalMode string
}

type codexMCPConfig struct {
	MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
}

type codexMCPServer struct {
	Command                  string            `toml:"command"`
	Args                     []string          `toml:"args"`
	Env                      map[string]string `toml:"env"`
	EnvVars                  mcpEnvVars        `toml:"env_vars"`
	CWD                      string            `toml:"cwd"`
	URL                      string            `toml:"url"`
	BearerTokenEnvVar        string            `toml:"bearer_token_env_var"`
	HTTPHeaders              map[string]string `toml:"http_headers"`
	EnvHTTPHeaders           map[string]string `toml:"env_http_headers"`
	StartupTimeoutSec        float64           `toml:"startup_timeout_sec"`
	ToolTimeoutSec           float64           `toml:"tool_timeout_sec"`
	Enabled                  *bool             `toml:"enabled"`
	Required                 bool              `toml:"required"`
	EnabledTools             []string          `toml:"enabled_tools"`
	DisabledTools            []string          `toml:"disabled_tools"`
	DefaultToolsApprovalMode string            `toml:"default_tools_approval_mode"`
}

type mcpEnvVars []string

func (c *Config) LoadDefaultMCPServers() error {
	if !c.MCPEnabled {
		c.MCPServers = nil
		return nil
	}
	files := c.MCPConfigFiles
	if len(files) == 0 {
		files = DefaultMCPConfigFiles()
	}
	servers, err := loadMCPServers(files, c.MCPAllowedServers)
	if err != nil {
		return err
	}
	c.MCPConfigFiles = files
	c.MCPServers = filterMCPServers(servers, c.MCPAllowedServers)
	return nil
}

func filterMCPServers(servers []MCPServer, allowed []string) []MCPServer {
	if len(allowed) == 0 {
		return servers
	}
	byName := map[string]MCPServer{}
	var allowedNames []string
	for _, name := range allowed {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, exists := byName[name]; !exists {
			allowedNames = append(allowedNames, name)
		}
		byName[name] = MCPServer{}
	}
	if len(allowedNames) == 0 {
		return servers
	}
	for _, server := range servers {
		name := strings.ToLower(strings.TrimSpace(server.Name))
		if _, ok := byName[name]; ok {
			byName[name] = server
		}
	}
	out := make([]MCPServer, 0, len(allowedNames))
	for _, name := range allowedNames {
		if server := byName[name]; server.Name != "" {
			out = append(out, server)
		}
	}
	return out
}

func DefaultMCPConfigFiles() []string {
	path := DefaultMCPConfigFile()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{path}
}

func DefaultMCPConfigFile() string {
	return filepath.Join(BillyHomeDir(), "mcp.config.toml")
}

func EnsureDefaultMCPConfigFile() (string, error) {
	path := DefaultMCPConfigFile()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(defaultMCPConfig), 0o600)
}

const defaultMCPConfig = `# billyharness MCP config.
# Native web_search, web_fetch, and web_crawl are built in; keep them out of MCP.
# Secrets can live in $BILLYHARNESS_HOME/.env and be referenced via env_vars.

[mcp_servers.telegram]
command = "telegram-mcp-hermes"
startup_timeout_sec = 30.0
tool_timeout_sec = 300.0

[mcp_servers.telegram-parilka]
command = "/root/telegram-parilka-mcp/bin/telegram-parilka-mcp"
startup_timeout_sec = 30.0
tool_timeout_sec = 300.0

[mcp_servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env_vars = ["GITHUB_PERSONAL_ACCESS_TOKEN"]
startup_timeout_sec = 30.0
tool_timeout_sec = 300.0

[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]
env_vars = ["CONTEXT7_API_KEY"]
startup_timeout_sec = 30.0
tool_timeout_sec = 300.0
`

func LoadMCPServers(files []string) ([]MCPServer, error) {
	return loadMCPServers(files, nil)
}

func loadMCPServers(files []string, allowed []string) ([]MCPServer, error) {
	merged := map[string]MCPServer{}
	order := []string{}
	allowedSet := mcpAllowedSet(allowed)
	for _, file := range files {
		if strings.TrimSpace(file) == "" {
			continue
		}
		var root codexMCPConfig
		if _, err := toml.DecodeFile(file, &root); err != nil {
			return nil, err
		}
		for name, raw := range root.MCPServers {
			if len(allowedSet) > 0 && !allowedSet[strings.ToLower(strings.TrimSpace(name))] {
				continue
			}
			if raw.Enabled != nil && !*raw.Enabled {
				delete(merged, name)
				order = removeString(order, name)
				continue
			}
			if err := raw.validate(name); err != nil {
				return nil, err
			}
			server := raw.toConfig(name)
			if _, exists := merged[name]; !exists {
				order = append(order, name)
			}
			merged[name] = server
		}
	}
	out := make([]MCPServer, 0, len(merged))
	emitted := map[string]bool{}
	for _, name := range order {
		if server, ok := merged[name]; ok && !emitted[name] {
			out = append(out, server)
			emitted[name] = true
		}
	}
	return out, nil
}

func mcpAllowedSet(allowed []string) map[string]bool {
	if len(allowed) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, name := range allowed {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			out[name] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (v *mcpEnvVars) UnmarshalTOML(value any) error {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			if typed != "" {
				out = append(out, typed)
			}
		case map[string]any:
			if name, ok := typed["name"].(string); ok && name != "" {
				out = append(out, name)
			}
		case map[any]any:
			if name, ok := typed["name"].(string); ok && name != "" {
				out = append(out, name)
			}
		}
	}
	*v = out
	return nil
}

func (s codexMCPServer) validate(name string) error {
	if s.Command != "" {
		if s.URL != "" {
			return fmt.Errorf("mcp_servers.%s: command and url are mutually exclusive", name)
		}
		if s.BearerTokenEnvVar != "" || len(s.HTTPHeaders) > 0 || len(s.EnvHTTPHeaders) > 0 {
			return fmt.Errorf("mcp_servers.%s: HTTP fields are not supported for stdio transport", name)
		}
		return nil
	}
	if s.URL != "" {
		if len(s.Args) > 0 || len(s.Env) > 0 || len(s.EnvVars) > 0 || s.CWD != "" {
			return fmt.Errorf("mcp_servers.%s: stdio fields are not supported for streamable HTTP transport", name)
		}
		return nil
	}
	return fmt.Errorf("mcp_servers.%s: command or url required", name)
}

func (s codexMCPServer) toConfig(name string) MCPServer {
	startup := time.Duration(s.StartupTimeoutSec * float64(time.Second))
	if startup <= 0 {
		startup = 30 * time.Second
	}
	tool := time.Duration(s.ToolTimeoutSec * float64(time.Second))
	if tool <= 0 {
		tool = 300 * time.Second
	}
	enabled := true
	if s.Enabled != nil {
		enabled = *s.Enabled
	}
	return MCPServer{
		Name:                     name,
		Command:                  s.Command,
		Args:                     append([]string(nil), s.Args...),
		Env:                      cloneStringMap(s.Env),
		EnvVars:                  append([]string(nil), s.EnvVars...),
		CWD:                      s.CWD,
		URL:                      s.URL,
		UnsupportedReason:        s.unsupportedReason(),
		BearerTokenEnvVar:        s.BearerTokenEnvVar,
		HTTPHeaders:              cloneStringMap(s.HTTPHeaders),
		EnvHTTPHeaders:           cloneStringMap(s.EnvHTTPHeaders),
		StartupTimeout:           startup,
		ToolTimeout:              tool,
		Enabled:                  enabled,
		Required:                 s.Required,
		EnabledTools:             append([]string(nil), s.EnabledTools...),
		DisabledTools:            append([]string(nil), s.DisabledTools...),
		DefaultToolsApprovalMode: s.DefaultToolsApprovalMode,
	}
}

func (s codexMCPServer) unsupportedReason() string {
	if strings.TrimSpace(s.URL) == "" {
		return ""
	}
	return "streamable HTTP MCP is not implemented in billyharness yet; use stdio MCP or remove the url server"
}
