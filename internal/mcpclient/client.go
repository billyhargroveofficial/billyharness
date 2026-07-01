package mcpclient

import (
	"context"
	"encoding/json"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	protocolVersion              = "2025-06-18"
	defaultMCPToolOutputBytes    = 64 * 1024
	mcpReadBufferBytes           = 32 * 1024
	mcpCallResponseOverheadBytes = 64 * 1024
	minMCPCallResponseBytes      = 256 * 1024
	maxMCPControlResponseBytes   = 4 * 1024 * 1024
	maxMCPReconnectBackoff       = 5 * time.Second
)

const (
	mcpStateDisabled     = "disabled"
	mcpStateConnected    = "connected"
	mcpStateFailed       = "failed"
	mcpStateCrashed      = "crashed"
	mcpStateRestarting   = "restarting"
	mcpStateReconnected  = "reconnected"
	mcpStateDisconnected = "disconnected"
	mcpStateUnsupported  = "unsupported"
)

type ExternalTool struct {
	Spec    protocol.ToolSpec
	Handler func(context.Context, json.RawMessage) (string, error)
}

type Prompt struct {
	Server      string           `json:"server"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type CatalogChange struct {
	Version     int64    `json:"version"`
	ToolCount   int      `json:"tool_count"`
	PromptCount int      `json:"prompt_count,omitempty"`
	Collisions  []string `json:"collisions,omitempty"`
}

type CatalogSnapshot struct {
	Version      int64
	Tools        []ExternalTool
	Prompts      []Prompt
	Instructions []string
	Collisions   []string
}

type ServerStatus struct {
	Name              string     `json:"name"`
	Transport         string     `json:"transport"`
	Command           string     `json:"command,omitempty"`
	URL               string     `json:"url,omitempty"`
	UnsupportedReason string     `json:"unsupported_reason,omitempty"`
	Enabled           bool       `json:"enabled"`
	Required          bool       `json:"required"`
	Connected         bool       `json:"connected"`
	State             string     `json:"state"`
	ToolCount         int        `json:"tool_count"`
	PID               int        `json:"pid,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	LastConnectedAt   *time.Time `json:"last_connected_at,omitempty"`
	LastEventAt       *time.Time `json:"last_event_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	LastErrorAt       *time.Time `json:"last_error_at,omitempty"`
	StderrTail        string     `json:"stderr_tail,omitempty"`
	Error             string     `json:"error,omitempty"`
	RetryCount        int        `json:"retry_count"`
	RestartCount      int        `json:"restart_count"`
	RetryBackoffMS    int64      `json:"retry_backoff_ms,omitempty"`
	NextRetryAt       *time.Time `json:"next_retry_at,omitempty"`
}

type ManagerSettings struct {
	WorkspaceRoots     []string
	MaxToolOutputBytes int
	MCP                config.MCPSettings
}
