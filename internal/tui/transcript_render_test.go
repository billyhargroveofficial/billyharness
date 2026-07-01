package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	tuirender "github.com/billyhargroveofficial/billyharness/internal/tui/render"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func TestHiddenReasoningIsPreserved(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24

	handled, _ := m.handleSlashCommand("/thinking off")
	if !handled {
		t.Fatalf("/thinking off returned false")
	}
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantReasoning, Data: "hidden reasoning"})
	if len(m.blocks) != 1 {
		t.Fatalf("reasoning block was not preserved")
	}
	m.resize(false)
	if strings.Contains(m.viewport.View(), "hidden reasoning") {
		t.Fatalf("hidden reasoning should not render")
	}

	handled, _ = m.handleSlashCommand("/thinking on")
	if !handled {
		t.Fatalf("/thinking on returned false")
	}
	m.reflow(false)
	if !strings.Contains(m.viewport.View(), "hidden reasoning") {
		t.Fatalf("reasoning should render again after /thinking on")
	}
}

func TestToolAndThinkViewsAffectRendering(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("tool", "TOOL shell", "line1\nline2\nline3")
	m.addBlock("reasoning", "THINKING", "private chain")

	handled, _ := m.handleSlashCommand("/toolview hidden")
	if !handled {
		t.Fatalf("/toolview hidden returned false")
	}
	handled, _ = m.handleSlashCommand("/thinkview collapsed")
	if !handled {
		t.Fatalf("/thinkview collapsed returned false")
	}
	m.resize(false)
	view := m.viewport.View()
	if strings.Contains(view, "TOOL shell") {
		t.Fatalf("hidden tool block should not render")
	}
	if !strings.Contains(view, "[collapsed:") || strings.Contains(view, "private chain") {
		t.Fatalf("collapsed thinking should render a preview without full content, view=%q", view)
	}
}

func TestToolViewErrorsOnlyShowsFailedTools(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 24
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
		ID:        "ok-call",
		Name:      "fs_read_file",
		Arguments: json.RawMessage(`{"path":"ok.txt"}`),
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		CallID:  "ok-call",
		Name:    "fs_read_file",
		Content: "ok",
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
		ID:        "bad-call",
		Name:      "fs_read_file",
		Arguments: json.RawMessage(`{"path":"bad.txt"}`),
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		CallID:  "bad-call",
		Name:    "fs_read_file",
		Content: "permission denied",
		IsError: true,
	}})

	handled, _ := m.handleSlashCommand("/toolview errors")
	if !handled {
		t.Fatalf("/toolview errors returned false")
	}
	m.resize(false)
	view := stripANSITest(m.viewport.View())
	if strings.Contains(view, "ok.txt") || strings.Contains(view, "Done Read") {
		t.Fatalf("errors-only view should hide successful tools: %q", view)
	}
	if !strings.Contains(view, "Failed Read bad.txt") || !strings.Contains(view, "permission denied") {
		t.Fatalf("errors-only view should show failed tool: %q", view)
	}
}

func TestToolAndThinkingBlocksRenderWithoutSelectionMarkersOrIndent(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	tool := stripANSITest(m.renderBlock(0, transcript.Cell{Kind: "tool", Title: "TOOL shell", Content: "result"}))
	thinking := stripANSITest(m.renderBlock(1, transcript.Cell{Kind: "reasoning", Title: "THINKING", Content: "thought"}))
	for _, rendered := range []string{tool, thinking} {
		if strings.Contains(rendered, ">") {
			t.Fatalf("block should not render selection marker: %q", rendered)
		}
		if strings.Contains(rendered, "┌") || strings.Contains(rendered, "└─") {
			t.Fatalf("activity block should not render heavy box borders: %q", rendered)
		}
	}
}

func TestToolBlocksRenderCodexActivityStyle(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			Name:      "shell_exec",
			Arguments: json.RawMessage(`{"argv":["rg","-n","selection","internal/tui"],"cwd":"/root/billyharness","timeout_sec":20}`),
		},
	})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: "internal/tui/tui.go:2422: selection\n"})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	for _, want := range []string{"• Ran rg -n selection internal/tui", "└ cwd: /root/billyharness", "│ internal/tui/tui.go:2422: selection"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("tool activity block missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "TOOL") || strings.Contains(rendered, `"argv"`) {
		t.Fatalf("tool activity block should not show raw tool/json chrome: %q", rendered)
	}
}

