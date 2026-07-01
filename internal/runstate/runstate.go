package runstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	ProviderID              string                     `json:"provider_id"`
	ModelID                 string                     `json:"model_id"`
	ReasoningMode           string                     `json:"reasoning_mode,omitempty"`
	ContextBudgetTokens     int64                      `json:"context_budget_tokens,omitempty"`
	ToolSnapshotHash        string                     `json:"tool_snapshot_hash,omitempty"`
	MCPStatusSnapshotHash   string                     `json:"mcp_status_snapshot_hash,omitempty"`
	ProfileInstructionHash  string                     `json:"profile_instruction_hash,omitempty"`
	PromptInventory         *protocol.PromptInventory  `json:"prompt_inventory,omitempty"`
	PromptCacheBreak        *protocol.PromptCacheBreak `json:"prompt_cache_break,omitempty"`
	DangerousPermissionMode string                     `json:"dangerous_permission_mode,omitempty"`
	AccessMode              string                     `json:"access_mode,omitempty"`
}

type SnapshotInput struct {
	Provider              config.ProviderBinding
	Profile               config.ProfileSelection
	Runtime               config.RuntimeLimits
	ToolPolicy            config.ToolPolicySettings
	MCP                   config.MCPSettings
	MCPStatusSnapshotHash string
}

func NewSnapshot(input SnapshotInput, messages []protocol.Message, specs []protocol.ToolSpec) Snapshot {
	mcpHash := input.MCPStatusSnapshotHash
	if mcpHash == "" {
		mcpHash = mcpSnapshotHash(input.MCP)
	}
	inventory := promptInventory(messages, specs)
	return Snapshot{
		ProviderID:              input.Provider.Provider.Provider,
		ModelID:                 input.Provider.Model.Model,
		ReasoningMode:           reasoningMode(input.Provider.Model),
		ContextBudgetTokens:     input.Runtime.ContextWindowTokens,
		ToolSnapshotHash:        toolSnapshotHash(specs),
		MCPStatusSnapshotHash:   mcpHash,
		ProfileInstructionHash:  instructionHash(input.Profile, messages),
		PromptInventory:         inventory,
		DangerousPermissionMode: dangerousPermissionMode(input.ToolPolicy),
		AccessMode:              config.NormalizeAccessMode(input.ToolPolicy.AccessMode),
	}
}

func (s Snapshot) WithPromptCacheBreak(previous *Snapshot) Snapshot {
	s.PromptCacheBreak = PromptCacheBreak(previous, s)
	return s
}

func PromptCacheBreak(previous *Snapshot, current Snapshot) *protocol.PromptCacheBreak {
	currentSignature := promptCacheSignature(current)
	if previous == nil {
		return &protocol.PromptCacheBreak{
			Status:           "initial",
			Reason:           "initial_request",
			CurrentSignature: currentSignature,
		}
	}
	previousSignature := promptCacheSignature(*previous)
	if previousSignature == currentSignature {
		return &protocol.PromptCacheBreak{
			Status:            "unchanged",
			Reason:            "unchanged",
			PreviousSignature: previousSignature,
			CurrentSignature:  currentSignature,
		}
	}
	changed := promptCacheChangedFields(*previous, current)
	return &protocol.PromptCacheBreak{
		Status:            "changed",
		Reason:            strings.Join(changed, ","),
		ChangedFields:     changed,
		PreviousSignature: previousSignature,
		CurrentSignature:  currentSignature,
	}
}

