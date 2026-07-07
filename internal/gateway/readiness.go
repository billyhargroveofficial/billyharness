package gateway

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

const (
	readinessOK   = "ok"
	readinessWarn = "warn"
	readinessFail = "fail"
)

func (s *Server) readinessResponse() ReadinessResponse {
	checks := make([]ReadinessCheck, 0, 4)
	checks = append(checks, s.providerReadinessCheck())

	toolsStatus, toolCheck := s.toolCatalogReadiness()
	checks = append(checks, toolCheck)

	mcpStatus, mcpCheck := s.mcpReadiness()
	checks = append(checks, mcpCheck)

	agentClubStatus, agentClubCheck := s.agentClubReadiness()
	checks = append(checks, agentClubCheck)

	var sessionStore *gatewayapi.SessionStoreHealth
	storeCheck, storeHealth := s.sessionStoreReadiness()
	checks = append(checks, storeCheck)
	if storeHealth != nil {
		sessionStore = storeHealth
	}

	resp := ReadinessResponse{
		OK:           readinessChecksOK(checks),
		Provider:     s.providerAuth.Provider,
		Model:        s.providerAuth.Model,
		GatewayAddr:  s.gatewayAddr,
		Checks:       checks,
		Tools:        toolsStatus,
		MCP:          mcpStatus,
		AgentClub:    agentClubStatus,
		SessionStore: sessionStore,
	}
	return resp
}

func (s *Server) agentClubReadiness() (gatewayapi.AgentClubReadinessStatus, ReadinessCheck) {
	status := s.agentClubStatus
	if status.ConfiguredFileCount == 0 && len(s.agentClubConfig.ConfigFiles) > 0 {
		status.ConfiguredFileCount = len(s.agentClubConfig.ConfigFiles)
	}
	if s.agentClub != nil {
		status.Configured = true
		summary := s.agentClub.Summary()
		if status.CapabilityCount == 0 {
			status.CapabilityCount = summary.CapabilityCount
		}
		if status.BindingCount == 0 {
			status.BindingCount = summary.BindingCount
		}
		if status.EnabledBindingCount == 0 {
			status.EnabledBindingCount = summary.EnabledBindingCount
		}
		if status.TriggerCount == 0 {
			status.TriggerCount = summary.TriggerCount
		}
		if status.EnabledTriggerCount == 0 {
			status.EnabledTriggerCount = summary.EnabledTriggerCount
		}
	}
	if !status.Configured && status.ConfiguredFileCount == 0 {
		return status, ReadinessCheck{Name: "agentclub_registry", Status: readinessOK, Detail: "no config files"}
	}
	detail := fmt.Sprintf("files=%d capabilities=%d enabled_bindings=%d enabled_triggers=%d missing_secrets=%d",
		status.ConfiguredFileCount,
		status.CapabilityCount,
		status.EnabledBindingCount,
		status.EnabledTriggerCount,
		status.MissingSecretEnvCount,
	)
	if status.MissingSecretEnvCount > 0 {
		return status, ReadinessCheck{Name: "agentclub_registry", Status: readinessWarn, Detail: detail}
	}
	return status, ReadinessCheck{Name: "agentclub_registry", Status: readinessOK, Detail: detail}
}

func (s *Server) providerReadinessCheck() ReadinessCheck {
	provider := strings.TrimSpace(s.providerAuth.Provider)
	model := strings.TrimSpace(s.providerAuth.Model)
	if provider == "" || model == "" {
		return ReadinessCheck{
			Name:   "effective_config",
			Status: readinessWarn,
			Detail: "provider/model not fully set",
		}
	}
	return ReadinessCheck{
		Name:   "effective_config",
		Status: readinessOK,
		Detail: "provider=" + provider + " model=" + model,
	}
}

func (s *Server) toolCatalogReadiness() (ReadinessCatalogStatus, ReadinessCheck) {
	count := 0
	if s.registry != nil {
		count = len(s.registry.Specs())
	}
	status := ReadinessCatalogStatus{Count: count}
	if count == 0 {
		return status, ReadinessCheck{Name: "tool_catalog", Status: readinessFail, Detail: "no visible tools"}
	}
	return status, ReadinessCheck{Name: "tool_catalog", Status: readinessOK, Detail: fmt.Sprintf("%d visible tools", count)}
}

