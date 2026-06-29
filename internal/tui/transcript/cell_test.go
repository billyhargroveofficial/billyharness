package transcript

import (
	"encoding/json"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestEncodeDecodeCellsPreservesCanonicalFields(t *testing.T) {
	cells := []Cell{
		{
			ID:             "cell-1",
			Kind:           "tool",
			CellType:       CellTypeToolCall,
			Title:          "Done Read README.md",
			Content:        "output",
			EventType:      protocol.EventToolCallFinished,
			TurnID:         "turn-1",
			StepID:         "step-1",
			CallID:         "call-1",
			AttemptID:      "attempt-1",
			ParentStepID:   "parent-1",
			ToolName:       "fs_read_file",
			RawCopy:        "raw output",
			RenderCacheKey: "cache-1",
			Collapsed:      true,
			CollapseSet:    true,
		},
		{
			ID:          "cell-2",
			Kind:        "assistant",
			CellType:    CellTypeAssistantFinal,
			Title:       "ASSISTANT",
			Content:     "hello",
			RawCopy:     "hello",
			Collapsed:   true,
			CollapseSet: false,
		},
	}

	decoded := DecodeCells(EncodeCells(cells))
	if len(decoded) != len(cells) {
		t.Fatalf("decoded cells = %d, want %d", len(decoded), len(cells))
	}
	if got := decoded[0]; got.CellType != CellTypeToolCall || got.CallID != "call-1" || got.ToolName != "fs_read_file" || !got.Collapsed || !got.CollapseSet {
		t.Fatalf("decoded tool cell lost canonical fields: %#v", got)
	}
	if got := decoded[1]; got.Collapsed {
		t.Fatalf("collapse should persist only when explicitly set: %#v", got)
	}
}

func TestApplyEventIdentityExtractsToolNamesWithoutOverwriting(t *testing.T) {
	cell := Cell{ToolName: "existing"}
	ApplyEventIdentity(&cell, protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-1",
		StepID: "step-1",
		Data: protocol.ToolCall{
			ID:        "call-1",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		},
	})
	if cell.ToolName != "existing" || cell.TurnID != "turn-1" || cell.StepID != "step-1" || cell.CallID != "call-1" {
		t.Fatalf("tool call identity = %#v", cell)
	}

	resultCell := Cell{}
	ApplyEventIdentity(&resultCell, protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{
			CallID:  "call-2",
			Name:    "web_search",
			Content: "done",
		},
	})
	if resultCell.ToolName != "web_search" || resultCell.CallID != "call-2" {
		t.Fatalf("tool result identity = %#v", resultCell)
	}

	progressCell := Cell{}
	ApplyEventIdentity(&progressCell, protocol.Event{
		Type: protocol.EventToolCallProgress,
		Data: protocol.ToolProgressEvent{
			CallID:    "call-3",
			Name:      "mcp_call",
			AttemptID: "attempt-3",
		},
	})
	if progressCell.ToolName != "mcp_call" || progressCell.CallID != "call-3" || progressCell.AttemptID != "attempt-3" {
		t.Fatalf("tool progress identity = %#v", progressCell)
	}
}
