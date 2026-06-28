package protocol

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const EventSchemaVersion = 1

type EventSource string

const (
	EventSourceAgent    EventSource = "agent"
	EventSourceGateway  EventSource = "gateway"
	EventSourceTUI      EventSource = "tui"
	EventSourceTelegram EventSource = "telegram"
	EventSourceTool     EventSource = "tool"
	EventSourceProvider EventSource = "provider"
	EventSourceMCP      EventSource = "mcp"
	EventSourceBench    EventSource = "bench"
)

type EventEnvelope struct {
	Seq          int64
	Source       EventSource
	TS           string
	SubmissionID string
	RunID        string
	Now          func() time.Time
}

type EventEnricher struct {
	seq    int64
	env    EventEnvelope
	emit   func(Event)
	closed bool
}

func NewEventEnricher(runID string, source EventSource, emit func(Event)) *EventEnricher {
	return NewEventEnricherWithEnvelope(EventEnvelope{
		RunID:  runID,
		Source: source,
	}, emit)
}

func NewEventEnricherWithEnvelope(env EventEnvelope, emit func(Event)) *EventEnricher {
	if env.Now == nil {
		env.Now = func() time.Time {
			return time.Now().UTC()
		}
	}
	return &EventEnricher{
		env:  env,
		emit: emit,
	}
}

func (e *EventEnricher) Emit(event Event) {
	if e == nil || e.emit == nil {
		return
	}
	e.seq++
	env := e.env
	env.Seq = e.seq
	e.emit(EnrichEvent(event, env))
}

func EnrichEvent(event Event, env EventEnvelope) Event {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = EventSchemaVersion
	}
	if env.Seq > 0 && event.Seq == 0 {
		event.Seq = env.Seq
	}
	if env.Source != "" && event.Source == "" {
		event.Source = env.Source
	}
	if env.SubmissionID != "" && event.SubmissionID == "" {
		event.SubmissionID = env.SubmissionID
	}
	if env.RunID != "" && event.RunID == "" {
		event.RunID = env.RunID
	}
	if event.TS == "" {
		event.TS = env.TS
		if event.TS == "" {
			now := time.Now().UTC()
			if env.Now != nil {
				now = env.Now().UTC()
			}
			event.TS = now.Format(time.RFC3339Nano)
		}
	}
	enrichEventIDsFromData(&event)
	return event
}

func ValidateEventEnvelope(event Event) error {
	if event.SchemaVersion == 0 {
		return nil
	}
	if event.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("unsupported event schema_version %d", event.SchemaVersion)
	}
	if event.Type == "" {
		return fmt.Errorf("event missing type")
	}
	if event.Seq <= 0 {
		return fmt.Errorf("%s missing seq", event.Type)
	}
	if event.Source == "" {
		return fmt.Errorf("%s missing source", event.Type)
	}
	if strings.TrimSpace(event.TS) == "" {
		return fmt.Errorf("%s missing ts", event.Type)
	}
	if _, err := time.Parse(time.RFC3339Nano, event.TS); err != nil {
		return fmt.Errorf("%s invalid ts: %w", event.Type, err)
	}

	switch event.Type {
	case EventRunStarted, EventRunCompleted, EventRunFailed, EventContextCompacted:
		return requireEnvelope(event, "run_id")
	case EventTurnStarted, EventTurnCompleted:
		return requireEnvelope(event, "run_id", "turn_id")
	case EventStepStarted, EventStepCompleted:
		return requireEnvelope(event, "run_id", "turn_id", "step_id")
	case EventModelCallStarted, EventModelCallFinished, EventAssistantDelta, EventAssistantReasoning, EventProviderUsageUpdate:
		return requireEnvelope(event, "run_id", "turn_id", "step_id")
	case EventToolCallRequested, EventToolPermissionRequested, EventToolPermissionDecided, EventToolAudit:
		return requireEnvelope(event, "run_id", "call_id")
	case EventToolCallStarted, EventToolCallFinished, EventToolCallFailed, EventToolCallAborted, EventToolOutputRefCreated:
		return requireEnvelope(event, "run_id", "call_id", "attempt_id")
	case EventSessionStatus:
		return nil
	default:
		return nil
	}
}