func (s Snapshot) Metadata() map[string]any {
	metadata := map[string]any{
		"provider_id":               s.ProviderID,
		"model_id":                  s.ModelID,
		"dangerous_permission_mode": s.DangerousPermissionMode,
		"access_mode":               s.AccessMode,
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
	if s.PromptInventory != nil {
		metadata["prompt_inventory"] = s.PromptInventory
		if s.PromptInventory.Hash != "" {
			metadata["prompt_inventory_hash"] = s.PromptInventory.Hash
		}
		if s.PromptInventory.TotalBytes > 0 {
			metadata["prompt_inventory_bytes"] = s.PromptInventory.TotalBytes
		}
		if s.PromptInventory.ApproxTokens > 0 {
			metadata["prompt_inventory_approx_tokens"] = s.PromptInventory.ApproxTokens
		}
	}
	if s.PromptCacheBreak != nil {
		metadata["prompt_cache_break"] = s.PromptCacheBreak
		if s.PromptCacheBreak.Status != "" {
			metadata["prompt_cache_status"] = s.PromptCacheBreak.Status
		}
		if s.PromptCacheBreak.Reason != "" {
			metadata["prompt_cache_reason"] = s.PromptCacheBreak.Reason
		}
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
	switch config.NormalizeAccessMode(policy.AccessMode) {
	case config.AccessModePlan:
		return "plan_mode_read_only"
	case config.AccessModeGuarded:
		return "guarded_mode"
	}
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
		if instructionHashMessage(msg) {
			payload.Messages = append(payload.Messages, instructionMessage{Role: msg.Role, Name: msg.Name, Content: msg.Content})
		}
	}
	return hashJSON(payload)
}

func instructionHashMessage(msg protocol.Message) bool {
	if msg.Role == protocol.RoleSystem {
		return true
	}
	if msg.Role != protocol.RoleUser {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, "# Project context") ||
		strings.HasPrefix(content, "# AGENTS.md instructions") ||
		strings.HasPrefix(content, "# MCP server instructions")
}

func promptInventory(messages []protocol.Message, specs []protocol.ToolSpec) *protocol.PromptInventory {
	sections := make([]protocol.PromptSection, 0, len(messages)+1)
	totalBytes := 0
	totalTokens := 0
	for i, msg := range messages {
		name, ok := promptSectionName(msg)
		if !ok {
			continue
		}
		byteCount := len([]byte(msg.Content))
		approxTokens := approxTokensForText(msg.Content)
		totalBytes += byteCount
		totalTokens += approxTokens
		sections = append(sections, protocol.PromptSection{
			Name:         name,
			Role:         msg.Role,
			Index:        i,
			ByteCount:    byteCount,
			ApproxTokens: approxTokens,
			SHA256:       hashBytes([]byte(msg.Content)),
		})
	}
	if len(specs) > 0 {
		byteCount := toolSchemaBytes(specs)
		approxTokens := approxTokensForBytes(byteCount)
		totalBytes += byteCount
		totalTokens += approxTokens
		sections = append(sections, protocol.PromptSection{
			Name:         "tool_schemas",
			Index:        -1,
			ByteCount:    byteCount,
			ApproxTokens: approxTokens,
			SHA256:       toolSnapshotHash(specs),
		})
	}
	if len(sections) == 0 {
		return nil
	}
	inventory := &protocol.PromptInventory{
		Sections:        sections,
		ToolSchemaCount: len(specs),
		TotalBytes:      totalBytes,
		ApproxTokens:    totalTokens,
	}
	inventory.Hash = hashJSON(struct {
		Sections        []protocol.PromptSection `json:"sections,omitempty"`
		ToolSchemaCount int                      `json:"tool_schema_count,omitempty"`
		TotalBytes      int                      `json:"total_bytes,omitempty"`
		ApproxTokens    int                      `json:"approx_tokens,omitempty"`
	}{
		Sections:        inventory.Sections,
		ToolSchemaCount: inventory.ToolSchemaCount,
		TotalBytes:      inventory.TotalBytes,
		ApproxTokens:    inventory.ApproxTokens,
	})
	return inventory
}

func promptSectionName(msg protocol.Message) (string, bool) {
	content := strings.TrimSpace(msg.Content)
	if msg.Role == protocol.RoleSystem {
		if strings.HasPrefix(content, "# Billyharness profile") {
			return "profile", true
		}
		return "system_prompt", content != ""
	}
	if msg.Role != protocol.RoleUser {
		return "", false
	}
	switch {
	case strings.HasPrefix(content, "# Project context"):
		return "project_context", true
	case strings.HasPrefix(content, "# AGENTS.md instructions"):
		return "agents_instructions", true
	case strings.HasPrefix(content, "# MCP server instructions"):
		return "mcp_instructions", true
	case strings.HasPrefix(content, "# user_prompt_submit hook context"):
		return "prompt_hook_context", true
	default:
		return "", false
	}
}

func toolSchemaBytes(specs []protocol.ToolSpec) int {
	total := 0
	for _, spec := range specs {
		total += len([]byte(spec.Name))
		total += len([]byte(spec.Description))
		total += len(spec.Parameters)
	}
	return total
}

func approxTokensForText(text string) int {
	return approxTokensForBytes(utf8.RuneCountInString(text))
}

func approxTokensForBytes(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func promptCacheSignature(s Snapshot) string {
	return hashJSON(struct {
		ProviderID              string `json:"provider_id,omitempty"`
		ModelID                 string `json:"model_id,omitempty"`
		ReasoningMode           string `json:"reasoning_mode,omitempty"`
		ContextBudgetTokens     int64  `json:"context_budget_tokens,omitempty"`
		ToolSnapshotHash        string `json:"tool_snapshot_hash,omitempty"`
		MCPStatusSnapshotHash   string `json:"mcp_status_snapshot_hash,omitempty"`
		ProfileInstructionHash  string `json:"profile_instruction_hash,omitempty"`
		PromptInventoryHash     string `json:"prompt_inventory_hash,omitempty"`
		DangerousPermissionMode string `json:"dangerous_permission_mode,omitempty"`
		AccessMode              string `json:"access_mode,omitempty"`
	}{
		ProviderID:              s.ProviderID,
		ModelID:                 s.ModelID,
		ReasoningMode:           s.ReasoningMode,
		ContextBudgetTokens:     s.ContextBudgetTokens,
		ToolSnapshotHash:        s.ToolSnapshotHash,
		MCPStatusSnapshotHash:   s.MCPStatusSnapshotHash,
		ProfileInstructionHash:  s.ProfileInstructionHash,
		PromptInventoryHash:     promptInventoryHash(s.PromptInventory),
		DangerousPermissionMode: s.DangerousPermissionMode,
		AccessMode:              s.AccessMode,
	})
}

func promptInventoryHash(inventory *protocol.PromptInventory) string {
	if inventory == nil {
		return ""
	}
	return inventory.Hash
}

func promptCacheChangedFields(previous, current Snapshot) []string {
	var changed []string
	addStringChange := func(name, oldValue, newValue string) {
		if oldValue != newValue {
			changed = append(changed, name)
		}
	}
	addInt64Change := func(name string, oldValue, newValue int64) {
		if oldValue != newValue {
			changed = append(changed, name)
		}
	}
	addStringChange("provider_changed", previous.ProviderID, current.ProviderID)
	addStringChange("model_changed", previous.ModelID, current.ModelID)
	addStringChange("reasoning_changed", previous.ReasoningMode, current.ReasoningMode)
	addInt64Change("context_budget_changed", previous.ContextBudgetTokens, current.ContextBudgetTokens)
	addStringChange("tool_schema_changed", previous.ToolSnapshotHash, current.ToolSnapshotHash)
	addStringChange("mcp_status_changed", previous.MCPStatusSnapshotHash, current.MCPStatusSnapshotHash)
	addStringChange("profile_instruction_changed", previous.ProfileInstructionHash, current.ProfileInstructionHash)
	addStringChange("permission_mode_changed", previous.DangerousPermissionMode, current.DangerousPermissionMode)
	addStringChange("access_mode_changed", previous.AccessMode, current.AccessMode)
	changed = append(changed, changedPromptSections(previous.PromptInventory, current.PromptInventory)...)
	if len(changed) == 0 {
		changed = append(changed, "prompt_cache_signature_changed")
	}
	return changed
}

func changedPromptSections(previous, current *protocol.PromptInventory) []string {
	prev := promptSectionMap(previous)
	next := promptSectionMap(current)
	names := map[string]bool{}
	for name := range prev {
		names[name] = true
	}
	for name := range next {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	var changed []string
	for _, name := range ordered {
		a, aOK := prev[name]
		b, bOK := next[name]
		if !aOK || !bOK || a.SHA256 != b.SHA256 || a.ByteCount != b.ByteCount {
			changed = append(changed, "prompt_section:"+name)
		}
	}
	return changed
}

func promptSectionMap(inventory *protocol.PromptInventory) map[string]protocol.PromptSection {
	out := map[string]protocol.PromptSection{}
	if inventory == nil {
		return out
	}
	for _, section := range inventory.Sections {
		out[section.Name] = section
	}
	return out
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
