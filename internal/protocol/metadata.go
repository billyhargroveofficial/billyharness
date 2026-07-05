package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

func EventCallID(event Event) string {
	event = EnrichEvent(event, EventEnvelope{})
	return strings.TrimSpace(event.CallID)
}

func MetadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func MetadataInt64(metadata map[string]any, key string) int64 {
	if len(metadata) == 0 {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func MetadataBool(metadata map[string]any, key string) bool {
	if len(metadata) == 0 {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	default:
		return false
	}
}

func DecodeTodoState(value any) (TodoState, bool) {
	if value == nil {
		return TodoState{}, false
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return TodoState{}, false
	}
	var state TodoState
	if err := json.Unmarshal(bytes, &state); err != nil {
		return TodoState{}, false
	}
	state = state.Recount()
	return state, len(state.Todos) > 0 || state.Pending > 0 || state.InProgress > 0 || state.Completed > 0 || state.Blocked > 0
}

func (s TodoState) Recount() TodoState {
	s.Pending = 0
	s.InProgress = 0
	s.Completed = 0
	s.Blocked = 0
	for _, item := range s.Todos {
		switch item.Status {
		case "pending":
			s.Pending++
		case "in_progress":
			s.InProgress++
		case "completed":
			s.Completed++
		case "blocked":
			s.Blocked++
		}
	}
	return s
}
