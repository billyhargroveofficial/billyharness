package config

import (
	"fmt"
	"reflect"
	"strings"
)

type RuntimeDiffSettings struct {
	Provider    ProviderBinding
	Profile     ProfileSelection
	Runtime     RuntimeLimits
	ToolPolicy  ToolPolicySettings
	MCP         MCPSettings
	Hooks       HookSettings
	GatewayAddr string
}

type RunOverrideSettings struct {
	Provider        string
	Model           string
	Profile         string
	Thinking        string
	ReasoningEffort string
	MaxToolRounds   int
	AccessMode      string
}

func RuntimeDiffSettingsFromConfig(cfg Config) RuntimeDiffSettings {
	return RuntimeDiffSettings{
		Provider:    cfg.ProviderBinding(),
		Profile:     cfg.ProfileSelection(),
		Runtime:     cfg.RuntimeLimits(),
		ToolPolicy:  cfg.ToolPolicySettings(),
		MCP:         cfg.MCPSettings(),
		Hooks:       cfg.HookSettings(),
		GatewayAddr: cfg.GatewayAddr,
	}
}

func RuntimeDiffSettingsWithRunOverrides(settings RuntimeDiffSettings, overrides RunOverrideSettings) (RuntimeDiffSettings, error) {
	cfg := runtimeDiffConfigFromSettings(builtInConfig(), settings)
	if strings.TrimSpace(overrides.Profile) != "" {
		cfg.Profile = NormalizeProfileName(overrides.Profile)
		if err := cfg.ApplyProfileMetadata(); err != nil {
			return RuntimeDiffSettings{}, err
		}
	}
	if overrides.Provider != "" {
		cfg.Provider = overrides.Provider
	}
	if overrides.Model != "" {
		cfg.Model = overrides.Model
	}
	cfg.ApplyModelProviderDefaults()
	if overrides.ReasoningEffort != "" {
		cfg.ReasoningEffort = overrides.ReasoningEffort
	}
	if overrides.Thinking != "" {
		cfg.Thinking = overrides.Thinking
	}
	if overrides.MaxToolRounds > 0 {
		cfg.MaxToolRounds = overrides.MaxToolRounds
	}
	if strings.TrimSpace(overrides.AccessMode) != "" {
		mode, ok := ParseAccessMode(overrides.AccessMode)
		if !ok {
			return RuntimeDiffSettings{}, fmt.Errorf("unsupported access_mode %q", overrides.AccessMode)
		}
		cfg.AccessMode = mode
	}
	return RuntimeDiffSettingsFromConfig(cfg), nil
}

func RuntimeOverridesFromConfig(cfg Config, source string) []ResolveOverride {
	if strings.TrimSpace(source) == "" {
		source = SourceGateway
	}
	var out []ResolveOverride
	for _, spec := range configSpecs() {
		out = append(out, ResolveOverride{
			Key:       spec.Key,
			Value:     spec.get(cfg),
			Source:    source,
			SourceKey: spec.Key,
		})
	}
	return out
}

func RuntimeDiffOverrides(base, current Config, source string) []ResolveOverride {
	if strings.TrimSpace(source) == "" {
		source = SourceGateway
	}
	var out []ResolveOverride
	for _, spec := range configSpecs() {
		if reflect.DeepEqual(spec.get(base), spec.get(current)) {
			continue
		}
		out = append(out, ResolveOverride{
			Key:       spec.Key,
			Value:     spec.get(current),
			Source:    source,
			SourceKey: spec.Key,
		})
	}
	return out
}

func RuntimeDiffOverridesFromSettings(base Config, settings RuntimeDiffSettings, source string) []ResolveOverride {
	return RuntimeDiffOverrides(base, runtimeDiffConfigFromSettings(base, settings), source)
}

