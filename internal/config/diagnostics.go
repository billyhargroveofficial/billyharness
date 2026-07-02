package config

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

type DiagnosticSnapshot struct {
	ProviderAuth       ProviderAuthSnapshot       `json:"provider_auth"`
	ProviderCapability ProviderCapabilitySnapshot `json:"provider_capability"`
	RuntimeTool        RuntimeToolSnapshot        `json:"runtime_tool"`
}

type DiagnosticSource struct {
	Source     string `json:"source,omitempty"`
	SourceKey  string `json:"source_key,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
	Warning    string `json:"warning,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ProviderAuthSnapshot struct {
	Provider              string            `json:"provider"`
	ProviderSource        *DiagnosticSource `json:"provider_source,omitempty"`
	Model                 string            `json:"model"`
	ModelSource           *DiagnosticSource `json:"model_source,omitempty"`
	Profile               string            `json:"profile"`
	ProfileSource         *DiagnosticSource `json:"profile_source,omitempty"`
	Thinking              string            `json:"thinking"`
	ReasoningEffort       string            `json:"reasoning_effort"`
	ReasoningEffortSource *DiagnosticSource `json:"reasoning_effort_source,omitempty"`
	DisableSpark          bool              `json:"disable_spark"`
	BaseURL               string            `json:"base_url,omitempty"`
	APIKeyEnv             string            `json:"api_key_env"`
	CredentialFile        string            `json:"credential_file"`
	CodexBaseURL          string            `json:"codex_base_url,omitempty"`
	CodexAuthFile         string            `json:"codex_auth_file"`
	CodexRefreshURL       string            `json:"codex_refresh_url,omitempty"`
	CodexAuthAPIBaseURL   string            `json:"codex_auth_api_base_url,omitempty"`
	CodexClientID         string            `json:"codex_client_id,omitempty"`
	CodexOriginator       string            `json:"codex_originator,omitempty"`
}

type RuntimeToolSnapshot struct {
	ContextWindowTokens                 int64             `json:"context_window_tokens"`
	ContextWindowTokensSource           *DiagnosticSource `json:"context_window_tokens_source,omitempty"`
	ContextCompactTokens                int               `json:"context_compact_tokens"`
	ContextCompactTokensSource          *DiagnosticSource `json:"context_compact_tokens_source,omitempty"`
	ContextCompactStrategy              string            `json:"context_compact_strategy"`
	ContextCompactStrategySource        *DiagnosticSource `json:"context_compact_strategy_source,omitempty"`
	ContextCompactSummaryProvider       string            `json:"context_compact_summary_provider"`
	ContextCompactSummaryProviderSource *DiagnosticSource `json:"context_compact_summary_provider_source,omitempty"`
	ContextCompactSummaryModel          string            `json:"context_compact_summary_model"`
	ContextCompactSummaryModelSource    *DiagnosticSource `json:"context_compact_summary_model_source,omitempty"`
	WebSummaryMode                      string            `json:"web_summary_mode"`
	WebSummaryModeSource                *DiagnosticSource `json:"web_summary_mode_source,omitempty"`
	WebSummaryProvider                  string            `json:"web_summary_provider"`
	WebSummaryProviderSource            *DiagnosticSource `json:"web_summary_provider_source,omitempty"`
	WebSummaryModel                     string            `json:"web_summary_model"`
	WebSummaryModelSource               *DiagnosticSource `json:"web_summary_model_source,omitempty"`
	WebCacheEnabled                     bool              `json:"web_cache_enabled"`
	WebCacheTTLMS                       int64             `json:"web_cache_ttl_ms"`
	WebCacheMaxBytes                    int64             `json:"web_cache_max_bytes"`
	WebSearchBackend                    string            `json:"web_search_backend"`
	WebSearchBackendSource              *DiagnosticSource `json:"web_search_backend_source,omitempty"`
	WebExtractBackend                   string            `json:"web_extract_backend"`
	WebExtractBackendSource             *DiagnosticSource `json:"web_extract_backend_source,omitempty"`
	WebTavilyAPIKeyEnv                  string            `json:"web_tavily_api_key_env"`
	WebTavilyAPIKeyPresent              bool              `json:"web_tavily_api_key_present"`
	WebTavilyAPIKeySource               string            `json:"web_tavily_api_key_source,omitempty"`
	WebExaAPIKeyEnv                     string            `json:"web_exa_api_key_env"`
	WebExaAPIKeyPresent                 bool              `json:"web_exa_api_key_present"`
	WebExaAPIKeySource                  string            `json:"web_exa_api_key_source,omitempty"`
	WebHermesEnvFileCount               int               `json:"web_hermes_env_file_count"`
	MaxToolRounds                       int               `json:"max_tool_rounds"`
	MaxParallelTools                    int               `json:"max_parallel_tools"`
	GatewayAddr                         string            `json:"gateway_addr"`
	MemoryEnabled                       bool              `json:"memory_enabled"`
	MemoryAutoExtractEnabled            bool              `json:"memory_auto_extract_enabled"`
	MemorySummaryMaxBytes               int               `json:"memory_summary_max_bytes"`
	MemoryIndexMaxBytes                 int               `json:"memory_index_max_bytes"`
	MemoryTopicMaxBytes                 int               `json:"memory_topic_max_bytes"`
	AutoApproveDangerous                bool              `json:"auto_approve_dangerous"`
	AccessMode                          string            `json:"access_mode"`
	MCPEnabled                          bool              `json:"mcp_enabled"`
	MCPAllowedServers                   string            `json:"mcp_allowed_servers"`
	MaxToolOutputBytes                  int               `json:"max_tool_output_bytes"`
	DiagnosticsEnabled                  bool              `json:"diagnostics_enabled"`
	DiagnosticsConfigFiles              string            `json:"diagnostics_config_files,omitempty"`
	DiagnosticsCommandCount             int               `json:"diagnostics_command_count"`
	StoreReasoningContent               bool              `json:"store_reasoning_content"`
}

