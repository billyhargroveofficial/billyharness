package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

type Config struct {
	Provider                  string
	Model                     string
	Profile                   string
	BaseURL                   string
	APIKeyEnv                 string
	CodexBaseURL              string
	CodexAuthFile             string
	CodexRefreshURL           string
	CodexAuthAPIBaseURL       string
	CodexClientID             string
	CodexOriginator           string
	Thinking                  string
	ReasoningEffort           string
	MaxTokens                 int
	MaxToolRounds             int
	MaxParallelTools          int
	ProviderMaxRetries        int
	ContextWindowTokens       int64
	ContextCompactTokens      int
	ContextCompactKeep        int
	ContextCompactMaxChars    int
	WebSummaryMode            string
	WebSummaryProvider        string
	WebSummaryModel           string
	WebSummaryMaxInputTokens  int
	WebSummaryMaxOutputTokens int
	WebSummaryTimeout         time.Duration
	RequestTimeout            time.Duration
	StreamIdleTimeout         time.Duration
	WorkspaceRoots            []string
	ProjectDocMaxBytes        int
	ProjectDocFallbacks       []string
	MaxToolOutputBytes        int
	AutoApproveDangerous      bool
	StoreReasoningContent     bool
	GatewayAddr               string
	MCPEnabled                bool
	MCPConfigFiles            []string
	MCPAllowedServers         []string
	MCPServers                []MCPServer
}

type MCPServer struct {
	Name                     string
	Command                  string
	Args                     []string
	Env                      map[string]string
	EnvVars                  []string
	CWD                      string
	URL                      string
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

func Default() Config {
	cwd, _ := os.Getwd()
	cfg := Config{
		Provider:                  env("FAST_AGENT_PROVIDER", "deepseek"),
		Model:                     env("FAST_AGENT_MODEL", "deepseek-v4-flash"),
		Profile:                   NormalizeProfileName(env("BILLYHARNESS_PROFILE", env("FAST_AGENT_PROFILE", DefaultProfileName))),
		BaseURL:                   env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		APIKeyEnv:                 env("DEEPSEEK_API_KEY_ENV", "DEEPSEEK_API_KEY"),
		CodexBaseURL:              env("FAST_AGENT_CODEX_BASE_URL", "https://chatgpt.com/backend-api/codex"),
		CodexAuthFile:             env("FAST_AGENT_CODEX_AUTH_FILE", DefaultCodexAuthFile()),
		CodexRefreshURL:           env("FAST_AGENT_CODEX_REFRESH_URL", "https://auth.openai.com/oauth/token"),
		CodexAuthAPIBaseURL:       env("CODEX_AUTHAPI_BASE_URL", "https://auth.openai.com/api/accounts"),
		CodexClientID:             env("FAST_AGENT_CODEX_CLIENT_ID", "app_EMoamEEZ73f0CkXaXp7hrann"),
		CodexOriginator:           env("FAST_AGENT_CODEX_ORIGINATOR", "billyharness"),
		Thinking:                  env("DEEPSEEK_THINKING", "enabled"),
		ReasoningEffort:           env("DEEPSEEK_REASONING_EFFORT", "high"),
		MaxTokens:                 envInt("FAST_AGENT_MAX_TOKENS", 8192),
		MaxToolRounds:             envInt("FAST_AGENT_MAX_TOOL_ROUNDS", 100),
		MaxParallelTools:          envInt("FAST_AGENT_MAX_PARALLEL_TOOLS", 4),
		ProviderMaxRetries:        envInt("FAST_AGENT_PROVIDER_MAX_RETRIES", 2),
		ContextWindowTokens:       int64(envInt("FAST_AGENT_CONTEXT_WINDOW_TOKENS", 1_000_000)),
		ContextCompactTokens:      envInt("FAST_AGENT_CONTEXT_COMPACT_TOKENS", 600_000),
		ContextCompactKeep:        envInt("FAST_AGENT_CONTEXT_COMPACT_KEEP", 32),
		ContextCompactMaxChars:    envInt("FAST_AGENT_CONTEXT_COMPACT_MAX_CHARS", 120_000),
		WebSummaryMode:            env("FAST_AGENT_WEB_SUMMARY_MODE", "extractive"),
		WebSummaryProvider:        env("FAST_AGENT_WEB_SUMMARY_PROVIDER", ""),
		WebSummaryModel:           env("FAST_AGENT_WEB_SUMMARY_MODEL", ""),
		WebSummaryMaxInputTokens:  envInt("FAST_AGENT_WEB_SUMMARY_MAX_INPUT_TOKENS", 12_000),
		WebSummaryMaxOutputTokens: envInt("FAST_AGENT_WEB_SUMMARY_MAX_OUTPUT_TOKENS", 700),
		WebSummaryTimeout:         time.Duration(envInt("FAST_AGENT_WEB_SUMMARY_TIMEOUT_SEC", 60)) * time.Second,
		RequestTimeout:            time.Duration(envInt("FAST_AGENT_REQUEST_TIMEOUT_SEC", 240)) * time.Second,
		StreamIdleTimeout:         time.Duration(envInt("FAST_AGENT_STREAM_IDLE_TIMEOUT_SEC", 60)) * time.Second,
		WorkspaceRoots:            []string{filepath.Clean(cwd)},
		ProjectDocMaxBytes:        envInt("FAST_AGENT_PROJECT_DOC_MAX_BYTES", 32*1024),
		ProjectDocFallbacks:       envList("FAST_AGENT_PROJECT_DOC_FALLBACK_FILENAMES"),
		MaxToolOutputBytes:        envInt("FAST_AGENT_MAX_TOOL_OUTPUT_BYTES", 64*1024),
		AutoApproveDangerous:      envBool("FAST_AGENT_AUTO_APPROVE_DANGEROUS", true),
		StoreReasoningContent:     envBool("FAST_AGENT_STORE_REASONING", false),
		GatewayAddr:               env("FAST_AGENT_GATEWAY_ADDR", "127.0.0.1:8765"),
		MCPEnabled:                envBool("FAST_AGENT_MCP_ENABLED", true),
		MCPConfigFiles:            envList("FAST_AGENT_MCP_CONFIG_FILES"),
		MCPAllowedServers:         envListDefault("FAST_AGENT_MCP_ALLOWED_SERVERS", []string{"telegram", "telegram-parilka", "github", "context7"}),
	}
	cfg.ApplyBillySettingsDefaults()
	cfg.ApplyModelProviderDefaults()
	cfg.ApplyWebSummaryDefaults()
	return cfg
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
	c.Provider = modelinfo.ProviderForModel(c.Model, c.Provider)
}

func (c *Config) ApplyWebSummaryDefaults() {
	c.WebSummaryMode = NormalizeWebSummaryMode(c.WebSummaryMode)
	c.WebSummaryModel = modelinfo.NormalizeAlias(c.WebSummaryModel)
	c.WebSummaryProvider = modelinfo.NormalizeProvider(c.WebSummaryProvider)
	if c.WebSummaryModel == "" {
		if modelinfo.ProviderForModel(c.Model, c.Provider) == modelinfo.ProviderOpenAICodex {
			c.WebSummaryModel = "gpt-5.4-mini"
		} else {
			c.WebSummaryModel = "deepseek-v4-flash"
		}
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
}

func NormalizeWebSummaryMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "model", "external", "llm", "provider":
		return "model"
	default:
		return "extractive"
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
		path, err := EnsureDefaultMCPConfigFile()
		if err != nil {
			return err
		}
		files = []string{path}
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

func DefaultCodexAuthFile() string {
	return filepath.Join(BillyHomeDir(), "auth", "codex.json")
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
