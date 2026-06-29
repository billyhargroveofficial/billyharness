package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
)

type Projector struct {
	cells   []Cell
	nextSeq int64
}

func NewProjector(cells ...Cell) *Projector {
	p := &Projector{}
	p.cells = append(p.cells, cells...)
	for _, cell := range p.cells {
		if strings.HasPrefix(cell.ID, "cell-") {
			var seq int64
			if _, err := fmt.Sscanf(cell.ID, "cell-%d", &seq); err == nil && seq > p.nextSeq {
				p.nextSeq = seq
			}
		}
	}
	return p
}

func (p *Projector) Cells() []Cell {
	if p == nil {
		return nil
	}
	out := make([]Cell, len(p.cells))
	copy(out, p.cells)
	return out
}

func (p *Projector) Apply(event protocol.Event) []Cell {
	if p == nil {
		return nil
	}
	switch event.Type {
	case protocol.EventAssistantReasoning:
		p.appendToOpenCell("reasoning", CellTypeThinking, "THINKING", fmt.Sprint(event.Data), event.Type)
	case protocol.EventAssistantDelta:
		p.appendToOpenCell("assistant", CellTypeAssistantStream, "ASSISTANT", fmt.Sprint(event.Data), event.Type)
	case protocol.EventToolAudit:
		p.appendToolAudit(event, auditEventText(event.Data))
	case protocol.EventToolCallRequested:
		p.addProtocolCell(event, "tool", CellTypeToolCall, toolCallTitle(event.Data), toolCallBody(event.Data))
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		p.appendToolResult(event)
	case protocol.EventToolCallStarted, protocol.EventToolCallProgress, protocol.EventToolPermissionRequested, protocol.EventToolPermissionDecided, protocol.EventToolOutputRefCreated:
		p.addProtocolCell(event, "tool", CellTypeToolCall, "Tool event", oneLineJSON(event.Data))
	case protocol.EventStepStarted, protocol.EventStepCompleted:
		p.applyStepEvent(event)
	case protocol.EventContextCompacted:
		p.addCell("status", CellTypeCompaction, "COMPACT", CompactEventText(event.Data), event.Type)
	case protocol.EventContextThreshold:
		p.addCell("status", CellTypeStatus, "CONTEXT", ContextThresholdEventText(event.Data), event.Type)
	case protocol.EventRunStarted:
		p.finishLiveCells()
	case protocol.EventRunCompleted, protocol.EventRunFailed:
		p.finishLiveCells()
	}
	return p.Cells()
}

func (p *Projector) ApplyRunSummary(summary RunSummary) []Cell {
	if p == nil {
		return nil
	}
	cell := NewRunSummaryCell(summary)
	idx := BuildIndex(p.cells)
	i, found := idx.RunSummary()
	if !found || summary.EventType == protocol.EventRunStarted {
		p.addCellFromTemplate(cell)
		return p.Cells()
	}
	p.cells[i].Kind = cell.Kind
	p.cells[i].CellType = cell.CellType
	p.cells[i].Title = cell.Title
	p.cells[i].Content = cell.Content
	p.cells[i].RawCopy = cell.RawCopy
	p.cells[i].EventType = cell.EventType
	p.cells[i].Updated = time.Now().UTC()
	return p.Cells()
}

func (p *Projector) appendToOpenCell(kind string, cellType CellType, title, text string, eventType protocol.EventType) {
	if len(p.cells) == 0 || p.cells[len(p.cells)-1].Kind != kind {
		p.addCell(kind, cellType, title, "", eventType)
	}
	i := len(p.cells) - 1
	if p.cells[i].Content == "" && p.cells[i].RawCopy == p.cells[i].Title {
		p.cells[i].RawCopy = ""
	}
	p.cells[i].Content += text
	p.cells[i].RawCopy += text
	p.cells[i].Live = kind == "assistant" || kind == "reasoning"
	p.cells[i].EventType = eventType
	p.cells[i].Updated = time.Now().UTC()
}

func (p *Projector) appendToolText(event protocol.Event, title, text string) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		text = "[no output]"
	}
	callID := eventCallID(event)
	idx := BuildIndex(p.cells)
	i, ok := idx.ToolCall(callID)
	if !ok {
		if len(p.cells) == 0 || p.cells[len(p.cells)-1].Kind != "tool" {
			p.addProtocolCell(event, "tool", CellTypeToolCall, title, text)
			return
		}
		i = len(p.cells) - 1
		ApplyEventIdentity(&p.cells[i], event)
	}
	if strings.TrimSpace(title) != "" {
		p.cells[i].Title = title
	}
	p.appendTextAt(i, text)
}

