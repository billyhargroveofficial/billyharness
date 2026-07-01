package protocol

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Risk string

const (
	RiskReadOnly Risk = "read_only"
	RiskNetwork  Risk = "network"
	RiskWrite    Risk = "write"
	RiskExecute  Risk = "execute"
	RiskExternal Risk = "external"
)

type Message struct {
	Role             Role       `json:"role"`
	Content          string     `json:"content,omitempty"`
	Name             string     `json:"name,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Arguments            json.RawMessage `json:"arguments"`
	InvalidArguments     string          `json:"invalid_arguments,omitempty"`
	InvalidArgumentError string          `json:"invalid_argument_error,omitempty"`
}

type ToolResult struct {
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Content   string         `json:"content"`
	IsError   bool           `json:"is_error,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	OutputRef string         `json:"output_ref,omitempty"`
	Compact   *ToolCompact   `json:"compact,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Risk        Risk            `json:"risk"`
}

type PromptSection struct {
	Name         string `json:"name"`
	Role         Role   `json:"role,omitempty"`
	Index        int    `json:"index"`
	ByteCount    int    `json:"byte_count"`
	ApproxTokens int    `json:"approx_tokens,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
}

type PromptInventory struct {
	Sections        []PromptSection `json:"sections,omitempty"`
	ToolSchemaCount int             `json:"tool_schema_count,omitempty"`
	TotalBytes      int             `json:"total_bytes,omitempty"`
	ApproxTokens    int             `json:"approx_tokens,omitempty"`
	Hash            string          `json:"hash,omitempty"`
}

type PromptCacheBreak struct {
	Status            string   `json:"status,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	ChangedFields     []string `json:"changed_fields,omitempty"`
	PreviousSignature string   `json:"previous_signature,omitempty"`
	CurrentSignature  string   `json:"current_signature,omitempty"`
}

type EventType string

const (
	EventRunStarted              EventType = "run.started"
	EventTurnStarted             EventType = "turn.started"
	EventTurnCompleted           EventType = "turn.completed"
	EventTurnChangeRecorded      EventType = "turn.change_recorded"
	EventTurnChangeReverted      EventType = "turn.change_reverted"
	EventStepStarted             EventType = "step.started"
	EventStepCompleted           EventType = "step.completed"
	EventModelCallStarted        EventType = "model.call_started"
	EventModelCallFinished       EventType = "model.call_finished"
	EventAssistantReasoning      EventType = "assistant.reasoning_delta"
	EventAssistantDelta          EventType = "assistant.content_delta"
	EventToolCallRequested       EventType = "tool.call_requested"
	EventToolPermissionRequested EventType = "tool.permission_requested"
	EventToolPermissionDecided   EventType = "tool.permission_decided"
	EventToolAudit               EventType = "tool.audit"
	EventToolCallProgress        EventType = "tool.call_progress"
	EventToolCallStarted         EventType = "tool.call_started"
	EventToolCallFinished        EventType = "tool.call_finished"
	EventToolCallFailed          EventType = "tool.call_failed"
	EventToolCallAborted         EventType = "tool.call_aborted"
	EventToolOutputRefCreated    EventType = "tool.output_ref_created"
	EventContextThreshold        EventType = "context.threshold"
	EventContextCompacted        EventType = "context.compacted"
	EventHookStarted             EventType = "hook.started"
	EventHookFinished            EventType = "hook.finished"
	EventHookFailed              EventType = "hook.failed"
	EventRunCompleted            EventType = "run.completed"
	EventRunFailed               EventType = "run.failed"
	EventProviderUsageUpdate     EventType = "provider.usage"
	EventProviderHelperUsage     EventType = "provider.helper_usage"
	EventSessionStatus           EventType = "session.status"
	EventGatewayStreamGap        EventType = "gateway.stream_gap"
	EventStreamStillRunning      EventType = "stream.still_running"
	EventSessionImported         EventType = "session.imported"
	EventUserInputRequested      EventType = "user_input.requested"
	EventUserInputAnswered       EventType = "user_input.answered"
	EventUserInputRejected       EventType = "user_input.rejected"
)

type SessionImportedEvent struct {
	Source           string   `json:"source,omitempty"`
	Format           string   `json:"format,omitempty"`
	ImportedMessages int      `json:"imported_messages,omitempty"`
	MessageCount     int      `json:"message_count,omitempty"`
	ApproxTokens     int      `json:"approx_tokens,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type GatewayStreamGapEvent struct {
	DroppedEvents  int64  `json:"dropped_events"`
	ReplayAfterSeq int64  `json:"replay_after_seq,omitempty"`
	Message        string `json:"message,omitempty"`
}