func TestToolBlocksAreOneLineByDefault(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"agent loop benchmark","limit":5}`),
		},
	})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: strings.Repeat("result line\n", 20)})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if got := strings.Count(strings.TrimSpace(rendered), "\n"); got != 0 {
		t.Fatalf("collapsed tool block should be one line, got %d newlines: %q", got, rendered)
	}
	if !strings.Contains(rendered, "• Searched web agent loop benchmark") {
		t.Fatalf("collapsed tool block should show query in title: %q", rendered)
	}
	if strings.Contains(rendered, "result line") || strings.Contains(rendered, `"query"`) {
		t.Fatalf("collapsed tool block should not show output or raw JSON: %q", rendered)
	}

	m.toggleSelectedBlock()
	expanded := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(expanded, "result line") {
		t.Fatalf("Ctrl+E toggle should expand selected collapsed tool block: %q", expanded)
	}
	if !m.blocks[0].CollapseSet || m.blocks[0].Collapsed {
		t.Fatalf("toggle should persist expanded state on the tool cell: %#v", m.blocks[0])
	}
}

func TestCollapsedStatePersistsWithSavedBlocks(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.addBlock("tool", "Called web_fetch", strings.Repeat("payload\n", 20))
	m.selected = 0
	m.setBlockCollapsed(0, true)

	decoded := decodeBlocks(encodeBlocks(m.blocks))
	if len(decoded) != 1 || !decoded[0].CollapseSet || !decoded[0].Collapsed {
		t.Fatalf("decoded collapsed state = %#v", decoded)
	}
	m.blocks = decoded
	m.collapsed = map[int]bool{}
	m.toolView = "auto"
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(rendered, "[collapsed:") || strings.Count(rendered, "payload") >= 20 {
		t.Fatalf("render should use persisted block collapsed state without map fallback: %q", rendered)
	}
	m.toggleSelectedBlock()
	if !m.blocks[0].CollapseSet || m.blocks[0].Collapsed {
		t.Fatalf("toggle should persist expanded state after decode: %#v", m.blocks[0])
	}
}

func TestToolCollapsedLineUsesFinishedSummary(t *testing.T) {
	m := newTestModel(t)
	m.width = 160
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-fetch",
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com/long/path?secret=query"}`),
		},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{
			CallID:    "call-fetch",
			Name:      "web_fetch",
			Content:   strings.Repeat("payload ", 200),
			Truncated: true,
			OutputRef: "/root/billyharness/tool-output/20260627/123456-web_fetch-call-fetch-abcd.txt",
			Metadata: map[string]any{
				"estimated_text_tokens": int64(1800),
				"duration_ms":           int64(123),
				"web_cache_hit":         true,
			},
		},
	})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if got := strings.Count(strings.TrimSpace(rendered), "\n"); got != 0 {
		t.Fatalf("collapsed finished tool block should be one line, got %d newlines: %q", got, rendered)
	}
	for _, want := range []string{"Done Fetched example.com/long/path?…", "truncated", "123456-web_fetch-call-fetch-abcd.txt", "123ms", "cache hit", "~1.8k tok"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("finished tool summary missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "payload payload") || strings.Contains(rendered, "secret=query") {
		t.Fatalf("collapsed finished tool line leaked payload or raw query: %q", rendered)
	}
}

func TestContextGatheringToolsGroupInCollapsedView(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	for i := 0; i < 12; i++ {
		callID := fmt.Sprintf("call-%02d", i)
		m.applyEvent(protocol.Event{
			Type:   protocol.EventToolCallRequested,
			TurnID: "turn-context",
			Data: protocol.ToolCall{
				ID:        callID,
				Name:      "fs_read_file",
				Arguments: json.RawMessage(fmt.Sprintf(`{"path":"file-%02d.txt"}`, i)),
			},
		})
		m.applyEvent(protocol.Event{
			Type:   protocol.EventToolCallFinished,
			TurnID: "turn-context",
			Data: protocol.ToolResult{
				CallID:  callID,
				Name:    "fs_read_file",
				Content: "payload that should stay behind the group",
			},
		})
	}

	m.reflow(true)
	rendered := stripANSITest(m.viewportContent)
	if !strings.Contains(rendered, "Context tools done") || !strings.Contains(rendered, "12 tools") || !strings.Contains(rendered, "files 12") {
		t.Fatalf("group summary missing: %q", rendered)
	}
	if strings.Contains(rendered, "payload that should stay behind the group") {
		t.Fatalf("collapsed grouped view leaked individual payload: %q", rendered)
	}
	if count := strings.Count(rendered, "Done Read file-"); count > 1 {
		t.Fatalf("collapsed grouped view should hide individual context tools, count=%d view=%q", count, rendered)
	}
	if lines := strings.Count(strings.TrimSpace(rendered), "\n") + 1; lines > 12 {
		t.Fatalf("long context tool run should remain compact, lines=%d view=%q", lines, rendered)
	}

	m.setToolView("expanded")
	m.reflow(true)
	expanded := stripANSITest(m.viewportContent)
	if !strings.Contains(expanded, "Done Read file-00.txt") || !strings.Contains(expanded, "payload that should stay behind the group") {
		t.Fatalf("expanded view should still expose individual tool output: %q", expanded)
	}
}

