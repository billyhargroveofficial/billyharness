package runtimeclient

import (
	"context"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Settings struct {
	ProviderBinding config.ProviderBinding
	Profile         config.ProfileSelection
	Runtime         config.RuntimeLimits
	ToolPolicy      config.ToolPolicySettings
	MCP             config.MCPSettings
	Hooks           config.HookSettings
	Instructions    config.InstructionSettings
}

func InitialMessages(settings config.InstructionSettings) []protocol.Message {
	return agent.InitialMessagesFromSettings(settings)
}

func RunLocal(ctx context.Context, settings Settings, messages []protocol.Message, prompt string, onEvent func(protocol.Event)) ([]protocol.Message, error) {
	prov, err := provider.NewFromBinding(settings.ProviderBinding)
	if err != nil {
		return nil, err
	}
	registry, err := tools.NewRegistryWithMCPFromSettings(ctx, registrySettings(settings), tools.WithWebSummarizer(provider.NewWebSummarizerFromProjections(settings.ProviderBinding, settings.ToolPolicy)))
	if err != nil {
		return nil, err
	}
	defer registry.Close()
	a := agent.NewFromSettings(agentSettings(settings), prov, registry)
	msgs := append([]protocol.Message(nil), messages...)
	msgs = append(msgs, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	return a.RunMessages(ctx, msgs, onEvent)
}

func MCPStatus(ctx context.Context, settings Settings) (mcpstatus.Response, error) {
	registry, err := tools.NewRegistryWithMCPFromSettings(ctx, registrySettings(settings), tools.WithWebSummarizer(provider.NewWebSummarizerFromProjections(settings.ProviderBinding, settings.ToolPolicy)))
	if err != nil {
		return mcpstatus.Response{}, err
	}
	defer registry.Close()
	mcpSettings := registry.MCPSettings()
	return mcpstatus.Response{
		ConfigFiles: mcpSettings.ConfigFiles,
		Allowed:     mcpSettings.AllowedServers,
		Enabled:     mcpSettings.Enabled,
		Servers:     registry.MCPStatuses(),
	}, nil
}

func registrySettings(settings Settings) tools.RegistrySettings {
	return tools.RegistrySettings{
		Provider:   settings.ProviderBinding,
		ToolPolicy: settings.ToolPolicy,
		MCP:        settings.MCP,
	}
}

func agentSettings(settings Settings) agent.Settings {
	return agent.Settings{
		ProviderBinding: settings.ProviderBinding,
		Profile:         settings.Profile,
		Runtime:         settings.Runtime,
		ToolPolicy:      settings.ToolPolicy,
		MCP:             settings.MCP,
		Hooks:           settings.Hooks,
		Instructions:    settings.Instructions,
	}
}
