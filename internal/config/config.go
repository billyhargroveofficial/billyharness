package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

type Config struct {
	Provider                      string
	Model                         string
	Profile                       string
	BaseURL                       string
	APIKeyEnv                     string
	CredentialFile                string
	CodexBaseURL                  string
	CodexAuthFile                 string
	CodexRefreshURL               string
	CodexAuthAPIBaseURL           string
	CodexClientID                 string
	CodexOriginator               string
	Thinking                      string
	ReasoningEffort               string
	DisableSpark                  bool
	MaxTokens                     int
	MaxToolRounds                 int
	MaxParallelTools              int
	ProviderMaxRetries            int
	ContextWindowTokens           int64
	ContextCompactTokens          int
	ContextCompactKeep            int
	ContextCompactMaxChars        int
	ContextCompactStrategy        string
	ContextCompactSummaryProvider string
	ContextCompactSummaryModel    string
	WebSummaryMode                string
	WebSummaryProvider            string
	WebSummaryModel               string
	WebSummaryMaxInputTokens      int
	WebSummaryMaxOutputTokens     int
	WebSummaryTimeout             time.Duration
	WebCacheEnabled               bool
	WebCacheTTL                   time.Duration
	WebCacheMaxBytes              int64
	RequestTimeout                time.Duration
	StreamIdleTimeout             time.Duration
	WorkspaceRoots                []string
	ProjectDocMaxBytes            int
	ProjectDocFallbacks           []string
	MaxToolOutputBytes            int
	AutoApproveDangerous          bool
	StoreReasoningContent         bool
	GatewayAddr                   string
	MCPEnabled                    bool
	MCPConfigFiles                []string
	MCPAllowedServers             []string
	MCPServers                    []MCPServer
	HooksEnabled                  bool
	HookConfigFiles               []string
	Hooks                         []Hook
}

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

type Hook struct {
	Name           string
	Event          string
	Command        string
	Args           []string
	Env            map[string]string
	EnvVars        []string
	CWD            string
	Timeout        time.Duration
	MaxOutputBytes int
	Fatal          bool
	Enabled        bool
}

func Default() Config {
	return MustResolve().Config
}

func (c Config) APIKey() string {
	if c.Provider == "mock" {
		return ""
	}
	if value := os.Getenv(c.APIKeyEnv); value != "" {
		return value
	}
	return dotenvValue(c.APIKeyEnv)
}

func (c *Config) ApplyModelProviderDefaults() {
	c.Model = modelinfo.NormalizeAlias(c.Model)
	if c.DisableSpark && modelinfo.IsSparkModel(c.Model) {
		c.Model = "gpt-5.4-mini"
	}
	c.Provider = modelinfo.ProviderForModel(c.Model, c.Provider)
}

func (c *Config) ApplyWebSummaryDefaults() {
	c.WebSummaryMode = NormalizeWebSummaryMode(c.WebSummaryMode)
	c.WebSummaryModel = modelinfo.NormalizeAlias(c.WebSummaryModel)
	if c.DisableSpark && modelinfo.IsSparkModel(c.WebSummaryModel) {
		c.WebSummaryModel = "gpt-5.4-mini"
	}
	c.WebSummaryProvider = modelinfo.NormalizeProvider(c.WebSummaryProvider)
	if c.WebSummaryModel == "" {
		c.WebSummaryModel = modelinfo.DefaultSummaryModel(c.Model, c.Provider)
	}
	if c.WebSummaryProvider == "" {
		c.WebSummaryProvider = modelinfo.ProviderForModel(c.WebSummaryModel, c.Provider)
	}
	if c.WebSummaryMaxInputTokens <= 0 {
		c.WebSummaryMaxInputTokens = 12_000
	}
	if c.WebSummaryMaxOutputTokens <= 0 {
		c.WebSummaryMaxOutputTokens = 700
	}
	if c.WebSummaryTimeout <= 0 {
		c.WebSummaryTimeout = 60 * time.Second
	}
	c.ContextCompactStrategy = NormalizeContextCompactStrategy(c.ContextCompactStrategy)
	c.ContextCompactSummaryModel = modelinfo.NormalizeAlias(c.ContextCompactSummaryModel)
	if c.DisableSpark && modelinfo.IsSparkModel(c.ContextCompactSummaryModel) {
		c.ContextCompactSummaryModel = "gpt-5.4-mini"
	}
	c.ContextCompactSummaryProvider = modelinfo.NormalizeProvider(c.ContextCompactSummaryProvider)
	if c.ContextCompactSummaryModel == "" {
		c.ContextCompactSummaryModel = c.WebSummaryModel
	}
	if c.ContextCompactSummaryProvider == "" {
		c.ContextCompactSummaryProvider = modelinfo.ProviderForModel(c.ContextCompactSummaryModel, c.WebSummaryProvider)
	}
}

func NormalizeWebSummaryMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "model", "external", "llm", "provider":
		return "model"
	default:
		return "extractive"
	}
}

func NormalizeContextCompactStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "model", "external", "llm", "provider":
		return "model"
	default:
		return "deterministic"
	}
}

func (c *Config) ApplyBillySettingsDefaults() {
	path := filepath.Join(BillyHomeDir(), "settings.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var settings struct {
		LastSelectedModel   string `json:"last_selected_model"`
		LastReasoningKind   string `json:"last_reasoning_kind"`
		LastReasoningEffort string `json:"last_reasoning_effort"`
		LastProfile         string `json:"last_profile"`
		ContextWindowTokens int64  `json:"context_window_tokens"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return
	}
	if os.Getenv("FAST_AGENT_CONTEXT_WINDOW_TOKENS") == "" && settings.ContextWindowTokens > 0 {
		c.ContextWindowTokens = settings.ContextWindowTokens
	}
	if os.Getenv("FAST_AGENT_MODEL") == "" && strings.TrimSpace(settings.LastSelectedModel) != "" {
		c.Model = strings.TrimSpace(settings.LastSelectedModel)
	}
	if os.Getenv("DEEPSEEK_THINKING") == "" && strings.TrimSpace(settings.LastReasoningKind) != "" {
		c.Thinking = strings.TrimSpace(settings.LastReasoningKind)
	}
	if os.Getenv("DEEPSEEK_REASONING_EFFORT") == "" && strings.TrimSpace(settings.LastReasoningEffort) != "" {
		c.ReasoningEffort = strings.TrimSpace(settings.LastReasoningEffort)
	}
	if os.Getenv("BILLYHARNESS_PROFILE") == "" && os.Getenv("FAST_AGENT_PROFILE") == "" && strings.TrimSpace(settings.LastProfile) != "" {
		c.Profile = NormalizeProfileName(settings.LastProfile)
	}
}

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

func (c *Config) LoadDefaultHooks() error {
	if !c.HooksEnabled {
		c.Hooks = nil
		return nil
	}
	files := c.HookConfigFiles
	if len(files) == 0 {
		files = DefaultHookConfigFiles()
	}
	hooks, err := LoadHooks(files)
	if err != nil {
		return err
	}
	c.HookConfigFiles = files
	c.Hooks = hooks
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

func DefaultHookConfigFiles() []string {
	path := DefaultHookConfigFile()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{path}
}

func DefaultHookConfigFile() string {
	return filepath.Join(BillyHomeDir(), "hooks.config.toml")
}

func DefaultCodexAuthFile() string {
	return filepath.Join(BillyHomeDir(), "auth", "codex.json")
}

func DefaultCredentialFile() string {
	return filepath.Join(BillyHomeDir(), "auth", "credentials.json")
}

const DefaultProfileName = "billy"

func NormalizeProfileName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return DefaultProfileName
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	normalized := strings.Trim(b.String(), ".")
	if normalized == "" {
		return DefaultProfileName
	}
	return normalized
}

func DefaultProfileDir(profile string) string {
	return filepath.Join(BillyHomeDir(), "profiles", NormalizeProfileName(profile))
}

func DefaultProfileFile(profile string) string {
	return filepath.Join(DefaultProfileDir(profile), "SOUL.md")
}

func EnsureDefaultProfileFile(profile string) (string, error) {
	name := NormalizeProfileName(profile)
	path := DefaultProfileFile(name)
	if _, err := EnsureDefaultProfileMetadataFile(name); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if name != DefaultProfileName {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(defaultBillyProfilePrompt), 0o600)
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

const defaultBillyProfilePrompt = `Пиши как внимательный преподаватель, ближе к стилю Claude Opus: спокойно, связно, человечески и без ощущения корпоративного отчёта.

По умолчанию отвечай цельными абзацами, а не списками. Не используй буллеты, нумерацию, таблицы, жирные заголовки, эмодзи и чрезмерный markdown, если я прямо не прошу список, чеклист, сравнение, алгоритм или таблицу. Если нужно перечислить несколько идей, вплетай их в обычные предложения.

В математике сначала объясняй смысл идеи, потом формулу, потом интуицию, потом короткий пример. Формулы пиши в LaTeX, но обязательно расшифровывай каждую переменную человеческим языком. Не перескакивай через шаги: если используется преобразование, теорема, распределение, оценка или приближение, объясни зачем оно нужно и почему это законно.

Не делай “конспект из пунктов”. Веди меня как преподаватель у доски: одна мысль плавно вытекает из другой. Если тема сложная, объясняй подробно, но без воды и повторов. Если я ошибаюсь в предпосылке, мягко поправь. Если ответ можно дать коротко, дай коротко; если без длинного объяснения я не пойму следующую тему, объясняй глубже.

Очень важно, разбирай материал максимально интересно, чтобы я прям хотел учиться и мне было максимально интересно выучить эту штуку, пока что я в ахуе просто и заебался учиться. С исторической справкой можно, я люблю всякие истории.
`

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

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

type codexMCPConfig struct {
	MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
}

type hooksConfig struct {
	Hooks map[string]map[string]hookTOML `toml:"hooks"`
}

type hookTOML struct {
	Command        string            `toml:"command"`
	Args           []string          `toml:"args"`
	Env            map[string]string `toml:"env"`
	EnvVars        mcpEnvVars        `toml:"env_vars"`
	CWD            string            `toml:"cwd"`
	TimeoutSec     float64           `toml:"timeout_sec"`
	MaxOutputBytes int               `toml:"max_output_bytes"`
	Fatal          bool              `toml:"fatal"`
	Enabled        *bool             `toml:"enabled"`
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

func LoadHooks(files []string) ([]Hook, error) {
	merged := map[string]Hook{}
	var keys []string
	for _, file := range files {
		if strings.TrimSpace(file) == "" {
			continue
		}
		var root hooksConfig
		if _, err := toml.DecodeFile(file, &root); err != nil {
			return nil, err
		}
		for rawEvent, hooksByName := range root.Hooks {
			event := normalizeHookEvent(rawEvent)
			if !validHookEvent(event) {
				return nil, fmt.Errorf("hooks.%s: unsupported hook event", rawEvent)
			}
			names := make([]string, 0, len(hooksByName))
			for name := range hooksByName {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				raw := hooksByName[name]
				hook, ok, err := raw.toHook(event, name)
				if err != nil {
					return nil, err
				}
				key := event + "/" + hook.Name
				if !ok {
					delete(merged, key)
					keys = removeString(keys, key)
					continue
				}
				if _, exists := merged[key]; !exists {
					keys = append(keys, key)
				}
				merged[key] = hook
			}
		}
	}
	out := make([]Hook, 0, len(merged))
	for _, key := range keys {
		if hook, ok := merged[key]; ok {
			out = append(out, hook)
		}
	}
	return out, nil
}

func (h hookTOML) toHook(event, name string) (Hook, bool, error) {
	enabled := true
	if h.Enabled != nil {
		enabled = *h.Enabled
	}
	if !enabled {
		return Hook{}, false, nil
	}
	if strings.TrimSpace(name) == "" {
		return Hook{}, false, fmt.Errorf("hooks.%s: hook name required", event)
	}
	if strings.TrimSpace(h.Command) == "" {
		return Hook{}, false, fmt.Errorf("hooks.%s.%s: command required", event, name)
	}
	timeout := time.Duration(h.TimeoutSec * float64(time.Second))
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	maxOutput := h.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 4096
	}
	if maxOutput > 64*1024 {
		maxOutput = 64 * 1024
	}
	return Hook{
		Name:           normalizeHookName(name),
		Event:          event,
		Command:        strings.TrimSpace(h.Command),
		Args:           append([]string(nil), h.Args...),
		Env:            cloneStringMap(h.Env),
		EnvVars:        append([]string(nil), h.EnvVars...),
		CWD:            strings.TrimSpace(h.CWD),
		Timeout:        timeout,
		MaxOutputBytes: maxOutput,
		Fatal:          h.Fatal,
		Enabled:        true,
	}, true, nil
}

func normalizeHookName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Trim(value, "_")
}

func normalizeHookEvent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Trim(value, "_")
}

func validHookEvent(event string) bool {
	switch event {
	case "session_start", "before_tool", "after_tool", "mcp_status_change", "provider_retry", "session_done":
		return true
	default:
		return false
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envList(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func envListDefault(key string, fallback []string) []string {
	if value := envList(key); len(value) > 0 {
		return value
	}
	return append([]string(nil), fallback...)
}

func LookupEnvOrDotenv(key string) (string, bool) {
	if value, ok := os.LookupEnv(key); ok {
		if strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	value := dotenvValue(key)
	return value, value != ""
}

func dotenvValue(key string) string {
	for _, path := range findDotenvFiles() {
		if value, ok := dotenvValueFromFile(path, key); ok {
			return value
		}
	}
	return ""
}

func dotenvValueFromFile(path, key string) (string, bool) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`), true
	}
	return "", false
}

func findDotenv() string {
	files := findDotenvFiles()
	if len(files) == 0 {
		return ""
	}
	return files[0]
}

func findDotenvFiles() []string {
	seen := map[string]bool{}
	var files []string
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || seen[path] {
			return
		}
		seen[path] = true
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	add(filepath.Join(BillyHomeDir(), ".env"))
	if dotenvHomeOnly() {
		return files
	}
	if explicit := strings.TrimSpace(os.Getenv("FAST_AGENT_ENV_FILE")); explicit != "" {
		return []string{explicit}
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			add(filepath.Join(dir, ".env"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return files
}

func dotenvHomeOnly() bool {
	return envBool("BILLYHARNESS_DOTENV_HOME_ONLY", false)
}

func BillyHomeDir() string {
	if explicit := os.Getenv("BILLYHARNESS_HOME"); explicit != "" {
		return explicit
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".billyharness"
	}
	return filepath.Join(home, "billyharness")
}