func (s *Server) mcpReadiness() (ReadinessMCPStatus, ReadinessCheck) {
	settings := s.mcpSettings
	var statuses []mcpServerReadinessStatus
	if s.registry != nil {
		settings = s.registry.MCPSettings()
		for _, status := range s.registry.MCPStatuses() {
			statuses = append(statuses, mcpServerReadinessStatus{
				Enabled:        status.Enabled,
				Required:       status.Required,
				Connected:      status.Connected,
				TransportState: status.TransportState,
				CatalogState:   status.CatalogState,
				ToolCount:      status.ToolCount,
				Error:          status.Error,
			})
		}
	}
	summary := ReadinessMCPStatus{
		Enabled:    settings.Enabled,
		Configured: len(statuses),
	}
	if summary.Configured == 0 && len(settings.Servers) > 0 {
		summary.Configured = len(settings.Servers)
	}
	if !settings.Enabled {
		return summary, ReadinessCheck{Name: "mcp_catalog", Status: readinessOK, Detail: "disabled"}
	}
	if len(statuses) == 0 && len(settings.Servers) > 0 {
		for _, server := range settings.Servers {
			if server.Required {
				summary.RequiredFailures++
			} else {
				summary.OptionalWarnings++
			}
		}
		detail := fmt.Sprintf("configured=%d connected=0 tools=0 no runtime status", summary.Configured)
		if summary.RequiredFailures > 0 {
			return summary, ReadinessCheck{Name: "mcp_catalog", Status: readinessFail, Detail: fmt.Sprintf("%s required_failures=%d", detail, summary.RequiredFailures)}
		}
		return summary, ReadinessCheck{Name: "mcp_catalog", Status: readinessWarn, Detail: fmt.Sprintf("%s optional_warnings=%d", detail, summary.OptionalWarnings)}
	}
	for _, status := range statuses {
		if status.Connected {
			summary.Connected++
		}
		if status.ToolCount > 0 {
			summary.ToolCount += status.ToolCount
		}
		if mcpServerReadinessFailed(status.Enabled, status.Connected, status.TransportState, status.CatalogState, status.Error) {
			if status.Required {
				summary.RequiredFailures++
			} else {
				summary.OptionalWarnings++
			}
			continue
		}
		if mcpServerReadinessWarn(status.CatalogState) {
			summary.OptionalWarnings++
		}
	}
	detail := fmt.Sprintf("configured=%d connected=%d tools=%d", summary.Configured, summary.Connected, summary.ToolCount)
	if summary.RequiredFailures > 0 {
		return summary, ReadinessCheck{Name: "mcp_catalog", Status: readinessFail, Detail: fmt.Sprintf("%s required_failures=%d", detail, summary.RequiredFailures)}
	}
	if summary.OptionalWarnings > 0 {
		return summary, ReadinessCheck{Name: "mcp_catalog", Status: readinessWarn, Detail: fmt.Sprintf("%s optional_warnings=%d", detail, summary.OptionalWarnings)}
	}
	return summary, ReadinessCheck{Name: "mcp_catalog", Status: readinessOK, Detail: detail}
}

func (s *Server) sessionStoreReadiness() (ReadinessCheck, *gatewayapi.SessionStoreHealth) {
	if s.store == nil {
		return ReadinessCheck{Name: "session_store", Status: readinessWarn, Detail: "disabled"}, nil
	}
	storeHealth := s.storeHealth
	storeHealth.Enabled = true
	status := readinessOK
	detail := fmt.Sprintf("loaded=%d errors=%d corrupt=%d", storeHealth.LoadedCount, storeHealth.ErrorCount, storeHealth.CorruptCount)
	if storeHealth.ErrorCount > 0 || storeHealth.CorruptCount > 0 {
		status = readinessFail
	}
	return ReadinessCheck{Name: "session_store", Status: status, Detail: detail}, &storeHealth
}

type mcpServerReadinessStatus struct {
	Enabled        bool
	Required       bool
	Connected      bool
	TransportState string
	CatalogState   string
	ToolCount      int
	Error          string
}

func readinessChecksOK(checks []ReadinessCheck) bool {
	for _, check := range checks {
		if strings.EqualFold(check.Status, readinessFail) {
			return false
		}
	}
	return true
}

func mcpServerReadinessFailed(enabled, connected bool, transportState, catalogState, errText string) bool {
	if !enabled {
		return false
	}
	transport := strings.ToLower(strings.TrimSpace(transportState))
	catalog := strings.ToLower(strings.TrimSpace(catalogState))
	switch transport {
	case "failed", "crashed", "unsupported":
		return true
	}
	switch catalog {
	case "tools_fetch_failed", "unsupported":
		return true
	}
	if !connected {
		return true
	}
	return strings.TrimSpace(errText) != ""
}

func mcpServerReadinessWarn(catalogState string) bool {
	switch strings.ToLower(strings.TrimSpace(catalogState)) {
	case "catalog_stale", "degraded":
		return true
	default:
		return false
	}
}