type StreamStillRunningEvent struct {
	RunID      string `json:"run_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	AttemptID  string `json:"attempt_id,omitempty"`
	Phase      string `json:"phase,omitempty"`
	ElapsedMS  int64  `json:"elapsed_ms,omitempty"`
	IdleMS     int64  `json:"idle_ms,omitempty"`
	IntervalMS int64  `json:"interval_ms,omitempty"`
	Count      int    `json:"count,omitempty"`
	Message    string `json:"message,omitempty"`
}

type ManagedProcessList struct {
	GeneratedAt string                 `json:"generated_at,omitempty"`
	Running     int                    `json:"running,omitempty"`
	Exited      int                    `json:"exited,omitempty"`
	Limit       int                    `json:"limit,omitempty"`
	Truncated   bool                   `json:"truncated,omitempty"`
	Processes   []ManagedProcessStatus `json:"processes,omitempty"`
}

type ManagedProcessStatus struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind,omitempty"`
	Argv              []string `json:"argv,omitempty"`
	Command           string   `json:"command,omitempty"`
	CWD               string   `json:"cwd,omitempty"`
	PID               int      `json:"pid,omitempty"`
	Running           bool     `json:"running"`
	Exited            bool     `json:"exited,omitempty"`
	ExitCode          int      `json:"exit_code,omitempty"`
	ExitError         string   `json:"exit_error,omitempty"`
	StartedAt         string   `json:"started_at,omitempty"`
	EndedAt           string   `json:"ended_at,omitempty"`
	ElapsedMS         int64    `json:"elapsed_ms,omitempty"`
	RetainedBytes     int64    `json:"retained_bytes,omitempty"`
	BaseCursor        int64    `json:"base_cursor,omitempty"`
	NextCursor        int64    `json:"next_cursor,omitempty"`
	DroppedBytes      int64    `json:"dropped_bytes,omitempty"`
	OutputRef         string   `json:"output_ref,omitempty"`
	OutputRefID       string   `json:"output_ref_id,omitempty"`
	OutputRefBytes    int64    `json:"output_ref_bytes,omitempty"`
	OutputRefAt       string   `json:"output_ref_at,omitempty"`
	DetectedPorts     []int    `json:"detected_ports,omitempty"`
	DetectedURLs      []string `json:"detected_urls,omitempty"`
	OutputTailPreview string   `json:"output_tail_preview,omitempty"`
}

type ProviderHelperUsageEvent struct {
	Kind            string  `json:"kind"`
	Provider        string  `json:"provider,omitempty"`
	Model           string  `json:"model,omitempty"`
	RequestID       string  `json:"request_id,omitempty"`
	RunID           string  `json:"run_id,omitempty"`
	TurnID          string  `json:"turn_id,omitempty"`
	StepID          string  `json:"step_id,omitempty"`
	CallID          string  `json:"call_id,omitempty"`
	AttemptID       string  `json:"attempt_id,omitempty"`
	CompactionID    string  `json:"compaction_id,omitempty"`
	InputTokens     int64   `json:"input_tokens,omitempty"`
	OutputTokens    int64   `json:"output_tokens,omitempty"`
	CacheHitTokens  int64   `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens int64   `json:"cache_miss_tokens,omitempty"`
	ReasoningTokens int64   `json:"reasoning_tokens,omitempty"`
	APITokens       int64   `json:"api_tokens,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
}

