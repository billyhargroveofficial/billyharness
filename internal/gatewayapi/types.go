package gatewayapi

import (
	"encoding/json"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	HeaderSessionClientID         = "X-Billyharness-Session-Client-ID"
	HeaderSessionClientType       = "X-Billyharness-Session-Client-Type"
	HeaderSessionTelegramChatID   = "X-Billyharness-Session-Telegram-Chat-ID"
	HeaderSessionTelegramThreadID = "X-Billyharness-Session-Telegram-Thread-ID"
	HeaderSessionTelegramUserID   = "X-Billyharness-Session-Telegram-User-ID"
	HeaderSessionTUIChatID        = "X-Billyharness-Session-TUI-Chat-ID"
)

type RunRequest struct {
	Prompt          string                   `json:"prompt"`
	Attachments     []protocol.AttachmentRef `json:"attachments,omitempty"`
	InputID         string                   `json:"input_id,omitempty"`
	ClientID        string                   `json:"client_id,omitempty"`
	ClientType      string                   `json:"client_type,omitempty"`
	Provider        string                   `json:"provider,omitempty"`
	Model           string                   `json:"model,omitempty"`
	Profile         string                   `json:"profile,omitempty"`
	Thinking        string                   `json:"thinking,omitempty"`
	ReasoningEffort string                   `json:"reasoning_effort,omitempty"`
	MaxToolRounds   int                      `json:"max_tool_rounds,omitempty"`
	AccessMode      string                   `json:"access_mode,omitempty"`
	InterruptPolicy string                   `json:"interrupt_policy,omitempty"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
}

const InterruptPolicyInterrupt = "interrupt"

type SessionInputRequest struct {
	InputID         string                   `json:"input_id"`
	Prompt          string                   `json:"prompt"`
	Attachments     []protocol.AttachmentRef `json:"attachments,omitempty"`
	InterruptPolicy string                   `json:"interrupt_policy,omitempty"`
	ClientID        string                   `json:"client_id,omitempty"`
	ClientType      string                   `json:"client_type,omitempty"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
}