func runtimeDiffConfigFromSettings(base Config, settings RuntimeDiffSettings) Config {
	cfg := base
	cfg.Provider = settings.Provider.Provider.Provider
	cfg.Model = settings.Provider.Model.Model
	cfg.Profile = settings.Profile.Profile
	cfg.BaseURL = settings.Provider.Provider.BaseURL
	cfg.APIKeyEnv = settings.Provider.Auth.APIKeyEnv
	cfg.CredentialFile = settings.Provider.Auth.CredentialFile
	cfg.CodexBaseURL = settings.Provider.Provider.CodexBaseURL
	cfg.CodexAuthFile = settings.Provider.Auth.CodexAuthFile
	cfg.CodexRefreshURL = settings.Provider.Auth.CodexRefreshURL
	cfg.CodexAuthAPIBaseURL = settings.Provider.Auth.CodexAuthAPIBaseURL
	cfg.CodexClientID = settings.Provider.Auth.CodexClientID
	cfg.CodexOriginator = settings.Provider.Auth.CodexOriginator
	cfg.Thinking = settings.Provider.Model.Thinking
	cfg.ReasoningEffort = settings.Provider.Model.ReasoningEffort
	cfg.DisableSpark = settings.Provider.Model.DisableSpark
	cfg.MaxTokens = settings.Runtime.MaxTokens
	cfg.MaxToolRounds = settings.Runtime.MaxToolRounds
	cfg.MaxParallelTools = settings.Runtime.MaxParallelTools
	cfg.ProviderMaxRetries = settings.Runtime.ProviderMaxRetries
	cfg.ContextWindowTokens = settings.Runtime.ContextWindowTokens
	cfg.ContextCompactTokens = settings.Runtime.ContextCompactTokens
	cfg.ContextCompactKeep = settings.Runtime.ContextCompactKeep
	cfg.ContextCompactMaxChars = settings.Runtime.ContextCompactMaxChars
	cfg.ContextCompactStrategy = settings.Runtime.ContextCompactStrategy
	cfg.ContextCompactSummaryProvider = settings.Runtime.ContextCompactSummaryProvider
	cfg.ContextCompactSummaryModel = settings.Runtime.ContextCompactSummaryModel
	cfg.WebSummaryMode = settings.ToolPolicy.WebSummaryMode
	cfg.WebSummaryProvider = settings.ToolPolicy.WebSummaryProvider
	cfg.WebSummaryModel = settings.ToolPolicy.WebSummaryModel
	cfg.WebSummaryMaxInputTokens = settings.ToolPolicy.WebSummaryMaxInputTokens
	cfg.WebSummaryMaxOutputTokens = settings.ToolPolicy.WebSummaryMaxOutputTokens
	cfg.WebSummaryTimeout = settings.ToolPolicy.WebSummaryTimeout
	cfg.WebCacheEnabled = settings.ToolPolicy.WebCacheEnabled
	cfg.WebCacheTTL = settings.ToolPolicy.WebCacheTTL
	cfg.WebCacheMaxBytes = settings.ToolPolicy.WebCacheMaxBytes
	cfg.RequestTimeout = settings.Runtime.RequestTimeout
	cfg.StreamIdleTimeout = settings.Runtime.StreamIdleTimeout
	cfg.WorkspaceRoots = cloneStrings(settings.ToolPolicy.WorkspaceRoots)
	cfg.ProjectDocMaxBytes = settings.ToolPolicy.ProjectDocMaxBytes
	cfg.ProjectDocFallbacks = cloneStrings(settings.ToolPolicy.ProjectDocFallbacks)
	cfg.MaxToolOutputBytes = settings.ToolPolicy.MaxToolOutputBytes
	cfg.AutoApproveDangerous = settings.ToolPolicy.AutoApproveDangerous
	cfg.AccessMode = NormalizeAccessMode(settings.ToolPolicy.AccessMode)
	cfg.StoreReasoningContent = settings.ToolPolicy.StoreReasoningContent
	cfg.GatewayAddr = settings.GatewayAddr
	cfg.MCPEnabled = settings.MCP.Enabled
	cfg.MCPConfigFiles = cloneStrings(settings.MCP.ConfigFiles)
	cfg.MCPAllowedServers = cloneStrings(settings.MCP.AllowedServers)
	cfg.MCPServers = cloneMCPServers(settings.MCP.Servers)
	cfg.HooksEnabled = settings.Hooks.Enabled
	cfg.HookConfigFiles = cloneStrings(settings.Hooks.ConfigFiles)
	cfg.Hooks = cloneHooks(settings.Hooks.Hooks)
	return cfg
}
