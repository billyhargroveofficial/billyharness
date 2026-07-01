package gatewayapi

import (
	"encoding/json"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type RunRequest struct {
	Prompt          string `json:"prompt"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Profile         string `json:"profile,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxToolRounds   int    `json:"max_tool_rounds,omitempty"`
	InterruptPolicy string `json:"interrupt_policy,omitempty"`
}

const InterruptPolicyInterrupt = "interrupt"

type CreateSessionRequest struct {
	Messages []protocol.Message `json:"messages,omitempty"`
	Profile  string             `json:"profile,omitempty"`
	Owner    SessionOwner       `json:"owner,omitempty"`
}

type SessionOwner struct {
	ClientType       string `json:"client_type,omitempty"`
	TelegramChatID   int64  `json:"telegram_chat_id,omitempty"`
	TelegramThreadID int    `json:"telegram_thread_id,omitempty"`
	TelegramUserID   int64  `json:"telegram_user_id,omitempty"`
	TUIChatID        string `json:"tui_chat_id,omitempty"`
	Profile          string `json:"profile,omitempty"`
	Model            string `json:"model,omitempty"`
}

type DeepSeekAuthRequest struct {
	APIKey string `json:"api_key"`
}

type CodexImportRequest struct {
	SourcePath string          `json:"source_path,omitempty"`
	AuthJSON   json.RawMessage `json:"auth_json,omitempty"`
}

type HealthResponse struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ConfigStatusResponse struct {
	Config      map[string]any            `json:"config"`
	Values      []config.ResolvedValue    `json:"values"`
	Diagnostics config.DiagnosticSnapshot `json:"diagnostics"`
	Warnings    []string                  `json:"warnings,omitempty"`
}

type SessionStatus struct {
	ID              string       `json:"id"`
	Created         time.Time    `json:"created"`
	Running         bool         `json:"running"`
	RunSeq          int64        `json:"run_seq"`
	StartedAt       time.Time    `json:"started_at,omitempty"`
	FinishedAt      time.Time    `json:"finished_at,omitempty"`
	LastEvent       string       `json:"last_event,omitempty"`
	LastEventAt     time.Time    `json:"last_event_at,omitempty"`
	Model           string       `json:"model,omitempty"`
	Provider        string       `json:"provider,omitempty"`
	Profile         string       `json:"profile,omitempty"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"`
	Owner           SessionOwner `json:"owner,omitempty"`
	MessageCount    int          `json:"message_count"`
	ModelCalls      int          `json:"model_calls"`
	ToolCalls       int          `json:"tool_calls"`
	DroppedEvents   int64        `json:"dropped_events,omitempty"`
	LastError       string       `json:"last_error,omitempty"`
}

type SessionListResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

type SessionSummary struct {
	ID              string       `json:"id"`
	Created         time.Time    `json:"created"`
	Running         bool         `json:"running"`
	RunSeq          int64        `json:"run_seq"`
	MessageCount    int          `json:"message_count"`
	DroppedEvents   int64        `json:"dropped_events,omitempty"`
	LastEvent       string       `json:"last_event,omitempty"`
	LastEventAt     time.Time    `json:"last_event_at,omitempty"`
	Model           string       `json:"model,omitempty"`
	Provider        string       `json:"provider,omitempty"`
	Profile         string       `json:"profile,omitempty"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"`
	Owner           SessionOwner `json:"owner,omitempty"`
	LastError       string       `json:"last_error,omitempty"`
}

type SessionResponse struct {
	ID           string             `json:"id"`
	Created      time.Time          `json:"created"`
	MessageCount int                `json:"message_count"`
	Messages     []protocol.Message `json:"messages,omitempty"`
	Running      bool               `json:"running"`
	Owner        SessionOwner       `json:"owner,omitempty"`
	Status       SessionStatus      `json:"status"`
}

type SessionContextResponse struct {
	ID                      string               `json:"id"`
	MessageCount            int                  `json:"message_count"`
	EstimatedTokens         int64                `json:"estimated_tokens"`
	ContextWindowTokens     int64                `json:"context_window_tokens"`
	ContextCompactTokens    int64                `json:"context_compact_tokens"`
	PercentUsed             float64              `json:"percent_used"`
	CompactThresholdPercent float64              `json:"compact_threshold_percent"`
	OverCompactThreshold    bool                 `json:"over_compact_threshold"`
	Estimator               string               `json:"estimator"`
	Sources                 []ContextSource      `json:"sources,omitempty"`
	Thresholds              []ContextThreshold   `json:"thresholds,omitempty"`
	TopContributors         []ContextContributor `json:"top_contributors,omitempty"`
}

type ContextContributor struct {
	Index           int    `json:"index"`
	Role            string `json:"role"`
	Source          string `json:"source,omitempty"`
	Name            string `json:"name,omitempty"`
	Chars           int    `json:"chars"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	Preview         string `json:"preview,omitempty"`
}

type ContextSource struct {
	Source          string  `json:"source"`
	MessageCount    int     `json:"message_count"`
	Chars           int     `json:"chars"`
	EstimatedTokens int64   `json:"estimated_tokens"`
	Percent         float64 `json:"percent"`
}

type ContextThreshold struct {
	Percent         int   `json:"percent"`
	Tokens          int64 `json:"tokens"`
	Crossed         bool  `json:"crossed"`
	RemainingTokens int64 `json:"remaining_tokens"`
}

type CancelSessionResponse struct {
	Cancelled bool `json:"cancelled"`
}

type BenchmarkListResponse struct {
	Dir  string                `json:"dir"`
	Runs []BenchmarkRunSummary `json:"runs"`
}

type BenchmarkRunSummary struct {
	RunID           string    `json:"run_id"`
	CreatedAt       time.Time `json:"created_at"`
	Harness         string    `json:"harness,omitempty"`
	ProfileHash     string    `json:"profile_hash,omitempty"`
	TasksPath       string    `json:"tasks_path,omitempty"`
	TaskCount       int       `json:"task_count,omitempty"`
	ManifestJSON    string    `json:"manifest_json"`
	ResultsJSONL    string    `json:"results_jsonl,omitempty"`
	EventsJSONL     string    `json:"events_jsonl,omitempty"`
	PayloadsDir     string    `json:"payloads_dir,omitempty"`
	ResultsPresent  bool      `json:"results_present"`
	EventsPresent   bool      `json:"events_present"`
	PayloadsPresent bool      `json:"payloads_present"`
}