type ProviderCapabilitySnapshot struct {
	Provider              string   `json:"provider"`
	Model                 string   `json:"model"`
	Known                 bool     `json:"known"`
	ContextWindowTokens   int64    `json:"context_window_tokens,omitempty"`
	MaxOutputTokens       int      `json:"max_output_tokens,omitempty"`
	ToolCalls             bool     `json:"tool_calls"`
	ParallelToolCalls     bool     `json:"parallel_tool_calls"`
	Streaming             bool     `json:"streaming"`
	Reasoning             bool     `json:"reasoning"`
	ReasoningModes        []string `json:"reasoning_modes,omitempty"`
	TokenAccountingFields []string `json:"token_accounting_fields,omitempty"`
	CacheAccountingFields []string `json:"cache_accounting_fields,omitempty"`
	WebSummaryModel       string   `json:"web_summary_model,omitempty"`
	MemoryHelperModel     string   `json:"memory_helper_model,omitempty"`
	CostMode              string   `json:"cost_mode,omitempty"`
	Subscription          bool     `json:"subscription"`
	ValidationError       string   `json:"validation_error,omitempty"`
}

func (c Config) DiagnosticSnapshot() DiagnosticSnapshot {
	return DiagnosticSnapshot{
		ProviderAuth:       c.ProviderAuthSnapshot(),
		ProviderCapability: c.ProviderCapabilitySnapshot(),
		RuntimeTool:        c.RuntimeToolSnapshot(),
	}
}

func (r ResolvedConfig) DiagnosticSnapshot() DiagnosticSnapshot {
	snapshot := r.Config.DiagnosticSnapshot()
	snapshot.ProviderAuth.ProviderSource = r.diagnosticSource("provider")
	snapshot.ProviderAuth.ModelSource = r.diagnosticSource("model")
	snapshot.ProviderAuth.ProfileSource = r.diagnosticSource("profile")
	snapshot.ProviderAuth.ReasoningEffortSource = r.diagnosticSource("reasoning_effort")
	snapshot.RuntimeTool.ContextWindowTokensSource = r.diagnosticSource("context_window_tokens")
	snapshot.RuntimeTool.ContextCompactTokensSource = r.diagnosticSource("context_compact_tokens")
	snapshot.RuntimeTool.ContextCompactStrategySource = r.diagnosticSource("context_compact_strategy")
	snapshot.RuntimeTool.ContextCompactSummaryProviderSource = r.diagnosticSource("context_compact_summary_provider")
	snapshot.RuntimeTool.ContextCompactSummaryModelSource = r.diagnosticSource("context_compact_summary_model")
	snapshot.RuntimeTool.WebSummaryModeSource = r.diagnosticSource("web_summary_mode")
	snapshot.RuntimeTool.WebSummaryProviderSource = r.diagnosticSource("web_summary_provider")
	snapshot.RuntimeTool.WebSummaryModelSource = r.diagnosticSource("web_summary_model")
	snapshot.RuntimeTool.WebSearchBackendSource = r.diagnosticSource("web_search_backend")
	snapshot.RuntimeTool.WebExtractBackendSource = r.diagnosticSource("web_extract_backend")
	return snapshot
}

func (r ResolvedConfig) diagnosticSource(key string) *DiagnosticSource {
	value, ok := r.Value(key)
	if !ok {
		return nil
	}
	return &DiagnosticSource{
		Source:     value.Source,
		SourceKey:  value.SourceKey,
		SourcePath: value.SourcePath,
		Warning:    value.Warning,
		Error:      value.Error,
	}
}