func TestContextToolGroupingUsesStructuredToolName(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	for i, name := range []string{"web_search", "web_fetch"} {
		callID := fmt.Sprintf("call-%d", i)
		m.applyEvent(protocol.Event{
			Type:   protocol.EventToolCallRequested,
			TurnID: "turn-context",
			Data: protocol.ToolCall{
				ID:        callID,
				Name:      name,
				Arguments: json.RawMessage(`{"query":"title-proof","url":"https://example.com"}`),
			},
		})
	}
	for i := range m.blocks {
		if m.blocks[i].CellType == cellTypeToolCall {
			m.blocks[i].Title = "opaque display text"
		}
	}

	summaries := m.contextToolSummaries("turn-context")
	if len(summaries) != 2 {
		t.Fatalf("summaries = %#v", summaries)
	}
	for _, summary := range summaries {
		if summary.category != "web" {
			t.Fatalf("summary should use structured tool name, got %#v", summary)
		}
	}
	m.upsertContextToolGroup("turn-context")
	rendered := stripANSITest(m.renderBlock(len(m.blocks)-1, m.blocks[len(m.blocks)-1]))
	if !strings.Contains(rendered, "web 2") {
		t.Fatalf("group summary should classify by tool name despite opaque titles: %q", rendered)
	}
}

func TestToolViewCurrentShowsOnlyLatestTurnTools(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.applyEvent(protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-old",
		Data: protocol.ToolCall{
			ID:        "old-call",
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"old query"}`),
		},
	})
	m.applyEvent(protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-new",
		Data: protocol.ToolCall{
			ID:        "new-call",
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"new query"}`),
		},
	})

	if ok := m.setToolView("current"); !ok {
		t.Fatal("/toolview current was rejected")
	}
	m.reflow(true)
	rendered := stripANSITest(m.viewportContent)
	if strings.Contains(rendered, "old query") {
		t.Fatalf("current tool view should hide old turn tools: %q", rendered)
	}
	if !strings.Contains(rendered, "new query") {
		t.Fatalf("current tool view should show latest turn tools: %q", rendered)
	}
	if !m.toolCollapsed(1) {
		t.Fatalf("current tool view should collapse tool cells by default")
	}
}

func TestToolResultsUpdateMatchingBlockByCallIDOutOfOrder(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-a",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"a.txt"}`),
		},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-b",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"b.txt"}`),
		},
	})

	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{CallID: "call-b", Name: "fs_read_file", Content: "beta"}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{CallID: "call-a", Name: "fs_read_file", Content: "alpha"}})

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(m.blocks))
	}
	if !strings.Contains(m.blocks[0].Content, "alpha") || strings.Contains(m.blocks[0].Content, "beta") {
		t.Fatalf("call-a block content = %q", m.blocks[0].Content)
	}
	if !strings.Contains(m.blocks[1].Content, "beta") || strings.Contains(m.blocks[1].Content, "alpha") {
		t.Fatalf("call-b block content = %q", m.blocks[1].Content)
	}
	if m.blocks[0].CallID != "call-a" || m.blocks[1].CallID != "call-b" {
		t.Fatalf("call ids = %q %q", m.blocks[0].CallID, m.blocks[1].CallID)
	}
}

