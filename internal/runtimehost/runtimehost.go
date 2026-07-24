package runtimehost

import (
	"context"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Settings struct {
	ProviderAuth    config.ProviderAuthSnapshot
	ProviderBinding config.ProviderBinding
	ProviderCaps    config.ProviderCapabilitySnapshot
	Profile         config.ProfileSelection
	Runtime         config.RuntimeLimits
	ToolPolicy      config.ToolPolicySettings
	Diagnostics     config.DiagnosticsSettings
	MCP             config.MCPSettings
	Hooks           config.HookSettings
	Instructions    config.InstructionSettings
	GatewayAddr     string
	Auth            config.AuthSettings
}

type Host struct {
	Settings Settings
	Provider provider.Provider
	Registry *tools.Registry
}

type Option func(*options)

type options struct {
	provider provider.Provider
	registry *tools.Registry
	noMCP    bool
}

type AgentOption func(*agent.Settings)

func SettingsFromConfig(cfg config.Config) Settings {
	return Settings{
		ProviderAuth:    cfg.ProviderAuthSnapshot(),
		ProviderBinding: cfg.ProviderBinding(),
		ProviderCaps:    cfg.ProviderCapabilitySnapshot(),
		Profile:         cfg.ProfileSelection(),
		Runtime:         cfg.RuntimeLimits(),
		ToolPolicy:      cfg.ToolPolicySettings(),
		Diagnostics:     cfg.DiagnosticsSettings(),
		MCP:             cfg.MCPSettings(),
		Hooks:           cfg.HookSettings(),
		Instructions:    cfg.InstructionSettings(),
		GatewayAddr:     cfg.GatewayAddr,
		Auth:            cfg.AuthSettings(),
	}
}

func SettingsFromRuntimeDiffSettings(settings config.RuntimeDiffSettings) Settings {
	hostSettings := Settings{
		ProviderBinding: settings.Provider,
		ProviderCaps:    providerCapsFromBinding(settings.Provider),
		Profile:         settings.Profile,
		Runtime:         settings.Runtime,
		ToolPolicy:      cloneToolPolicy(settings.ToolPolicy),
		Diagnostics:     cloneDiagnostics(settings.Diagnostics),
		MCP:             cloneMCP(settings.MCP),
		Hooks:           cloneHooks(settings.Hooks),
		Instructions:    InstructionSettingsFromRuntimeDiffSettings(settings),
		GatewayAddr:     settings.GatewayAddr,
		Auth:            settings.Provider.Auth,
	}
	hostSettings.ProviderAuth = providerAuthFromSettings(hostSettings)
	return hostSettings
}

func InstructionSettingsFromRuntimeDiffSettings(settings config.RuntimeDiffSettings) config.InstructionSettings {
	return config.InstructionSettings{
		Profile:                settings.Profile,
		WorkspaceRoots:         cloneStrings(settings.ToolPolicy.WorkspaceRoots),
		ProjectDocMaxBytes:     settings.ToolPolicy.ProjectDocMaxBytes,
		ProjectDocFallbacks:    cloneStrings(settings.ToolPolicy.ProjectDocFallbacks),
		ProjectContextMaxBytes: settings.ToolPolicy.ProjectContextMaxBytes,
		MemoryEnabled:          settings.ToolPolicy.MemoryEnabled,
		MemorySummaryMaxBytes:  settings.ToolPolicy.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:    settings.ToolPolicy.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:    settings.ToolPolicy.MemoryTopicMaxBytes,
	}
}

func New(ctx context.Context, cfg config.Config, opts ...Option) (*Host, error) {
	return NewFromSettings(ctx, SettingsFromConfig(cfg), opts...)
}

func NewFromSettings(ctx context.Context, settings Settings, opts ...Option) (*Host, error) {
	settings = settings.Clone()
	var options options
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	prov := options.provider
	if prov == nil {
		var err error
		prov, err = NewProvider(settings)
		if err != nil {
			return nil, err
		}
	}
	registry := options.registry
	if registry == nil {
		var err error
		if options.noMCP {
			registry = NewRegistryNoMCP(settings)
		} else {
			registry, err = NewRegistry(ctx, settings)
			if err != nil {
				return nil, err
			}
		}
	}
	return &Host{Settings: settings, Provider: prov, Registry: registry}, nil
}

func WithProvider(prov provider.Provider) Option {
	return func(opts *options) {
		opts.provider = prov
	}
}

func WithRegistry(registry *tools.Registry) Option {
	return func(opts *options) {
		opts.registry = registry
	}
}

func WithoutMCP() Option {
	return func(opts *options) {
		opts.noMCP = true
	}
}

func (h *Host) Agent(opts ...AgentOption) *agent.Agent {
	if h == nil {
		return nil
	}
	return NewAgent(h.Settings, h.Provider, h.Registry, opts...)
}

func (h *Host) Close() {
	if h != nil && h.Registry != nil {
		h.Registry.Close()
	}
}

func NewProvider(settings Settings) (provider.Provider, error) {
	return provider.NewFromBinding(settings.ProviderBinding)
}

func NewAgent(settings Settings, prov provider.Provider, registry *tools.Registry, opts ...AgentOption) *agent.Agent {
	agentSettings := settings.AgentSettings()
	for _, opt := range opts {
		if opt != nil {
			opt(&agentSettings)
		}
	}
	return agent.NewFromSettings(agentSettings, prov, registry)
}

func WithAskUser(handler agent.AskUserHandler) AgentOption {
	return func(settings *agent.Settings) {
		settings.AskUser = handler
	}
}

func WithRunCapabilities(capabilities tools.RunCapabilities) AgentOption {
	return func(settings *agent.Settings) {
		settings.RunCapabilities = capabilities.Clone()
	}
}

func WithContextMode(mode string) AgentOption {
	return func(settings *agent.Settings) {
		settings.ContextMode = mode
	}
}

func NewRegistry(ctx context.Context, settings Settings) (*tools.Registry, error) {
	return tools.NewRegistryWithMCPFromSettings(ctx, settings.RegistrySettings(), registryOptions(settings)...)
}

func NewRegistryNoMCP(settings Settings) *tools.Registry {
	return tools.NewRegistryFromSettings(settings.RegistrySettings(), registryOptions(settings)...)
}

func MCPStatus(ctx context.Context, settings Settings) (mcpstatus.Response, error) {
	registry, err := NewRegistry(ctx, settings)
	if err != nil {
		return mcpstatus.Response{}, err
	}
	defer registry.Close()
	mcpSettings := registry.MCPSettings()
	return mcpstatus.Response{
		Source:       "runtime config",
		ConfigFiles:  mcpSettings.ConfigFiles,
		Allowed:      mcpSettings.AllowedServers,
		Enabled:      mcpSettings.Enabled,
		Servers:      registry.MCPStatuses(),
		Prompts:      registry.MCPPrompts(),
		Instructions: registry.MCPServerInstructions(),
	}, nil
}

func InitialMessages(settings config.InstructionSettings) []protocol.Message {
	return agent.InitialMessagesFromSettings(settings)
}

func PromptSubmitOptions(source string, metadata map[string]string) agent.PromptSubmitOptions {
	return agent.PromptSubmitOptions{Source: source, Metadata: metadata}
}

func (s Settings) AgentSettings() agent.Settings {
	return agent.Settings{
		ProviderBinding: s.ProviderBinding,
		Profile:         s.Profile,
		Runtime:         s.Runtime,
		ToolPolicy:      s.ToolPolicy,
		MCP:             s.MCP,
		Hooks:           s.Hooks,
		Instructions:    s.Instructions,
	}
}

func (s Settings) RegistrySettings() tools.RegistrySettings {
	return tools.RegistrySettings{
		Provider:    s.ProviderBinding,
		Profile:     s.Profile,
		ToolPolicy:  s.ToolPolicy,
		Diagnostics: s.Diagnostics,
		MCP:         s.MCP,
	}
}

func (s Settings) RuntimeDiffSettings() config.RuntimeDiffSettings {
	return config.RuntimeDiffSettings{
		Provider:    s.ProviderBinding,
		Profile:     s.Profile,
		Runtime:     s.Runtime,
		ToolPolicy:  s.ToolPolicy,
		Diagnostics: s.Diagnostics,
		MCP:         s.MCP,
		Hooks:       s.Hooks,
		GatewayAddr: s.GatewayAddr,
	}
}

func (s Settings) Clone() Settings {
	s.ToolPolicy = cloneToolPolicy(s.ToolPolicy)
	s.Diagnostics = cloneDiagnostics(s.Diagnostics)
	s.MCP = cloneMCP(s.MCP)
	s.Hooks = cloneHooks(s.Hooks)
	s.Instructions = cloneInstructions(s.Instructions)
	return s
}

func registryOptions(settings Settings) []tools.RegistryOption {
	return []tools.RegistryOption{
		tools.WithWebSummarizer(provider.NewWebSummarizerFromProjections(settings.ProviderBinding, settings.ToolPolicy)),
	}
}

func providerCapsFromBinding(binding config.ProviderBinding) config.ProviderCapabilitySnapshot {
	providerID := modelinfo.ProviderForModel(binding.Model.Model, binding.Provider.Provider)
	info := modelinfo.Lookup(binding.Model.Model)
	out := config.ProviderCapabilitySnapshot{
		Provider:              providerID,
		Model:                 info.Model,
		Known:                 info.Known,
		ContextWindowTokens:   info.ContextWindowTokens,
		MaxOutputTokens:       info.MaxOutputTokens,
		ToolCalls:             info.ToolCalls,
		ParallelToolCalls:     info.ParallelToolCalls,
		Streaming:             info.Streaming,
		Reasoning:             info.Reasoning,
		ReasoningModes:        cloneStrings(info.ReasoningModes),
		TokenAccountingFields: cloneStrings(info.TokenAccountingFields),
		CacheAccountingFields: cloneStrings(info.CacheAccountingFields),
		WebSummaryModel:       info.HelperModels.WebSummary,
		MemoryHelperModel:     info.HelperModels.Memory,
		CostMode:              info.CostMode,
		Subscription:          info.Subscription,
	}
	if out.Model == "" {
		out.Model = modelinfo.NormalizeAlias(binding.Model.Model)
	}
	if err := modelinfo.ValidateCapabilityPolicy(modelinfo.CapabilityPolicyRequest{
		Provider:           providerID,
		Model:              binding.Model.Model,
		Thinking:           binding.Model.Thinking,
		ReasoningEffort:    binding.Model.ReasoningEffort,
		MaxOutputTokens:    binding.Model.MaxTokens,
		RequireStreaming:   true,
		RequireToolCalls:   providerID != modelinfo.ProviderMock,
		RequireParallel:    binding.Limits.MaxParallelTools > 1 && providerID != modelinfo.ProviderMock,
		AllowUnknownModels: modelinfo.Provider(providerID).Custom || providerID == modelinfo.ProviderMock,
	}); err != nil {
		out.ValidationError = err.Error()
	}
	return out
}

func providerAuthFromSettings(settings Settings) config.ProviderAuthSnapshot {
	return config.ProviderAuthSnapshot{
		Provider:            settings.ProviderBinding.Provider.Provider,
		Model:               settings.ProviderBinding.Model.Model,
		Profile:             settings.Profile.Profile,
		Thinking:            settings.ProviderBinding.Model.Thinking,
		ReasoningEffort:     settings.ProviderBinding.Model.ReasoningEffort,
		DisableSpark:        settings.ProviderBinding.Model.DisableSpark,
		BaseURL:             settings.ProviderBinding.Provider.BaseURL,
		APIKeyEnv:           settings.ProviderBinding.Auth.APIKeyEnv,
		CredentialFile:      settings.ProviderBinding.Auth.CredentialFile,
		CodexBaseURL:        settings.ProviderBinding.Provider.CodexBaseURL,
		CodexAuthFile:       settings.ProviderBinding.Auth.CodexAuthFile,
		CodexRefreshURL:     settings.ProviderBinding.Auth.CodexRefreshURL,
		CodexAuthAPIBaseURL: settings.ProviderBinding.Auth.CodexAuthAPIBaseURL,
		CodexClientID:       settings.ProviderBinding.Auth.CodexClientID,
		CodexOriginator:     settings.ProviderBinding.Auth.CodexOriginator,
	}
}

func cloneToolPolicy(settings config.ToolPolicySettings) config.ToolPolicySettings {
	settings.WorkspaceRoots = cloneStrings(settings.WorkspaceRoots)
	settings.ProjectDocFallbacks = cloneStrings(settings.ProjectDocFallbacks)
	settings.WebHermesEnvFiles = cloneStrings(settings.WebHermesEnvFiles)
	return settings
}

func cloneDiagnostics(settings config.DiagnosticsSettings) config.DiagnosticsSettings {
	return config.Config{
		DiagnosticsEnabled:     settings.Enabled,
		DiagnosticsConfigFiles: settings.ConfigFiles,
		DiagnosticsCommands:    settings.Commands,
	}.DiagnosticsSettings()
}

func cloneMCP(settings config.MCPSettings) config.MCPSettings {
	return config.Config{
		MCPEnabled:                   settings.Enabled,
		MCPConfigFiles:               settings.ConfigFiles,
		MCPAllowedServers:            settings.AllowedServers,
		MCPPromoteServerInstructions: settings.PromoteServerInstructions,
		MCPServers:                   settings.Servers,
	}.MCPSettings()
}

func cloneHooks(settings config.HookSettings) config.HookSettings {
	return config.Config{
		HooksEnabled:    settings.Enabled,
		HookConfigFiles: settings.ConfigFiles,
		Hooks:           settings.Hooks,
	}.HookSettings()
}

func cloneInstructions(settings config.InstructionSettings) config.InstructionSettings {
	settings.WorkspaceRoots = cloneStrings(settings.WorkspaceRoots)
	settings.ProjectDocFallbacks = cloneStrings(settings.ProjectDocFallbacks)
	return settings
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
