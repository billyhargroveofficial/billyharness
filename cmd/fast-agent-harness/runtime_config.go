package main

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func resolveRuntimeConfig(overrides ...config.ResolveOverride) (config.Config, error) {
	resolved, err := config.ResolveStrict(overrides...)
	if err != nil {
		return config.Config{}, err
	}
	return resolved.Config, nil
}

func appendStringOverride(overrides []config.ResolveOverride, key, value, sourceKey string) []config.ResolveOverride {
	if strings.TrimSpace(value) == "" {
		return overrides
	}
	return append(overrides, config.ResolveOverride{
		Key:       key,
		Value:     value,
		Source:    config.SourceCLI,
		SourceKey: sourceKey,
	})
}