func TestTranscriptBlocksCarryTypedCellMetadata(t *testing.T) {
	m := newTestModel(t)
	m.addBlock("user", "USER", "hello")
	if m.blocks[0].CellType != cellTypeUser || m.blocks[0].ID == "" || m.blocks[0].RenderCacheKey == "" || m.blocks[0].RawCopy != "hello" {
		t.Fatalf("user cell = %#v", m.blocks[0])
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "draft"})
	assistantIndex := len(m.blocks) - 1
	streamKey := m.blocks[assistantIndex].RenderCacheKey
	if m.blocks[assistantIndex].CellType != cellTypeAssistantStream || !m.blocks[assistantIndex].Live {
		t.Fatalf("assistant stream cell = %#v", m.blocks[assistantIndex])
	}
	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	if m.blocks[assistantIndex].CellType != cellTypeAssistantFinal || m.blocks[assistantIndex].Live || m.blocks[assistantIndex].RenderCacheKey == streamKey {
		t.Fatalf("assistant final cell = %#v", m.blocks[assistantIndex])
	}
	if got := m.blocks[len(m.blocks)-1].CellType; got != cellTypeRunSummary {
		t.Fatalf("run summary cellType = %q", got)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantReasoning, Data: "hidden"})
	if got := m.blocks[len(m.blocks)-1].CellType; got != cellTypeThinking {
		t.Fatalf("thinking cellType = %q", got)
	}

	m.applyEvent(protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-001",
		StepID: "turn-001:tool-call-001",
		Data: protocol.ToolCall{
			ID:        "call-1",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		},
	})
	toolCell := m.blocks[len(m.blocks)-1]
	if toolCell.CellType != cellTypeToolCall || toolCell.TurnID != "turn-001" || toolCell.StepID != "turn-001:tool-call-001" || toolCell.CallID != "call-1" || toolCell.ToolName != "fs_read_file" {
		t.Fatalf("tool cell = %#v", toolCell)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
		TurnID:        "turn-001",
		StepID:        "turn-001:tool-batch-001",
		Kind:          protocol.StepKindToolBatch,
		Status:        protocol.StepStatusStarted,
		BatchID:       "turn-001:tool-batch-001",
		BatchSize:     2,
		Parallel:      true,
		ParallelLimit: 2,
	}})
	batchIndex := len(m.blocks) - 1
	batchCell := m.blocks[batchIndex]
	if batchCell.CellType != cellTypeToolBatch || batchCell.StepID != "turn-001:tool-batch-001" || batchCell.TurnID != "turn-001" {
		t.Fatalf("tool batch cell = %#v", batchCell)
	}
	if rendered := stripANSITest(m.renderBlock(batchIndex, batchCell)); !strings.Contains(rendered, "Tool batch running") || !strings.Contains(rendered, "2 tools") {
		t.Fatalf("tool batch render missing compact status: %q", rendered)
	}
	m.applyEvent(protocol.Event{Type: protocol.EventStepCompleted, Data: map[string]any{
		"turn_id":        "turn-001",
		"step_id":        "turn-001:tool-batch-001",
		"kind":           protocol.StepKindToolBatch,
		"status":         protocol.StepStatusCompleted,
		"batch_id":       "turn-001:tool-batch-001",
		"batch_size":     2,
		"parallel":       true,
		"parallel_limit": 2,
		"duration_ms":    37,
	}})
	if len(m.blocks) != batchIndex+1 {
		t.Fatalf("completed batch should update existing cell, blocks=%d batchIndex=%d", len(m.blocks), batchIndex)
	}
	batchCell = m.blocks[batchIndex]
	if batchCell.CellType != cellTypeToolBatch || !strings.Contains(batchCell.Title, "Tool batch done") || !strings.Contains(batchCell.Title, "0s") {
		t.Fatalf("completed tool batch cell = %#v", batchCell)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventContextCompacted, Data: map[string]any{"reason": "threshold"}})
	if got := m.blocks[len(m.blocks)-1].CellType; got != cellTypeCompaction {
		t.Fatalf("compaction cellType = %q", got)
	}
	m.addInfoBlock("MCP", "connected")
	if got := m.blocks[len(m.blocks)-1].CellType; got != cellTypeMCPStatus {
		t.Fatalf("mcp cellType = %q", got)
	}
	m.addBlock("error", "ERROR", "boom")
	if got := m.blocks[len(m.blocks)-1].CellType; got != cellTypeError {
		t.Fatalf("error cellType = %q", got)
	}

	decoded := decodeBlocks(encodeBlocks(m.blocks))
	m.blocks = decoded
	m.ensureBlockMetadata()
	foundToolName := false
	for i, block := range m.blocks {
		if block.ID == "" || block.CellType == "" || block.RenderCacheKey == "" || block.RawCopy == "" {
			t.Fatalf("decoded block[%d] missing metadata: %#v", i, block)
		}
		if block.CallID == "call-1" && block.ToolName == "fs_read_file" {
			foundToolName = true
		}
	}
	if !foundToolName {
		t.Fatalf("decoded blocks lost structured tool name: %#v", m.blocks)
	}
}

