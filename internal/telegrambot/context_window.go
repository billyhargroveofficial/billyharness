package telegrambot

import "github.com/billyhargroveofficial/billyharness/internal/modelinfo"

func effectiveContextWindowForModel(model string, fallback int64) int64 {
	info := modelinfo.Lookup(model)
	if info.ContextWindowTokens > 0 && info.Provider != "" {
		return info.ContextWindowTokens
	}
	if fallback > 0 {
		return fallback
	}
	return defaultContextWindowTokens
}
