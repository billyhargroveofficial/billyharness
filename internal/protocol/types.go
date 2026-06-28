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
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
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
	EventToolCallStarted         EventType = "tool.call_started"
	EventToolCallFinished        EventType = "tool.call_finished"
	EventToolCallFailed          EventType = "tool.call_failed"
	EventToolCallAborted         EventType = "tool.call_aborted"
	EventToolOutputRefCreated    EventType = "tool.output_ref_created"
	EventContextCompacted        EventType = "context.compacted"
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