func TestTUITranscriptProjectionMatchesTranscriptProjector(t *testing.T) {
	events := []protocol.Event{
		{Type: protocol.EventAssistantDelta, Data: "hello "},
		{Type: protocol.EventAssistantDelta, Data: "world"},
		{Type: protocol.EventAssistantReasoning, Data: "private"},
		{Type: protocol.EventToolAudit, Data: map[string]any{
			"name":          "shell_exec",
			"risk":          string(protocol.RiskExecute),
			"auto_approved": true,
		}},
		{Type: protocol.EventToolCallRequested, TurnID: "turn-001", StepID: "turn-001:tool-call-001", Data: protocol.ToolCall{
			ID:        "call-1",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		}},
		{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
			CallID:  "call-1",
			Name:    "fs_read_file",
			Content: "alpha",
		}},
		{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
			TurnID:        "turn-001",
			StepID:        "turn-001:tool-batch-001",
			Kind:          protocol.StepKindToolBatch,
			Status:        protocol.StepStatusStarted,
			BatchID:       "turn-001:tool-batch-001",
			BatchSize:     2,
			Parallel:      true,
			ParallelLimit: 2,
		}},
		{Type: protocol.EventStepCompleted, Data: map[string]any{
			"turn_id":        "turn-001",
			"step_id":        "turn-001:tool-batch-001",
			"kind":           protocol.StepKindToolBatch,
			"status":         protocol.StepStatusCompleted,
			"batch_id":       "turn-001:tool-batch-001",
			"batch_size":     2,
			"parallel":       true,
			"parallel_limit": 2,
			"duration_ms":    37,
		}},
		{Type: protocol.EventContextCompacted, Data: map[string]any{
			"compaction_id":           "compact-1",
			"reason":                  "threshold",
			"trigger_source":          "after_tool_results",
			"before_estimated_tokens": int64(805000),
			"after_estimated_tokens":  int64(210000),
		}},
		{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
			Percent:             70,
			EstimatedTokens:     705000,
			ContextWindowTokens: 1000000,
			ThresholdTokens:     700000,
			RemainingTokens:     295000,
			MessageCount:        44,
			Round:               3,
			Stage:               "after_tool_results",
		}},
		{Type: protocol.EventRunCompleted},
	}

	m := newTestModel(t)
	p := transcript.NewProjector()
	for _, event := range events {
		p.Apply(event)
		m.applyEvent(event)
	}

	got := nonSummaryTranscriptCells(m.blocks)
	want := p.Cells()
	if len(got) != len(want) {
		t.Fatalf("projected cells = %d, want %d\ngot: %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind ||
			got[i].CellType != want[i].CellType ||
			got[i].Title != want[i].Title ||
			got[i].Content != want[i].Content ||
			got[i].Live != want[i].Live ||
			got[i].EventType != want[i].EventType ||
			got[i].TurnID != want[i].TurnID ||
			got[i].StepID != want[i].StepID ||
			got[i].CallID != want[i].CallID ||
			got[i].ToolName != want[i].ToolName {
			t.Fatalf("cell[%d]\ngot:  %#v\nwant: %#v", i, got[i], want[i])
		}
	}
}

func TestTUITranscriptProjectorPersistsAndRehydratesAfterManualBlocks(t *testing.T) {
	m := newTestModel(t)
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "hello"})
	first := m.transcriptProjector
	if first == nil {
		t.Fatal("transcript projector is nil after projected event")
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: " world"})
	if m.transcriptProjector != first {
		t.Fatal("transcript projector should be reused across projected events")
	}
	if len(m.blocks) != 1 || m.blocks[0].Content != "hello world" {
		t.Fatalf("assistant projection = %#v", m.blocks)
	}

	m.addInfoBlock("STATUS", "manual block")
	if !m.transcriptStale {
		t.Fatal("manual block insertion should mark transcript projector stale")
	}
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "again"})
	if m.transcriptProjector == first {
		t.Fatal("stale transcript projector should be rehydrated before next projected event")
	}
	if len(m.blocks) != 3 || m.blocks[1].Title != "STATUS" || m.blocks[2].Content != "again" {
		t.Fatalf("rehydrated projection should preserve manual block and append new assistant cell: %#v", m.blocks)
	}
}

func nonSummaryTranscriptCells(cells []transcript.Cell) []transcript.Cell {
	out := make([]transcript.Cell, 0, len(cells))
	for _, cell := range cells {
		if cell.CellType == transcript.CellTypeRunSummary {
			continue
		}
		out = append(out, cell)
	}
	return out
}

