package config

import "strings"

type DiagnosticSnapshot struct {
	ProviderAuth ProviderAuthSnapshot `json:"provider_auth"`
	RuntimeTool  RuntimeToolSnapshot  `json:"runtime_tool"`
}

type ProviderAuthSnapshot struct {
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	Profile             string `json:"profile"`
	Thinking            string `json:"thinking"`
	ReasoningEffort     string `json:"reasoning_effort"`
	DisableSpark        bool   `json:"disable_spark"`
	BaseURL             string `json:"base_url,omitempty"`
	APIKeyEnv           string `json:"api_key_env"`
	CredentialFile      string `json:"credential_file"`
	CodexBaseURL        string `json:"codex_base_url,omitempty"`
	CodexAuthFile       string `json:"codex_auth_file"`
	CodexRefreshURL     string `json:"codex_refresh_url,omitempty"`
	CodexAuthAPIBaseURL string `json:"codex_auth_api_base_url,omitempty"`
	CodexClientID       string `json:"codex_client_id,omitempty"`
	CodexOriginator     string `json:"codex_originator,omitempty"`
}

type RuntimeToolSnapshot struct {
	ContextWindowTokens           int64  `json:"context_window_tokens"`
	ContextCompactTokens          int    `json:"context_compact_tokens"`
	ContextCompactStrategy        string `json:"context_compact_strategy"`
	ContextCompactSummaryProvider string `json:"context_compact_summary_provider"`
	ContextCompactSummaryModel    string `json:"context_compact_summary_model"`
	WebSummaryMode                string `json:"web_summary_mode"`
	WebSummaryProvider            string `json:"web_summary_provider"`
	WebSummaryModel               string `json:"web_summary_model"`
	WebCacheEnabled               bool   `json:"web_cache_enabled"`
	WebCacheTTLMS                 int64  `json:"web_cache_ttl_ms"`
	WebCacheMaxBytes              int64  `json:"web_cache_max_bytes"`
	MaxToolRounds                 int    `json:"max_tool_rounds"`
	MaxParallelTools              int    `json:"max_parallel_tools"`
	GatewayAddr                   string `json:"gateway_addr"`
	AutoApproveDangerous          bool   `json:"auto_approve_dangerous"`
	AccessMode                    string `json:"access_mode"`
	MCPEnabled                    bool   `json:"mcp_enabled"`
	MCPAllowedServers             string `json:"mcp_allowed_servers"`
	MaxToolOutputBytes            int    `json:"max_tool_output_bytes"`
	StoreReasoningContent         bool   `json:"store_reasoning_content"`
}

func (c Config) DiagnosticSnapshot() DiagnosticSnapshot {
	return DiagnosticSnapshot{
		ProviderAuth: c.ProviderAuthSnapshot(),
		RuntimeTool:  c.RuntimeToolSnapshot(),
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
	mcp := c.MCPSettings()
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
		MaxToolRounds:                 limits.MaxToolRounds,
		MaxParallelTools:              limits.MaxParallelTools,
		GatewayAddr:                   c.GatewayAddr,
		AutoApproveDangerous:          tools.AutoApproveDangerous,
		AccessMode:                    tools.AccessMode,
		MCPEnabled:                    mcp.Enabled,
		MCPAllowedServers:             strings.Join(mcp.AllowedServers, ","),
		MaxToolOutputBytes:            tools.MaxToolOutputBytes,
		StoreReasoningContent:         tools.StoreReasoningContent,
	}
}
