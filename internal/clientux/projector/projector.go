package projector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type RunState string

const (
	RunStateIdle      RunState = "idle"
	RunStateRunning   RunState = "running"
	RunStateCompleted RunState = "completed"
	RunStateFailed    RunState = "failed"
)

type Snapshot struct {
	AssistantText string
	ReasoningText string

	RunState  RunState
	LastSeq   int64
	SeqGap    *EventSeqGap
	LastError string
	Errors    []string

	ModelCalls int
	ToolCalls  int

	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64

	LastInputTokens     int64
	LastOutputTokens    int64
	LastCacheHitTokens  int64
	LastCacheMissTokens int64

	ToolSummaryInputTokens  int64
	ToolSummaryOutputTokens int64
	ToolSummaryAPITokens    int64

	ToolsByCallID     map[string]ToolItem
	ContextThresholds []ContextThreshold
	TurnChanges       []TurnChangeItem
	LatestTurnChange  *TurnChangeItem
	TodoState         protocol.TodoState
}

type ToolItem struct {
	CallID    string
	AttemptID string
	Name      string
	Call      protocol.ToolCall
	Compact   *protocol.ToolCompact
	Status    string
	Content   string
	Error     string
	IsError   bool
	LastEvent protocol.EventType
}

type ContextThreshold struct {
	Percent             int
	EstimatedTokens     int64
	ContextWindowTokens int64
	ThresholdTokens     int64
	RemainingTokens     int64
	MessageCount        int
	Stage               string
}

type TurnChangeItem struct {
	ChangeID         string
	Status           string
	Summary          string
	ToolName         string
	FileCount        int
	Added            int
	Modified         int
	Deleted          int
	Directories      int
	Additions        int
	Deletions        int
	BinaryFiles      int
	LargeFiles       int
	Reversible       bool
	PatchOutputRef   string
	PatchOutputRefID string
	PreviewTruncated bool
	Files            []protocol.TurnChangeFile
	LastEvent        protocol.EventType
}

type EventSeqGap struct {
	AfterSeq int64
	GotSeq   int64
}

type Projector struct {
	assistant             strings.Builder
	reasoning             strings.Builder
	snapshot              Snapshot
	usage                 usageAccumulator
	pendingAssistantBreak bool
}

func New() *Projector {
	p := &Projector{}
	p.resetRun()
	return p
}

