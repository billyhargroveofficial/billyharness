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
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Risk        Risk            `json:"risk"`
}

type EventType string

const (
	EventRunStarted              EventType = "run.started"
	EventTurnStarted             EventType = "turn.started"
	EventTurnCompleted           EventType = "turn.completed"
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
	EventSessionStatus           EventType = "session.status"
)

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
	RequestID               string `json:"request_id"`
	Round                   int    `json:"round,omitempty"`
	MessageCount            int    `json:"message_count,omitempty"`
	ToolCount               int    `json:"tool_count,omitempty"`
	ProviderID              string `json:"provider_id,omitempty"`
	ModelID                 string `json:"model_id,omitempty"`
	Reasoning               string `json:"reasoning,omitempty"`
	ReasoningMode           string `json:"reasoning_mode,omitempty"`
	ContextBudgetTokens     int64  `json:"context_budget_tokens,omitempty"`
	ToolSnapshotHash        string `json:"tool_snapshot_hash,omitempty"`
	MCPStatusSnapshotHash   string `json:"mcp_status_snapshot_hash,omitempty"`
	ProfileInstructionHash  string `json:"profile_instruction_hash,omitempty"`
	DangerousPermissionMode string `json:"dangerous_permission_mode,omitempty"`
	Status                  string `json:"status"`
	ProviderRequestID       string `json:"provider_request_id,omitempty"`
	Attempts                int    `json:"attempts,omitempty"`
	Retries                 int    `json:"retries"`
	StatusCode              int    `json:"status_code,omitempty"`
	TotalLatencyMS          *int64 `json:"total_latency_ms,omitempty"`
	FirstDeltaMS            *int64 `json:"first_delta_ms,omitempty"`
	InputTokens             int64  `json:"input_tokens,omitempty"`
	OutputTokens            int64  `json:"output_tokens,omitempty"`
	CacheHitTokens          int64  `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens         int64  `json:"cache_miss_tokens,omitempty"`
	ReasoningTokens         int64  `json:"reasoning_tokens,omitempty"`
	Error                   string `json:"error,omitempty"`
}

type ToolProgressEvent struct {
	CallID    string         `json:"call_id"`
	Name      string         `json:"name,omitempty"`
	AttemptID string         `json:"attempt_id,omitempty"`
	Phase     string         `json:"phase"`
	Status    string         `json:"status"`
	Message   string         `json:"message,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
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
	CallID               string `json:"call_id"`
	Name                 string `json:"name,omitempty"`
	AttemptID            string `json:"attempt_id"`
	OutputRef            string `json:"output_ref"`
	OutputRefID          string `json:"output_ref_id,omitempty"`
	OutputRefBytes       int64  `json:"output_ref_bytes,omitempty"`
	OutputRefSHA256      string `json:"output_ref_sha256,omitempty"`
	OutputRefPermissions string `json:"output_ref_permissions,omitempty"`
	OutputRefPlaintext   bool   `json:"output_ref_plaintext,omitempty"`
	Truncated            bool   `json:"truncated"`
}

type HookEvent struct {
	HookEvent             string         `json:"hook_event"`
	HookName              string         `json:"hook_name,omitempty"`
	Name                  string         `json:"name,omitempty"`
	Command               string         `json:"command,omitempty"`
	Fatal                 bool           `json:"fatal"`
	Status                string         `json:"status"`
	Payload               map[string]any `json:"payload,omitempty"`
	Phase                 string         `json:"phase,omitempty"`
	Error                 string         `json:"error,omitempty"`
	DurationMS            *int64         `json:"duration_ms,omitempty"`
	Stdout                string         `json:"stdout,omitempty"`
	Stderr                string         `json:"stderr,omitempty"`
	StdoutTruncated       *bool          `json:"stdout_truncated,omitempty"`
	StderrTruncated       *bool          `json:"stderr_truncated,omitempty"`
	TimeoutMS             *int64         `json:"timeout_ms,omitempty"`
	ExitCode              *int           `json:"exit_code,omitempty"`
	TimedOut              *bool          `json:"timed_out,omitempty"`
	TurnID                string         `json:"turn_id,omitempty"`
	StepID                string         `json:"step_id,omitempty"`
	CallID                string         `json:"call_id,omitempty"`
	AttemptID             string         `json:"attempt_id,omitempty"`
	ToolName              string         `json:"tool_name,omitempty"`
	RequestID             string         `json:"request_id,omitempty"`
	ProviderID            string         `json:"provider_id,omitempty"`
	ModelID               string         `json:"model_id,omitempty"`
	ProviderRequestID     string         `json:"provider_request_id,omitempty"`
	Attempts              *int           `json:"attempts,omitempty"`
	Retries               *int           `json:"retries,omitempty"`
	StatusCode            *int           `json:"status_code,omitempty"`
	ServerName            string         `json:"server_name,omitempty"`
	Transport             string         `json:"transport,omitempty"`
	Connected             *bool          `json:"connected,omitempty"`
	State                 string         `json:"state,omitempty"`
	ToolCount             *int           `json:"tool_count,omitempty"`
	RetryCount            *int           `json:"retry_count,omitempty"`
	RestartCount          *int           `json:"restart_count,omitempty"`
	RetryBackoffMS        *int64         `json:"retry_backoff_ms,omitempty"`
	ArgsSummary           string         `json:"args_summary,omitempty"`
	ErrorCode             string         `json:"error_code,omitempty"`
	IsError               *bool          `json:"is_error,omitempty"`
	OutputBytes           *int64         `json:"output_bytes,omitempty"`
	OutputEstimatedTokens *int64         `json:"output_estimated_tokens,omitempty"`
	Truncated             *bool          `json:"truncated,omitempty"`
	OutputRef             string         `json:"output_ref,omitempty"`
	PermissionDecision    string         `json:"permission_decision,omitempty"`
	PermissionSource      string         `json:"permission_source,omitempty"`
	PermissionReason      string         `json:"permission_reason,omitempty"`
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