func (p *Projector) appendToolAudit(event protocol.Event, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	callID := eventCallID(event)
	idx := BuildIndex(p.cells)
	i, ok := idx.ToolCall(callID)
	if !ok {
		if len(p.cells) == 0 || p.cells[len(p.cells)-1].Kind != "tool" {
			p.addProtocolCell(event, "audit", CellTypeAuditSecurity, "AUDIT", text)
			return
		}
		i = len(p.cells) - 1
		ApplyEventIdentity(&p.cells[i], event)
	}
	if strings.TrimSpace(p.cells[i].Content) == "" {
		p.cells[i].Content = "audit: " + text
	} else {
		p.cells[i].Content = strings.TrimRight(p.cells[i].Content, "\n") + "\naudit: " + text
	}
	p.cells[i].RawCopy = p.cells[i].Content
	p.cells[i].Updated = time.Now().UTC()
}

func (p *Projector) appendToolResult(event protocol.Event) {
	text := toolResultText(event.Data)
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		text = "[no output]"
	}
	callID := eventCallID(event)
	idx := BuildIndex(p.cells)
	i, ok := idx.ToolCall(callID)
	if !ok {
		if len(p.cells) == 0 || p.cells[len(p.cells)-1].Kind != "tool" {
			p.addProtocolCell(event, "tool", CellTypeToolCall, toolResultTitle(event.Data, fallbackToolResultTitle(event)), text)
			return
		}
		i = len(p.cells) - 1
		ApplyEventIdentity(&p.cells[i], event)
	}
	p.cells[i].Title = toolResultTitle(event.Data, p.cells[i].Title)
	p.appendTextAt(i, text)
}

func (p *Projector) appendTextAt(i int, text string) {
	if i < 0 || i >= len(p.cells) {
		return
	}
	content := strings.TrimRight(p.cells[i].Content, "\n")
	if strings.TrimSpace(content) == "" {
		p.cells[i].Content = text
	} else {
		p.cells[i].Content = content + "\n" + text
	}
	p.cells[i].RawCopy = p.cells[i].Content
	p.cells[i].Updated = time.Now().UTC()
}

func (p *Projector) applyStepEvent(event protocol.Event) {
	step, ok := stepEventFromAny(event.Data)
	if !ok || step.Kind != protocol.StepKindToolBatch {
		return
	}
	if step.StepID == "" {
		step.StepID = step.BatchID
	}
	if step.StepID == "" {
		return
	}
	title := toolBatchTitle(step)
	body := toolBatchBody(step)
	idx := BuildIndex(p.cells)
	i, found := idx.Step(step.StepID, CellTypeToolBatch)
	if !found {
		cell := p.newCell("tool", CellTypeToolBatch, title, body, event.Type)
		cell.TurnID = firstNonEmpty(step.TurnID, event.TurnID)
		cell.StepID = step.StepID
		p.cells = append(p.cells, cell)
		return
	}
	p.cells[i].Title = title
	p.cells[i].Content = body
	p.cells[i].RawCopy = body
	p.cells[i].EventType = event.Type
	p.cells[i].Updated = time.Now().UTC()
}

func (p *Projector) addProtocolCell(event protocol.Event, kind string, cellType CellType, title, content string) {
	cell := p.newCell(kind, cellType, title, content, event.Type)
	ApplyEventIdentity(&cell, event)
	p.cells = append(p.cells, cell)
}

func (p *Projector) addCell(kind string, cellType CellType, title, content string, eventType protocol.EventType) {
	p.cells = append(p.cells, p.newCell(kind, cellType, title, content, eventType))
}

func (p *Projector) addCellFromTemplate(cell Cell) {
	next := p.newCell(cell.Kind, cell.CellType, cell.Title, cell.Content, cell.EventType)
	next.RawCopy = cell.RawCopy
	p.cells = append(p.cells, next)
}

func (p *Projector) newCell(kind string, cellType CellType, title, content string, eventType protocol.EventType) Cell {
	now := time.Now().UTC()
	p.nextSeq++
	rawCopy := content
	if strings.TrimSpace(rawCopy) == "" {
		rawCopy = title
	}
	return Cell{
		ID:        fmt.Sprintf("cell-%06d", p.nextSeq),
		Kind:      kind,
		CellType:  cellType,
		Title:     title,
		Content:   content,
		RawCopy:   rawCopy,
		EventType: eventType,
		Started:   now,
		Updated:   now,
	}
}

func (p *Projector) finishLiveCells() {
	now := time.Now().UTC()
	for i := range p.cells {
		if !p.cells[i].Live {
			continue
		}
		p.cells[i].Live = false
		p.cells[i].Updated = now
		if p.cells[i].Kind == "assistant" {
			p.cells[i].CellType = CellTypeAssistantFinal
		}
	}
}