func (p *Projector) Apply(event protocol.Event) Snapshot {
	if p == nil {
		return Snapshot{RunState: RunStateIdle, ToolsByCallID: map[string]ToolItem{}}
	}
	if event.Seq > 0 && event.Seq <= p.snapshot.LastSeq {
		return p.Snapshot()
	}
	if event.Seq > 0 {
		if p.snapshot.LastSeq > 0 && event.Seq > p.snapshot.LastSeq+1 {
			p.snapshot.SeqGap = &EventSeqGap{AfterSeq: p.snapshot.LastSeq, GotSeq: event.Seq}
		}
		p.snapshot.LastSeq = event.Seq
	}
	switch event.Type {
	case protocol.EventRunStarted:
		p.resetRun()
		if event.Seq > 0 {
			p.snapshot.LastSeq = event.Seq
		}
		p.snapshot.RunState = RunStateRunning
	case protocol.EventModelCallStarted:
		p.snapshot.ModelCalls++
		p.usage.Reset()
		if strings.TrimSpace(p.assistant.String()) != "" {
			p.pendingAssistantBreak = true
		}
	case protocol.EventAssistantDelta:
		text := fmt.Sprint(event.Data)
		if p.pendingAssistantBreak && strings.TrimSpace(text) != "" {
			existing := p.assistant.String()
			switch {
			case strings.HasSuffix(existing, "\n\n"):
			case strings.HasSuffix(existing, "\n"):
				p.assistant.WriteString("\n")
			default:
				p.assistant.WriteString("\n\n")
			}
			p.pendingAssistantBreak = false
		}
		p.assistant.WriteString(text)
		p.snapshot.AssistantText = p.assistant.String()
	case protocol.EventAssistantReasoning:
		text := fmt.Sprint(event.Data)
		p.reasoning.WriteString(text)
		p.snapshot.ReasoningText = p.reasoning.String()
	case protocol.EventToolCallRequested:
		p.snapshot.ToolCalls++
		p.upsertToolCall(event)
		if strings.TrimSpace(p.assistant.String()) != "" {
			p.pendingAssistantBreak = true
		}
	case protocol.EventToolCallStarted:
		p.upsertToolStatus(event, "running", "")
	case protocol.EventToolCallProgress:
		p.upsertToolProgress(event)
	case protocol.EventToolOutputRefCreated:
		p.upsertToolOutputRef(event)
	case protocol.EventToolCallFinished:
		p.upsertToolResult(event, "finished")
		p.observeToolSummary(event.Data)
	case protocol.EventToolCallFailed:
		p.upsertToolResult(event, "failed")
		p.observeToolSummary(event.Data)
	case protocol.EventToolCallAborted:
		p.upsertToolResult(event, "aborted")
		p.observeToolSummary(event.Data)
	case protocol.EventContextThreshold:
		if threshold, ok := decodeData[protocol.ContextThresholdEvent](event.Data); ok {
			p.snapshot.ContextThresholds = append(p.snapshot.ContextThresholds, ContextThreshold{
				Percent:             threshold.Percent,
				EstimatedTokens:     threshold.EstimatedTokens,
				ContextWindowTokens: threshold.ContextWindowTokens,
				ThresholdTokens:     threshold.ThresholdTokens,
				RemainingTokens:     threshold.RemainingTokens,
				MessageCount:        threshold.MessageCount,
				Stage:               threshold.Stage,
			})
		}
	case protocol.EventTurnChangeRecorded:
		if change, ok := decodeData[protocol.TurnChangeEvent](event.Data); ok {
			p.upsertTurnChange(change, event.Type)
		}
	case protocol.EventTurnChangeReverted:
		if change, ok := decodeData[protocol.TurnChangeEvent](event.Data); ok {
			p.upsertTurnChange(change, event.Type)
		}
	case protocol.EventProviderUsageUpdate:
		delta := p.usage.Apply(parseUsage(event.Data))
		current := p.usage.Current()
		p.snapshot.InputTokens += delta.InputTokens
		p.snapshot.OutputTokens += delta.OutputTokens
		p.snapshot.CacheHitTokens += delta.CacheHitTokens
		p.snapshot.CacheMissTokens += delta.CacheMissTokens
		p.snapshot.ReasoningTokens += delta.ReasoningTokens
		p.snapshot.LastInputTokens = current.InputTokens
		p.snapshot.LastOutputTokens = current.OutputTokens
		p.snapshot.LastCacheHitTokens = current.CacheHitTokens
		p.snapshot.LastCacheMissTokens = current.CacheMissTokens
	case protocol.EventRunCompleted:
		p.snapshot.RunState = RunStateCompleted
	case protocol.EventRunFailed:
		errText := strings.TrimSpace(fmt.Sprint(event.Data))
		p.snapshot.RunState = RunStateFailed
		p.snapshot.LastError = errText
		if errText != "" {
			p.snapshot.Errors = append(p.snapshot.Errors, errText)
		}
	}
	return p.Snapshot()
}

func (p *Projector) Snapshot() Snapshot {
	if p == nil {
		return Snapshot{RunState: RunStateIdle, ToolsByCallID: map[string]ToolItem{}}
	}
	out := p.snapshot
	out.ToolsByCallID = make(map[string]ToolItem, len(p.snapshot.ToolsByCallID))
	for key, value := range p.snapshot.ToolsByCallID {
		out.ToolsByCallID[key] = cloneToolItem(value)
	}
	out.ContextThresholds = append([]ContextThreshold(nil), p.snapshot.ContextThresholds...)
	out.TurnChanges = cloneTurnChangeItems(p.snapshot.TurnChanges)
	out.TodoState = cloneTodoState(p.snapshot.TodoState)
	if p.snapshot.LatestTurnChange != nil {
		latest := cloneTurnChangeItem(*p.snapshot.LatestTurnChange)
		out.LatestTurnChange = &latest
	}
	out.Errors = append([]string(nil), p.snapshot.Errors...)
	if p.snapshot.SeqGap != nil {
		gap := *p.snapshot.SeqGap
		out.SeqGap = &gap
	}
	return out
}

