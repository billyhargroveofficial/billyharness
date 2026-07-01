package transcript

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestProjectorApplyStreamsAndFinalizesAssistantCells(t *testing.T) {
	p := NewProjector()
	p.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: "hello "})
	cells := p.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: "world"})
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	if got := cells[0]; got.Kind != "assistant" || got.CellType != CellTypeAssistantStream || !got.Live || got.Content != "hello world" || got.RawCopy != "hello world" {
		t.Fatalf("stream cell = %#v", got)
	}

	cells = p.Apply(protocol.Event{Type: protocol.EventRunCompleted})
	if got := cells[0]; got.CellType != CellTypeAssistantFinal || got.Live {
		t.Fatalf("final cell = %#v", got)
	}
}

func TestProjectorApplyUpdatesToolCellsByCallID(t *testing.T) {
	p := NewProjector()
	p.Apply(protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-1",
		StepID: "step-1",
		Data: protocol.ToolCall{
			ID:        "call-a",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		},
	})
	p.Apply(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-b",
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"agent"}`),
		},
	})
	cells := p.Apply(protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{
			CallID:  "call-a",
			Name:    "fs_read_file",
			Content: "alpha",
		},
	})

	if len(cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(cells))
	}
	if got := cells[0]; got.CallID != "call-a" || got.ToolName != "fs_read_file" || !strings.Contains(got.Title, "Done Read README.md") || !strings.Contains(got.Content, "alpha") {
		t.Fatalf("call-a cell = %#v", got)
	}
	if got := cells[1]; got.CallID != "call-b" || strings.Contains(got.Content, "alpha") {
		t.Fatalf("call-b cell = %#v", got)
	}
}

func TestProjectorApplyToolCompactLifecycleCells(t *testing.T) {
	p := NewProjector()
	cells := p.Apply(protocol.Event{
		Type: protocol.EventToolCallProgress,
		Data: protocol.ToolProgressEvent{
			CallID: "call-a",
			Name:   "custom_tool",
			Phase:  "executing",
			Status: protocol.StepStatusStarted,
			Compact: &protocol.ToolCompact{
				CallID:    "call-a",
				Name:      "custom_tool",
				Lifecycle: "executing",
				Status:    protocol.StepStatusStarted,
				Summary:   "started custom_tool target=README.md",
			},
		},
	})
	if len(cells) != 1 || cells[0].Title != "Tool progress" || !strings.Contains(cells[0].Content, "started custom_tool") {
		t.Fatalf("progress cell = %#v", cells)
	}
	if strings.Contains(cells[0].Content, `"call_id"`) || strings.Contains(cells[0].Content, `"compact"`) {
		t.Fatalf("progress cell leaked raw JSON: %q", cells[0].Content)
	}
	cells = p.Apply(protocol.Event{
		Type: protocol.EventToolOutputRefCreated,
		Data: protocol.ToolOutputRefEvent{
			CallID:    "call-a",
			Name:      "custom_tool",
			AttemptID: "attempt-a",
			OutputRef: "/root/billyharness/tool-output/custom.txt",
			Compact: &protocol.ToolCompact{
				CallID:    "call-a",
				Name:      "custom_tool",
				Lifecycle: "output_ref",
				Status:    "output_ref",
				Summary:   "custom_tool output ref",
				OutputRef: "/root/billyharness/tool-output/custom.txt",
			},
		},
	})
	if len(cells) != 2 || cells[1].Title != "Tool output" || !strings.Contains(cells[1].Content, "custom.txt") {
		t.Fatalf("output ref cell = %#v", cells)
	}
}

func TestProjectorApplyTodoPlanStateWithoutRawArgs(t *testing.T) {
	p := NewProjector()
	cells := p.Apply(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call_todo",
			Name:      "todo_write",
			Arguments: json.RawMessage(`{"todos":[{"id":"raw","content":"raw secret payload","status":"pending"}]}`),
		},
	})
	if len(cells) != 1 || cells[0].Title != "Updated plan" {
		t.Fatalf("todo call cell = %#v", cells)
	}
	if strings.Contains(cells[0].Content, "raw secret") || strings.Contains(cells[0].RawCopy, "raw secret") {
		t.Fatalf("todo call leaked raw args: %#v", cells[0])
	}

	state := protocol.TodoState{Todos: []protocol.TodoItem{
		{ID: "done", Content: "Inspect plan", Status: "completed", Priority: "high"},
		{ID: "build", Content: "Build todo_write", Status: "in_progress", Priority: "high"},
	}}
	cells = p.Apply(protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{
			CallID:  "call_todo",
			Name:    "todo_write",
			Content: "plan 2 todos\n- [completed] (high) done: Inspect plan\n- [in_progress] (high) build: Build todo_write",
			Metadata: map[string]any{
				"todo_state": state,
			},
		},
	})
	if len(cells) != 1 {
		t.Fatalf("todo result cells = %#v", cells)
	}
	for _, want := range []string{"Plan 2 todos", "1 in progress", "1 completed", "now: Build todo_write"} {
		if !strings.Contains(cells[0].Title, want) {
			t.Fatalf("todo result title missing %q: %q", want, cells[0].Title)
		}
	}
	for _, want := range []string{"plan 2 todos", "Build todo_write"} {
		if !strings.Contains(cells[0].Content, want) {
			t.Fatalf("todo result content missing %q:\n%s", want, cells[0].Content)
		}
	}
	for _, notWant := range []string{"raw secret", `"todos"`} {
		if strings.Contains(cells[0].Content, notWant) || strings.Contains(cells[0].RawCopy, notWant) {
			t.Fatalf("todo result leaked %q: %#v", notWant, cells[0])
		}
	}
}

func TestProjectorApplyUsesCompactToolAuditText(t *testing.T) {
	p := NewProjector()
	cells := p.Apply(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"name":          "shell_exec",
		"risk":          string(protocol.RiskExecute),
		"auto_approved": true,
	}})
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	if got := cells[0]; got.Kind != "audit" || got.CellType != CellTypeAuditSecurity || got.Title != "AUDIT" || got.Content != "execute shell_exec auto-approved" {
		t.Fatalf("audit cell = %#v", got)
	}

	p = NewProjector()
	p.Apply(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
		ID:        "call-shell",
		Name:      "shell_exec",
		Arguments: json.RawMessage(`{"argv":["pwd"],"cwd":"/root/billyharness"}`),
	}})
	cells = p.Apply(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"name":          "shell_exec",
		"risk":          string(protocol.RiskExecute),
		"auto_approved": true,
	}})
	if len(cells) != 1 || cells[0].Kind != "tool" || !strings.Contains(cells[0].Content, "audit: execute shell_exec auto-approved") {
		t.Fatalf("tool audit cell = %#v", cells)
	}
}

func TestProjectorApplyUpsertsToolBatchSteps(t *testing.T) {
	p := NewProjector()
	p.Apply(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
		TurnID:        "turn-1",
		StepID:        "batch-1",
		Kind:          protocol.StepKindToolBatch,
		Status:        protocol.StepStatusStarted,
		BatchID:       "batch-1",
		BatchSize:     2,
		Parallel:      true,
		ParallelLimit: 2,
	}})
	cells := p.Apply(protocol.Event{Type: protocol.EventStepCompleted, Data: map[string]any{
		"turn_id":        "turn-1",
		"step_id":        "batch-1",
		"kind":           protocol.StepKindToolBatch,
		"status":         protocol.StepStatusCompleted,
		"batch_id":       "batch-1",
		"batch_size":     2,
		"parallel":       true,
		"parallel_limit": 2,
		"duration_ms":    37,
	}})

	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	if got := cells[0]; got.CellType != CellTypeToolBatch || got.StepID != "batch-1" || !strings.Contains(got.Title, "Tool batch done") || !strings.Contains(got.Title, "0s") {
		t.Fatalf("batch cell = %#v", got)
	}
}

func TestProjectorApplyContextDiagnosticCells(t *testing.T) {
	p := NewProjector()
	cells := p.Apply(protocol.Event{Type: protocol.EventContextCompacted, Data: map[string]any{
		"compaction_id":           "compact-1",
		"reason":                  "threshold",
		"trigger_source":          "after_tool_results",
		"before_estimated_tokens": int64(805000),
		"after_estimated_tokens":  int64(210000),
		"protected_prefix": map[string]any{
			"messages":         2,
			"chars":            400,
			"estimated_tokens": int64(100),
			"reasons": map[string]any{
				"system": float64(1),
			},
		},
	}})
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	if got := cells[0]; got.CellType != CellTypeCompaction || got.Title != "COMPACT" ||
		!strings.Contains(got.Content, "id: compact-1") ||
		!strings.Contains(got.Content, "reason: threshold (after_tool_results)") ||
		!strings.Contains(got.Content, "context: before ~805k / after ~210k") {
		t.Fatalf("compaction cell = %#v", got)
	}

	cells = p.Apply(protocol.Event{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
		Percent:             70,
		EstimatedTokens:     705000,
		ContextWindowTokens: 1000000,
		ThresholdTokens:     700000,
		RemainingTokens:     295000,
		MessageCount:        44,
		Round:               3,
		Stage:               "after_tool_results",
	}})
	if len(cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(cells))
	}
	if got := cells[1]; got.CellType != CellTypeStatus || got.Title != "CONTEXT" ||
		!strings.Contains(got.Content, "threshold: 70%") ||
		!strings.Contains(got.Content, "active: 705k / 1.0m") ||
		!strings.Contains(got.Content, "remaining window: 295k") ||
		!strings.Contains(got.Content, "stage: after_tool_results") {
		t.Fatalf("threshold cell = %#v", got)
	}
}

func TestProjectorApplyTurnDiffDisplayCell(t *testing.T) {
	p := NewProjector()
	cells := p.Apply(protocol.Event{
		Type:   protocol.EventTurnChangeRecorded,
		TurnID: "turn-1",
		Data: protocol.TurnChangeEvent{
			ChangeID:       "change-1",
			TurnID:         "turn-1",
			ToolName:       "shell_exec",
			FileCount:      2,
			Added:          1,
			Modified:       1,
			Additions:      9,
			Deletions:      2,
			BinaryFiles:    1,
			Reversible:     true,
			PatchOutputRef: "/root/billyharness/tool-output/change-1.json",
			Files: []protocol.TurnChangeFile{
				{RelPath: "internal/a.go", Change: "modified", Additions: 9, Deletions: 2, Reversible: true},
				{RelPath: "asset.bin", Change: "added", Binary: true, Reversible: true},
			},
		},
	})
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	got := cells[0]
	if got.Kind != "status" || got.CellType != CellTypeStatus || got.Title != "CHANGES" || got.TurnID != "turn-1" {
		t.Fatalf("turn diff cell = %#v", got)
	}
	for _, want := range []string{"summary: 2 files", "+9 -2", "shell changes", "patch_ref: /root/billyharness/tool-output/change-1.json", "M internal/a.go +9 -2", "A asset.bin binary"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("turn diff cell missing %q:\n%s", want, got.Content)
		}
	}
}

func TestProjectorApplyTurnDiffRevertedCell(t *testing.T) {
	p := NewProjector()
	cells := p.Apply(protocol.Event{Type: protocol.EventTurnChangeReverted, Data: protocol.TurnChangeEvent{
		ChangeID:  "change-1",
		Status:    "reverted",
		FileCount: 1,
		Deleted:   1,
	}})
	if len(cells) != 1 || cells[0].Title != "REVERTED" || !strings.Contains(cells[0].Content, "reverted") {
		t.Fatalf("reverted cell = %#v", cells)
	}
}

func TestProjectorApplyRunSummaryUpsertsLatestSummary(t *testing.T) {
	p := NewProjector()
	p.ApplyRunSummary(RunSummary{EventType: protocol.EventRunStarted, State: "running", SessionModelCalls: 1})
	cells := p.ApplyRunSummary(RunSummary{EventType: protocol.EventRunCompleted, State: "completed", SessionModelCalls: 2, SessionToolCalls: 3})
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	if got := cells[0]; got.CellType != CellTypeRunSummary || !strings.Contains(got.Title, "Run done") || !strings.Contains(got.Content, "agent turns: 0 / session 2") {
		t.Fatalf("summary cell = %#v", got)
	}
}