func stepEventFromAny(value any) (protocol.StepEvent, bool) {
	switch step := value.(type) {
	case protocol.StepEvent:
		return step, true
	case *protocol.StepEvent:
		if step == nil {
			return protocol.StepEvent{}, false
		}
		return *step, true
	default:
		bytes, err := json.Marshal(value)
		if err != nil {
			return protocol.StepEvent{}, false
		}
		var out protocol.StepEvent
		if err := json.Unmarshal(bytes, &out); err != nil {
			return protocol.StepEvent{}, false
		}
		return out, out.Kind != ""
	}
}

func toolBatchTitle(step protocol.StepEvent) string {
	status := "Tool batch"
	switch step.Status {
	case protocol.StepStatusStarted:
		status = "Tool batch running"
	case protocol.StepStatusCompleted:
		status = "Tool batch done"
	case protocol.StepStatusFailed:
		status = "Tool batch failed"
	}
	var parts []string
	parts = append(parts, status)
	if step.BatchSize > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", step.BatchSize))
	}
	if step.Parallel {
		if step.ParallelLimit > 0 {
			parts = append(parts, fmt.Sprintf("parallel x%d", step.ParallelLimit))
		} else {
			parts = append(parts, "parallel")
		}
	}
	if step.DurationMS > 0 {
		parts = append(parts, compactDuration(time.Duration(step.DurationMS)*time.Millisecond))
	}
	return strings.Join(parts, " · ")
}

func toolBatchBody(step protocol.StepEvent) string {
	var lines []string
	if step.BatchID != "" {
		lines = append(lines, "batch: "+step.BatchID)
	}
	if step.TurnID != "" {
		lines = append(lines, "turn: "+step.TurnID)
	}
	if step.Round > 0 {
		lines = append(lines, fmt.Sprintf("round: %d", step.Round))
	}
	if step.Status != "" {
		lines = append(lines, "status: "+step.Status)
	}
	if step.Error != "" {
		lines = append(lines, "error: "+step.Error)
	}
	return strings.Join(lines, "\n")
}

func toolCallTitle(value any) string {
	_, line := toolrender.CallKeyAndLine(value, toolrender.StyleTUI)
	if strings.TrimSpace(line) != "" {
		return line
	}
	return "Called " + toolName(value)
}

func toolCallBody(value any) string {
	args := toolrender.CallArgs(value)
	if len(args) == 0 {
		return ""
	}
	switch toolName(value) {
	case "shell_exec":
		return toolMetaLines(args, "cwd", "timeout_sec")
	case "fs_read_file", "fs_list", "fs_search", "fs_make_dir", "web_fetch", "web_extract", "web_search", "web_crawl", "time_now":
		return ""
	case "fs_write_file":
		return toolMetaLines(args, "append", "create_dirs")
	default:
		return pretty(args)
	}
}

func toolResultText(value any) string {
	if result, ok := decodeToolResult(value); ok {
		if strings.TrimSpace(result.Content) != "" {
			return result.Content
		}
	}
	return fmt.Sprint(value)
}

func fallbackToolResultTitle(event protocol.Event) string {
	if result, ok := decodeToolResult(event.Data); ok && strings.TrimSpace(result.Name) != "" {
		return "Called " + strings.TrimSpace(result.Name)
	}
	return "Called tool"
}

func toolResultTitle(value any, base string) string {
	summary, ok := toolrender.ResultSummaryFor(value, base, toolrender.StyleTUI)
	if ok && strings.TrimSpace(summary.Line) != "" {
		return summary.Line
	}
	return base
}

func toolName(value any) string {
	return toolrender.CallName(value)
}

func auditEventText(value any) string {
	fields := mapFromAny(value)
	name := stringField(fields, "name")
	if name == "" {
		name = "tool"
	}
	risk := stringField(fields, "risk")
	if risk == "" {
		risk = "unknown risk"
	}
	decision := "approval required"
	if boolField(fields, "auto_approved") {
		decision = "auto-approved"
	}
	return fmt.Sprintf("%s %s %s", risk, name, decision)
}

func pretty(value any) string {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(bytes)
}

func mapFromAny(value any) map[string]any {
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(bytes, &fields); err != nil {
		return nil
	}
	return fields
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	switch value := fields[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func boolField(fields map[string]any, key string) bool {
	if fields == nil {
		return false
	}
	switch value := fields[key].(type) {
	case bool:
		return value
	case string:
		return value == "true" || value == "1" || value == "yes"
	default:
		return false
	}
}

func toolMetaLines(args map[string]any, keys ...string) string {
	var lines []string
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed == "" || (key == "cwd" && typed == ".") {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %s", key, typed))
		case bool:
			lines = append(lines, fmt.Sprintf("%s: %t", key, typed))
		case float64:
			if typed == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %g", key, typed))
		default:
			if typed != nil {
				lines = append(lines, fmt.Sprintf("%s: %v", key, typed))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func eventCallID(event protocol.Event) string {
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	return strings.TrimSpace(event.CallID)
}

func oneLineJSON(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSpace(string(bytes))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