type Event struct {
	SchemaVersion int         `json:"schema_version,omitempty"`
	Seq           int64       `json:"seq,omitempty"`
	Source        EventSource `json:"source,omitempty"`
	TS            string      `json:"ts,omitempty"`
	SubmissionID  string      `json:"submission_id,omitempty"`
	RunID         string      `json:"run_id,omitempty"`
	TurnID        string      `json:"turn_id,omitempty"`
	StepID        string      `json:"step_id,omitempty"`
	CallID        string      `json:"call_id,omitempty"`
	AttemptID     string      `json:"attempt_id,omitempty"`
	ParentStepID  string      `json:"parent_step_id,omitempty"`
	ProfileHash   string      `json:"profile_hash,omitempty"`
	DurationMS    int64       `json:"duration_ms,omitempty"`
	Type          EventType   `json:"type"`
	Data          any         `json:"data,omitempty"`
}

const (
	TurnStatusStarted   = "started"
	TurnStatusCompleted = "completed"
	TurnStatusFailed    = "failed"

	TurnStopFinalAnswer = "final_answer"
	TurnStopToolResults = "tool_results"
	TurnStopError       = "error"

	StepStatusStarted   = "started"
	StepStatusCompleted = "completed"
	StepStatusFailed    = "failed"

	StepKindModelCall = "model_call"
	StepKindToolBatch = "tool_batch"
	StepKindToolCall  = "tool_call"
)

