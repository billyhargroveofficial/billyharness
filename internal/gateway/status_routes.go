package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		OK:       true,
		Provider: s.providerAuth.Provider,
		Model:    s.providerAuth.Model,
	})
}

func (s *Server) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	resp := s.readinessResponse()
	status := http.StatusOK
	if !resp.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.registry.Specs())
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, credentials.CurrentStatusForRuntime(
		s.providerBinding.Auth,
		s.providerBinding.Provider.Provider,
		s.providerBinding.Model.Model,
	))
}

func (s *Server) handleConfigStatus(w http.ResponseWriter, _ *http.Request) {
	_, resolved, err := config.ResolveEffectiveFromBase(func(base config.Config) []config.ResolveOverride {
		return config.RuntimeDiffOverridesFromSettings(base, s.runtimeDiffSettings(), config.SourceGateway)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := publicConfigStatusResponse(resolved)
	if resp.Diagnostics != nil {
		resp.Diagnostics["agent_club"] = s.agentClubStatus
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeepSeekAuth(w http.ResponseWriter, r *http.Request) {
	var req DeepSeekAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	status, err := s.auth.SaveDeepSeekAPIKey(req.APIKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deepseek": status,
	})
}

func (s *Server) handleCodexImport(w http.ResponseWriter, r *http.Request) {
	var req CodexImportRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	var (
		status credentials.ProviderStatus
		err    error
	)
	if len(req.AuthJSON) > 0 {
		status, err = s.auth.SaveCodexAuthJSON(req.AuthJSON)
	} else {
		status, err = s.auth.ImportCodexAuth(req.SourcePath)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"codex": status,
	})
}

func (s *Server) handleMCP(w http.ResponseWriter, _ *http.Request) {
	mcpSettings := s.registry.MCPSettings()
	writeJSON(w, http.StatusOK, mcpstatus.Response{
		Source:       "runtime config",
		ConfigFiles:  mcpSettings.ConfigFiles,
		Allowed:      mcpSettings.AllowedServers,
		Enabled:      mcpSettings.Enabled,
		Servers:      s.registry.MCPStatuses(),
		Prompts:      s.registry.MCPPrompts(),
		Instructions: s.registry.MCPServerInstructions(),
	})
}

func (s *Server) handleManagedProcesses(w http.ResponseWriter, r *http.Request) {
	includeExited := parseBoolQuery(r.URL.Query().Get("include_exited"))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	processes := s.registry.ManagedShellProcesses(includeExited, limit)
	writeJSON(w, http.StatusOK, ManagedProcessResponse{
		Processes: processes,
		Text:      tools.FormatManagedShellProcesses(processes),
	})
}

func parseBoolQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
