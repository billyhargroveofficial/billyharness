package telegrambot

import "github.com/billyhargroveofficial/billyharness/internal/modelinfo"

func effectiveContextWindowForModel(model string, fallback int64) int64 {
	return resolveContextWindowForModel(model, fallback, "").Tokens
}

type contextWindowResolution struct {
	Tokens int64
	Source string
}

func resolveContextWindowForModel(model string, fallback int64, source string) contextWindowResolution {
	if source == "override" && fallback > 0 {
		return contextWindowResolution{Tokens: fallback, Source: "override"}
	}
	info := modelinfo.Lookup(model)
	if info.ContextWindowTokens > 0 && info.Provider != "" {
		return contextWindowResolution{Tokens: info.ContextWindowTokens, Source: "model"}
	}
	if fallback > 0 {
		if source == "" {
			source = "fallback"
		}
		return contextWindowResolution{Tokens: fallback, Source: source}
	}
	return contextWindowResolution{Tokens: defaultContextWindowTokens, Source: "fallback"}
}
