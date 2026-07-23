package config

import (
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

type AuthSettings struct {
	APIKeyEnv           string
	CredentialFile      string
	CodexAuthFile       string
	CodexRefreshURL     string
	CodexAuthAPIBaseURL string
	CodexClientID       string
	CodexOriginator     string
}

type ProviderSelection struct {
	Provider     string
	BaseURL      string
	CodexBaseURL string
}

type ModelSelection struct {
	Model           string
	Thinking        string
	ReasoningEffort string
	DisableSpark    bool
	MaxTokens       int
}

type ProfileSelection struct {
	Profile string
}

type RuntimeLimits struct {
	MaxTokens                     int
	MaxToolRounds                 int
	MaxToolCalls                  int
	MaxParallelTools              int
	ProviderMaxRetries            int
	MaxToolOutputBytes            int
	ContextWindowTokens           int64
	ContextWindowSource           string
	ContextCompactTokens          int
	ContextCompactSource          string
	ContextCompactKeep            int
	ContextCompactMaxChars        int
	ContextCompactStrategy        string
	ContextCompactSummaryProvider string
	ContextCompactSummaryModel    string
	RequestTimeout                time.Duration
	StreamIdleTimeout             time.Duration
}

type ToolPolicySettings struct {
	WorkspaceRoots            []string
	ProjectDocMaxBytes        int
	ProjectDocFallbacks       []string
	ProjectContextMaxBytes    int
	MemoryEnabled             bool
	MemorySummaryMaxBytes     int
	MemoryIndexMaxBytes       int
	MemoryTopicMaxBytes       int
	MaxToolOutputBytes        int
	AutoApproveDangerous      bool
	AccessMode                string
	StoreReasoningContent     bool
	WebSummaryMode            string
	WebSummaryProvider        string
	WebSummaryModel           string
	WebSummaryMaxInputTokens  int
	WebSummaryMaxOutputTokens int
	WebSummaryTimeout         time.Duration
	WebCacheEnabled           bool
	WebCacheTTL               time.Duration
	WebCacheMaxBytes          int64
	WebSearchBackend          string
	WebExtractBackend         string
	WebTavilyAPIKeyEnv        string
	WebExaAPIKeyEnv           string
	WebHermesEnvFiles         []string
}

type DiagnosticsSettings struct {
	Enabled     bool
	ConfigFiles []string
	Commands    []DiagnosticCommand
}

type MCPSettings struct {
	Enabled                   bool
	ConfigFiles               []string
	AllowedServers            []string
	PromoteServerInstructions bool
	Servers                   []MCPServer
}

type HookSettings struct {
	Enabled     bool
	ConfigFiles []string
	Hooks       []Hook
}

type AgentClubSettings struct {
	ConfigFiles []string
}

type InstructionSettings struct {
	Profile                ProfileSelection
	WorkspaceRoots         []string
	ProjectDocMaxBytes     int
	ProjectDocFallbacks    []string
	ProjectContextMaxBytes int
	MemoryEnabled          bool
	MemorySummaryMaxBytes  int
	MemoryIndexMaxBytes    int
	MemoryTopicMaxBytes    int
}

type ProviderBinding struct {
	Provider ProviderSelection
	Model    ModelSelection
	Auth     AuthSettings
	Limits   RuntimeLimits
}

func (c Config) AuthSettings() AuthSettings {
	cfg := c
	cfg.ApplyModelProviderDefaults()
	apiKeyEnv := strings.TrimSpace(cfg.APIKeyEnv)
	if cfg.Provider == modelinfo.ProviderQwen {
		apiKeyEnv = modelinfo.Provider(modelinfo.ProviderQwen).APIKeyEnv
	} else if !cfg.apiKeyEnvExplicitOverride && (apiKeyEnv == "" || knownProviderAPIKeyEnv(apiKeyEnv)) {
		if providerEnv := modelinfo.Provider(cfg.Provider).APIKeyEnv; providerEnv != "" {
			apiKeyEnv = providerEnv
		}
	}
	return AuthSettings{
		APIKeyEnv:           apiKeyEnv,
		CredentialFile:      cfg.CredentialFile,
		CodexAuthFile:       cfg.CodexAuthFile,
		CodexRefreshURL:     cfg.CodexRefreshURL,
		CodexAuthAPIBaseURL: cfg.CodexAuthAPIBaseURL,
		CodexClientID:       cfg.CodexClientID,
		CodexOriginator:     cfg.CodexOriginator,
	}
}

func (c Config) ProviderSelection() ProviderSelection {
	cfg := c
	cfg.ApplyModelProviderDefaults()
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if cfg.Provider == modelinfo.ProviderQwen {
		baseURL = modelinfo.Provider(modelinfo.ProviderQwen).BaseURL
	} else if !cfg.baseURLExplicitOverride && (baseURL == "" || knownProviderBaseURL(baseURL)) {
		if providerURL := modelinfo.Provider(cfg.Provider).BaseURL; providerURL != "" {
			baseURL = providerURL
		}
	}
	return ProviderSelection{
		Provider:     cfg.Provider,
		BaseURL:      baseURL,
		CodexBaseURL: cfg.CodexBaseURL,
	}
}

func knownProviderBaseURL(value string) bool {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	for _, provider := range modelinfo.Providers() {
		if provider.BaseURL != "" && value == strings.TrimRight(provider.BaseURL, "/") {
			return true
		}
	}
	return false
}

func knownProviderAPIKeyEnv(value string) bool {
	value = strings.TrimSpace(value)
	for _, provider := range modelinfo.Providers() {
		if provider.APIKeyEnv != "" && value == provider.APIKeyEnv {
			return true
		}
	}
	return false
}

func (c Config) ModelSelection() ModelSelection {
	cfg := c
	cfg.ApplyModelProviderDefaults()
	return ModelSelection{
		Model:           cfg.Model,
		Thinking:        cfg.Thinking,
		ReasoningEffort: cfg.ReasoningEffort,
		DisableSpark:    cfg.DisableSpark,
		MaxTokens:       cfg.MaxTokens,
	}
}

func (c Config) ProfileSelection() ProfileSelection {
	return ProfileSelection{Profile: NormalizeProfileName(c.Profile)}
}

func (c Config) RuntimeLimits() RuntimeLimits {
	return RuntimeLimits{
		MaxTokens:                     c.MaxTokens,
		MaxToolRounds:                 c.MaxToolRounds,
		MaxParallelTools:              c.MaxParallelTools,
		ProviderMaxRetries:            c.ProviderMaxRetries,
		MaxToolOutputBytes:            c.MaxToolOutputBytes,
		ContextWindowTokens:           c.ContextWindowTokens,
		ContextWindowSource:           c.ContextWindowSourceLabel(),
		ContextCompactTokens:          c.ContextCompactTokens,
		ContextCompactSource:          c.ContextCompactSourceLabel(),
		ContextCompactKeep:            c.ContextCompactKeep,
		ContextCompactMaxChars:        c.ContextCompactMaxChars,
		ContextCompactStrategy:        c.ContextCompactStrategy,
		ContextCompactSummaryProvider: c.ContextCompactSummaryProvider,
		ContextCompactSummaryModel:    c.ContextCompactSummaryModel,
		RequestTimeout:                c.RequestTimeout,
		StreamIdleTimeout:             c.StreamIdleTimeout,
	}
}

func (c Config) ToolPolicySettings() ToolPolicySettings {
	cfg := c
	cfg.ApplyModelProviderDefaults()
	cfg.ApplyWebSummaryDefaults()
	return ToolPolicySettings{
		WorkspaceRoots:            cloneStrings(cfg.WorkspaceRoots),
		ProjectDocMaxBytes:        cfg.ProjectDocMaxBytes,
		ProjectDocFallbacks:       cloneStrings(cfg.ProjectDocFallbacks),
		ProjectContextMaxBytes:    cfg.ProjectContextMaxBytes,
		MemoryEnabled:             cfg.MemoryEnabled,
		MemorySummaryMaxBytes:     cfg.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:       cfg.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:       cfg.MemoryTopicMaxBytes,
		MaxToolOutputBytes:        cfg.MaxToolOutputBytes,
		AutoApproveDangerous:      cfg.AutoApproveDangerous,
		AccessMode:                NormalizeAccessMode(cfg.AccessMode),
		StoreReasoningContent:     cfg.StoreReasoningContent,
		WebSummaryMode:            cfg.WebSummaryMode,
		WebSummaryProvider:        cfg.WebSummaryProvider,
		WebSummaryModel:           cfg.WebSummaryModel,
		WebSummaryMaxInputTokens:  cfg.WebSummaryMaxInputTokens,
		WebSummaryMaxOutputTokens: cfg.WebSummaryMaxOutputTokens,
		WebSummaryTimeout:         cfg.WebSummaryTimeout,
		WebCacheEnabled:           cfg.WebCacheEnabled,
		WebCacheTTL:               cfg.WebCacheTTL,
		WebCacheMaxBytes:          cfg.WebCacheMaxBytes,
		WebSearchBackend:          cfg.WebSearchBackend,
		WebExtractBackend:         cfg.WebExtractBackend,
		WebTavilyAPIKeyEnv:        cfg.WebTavilyAPIKeyEnv,
		WebExaAPIKeyEnv:           cfg.WebExaAPIKeyEnv,
		WebHermesEnvFiles:         cloneStrings(cfg.WebHermesEnvFiles),
	}
}

func (c Config) DiagnosticsSettings() DiagnosticsSettings {
	return DiagnosticsSettings{
		Enabled:     c.DiagnosticsEnabled,
		ConfigFiles: cloneStrings(c.DiagnosticsConfigFiles),
		Commands:    cloneDiagnosticCommands(c.DiagnosticsCommands),
	}
}

func (c Config) MCPSettings() MCPSettings {
	return MCPSettings{
		Enabled:                   c.MCPEnabled,
		ConfigFiles:               cloneStrings(c.MCPConfigFiles),
		AllowedServers:            cloneStrings(c.MCPAllowedServers),
		PromoteServerInstructions: c.MCPPromoteServerInstructions,
		Servers:                   cloneMCPServers(c.MCPServers),
	}
}

func LoadDefaultMCPSettings(settings MCPSettings) (MCPSettings, error) {
	cfg := Config{
		MCPEnabled:                   settings.Enabled,
		MCPConfigFiles:               cloneStrings(settings.ConfigFiles),
		MCPAllowedServers:            cloneStrings(settings.AllowedServers),
		MCPPromoteServerInstructions: settings.PromoteServerInstructions,
		MCPServers:                   cloneMCPServers(settings.Servers),
	}
	if err := cfg.LoadDefaultMCPServers(); err != nil {
		return MCPSettings{}, err
	}
	return cfg.MCPSettings(), nil
}

func (c Config) HookSettings() HookSettings {
	return HookSettings{
		Enabled:     c.HooksEnabled,
		ConfigFiles: cloneStrings(c.HookConfigFiles),
		Hooks:       cloneHooks(c.Hooks),
	}
}

func (c Config) AgentClubSettings() AgentClubSettings {
	return AgentClubSettings{
		ConfigFiles: cloneStrings(c.AgentClubConfigFiles),
	}
}

func (c Config) InstructionSettings() InstructionSettings {
	var profile ProfileSelection
	if strings.TrimSpace(c.Profile) != "" {
		profile = c.ProfileSelection()
	}
	return InstructionSettings{
		Profile:                profile,
		WorkspaceRoots:         cloneStrings(c.WorkspaceRoots),
		ProjectDocMaxBytes:     c.ProjectDocMaxBytes,
		ProjectDocFallbacks:    cloneStrings(c.ProjectDocFallbacks),
		ProjectContextMaxBytes: c.ProjectContextMaxBytes,
		MemoryEnabled:          c.MemoryEnabled,
		MemorySummaryMaxBytes:  c.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:    c.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:    c.MemoryTopicMaxBytes,
	}
}

func (c Config) ProviderBinding() ProviderBinding {
	c.ApplyModelProviderDefaults()
	return ProviderBinding{
		Provider: c.ProviderSelection(),
		Model:    c.ModelSelection(),
		Auth:     c.AuthSettings(),
		Limits:   c.RuntimeLimits(),
	}
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneMCPServers(in []MCPServer) []MCPServer {
	if len(in) == 0 {
		return nil
	}
	out := make([]MCPServer, 0, len(in))
	for _, server := range in {
		server.Args = cloneStrings(server.Args)
		server.Env = cloneStringMap(server.Env)
		server.EnvVars = cloneStrings(server.EnvVars)
		server.HTTPHeaders = cloneStringMap(server.HTTPHeaders)
		server.EnvHTTPHeaders = cloneStringMap(server.EnvHTTPHeaders)
		server.EnabledTools = cloneStrings(server.EnabledTools)
		server.DisabledTools = cloneStrings(server.DisabledTools)
		server.ToolRisks = cloneStringMap(server.ToolRisks)
		out = append(out, server)
	}
	return out
}

func cloneDiagnosticCommands(in []DiagnosticCommand) []DiagnosticCommand {
	if len(in) == 0 {
		return nil
	}
	out := make([]DiagnosticCommand, 0, len(in))
	for _, command := range in {
		command.Args = cloneStrings(command.Args)
		out = append(out, command)
	}
	return out
}

func cloneDiagnosticsSettings(settings DiagnosticsSettings) DiagnosticsSettings {
	return DiagnosticsSettings{
		Enabled:     settings.Enabled,
		ConfigFiles: cloneStrings(settings.ConfigFiles),
		Commands:    cloneDiagnosticCommands(settings.Commands),
	}
}

func cloneHooks(in []Hook) []Hook {
	if len(in) == 0 {
		return nil
	}
	out := make([]Hook, 0, len(in))
	for _, hook := range in {
		hook.Args = cloneStrings(hook.Args)
		hook.Env = cloneStringMap(hook.Env)
		hook.EnvVars = cloneStrings(hook.EnvVars)
		out = append(out, hook)
	}
	return out
}
