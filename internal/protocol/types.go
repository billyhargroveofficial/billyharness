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
	EventRunStarted          EventType = "run.started"
	EventModelCallStarted    EventType = "model.call_started"
	EventModelCallFinished   EventType = "model.call_finished"
	EventAssistantReasoning  EventType = "assistant.reasoning_delta"
	EventAssistantDelta      EventType = "assistant.content_delta"
	EventToolCallRequested   EventType = "tool.call_requested"
	EventToolCallStarted     EventType = "tool.call_started"
	EventToolCallFinished    EventType = "tool.call_finished"
	EventContextCompacted    EventType = "context.compacted"
	EventRunCompleted        EventType = "run.completed"
	EventRunFailed           EventType = "run.failed"
	EventProviderUsageUpdate EventType = "provider.usage"
)

type Event struct {
	Type EventType `json:"type"`
	Data any       `json:"data,omitempty"`
}