type SessionInputResponse struct {
	InputID        string `json:"input_id"`
	State          string `json:"state"`
	Duplicate      bool   `json:"duplicate,omitempty"`
	Seq            int64  `json:"seq,omitempty"`
	TerminalStatus string `json:"terminal_status,omitempty"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

type SessionInputCompleteRequest struct {
	TerminalStatus string `json:"terminal_status"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

type CreateSessionRequest struct {
	Messages []protocol.Message `json:"messages,omitempty"`
	Profile  string             `json:"profile,omitempty"`
	Owner    SessionOwner       `json:"owner,omitempty"`
}

type SessionOwner struct {
	ClientID         string `json:"client_id,omitempty"`
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

type ReadinessResponse struct {
	OK           bool                     `json:"ok"`
	Provider     string                   `json:"provider"`
	Model        string                   `json:"model"`
	GatewayAddr  string                   `json:"gateway_addr,omitempty"`
	Checks       []ReadinessCheck         `json:"checks"`
	Tools        ReadinessCatalogStatus   `json:"tools"`
	MCP          ReadinessMCPStatus       `json:"mcp"`
	AgentClub    AgentClubReadinessStatus `json:"agent_club"`
	SessionStore *SessionStoreHealth      `json:"session_store,omitempty"`
}

type ReadinessCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type ReadinessCatalogStatus struct {
	Count int `json:"count"`
}

type ReadinessMCPStatus struct {
	Enabled          bool `json:"enabled"`
	Configured       int  `json:"configured"`
	Connected        int  `json:"connected"`
	RequiredFailures int  `json:"required_failures,omitempty"`
	OptionalWarnings int  `json:"optional_warnings,omitempty"`
	ToolCount        int  `json:"tool_count,omitempty"`
}

type AgentClubReadinessStatus struct {
	ConfiguredFileCount    int      `json:"configured_file_count"`
	ConfiguredFileBasename []string `json:"configured_file_basenames,omitempty"`
	CapabilityCount        int      `json:"capability_count"`
	BindingCount           int      `json:"binding_count"`
	EnabledBindingCount    int      `json:"enabled_binding_count"`
	TriggerCount           int      `json:"trigger_count"`
	EnabledTriggerCount    int      `json:"enabled_trigger_count"`
	HMACSecretEnvCount     int      `json:"hmac_secret_env_count,omitempty"`
	MissingSecretEnvCount  int      `json:"missing_secret_env_count,omitempty"`
	Configured             bool     `json:"configured"`
}

type SessionStoreHealth struct {
	Enabled      bool                    `json:"enabled"`
	LoadedCount  int                     `json:"loaded_count"`
	ErrorCount   int                     `json:"error_count,omitempty"`
	CorruptCount int                     `json:"corrupt_count,omitempty"`
	Errors       []SessionStoreLoadError `json:"errors,omitempty"`
}

type SessionStoreLoadError struct {
	Entry          string `json:"entry,omitempty"`
	EntryType      string `json:"entry_type,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	SessionIDHash  string `json:"session_id_hash,omitempty"`
	Corrupt        bool   `json:"corrupt,omitempty"`
	CorruptionKind string `json:"corruption_kind,omitempty"`
	Line           int    `json:"line,omitempty"`
	RecordNo       int64  `json:"record_no,omitempty"`
	Error          string `json:"error"`
}

type ConfigStatusResponse struct {
	Config      map[string]any      `json:"config"`
	Values      []ConfigStatusValue `json:"values"`
	Diagnostics map[string]any      `json:"diagnostics"`
	Warnings    []string            `json:"warnings,omitempty"`
}

type ConfigStatusValue struct {
	Key      string `json:"key"`
	Value    any    `json:"value,omitempty"`
	Source   string `json:"source,omitempty"`
	Redacted bool   `json:"redacted,omitempty"`
	Warning  string `json:"warning,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ManagedProcessResponse struct {
	Processes protocol.ManagedProcessList `json:"processes"`
	Text      string                      `json:"text,omitempty"`
}

type SessionStatus struct {
	ID               string                      `json:"id"`
	Created          time.Time                   `json:"created"`
	Running          bool                        `json:"running"`
	RunSeq           int64                       `json:"run_seq"`
	StartedAt        time.Time                   `json:"started_at,omitempty"`
	FinishedAt       time.Time                   `json:"finished_at,omitempty"`
	LastEvent        string                      `json:"last_event,omitempty"`
	LastEventAt      time.Time                   `json:"last_event_at,omitempty"`
	Model            string                      `json:"model,omitempty"`
	Provider         string                      `json:"provider,omitempty"`
	Profile          string                      `json:"profile,omitempty"`
	ReasoningEffort  string                      `json:"reasoning_effort,omitempty"`
	AccessMode       string                      `json:"access_mode,omitempty"`
	Owner            SessionOwner                `json:"owner,omitempty"`
	MessageCount     int                         `json:"message_count"`
	AttachmentCount  int                         `json:"attachment_count,omitempty"`
	ImageSubmissions int                         `json:"image_submissions,omitempty"`
	ModelCalls       int                         `json:"model_calls"`
	ToolCalls        int                         `json:"tool_calls"`
	DroppedEvents    int64                       `json:"dropped_events,omitempty"`
	LastError        string                      `json:"last_error,omitempty"`
	ContextEpochHash string                      `json:"context_epoch_hash,omitempty"`
	LockedEpochHash  string                      `json:"locked_context_epoch_hash,omitempty"`
	ContextEpoch     *protocol.ContextEpoch      `json:"context_epoch,omitempty"`
	LockedEpoch      *protocol.ContextEpoch      `json:"locked_context_epoch,omitempty"`
	ContextDrift     *protocol.ContextEpochDrift `json:"context_epoch_drift,omitempty"`
}

type SessionListResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

type SessionSummary struct {
	ID               string       `json:"id"`
	Created          time.Time    `json:"created"`
	Running          bool         `json:"running"`
	RunSeq           int64        `json:"run_seq"`
	MessageCount     int          `json:"message_count"`
	AttachmentCount  int          `json:"attachment_count,omitempty"`
	ImageSubmissions int          `json:"image_submissions,omitempty"`
	DroppedEvents    int64        `json:"dropped_events,omitempty"`
	LastEvent        string       `json:"last_event,omitempty"`
	LastEventAt      time.Time    `json:"last_event_at,omitempty"`
	Model            string       `json:"model,omitempty"`
	Provider         string       `json:"provider,omitempty"`
	Profile          string       `json:"profile,omitempty"`
	ReasoningEffort  string       `json:"reasoning_effort,omitempty"`
	AccessMode       string       `json:"access_mode,omitempty"`
	Owner            SessionOwner `json:"owner,omitempty"`
	LastError        string       `json:"last_error,omitempty"`
}

type SessionResponse struct {
	ID               string             `json:"id"`
	Created          time.Time          `json:"created"`
	MessageCount     int                `json:"message_count"`
	AttachmentCount  int                `json:"attachment_count,omitempty"`
	ImageSubmissions int                `json:"image_submissions,omitempty"`
	Messages         []protocol.Message `json:"messages,omitempty"`
	Running          bool               `json:"running"`
	Owner            SessionOwner       `json:"owner,omitempty"`
	Status           SessionStatus      `json:"status"`
}

type SessionContextResponse struct {
	ID                      string                      `json:"id"`
	MessageCount            int                         `json:"message_count"`
	AttachmentCount         int                         `json:"attachment_count,omitempty"`
	ImageSubmissions        int                         `json:"image_submissions,omitempty"`
	EstimatedTokens         int64                       `json:"estimated_tokens"`
	ContextWindowTokens     int64                       `json:"context_window_tokens"`
	ContextWindowSource     string                      `json:"context_window_source,omitempty"`
	ContextCompactTokens    int64                       `json:"context_compact_tokens"`
	PercentUsed             float64                     `json:"percent_used"`
	CompactThresholdPercent float64                     `json:"compact_threshold_percent"`
	OverCompactThreshold    bool                        `json:"over_compact_threshold"`
	Estimator               string                      `json:"estimator"`
	Sources                 []ContextSource             `json:"sources,omitempty"`
	Thresholds              []ContextThreshold          `json:"thresholds,omitempty"`
	TopContributors         []ContextContributor        `json:"top_contributors,omitempty"`
	Runtime                 ContextRuntime              `json:"runtime,omitempty"`
	Usage                   ContextUsage                `json:"usage,omitempty"`
	Prompt                  ContextPrompt               `json:"prompt,omitempty"`
	Memory                  ContextMemory               `json:"memory,omitempty"`
	ContextEpoch            *protocol.ContextEpoch      `json:"context_epoch,omitempty"`
	ContextDrift            *protocol.ContextEpochDrift `json:"context_epoch_drift,omitempty"`
	Diagnostics             ContextDiagnostics          `json:"diagnostics,omitempty"`
	LastCompaction          *ContextCompaction          `json:"last_compaction,omitempty"`
	OutputRefs              ContextOutputRefs           `json:"output_refs,omitempty"`
	Warnings                []string                    `json:"warnings,omitempty"`
}

type ContextRuntime struct {
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Profile       string `json:"profile,omitempty"`
	ReasoningMode string `json:"reasoning_mode,omitempty"`
	AccessMode    string `json:"access_mode,omitempty"`
}

type ContextUsage struct {
	ModelCalls              int     `json:"model_calls,omitempty"`
	ToolCalls               int     `json:"tool_calls,omitempty"`
	InputTokens             int64   `json:"input_tokens,omitempty"`
	OutputTokens            int64   `json:"output_tokens,omitempty"`
	CacheHitTokens          int64   `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens         int64   `json:"cache_miss_tokens,omitempty"`
	ReasoningTokens         int64   `json:"reasoning_tokens,omitempty"`
	LastInputTokens         int64   `json:"last_input_tokens,omitempty"`
	LastOutputTokens        int64   `json:"last_output_tokens,omitempty"`
	LastCacheHitTokens      int64   `json:"last_cache_hit_tokens,omitempty"`
	LastCacheMissTokens     int64   `json:"last_cache_miss_tokens,omitempty"`
	WebSummaryInputTokens   int64   `json:"web_summary_input_tokens,omitempty"`
	WebSummaryOutputTokens  int64   `json:"web_summary_output_tokens,omitempty"`
	HelperModelCalls        int     `json:"helper_model_calls,omitempty"`
	HelperModelInputTokens  int64   `json:"helper_model_input_tokens,omitempty"`
	HelperModelOutputTokens int64   `json:"helper_model_output_tokens,omitempty"`
	HelperModelCacheHit     int64   `json:"helper_model_cache_hit_tokens,omitempty"`
	HelperModelCacheMiss    int64   `json:"helper_model_cache_miss_tokens,omitempty"`
	HelperModelAPITokens    int64   `json:"helper_model_api_tokens,omitempty"`
	HelperAPICalls          int     `json:"helper_api_calls,omitempty"`
	HelperCostUSD           float64 `json:"helper_cost_usd,omitempty"`
}

type ContextPrompt struct {
	InventoryHash string                   `json:"inventory_hash,omitempty"`
	SectionCount  int                      `json:"section_count,omitempty"`
	TotalBytes    int                      `json:"total_bytes,omitempty"`
	ApproxTokens  int                      `json:"approx_tokens,omitempty"`
	ToolSchemas   int                      `json:"tool_schemas,omitempty"`
	Sections      []protocol.PromptSection `json:"sections,omitempty"`
	CacheStatus   string                   `json:"cache_status,omitempty"`
	CacheReason   string                   `json:"cache_reason,omitempty"`
}

type ContextMemory struct {
	Policy          string `json:"policy,omitempty"`
	Status          string `json:"status,omitempty"`
	LockedHash      string `json:"locked_hash,omitempty"`
	CurrentHash     string `json:"current_hash,omitempty"`
	CurrentRoots    int    `json:"current_roots,omitempty"`
	CurrentEntries  int    `json:"current_entries,omitempty"`
	CurrentWarnings int    `json:"current_warnings,omitempty"`
	CurrentCapped   bool   `json:"current_capped,omitempty"`
	CurrentError    string `json:"current_error,omitempty"`
}

type ContextDiagnostics struct {
	CurrentEpoch              int    `json:"current_epoch,omitempty"`
	ContextEpochHash          string `json:"context_epoch_hash,omitempty"`
	ContextEpochStatus        string `json:"context_epoch_status,omitempty"`
	ConfigHash                string `json:"config_hash,omitempty"`
	ToolCatalogHash           string `json:"tool_catalog_hash,omitempty"`
	MCPCatalogHash            string `json:"mcp_catalog_hash,omitempty"`
	DocsIndexHash             string `json:"docs_index_hash,omitempty"`
	CompactionEvents          int    `json:"compaction_events,omitempty"`
	ThresholdEvents           int    `json:"threshold_events,omitempty"`
	ToolCallEvents            int    `json:"tool_call_events,omitempty"`
	HelperModelCalls          int    `json:"helper_model_calls,omitempty"`
	ProtectedPrefixTokens     int64  `json:"protected_prefix_tokens,omitempty"`
	BodyTokens                int64  `json:"body_tokens,omitempty"`
	WindowRemainingTokens     int64  `json:"window_remaining_tokens,omitempty"`
	CompactMarginTokens       int64  `json:"compact_margin_tokens,omitempty"`
	MemoryContextHash         string `json:"memory_context_hash,omitempty"`
	ProjectContextHash        string `json:"project_context_hash,omitempty"`
	AgentsInstructionsHash    string `json:"agents_instructions_hash,omitempty"`
	MCPInstructionsHash       string `json:"mcp_instructions_hash,omitempty"`
	PromptInventoryHash       string `json:"prompt_inventory_hash,omitempty"`
	LastCompactionHistoryHash string `json:"last_compaction_history_hash,omitempty"`
}

type ContextCompaction struct {
	Seq             int64  `json:"seq,omitempty"`
	CompactionID    string `json:"compaction_id,omitempty"`
	ContextEpoch    int    `json:"context_epoch,omitempty"`
	Strategy        string `json:"strategy,omitempty"`
	BeforeTokens    int64  `json:"before_tokens,omitempty"`
	AfterTokens     int64  `json:"after_tokens,omitempty"`
	Reason          string `json:"reason,omitempty"`
	InputSpanHash   string `json:"input_span_hash,omitempty"`
	ReplacementHash string `json:"replacement_hash,omitempty"`
	PreHistoryHash  string `json:"pre_history_hash,omitempty"`
	PostHistoryHash string `json:"post_history_hash,omitempty"`
}

type ContextOutputRefs struct {
	Count             int `json:"count,omitempty"`
	LargeInlineCount  int `json:"large_inline_count,omitempty"`
	SourceBucketCount int `json:"source_bucket_count,omitempty"`
}

type ContextContributor struct {
	Index             int    `json:"index"`
	Role              string `json:"role"`
	Source            string `json:"source,omitempty"`
	Name              string `json:"name,omitempty"`
	Chars             int    `json:"chars"`
	EstimatedTokens   int64  `json:"estimated_tokens"`
	Preview           string `json:"preview,omitempty"`
	LargeInline       bool   `json:"large_inline,omitempty"`
	HasOutputRef      bool   `json:"has_output_ref,omitempty"`
	InlineBudgetBytes int    `json:"inline_budget_bytes,omitempty"`
}

type ContextSource struct {
	Source           string  `json:"source"`
	MessageCount     int     `json:"message_count"`
	Chars            int     `json:"chars"`
	EstimatedTokens  int64   `json:"estimated_tokens"`
	Percent          float64 `json:"percent"`
	LargeInlineCount int     `json:"large_inline_count,omitempty"`
	OutputRefCount   int     `json:"output_ref_count,omitempty"`
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

type UserInputAnswerRequest struct {
	Text     string                     `json:"text,omitempty"`
	Answers  []protocol.UserInputAnswer `json:"answers,omitempty"`
	Source   string                     `json:"source,omitempty"`
	Metadata map[string]string          `json:"metadata,omitempty"`
}

type UserInputRejectRequest struct {
	Reason   string            `json:"reason,omitempty"`
	Source   string            `json:"source,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type UserInputResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

type SessionUndoRequest struct {
	ChangeID string `json:"change_id,omitempty"`
	Preview  bool   `json:"preview,omitempty"`
}

type SessionUndoResponse struct {
	ChangeID       string                   `json:"change_id,omitempty"`
	Preview        bool                     `json:"preview,omitempty"`
	Patch          string                   `json:"patch,omitempty"`
	PatchTruncated bool                     `json:"patch_truncated,omitempty"`
	RestoredFiles  []string                 `json:"restored_files,omitempty"`
	Conflicts      []string                 `json:"conflicts,omitempty"`
	Change         protocol.TurnChangeEvent `json:"change,omitempty"`
}
