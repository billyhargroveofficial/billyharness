package modelinfo

import (
	"fmt"
	"strings"
)

const (
	ProviderDeepSeek    = "deepseek"
	ProviderOpenAICodex = "openai-codex"
	ProviderMock        = "mock"
)

type Pricing struct {
	CacheHitPer1M  float64
	CacheMissPer1M float64
	InputPer1M     float64
	OutputPer1M    float64
}

type HelperModels struct {
	WebSummary string
	Memory     string
}

type Info struct {
	Model                 string
	Provider              string
	Subscription          bool
	Pricing               Pricing
	Known                 bool
	InputModalities       []string
	VisionInput           bool
	ContextWindowTokens   int64
	MaxOutputTokens       int
	ReasoningModes        []string
	Reasoning             bool
	ToolCalls             bool
	ParallelToolCalls     bool
	Streaming             bool
	TokenAccountingFields []string
	CacheAccountingFields []string
	DefaultSummaryModel   string
	HelperModels          HelperModels
	CostMode              string
}

type ProviderInfo struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Transport        string   `json:"transport"`
	Auth             string   `json:"auth"`
	OpenAICompatible bool     `json:"openai_compatible"`
	Subscription     bool     `json:"subscription"`
	Custom           bool     `json:"custom"`
	Models           []string `json:"models,omitempty"`
}

type CapabilityPolicyRequest struct {
	Provider           string
	Model              string
	Thinking           string
	ReasoningEffort    string
	MaxOutputTokens    int
	RequireToolCalls   bool
	RequireParallel    bool
	RequireStreaming   bool
	RequireVisionInput bool
	HelperKind         string
	AllowUnknownModels bool
}

func Lookup(model string) Info {
	model = NormalizeAlias(model)
	switch model {
	case "deepseek-v4-flash":
		info := deepSeekInfo(model)
		info.Pricing = Pricing{CacheHitPer1M: 0.0028, CacheMissPer1M: 0.14, InputPer1M: 0.14, OutputPer1M: 0.28}
		return info
	case "deepseek-v4-pro":
		info := deepSeekInfo(model)
		info.Pricing = Pricing{CacheHitPer1M: 0.003625, CacheMissPer1M: 0.435, InputPer1M: 0.435, OutputPer1M: 0.87}
		return info
	case "gpt-5.5", "gpt-5.5-pro", "gpt-5.4", "gpt-5.4-pro", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.3-codex-spark":
		return codexInfo(model, true)
	case "mock", "mock-summarizer", "mock-summary":
		return mockInfo(model)
	default:
		if IsCodexModel(model) {
			return codexInfo(model, false)
		}
		if IsDeepSeekModel(model) {
			info := deepSeekInfo(model)
			info.Known = false
			return info
		}
		return Info{Model: model}
	}
}

func InputCapabilityLabel(model string) string {
	if Lookup(model).VisionInput {
		return "vision-capable"
	}
	return "text-only"
}

func deepSeekInfo(model string) Info {
	return Info{
		Model:                 model,
		Provider:              ProviderDeepSeek,
		Known:                 true,
		InputModalities:       []string{"text"},
		ContextWindowTokens:   1_000_000,
		MaxOutputTokens:       8_192,
		ReasoningModes:        []string{"off", "low", "medium", "high", "xhigh", "max"},
		Reasoning:             true,
		ToolCalls:             true,
		ParallelToolCalls:     true,
		Streaming:             true,
		TokenAccountingFields: []string{"input_tokens", "output_tokens", "reasoning_tokens"},
		CacheAccountingFields: []string{"cache_hit_tokens", "cache_miss_tokens"},
		DefaultSummaryModel:   "deepseek-v4-flash",
		HelperModels:          HelperModels{WebSummary: "deepseek-v4-flash", Memory: "deepseek-v4-flash"},
		CostMode:              "metered",
	}
}

