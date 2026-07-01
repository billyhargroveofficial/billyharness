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
)

const (
	SourceBuiltIn     = "built-in defaults"
	SourceHomeConfig  = "$BILLYHARNESS_HOME/config.toml"
	SourceProject     = "project .billyharness/config.toml"
	SourceSettings    = "$BILLYHARNESS_HOME/settings.json"
	SourceProfile     = "$BILLYHARNESS_HOME/profiles/<profile>/profile.toml"
	SourceDotenv      = ".env"
	SourceEnvironment = "environment"
	SourceCLI         = "cli flag"
	SourceGateway     = "gateway/tui runtime override"
	SourceDerived     = "derived"
)

type ResolvedConfig struct {
	Config   Config          `json:"config"`
	Values   []ResolvedValue `json:"values"`
	Warnings []string        `json:"warnings,omitempty"`
}

type ResolvedValue struct {
	Key        string `json:"key"`
	Value      any    `json:"value"`
	Source     string `json:"source"`
	SourcePath string `json:"source_path,omitempty"`
	SourceKey  string `json:"source_key,omitempty"`
	Redacted   bool   `json:"redacted,omitempty"`
	Warning    string `json:"warning,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ResolveOverride struct {
	Key        string
	Value      any
	Source     string
	SourcePath string
	SourceKey  string
}

type resolveState struct {
	cfg      Config
	values   map[string]ResolvedValue
	warnings []string
}

type configSpec struct {
	Key      string
	Env      []string
	Redacted bool
	get      func(Config) any
	set      func(*Config, any) error
}

func Resolve(overrides ...ResolveOverride) (ResolvedConfig, error) {
	state := resolveState{
		cfg:    builtInConfig(),
		values: map[string]ResolvedValue{},
	}
	for _, spec := range configSpecs() {
		state.record(spec.Key, displayConfigValue(spec.get(state.cfg)), SourceBuiltIn, "", spec.Key, spec.Redacted, "", "")
	}
	if err := state.applyTOML(DefaultConfigFile(), SourceHomeConfig); err != nil {
		return ResolvedConfig{}, err
	}
	if path := findProjectConfigFile(); path != "" {
		if err := state.applyTOML(path, SourceProject); err != nil {
			return ResolvedConfig{}, err
		}
	}
	state.applyBillySettings()
	state.applyDotenv()
	state.applyEnvironment()
	for _, override := range overrides {
		state.applyOverride(override)
	}
	if err := state.applyProfileMetadata(); err != nil {
		return ResolvedConfig{}, err
	}
	state.finalizeDerivedValues()
	return ResolvedConfig{
		Config:   state.cfg,
		Values:   state.sortedValues(),
		Warnings: append([]string(nil), state.warnings...),
	}, nil
}

func MustResolve(overrides ...ResolveOverride) ResolvedConfig {
	resolved, err := Resolve(overrides...)
	if err != nil {
		cfg := builtInConfig()
		cfg.ApplyBillySettingsDefaults()
		cfg.ApplyModelProviderDefaults()
		cfg.ApplyWebSummaryDefaults()
		return ResolvedConfig{
			Config: cfg,
			Warnings: []string{
				err.Error(),
			},
		}
	}
	return resolved
}

func DefaultConfigFile() string {
	return filepath.Join(BillyHomeDir(), "config.toml")
}

func ProjectConfigFileFrom(dir string) string {
	dir = filepath.Clean(dir)
	for {
		candidate := filepath.Join(dir, ".billyharness", "config.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (r ResolvedConfig) Value(key string) (ResolvedValue, bool) {
	key = normalizeConfigKey(key)
	for _, value := range r.Values {
		if value.Key == key {
			return value, true
		}
	}
	return ResolvedValue{}, false
}

func (r ResolvedConfig) SanitizedValues() []ResolvedValue {
	out := make([]ResolvedValue, 0, len(r.Values))
	for _, value := range r.Values {
		if value.Redacted {
			value.Value = "[redacted]"
		}
		out = append(out, value)
	}
	return out
}

func (r ResolvedConfig) SanitizedConfig() map[string]any {
	out := make(map[string]any, len(r.Values))
	for _, value := range r.SanitizedValues() {
		out[value.Key] = value.Value
	}
	return out
}

func configSpecs() []configSpec {
	return []configSpec{
		stringSpec("provider", []string{"FAST_AGENT_PROVIDER"}, func(c Config) any { return c.Provider }, func(c *Config, v string) { c.Provider = v }),
		stringSpec("model", []string{"FAST_AGENT_MODEL"}, func(c Config) any { return c.Model }, func(c *Config, v string) { c.Model = v }),
		stringSpec("profile", []string{"BILLYHARNESS_PROFILE", "FAST_AGENT_PROFILE"}, func(c Config) any { return c.Profile }, func(c *Config, v string) { c.Profile = NormalizeProfileName(v) }),
		stringSpec("base_url", []string{"DEEPSEEK_BASE_URL"}, func(c Config) any { return c.BaseURL }, func(c *Config, v string) { c.BaseURL = v }),
		stringSpec("api_key_env", []string{"DEEPSEEK_API_KEY_ENV"}, func(c Config) any { return c.APIKeyEnv }, func(c *Config, v string) { c.APIKeyEnv = v }),
		stringSpec("credential_file", []string{"BILLYHARNESS_CREDENTIAL_FILE", "FAST_AGENT_CREDENTIAL_FILE"}, func(c Config) any { return c.CredentialFile }, func(c *Config, v string) { c.CredentialFile = v }),
		stringSpec("codex_base_url", []string{"FAST_AGENT_CODEX_BASE_URL"}, func(c Config) any { return c.CodexBaseURL }, func(c *Config, v string) { c.CodexBaseURL = v }),
		stringSpec("codex_auth_file", []string{"FAST_AGENT_CODEX_AUTH_FILE"}, func(c Config) any { return c.CodexAuthFile }, func(c *Config, v string) { c.CodexAuthFile = v }),
		stringSpec("codex_refresh_url", []string{"FAST_AGENT_CODEX_REFRESH_URL"}, func(c Config) any { return c.CodexRefreshURL }, func(c *Config, v string) { c.CodexRefreshURL = v }),
		stringSpec("codex_auth_api_base_url", []string{"CODEX_AUTHAPI_BASE_URL"}, func(c Config) any { return c.CodexAuthAPIBaseURL }, func(c *Config, v string) { c.CodexAuthAPIBaseURL = v }),
		stringSpec("codex_client_id", []string{"FAST_AGENT_CODEX_CLIENT_ID"}, func(c Config) any { return c.CodexClientID }, func(c *Config, v string) { c.CodexClientID = v }),
		stringSpec("codex_originator", []string{"FAST_AGENT_CODEX_ORIGINATOR"}, func(c Config) any { return c.CodexOriginator }, func(c *Config, v string) { c.CodexOriginator = v }),
		stringSpec("thinking", []string{"DEEPSEEK_THINKING"}, func(c Config) any { return c.Thinking }, func(c *Config, v string) { c.Thinking = v }),
		stringSpec("reasoning_effort", []string{"DEEPSEEK_REASONING_EFFORT"}, func(c Config) any { return c.ReasoningEffort }, func(c *Config, v string) { c.ReasoningEffort = v }),
		boolSpec("disable_spark", []string{"BILLYHARNESS_DISABLE_SPARK", "FAST_AGENT_DISABLE_SPARK"}, func(c Config) any { return c.DisableSpark }, func(c *Config, v bool) { c.DisableSpark = v }),
		intSpec("max_tokens", []string{"FAST_AGENT_MAX_TOKENS"}, func(c Config) any { return c.MaxTokens }, func(c *Config, v int) { c.MaxTokens = v }),
		intSpec("max_tool_rounds", []string{"FAST_AGENT_MAX_TOOL_ROUNDS"}, func(c Config) any { return c.MaxToolRounds }, func(c *Config, v int) { c.MaxToolRounds = v }),
		intSpec("max_parallel_tools", []string{"FAST_AGENT_MAX_PARALLEL_TOOLS"}, func(c Config) any { return c.MaxParallelTools }, func(c *Config, v int) { c.MaxParallelTools = v }),
		intSpec("provider_max_retries", []string{"FAST_AGENT_PROVIDER_MAX_RETRIES"}, func(c Config) any { return c.ProviderMaxRetries }, func(c *Config, v int) { c.ProviderMaxRetries = v }),
		int64Spec("context_window_tokens", []string{"FAST_AGENT_CONTEXT_WINDOW_TOKENS"}, func(c Config) any { return c.ContextWindowTokens }, func(c *Config, v int64) { c.ContextWindowTokens = v }),
		intSpec("context_compact_tokens", []string{"FAST_AGENT_CONTEXT_COMPACT_TOKENS"}, func(c Config) any { return c.ContextCompactTokens }, func(c *Config, v int) { c.ContextCompactTokens = v }),
		intSpec("context_compact_keep", []string{"FAST_AGENT_CONTEXT_COMPACT_KEEP"}, func(c Config) any { return c.ContextCompactKeep }, func(c *Config, v int) { c.ContextCompactKeep = v }),
		intSpec("context_compact_max_chars", []string{"FAST_AGENT_CONTEXT_COMPACT_MAX_CHARS"}, func(c Config) any { return c.ContextCompactMaxChars }, func(c *Config, v int) { c.ContextCompactMaxChars = v }),
		stringSpec("context_compact_strategy", []string{"FAST_AGENT_CONTEXT_COMPACT_STRATEGY"}, func(c Config) any { return c.ContextCompactStrategy }, func(c *Config, v string) { c.ContextCompactStrategy = v }),
		stringSpec("context_compact_summary_provider", []string{"FAST_AGENT_CONTEXT_COMPACT_SUMMARY_PROVIDER"}, func(c Config) any { return c.ContextCompactSummaryProvider }, func(c *Config, v string) { c.ContextCompactSummaryProvider = v }),
		stringSpec("context_compact_summary_model", []string{"FAST_AGENT_CONTEXT_COMPACT_SUMMARY_MODEL"}, func(c Config) any { return c.ContextCompactSummaryModel }, func(c *Config, v string) { c.ContextCompactSummaryModel = v }),
		stringSpec("web_summary_mode", []string{"FAST_AGENT_WEB_SUMMARY_MODE"}, func(c Config) any { return c.WebSummaryMode }, func(c *Config, v string) { c.WebSummaryMode = v }),
		stringSpec("web_summary_provider", []string{"FAST_AGENT_WEB_SUMMARY_PROVIDER"}, func(c Config) any { return c.WebSummaryProvider }, func(c *Config, v string) { c.WebSummaryProvider = v }),
		stringSpec("web_summary_model", []string{"FAST_AGENT_WEB_SUMMARY_MODEL"}, func(c Config) any { return c.WebSummaryModel }, func(c *Config, v string) { c.WebSummaryModel = v }),
		intSpec("web_summary_max_input_tokens", []string{"FAST_AGENT_WEB_SUMMARY_MAX_INPUT_TOKENS"}, func(c Config) any { return c.WebSummaryMaxInputTokens }, func(c *Config, v int) { c.WebSummaryMaxInputTokens = v }),
		intSpec("web_summary_max_output_tokens", []string{"FAST_AGENT_WEB_SUMMARY_MAX_OUTPUT_TOKENS"}, func(c Config) any { return c.WebSummaryMaxOutputTokens }, func(c *Config, v int) { c.WebSummaryMaxOutputTokens = v }),
		durationSecondsSpec("web_summary_timeout_sec", []string{"FAST_AGENT_WEB_SUMMARY_TIMEOUT_SEC"}, func(c Config) any { return c.WebSummaryTimeout }, func(c *Config, v time.Duration) { c.WebSummaryTimeout = v }),
		boolSpec("web_cache_enabled", []string{"FAST_AGENT_WEB_CACHE_ENABLED", "BILLYHARNESS_WEB_CACHE_ENABLED"}, func(c Config) any { return c.WebCacheEnabled }, func(c *Config, v bool) { c.WebCacheEnabled = v }),
		durationSecondsSpec("web_cache_ttl_sec", []string{"FAST_AGENT_WEB_CACHE_TTL_SEC", "BILLYHARNESS_WEB_CACHE_TTL_SEC"}, func(c Config) any { return c.WebCacheTTL }, func(c *Config, v time.Duration) { c.WebCacheTTL = v }),
		int64Spec("web_cache_max_bytes", []string{"FAST_AGENT_WEB_CACHE_MAX_BYTES", "BILLYHARNESS_WEB_CACHE_MAX_BYTES"}, func(c Config) any { return c.WebCacheMaxBytes }, func(c *Config, v int64) { c.WebCacheMaxBytes = v }),
		durationSecondsSpec("request_timeout_sec", []string{"FAST_AGENT_REQUEST_TIMEOUT_SEC"}, func(c Config) any { return c.RequestTimeout }, func(c *Config, v time.Duration) { c.RequestTimeout = v }),
		durationSecondsSpec("stream_idle_timeout_sec", []string{"FAST_AGENT_STREAM_IDLE_TIMEOUT_SEC"}, func(c Config) any { return c.StreamIdleTimeout }, func(c *Config, v time.Duration) { c.StreamIdleTimeout = v }),
		intSpec("project_doc_max_bytes", []string{"FAST_AGENT_PROJECT_DOC_MAX_BYTES"}, func(c Config) any { return c.ProjectDocMaxBytes }, func(c *Config, v int) { c.ProjectDocMaxBytes = v }),
		stringListSpec("project_doc_fallback_filenames", []string{"FAST_AGENT_PROJECT_DOC_FALLBACK_FILENAMES"}, func(c Config) any { return c.ProjectDocFallbacks }, func(c *Config, v []string) { c.ProjectDocFallbacks = v }),
		intSpec("max_tool_output_bytes", []string{"FAST_AGENT_MAX_TOOL_OUTPUT_BYTES"}, func(c Config) any { return c.MaxToolOutputBytes }, func(c *Config, v int) { c.MaxToolOutputBytes = v }),
		boolSpec("auto_approve_dangerous", []string{"FAST_AGENT_AUTO_APPROVE_DANGEROUS"}, func(c Config) any { return c.AutoApproveDangerous }, func(c *Config, v bool) { c.AutoApproveDangerous = v }),
		boolSpec("store_reasoning", []string{"FAST_AGENT_STORE_REASONING"}, func(c Config) any { return c.StoreReasoningContent }, func(c *Config, v bool) { c.StoreReasoningContent = v }),
		stringSpec("gateway_addr", []string{"FAST_AGENT_GATEWAY_ADDR"}, func(c Config) any { return c.GatewayAddr }, func(c *Config, v string) { c.GatewayAddr = v }),
		boolSpec("mcp_enabled", []string{"FAST_AGENT_MCP_ENABLED"}, func(c Config) any { return c.MCPEnabled }, func(c *Config, v bool) { c.MCPEnabled = v }),
		stringListSpec("mcp_config_files", []string{"FAST_AGENT_MCP_CONFIG_FILES"}, func(c Config) any { return c.MCPConfigFiles }, func(c *Config, v []string) { c.MCPConfigFiles = v }),
		stringListSpec("mcp_allowed_servers", []string{"FAST_AGENT_MCP_ALLOWED_SERVERS"}, func(c Config) any { return c.MCPAllowedServers }, func(c *Config, v []string) { c.MCPAllowedServers = v }),
		boolSpec("hooks_enabled", []string{"BILLYHARNESS_HOOKS_ENABLED", "FAST_AGENT_HOOKS_ENABLED"}, func(c Config) any { return c.HooksEnabled }, func(c *Config, v bool) { c.HooksEnabled = v }),
		stringListSpec("hooks_config_files", []string{"BILLYHARNESS_HOOKS_CONFIG_FILES", "FAST_AGENT_HOOKS_CONFIG_FILES"}, func(c Config) any { return c.HookConfigFiles }, func(c *Config, v []string) { c.HookConfigFiles = v }),
	}
}

func stringSpec(key string, env []string, get func(Config) any, set func(*Config, string)) configSpec {
	return configSpec{Key: key, Env: env, get: get, set: func(c *Config, value any) error {
		set(c, strings.TrimSpace(fmt.Sprint(value)))
		return nil
	}}
}

func intSpec(key string, env []string, get func(Config) any, set func(*Config, int)) configSpec {
	return configSpec{Key: key, Env: env, get: get, set: func(c *Config, value any) error {
		parsed, err := parseIntValue(value)
		if err != nil {
			return err
		}
		set(c, parsed)
		return nil
	}}
}

func int64Spec(key string, env []string, get func(Config) any, set func(*Config, int64)) configSpec {
	return configSpec{Key: key, Env: env, get: get, set: func(c *Config, value any) error {
		parsed, err := parseInt64Value(value)
		if err != nil {
			return err
		}
		set(c, parsed)
		return nil
	}}
}

func boolSpec(key string, env []string, get func(Config) any, set func(*Config, bool)) configSpec {
	return configSpec{Key: key, Env: env, get: get, set: func(c *Config, value any) error {
		parsed, err := parseBoolValue(value)
		if err != nil {
			return err
		}
		set(c, parsed)
		return nil
	}}
}

func durationSecondsSpec(key string, env []string, get func(Config) any, set func(*Config, time.Duration)) configSpec {
	return configSpec{Key: key, Env: env, get: get, set: func(c *Config, value any) error {
		parsed, err := parseIntValue(value)
		if err != nil {
			return err
		}
		set(c, time.Duration(parsed)*time.Second)
		return nil
	}}
}

func stringListSpec(key string, env []string, get func(Config) any, set func(*Config, []string)) configSpec {
	return configSpec{Key: key, Env: env, get: get, set: func(c *Config, value any) error {
		parsed, err := parseStringListValue(value)
		if err != nil {
			return err
		}
		set(c, parsed)
		return nil
	}}
}

func (s *resolveState) applyTOML(path, source string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	raw := map[string]any{}
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	for key, value := range raw {
		normalized := normalizeConfigKey(key)
		if _, ok := specByKey(normalized); !ok {
			if isConfigNamespace(normalized, value) {
				continue
			}
			s.warn(fmt.Sprintf("unknown config key %q in %s", key, path))
			continue
		}
		s.applyValue(normalized, value, source, path, key)
	}
	return nil
}

func (s *resolveState) applyBillySettings() {
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
		s.warn(fmt.Sprintf("load %s: %v", path, err))
		return
	}
	if settings.ContextWindowTokens > 0 {
		s.applyValue("context_window_tokens", settings.ContextWindowTokens, SourceSettings, path, "context_window_tokens")
	}
	if strings.TrimSpace(settings.LastSelectedModel) != "" {
		s.applyValue("model", settings.LastSelectedModel, SourceSettings, path, "last_selected_model")
	}
	if strings.TrimSpace(settings.LastReasoningKind) != "" {
		s.applyValue("thinking", settings.LastReasoningKind, SourceSettings, path, "last_reasoning_kind")
	}
	if strings.TrimSpace(settings.LastReasoningEffort) != "" {
		s.applyValue("reasoning_effort", settings.LastReasoningEffort, SourceSettings, path, "last_reasoning_effort")
	}
	if strings.TrimSpace(settings.LastProfile) != "" {
		s.applyValue("profile", settings.LastProfile, SourceSettings, path, "last_profile")
	}
}

func (s *resolveState) applyProfileMetadata() error {
	meta, path, ok, err := LoadProfileMetadata(s.cfg.Profile)
	if err != nil || !ok {
		return err
	}
	source := SourceProfile
	s.record("profile_name", meta.Name, source, path, "name", false, "", "")
	if strings.TrimSpace(meta.ToolPolicy) != "" {
		s.record("profile_tool_policy", meta.ToolPolicy, source, path, "tool_policy", false, "", "")
	}
	if len(meta.InstructionFragments) > 0 {
		s.record("profile_instruction_fragments", append([]string(nil), meta.InstructionFragments...), source, path, "instruction_fragments", false, "", "")
	}
	if len(meta.CostBudgetHints) > 0 {
		s.record("profile_cost_budget_hints", append([]string(nil), meta.CostBudgetHints...), source, path, "cost_budget_hints", false, "", "")
	}
	if strings.TrimSpace(meta.Provider) != "" {
		s.applyProfileValue("provider", meta.Provider, source, path, "provider")
	}
	if strings.TrimSpace(meta.Model) != "" {
		s.applyProfileValue("model", meta.Model, source, path, "model")
	}
	if strings.TrimSpace(meta.Thinking) != "" {
		s.applyProfileValue("thinking", meta.Thinking, source, path, "thinking")
	}
	if strings.TrimSpace(meta.ReasoningEffort) != "" {
		s.applyProfileValue("reasoning_effort", meta.ReasoningEffort, source, path, "reasoning_effort")
	}
	if meta.DisableSpark != nil {
		s.applyProfileValue("disable_spark", *meta.DisableSpark, source, path, "disable_spark")
	}
	if meta.ContextWindowTokens > 0 {
		s.applyProfileValue("context_window_tokens", meta.ContextWindowTokens, source, path, "context_window_tokens")
	}
	if strings.TrimSpace(meta.WebSummaryMode) != "" {
		s.applyProfileValue("web_summary_mode", meta.WebSummaryMode, source, path, "web_summary_mode")
	}
	if len(meta.MCPAllowlist) > 0 {
		s.applyProfileValue("mcp_allowed_servers", meta.MCPAllowlist, source, path, "mcp_allowlist")
	}
	return nil
}

func (s *resolveState) applyProfileValue(key string, value any, source, sourcePath, sourceKey string) {
	current, ok := s.values[normalizeConfigKey(key)]
	if ok {
		switch current.Source {
		case SourceDotenv, SourceEnvironment, SourceCLI, SourceGateway:
			return
		case SourceSettings:
			if !s.profileSelectedExplicitly() {
				return
			}
		}
	}
	s.applyValue(key, value, source, sourcePath, sourceKey)
}

func (s *resolveState) profileSelectedExplicitly() bool {
	value, ok := s.values["profile"]
	if !ok {
		return false
	}
	switch value.Source {
	case SourceHomeConfig, SourceProject, SourceDotenv, SourceEnvironment, SourceCLI, SourceGateway:
		return true
	default:
		return false
	}
}

func (s *resolveState) applyDotenv() {
	for _, spec := range configSpecs() {
		for _, envKey := range spec.Env {
			value, path, ok := dotenvValueWithSource(envKey)
			if !ok {
				continue
			}
			s.applyValue(spec.Key, value, SourceDotenv, path, envKey)
			break
		}
	}
}

func (s *resolveState) applyEnvironment() {
	for _, spec := range configSpecs() {
		for _, envKey := range spec.Env {
			value, ok := os.LookupEnv(envKey)
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			s.applyValue(spec.Key, value, SourceEnvironment, "", envKey)
			break
		}
	}
}

func (s *resolveState) applyOverride(override ResolveOverride) {
	key := normalizeConfigKey(override.Key)
	if key == "" {
		return
	}
	source := strings.TrimSpace(override.Source)
	if source == "" {
		source = SourceCLI
	}
	sourceKey := strings.TrimSpace(override.SourceKey)
	if sourceKey == "" {
		sourceKey = key
	}
	s.applyValue(key, override.Value, source, override.SourcePath, sourceKey)
}

func (s *resolveState) applyValue(key string, value any, source, sourcePath, sourceKey string) {
	key = normalizeConfigKey(key)
	spec, ok := specByKey(key)
	if !ok {
		s.warn(fmt.Sprintf("unknown config key %q from %s", key, source))
		return
	}
	if err := spec.set(&s.cfg, value); err != nil {
		s.record(key, displayConfigValue(spec.get(s.cfg)), source, sourcePath, sourceKey, spec.Redacted, "", err.Error())
		s.warn(fmt.Sprintf("invalid config %s from %s: %v", key, source, err))
		return
	}
	s.record(key, displayConfigValue(spec.get(s.cfg)), source, sourcePath, sourceKey, spec.Redacted, "", "")
}

func (s *resolveState) finalizeDerivedValues() {
	beforeProvider := s.cfg.Provider
	beforeModel := s.cfg.Model
	s.cfg.ApplyModelProviderDefaults()
	if s.cfg.Model != beforeModel {
		s.record("model", s.cfg.Model, SourceDerived, "", "model alias", false, "normalized from "+beforeModel, "")
	}
	if s.cfg.Provider != beforeProvider {
		s.record("provider", s.cfg.Provider, SourceDerived, "", "model", false, "derived from model "+s.cfg.Model, "")
	}
	beforeWebProvider := s.cfg.WebSummaryProvider
	beforeWebModel := s.cfg.WebSummaryModel
	beforeWebMode := s.cfg.WebSummaryMode
	beforeCompactStrategy := s.cfg.ContextCompactStrategy
	beforeCompactProvider := s.cfg.ContextCompactSummaryProvider
	beforeCompactModel := s.cfg.ContextCompactSummaryModel
	s.cfg.ApplyWebSummaryDefaults()
	if s.cfg.WebSummaryMode != beforeWebMode {
		s.record("web_summary_mode", s.cfg.WebSummaryMode, SourceDerived, "", "web_summary_mode", false, "normalized from "+beforeWebMode, "")
	}
	if s.cfg.WebSummaryModel != beforeWebModel {
		sourceKey := "provider"
		if beforeWebModel != "" {
			sourceKey = "web_summary_model"
		}
		s.record("web_summary_model", s.cfg.WebSummaryModel, SourceDerived, "", sourceKey, false, "defaulted for provider "+s.cfg.Provider, "")
	}
	if s.cfg.WebSummaryProvider != beforeWebProvider {
		s.record("web_summary_provider", s.cfg.WebSummaryProvider, SourceDerived, "", "web_summary_model", false, "derived from web summary model "+s.cfg.WebSummaryModel, "")
	}
	if s.cfg.ContextCompactStrategy != beforeCompactStrategy {
		s.record("context_compact_strategy", s.cfg.ContextCompactStrategy, SourceDerived, "", "context_compact_strategy", false, "normalized from "+beforeCompactStrategy, "")
	}
	if s.cfg.ContextCompactSummaryModel != beforeCompactModel {
		s.record("context_compact_summary_model", s.cfg.ContextCompactSummaryModel, SourceDerived, "", "context_compact_summary_model", false, "defaulted from web summary model "+s.cfg.WebSummaryModel, "")
	}
	if s.cfg.ContextCompactSummaryProvider != beforeCompactProvider {
		s.record("context_compact_summary_provider", s.cfg.ContextCompactSummaryProvider, SourceDerived, "", "context_compact_summary_model", false, "derived from context compact summary model "+s.cfg.ContextCompactSummaryModel, "")
	}
	for _, spec := range configSpecs() {
		if _, ok := s.values[spec.Key]; !ok {
			s.record(spec.Key, displayConfigValue(spec.get(s.cfg)), SourceDerived, "", spec.Key, spec.Redacted, "", "")
		}
	}
}

func (s *resolveState) record(key string, value any, source, sourcePath, sourceKey string, redacted bool, warning, err string) {
	key = normalizeConfigKey(key)
	if redacted {
		value = "[redacted]"
	}
	s.values[key] = ResolvedValue{
		Key:        key,
		Value:      value,
		Source:     source,
		SourcePath: sourcePath,
		SourceKey:  sourceKey,
		Redacted:   redacted,
		Warning:    warning,
		Error:      err,
	}
}

func (s *resolveState) warn(message string) {
	if strings.TrimSpace(message) != "" {
		s.warnings = append(s.warnings, message)
	}
}

func (s *resolveState) sortedValues() []ResolvedValue {
	out := make([]ResolvedValue, 0, len(s.values))
	for _, value := range s.values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func specByKey(key string) (configSpec, bool) {
	key = normalizeConfigKey(key)
	for _, spec := range configSpecs() {
		if spec.Key == key {
			return spec, true
		}
	}
	return configSpec{}, false
}

func normalizeConfigKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	return key
}

func findProjectConfigFile() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return ProjectConfigFileFrom(cwd)
}

func dotenvValueWithSource(key string) (string, string, bool) {
	for _, path := range findDotenvFiles() {
		if value, ok := dotenvValueFromFile(path, key); ok && strings.TrimSpace(value) != "" {
			return value, path, true
		}
	}
	return "", "", false
}

func isConfigNamespace(key string, value any) bool {
	if _, ok := value.(map[string]any); ok {
		return true
	}
	return strings.HasPrefix(key, "mcp_servers")
}

func displayConfigValue(value any) any {
	switch v := value.(type) {
	case time.Duration:
		return int(v / time.Second)
	case []string:
		if len(v) == 0 {
			return []string{}
		}
		return append([]string(nil), v...)
	default:
		return v
	}
}

func parseIntValue(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", value)
	}
}

func parseInt64Value(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", value)
	}
}

func parseBoolValue(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(v))
	default:
		return false, fmt.Errorf("expected boolean, got %T", value)
	}
}

func parseStringListValue(value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return cleanStringList(v), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return cleanStringList(out), nil
	case string:
		return cleanStringList(strings.Split(v, ",")), nil
	default:
		return nil, fmt.Errorf("expected string list, got %T", value)
	}
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
