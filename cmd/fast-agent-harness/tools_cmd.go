package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func printTools() error {
	cfg := config.Default()
	registry, err := newToolRegistry(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(registry.Specs())
}

func newToolRegistry(ctx context.Context, cfg config.Config) (*tools.Registry, error) {
	settings := tools.RegistrySettingsFromConfig(cfg)
	return tools.NewRegistryWithMCPFromSettings(ctx, settings, tools.WithWebSummarizer(provider.NewWebSummarizerFromProjections(settings.Provider, settings.ToolPolicy)))
}

func newToolRegistryNoMCP(cfg config.Config) *tools.Registry {
	settings := tools.RegistrySettingsFromConfig(cfg)
	return tools.NewRegistryFromSettings(settings, tools.WithWebSummarizer(provider.NewWebSummarizerFromProjections(settings.Provider, settings.ToolPolicy)))
}