func codexInfo(model string, known bool) Info {
	return Info{
		Model:                 model,
		Provider:              ProviderOpenAICodex,
		Subscription:          true,
		Known:                 known,
		InputModalities:       []string{"text", "image"},
		VisionInput:           true,
		ContextWindowTokens:   codexContextWindowTokens(model),
		MaxOutputTokens:       8_192,
		ReasoningModes:        []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"},
		Reasoning:             true,
		ToolCalls:             true,
		ParallelToolCalls:     true,
		Streaming:             true,
		TokenAccountingFields: []string{"input_tokens", "output_tokens", "reasoning_tokens"},
		CacheAccountingFields: []string{"cache_hit_tokens", "cache_miss_tokens"},
		DefaultSummaryModel:   "gpt-5.4-mini",
		HelperModels:          HelperModels{WebSummary: "gpt-5.4-mini", Memory: "gpt-5.4-mini"},
		CostMode:              "subscription",
	}
}

func codexContextWindowTokens(model string) int64 {
	switch NormalizeAlias(model) {
	case "gpt-5.5-pro", "gpt-5.4-pro":
		return 400_000
	case "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano":
		return 256_000
	case "gpt-5.3-codex-spark":
		return 128_000
	default:
		return 256_000
	}
}

func mockInfo(model string) Info {
	return Info{
		Model:                 model,
		Provider:              ProviderMock,
		Known:                 true,
		InputModalities:       []string{"text"},
		ContextWindowTokens:   1_000_000,
		MaxOutputTokens:       8_192,
		ReasoningModes:        []string{"off", "low", "medium", "high", "xhigh", "max"},
		Reasoning:             true,
		Streaming:             true,
		TokenAccountingFields: []string{"input_tokens", "output_tokens", "reasoning_tokens"},
		CacheAccountingFields: []string{"cache_hit_tokens", "cache_miss_tokens"},
		DefaultSummaryModel:   "mock-summarizer",
		HelperModels:          HelperModels{WebSummary: "mock-summarizer", Memory: "mock-summarizer"},
		CostMode:              "none",
	}
}

