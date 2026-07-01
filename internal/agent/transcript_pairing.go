package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type pendingTranscriptToolCall struct {
	ID           string
	Name         string
	MessageIndex int
}

func validateTranscriptPairing(messages []protocol.Message) error {
	pending := map[string]pendingTranscriptToolCall{}
	seenCalls := map[string]int{}
	completedResults := map[string]int{}
	for index, msg := range messages {
		switch msg.Role {
		case protocol.RoleAssistant:
			if len(pending) > 0 {
				call := firstPendingTranscriptToolCall(pending)
				return fmt.Errorf("malformed transcript: assistant message at index %d before tool result for call_id %q from assistant index %d", index, call.ID, call.MessageIndex)
			}
			seenInMessage := map[string]bool{}
			for _, call := range msg.ToolCalls {
				id := strings.TrimSpace(call.ID)
				if id == "" {
					return fmt.Errorf("malformed transcript: assistant message at index %d has tool call with empty id", index)
				}
				if seenInMessage[id] {
					return fmt.Errorf("malformed transcript: assistant message at index %d repeats tool call id %q", index, id)
				}
				if previous, ok := seenCalls[id]; ok {
					return fmt.Errorf("malformed transcript: tool call id %q reused at assistant index %d after assistant index %d", id, index, previous)
				}
				seenInMessage[id] = true
				seenCalls[id] = index
				pending[id] = pendingTranscriptToolCall{ID: id, Name: call.Name, MessageIndex: index}
			}
		case protocol.RoleTool:
			id := strings.TrimSpace(msg.ToolCallID)
			if id == "" {
				return fmt.Errorf("malformed transcript: tool result at index %d has empty tool_call_id", index)
			}
			if previous, ok := completedResults[id]; ok {
				return fmt.Errorf("malformed transcript: duplicate tool result for call_id %q at index %d after index %d", id, index, previous)
			}
			if _, ok := pending[id]; !ok {
				if previous, seen := seenCalls[id]; seen {
					return fmt.Errorf("malformed transcript: tool result for call_id %q at index %d has no pending assistant call after assistant index %d", id, index, previous)
				}
				return fmt.Errorf("malformed transcript: orphan tool result for unknown call_id %q at index %d", id, index)
			}
			delete(pending, id)
			completedResults[id] = index
		default:
			if len(pending) > 0 {
				call := firstPendingTranscriptToolCall(pending)
				return fmt.Errorf("malformed transcript: %s message at index %d before tool result for call_id %q from assistant index %d", msg.Role, index, call.ID, call.MessageIndex)
			}
		}
	}
	if len(pending) > 0 {
		call := firstPendingTranscriptToolCall(pending)
		return fmt.Errorf("malformed transcript: missing tool result for call_id %q from assistant index %d", call.ID, call.MessageIndex)
	}
	return nil
}

func firstPendingTranscriptToolCall(pending map[string]pendingTranscriptToolCall) pendingTranscriptToolCall {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return pending[ids[0]]
}
