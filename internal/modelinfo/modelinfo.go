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
	Model        string
	Provider     string
	Subscription bool
	Pricing      Pricing
	Known        bool
}

func Lookup(model string) Info {
	model = NormalizeAlias(model)
	switch model {
	case "deepseek-v4-flash":
		return Info{
			Model:    model,
			Provider: ProviderDeepSeek,
			Pricing:  Pricing{CacheHitPer1M: 0.0028, CacheMissPer1M: 0.14, InputPer1M: 0.14, OutputPer1M: 0.28},
			Known:    true,
		}
	case "deepseek-v4-pro":
		return Info{
			Model:    model,
			Provider: ProviderDeepSeek,
			Pricing:  Pricing{CacheHitPer1M: 0.003625, CacheMissPer1M: 0.435, InputPer1M: 0.435, OutputPer1M: 0.87},
			Known:    true,
		}
	case "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark":
		return Info{Model: model, Provider: ProviderOpenAICodex, Subscription: true, Known: true}
	default:
		if IsCodexModel(model) {
			return Info{Model: model, Provider: ProviderOpenAICodex, Subscription: true}
		}
		if IsDeepSeekModel(model) {
			return Info{Model: model, Provider: ProviderDeepSeek}
		}
		return Info{Model: model}
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

func IsDeepSeekModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-")
}