func Provider(id string) ProviderInfo {
	switch NormalizeProvider(id) {
	case ProviderDeepSeek:
		return ProviderInfo{
			ID:               ProviderDeepSeek,
			Name:             "DeepSeek",
			Transport:        "openai-compatible-chat-completions",
			Auth:             "api-key",
			OpenAICompatible: true,
			Models:           []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		}
	case ProviderOpenAICodex:
		return ProviderInfo{
			ID:               ProviderOpenAICodex,
			Name:             "Codex/OpenAI OAuth",
			Transport:        "codex-responses",
			Auth:             "codex-oauth",
			OpenAICompatible: false,
			Subscription:     true,
			Models:           []string{"gpt-5.5", "gpt-5.5-pro", "gpt-5.4", "gpt-5.4-pro", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.3-codex-spark"},
		}
	case ProviderMock:
		return ProviderInfo{ID: ProviderMock, Name: "Mock", Transport: "in-process", Auth: "none", Models: []string{"mock"}}
	default:
		id = NormalizeProvider(id)
		if id == "" {
			id = "custom"
		}
		return ProviderInfo{
			ID:               id,
			Name:             "OpenAI-compatible custom provider",
			Transport:        "openai-compatible-chat-completions",
			Auth:             "api-key",
			OpenAICompatible: true,
			Custom:           true,
		}
	}
}

func Providers() []ProviderInfo {
	return []ProviderInfo{
		Provider(ProviderDeepSeek),
		Provider(ProviderOpenAICodex),
		Provider("custom"),
	}
}

func DefaultSummaryModel(model, provider string) string {
	info := Lookup(model)
	if info.HelperModels.WebSummary != "" {
		return info.HelperModels.WebSummary
	}
	if info.DefaultSummaryModel != "" {
		return info.DefaultSummaryModel
	}
	switch ProviderForModel(model, provider) {
	case ProviderOpenAICodex:
		return "gpt-5.4-mini"
	default:
		return "deepseek-v4-flash"
	}
}

func ValidateCapabilityPolicy(req CapabilityPolicyRequest) error {
	model := NormalizeAlias(req.Model)
	provider := ProviderForModel(model, req.Provider)
	info := Lookup(model)
	if !info.Known && info.Provider == "" && !req.AllowUnknownModels {
		return fmt.Errorf("unsupported model %q for provider %q: model capabilities are unknown", model, provider)
	}
	if info.Provider != "" && provider != "" && info.Provider != provider && !Provider(provider).Custom {
		return fmt.Errorf("unsupported provider/model combination: model %q belongs to provider %q, not %q", model, info.Provider, provider)
	}
	scope := "model"
	if req.HelperKind != "" {
		scope = req.HelperKind + " helper model"
	}
	if req.RequireStreaming && info.Known && !info.Streaming {
		return fmt.Errorf("unsupported %s %q on provider %q: streaming is required", scope, model, provider)
	}
	if req.RequireVisionInput && !info.VisionInput {
		if !info.Known && info.Provider == "" {
			return fmt.Errorf("unsupported %s %q on provider %q: image input is required but model capabilities are unknown", scope, model, provider)
		}
		return fmt.Errorf("unsupported %s %q on provider %q: image input is required", scope, model, provider)
	}
	if req.RequireToolCalls && info.Known && !info.ToolCalls {
		return fmt.Errorf("unsupported %s %q on provider %q: tool calls are required", scope, model, provider)
	}
	if req.RequireParallel && info.Known && !info.ParallelToolCalls {
		return fmt.Errorf("unsupported %s %q on provider %q: parallel tool calls are required", scope, model, provider)
	}
	if req.MaxOutputTokens > 0 && info.MaxOutputTokens > 0 && req.MaxOutputTokens > info.MaxOutputTokens {
		return fmt.Errorf("unsupported %s %q on provider %q: max_output_tokens=%d exceeds capability max_output_tokens=%d", scope, model, provider, req.MaxOutputTokens, info.MaxOutputTokens)
	}
	effort := NormalizeReasoningEffort(req.ReasoningEffort)
	if provider == ProviderDeepSeek && !thinkingEnabled(req.Thinking) {
		effort = "off"
	}
	if effort != "off" {
		if info.Known && !info.Reasoning {
			return fmt.Errorf("unsupported %s %q on provider %q: reasoning is not supported", scope, model, provider)
		}
		if info.Known && !hasString(info.ReasoningModes, effort) {
			return fmt.Errorf("unsupported reasoning_effort %q for %s %q on provider %q; supported modes: %s", effort, scope, model, provider, strings.Join(info.ReasoningModes, ","))
		}
	}
	return nil
}

func NormalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "disabled", "none", "false":
		return "off"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func thinkingEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "enabled", "on", "true", "1", "yes":
		return true
	default:
		return false
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func NormalizeAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	switch value {
	case "flash", "v4 flash", "v4-flash", "deepseek flash", "deepseek-v4-flash":
		return "deepseek-v4-flash"
	case "pro", "v4 pro", "v4-pro", "deepseek pro", "deepseek-v4-pro":
		return "deepseek-v4-pro"
	case "gpt", "codex", "chatgpt", "gpt max", "gpt-5.5":
		return "gpt-5.5"
	case "gpt fast", "gpt mini", "gpt-5.4-mini":
		return "gpt-5.4-mini"
	case "spark", "codex spark", "gpt-5.3-codex-spark":
		return "gpt-5.3-codex-spark"
	default:
		return value
	}
}

func ProviderForModel(model, currentProvider string) string {
	info := Lookup(model)
	provider := NormalizeProvider(currentProvider)
	switch info.Provider {
	case ProviderOpenAICodex:
		if provider == "" || provider == ProviderDeepSeek {
			return ProviderOpenAICodex
		}
	case ProviderDeepSeek:
		if provider == "" || provider == ProviderOpenAICodex {
			return ProviderDeepSeek
		}
	}
	if provider != "" {
		return provider
	}
	return ProviderDeepSeek
}

func NormalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai-codex", "codex", "chatgpt-codex", "chatgpt":
		return ProviderOpenAICodex
	case "deepseek":
		return ProviderDeepSeek
	case "mock":
		return ProviderMock
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func IsCodexModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4")
}

func IsSparkModel(model string) bool {
	return NormalizeAlias(model) == "gpt-5.3-codex-spark"
}

func IsDeepSeekModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-")
}