func (p *Projector) resetRun() {
	p.assistant.Reset()
	p.reasoning.Reset()
	lastSeq := p.snapshot.LastSeq
	seqGap := p.snapshot.SeqGap
	todoState := cloneTodoState(p.snapshot.TodoState)
	p.snapshot = Snapshot{
		RunState:      RunStateIdle,
		LastSeq:       lastSeq,
		SeqGap:        seqGap,
		ToolsByCallID: map[string]ToolItem{},
		TodoState:     todoState,
	}
	p.usage.Reset()
	p.pendingAssistantBreak = false
}

func (p *Projector) upsertTurnChange(change protocol.TurnChangeEvent, eventType protocol.EventType) {
	change.ChangeID = strings.TrimSpace(change.ChangeID)
	if change.ChangeID == "" {
		return
	}
	item := turnChangeItemFromEvent(change, eventType)
	for i := range p.snapshot.TurnChanges {
		if p.snapshot.TurnChanges[i].ChangeID != item.ChangeID {
			continue
		}
		p.snapshot.TurnChanges[i] = item
		latest := cloneTurnChangeItem(item)
		p.snapshot.LatestTurnChange = &latest
		return
	}
	p.snapshot.TurnChanges = append(p.snapshot.TurnChanges, item)
	latest := cloneTurnChangeItem(item)
	p.snapshot.LatestTurnChange = &latest
}

func turnChangeItemFromEvent(change protocol.TurnChangeEvent, eventType protocol.EventType) TurnChangeItem {
	return TurnChangeItem{
		ChangeID:         change.ChangeID,
		Status:           change.Status,
		Summary:          change.Summary,
		ToolName:         change.ToolName,
		FileCount:        change.FileCount,
		Added:            change.Added,
		Modified:         change.Modified,
		Deleted:          change.Deleted,
		Directories:      change.Directories,
		Additions:        change.Additions,
		Deletions:        change.Deletions,
		BinaryFiles:      change.BinaryFiles,
		LargeFiles:       change.LargeFiles,
		Reversible:       change.Reversible,
		PatchOutputRef:   change.PatchOutputRef,
		PatchOutputRefID: change.PatchOutputRefID,
		PreviewTruncated: change.PreviewTruncated,
		Files:            append([]protocol.TurnChangeFile(nil), change.Files...),
		LastEvent:        eventType,
	}
}

func cloneTurnChangeItems(items []TurnChangeItem) []TurnChangeItem {
	out := make([]TurnChangeItem, len(items))
	for i, item := range items {
		out[i] = cloneTurnChangeItem(item)
	}
	return out
}

func cloneTurnChangeItem(item TurnChangeItem) TurnChangeItem {
	item.Files = append([]protocol.TurnChangeFile(nil), item.Files...)
	return item
}

func (p *Projector) upsertToolCall(event protocol.Event) {
	callID := eventCallID(event)
	call, ok := decodeData[protocol.ToolCall](event.Data)
	if ok {
		if callID == "" {
			callID = strings.TrimSpace(call.ID)
		}
	}
	if callID == "" {
		return
	}
	item := p.snapshot.ToolsByCallID[callID]
	item.CallID = callID
	item.LastEvent = event.Type
	item.Status = "requested"
	if ok {
		item.Name = call.Name
		item.Call = cloneToolCall(call)
	}
	p.snapshot.ToolsByCallID[callID] = item
}

func cloneToolItem(item ToolItem) ToolItem {
	item.Call = cloneToolCall(item.Call)
	if item.Compact != nil {
		compact := cloneToolCompact(*item.Compact)
		item.Compact = &compact
	}
	return item
}

func cloneToolCall(call protocol.ToolCall) protocol.ToolCall {
	if call.Arguments != nil {
		call.Arguments = append([]byte(nil), call.Arguments...)
	}
	return call
}

func cloneToolCompact(compact protocol.ToolCompact) protocol.ToolCompact {
	compact.Hints = append([]string(nil), compact.Hints...)
	return compact
}

func (p *Projector) upsertToolStatus(event protocol.Event, status, message string) {
	callID := eventCallID(event)
	if callID == "" {
		return
	}
	item := p.snapshot.ToolsByCallID[callID]
	item.CallID = callID
	item.AttemptID = strings.TrimSpace(event.AttemptID)
	item.Status = status
	item.LastEvent = event.Type
	if message != "" {
		item.Content = message
	}
	p.snapshot.ToolsByCallID[callID] = item
}

