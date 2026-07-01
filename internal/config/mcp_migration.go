package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type MCPMigrationOptions struct {
	Files []string
}

type MCPMigrationReport struct {
	Files       []MCPMigrationFile       `json:"files"`
	Servers     []MCPMigrationServer     `json:"servers"`
	Suggestions []MCPMigrationSuggestion `json:"suggestions,omitempty"`
	Warnings    []string                 `json:"warnings,omitempty"`
}

type MCPMigrationFile struct {
	Source      string `json:"source"`
	Path        string `json:"path"`
	Exists      bool   `json:"exists"`
	Format      string `json:"format,omitempty"`
	ServerCount int    `json:"server_count,omitempty"`
	Error       string `json:"error,omitempty"`
}

type MCPMigrationServer struct {
	Source                   string            `json:"source"`
	SourcePath               string            `json:"source_path"`
	Name                     string            `json:"name"`
	Transport                string            `json:"transport"`
	Compatible               bool              `json:"compatible"`
	RuntimeSupported         bool              `json:"runtime_supported"`
	UnsupportedReason        string            `json:"unsupported_reason,omitempty"`
	Command                  string            `json:"command,omitempty"`
	Args                     []string          `json:"args,omitempty"`
	CWD                      string            `json:"cwd,omitempty"`
	URL                      string            `json:"url,omitempty"`
	EnvVars                  []string          `json:"env_vars,omitempty"`
	RedactedEnv              []string          `json:"redacted_env,omitempty"`
	BearerTokenEnvVar        string            `json:"bearer_token_env_var,omitempty"`
	HTTPHeaderNames          []string          `json:"http_header_names,omitempty"`
	EnvHTTPHeaders           map[string]string `json:"env_http_headers,omitempty"`
	RedactedHTTPHeaders      []string          `json:"redacted_http_headers,omitempty"`
	StartupTimeoutSec        float64           `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec           float64           `json:"tool_timeout_sec,omitempty"`
	Required                 bool              `json:"required,omitempty"`
	EnabledTools             []string          `json:"enabled_tools,omitempty"`
	DisabledTools            []string          `json:"disabled_tools,omitempty"`
	DefaultToolsApprovalMode string            `json:"default_tools_approval_mode,omitempty"`
	Notes                    []string          `json:"notes,omitempty"`
}

type MCPMigrationSuggestion struct {
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	SourcePath string   `json:"source_path"`
	TOML       string   `json:"toml"`
	Notes      []string `json:"notes,omitempty"`
}

type mcpMigrationCandidate struct {
	Source string
	Path   string
}

type mcpMigrationRawServer struct {
	Command                  string
	Args                     []string
	EnvKeys                  []string
	EnvVars                  []string
	CWD                      string
	URL                      string
	BearerTokenEnvVar        string
	HTTPHeaderNames          []string
	EnvHTTPHeaders           map[string]string
	StartupTimeoutSec        float64
	ToolTimeoutSec           float64
	Enabled                  *bool
	Required                 bool
	EnabledTools             []string
	DisabledTools            []string
	DefaultToolsApprovalMode string
}

func ScanMCPMigration(opts MCPMigrationOptions) (MCPMigrationReport, error) {
	candidates := defaultMCPMigrationCandidates()
	if len(cleanStringList(opts.Files)) > 0 {
		candidates = nil
		for _, file := range cleanStringList(opts.Files) {
			candidates = append(candidates, mcpMigrationCandidate{Source: "explicit", Path: file})
		}
	}

	var report MCPMigrationReport
	for _, candidate := range candidates {
		fileReport := MCPMigrationFile{Source: candidate.Source, Path: candidate.Path}
		info, err := os.Stat(candidate.Path)
		if err != nil {
			if os.IsNotExist(err) {
				report.Files = append(report.Files, fileReport)
				continue
			}
			fileReport.Error = err.Error()
			report.Files = append(report.Files, fileReport)
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", candidate.Path, err))
			continue
		}
		if info.IsDir() {
			fileReport.Exists = true
			fileReport.Error = "is a directory"
			report.Files = append(report.Files, fileReport)
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: is a directory", candidate.Path))
			continue
		}
		raw, err := os.ReadFile(candidate.Path)
		if err != nil {
			fileReport.Exists = true
			fileReport.Error = err.Error()
			report.Files = append(report.Files, fileReport)
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", candidate.Path, err))
			continue
		}
		servers, format, warnings, err := parseMCPMigrationFile(candidate, raw)
		fileReport.Exists = true
		fileReport.Format = format
		fileReport.ServerCount = len(servers)
		if err != nil {
			fileReport.Error = err.Error()
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", candidate.Path, err))
		}
		for _, warning := range warnings {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", candidate.Path, warning))
		}
		report.Files = append(report.Files, fileReport)
		report.Servers = append(report.Servers, servers...)
		for _, server := range servers {
			if server.Compatible {
				report.Suggestions = append(report.Suggestions, mcpMigrationSuggestion(server))
			}
		}
	}
	return report, nil
}

func defaultMCPMigrationCandidates() []mcpMigrationCandidate {
	var candidates []mcpMigrationCandidate
	home, _ := os.UserHomeDir()

	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" && home != "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if codexHome != "" {
		candidates = append(candidates, mcpMigrationCandidate{Source: "codex", Path: filepath.Join(codexHome, "config.toml")})
	}

	claudeHome := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeHome == "" && home != "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	if home != "" {
		candidates = append(candidates, mcpMigrationCandidate{Source: "claude", Path: filepath.Join(home, ".claude.json")})
	}
	if claudeHome != "" {
		candidates = append(candidates,
			mcpMigrationCandidate{Source: "claude", Path: filepath.Join(claudeHome, "mcp.json")},
			mcpMigrationCandidate{Source: "claude", Path: filepath.Join(claudeHome, "settings.json")},
		)
	}

	if path := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); path != "" {
		candidates = append(candidates, mcpMigrationCandidate{Source: "opencode", Path: path})
	}
	if home != "" {
		candidates = append(candidates,
			mcpMigrationCandidate{Source: "opencode", Path: filepath.Join(home, ".config", "opencode", "opencode.json")},
			mcpMigrationCandidate{Source: "opencode", Path: filepath.Join(home, ".config", "opencode", "config.json")},
		)
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates,
			mcpMigrationCandidate{Source: "workspace", Path: filepath.Join(cwd, ".mcp.json")},
			mcpMigrationCandidate{Source: "workspace", Path: filepath.Join(cwd, "mcp.json")},
		)
	}
	return dedupeMCPMigrationCandidates(candidates)
}

func dedupeMCPMigrationCandidates(in []mcpMigrationCandidate) []mcpMigrationCandidate {
	out := make([]mcpMigrationCandidate, 0, len(in))
	seen := map[string]bool{}
	for _, candidate := range in {
		if strings.TrimSpace(candidate.Path) == "" {
			continue
		}
		key := candidate.Source + "\x00" + candidate.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, candidate)
	}
	return out
}

func parseMCPMigrationFile(candidate mcpMigrationCandidate, raw []byte) ([]MCPMigrationServer, string, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, "", nil, fmt.Errorf("empty config file")
	}
	if filepath.Ext(candidate.Path) == ".toml" || bytes.Contains(trimmed, []byte("[mcp_servers.")) {
		servers, warnings, err := parseTOMLMCPMigration(candidate, raw)
		return servers, "toml", warnings, err
	}
	if trimmed[0] == '{' {
		servers, warnings, err := parseJSONMCPMigration(candidate, raw)
		return servers, "json", warnings, err
	}
	return nil, "", nil, fmt.Errorf("unsupported MCP config format")
}

func parseTOMLMCPMigration(candidate mcpMigrationCandidate, raw []byte) ([]MCPMigrationServer, []string, error) {
	var root codexMCPConfig
	if _, err := toml.Decode(string(raw), &root); err != nil {
		return nil, nil, err
	}
	if len(root.MCPServers) == 0 {
		return nil, []string{"no mcp_servers entries found"}, nil
	}
	names := sortedMapKeys(root.MCPServers)
	var servers []MCPMigrationServer
	var warnings []string
	for _, name := range names {
		rawServer := root.MCPServers[name]
		if rawServer.Enabled != nil && !*rawServer.Enabled {
			warnings = append(warnings, fmt.Sprintf("mcp_servers.%s is disabled; skipped", name))
			continue
		}
		server := migrationServerFromRaw(candidate, name, mcpMigrationRawServer{
			Command:                  rawServer.Command,
			Args:                     append([]string(nil), rawServer.Args...),
			EnvKeys:                  sortedMapKeys(rawServer.Env),
			EnvVars:                  append([]string(nil), rawServer.EnvVars...),
			CWD:                      rawServer.CWD,
			URL:                      rawServer.URL,
			BearerTokenEnvVar:        rawServer.BearerTokenEnvVar,
			HTTPHeaderNames:          sortedMapKeys(rawServer.HTTPHeaders),
			EnvHTTPHeaders:           cloneStringMap(rawServer.EnvHTTPHeaders),
			StartupTimeoutSec:        rawServer.StartupTimeoutSec,
			ToolTimeoutSec:           rawServer.ToolTimeoutSec,
			Required:                 rawServer.Required,
			EnabledTools:             append([]string(nil), rawServer.EnabledTools...),
			DisabledTools:            append([]string(nil), rawServer.DisabledTools...),
			DefaultToolsApprovalMode: rawServer.DefaultToolsApprovalMode,
		})
		servers = append(servers, server)
	}
	return servers, warnings, nil
}

func parseJSONMCPMigration(candidate mcpMigrationCandidate, raw []byte) ([]MCPMigrationServer, []string, error) {
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, nil, err
	}
	containers := jsonMCPContainers(root)
	if len(containers) == 0 {
		return nil, []string{"no mcpServers entries found"}, nil
	}
	var servers []MCPMigrationServer
	var warnings []string
	seen := map[string]bool{}
	for _, container := range containers {
		names := sortedAnyMapKeys(container)
		for _, name := range names {
			if seen[name] {
				continue
			}
			rawMap, ok := container[name].(map[string]any)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("mcpServers.%s is not an object; skipped", name))
				continue
			}
			rawServer := migrationRawServerFromJSON(rawMap)
			if rawServer.Enabled != nil && !*rawServer.Enabled {
				warnings = append(warnings, fmt.Sprintf("mcpServers.%s is disabled; skipped", name))
				continue
			}
			seen[name] = true
			servers = append(servers, migrationServerFromRaw(candidate, name, rawServer))
		}
	}
	return servers, warnings, nil
}

func jsonMCPContainers(root map[string]any) []map[string]any {
	var containers []map[string]any
	for _, key := range []string{"mcpServers", "mcp_servers"} {
		if container, ok := root[key].(map[string]any); ok {
			containers = append(containers, container)
		}
	}
	if mcp, ok := root["mcp"].(map[string]any); ok {
		for _, key := range []string{"servers", "mcpServers", "mcp_servers"} {
			if container, ok := mcp[key].(map[string]any); ok {
				containers = append(containers, container)
			}
		}
	}
	return containers
}

func migrationRawServerFromJSON(raw map[string]any) mcpMigrationRawServer {
	url := stringValue(raw, "url", "serverUrl", "server_url", "endpoint")
	if url == "" {
		if transport, ok := raw["transport"].(map[string]any); ok {
			url = stringValue(transport, "url", "endpoint")
		}
	}
	command, commandArgs := commandAndArgsValue(raw, "command", "cmd")
	args := append(commandArgs, stringListValue(raw, "args", "arguments")...)
	startupTimeout := numberValue(raw, "startup_timeout_sec", "startupTimeoutSec")
	toolTimeout := numberValue(raw, "tool_timeout_sec", "toolTimeoutSec")
	if timeout, ok := raw["timeout"].(map[string]any); ok {
		if startupTimeout == 0 {
			startupTimeout = numberValue(timeout, "startup", "startup_ms") / 1000
		}
		if toolTimeout == 0 {
			toolTimeout = numberValue(timeout, "request", "request_ms") / 1000
		}
	}
	server := mcpMigrationRawServer{
		Command:                  command,
		Args:                     args,
		EnvKeys:                  sortedMapKeys(stringMapValue(raw, "env", "environment")),
		EnvVars:                  stringListValue(raw, "envVars", "env_vars"),
		CWD:                      stringValue(raw, "cwd", "workingDirectory", "working_directory"),
		URL:                      url,
		BearerTokenEnvVar:        stringValue(raw, "bearer_token_env_var", "bearerTokenEnvVar"),
		HTTPHeaderNames:          sortedMapKeys(stringMapValue(raw, "headers", "httpHeaders", "http_headers")),
		EnvHTTPHeaders:           stringMapValue(raw, "envHttpHeaders", "env_http_headers"),
		StartupTimeoutSec:        startupTimeout,
		ToolTimeoutSec:           toolTimeout,
		Required:                 boolValue(raw, false, "required"),
		EnabledTools:             stringListValue(raw, "enabled_tools", "enabledTools"),
		DisabledTools:            stringListValue(raw, "disabled_tools", "disabledTools"),
		DefaultToolsApprovalMode: stringValue(raw, "default_tools_approval_mode", "defaultToolsApprovalMode"),
	}
	if enabled, ok := optionalBoolValue(raw, "enabled"); ok {
		server.Enabled = &enabled
	} else if disabled, ok := optionalBoolValue(raw, "disabled"); ok {
		enabled = !disabled
		server.Enabled = &enabled
	}
	return server
}

func migrationServerFromRaw(candidate mcpMigrationCandidate, name string, raw mcpMigrationRawServer) MCPMigrationServer {
	server := MCPMigrationServer{
		Source:                   candidate.Source,
		SourcePath:               candidate.Path,
		Name:                     name,
		Command:                  strings.TrimSpace(raw.Command),
		Args:                     cleanStringList(raw.Args),
		CWD:                      strings.TrimSpace(raw.CWD),
		URL:                      strings.TrimSpace(raw.URL),
		EnvVars:                  cleanStringList(append(raw.EnvVars, raw.EnvKeys...)),
		RedactedEnv:              cleanStringList(raw.EnvKeys),
		BearerTokenEnvVar:        strings.TrimSpace(raw.BearerTokenEnvVar),
		HTTPHeaderNames:          cleanStringList(raw.HTTPHeaderNames),
		EnvHTTPHeaders:           cleanStringMap(raw.EnvHTTPHeaders),
		RedactedHTTPHeaders:      cleanStringList(raw.HTTPHeaderNames),
		StartupTimeoutSec:        raw.StartupTimeoutSec,
		ToolTimeoutSec:           raw.ToolTimeoutSec,
		Required:                 raw.Required,
		EnabledTools:             cleanStringList(raw.EnabledTools),
		DisabledTools:            cleanStringList(raw.DisabledTools),
		DefaultToolsApprovalMode: strings.TrimSpace(raw.DefaultToolsApprovalMode),
	}
	switch {
	case server.Command != "" && server.URL == "":
		server.Transport = "stdio"
		server.Compatible = true
		server.RuntimeSupported = true
	case server.URL != "" && server.Command == "":
		server.Transport = "http"
		server.Compatible = true
		server.RuntimeSupported = false
		server.UnsupportedReason = "streamable HTTP MCP is parsed for diagnostics but not started by billyharness yet"
		server.Notes = append(server.Notes, server.UnsupportedReason)
	case server.Command != "" && server.URL != "":
		server.Transport = "invalid"
		server.UnsupportedReason = "command and url are mutually exclusive"
		server.Notes = append(server.Notes, server.UnsupportedReason)
	default:
		server.Transport = "invalid"
		server.UnsupportedReason = "command or url required"
		server.Notes = append(server.Notes, server.UnsupportedReason)
	}
	if len(server.RedactedEnv) > 0 {
		server.Notes = append(server.Notes, "env values were redacted; set these names in the environment or $BILLYHARNESS_HOME/.env")
	}
	if len(server.RedactedHTTPHeaders) > 0 {
		server.Notes = append(server.Notes, "static HTTP header values were redacted; prefer bearer_token_env_var or env_http_headers")
	}
	return server
}

func mcpMigrationSuggestion(server MCPMigrationServer) MCPMigrationSuggestion {
	return MCPMigrationSuggestion{
		Name:       server.Name,
		Source:     server.Source,
		SourcePath: server.SourcePath,
		TOML:       renderMCPMigrationTOML(server),
		Notes:      append([]string(nil), server.Notes...),
	}
}

func renderMCPMigrationTOML(server MCPMigrationServer) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("[mcp_servers.%s]", quoteBareOrDottedKey(server.Name)))
	switch server.Transport {
	case "stdio":
		lines = append(lines, "command = "+strconv.Quote(server.Command))
		if len(server.Args) > 0 {
			lines = append(lines, "args = "+tomlStringList(server.Args))
		}
		if server.CWD != "" {
			lines = append(lines, "cwd = "+strconv.Quote(server.CWD))
		}
	case "http":
		lines = append(lines, "url = "+strconv.Quote(server.URL))
		if server.BearerTokenEnvVar != "" {
			lines = append(lines, "bearer_token_env_var = "+strconv.Quote(server.BearerTokenEnvVar))
		}
		if len(server.EnvHTTPHeaders) > 0 {
			lines = append(lines, "env_http_headers = "+tomlStringMap(server.EnvHTTPHeaders))
		}
	}
	if len(server.EnvVars) > 0 {
		lines = append(lines, "env_vars = "+tomlStringList(server.EnvVars))
	}
	if server.StartupTimeoutSec > 0 {
		lines = append(lines, fmt.Sprintf("startup_timeout_sec = %.1f", server.StartupTimeoutSec))
	}
	if server.ToolTimeoutSec > 0 {
		lines = append(lines, fmt.Sprintf("tool_timeout_sec = %.1f", server.ToolTimeoutSec))
	}
	if server.Required {
		lines = append(lines, "required = true")
	}
	if len(server.EnabledTools) > 0 {
		lines = append(lines, "enabled_tools = "+tomlStringList(server.EnabledTools))
	}
	if len(server.DisabledTools) > 0 {
		lines = append(lines, "disabled_tools = "+tomlStringList(server.DisabledTools))
	}
	if server.DefaultToolsApprovalMode != "" {
		lines = append(lines, "default_tools_approval_mode = "+strconv.Quote(server.DefaultToolsApprovalMode))
	}
	if len(server.RedactedEnv) > 0 {
		lines = append(lines, "# Redacted env values from source; define these names outside the config: "+strings.Join(server.RedactedEnv, ", "))
	}
	if len(server.RedactedHTTPHeaders) > 0 {
		lines = append(lines, "# Redacted static HTTP headers from source: "+strings.Join(server.RedactedHTTPHeaders, ", "))
	}
	if server.UnsupportedReason != "" {
		lines = append(lines, "# Note: "+server.UnsupportedReason)
	}
	return strings.Join(lines, "\n")
}

func FormatMCPMigrationReport(report MCPMigrationReport) string {
	var lines []string
	found := 0
	for _, file := range report.Files {
		if file.Exists {
			found++
		}
	}
	lines = append(lines, "billyharness MCP migration diagnostics")
	lines = append(lines, fmt.Sprintf("files: checked=%d found=%d servers=%d suggestions=%d", len(report.Files), found, len(report.Servers), len(report.Suggestions)))
	if len(report.Files) > 0 {
		lines = append(lines, "", "files:")
		for _, file := range report.Files {
			status := "missing"
			if file.Exists {
				status = "found"
			}
			detail := fmt.Sprintf("- %s %s source=%s", status, file.Path, file.Source)
			if file.Format != "" {
				detail += " format=" + file.Format
			}
			if file.ServerCount > 0 {
				detail += fmt.Sprintf(" servers=%d", file.ServerCount)
			}
			if file.Error != "" {
				detail += " error=" + file.Error
			}
			lines = append(lines, detail)
		}
	}
	if len(report.Servers) > 0 {
		lines = append(lines, "", "servers:")
		for _, server := range report.Servers {
			status := "unsupported"
			if server.RuntimeSupported {
				status = "supported"
			} else if server.Compatible {
				status = "parsed"
			}
			target := server.Command
			if target == "" {
				target = server.URL
			}
			line := fmt.Sprintf("- %s source=%s transport=%s status=%s target=%s", server.Name, server.Source, server.Transport, status, target)
			if len(server.EnvVars) > 0 {
				line += " env_vars=" + strings.Join(server.EnvVars, ",")
			}
			if server.UnsupportedReason != "" {
				line += " note=" + server.UnsupportedReason
			}
			lines = append(lines, line)
		}
	}
	if len(report.Warnings) > 0 {
		lines = append(lines, "", "warnings:")
		for _, warning := range report.Warnings {
			lines = append(lines, "- "+warning)
		}
	}
	if len(report.Suggestions) > 0 {
		lines = append(lines, "", "suggested billyharness mcp.config.toml snippets:")
		for _, suggestion := range report.Suggestions {
			lines = append(lines, "", suggestion.TOML)
			for _, note := range suggestion.Notes {
				lines = append(lines, "# "+note)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func cleanStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	keys := sortedMapKeys(in)
	for _, key := range keys {
		value := strings.TrimSpace(in[key])
		if strings.TrimSpace(key) != "" && value != "" {
			out[strings.TrimSpace(key)] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedMapKeys[V any](in map[string]V) []string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, strings.TrimSpace(key))
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyMapKeys(in map[string]any) []string {
	return sortedMapKeys(in)
}

func stringValue(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func commandAndArgsValue(raw map[string]any, keys ...string) (string, []string) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed), nil
		case []any:
			var parts []string
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			if len(parts) > 0 {
				return parts[0], append([]string(nil), parts[1:]...)
			}
		case []string:
			parts := cleanStringList(typed)
			if len(parts) > 0 {
				return parts[0], append([]string(nil), parts[1:]...)
			}
		}
	}
	return "", nil
}

func stringListValue(raw map[string]any, keys ...string) []string {
	var out []string
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				switch itemTyped := item.(type) {
				case string:
					out = append(out, itemTyped)
				case map[string]any:
					out = append(out, stringValue(itemTyped, "name", "key"))
				}
			}
		case []string:
			out = append(out, typed...)
		case string:
			out = append(out, typed)
		}
	}
	return cleanStringList(out)
}

func stringMapValue(raw map[string]any, keys ...string) map[string]string {
	out := map[string]string{}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			for mapKey, mapValue := range typed {
				if text := fmt.Sprint(mapValue); strings.TrimSpace(mapKey) != "" && strings.TrimSpace(text) != "" {
					out[strings.TrimSpace(mapKey)] = strings.TrimSpace(text)
				}
			}
		case map[string]string:
			for mapKey, mapValue := range typed {
				if strings.TrimSpace(mapKey) != "" && strings.TrimSpace(mapValue) != "" {
					out[strings.TrimSpace(mapKey)] = strings.TrimSpace(mapValue)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boolValue(raw map[string]any, fallback bool, keys ...string) bool {
	if value, ok := optionalBoolValue(raw, keys...); ok {
		return value
	}
	return fallback
}

func optionalBoolValue(raw map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
			if err == nil {
				return parsed, true
			}
		}
	}
	return false, false
}

func numberValue(raw map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case json.Number:
			parsed, _ := typed.Float64()
			return parsed
		case string:
			parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			return parsed
		}
	}
	return 0
}

func quoteBareOrDottedKey(key string) string {
	parts := strings.Split(key, ".")
	for i, part := range parts {
		if !isTOMLBareKey(part) {
			parts[i] = strconv.Quote(part)
		}
	}
	return strings.Join(parts, ".")
}

func isTOMLBareKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func tomlStringList(values []string) string {
	cleaned := cleanStringList(values)
	quoted := make([]string, 0, len(cleaned))
	for _, value := range cleaned {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func tomlStringMap(values map[string]string) string {
	keys := sortedMapKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, quoteBareOrDottedKey(key)+" = "+strconv.Quote(values[key]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}