func (c Config) ProviderAuthSnapshot() ProviderAuthSnapshot {
	binding := c.ProviderBinding()
	profile := c.ProfileSelection()
	return ProviderAuthSnapshot{
		Provider:            binding.Provider.Provider,
		Model:               binding.Model.Model,
		Profile:             profile.Profile,
		Thinking:            binding.Model.Thinking,
		ReasoningEffort:     binding.Model.ReasoningEffort,
		DisableSpark:        binding.Model.DisableSpark,
		BaseURL:             binding.Provider.BaseURL,
		APIKeyEnv:           binding.Auth.APIKeyEnv,
		CredentialFile:      binding.Auth.CredentialFile,
		CodexBaseURL:        binding.Provider.CodexBaseURL,
		CodexAuthFile:       binding.Auth.CodexAuthFile,
		CodexRefreshURL:     binding.Auth.CodexRefreshURL,
		CodexAuthAPIBaseURL: binding.Auth.CodexAuthAPIBaseURL,
		CodexClientID:       binding.Auth.CodexClientID,
		CodexOriginator:     binding.Auth.CodexOriginator,
	}
}

func (c Config) RuntimeToolSnapshot() RuntimeToolSnapshot {
	limits := c.RuntimeLimits()
	tools := c.ToolPolicySettings()
	diagnostics := c.DiagnosticsSettings()
	mcp := c.MCPSettings()
	_, tavilySource, tavilyOK := LookupEnvDotenvOrFiles(tools.WebTavilyAPIKeyEnv, tools.WebHermesEnvFiles)
	_, exaSource, exaOK := LookupEnvDotenvOrFiles(tools.WebExaAPIKeyEnv, tools.WebHermesEnvFiles)
	return RuntimeToolSnapshot{
		ContextWindowTokens:           limits.ContextWindowTokens,
		ContextCompactTokens:          limits.ContextCompactTokens,
		ContextCompactStrategy:        limits.ContextCompactStrategy,
		ContextCompactSummaryProvider: limits.ContextCompactSummaryProvider,
		ContextCompactSummaryModel:    limits.ContextCompactSummaryModel,
		WebSummaryMode:                tools.WebSummaryMode,
		WebSummaryProvider:            tools.WebSummaryProvider,
		WebSummaryModel:               tools.WebSummaryModel,
		WebCacheEnabled:               tools.WebCacheEnabled,
		WebCacheTTLMS:                 tools.WebCacheTTL.Milliseconds(),
		WebCacheMaxBytes:              tools.WebCacheMaxBytes,
		WebSearchBackend:              tools.WebSearchBackend,
		WebExtractBackend:             tools.WebExtractBackend,
		WebTavilyAPIKeyEnv:            tools.WebTavilyAPIKeyEnv,
		WebTavilyAPIKeyPresent:        tavilyOK,
		WebTavilyAPIKeySource:         tavilySource,
		WebExaAPIKeyEnv:               tools.WebExaAPIKeyEnv,
		WebExaAPIKeyPresent:           exaOK,
		WebExaAPIKeySource:            exaSource,
		WebHermesEnvFileCount:         len(tools.WebHermesEnvFiles),
		MaxToolRounds:                 limits.MaxToolRounds,
		MaxParallelTools:              limits.MaxParallelTools,
		GatewayAddr:                   c.GatewayAddr,
		MemoryEnabled:                 tools.MemoryEnabled,
		MemoryAutoExtractEnabled:      c.MemoryAutoExtractEnabled,
		MemorySummaryMaxBytes:         tools.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:           tools.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:           tools.MemoryTopicMaxBytes,
		AutoApproveDangerous:          tools.AutoApproveDangerous,
		AccessMode:                    tools.AccessMode,
		MCPEnabled:                    mcp.Enabled,
		MCPAllowedServers:             strings.Join(mcp.AllowedServers, ","),
		MaxToolOutputBytes:            tools.MaxToolOutputBytes,
		DiagnosticsEnabled:            diagnostics.Enabled,
		DiagnosticsConfigFiles:        strings.Join(diagnostics.ConfigFiles, ","),
		DiagnosticsCommandCount:       len(diagnostics.Commands),
		StoreReasoningContent:         tools.StoreReasoningContent,
	}
}

func (c Config) ProviderCapabilitySnapshot() ProviderCapabilitySnapshot {
	binding := c.ProviderBinding()
	provider := modelinfo.ProviderForModel(binding.Model.Model, binding.Provider.Provider)
	info := modelinfo.Lookup(binding.Model.Model)
	out := ProviderCapabilitySnapshot{
		Provider:              provider,
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
		Provider:           provider,
		Model:              binding.Model.Model,
		Thinking:           binding.Model.Thinking,
		ReasoningEffort:    binding.Model.ReasoningEffort,
		MaxOutputTokens:    binding.Model.MaxTokens,
		RequireStreaming:   true,
		RequireToolCalls:   provider != modelinfo.ProviderMock,
		RequireParallel:    binding.Limits.MaxParallelTools > 1 && provider != modelinfo.ProviderMock,
		AllowUnknownModels: modelinfo.Provider(provider).Custom || provider == modelinfo.ProviderMock,
	}); err != nil {
		out.ValidationError = err.Error()
	}
	return out
}