func (p *Projector) upsertToolProgress(event protocol.Event) {
	progress, ok := decodeData[protocol.ToolProgressEvent](event.Data)
	if !ok {
		p.upsertToolStatus(event, "progress", "")
		return
	}
	callID := strings.TrimSpace(progress.CallID)
	if callID == "" {
		callID = eventCallID(event)
	}
	if callID == "" {
		return
	}
	item := p.snapshot.ToolsByCallID[callID]
	item.CallID = callID
	item.Name = firstNonEmpty(item.Name, progress.Name)
	item.AttemptID = firstNonEmpty(strings.TrimSpace(progress.AttemptID), strings.TrimSpace(event.AttemptID), item.AttemptID)
	item.Status = firstNonEmpty(progress.Status, progress.Phase, "progress")
	item.Content = progress.Message
	if progress.Compact != nil {
		compact := cloneToolCompact(*progress.Compact)
		item.Compact = &compact
	}
	item.LastEvent = event.Type
	p.snapshot.ToolsByCallID[callID] = item
}

func (p *Projector) upsertToolOutputRef(event protocol.Event) {
	ref, ok := decodeData[protocol.ToolOutputRefEvent](event.Data)
	if !ok {
		return
	}
	callID := strings.TrimSpace(ref.CallID)
	if callID == "" {
		callID = eventCallID(event)
	}
	if callID == "" {
		return
	}
	item := p.snapshot.ToolsByCallID[callID]
	item.CallID = callID
	item.Name = firstNonEmpty(item.Name, ref.Name)
	item.AttemptID = firstNonEmpty(strings.TrimSpace(ref.AttemptID), strings.TrimSpace(event.AttemptID), item.AttemptID)
	item.Status = "output_ref"
	item.LastEvent = event.Type
	if ref.Compact != nil {
		compact := cloneToolCompact(*ref.Compact)
		item.Compact = &compact
	}
	p.snapshot.ToolsByCallID[callID] = item
}

func (p *Projector) upsertToolResult(event protocol.Event, status string) {
	callID := eventCallID(event)
	result, ok := decodeData[protocol.ToolResult](event.Data)
	if ok {
		if callID == "" {
			callID = strings.TrimSpace(result.CallID)
		}
	}
	if callID == "" {
		return
	}
	item := p.snapshot.ToolsByCallID[callID]
	item.CallID = callID
	item.AttemptID = strings.TrimSpace(event.AttemptID)
	item.Status = status
	item.LastEvent = event.Type
	if ok {
		item.Name = firstNonEmpty(item.Name, result.Name)
		item.Content = result.Content
		item.IsError = result.IsError
		item.Error = result.ErrorCode
		if state, ok := todoStateFromMetadata(result.Metadata); ok {
			p.snapshot.TodoState = cloneTodoState(state)
		}
		if result.Compact != nil {
			compact := cloneToolCompact(*result.Compact)
			item.Compact = &compact
		}
	}
	if status == "failed" || status == "aborted" {
		item.IsError = true
		if item.Content == "" {
			item.Content = fmt.Sprint(event.Data)
		}
	}
	p.snapshot.ToolsByCallID[callID] = item
}

func (p *Projector) observeToolSummary(data any) {
	result, ok := decodeData[protocol.ToolResult](data)
	if !ok || result.Metadata == nil {
		return
	}
	p.snapshot.ToolSummaryInputTokens += metadataInt64(result.Metadata, "tool_summary_input_tokens")
	p.snapshot.ToolSummaryOutputTokens += metadataInt64(result.Metadata, "tool_summary_output_tokens")
	apiTokens := metadataInt64(result.Metadata, "tool_summary_api_total_tokens")
	if apiTokens == 0 {
		apiTokens = metadataInt64(result.Metadata, "tool_summary_api_input_tokens") + metadataInt64(result.Metadata, "tool_summary_api_output_tokens")
	}
	p.snapshot.ToolSummaryAPITokens += apiTokens
}

func eventCallID(event protocol.Event) string {
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	return strings.TrimSpace(event.CallID)
}