func TestTranscriptCellsUseModelRichTerminalTextCache(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "A **bold** answer\n"})
	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	m.reflow(true)

	if len(m.blocks) == 0 {
		t.Fatalf("expected transcript blocks: %#v", m.blocks)
	}
	cache := m.richBlockCache(m.blocks[0])
	if cache.Text == "" || cache.Key == "" {
		t.Fatalf("expected model-owned rich terminal cache on first cell: %#v", cache)
	}
	firstRender := cache.Text
	firstKey := cache.Key
	m.reflow(true)
	cache = m.richBlockCache(m.blocks[0])
	if cache.Text != firstRender || cache.Key != firstKey {
		t.Fatalf("rich terminal cache should be stable across identical reflow")
	}
	m.width = 60
	m.reflow(true)
	cache = m.richBlockCache(m.blocks[0])
	if cache.Key == firstKey {
		t.Fatalf("rich terminal cache key should include width")
	}

	decoded := decodeBlocks(encodeBlocks(m.blocks))
	if len(decoded) == 0 {
		t.Fatal("decoded blocks empty")
	}
	if decoded[0].ID != m.blocks[0].ID || decoded[0].Content != m.blocks[0].Content {
		t.Fatalf("decode should preserve transcript data without cache fields: decoded=%#v block=%#v", decoded[0], m.blocks[0])
	}
	if len(m.richRenderCache) == 0 {
		t.Fatal("model-owned rich terminal cache should remain outside persisted blocks")
	}
}

func TestRenderBlockCachedReturnsCacheWithoutMutatingModel(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "A **bold** answer\n"})
	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})

	rendered, cache := m.renderBlockCached(0)
	if rendered == "" || cache.Text == "" || cache.Key == "" {
		t.Fatalf("rendered=%q cache=%#v, want rendered text and cache", rendered, cache)
	}
	if got := m.richBlockCache(m.blocks[0]); got != (tuirender.CellCache{}) {
		t.Fatalf("renderBlockCached should not mutate model cache map: %#v", got)
	}

	m.reflow(true)
	if got := m.richBlockCache(m.blocks[0]); got != cache {
		t.Fatalf("reflow should explicitly apply cache %#v, got=%#v", cache, got)
	}
}

func TestResizeWithoutWidthChangeDoesNotReflowTranscript(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 32
	m.resize(true)
	m.addBlock("assistant", "ASSISTANT", "A cached answer")
	m.reflow(true)
	original := m.viewportContent
	if original == "" {
		t.Fatal("expected initial viewport content")
	}

	cache := m.richBlockCache(m.blocks[0])
	cache.Text = "bad cached render"
	m.setRichBlockCache(m.blocks[0], cache)
	m.textarea.SetValue("typing should only resize input")
	m.resize(false)

	if m.viewportContent != original {
		t.Fatalf("resize without width change should not rebuild transcript: %q", m.viewportContent)
	}
}

func TestPrintableInputDoesNotReflowTranscript(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 32
	m.resize(true)
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "cached transcript"})
	m.reflow(true)
	original := m.viewportContent
	if original == "" {
		t.Fatal("expected initial viewport content")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	updated := next.(Model)
	if updated.viewportContent != original {
		t.Fatalf("printable keypress should not rebuild transcript: %q", updated.viewportContent)
	}
	if updated.textarea.Value() != "x" {
		t.Fatalf("textarea value = %q, want typed input", updated.textarea.Value())
	}
}

func TestRunSummaryCellUpdatesByRunLifecycle(t *testing.T) {
	m := newTestModel(t)
	m.runStartedAt = time.Now().Add(-2 * time.Second)
	m.modelCalls = 4
	m.toolCalls = 7
	m.applyEvent(protocol.Event{Type: protocol.EventRunStarted})
	if len(m.blocks) != 1 || m.blocks[0].CellType != cellTypeRunSummary {
		t.Fatalf("run start should create one summary cell: %#v", m.blocks)
	}
	if !strings.Contains(m.blocks[0].Title, "Run running") {
		t.Fatalf("run start title = %q", m.blocks[0].Title)
	}
	m.applyEvent(protocol.Event{Type: protocol.EventModelCallStarted})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-1", Name: "fs_read_file"}})
	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})

	if summaries := countCells(m.blocks, cellTypeRunSummary); summaries != 1 {
		t.Fatalf("run completion should update summary cell, got %d summaries: %#v", summaries, m.blocks)
	}
	summary := m.blocks[0]
	if !strings.Contains(summary.Title, "Run done") {
		t.Fatalf("run completion title = %q", summary.Title)
	}
	for _, want := range []string{"agent turns: 1 / session 5", "tools: 1 / session 8", "context:"} {
		if !strings.Contains(summary.Content, want) {
			t.Fatalf("run summary missing %q:\n%s", want, summary.Content)
		}
	}
}

