package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func printTools() error {
	cfg, err := resolveRuntimeConfig()
	if err != nil {
		return err
	}
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
	return runtimehost.NewRegistry(ctx, runtimehost.SettingsFromConfig(cfg))
}

func newToolRegistryNoMCP(cfg config.Config) *tools.Registry {
	return runtimehost.NewRegistryNoMCP(runtimehost.SettingsFromConfig(cfg))
}