func decodeData[T any](data any) (T, bool) {
	var out T
	switch value := data.(type) {
	case T:
		return value, true
	case *T:
		if value == nil {
			return out, false
		}
		return *value, true
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return out, false
	}
	if err := json.Unmarshal(bytes, &out); err != nil {
		return out, false
	}
	return out, true
}

func metadataInt64(metadata map[string]any, key string) int64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case float64:
		if value > 0 {
			return int64(value)
		}
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type tokenUsage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64
}

type usageAccumulator struct {
	last    tokenUsage
	hasLast bool
}

func parseUsage(data any) tokenUsage {
	bytes, _ := json.Marshal(data)
	var u struct {
		InputTokens     int64 `json:"input_tokens"`
		OutputTokens    int64 `json:"output_tokens"`
		CacheHitTokens  int64 `json:"cache_hit_tokens"`
		CacheMissTokens int64 `json:"cache_miss_tokens"`
		ReasoningTokens int64 `json:"reasoning_tokens"`
	}
	_ = json.Unmarshal(bytes, &u)
	return tokenUsage{
		InputTokens:     nonNegative(u.InputTokens),
		OutputTokens:    nonNegative(u.OutputTokens),
		CacheHitTokens:  nonNegative(u.CacheHitTokens),
		CacheMissTokens: nonNegative(u.CacheMissTokens),
		ReasoningTokens: nonNegative(u.ReasoningTokens),
	}
}

func (a *usageAccumulator) Reset() {
	a.last = tokenUsage{}
	a.hasLast = false
}

func (a *usageAccumulator) Apply(update tokenUsage) tokenUsage {
	if update == (tokenUsage{}) {
		return tokenUsage{}
	}
	if !a.hasLast {
		a.last = update
		a.hasLast = true
		return update
	}
	if update == a.last {
		return tokenUsage{}
	}
	if update.atLeast(a.last) {
		delta := update.minus(a.last)
		a.last = update
		return delta
	}
	a.last = update
	return update
}

func (a usageAccumulator) Current() tokenUsage {
	if !a.hasLast {
		return tokenUsage{}
	}
	return a.last
}

func (u tokenUsage) atLeast(other tokenUsage) bool {
	return u.InputTokens >= other.InputTokens &&
		u.OutputTokens >= other.OutputTokens &&
		u.CacheHitTokens >= other.CacheHitTokens &&
		u.CacheMissTokens >= other.CacheMissTokens &&
		u.ReasoningTokens >= other.ReasoningTokens
}

func (u tokenUsage) minus(other tokenUsage) tokenUsage {
	return tokenUsage{
		InputTokens:     u.InputTokens - other.InputTokens,
		OutputTokens:    u.OutputTokens - other.OutputTokens,
		CacheHitTokens:  u.CacheHitTokens - other.CacheHitTokens,
		CacheMissTokens: u.CacheMissTokens - other.CacheMissTokens,
		ReasoningTokens: u.ReasoningTokens - other.ReasoningTokens,
	}
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func todoStateFromMetadata(metadata map[string]any) (protocol.TodoState, bool) {
	if len(metadata) == 0 {
		return protocol.TodoState{}, false
	}
	bytes, err := json.Marshal(metadata["todo_state"])
	if err != nil {
		return protocol.TodoState{}, false
	}
	var state protocol.TodoState
	if err := json.Unmarshal(bytes, &state); err != nil {
		return protocol.TodoState{}, false
	}
	state = recountTodoState(state)
	return state, len(state.Todos) > 0 || state.Pending > 0 || state.InProgress > 0 || state.Completed > 0 || state.Blocked > 0
}

func cloneTodoState(state protocol.TodoState) protocol.TodoState {
	state.Todos = append([]protocol.TodoItem(nil), state.Todos...)
	return state
}

func recountTodoState(state protocol.TodoState) protocol.TodoState {
	state.Pending = 0
	state.InProgress = 0
	state.Completed = 0
	state.Blocked = 0
	for _, item := range state.Todos {
		switch item.Status {
		case "pending":
			state.Pending++
		case "in_progress":
			state.InProgress++
		case "completed":
			state.Completed++
		case "blocked":
			state.Blocked++
		}
	}
	return state
}
