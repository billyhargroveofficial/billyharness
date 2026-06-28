package modelinfo

import "strings"

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

type Info struct {
	Model                 string
	Provider              string
	Subscription          bool
	Pricing               Pricing
	Known                 bool
	ContextWindowTokens   int64
	ReasoningModes        []string
	ToolCalls             bool
	ParallelToolCalls     bool
	Streaming             bool
	TokenAccountingFields []string
	CacheAccountingFields []string
	DefaultSummaryModel   string
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
	case "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark":
		return codexInfo(model, true)
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

func deepSeekInfo(model string) Info {
	return Info{
		Model:                 model,
		Provider:              ProviderDeepSeek,
		Known:                 true,
		ContextWindowTokens:   1_000_000,
		ReasoningModes:        []string{"off", "low", "medium", "high", "xhigh", "max"},
		ToolCalls:             true,
		ParallelToolCalls:     true,
		Streaming:             true,
		TokenAccountingFields: []string{"input_tokens", "output_tokens", "reasoning_tokens"},
		CacheAccountingFields: []string{"cache_hit_tokens", "cache_miss_tokens"},
		DefaultSummaryModel:   "deepseek-v4-flash",
	}
}

func codexInfo(model string, known bool) Info {
	return Info{
		Model:                 model,
		Provider:              ProviderOpenAICodex,
		Subscription:          true,
		Known:                 known,
		ContextWindowTokens:   1_000_000,
		ReasoningModes:        []string{"off", "low", "medium", "high", "xhigh", "max"},
		ToolCalls:             true,
		ParallelToolCalls:     true,
		Streaming:             true,
		TokenAccountingFields: []string{"input_tokens", "output_tokens", "reasoning_tokens"},
		CacheAccountingFields: []string{"cache_hit_tokens", "cache_miss_tokens"},
		DefaultSummaryModel:   "gpt-5.4-mini",
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
			Models:           []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark"},
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