func countCells(blocks []transcript.Cell, cellType transcript.CellType) int {
	var count int
	for _, block := range blocks {
		if block.CellType == cellType {
			count++
		}
	}
	return count
}

func TestToolAuditUpdatesMatchingBlockByCallID(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{ID: "call-a", Name: "fs_write_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{ID: "call-b", Name: "fs_read_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolAudit,
		Data: map[string]any{
			"call_id":       "call-a",
			"name":          "fs_write_file",
			"risk":          "write",
			"auto_approved": true,
		},
	})

	if !strings.Contains(m.blocks[0].Content, "audit: write fs_write_file auto-approved") {
		t.Fatalf("call-a block content = %q", m.blocks[0].Content)
	}
	if strings.Contains(m.blocks[1].Content, "audit:") {
		t.Fatalf("call-b should not receive call-a audit: %q", m.blocks[1].Content)
	}
}

func TestToolBlocksCompactLongWebFetchURL(t *testing.T) {
	m := newTestModel(t)
	m.width = 160
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com/some/really/long/path/that/should/not/eat/the/whole/tui/line?with=a&lot=of&query=params"}`),
		},
	})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(rendered, "Fetched example.com") {
		t.Fatalf("tool block should show compact host/path: %q", rendered)
	}
	if strings.Contains(rendered, "with=a&lot=of&query=params") {
		t.Fatalf("tool block leaked full query string: %q", rendered)
	}
	if len([]rune(rendered)) > 140 {
		t.Fatalf("tool block title too long: %d %q", len([]rune(rendered)), rendered)
	}
}

func TestUserAndAssistantBlocksRenderWithoutRoleLabels(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	user := m.renderBlock(0, transcript.Cell{Kind: "user", Title: "USER", Content: "hello"})
	assistant := m.renderBlock(1, transcript.Cell{Kind: "assistant", Title: "ASSISTANT", Content: "world"})
	if strings.Contains(strings.ToLower(user), "user") {
		t.Fatalf("user block should not render role label: %q", user)
	}
	if strings.Contains(strings.ToLower(assistant), "assistant") {
		t.Fatalf("assistant block should not render role label: %q", assistant)
	}
	if !strings.Contains(user, "hello") || !strings.Contains(assistant, "world") {
		t.Fatalf("blocks should render content, got user=%q assistant=%q", user, assistant)
	}
}

func TestAssistantBlockRendersTerminalSafeMarkdown(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	rendered := stripANSITest(m.renderBlock(0, transcript.Cell{Kind: "assistant", Title: "ASSISTANT", Content: strings.Join([]string{
		"# Summary",
		"",
		"- **fast** path with `code`",
		"- _lean_ path",
		"1. [docs](https://example.com)",
		"> quoted",
		"---",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
		"```go",
		"fmt.Println(1)",
		"```",
	}, "\n")}))
	for _, want := range []string{"Summary", "•", "fast", "lean", "code", "docs", "https://example.com", "│ quoted", "────", "┌", "Name", "Billy", "10", "fmt.Println(1)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown missing %q: %q", want, rendered)
		}
	}
	for _, leak := range []string{"```", "**", "_lean_", "`10`"} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("markdown syntax %q should not leak into rendered output: %q", leak, rendered)
		}
	}
}

func TestMarkdownTableDoesNotLeakInlineDelimitersWhenTruncated(t *testing.T) {
	m := newTestModel(t)
	m.width = 48
	rendered := stripANSITest(m.renderBlock(0, transcript.Cell{Kind: "assistant", Title: "ASSISTANT", Content: strings.Join([]string{
		"| Параметр | Значение |",
		"| --- | --- |",
		"| 🌡 **Температура с очень длинным описанием** | +21 °C |",
		"| 💦 **Влажность воздуха тоже длинная** | 45% |",
	}, "\n")}))
	for _, want := range []string{"┌", "Температ", "Влажност", "+21", "45%"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered table missing %q: %q", want, rendered)
		}
	}
	for _, leak := range []string{"**", "__", "`"} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("table markdown delimiter %q leaked after truncation: %q", leak, rendered)
		}
	}
}