type TurnEvent struct {
	TurnID        string         `json:"turn_id"`
	Round         int            `json:"round"`
	Status        string         `json:"status"`
	StopReason    string         `json:"stop_reason,omitempty"`
	Model         string         `json:"model,omitempty"`
	MessageCount  int            `json:"message_count,omitempty"`
	ToolCallCount int            `json:"tool_call_count,omitempty"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
	Error         string         `json:"error,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type TurnChangeFile struct {
	Path         string `json:"path"`
	RelPath      string `json:"rel_path,omitempty"`
	Change       string `json:"change"`
	Kind         string `json:"kind,omitempty"`
	Additions    int    `json:"additions,omitempty"`
	Deletions    int    `json:"deletions,omitempty"`
	Binary       bool   `json:"binary,omitempty"`
	Large        bool   `json:"large,omitempty"`
	Reversible   bool   `json:"reversible"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
}

type TurnChangeEvent struct {
	ChangeID                  string           `json:"change_id"`
	RunID                     string           `json:"run_id,omitempty"`
	TurnID                    string           `json:"turn_id,omitempty"`
	StepID                    string           `json:"step_id,omitempty"`
	CallID                    string           `json:"call_id,omitempty"`
	AttemptID                 string           `json:"attempt_id,omitempty"`
	ToolName                  string           `json:"tool_name,omitempty"`
	Status                    string           `json:"status,omitempty"`
	Summary                   string           `json:"summary,omitempty"`
	FileCount                 int              `json:"file_count,omitempty"`
	Added                     int              `json:"added,omitempty"`
	Modified                  int              `json:"modified,omitempty"`
	Deleted                   int              `json:"deleted,omitempty"`
	Directories               int              `json:"directories,omitempty"`
	Additions                 int              `json:"additions,omitempty"`
	Deletions                 int              `json:"deletions,omitempty"`
	BinaryFiles               int              `json:"binary_files,omitempty"`
	LargeFiles                int              `json:"large_files,omitempty"`
	Reversible                bool             `json:"reversible"`
	PatchOutputRef            string           `json:"patch_output_ref,omitempty"`
	PatchOutputRefID          string           `json:"patch_output_ref_id,omitempty"`
	PatchOutputRefBytes       int64            `json:"patch_output_ref_bytes,omitempty"`
	PatchOutputRefSHA256      string           `json:"patch_output_ref_sha256,omitempty"`
	PatchOutputRefPermissions string           `json:"patch_output_ref_permissions,omitempty"`
	PatchOutputRefPlaintext   bool             `json:"patch_output_ref_plaintext,omitempty"`
	PreviewTruncated          bool             `json:"preview_truncated,omitempty"`
	Files                     []TurnChangeFile `json:"files,omitempty"`
}

type StepEvent struct {
	TurnID        string         `json:"turn_id"`
	StepID        string         `json:"step_id"`
	Round         int            `json:"round"`
	Index         int            `json:"index,omitempty"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	Name          string         `json:"name,omitempty"`
	MessageCount  int            `json:"message_count,omitempty"`
	ToolCallID    string         `json:"tool_call_id,omitempty"`
	BatchID       string         `json:"batch_id,omitempty"`
	BatchSize     int            `json:"batch_size,omitempty"`
	Parallel      bool           `json:"parallel,omitempty"`
	ParallelLimit int            `json:"parallel_limit,omitempty"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
	Error         string         `json:"error,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type ModelCallEvent struct {
	RequestID               string            `json:"request_id"`
	Round                   int               `json:"round,omitempty"`
	MessageCount            int               `json:"message_count,omitempty"`
	ToolCount               int               `json:"tool_count,omitempty"`
	ProviderID              string            `json:"provider_id,omitempty"`
	ModelID                 string            `json:"model_id,omitempty"`
	Reasoning               string            `json:"reasoning,omitempty"`
	ReasoningMode           string            `json:"reasoning_mode,omitempty"`
	ContextBudgetTokens     int64             `json:"context_budget_tokens,omitempty"`
	ToolSnapshotHash        string            `json:"tool_snapshot_hash,omitempty"`
	MCPStatusSnapshotHash   string            `json:"mcp_status_snapshot_hash,omitempty"`
	ProfileInstructionHash  string            `json:"profile_instruction_hash,omitempty"`
	PromptInventoryHash     string            `json:"prompt_inventory_hash,omitempty"`
	PromptInventory         *PromptInventory  `json:"prompt_inventory,omitempty"`
	PromptCacheBreak        *PromptCacheBreak `json:"prompt_cache_break,omitempty"`
	DangerousPermissionMode string            `json:"dangerous_permission_mode,omitempty"`
	AccessMode              string            `json:"access_mode,omitempty"`
	Status                  string            `json:"status"`
	ProviderRequestID       string            `json:"provider_request_id,omitempty"`
	Attempts                int               `json:"attempts,omitempty"`
	Retries                 int               `json:"retries"`
	StatusCode              int               `json:"status_code,omitempty"`
	TotalLatencyMS          *int64            `json:"total_latency_ms,omitempty"`
	FirstDeltaMS            *int64            `json:"first_delta_ms,omitempty"`
	InputTokens             int64             `json:"input_tokens,omitempty"`
	OutputTokens            int64             `json:"output_tokens,omitempty"`
	CacheHitTokens          int64             `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens         int64             `json:"cache_miss_tokens,omitempty"`
	ReasoningTokens         int64             `json:"reasoning_tokens,omitempty"`
	Error                   string            `json:"error,omitempty"`
}

type ToolProgressEvent struct {
	CallID    string         `json:"call_id"`
	Name      string         `json:"name,omitempty"`
	AttemptID string         `json:"attempt_id,omitempty"`
	Phase     string         `json:"phase"`
	Status    string         `json:"status"`
	Message   string         `json:"message,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Compact   *ToolCompact   `json:"compact,omitempty"`
}

