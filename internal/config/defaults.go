package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

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
	c.WebSearchBackend = NormalizeWebBackend(c.WebSearchBackend)
	c.WebExtractBackend = NormalizeWebBackend(c.WebExtractBackend)
	if strings.TrimSpace(c.WebTavilyAPIKeyEnv) == "" {
		c.WebTavilyAPIKeyEnv = "TAVILY_API_KEY"
	}
	if strings.TrimSpace(c.WebExaAPIKeyEnv) == "" {
		c.WebExaAPIKeyEnv = "EXA_API_KEY"
	}
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

func NormalizeWebBackend(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "exa":
		return "exa"
	case "tavily":
		return "tavily"
	case "auto":
		return "auto"
	default:
		return "native"
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

func builtInConfig() Config {
	cwd, _ := os.Getwd()
	return Config{
		Provider:                  "deepseek",
		Model:                     "deepseek-v4-flash",
		Profile:                   DefaultProfileName,
		BaseURL:                   "https://api.deepseek.com",
		APIKeyEnv:                 "DEEPSEEK_API_KEY",
		CredentialFile:            DefaultCredentialFile(),
		CodexBaseURL:              "https://chatgpt.com/backend-api/codex",
		CodexAuthFile:             DefaultCodexAuthFile(),
		CodexRefreshURL:           "https://auth.openai.com/oauth/token",
		CodexAuthAPIBaseURL:       "https://auth.openai.com/api/accounts",
		CodexClientID:             "app_EMoamEEZ73f0CkXaXp7hrann",
		CodexOriginator:           "billyharness",
		Thinking:                  "enabled",
		ReasoningEffort:           "high",
		DisableSpark:              false,
		MaxTokens:                 8192,
		MaxToolRounds:             100,
		MaxParallelTools:          4,
		ProviderMaxRetries:        2,
		ContextWindowTokens:       1_000_000,
		ContextCompactTokens:      600_000,
		ContextCompactKeep:        32,
		ContextCompactMaxChars:    120_000,
		ContextCompactStrategy:    "deterministic",
		WebSummaryMode:            "extractive",
		WebSummaryMaxInputTokens:  12_000,
		WebSummaryMaxOutputTokens: 700,
		WebSummaryTimeout:         60 * time.Second,
		WebCacheEnabled:           true,
		WebCacheTTL:               10 * time.Minute,
		WebCacheMaxBytes:          128 * 1024 * 1024,
		WebSearchBackend:          "native",
		WebExtractBackend:         "native",
		WebTavilyAPIKeyEnv:        "TAVILY_API_KEY",
		WebExaAPIKeyEnv:           "EXA_API_KEY",
		RequestTimeout:            240 * time.Second,
		StreamIdleTimeout:         60 * time.Second,
		WorkspaceRoots:            []string{filepath.Clean(cwd)},
		ProjectDocMaxBytes:        32 * 1024,
		ProjectContextMaxBytes:    4 * 1024,
		MemoryEnabled:             true,
		MemoryAutoExtractEnabled:  false,
		MemorySummaryMaxBytes:     2 * 1024,
		MemoryIndexMaxBytes:       25 * 1024,
		MemoryTopicMaxBytes:       64 * 1024,
		MaxToolOutputBytes:        64 * 1024,
		DiagnosticsEnabled:        true,
		AutoApproveDangerous:      true,
		AccessMode:                AccessModeBuild,
		GatewayAddr:               "127.0.0.1:8765",
		MCPEnabled:                true,
		MCPAllowedServers:         []string{"telegram", "telegram-parilka", "github", "context7"},
		HooksEnabled:              true,
	}
}
