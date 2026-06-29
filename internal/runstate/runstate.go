package runstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Submission struct {
	ID        string
	Prompt    string
	CreatedAt time.Time
}

type Run struct {
	ID           string
	SubmissionID string
	Status       string
	StartedAt    time.Time
	CompletedAt  time.Time
}

type Turn struct {
	ID           string
	RunID        string
	Round        int
	Status       string
	Snapshot     Snapshot
	MessageCount int
	StartedAt    time.Time
	CompletedAt  time.Time
}

type Step struct {
	ID           string
	TurnID       string
	ParentStepID string
	CallID       string
	AttemptID    string
	Kind         string
	Status       string
	Name         string
	Index        int
	MessageCount int
	StartedAt    time.Time
	CompletedAt  time.Time
}

type Snapshot struct {
	ProviderID              string `json:"provider_id"`
	ModelID                 string `json:"model_id"`
	ReasoningMode           string `json:"reasoning_mode,omitempty"`
	ContextBudgetTokens     int64  `json:"context_budget_tokens,omitempty"`
	ToolSnapshotHash        string `json:"tool_snapshot_hash,omitempty"`
	MCPStatusSnapshotHash   string `json:"mcp_status_snapshot_hash,omitempty"`
	ProfileInstructionHash  string `json:"profile_instruction_hash,omitempty"`
	DangerousPermissionMode string `json:"dangerous_permission_mode,omitempty"`
}

type SnapshotInput struct {
	Provider   config.ProviderBinding
	Profile    config.ProfileSelection
	Runtime    config.RuntimeLimits
	ToolPolicy config.ToolPolicySettings
	MCP        config.MCPSettings
}

func NewSnapshot(input SnapshotInput, messages []protocol.Message, specs []protocol.ToolSpec) Snapshot {
	return Snapshot{
		ProviderID:              input.Provider.Provider.Provider,
		ModelID:                 input.Provider.Model.Model,
		ReasoningMode:           reasoningMode(input.Provider.Model),
		ContextBudgetTokens:     input.Runtime.ContextWindowTokens,
		ToolSnapshotHash:        toolSnapshotHash(specs),
		MCPStatusSnapshotHash:   mcpSnapshotHash(input.MCP),
		ProfileInstructionHash:  instructionHash(input.Profile, messages),
		DangerousPermissionMode: dangerousPermissionMode(input.ToolPolicy),
	}
}

func (s Snapshot) Metadata() map[string]any {
	metadata := map[string]any{
		"provider_id":               s.ProviderID,
		"model_id":                  s.ModelID,
		"dangerous_permission_mode": s.DangerousPermissionMode,
	}
	if s.ReasoningMode != "" {
		metadata["reasoning_mode"] = s.ReasoningMode
	}
	if s.ContextBudgetTokens > 0 {
		metadata["context_budget_tokens"] = s.ContextBudgetTokens
	}
	if s.ToolSnapshotHash != "" {
		metadata["tool_snapshot_hash"] = s.ToolSnapshotHash
	}
	if s.MCPStatusSnapshotHash != "" {
		metadata["mcp_status_snapshot_hash"] = s.MCPStatusSnapshotHash
	}
	if s.ProfileInstructionHash != "" {
		metadata["profile_instruction_hash"] = s.ProfileInstructionHash
	}
	return metadata
}

func reasoningMode(model config.ModelSelection) string {
	thinking := strings.TrimSpace(model.Thinking)
	effort := strings.TrimSpace(model.ReasoningEffort)
	switch {
	case thinking != "" && effort != "":
		return thinking + "/" + effort
	case effort != "":
		return effort
	default:
		return thinking
	}
}

func dangerousPermissionMode(policy config.ToolPolicySettings) string {
	if policy.AutoApproveDangerous {
		return "auto_approve_dangerous"
	}
	return "safe_only"
}

func instructionHash(profile config.ProfileSelection, messages []protocol.Message) string {
	type instructionMessage struct {
		Role    protocol.Role `json:"role"`
		Name    string        `json:"name,omitempty"`
		Content string        `json:"content"`
	}
	payload := struct {
		Profile  string               `json:"profile,omitempty"`
		Messages []instructionMessage `json:"messages,omitempty"`
	}{Profile: profile.Profile}
	for _, msg := range messages {
		if msg.Role == protocol.RoleSystem {
			payload.Messages = append(payload.Messages, instructionMessage{Role: msg.Role, Name: msg.Name, Content: msg.Content})
		}
	}
	return hashJSON(payload)
}

func toolSnapshotHash(specs []protocol.ToolSpec) string {
	type toolSnapshot struct {
		Name         string        `json:"name"`
		Description  string        `json:"description,omitempty"`
		Parameters   string        `json:"parameters,omitempty"`
		Risk         protocol.Risk `json:"risk,omitempty"`
		SchemaSHA256 string        `json:"schema_sha256,omitempty"`
	}
	items := make([]toolSnapshot, 0, len(specs))
	for _, spec := range specs {
		items = append(items, toolSnapshot{
			Name:         spec.Name,
			Description:  spec.Description,
			Parameters:   string(spec.Parameters),
			Risk:         spec.Risk,
			SchemaSHA256: hashBytes(spec.Parameters),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return hashJSON(items)
}

func mcpSnapshotHash(settings config.MCPSettings) string {
	type serverSnapshot struct {
		Name      string   `json:"name"`
		Enabled   bool     `json:"enabled"`
		Required  bool     `json:"required,omitempty"`
		Transport string   `json:"transport,omitempty"`
		Command   string   `json:"command,omitempty"`
		URLSet    bool     `json:"url_set,omitempty"`
		ToolsOn   []string `json:"enabled_tools,omitempty"`
		ToolsOff  []string `json:"disabled_tools,omitempty"`
	}
	payload := struct {
		Enabled        bool             `json:"enabled"`
		ConfigFiles    []string         `json:"config_files,omitempty"`
		AllowedServers []string         `json:"allowed_servers,omitempty"`
		Servers        []serverSnapshot `json:"servers,omitempty"`
	}{
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
		payload.Servers = append(payload.Servers, serverSnapshot{
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
	return hashJSON(payload)
}

func hashJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