type ToolPermissionEvent struct {
	CallID           string `json:"call_id"`
	Name             string `json:"name,omitempty"`
	Risk             Risk   `json:"risk,omitempty"`
	RequiresApproval bool   `json:"requires_approval"`
	Decision         string `json:"decision,omitempty"`
	Source           string `json:"source,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type ToolOutputRefEvent struct {
	CallID               string       `json:"call_id"`
	Name                 string       `json:"name,omitempty"`
	AttemptID            string       `json:"attempt_id"`
	OutputRef            string       `json:"output_ref"`
	OutputRefID          string       `json:"output_ref_id,omitempty"`
	OutputRefBytes       int64        `json:"output_ref_bytes,omitempty"`
	OutputRefSHA256      string       `json:"output_ref_sha256,omitempty"`
	OutputRefPermissions string       `json:"output_ref_permissions,omitempty"`
	OutputRefPlaintext   bool         `json:"output_ref_plaintext,omitempty"`
	Truncated            bool         `json:"truncated"`
	Compact              *ToolCompact `json:"compact,omitempty"`
}

type ToolCompact struct {
	DisplayVersion  int      `json:"display_version,omitempty"`
	CallID          string   `json:"call_id,omitempty"`
	AttemptID       string   `json:"attempt_id,omitempty"`
	Name            string   `json:"name,omitempty"`
	Lifecycle       string   `json:"lifecycle,omitempty"`
	Status          string   `json:"status,omitempty"`
	Title           string   `json:"title,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Detail          string   `json:"detail,omitempty"`
	Category        string   `json:"category,omitempty"`
	Group           string   `json:"group,omitempty"`
	Verb            string   `json:"verb,omitempty"`
	Target          string   `json:"target,omitempty"`
	Path            string   `json:"path,omitempty"`
	URL             string   `json:"url,omitempty"`
	Query           string   `json:"query,omitempty"`
	Preview         string   `json:"preview,omitempty"`
	Error           string   `json:"error,omitempty"`
	OutputRef       string   `json:"output_ref,omitempty"`
	OutputRefID     string   `json:"output_ref_id,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	EstimatedTokens int64    `json:"estimated_tokens,omitempty"`
	OriginalBytes   int64    `json:"original_bytes,omitempty"`
	Truncated       bool     `json:"truncated,omitempty"`
	CollapseDefault bool     `json:"collapse_default,omitempty"`
	IsError         bool     `json:"is_error,omitempty"`
	Hints           []string `json:"hints,omitempty"`
}

type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

type TodoState struct {
	Todos      []TodoItem `json:"todos,omitempty"`
	Pending    int        `json:"pending"`
	InProgress int        `json:"in_progress"`
	Completed  int        `json:"completed"`
	Blocked    int        `json:"blocked"`
}

type UserInputOption struct {
	ID          string `json:"id,omitempty"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type UserInputQuestion struct {
	ID            string            `json:"id"`
	Header        string            `json:"header,omitempty"`
	Question      string            `json:"question"`
	Options       []UserInputOption `json:"options,omitempty"`
	AllowFreeform bool              `json:"allow_freeform,omitempty"`
}

type UserInputRequestEvent struct {
	RequestID string              `json:"request_id"`
	SessionID string              `json:"session_id,omitempty"`
	RunID     string              `json:"run_id,omitempty"`
	TurnID    string              `json:"turn_id,omitempty"`
	StepID    string              `json:"step_id,omitempty"`
	CallID    string              `json:"call_id,omitempty"`
	AttemptID string              `json:"attempt_id,omitempty"`
	Source    string              `json:"source,omitempty"`
	Questions []UserInputQuestion `json:"questions"`
}

type UserInputAnswer struct {
	QuestionID  string `json:"question_id,omitempty"`
	OptionID    string `json:"option_id,omitempty"`
	OptionLabel string `json:"option_label,omitempty"`
	Text        string `json:"text,omitempty"`
}

type UserInputAnswerEvent struct {
	RequestID string            `json:"request_id"`
	SessionID string            `json:"session_id,omitempty"`
	RunID     string            `json:"run_id,omitempty"`
	TurnID    string            `json:"turn_id,omitempty"`
	StepID    string            `json:"step_id,omitempty"`
	CallID    string            `json:"call_id,omitempty"`
	AttemptID string            `json:"attempt_id,omitempty"`
	Source    string            `json:"source,omitempty"`
	Answers   []UserInputAnswer `json:"answers,omitempty"`
	Status    string            `json:"status,omitempty"`
}

type UserInputRejectEvent struct {
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	StepID    string `json:"step_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	Source    string `json:"source,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Status    string `json:"status,omitempty"`
}

type HookEvent struct {
	HookEvent                 string         `json:"hook_event"`
	HookName                  string         `json:"hook_name,omitempty"`
	Name                      string         `json:"name,omitempty"`
	Command                   string         `json:"command,omitempty"`
	Fatal                     bool           `json:"fatal"`
	Status                    string         `json:"status"`
	Payload                   map[string]any `json:"payload,omitempty"`
	Phase                     string         `json:"phase,omitempty"`
	Error                     string         `json:"error,omitempty"`
	DurationMS                *int64         `json:"duration_ms,omitempty"`
	Stdout                    string         `json:"stdout,omitempty"`
	Stderr                    string         `json:"stderr,omitempty"`
	StdoutTruncated           *bool          `json:"stdout_truncated,omitempty"`
	StderrTruncated           *bool          `json:"stderr_truncated,omitempty"`
	TimeoutMS                 *int64         `json:"timeout_ms,omitempty"`
	ExitCode                  *int           `json:"exit_code,omitempty"`
	TimedOut                  *bool          `json:"timed_out,omitempty"`
	TurnID                    string         `json:"turn_id,omitempty"`
	StepID                    string         `json:"step_id,omitempty"`
	CallID                    string         `json:"call_id,omitempty"`
	AttemptID                 string         `json:"attempt_id,omitempty"`
	ToolName                  string         `json:"tool_name,omitempty"`
	RequestID                 string         `json:"request_id,omitempty"`
	ProviderID                string         `json:"provider_id,omitempty"`
	ModelID                   string         `json:"model_id,omitempty"`
	ProviderRequestID         string         `json:"provider_request_id,omitempty"`
	Attempts                  *int           `json:"attempts,omitempty"`
	Retries                   *int           `json:"retries,omitempty"`
	StatusCode                *int           `json:"status_code,omitempty"`
	ServerName                string         `json:"server_name,omitempty"`
	Transport                 string         `json:"transport,omitempty"`
	Connected                 *bool          `json:"connected,omitempty"`
	State                     string         `json:"state,omitempty"`
	ToolCount                 *int           `json:"tool_count,omitempty"`
	RetryCount                *int           `json:"retry_count,omitempty"`
	RestartCount              *int           `json:"restart_count,omitempty"`
	RetryBackoffMS            *int64         `json:"retry_backoff_ms,omitempty"`
	ArgsSummary               string         `json:"args_summary,omitempty"`
	ErrorCode                 string         `json:"error_code,omitempty"`
	IsError                   *bool          `json:"is_error,omitempty"`
	OutputBytes               *int64         `json:"output_bytes,omitempty"`
	OutputEstimatedTokens     *int64         `json:"output_estimated_tokens,omitempty"`
	Truncated                 *bool          `json:"truncated,omitempty"`
	OutputRef                 string         `json:"output_ref,omitempty"`
	PermissionDecision        string         `json:"permission_decision,omitempty"`
	PermissionSource          string         `json:"permission_source,omitempty"`
	PermissionReason          string         `json:"permission_reason,omitempty"`
	Decision                  string         `json:"decision,omitempty"`
	BlockReason               string         `json:"block_reason,omitempty"`
	AdditionalContextBytes    *int64         `json:"additional_context_bytes,omitempty"`
	UpdatedPromptBytes        *int64         `json:"updated_prompt_bytes,omitempty"`
	UpdatedPromptSHA256       string         `json:"updated_prompt_sha256,omitempty"`
	PromptHookContextCapBytes *int           `json:"prompt_hook_context_cap_bytes,omitempty"`
	PromptHookPromptCapBytes  *int           `json:"prompt_hook_prompt_cap_bytes,omitempty"`
	PromptHookReasonCapBytes  *int           `json:"prompt_hook_reason_cap_bytes,omitempty"`
}

type ContextThresholdEvent struct {
	Percent             int    `json:"percent"`
	EstimatedTokens     int64  `json:"estimated_tokens"`
	ContextWindowTokens int64  `json:"context_window_tokens"`
	ThresholdTokens     int64  `json:"threshold_tokens"`
	RemainingTokens     int64  `json:"remaining_tokens"`
	MessageCount        int    `json:"message_count"`
	Round               int    `json:"round,omitempty"`
	Stage               string `json:"stage,omitempty"`
	Estimator           string `json:"estimator,omitempty"`
}