func requireEnvelope(event Event, fields ...string) error {
	for _, field := range fields {
		switch field {
		case "run_id":
			if strings.TrimSpace(event.RunID) == "" {
				return fmt.Errorf("%s missing run_id", event.Type)
			}
		case "turn_id":
			if strings.TrimSpace(event.TurnID) == "" {
				return fmt.Errorf("%s missing turn_id", event.Type)
			}
		case "step_id":
			if strings.TrimSpace(event.StepID) == "" {
				return fmt.Errorf("%s missing step_id", event.Type)
			}
		case "call_id":
			if strings.TrimSpace(event.CallID) == "" {
				return fmt.Errorf("%s missing call_id", event.Type)
			}
		case "attempt_id":
			if strings.TrimSpace(event.AttemptID) == "" {
				return fmt.Errorf("%s missing attempt_id", event.Type)
			}
		}
	}
	return nil
}

func enrichEventIDsFromData(event *Event) {
	if event == nil || event.Data == nil {
		return
	}
	switch data := event.Data.(type) {
	case TurnEvent:
		copyTurnEnvelope(event, data)
	case *TurnEvent:
		if data != nil {
			copyTurnEnvelope(event, *data)
		}
	case StepEvent:
		copyStepEnvelope(event, data)
	case *StepEvent:
		if data != nil {
			copyStepEnvelope(event, *data)
		}
	case ToolCall:
		if event.CallID == "" {
			event.CallID = data.ID
		}
	case *ToolCall:
		if data != nil && event.CallID == "" {
			event.CallID = data.ID
		}
	case ToolResult:
		copyToolResultEnvelope(event, data)
	case *ToolResult:
		if data != nil {
			copyToolResultEnvelope(event, *data)
		}
	case map[string]any:
		copyMapEnvelope(event, data)
	case json.RawMessage:
		copyRawEnvelope(event, data)
	case []byte:
		copyRawEnvelope(event, json.RawMessage(data))
	}
}

func copyTurnEnvelope(event *Event, turn TurnEvent) {
	if event.TurnID == "" {
		event.TurnID = turn.TurnID
	}
	if event.DurationMS == 0 {
		event.DurationMS = turn.DurationMS
	}
	copyMapEnvelope(event, turn.Metadata)
}

func copyStepEnvelope(event *Event, step StepEvent) {
	if event.TurnID == "" {
		event.TurnID = step.TurnID
	}
	if event.StepID == "" {
		event.StepID = step.StepID
	}
	if event.CallID == "" {
		event.CallID = step.ToolCallID
	}
	if event.ParentStepID == "" && step.BatchID != "" && step.BatchID != step.StepID {
		event.ParentStepID = step.BatchID
	}
	if event.DurationMS == 0 {
		event.DurationMS = step.DurationMS
	}
	copyMapEnvelope(event, step.Metadata)
}

func copyToolResultEnvelope(event *Event, result ToolResult) {
	if event.CallID == "" {
		event.CallID = result.CallID
	}
	copyMapEnvelope(event, result.Metadata)
}

func copyRawEnvelope(event *Event, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		copyMapEnvelope(event, m)
	}
}

func copyMapEnvelope(event *Event, m map[string]any) {
	if len(m) == 0 {
		return
	}
	if event.SubmissionID == "" {
		event.SubmissionID = stringValue(m, "submission_id")
	}
	if event.RunID == "" {
		event.RunID = stringValue(m, "run_id")
	}
	if event.TurnID == "" {
		event.TurnID = stringValue(m, "turn_id")
	}
	if event.StepID == "" {
		event.StepID = stringValue(m, "step_id")
	}
	if event.CallID == "" {
		event.CallID = firstStringValue(m, "call_id", "tool_call_id")
	}
	if event.AttemptID == "" {
		event.AttemptID = stringValue(m, "attempt_id")
	}
	if event.ParentStepID == "" {
		event.ParentStepID = firstStringValue(m, "parent_step_id", "batch_id")
	}
	if event.DurationMS == 0 {
		event.DurationMS = int64Value(m, "duration_ms")
	}
}

func firstStringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(m, key); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(m map[string]any, key string) string {
	switch value := m[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func int64Value(m map[string]any, key string) int64 {
	switch value := m[key].(type) {
	case int:
		return int64(value)
	case int8:
		return int64(value)
	case int16:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case uint:
		return int64(value)
	case uint8:
		return int64(value)
	case uint16:
		return int64(value)
	case uint32:
		return int64(value)
	case uint64:
		if value <= uint64(^uint(0)>>1) {
			return int64(value)
		}
	case float64:
		return int64(value)
	case float32:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	}
	return 0
}
