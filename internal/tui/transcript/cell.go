package transcript

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type CellType string

const (
	CellTypeUser            CellType = "user"
	CellTypeAssistantStream CellType = "assistant_stream"
	CellTypeAssistantFinal  CellType = "assistant_final"
	CellTypeThinking        CellType = "thinking"
	CellTypeToolCall        CellType = "tool_call"
	CellTypeToolBatch       CellType = "tool_batch"
	CellTypeToolGroup       CellType = "tool_group"
	CellTypeAuditSecurity   CellType = "audit_security"
	CellTypeCompaction      CellType = "compaction"
	CellTypeMCPStatus       CellType = "mcp_status"
	CellTypeRunSummary      CellType = "run_summary"
	CellTypeError           CellType = "error"
	CellTypeStatus          CellType = "status"
)

type Cell struct {
	ID             string
	Kind           string
	CellType       CellType
	Title          string
	Content        string
	Live           bool
	EventType      protocol.EventType
	TurnID         string
	StepID         string
	CallID         string
	AttemptID      string
	ParentStepID   string
	ToolName       string
	RawCopy        string
	RenderCacheKey string
	Collapsed      bool
	CollapseSet    bool
	Started        time.Time
	Updated        time.Time
}

type PersistedCell struct {
	ID             string             `json:"id,omitempty"`
	Kind           string             `json:"kind"`
	CellType       CellType           `json:"cell_type,omitempty"`
	Title          string             `json:"title"`
	Content        string             `json:"content"`
	EventType      protocol.EventType `json:"event_type,omitempty"`
	TurnID         string             `json:"turn_id,omitempty"`
	StepID         string             `json:"step_id,omitempty"`
	CallID         string             `json:"call_id,omitempty"`
	AttemptID      string             `json:"attempt_id,omitempty"`
	ParentStepID   string             `json:"parent_step_id,omitempty"`
	ToolName       string             `json:"tool_name,omitempty"`
	RawCopy        string             `json:"raw_copy,omitempty"`
	RenderCacheKey string             `json:"render_cache_key,omitempty"`
	Collapsed      bool               `json:"collapsed,omitempty"`
	CollapseSet    bool               `json:"collapse_set,omitempty"`
}

func EncodeCells(cells []Cell) []PersistedCell {
	out := make([]PersistedCell, 0, len(cells))
	for _, cell := range cells {
		out = append(out, PersistedCell{
			ID:             cell.ID,
			Kind:           cell.Kind,
			CellType:       cell.CellType,
			Title:          cell.Title,
			Content:        cell.Content,
			EventType:      cell.EventType,
			TurnID:         cell.TurnID,
			StepID:         cell.StepID,
			CallID:         cell.CallID,
			AttemptID:      cell.AttemptID,
			ParentStepID:   cell.ParentStepID,
			ToolName:       cell.ToolName,
			RawCopy:        cell.RawCopy,
			RenderCacheKey: cell.RenderCacheKey,
			Collapsed:      cell.CollapseSet && cell.Collapsed,
			CollapseSet:    cell.CollapseSet,
		})
	}
	return out
}

func DecodeCells(cells []PersistedCell) []Cell {
	out := make([]Cell, 0, len(cells))
	for _, cell := range cells {
		out = append(out, Cell{
			ID:             cell.ID,
			Kind:           cell.Kind,
			CellType:       cell.CellType,
			Title:          cell.Title,
			Content:        cell.Content,
			EventType:      cell.EventType,
			TurnID:         cell.TurnID,
			StepID:         cell.StepID,
			CallID:         cell.CallID,
			AttemptID:      cell.AttemptID,
			ParentStepID:   cell.ParentStepID,
			ToolName:       cell.ToolName,
			RawCopy:        cell.RawCopy,
			RenderCacheKey: cell.RenderCacheKey,
			Collapsed:      cell.Collapsed,
			CollapseSet:    cell.CollapseSet,
		})
	}
	return out
}

func ApplyEventIdentity(cell *Cell, event protocol.Event) {
	if cell == nil {
		return
	}
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	if name := EventToolName(event); name != "" && cell.ToolName == "" {
		cell.ToolName = name
	}
	if event.TurnID != "" && cell.TurnID == "" {
		cell.TurnID = event.TurnID
	}
	if event.StepID != "" && cell.StepID == "" {
		cell.StepID = event.StepID
	}
	if event.CallID != "" && cell.CallID == "" {
		cell.CallID = event.CallID
	}
	if event.AttemptID != "" && cell.AttemptID == "" {
		cell.AttemptID = event.AttemptID
	}
	if event.ParentStepID != "" && cell.ParentStepID == "" {
		cell.ParentStepID = event.ParentStepID
	}
}

func EventToolName(event protocol.Event) string {
	switch event.Type {
	case protocol.EventToolCallRequested:
		if call, ok := decodeToolCall(event.Data); ok {
			return strings.TrimSpace(call.Name)
		}
	case protocol.EventToolCallStarted, protocol.EventToolCallProgress, protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		if result, ok := decodeToolResult(event.Data); ok && strings.TrimSpace(result.Name) != "" {
			return strings.TrimSpace(result.Name)
		}
		if progress, ok := decodeToolProgress(event.Data); ok && strings.TrimSpace(progress.Name) != "" {
			return strings.TrimSpace(progress.Name)
		}
	}
	return ""
}

func decodeToolCall(value any) (protocol.ToolCall, bool) {
	bytes, _ := json.Marshal(value)
	var call protocol.ToolCall
	if err := json.Unmarshal(bytes, &call); err != nil || strings.TrimSpace(call.Name) == "" {
		return protocol.ToolCall{}, false
	}
	return call, true
}

func decodeToolResult(value any) (protocol.ToolResult, bool) {
	bytes, _ := json.Marshal(value)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err != nil {
		return protocol.ToolResult{}, false
	}
	return result, result.Name != "" || result.CallID != ""
}

func decodeToolProgress(value any) (protocol.ToolProgressEvent, bool) {
	bytes, _ := json.Marshal(value)
	var progress protocol.ToolProgressEvent
	if err := json.Unmarshal(bytes, &progress); err != nil {
		return protocol.ToolProgressEvent{}, false
	}
	return progress, progress.Name != "" || progress.CallID != ""
}
