package gateway

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
)

func (s *Server) sessionContextResponse(session *Session) SessionContextResponse {
	if session == nil {
		return clientux.BuildContextResponse(s.runtime, "", nil)
	}
	snapshot := s.sessionSnapshot(session)
	var events []protocol.Event
	var warnings []string
	if s != nil && s.store != nil {
		replayed, err := s.store.ReplayEventsAfter(session.ID, 0)
		if err != nil {
			warnings = append(warnings, "event replay unavailable: "+err.Error())
		} else {
			events = replayed
		}
	}
	messages := session.messages()
	status := session.Status()
	return clientux.BuildContextResponseWithOptions(snapshot.Runtime, session.ID, messages, clientux.ContextReportOptions{
		Runtime: gatewayapi.ContextRuntime{
			Provider:      snapshot.Provider.Provider.Provider,
			Model:         snapshot.Provider.Model.Model,
			Profile:       snapshot.Profile.Profile,
			ReasoningMode: snapshot.Provider.Model.ReasoningEffort,
			AccessMode:    snapshot.ToolPolicy.AccessMode,
		},
		Memory:       sessionMemoryContextStatus(instructionSettingsFromSessionSnapshot(snapshot), messages),
		ContextEpoch: status.ContextEpoch,
		ContextDrift: status.ContextDrift,
		Events:       events,
		Warnings:     warnings,
	})
}

func (s *Server) saveSession(session *Session) error {
	if s.store == nil {
		return nil
	}
	s.refreshSessionSnapshots(session)
	return s.store.Save(session)
}

func (s *Server) attachSessionStore(session *Session) {
	if s.store == nil || session == nil {
		return
	}
	session.eventRecorder = func(event protocol.Event) (protocol.Event, error) {
		stored, err := s.store.AppendEvent(session, event)
		if err != nil {
			return event, sessionPersistenceError{SessionID: session.ID, EventType: event.Type, Err: err}
		}
		return stored, nil
	}
}

func (s *Server) refreshSessionSnapshots(session *Session) {
	if s == nil || session == nil {
		return
	}
	snapshot := s.sessionSnapshot(session)
	var specs []protocol.ToolSpec
	if s.registry != nil {
		specs = s.registry.SnapshotWithToolPolicy(context.Background(), snapshot.ToolPolicy).Specs()
	}
	runtimeSnapshot := runstate.NewSnapshot(runstate.SnapshotInput{
		Provider:      snapshot.Provider,
		Profile:       snapshot.Profile,
		Runtime:       snapshot.Runtime,
		ToolPolicy:    snapshot.ToolPolicy,
		MCP:           snapshot.MCP,
		DocsIndexHash: docsIndexHash(instructionSettingsFromSessionSnapshot(snapshot)),
	}, session.messages(), specs)
	session.setStoreSnapshots(sessionStoreSnapshots{
		Config:        sessionConfigSnapshot(snapshot.ProviderAuth, snapshot.Runtime, snapshot.ToolPolicy, snapshot.MCP, snapshot.GatewayAddr),
		ModelProvider: runtimeSnapshot.Metadata(),
		MCP:           s.mcpSnapshot(snapshot.MCP),
	})
}

type sessionSnapshotProjection struct {
	ProviderAuth config.ProviderAuthSnapshot
	Provider     config.ProviderBinding
	Profile      config.ProfileSelection
	Runtime      config.RuntimeLimits
	ToolPolicy   config.ToolPolicySettings
	Diagnostics  config.DiagnosticsSettings
	MCP          config.MCPSettings
	GatewayAddr  string
}

func (s *Server) sessionSnapshot(session *Session) sessionSnapshotProjection {
	settings := s.runtimeDiffSettings()
	if session == nil {
		return sessionSnapshotFromRuntimeDiffSettings(settings, s.providerAuth)
	}
	status := session.Status()
	if next, err := config.RuntimeDiffSettingsWithRunOverrides(settings, config.RunOverrideSettings{
		Provider:        status.Provider,
		Model:           status.Model,
		Profile:         status.Profile,
		ReasoningEffort: status.ReasoningEffort,
		AccessMode:      status.AccessMode,
	}); err == nil {
		settings = next
	} else {
		log.Printf("gateway session snapshot runtime override failed id=%s: %v", session.ID, err)
	}
	return sessionSnapshotFromRuntimeDiffSettings(settings, s.providerAuth)
}

func sessionSnapshotFromRuntimeDiffSettings(settings config.RuntimeDiffSettings, providerAuth config.ProviderAuthSnapshot) sessionSnapshotProjection {
	providerAuth.Provider = settings.Provider.Provider.Provider
	providerAuth.Model = settings.Provider.Model.Model
	providerAuth.Profile = settings.Profile.Profile
	providerAuth.Thinking = settings.Provider.Model.Thinking
	providerAuth.ReasoningEffort = settings.Provider.Model.ReasoningEffort
	providerAuth.DisableSpark = settings.Provider.Model.DisableSpark
	providerAuth.BaseURL = settings.Provider.Provider.BaseURL
	providerAuth.APIKeyEnv = settings.Provider.Auth.APIKeyEnv
	providerAuth.CredentialFile = settings.Provider.Auth.CredentialFile
	providerAuth.CodexBaseURL = settings.Provider.Provider.CodexBaseURL
	providerAuth.CodexAuthFile = settings.Provider.Auth.CodexAuthFile
	providerAuth.CodexRefreshURL = settings.Provider.Auth.CodexRefreshURL
	providerAuth.CodexAuthAPIBaseURL = settings.Provider.Auth.CodexAuthAPIBaseURL
	providerAuth.CodexClientID = settings.Provider.Auth.CodexClientID
	providerAuth.CodexOriginator = settings.Provider.Auth.CodexOriginator
	return sessionSnapshotProjection{
		ProviderAuth: providerAuth,
		Provider:     settings.Provider,
		Profile:      settings.Profile,
		Runtime:      settings.Runtime,
		ToolPolicy:   settings.ToolPolicy,
		Diagnostics:  settings.Diagnostics,
		MCP:          settings.MCP,
		GatewayAddr:  settings.GatewayAddr,
	}
}

