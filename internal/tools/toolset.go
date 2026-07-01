package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

// ToolSet is an immutable per-provider-turn view of model-visible tools,
// execution handlers, policy metadata, and the dynamic MCP catalog mirror.
type ToolSet struct {
	registry        *Registry
	mcpSnapshotHash string
}

type mcpStatusSnapshot struct {
	Name              string `json:"name"`
	Transport         string `json:"transport,omitempty"`
	Enabled           bool   `json:"enabled"`
	Required          bool   `json:"required"`
	Connected         bool   `json:"connected"`
	State             string `json:"state,omitempty"`
	ToolCount         int    `json:"tool_count,omitempty"`
	UnsupportedReason string `json:"unsupported_reason,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	Error             string `json:"error,omitempty"`
	RetryCount        int    `json:"retry_count,omitempty"`
	RestartCount      int    `json:"restart_count,omitempty"`
}

type mcpToolSnapshot struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Risk         protocol.Risk `json:"risk,omitempty"`
	SchemaSHA256 string        `json:"schema_sha256,omitempty"`
}

type mcpServerSettingsSnapshot struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Required  bool     `json:"required,omitempty"`
	Transport string   `json:"transport,omitempty"`
	Command   string   `json:"command,omitempty"`
	URLSet    bool     `json:"url_set,omitempty"`
	ToolsOn   []string `json:"enabled_tools,omitempty"`
	ToolsOff  []string `json:"disabled_tools,omitempty"`
}

type mcpSettingsHashSnapshot struct {
	Enabled        bool                        `json:"enabled"`
	ConfigFiles    []string                    `json:"config_files,omitempty"`
	AllowedServers []string                    `json:"allowed_servers,omitempty"`
	Servers        []mcpServerSettingsSnapshot `json:"servers,omitempty"`
}

func (r *Registry) Snapshot(ctx context.Context) ToolSet {
	if r == nil {
		return ToolSet{}
	}
	return r.SnapshotWithToolPolicy(ctx, r.toolPolicy)
}

func (r *Registry) SnapshotWithToolPolicy(ctx context.Context, policy config.ToolPolicySettings) ToolSet {
	if r == nil {
		return ToolSet{}
	}
	r.refreshMCPTools(ctx)

	snapshot := &Registry{
		toolPolicy:      cloneToolPolicySettings(policy),
		mcpSettings:     cloneMCPSettings(r.mcpSettings),
		tools:           cloneToolMap(r.tools),
		mcpTools:        map[string]Tool{},
		webSummarizer:   r.webSummarizer,
		webSummarySlots: r.webSummarySlots,
		webSummarySeq:   r.webSummarySeq,
	}
	r.mcpMu.RLock()
	snapshot.mcpTools = cloneToolMap(r.mcpTools)
	snapshot.mcpCatalog = cloneMCPCatalogState(r.mcpCatalog)
	snapshot.instructions = append([]string(nil), r.instructions...)
	r.mcpMu.RUnlock()
	snapshot.mcpStatuses = cloneMCPStatuses(r.MCPStatuses())
	snapshot.mcpCatalog.Kind = "dynamic_mcp_catalog"
	snapshot.mcpCatalog.ToolCount = len(snapshot.mcpTools)
	snapshot.mcpCatalog.ModelVisible = false

	if _, ok := snapshot.tools["tool_search"]; ok {
		snapshot.addToolSearch()
	}
	if _, hasList := snapshot.tools["mcp_list_tools"]; hasList {
		snapshot.addMCPGateway()
	} else if _, hasCall := snapshot.tools["mcp_call"]; hasCall {
		snapshot.addMCPGateway()
	}
	return ToolSet{registry: snapshot, mcpSnapshotHash: snapshot.mcpSnapshotHash()}
}

func (s ToolSet) Specs() []protocol.ToolSpec {
	if s.registry == nil {
		return nil
	}
	return s.registry.Specs()
}

func (s ToolSet) Call(ctx context.Context, call protocol.ToolCall) (Result, error) {
	if s.registry == nil {
		return errorResult("tool_registry_unavailable", "tool registry unavailable"), nil
	}
	return s.registry.Call(ctx, call)
}

func (s ToolSet) CanRunParallel(name string) bool {
	return s.registry != nil && s.registry.CanRunParallel(name)
}

func (s ToolSet) ParallelMetadata(name string) (ParallelMetadata, bool) {
	if s.registry == nil {
		return ParallelMetadata{}, false
	}
	return s.registry.ParallelMetadata(name)
}

func (s ToolSet) Risk(name string) (protocol.Risk, bool) {
	if s.registry == nil {
		return "", false
	}
	return s.registry.Risk(name)
}

func (s ToolSet) PolicyDecision(name string) PolicyDecision {
	if s.registry == nil {
		return PolicyDecision{
			Name:     name,
			Decision: "allow",
			Source:   "auto",
			Reason:   "tool_registry_unavailable",
		}
	}
	return s.registry.PolicyDecision(name)
}

func (s ToolSet) MCPStatusSnapshotHash() string {
	return s.mcpSnapshotHash
}

func cloneToolMap(in map[string]Tool) map[string]Tool {
	out := make(map[string]Tool, len(in))
	for name, tool := range in {
		out[name] = cloneTool(tool)
	}
	return out
}

func cloneTool(tool Tool) Tool {
	tool.Spec.Parameters = append(json.RawMessage(nil), tool.Spec.Parameters...)
	tool.Parallel = normalizeParallelMetadata(tool.Spec.Name, tool.Spec.Risk, tool.Parallel)
	return tool
}

func cloneMCPCatalogState(state mcpCatalogState) mcpCatalogState {
	state.Collisions = append([]string(nil), state.Collisions...)
	return state
}

func cloneMCPStatuses(in []mcpclient.ServerStatus) []mcpclient.ServerStatus {
	out := make([]mcpclient.ServerStatus, len(in))
	for i, status := range in {
		out[i] = status
		out[i].StartedAt = cloneTimePtr(status.StartedAt)
		out[i].LastConnectedAt = cloneTimePtr(status.LastConnectedAt)
		out[i].LastEventAt = cloneTimePtr(status.LastEventAt)
		out[i].LastErrorAt = cloneTimePtr(status.LastErrorAt)
		out[i].NextRetryAt = cloneTimePtr(status.NextRetryAt)
	}
	return out
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func (r *Registry) mcpSnapshotHash() string {
	if r == nil {
		return ""
	}
	payload := struct {
		Settings mcpSettingsHashSnapshot `json:"settings"`
		Catalog  mcpCatalogState         `json:"catalog"`
		Tools    []mcpToolSnapshot       `json:"tools,omitempty"`
		Statuses []mcpStatusSnapshot     `json:"statuses,omitempty"`
	}{
		Settings: mcpSettingsSnapshot(r.mcpSettings),
		Catalog:  cloneMCPCatalogState(r.mcpCatalogSnapshot()),
	}
	mcpTools := r.mcpToolsSnapshot()
	names := make([]string, 0, len(mcpTools))
	for name := range mcpTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tool := mcpTools[name]
		hash := sha256.Sum256(tool.Spec.Parameters)
		payload.Tools = append(payload.Tools, mcpToolSnapshot{
			Name:         tool.Spec.Name,
			Description:  tool.Spec.Description,
			Risk:         tool.Spec.Risk,
			SchemaSHA256: hex.EncodeToString(hash[:]),
		})
	}
	statuses := r.MCPStatuses()
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	for _, status := range statuses {
		payload.Statuses = append(payload.Statuses, mcpStatusSnapshot{
			Name:              status.Name,
			Transport:         status.Transport,
			Enabled:           status.Enabled,
			Required:          status.Required,
			Connected:         status.Connected,
			State:             status.State,
			ToolCount:         status.ToolCount,
			UnsupportedReason: status.UnsupportedReason,
			LastError:         status.LastError,
			Error:             status.Error,
			RetryCount:        status.RetryCount,
			RestartCount:      status.RestartCount,
		})
	}
	bytes, _ := json.Marshal(payload)
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:])
}

func mcpSettingsSnapshot(settings config.MCPSettings) mcpSettingsHashSnapshot {
	payload := mcpSettingsHashSnapshot{
		Enabled:        settings.Enabled,
		ConfigFiles:    append([]string(nil), settings.ConfigFiles...),
		AllowedServers: append([]string(nil), settings.AllowedServers...),
	}
	for i := range payload.ConfigFiles {
		payload.ConfigFiles[i] = filepath.Clean(payload.ConfigFiles[i])
	}
	sort.Strings(payload.ConfigFiles)
	sort.Strings(payload.AllowedServers)
	for _, server := range settings.Servers {
		transport := "stdio"
		if strings.TrimSpace(server.URL) != "" {
			transport = "http"
		}
		on := append([]string(nil), server.EnabledTools...)
		off := append([]string(nil), server.DisabledTools...)
		sort.Strings(on)
		sort.Strings(off)
		payload.Servers = append(payload.Servers, mcpServerSettingsSnapshot{
			Name:      server.Name,
			Enabled:   server.Enabled,
			Required:  server.Required,
			Transport: transport,
			Command:   filepath.Base(server.Command),
			URLSet:    strings.TrimSpace(server.URL) != "",
			ToolsOn:   on,
			ToolsOff:  off,
		})
	}
	sort.Slice(payload.Servers, func(i, j int) bool {
		return payload.Servers[i].Name < payload.Servers[j].Name
	})
	return payload
}
