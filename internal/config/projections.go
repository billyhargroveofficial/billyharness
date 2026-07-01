package config

import (
	"strings"
	"time"
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
	MaxParallelTools              int
	ProviderMaxRetries            int
	MaxToolOutputBytes            int
	ContextWindowTokens           int64
	ContextCompactTokens          int
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
}

type DiagnosticsSettings struct {
	Enabled     bool
	ConfigFiles []string
	Commands    []DiagnosticCommand
}

type MCPSettings struct {
	Enabled        bool
	ConfigFiles    []string
	AllowedServers []string
	Servers        []MCPServer
}

type HookSettings struct {
	Enabled     bool
	ConfigFiles []string
	Hooks       []Hook
}

type InstructionSettings struct {
	Profile             ProfileSelection
	WorkspaceRoots      []string
	ProjectDocMaxBytes  int
	ProjectDocFallbacks []string
}

type ProviderBinding struct {
	Provider ProviderSelection
	Model    ModelSelection
	Auth     AuthSettings
	Limits   RuntimeLimits
}

func (c Config) AuthSettings() AuthSettings {
	return AuthSettings{
		APIKeyEnv:           c.APIKeyEnv,
		CredentialFile:      c.CredentialFile,
		CodexAuthFile:       c.CodexAuthFile,
		CodexRefreshURL:     c.CodexRefreshURL,
		CodexAuthAPIBaseURL: c.CodexAuthAPIBaseURL,
		CodexClientID:       c.CodexClientID,
		CodexOriginator:     c.CodexOriginator,
	}
}

func (c Config) ProviderSelection() ProviderSelection {
	cfg := c
	cfg.ApplyModelProviderDefaults()
	return ProviderSelection{
		Provider:     cfg.Provider,
		BaseURL:      cfg.BaseURL,
		CodexBaseURL: cfg.CodexBaseURL,
	}
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
		ContextCompactTokens:          c.ContextCompactTokens,
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
		Enabled:        c.MCPEnabled,
		ConfigFiles:    cloneStrings(c.MCPConfigFiles),
		AllowedServers: cloneStrings(c.MCPAllowedServers),
		Servers:        cloneMCPServers(c.MCPServers),
	}
}

func LoadDefaultMCPSettings(settings MCPSettings) (MCPSettings, error) {
	cfg := Config{
		MCPEnabled:        settings.Enabled,
		MCPConfigFiles:    cloneStrings(settings.ConfigFiles),
		MCPAllowedServers: cloneStrings(settings.AllowedServers),
		MCPServers:        cloneMCPServers(settings.Servers),
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

func (c Config) InstructionSettings() InstructionSettings {
	var profile ProfileSelection
	if strings.TrimSpace(c.Profile) != "" {
		profile = c.ProfileSelection()
	}
	return InstructionSettings{
		Profile:             profile,
		WorkspaceRoots:      cloneStrings(c.WorkspaceRoots),
		ProjectDocMaxBytes:  c.ProjectDocMaxBytes,
		ProjectDocFallbacks: cloneStrings(c.ProjectDocFallbacks),
	}
}

func (c Config) ProviderBinding() ProviderBinding {
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