func sessionConfigSnapshot(providerAuth config.ProviderAuthSnapshot, limits config.RuntimeLimits, toolPolicy config.ToolPolicySettings, mcpSettings config.MCPSettings, gatewayAddr string) map[string]any {
	return map[string]any{
		"provider":                         providerAuth.Provider,
		"model":                            providerAuth.Model,
		"profile":                          providerAuth.Profile,
		"thinking":                         providerAuth.Thinking,
		"reasoning_effort":                 providerAuth.ReasoningEffort,
		"disable_spark":                    providerAuth.DisableSpark,
		"max_tokens":                       limits.MaxTokens,
		"max_tool_rounds":                  limits.MaxToolRounds,
		"max_tool_calls":                   limits.MaxToolCalls,
		"max_parallel_tools":               limits.MaxParallelTools,
		"provider_max_retries":             limits.ProviderMaxRetries,
		"context_window_tokens":            limits.ContextWindowTokens,
		"context_window_source":            limits.ContextWindowSource,
		"context_compact_tokens":           limits.ContextCompactTokens,
		"context_compact_source":           limits.ContextCompactSource,
		"context_compact_keep":             limits.ContextCompactKeep,
		"context_compact_max_chars":        limits.ContextCompactMaxChars,
		"context_compact_strategy":         limits.ContextCompactStrategy,
		"context_compact_summary_provider": limits.ContextCompactSummaryProvider,
		"context_compact_summary_model":    limits.ContextCompactSummaryModel,
		"web_summary_mode":                 toolPolicy.WebSummaryMode,
		"web_summary_provider":             toolPolicy.WebSummaryProvider,
		"web_summary_model":                toolPolicy.WebSummaryModel,
		"web_summary_max_input_tokens":     toolPolicy.WebSummaryMaxInputTokens,
		"web_summary_max_output_tokens":    toolPolicy.WebSummaryMaxOutputTokens,
		"web_cache_enabled":                toolPolicy.WebCacheEnabled,
		"web_cache_ttl_ms":                 toolPolicy.WebCacheTTL.Milliseconds(),
		"web_cache_max_bytes":              toolPolicy.WebCacheMaxBytes,
		"workspace_roots":                  append([]string(nil), toolPolicy.WorkspaceRoots...),
		"project_context_max_bytes":        toolPolicy.ProjectContextMaxBytes,
		"max_tool_output_bytes":            toolPolicy.MaxToolOutputBytes,
		"auto_approve_dangerous":           toolPolicy.AutoApproveDangerous,
		"access_mode":                      toolPolicy.AccessMode,
		"store_reasoning_content":          toolPolicy.StoreReasoningContent,
		"gateway_addr":                     gatewayAddr,
		"mcp_enabled":                      mcpSettings.Enabled,
		"mcp_config_files":                 append([]string(nil), mcpSettings.ConfigFiles...),
		"mcp_allowed_servers":              append([]string(nil), mcpSettings.AllowedServers...),
	}
}

func (s *Server) mcpSnapshot(mcpSettings config.MCPSettings) map[string]any {
	if s != nil && s.registry != nil {
		registrySettings := s.registry.MCPSettings()
		if len(registrySettings.Servers) > 0 || len(registrySettings.ConfigFiles) > 0 {
			mcpSettings = registrySettings
		}
	}
	var runtimeStatuses []any
	connected := 0
	if s.registry != nil {
		for _, status := range s.registry.MCPStatuses() {
			runtimeStatuses = append(runtimeStatuses, status)
			if status.Connected {
				connected++
			}
		}
	}
	return map[string]any{
		"enabled":        mcpSettings.Enabled,
		"config_files":   append([]string(nil), mcpSettings.ConfigFiles...),
		"allowed":        append([]string(nil), mcpSettings.AllowedServers...),
		"server_count":   len(mcpSettings.Servers),
		"status_count":   len(runtimeStatuses),
		"connected":      connected,
		"configured":     mcpServerSummaries(mcpSettings.Servers),
		"runtime_status": runtimeStatuses,
	}
}

func mcpServerSummaries(servers []config.MCPServer) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, server := range servers {
		transport := "stdio"
		if strings.TrimSpace(server.URL) != "" {
			transport = "http"
		}
		out = append(out, map[string]any{
			"name":           server.Name,
			"enabled":        server.Enabled,
			"required":       server.Required,
			"transport":      transport,
			"command":        filepath.Base(server.Command),
			"url_set":        strings.TrimSpace(server.URL) != "",
			"enabled_tools":  append([]string(nil), server.EnabledTools...),
			"disabled_tools": append([]string(nil), server.DisabledTools...),
		})
	}
	return out
}