func TestLiveAssistantMarkdownKeepsUnstableTailRawUntilCompleted(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Join([]string{
		"## Weather",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
	}, "\n")})
	if len(m.blocks) != 1 || !m.blocks[0].Live {
		t.Fatalf("assistant block should be live: %#v", m.blocks)
	}

	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "| **Billy** | `10` |") {
		t.Fatalf("live markdown tail should stay raw, got: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	if m.blocks[0].Live {
		t.Fatalf("assistant block should be finalized")
	}
	final := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(final, "┌") || !strings.Contains(final, "Billy") || !strings.Contains(final, "10") {
		t.Fatalf("final markdown table did not render: %q", final)
	}
	for _, leak := range []string{"**", "`10`"} {
		if strings.Contains(final, leak) {
			t.Fatalf("final markdown syntax %q leaked: %q", leak, final)
		}
	}
}

func TestAssistantDeltasUpdateSingleLiveBlock(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "Intro\n"})
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "This is **bo"})
	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "Intro") || !strings.Contains(live, "This is **bo") {
		t.Fatalf("incomplete bold line should stay raw while live: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "ld**\n"})
	if len(m.blocks) != 1 {
		t.Fatalf("assistant deltas should update one block, got %#v", m.blocks)
	}
	if !m.blocks[0].Live {
		t.Fatalf("assistant block should remain live")
	}
	if got := m.blocks[0].Content; got != "Intro\nThis is **bold**\n" {
		t.Fatalf("assistant content = %q", got)
	}
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if strings.Contains(rendered, "**") || !strings.Contains(rendered, "bold") {
		t.Fatalf("completed bold line should render as markdown while live: %q", rendered)
	}
}

func TestLiveAssistantMarkdownWaitsForCodeFenceBoundary(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Join([]string{
		"Before",
		"",
		"```go",
		"fmt.Println(1)",
		"```",
	}, "\n")})

	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "Before") {
		t.Fatalf("stable prefix should render before open fence tail: %q", live)
	}
	if !strings.Contains(live, "```go") || !strings.Contains(live, "fmt.Println(1)") {
		t.Fatalf("fence without newline boundary should stay raw while live: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	final := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if strings.Contains(final, "```") || !strings.Contains(final, "fmt.Println(1)") {
		t.Fatalf("final code fence should render without markdown fences: %q", final)
	}
}

func TestLiveAssistantMarkdownWaitsForTableBoundary(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Join([]string{
		"Scores",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
	}, "\n") + "\n"})

	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "Scores") {
		t.Fatalf("stable prefix should render before table tail: %q", live)
	}
	if !strings.Contains(live, "| **Billy** | `10` |") || strings.Contains(live, "┌") {
		t.Fatalf("table without boundary should stay raw while live: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "\nDone\n"})
	live = stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "┌") || !strings.Contains(live, "Billy") || !strings.Contains(live, "10") || !strings.Contains(live, "Done") {
		t.Fatalf("table should render after explicit boundary: %q", live)
	}
	for _, leak := range []string{"**", "`10`"} {
		if strings.Contains(live, leak) {
			t.Fatalf("rendered table syntax %q leaked: %q", leak, live)
		}
	}
}

func TestToolAuditRendersCompactBlock(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"name":          "shell_exec",
		"risk":          string(protocol.RiskExecute),
		"auto_approved": true,
	}})

	if len(m.blocks) != 1 || m.blocks[0].Kind != "audit" {
		t.Fatalf("expected audit block, got %#v", m.blocks)
	}
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	for _, want := range []string{"Tool audit", "execute shell_exec auto-approved"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("audit render missing %q: %q", want, rendered)
		}
	}
}

func TestToolAuditDoesNotSplitToolResult(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: json.RawMessage(`{"argv":["pwd"],"cwd":"/root/billyharness"}`),
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"name":          "shell_exec",
		"risk":          string(protocol.RiskExecute),
		"auto_approved": true,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		Name:    "shell_exec",
		Content: "/root/billyharness\n",
	}})

	if len(m.blocks) != 1 || m.blocks[0].Kind != "tool" {
		t.Fatalf("tool audit/result should stay in one block: %#v", m.blocks)
	}
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	for _, want := range []string{"audit: execute shell_exec auto-approved", "/root/billyharness"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("tool block missing %q: %q", want, rendered)
		}
	}
}

func TestUnsupportedMarkdownImageIsOmitted(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	rendered := stripANSITest(m.renderBlock(0, transcript.Cell{Kind: "assistant", Title: "ASSISTANT", Content: "![secret diagram](https://example.com/image.png)"}))
	if strings.Contains(rendered, "https://example.com/image.png") {
		t.Fatalf("image URL should not render as supported markdown: %q", rendered)
	}
	if !strings.Contains(rendered, "image omitted: secret diagram") {
		t.Fatalf("image placeholder missing: %q", rendered)
	}
}
