package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
	tuirender "github.com/billyhargroveofficial/billyharness/internal/tui/render"
	tuiselection "github.com/billyhargroveofficial/billyharness/internal/tui/selection"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func (m *Model) handleCopyCommand(value string) (bool, tea.Cmd) {
	target := strings.ToLower(strings.TrimSpace(value))
	if target == "" {
		target = "selected"
	}
	text, label, ok := m.semanticCopyText(target)
	if !ok || strings.TrimSpace(text) == "" {
		m.status = "copy target empty: " + target
		return false, nil
	}
	m.status = "copying " + label
	return true, copySelectionCmd(text)
}

func (m Model) semanticCopyText(target string) (text, label string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "selected", "cell", "selected-cell":
		if m.selected < 0 || m.selected >= len(m.blocks) {
			return "", "selected cell", false
		}
		return strings.TrimSpace(m.blocks[m.selected].RawCopy), "selected cell", true
	case "last", "assistant", "last-assistant":
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].Kind == "assistant" {
				return strings.TrimSpace(m.blocks[i].RawCopy), "last assistant", true
			}
		}
		return "", "last assistant", false
	case "tool", "raw-tool", "last-tool", "tool-output":
		if text, ok := m.semanticToolCopyText(); ok {
			return text, "raw tool output", true
		}
		return "", "raw tool output", false
	case "transcript", "all", "full":
		var parts []string
		for _, block := range m.blocks {
			text := strings.TrimSpace(block.RawCopy)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n"), "full transcript", len(parts) > 0
	case "code", "codeblock", "code-block":
		if text, ok := m.semanticCodeBlockCopyText(); ok {
			return text, "code block", true
		}
		return "", "code block", false
	case "command", "input", "line":
		return strings.TrimSpace(m.textarea.Value()), "command line", true
	default:
		return "", target, false
	}
}

func (m Model) semanticToolCopyText() (string, bool) {
	if m.selected >= 0 && m.selected < len(m.blocks) && isToolCopyBlock(m.blocks[m.selected]) {
		text := strings.TrimSpace(m.blocks[m.selected].RawCopy)
		return text, text != ""
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if !isToolCopyBlock(m.blocks[i]) {
			continue
		}
		text := strings.TrimSpace(m.blocks[i].RawCopy)
		if text != "" {
			return text, true
		}
	}
	return "", false
}

func isToolCopyBlock(b transcript.Cell) bool {
	return b.Kind == "tool" || b.CellType == cellTypeToolCall || b.CellType == cellTypeToolBatch
}

func (m Model) semanticCodeBlockCopyText() (string, bool) {
	if m.selected >= 0 && m.selected < len(m.blocks) {
		if text, ok := tuirender.LastFencedCodeBlock(m.blocks[m.selected].RawCopy); ok {
			return text, true
		}
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if text, ok := tuirender.LastFencedCodeBlock(m.blocks[i].RawCopy); ok {
			return text, true
		}
	}
	return "", false
}

func (m *Model) saveSettings() error {
	if m.settingsPath == "" {
		return nil
	}
	m.settings.Theme = m.theme
	m.settings.ToolView = m.toolView
	m.settings.ThinkView = m.thinkView
	m.settings.LastLocalChatID = m.localChatID
	m.settings.LastGatewaySessionID = m.sessionID
	m.settings.LastSelectedModel = m.currentModel()
	m.settings.LastProfile = m.currentProfile()
	m.settings.LastAccessMode = config.NormalizeAccessMode(m.accessMode)
	m.settings.LastReasoningKind = m.currentThinking().kind
	m.settings.LastReasoningEffort = m.currentThinking().effort
	return saveAppSettings(m.settingsPath, m.settings)
}

func encodeBlocks(blocks []transcript.Cell) []savedBlock {
	return transcript.EncodeCells(blocks)
}

func decodeBlocks(blocks []savedBlock) []transcript.Cell {
	return transcript.DecodeCells(blocks)
}

func (m *Model) newBlock(kind, title, content string) transcript.Cell {
	now := time.Now().UTC()
	m.nextBlockSeq++
	rawCopy := content
	if strings.TrimSpace(rawCopy) == "" {
		rawCopy = title
	}
	b := transcript.Cell{
		ID:      fmt.Sprintf("%s-%d", normalizeBlockKind(kind), m.nextBlockSeq),
		Kind:    normalizeBlockKind(kind),
		Title:   title,
		Content: content,
		RawCopy: rawCopy,
		Started: now,
		Updated: now,
	}
	refreshBlockDerivedFields(&b)
	return b
}

func (m *Model) ensureBlockMetadata() {
	now := time.Now().UTC()
	for i := range m.blocks {
		m.blocks[i].Kind = normalizeBlockKind(m.blocks[i].Kind)
		if m.blocks[i].ID == "" {
			m.nextBlockSeq++
			m.blocks[i].ID = fmt.Sprintf("%s-%d", m.blocks[i].Kind, m.nextBlockSeq)
		}
		if m.blocks[i].Started.IsZero() {
			m.blocks[i].Started = now
		}
		if m.blocks[i].Updated.IsZero() {
			m.blocks[i].Updated = m.blocks[i].Started
		}
		if m.blocks[i].RawCopy == "" {
			m.blocks[i].RawCopy = m.blocks[i].Content
			if strings.TrimSpace(m.blocks[i].RawCopy) == "" {
				m.blocks[i].RawCopy = m.blocks[i].Title
			}
		}
		m.refreshBlockDerivedFields(i)
	}
	m.markTranscriptProjectorStale()
}

func normalizeBlockKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "user", "assistant", "reasoning", "tool", "error", "status", "audit":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return "status"
	}
}

func cellTypeForBlock(b transcript.Cell) transcript.CellType {
	switch b.CellType {
	case cellTypeToolBatch, cellTypeToolGroup, cellTypeRunSummary:
		return b.CellType
	}
	switch b.EventType {
	case protocol.EventAssistantDelta:
		if b.Live {
			return cellTypeAssistantStream
		}
		return cellTypeAssistantFinal
	case protocol.EventAssistantReasoning:
		return cellTypeThinking
	case protocol.EventToolCallRequested, protocol.EventToolCallStarted, protocol.EventToolCallFinished,
		protocol.EventToolCallFailed, protocol.EventToolCallAborted, protocol.EventToolCallProgress,
		protocol.EventToolPermissionRequested, protocol.EventToolPermissionDecided, protocol.EventToolOutputRefCreated:
		return cellTypeToolCall
	case protocol.EventToolAudit:
		return cellTypeAuditSecurity
	case protocol.EventContextCompacted:
		return cellTypeCompaction
	case protocol.EventRunCompleted, protocol.EventRunStarted:
		return cellTypeRunSummary
	case protocol.EventRunFailed:
		return cellTypeError
	}
	switch b.Kind {
	case "user":
		return cellTypeUser
	case "assistant":
		if b.Live {
			return cellTypeAssistantStream
		}
		return cellTypeAssistantFinal
	case "reasoning":
		return cellTypeThinking
	case "tool":
		return cellTypeToolCall
	case "audit":
		return cellTypeAuditSecurity
	case "error":
		return cellTypeError
	case "status":
		if strings.EqualFold(strings.TrimSpace(b.Title), "MCP") {
			return cellTypeMCPStatus
		}
		return cellTypeStatus
	default:
		return cellTypeStatus
	}
}

func refreshBlockDerivedFields(b *transcript.Cell) {
	if b == nil {
		return
	}
	b.Kind = normalizeBlockKind(b.Kind)
	b.CellType = cellTypeForBlock(*b)
	b.RenderCacheKey = transcriptRenderCacheKey(*b)
}

func (m *Model) refreshBlockDerivedFields(i int) {
	if i < 0 || i >= len(m.blocks) {
		return
	}
	refreshBlockDerivedFields(&m.blocks[i])
}

func transcriptRenderCacheKey(b transcript.Cell) string {
	return tuirender.BlockCacheKey(tuirender.BlockCacheKeyInput{
		ID:           b.ID,
		Kind:         b.Kind,
		CellType:     string(b.CellType),
		EventType:    string(b.EventType),
		Title:        b.Title,
		Content:      b.Content,
		RawCopy:      b.RawCopy,
		Live:         b.Live,
		TurnID:       b.TurnID,
		StepID:       b.StepID,
		CallID:       b.CallID,
		AttemptID:    b.AttemptID,
		ParentStepID: b.ParentStepID,
		Collapsed:    b.Collapsed,
		CollapseSet:  b.CollapseSet,
	})
}

func blockKindForEvent(eventType protocol.EventType) string {
	switch eventType {
	case protocol.EventAssistantDelta:
		return "assistant"
	case protocol.EventAssistantReasoning:
		return "reasoning"
	case protocol.EventToolCallRequested, protocol.EventToolCallStarted, protocol.EventToolCallFinished:
		return "tool"
	case protocol.EventToolAudit:
		return "audit"
	case protocol.EventContextThreshold:
		return "status"
	case protocol.EventTurnChangeRecorded, protocol.EventTurnChangeReverted:
		return "status"
	case protocol.EventUserInputRequested, protocol.EventUserInputAnswered, protocol.EventUserInputRejected:
		return "status"
	case protocol.EventRunFailed:
		return "error"
	default:
		return "status"
	}
}

func (m *Model) addInfoBlock(title, content string) {
	m.followOutput = true
	m.addBlock("status", title, content)
}

func (m Model) statusText() string {
	mode := "local"
	gateway := "none"
	session := "none"
	if m.gatewayURL != "" {
		mode = "gateway"
		gateway = m.gatewayURL
		if m.sessionID != "" {
			session = m.sessionID
		}
	}
	toolsMode := "safe"
	if m.dangerous {
		toolsMode = "dangerous"
	}
	thinkingDisplay := "hidden"
	if m.thinkView != "hidden" {
		thinkingDisplay = m.thinkView
	}
	follow := "off"
	if m.followOutput {
		follow = "on"
	}
	return fmt.Sprintf(
		"mode: %s\nchat: %s\nprovider: %s\nmodel: %s\nprofile: %s\naccess mode: %s\nreasoning: %s / %s\nthinking blocks: %s\ntool blocks: %s\ntheme: %s\ngateway: %s\ngateway session: %s\nlocal settings: %s\ntools: %s, max rounds %d\ncalls: model %d, tools %d\ntokens: input %d, output %d\ncontext: %s\ncost: %s\nfollow output: %s",
		mode,
		m.localChatID,
		m.currentProvider(),
		m.currentModel(),
		m.currentProfile(),
		m.currentAccessMode(),
		m.currentThinking().kind,
		m.currentThinking().effort,
		thinkingDisplay,
		m.toolView,
		m.theme,
		gateway,
		session,
		m.settingsPath,
		toolsMode,
		m.maxRounds,
		m.modelCalls,
		m.toolCalls,
		m.inputTok,
		m.outputTok,
		m.contextText(),
		m.costText(),
		follow,
	)
}

func (m Model) modelsText() string {
	var lines []string
	for i, model := range m.models {
		marker := " "
		if i == m.modelIndex {
			marker = "*"
		}
		provider := "deepseek"
		if isCodexModel(model) {
			provider = "openai-codex"
		}
		lines = append(lines, fmt.Sprintf("%s %-24s %s", marker, model, provider))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) resetProjectedAccounting() {
	m.uxProjector = uxprojector.New()
	m.runStartModelCalls = m.modelCalls
	m.runStartToolCalls = m.toolCalls
	m.runStartInputTok = m.inputTok
	m.runStartOutputTok = m.outputTok
	m.runStartCacheHit = m.cacheHitTok
	m.runStartCacheMiss = m.cacheMissTok
	m.runStartReasoning = m.reasoningTok
	m.runStartSummaryIn = m.toolSummaryInTok
	m.runStartSummaryOut = m.toolSummaryOutTok
	m.runStartSummaryAPI = m.toolSummaryAPITok
}

func (m *Model) applyProjectedAccounting(event protocol.Event) uxprojector.Snapshot {
	if event.Type == protocol.EventRunStarted || m.uxProjector == nil {
		m.resetProjectedAccounting()
	}
	snapshot := m.uxProjector.Apply(event)
	m.modelCalls = m.runStartModelCalls + snapshot.ModelCalls
	m.toolCalls = m.runStartToolCalls + snapshot.ToolCalls
	m.inputTok = m.runStartInputTok + snapshot.InputTokens
	m.outputTok = m.runStartOutputTok + snapshot.OutputTokens
	m.cacheHitTok = m.runStartCacheHit + snapshot.CacheHitTokens
	m.cacheMissTok = m.runStartCacheMiss + snapshot.CacheMissTokens
	m.reasoningTok = m.runStartReasoning + snapshot.ReasoningTokens
	m.lastInputTok = snapshot.LastInputTokens
	m.lastOutputTok = snapshot.LastOutputTokens
	m.lastCacheHitTok = snapshot.LastCacheHitTokens
	m.lastCacheMissTok = snapshot.LastCacheMissTokens
	m.toolSummaryInTok = m.runStartSummaryIn + snapshot.ToolSummaryInputTokens
	m.toolSummaryOutTok = m.runStartSummaryOut + snapshot.ToolSummaryOutputTokens
	m.toolSummaryAPITok = m.runStartSummaryAPI + snapshot.ToolSummaryAPITokens
	return snapshot
}

func (m *Model) applyProjectedTranscript(event protocol.Event) {
	projector := m.ensureTranscriptProjector()
	previous := append([]transcript.Cell(nil), m.blocks...)
	selected := m.selected
	m.blocks = refreshedTranscriptCells(projector.Apply(event))
	m.selected = selectedAfterTranscriptProjection(previous, m.blocks, selected)
}

func (m *Model) ensureTranscriptProjector() *transcript.Projector {
	if m.transcriptProjector == nil || m.transcriptStale {
		m.resetTranscriptProjector()
	}
	return m.transcriptProjector
}

func (m *Model) resetTranscriptProjector() {
	m.transcriptProjector = transcript.NewProjector(m.blocks...)
	m.transcriptStale = false
}

func (m *Model) markTranscriptProjectorStale() {
	m.transcriptStale = true
}

func refreshedTranscriptCells(cells []transcript.Cell) []transcript.Cell {
	blocks := make([]transcript.Cell, 0, len(cells))
	for _, cell := range cells {
		b := cell
		refreshBlockDerivedFields(&b)
		blocks = append(blocks, b)
	}
	return blocks
}

func selectedAfterTranscriptProjection(previous, next []transcript.Cell, current int) int {
	if len(next) == 0 {
		return 0
	}
	if len(next) > len(previous) {
		return len(next) - 1
	}
	for i := range next {
		if i >= len(previous) || projectedBlockChanged(previous[i], next[i]) {
			return i
		}
	}
	if current >= 0 && current < len(next) {
		return current
	}
	return len(next) - 1
}

func projectedBlockChanged(before, after transcript.Cell) bool {
	return before.ID != after.ID ||
		before.Kind != after.Kind ||
		before.CellType != after.CellType ||
		before.Title != after.Title ||
		before.Content != after.Content ||
		before.Live != after.Live ||
		before.EventType != after.EventType ||
		before.TurnID != after.TurnID ||
		before.StepID != after.StepID ||
		before.CallID != after.CallID ||
		before.AttemptID != after.AttemptID ||
		before.ParentStepID != after.ParentStepID ||
		before.ToolName != after.ToolName ||
		before.RawCopy != after.RawCopy ||
		before.Collapsed != after.Collapsed ||
		before.CollapseSet != after.CollapseSet
}

func transcriptProjectsEvent(eventType protocol.EventType) bool {
	switch eventType {
	case protocol.EventRunStarted,
		protocol.EventAssistantReasoning,
		protocol.EventAssistantDelta,
		protocol.EventToolAudit,
		protocol.EventToolCallRequested,
		protocol.EventToolCallStarted,
		protocol.EventToolCallProgress,
		protocol.EventToolCallFinished,
		protocol.EventToolCallFailed,
		protocol.EventToolCallAborted,
		protocol.EventToolPermissionRequested,
		protocol.EventToolPermissionDecided,
		protocol.EventToolOutputRefCreated,
		protocol.EventStepStarted,
		protocol.EventStepCompleted,
		protocol.EventContextCompacted,
		protocol.EventContextThreshold,
		protocol.EventTurnChangeRecorded,
		protocol.EventTurnChangeReverted,
		protocol.EventRunCompleted,
		protocol.EventRunFailed:
		return true
	default:
		return false
	}
}

func (m *Model) applyEvent(event protocol.Event) {
	if event.Seq > 0 && event.Seq <= m.lastGatewayEventSeq {
		return
	}
	if event.Seq > 0 {
		m.lastGatewayEventSeq = event.Seq
	}
	m.applyProjectedAccounting(event)
	if transcriptProjectsEvent(event.Type) {
		m.applyProjectedTranscript(event)
	}
	switch event.Type {
	case protocol.EventRunStarted:
		m.status = "run started"
		if m.runStartedAt.IsZero() {
			m.runStartedAt = time.Now()
		}
		m.upsertRunSummaryBlock(event.Type, "running", "")
	case protocol.EventModelCallStarted:
		m.status = fmt.Sprintf("model call %d", m.modelCalls)
	case protocol.EventAssistantReasoning:
	case protocol.EventAssistantDelta:
	case protocol.EventToolAudit:
		m.status = "tool audit " + auditToolName(event.Data)
	case protocol.EventToolCallRequested:
		m.status = "running tool " + toolName(event.Data)
		m.upsertContextToolGroup(event.TurnID)
	case protocol.EventToolCallFinished:
		m.collapseToolBlockIfLarge(eventCallID(event))
		m.upsertContextToolGroup(m.turnIDForToolEvent(event))
	case protocol.EventStepStarted, protocol.EventStepCompleted:
		m.applyStepStatus(event)
	case protocol.EventContextCompacted:
		m.status = "context compacted"
	case protocol.EventContextThreshold:
		m.status = "context threshold crossed"
	case protocol.EventTurnChangeRecorded:
		m.status = "turn changes recorded"
	case protocol.EventTurnChangeReverted:
		m.status = "turn changes reverted"
	case protocol.EventProviderUsageUpdate:
	case protocol.EventUserInputRequested:
		if req, ok := protocol.DecodeUserInputRequest(event.Data); ok {
			m.pendingUserInput = &req
			m.addEventBlock(event.Type, "QUESTION", formatUserInputRequest(req))
		}
		m.status = "answer requested"
	case protocol.EventUserInputAnswered:
		m.pendingUserInput = nil
		m.status = "answer sent"
	case protocol.EventUserInputRejected:
		m.pendingUserInput = nil
		m.status = "answer rejected"
	case protocol.EventRunCompleted:
		m.pendingUserInput = nil
		m.status = "completed"
		m.upsertRunSummaryBlock(event.Type, "completed", "")
	case protocol.EventRunFailed:
		m.pendingUserInput = nil
		m.upsertRunSummaryBlock(event.Type, "failed", fmt.Sprint(event.Data))
		m.addEventBlock(event.Type, "ERROR", fmt.Sprint(event.Data))
		m.status = "failed"
	}
}

func (m *Model) queueStreamEvent(event protocol.Event) {
	m.pendingStreamEvents = append(m.pendingStreamEvents, event)
}

func (m *Model) flushStreamEvents() bool {
	if len(m.pendingStreamEvents) == 0 {
		return false
	}
	events := append([]protocol.Event(nil), m.pendingStreamEvents...)
	m.pendingStreamEvents = m.pendingStreamEvents[:0]
	for _, event := range events {
		m.applyEvent(event)
	}
	return true
}

func shouldFlushStreamEvent(event protocol.Event) bool {
	switch event.Type {
	case protocol.EventRunCompleted, protocol.EventRunFailed,
		protocol.EventUserInputRequested, protocol.EventUserInputAnswered, protocol.EventUserInputRejected,
		protocol.EventToolCallRequested, protocol.EventToolCallStarted,
		protocol.EventToolCallFinished, protocol.EventToolCallFailed,
		protocol.EventToolCallAborted, protocol.EventToolOutputRefCreated,
		protocol.EventStepStarted, protocol.EventStepCompleted,
		protocol.EventTurnChangeRecorded, protocol.EventTurnChangeReverted:
		return true
	default:
		return false
	}
}

func (m *Model) upsertRunSummaryBlock(eventType protocol.EventType, state, errText string) {
	previous := append([]transcript.Cell(nil), m.blocks...)
	selected := m.selected
	cells := m.ensureTranscriptProjector().ApplyRunSummary(m.runSummary(eventType, state, errText))
	m.blocks = refreshedTranscriptCells(cells)
	if i, found := m.runSummaryBlockIndex(); found {
		m.selected = i
		return
	}
	m.selected = selectedAfterTranscriptProjection(previous, m.blocks, selected)
}

func (m Model) runSummaryBlockIndex() (int, bool) {
	return transcript.BuildIndex(m.blocks).RunSummary()
}

func (m Model) runSummary(eventType protocol.EventType, state, errText string) transcript.RunSummary {
	return transcript.RunSummary{
		EventType:           eventType,
		State:               state,
		Model:               m.currentModel(),
		Reasoning:           m.currentThinking().effortLabel(),
		Elapsed:             m.currentRunDuration(),
		RunModelCalls:       m.modelCalls - m.runStartModelCalls,
		SessionModelCalls:   m.modelCalls,
		RunToolCalls:        m.toolCalls - m.runStartToolCalls,
		SessionToolCalls:    m.toolCalls,
		ContextTokens:       m.contextTokens(),
		ContextWindowTokens: m.settings.ContextWindowTokens,
		Cost:                m.costText(),
		Error:               errText,
	}
}

func (m Model) currentRunDuration() time.Duration {
	if !m.runStartedAt.IsZero() {
		return time.Since(m.runStartedAt)
	}
	return m.lastRunDuration
}

func (m *Model) applyStepStatus(event protocol.Event) {
	step, ok := stepEventFromAny(event.Data)
	if !ok || step.Kind != protocol.StepKindToolBatch {
		return
	}
	switch step.Status {
	case protocol.StepStatusCompleted:
		m.status = "tool batch completed"
	case protocol.StepStatusFailed:
		m.status = "tool batch failed"
	default:
		m.status = "tool batch running"
	}
}

type contextToolSummary struct {
	title    string
	category string
	status   string
	failed   bool
}

func (m *Model) upsertContextToolGroup(turnID string) {
	if m.toolView != "collapsed" && m.toolView != "current" && m.toolView != "auto" {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = m.latestToolTurnID()
	}
	summaries := m.contextToolSummaries(turnID)
	if len(summaries) < 2 {
		return
	}
	title, body := contextToolGroupText(summaries)
	i, found := m.contextToolGroupIndex(turnID)
	if !found {
		selected := m.selected
		b := m.newBlock("tool", title, body)
		b.CellType = cellTypeToolGroup
		b.EventType = protocol.EventStepStarted
		b.TurnID = turnID
		b.RawCopy = body
		refreshBlockDerivedFields(&b)
		m.blocks = append(m.blocks, b)
		if selected >= 0 && selected < len(m.blocks) {
			m.selected = selected
		}
		m.markTranscriptProjectorStale()
		return
	}
	m.blocks[i].Title = title
	m.blocks[i].Content = body
	m.blocks[i].RawCopy = body
	m.blocks[i].Updated = time.Now().UTC()
	m.refreshBlockDerivedFields(i)
	m.markTranscriptProjectorStale()
}

func (m Model) contextToolSummaries(turnID string) []contextToolSummary {
	var summaries []contextToolSummary
	seen := map[string]bool{}
	for _, b := range m.blocks {
		if b.Kind != "tool" || b.CellType != cellTypeToolCall {
			continue
		}
		if strings.TrimSpace(turnID) != "" && b.TurnID != turnID {
			continue
		}
		if strings.TrimSpace(turnID) == "" && strings.TrimSpace(b.TurnID) != "" {
			continue
		}
		category, ok := contextToolCategory(b)
		if !ok {
			continue
		}
		key := b.CallID
		if key == "" {
			key = b.Title
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		status := contextToolStatus(b)
		summaries = append(summaries, contextToolSummary{
			title:    oneLinePreview(b.Title, 96),
			category: category,
			status:   status,
			failed:   isToolErrorBlock(b),
		})
	}
	return summaries
}

func contextToolGroupText(summaries []contextToolSummary) (string, string) {
	counts := map[string]int{}
	done := 0
	failed := 0
	for _, summary := range summaries {
		counts[summary.category]++
		switch summary.status {
		case "done":
			done++
		case "failed":
			failed++
		}
	}
	state := "running"
	if failed > 0 {
		state = "failed"
	} else if done == len(summaries) {
		state = "done"
	}
	var parts []string
	parts = append(parts, "Context tools "+state, fmt.Sprintf("%d tools", len(summaries)))
	for _, category := range []string{"files", "web", "mcp", "skills", "time"} {
		if counts[category] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", category, counts[category]))
		}
	}
	var lines []string
	for i, summary := range summaries {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("... %d more", len(summaries)-i))
			break
		}
		marker := "•"
		switch summary.status {
		case "done":
			marker = "✓"
		case "failed":
			marker = "!"
		}
		lines = append(lines, marker+" "+summary.title)
	}
	return strings.Join(parts, " · "), strings.Join(lines, "\n")
}

func (m Model) contextToolGroupIndex(turnID string) (int, bool) {
	for i := range m.blocks {
		if m.blocks[i].Kind == "tool" && m.blocks[i].CellType == cellTypeToolGroup && m.blocks[i].TurnID == turnID {
			return i, true
		}
	}
	return 0, false
}

func (m Model) hasContextToolGroup(turnID string) bool {
	_, ok := m.contextToolGroupIndex(turnID)
	return ok
}

func (m Model) shouldHideGroupedContextTool(b transcript.Cell) bool {
	if m.toolView != "collapsed" && m.toolView != "current" {
		return false
	}
	if b.Kind != "tool" || b.CellType != cellTypeToolCall || isToolErrorBlock(b) {
		return false
	}
	if _, ok := contextToolCategory(b); !ok {
		return false
	}
	return m.hasContextToolGroup(b.TurnID)
}

func (m Model) currentToolTurnID() string {
	return m.latestToolTurnID()
}

func (m Model) latestToolTurnID() string {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind != "tool" {
			continue
		}
		if turnID := strings.TrimSpace(m.blocks[i].TurnID); turnID != "" {
			return turnID
		}
	}
	return ""
}

func (m Model) turnIDForToolEvent(event protocol.Event) string {
	if turnID := strings.TrimSpace(event.TurnID); turnID != "" {
		return turnID
	}
	if i, ok := m.toolBlockIndex(eventCallID(event)); ok {
		return strings.TrimSpace(m.blocks[i].TurnID)
	}
	return ""
}

func contextToolCategory(b transcript.Cell) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(b.ToolName)) {
	case "fs_read_file", "fs_list", "fs_search":
		return "files", true
	case "web_search", "web_fetch", "web_extract", "web_crawl":
		return "web", true
	case "mcp_list_tools", "mcp_call":
		return "mcp", true
	case "skill_list", "skill_read":
		return "skills", true
	case "time_now":
		return "time", true
	default:
		return "", false
	}
}

func contextToolStatus(b transcript.Cell) string {
	if isToolErrorBlock(b) {
		return "failed"
	}
	title := strings.ToLower(strings.TrimSpace(b.Title))
	if strings.HasPrefix(title, "done ") {
		return "done"
	}
	return "running"
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

func eventCallID(event protocol.Event) string {
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	return strings.TrimSpace(event.CallID)
}

func (m *Model) collapseToolBlockIfLarge(callID string) {
	i, ok := m.toolBlockIndex(callID)
	if !ok {
		m.collapseLastToolBlockIfLarge()
		return
	}
	if len(m.blocks[i].Content) > 8000 || strings.Count(m.blocks[i].Content, "\n") > 40 {
		m.setBlockCollapsed(i, true)
	}
}

func (m *Model) collapseLastToolBlockIfLarge() {
	if len(m.blocks) == 0 {
		return
	}
	i := len(m.blocks) - 1
	if m.blocks[i].Kind != "tool" {
		return
	}
	if len(m.blocks[i].Content) > 8000 || strings.Count(m.blocks[i].Content, "\n") > 40 {
		m.setBlockCollapsed(i, true)
	}
}

func (m *Model) addBlock(kind, title, content string) {
	b := m.newBlock(kind, title, content)
	m.blocks = append(m.blocks, b)
	m.selected = len(m.blocks) - 1
	m.markTranscriptProjectorStale()
}

func (m *Model) addEventBlock(eventType protocol.EventType, title, content string) {
	b := m.newBlock(blockKindForEvent(eventType), title, content)
	b.EventType = eventType
	b.Live = b.Kind == "assistant" || b.Kind == "reasoning"
	if b.Live && strings.TrimSpace(content) == "" {
		b.RawCopy = ""
	}
	refreshBlockDerivedFields(&b)
	m.blocks = append(m.blocks, b)
	m.selected = len(m.blocks) - 1
	m.markTranscriptProjectorStale()
}

func (m *Model) toolBlockIndex(callID string) (int, bool) {
	return transcript.BuildIndex(m.blocks).ToolCall(callID)
}

func (m *Model) applyEventIdentityToBlock(i int, event protocol.Event) {
	if i < 0 || i >= len(m.blocks) {
		return
	}
	applyEventIdentity(&m.blocks[i], event)
	m.refreshBlockDerivedFields(i)
	m.markTranscriptProjectorStale()
}

func applyEventIdentity(b *transcript.Cell, event protocol.Event) {
	if b == nil {
		return
	}
	transcript.ApplyEventIdentity(b, event)
}

func (m *Model) finishLiveBlocks() {
	now := time.Now().UTC()
	changed := false
	for i := range m.blocks {
		if m.blocks[i].Live {
			m.blocks[i].Live = false
			m.blocks[i].Updated = now
			m.refreshBlockDerivedFields(i)
			changed = true
		}
	}
	if changed {
		m.markTranscriptProjectorStale()
	}
}

func (m *Model) reflow(gotoBottom bool) {
	m.reflowCount++
	var parts []string
	currentToolTurnID := ""
	if m.toolView == "current" {
		currentToolTurnID = m.currentToolTurnID()
	}
	for i, b := range m.blocks {
		if b.Kind == "reasoning" && m.thinkView == "hidden" {
			continue
		}
		if b.Kind == "tool" && m.toolView == "hidden" {
			continue
		}
		if b.Kind == "tool" && m.toolView == "current" && currentToolTurnID != "" && b.TurnID != "" && b.TurnID != currentToolTurnID {
			continue
		}
		if b.Kind == "tool" && m.toolView == "errors" && !isToolErrorBlock(b) {
			continue
		}
		if b.Kind == "tool" && b.CellType == cellTypeToolGroup && m.toolView == "errors" {
			continue
		}
		if b.Kind == "tool" && m.shouldHideGroupedContextTool(b) {
			continue
		}
		rendered, cache := m.renderBlockCached(i)
		m.setRichBlockCache(m.blocks[i], cache)
		parts = append(parts, rendered)
	}
	m.viewportContent = strings.Join(parts, "\n")
	m.viewport.SetContent(m.viewportContent)
	if m.hasSelection() {
		m.applySelectionHighlight()
	}
	if gotoBottom {
		m.viewport.GotoBottom()
	}
}

func isToolErrorBlock(b transcript.Cell) bool {
	if b.Kind != "tool" {
		return false
	}
	switch b.EventType {
	case protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		return true
	}
	title := strings.ToLower(strings.TrimSpace(b.Title))
	return strings.HasPrefix(title, "failed") ||
		strings.Contains(title, " failed ") ||
		strings.Contains(title, " aborted ") ||
		strings.Contains(strings.ToLower(b.Content), "error:")
}

func (m Model) renderBlockCached(i int) (string, tuirender.CellCache) {
	if i < 0 || i >= len(m.blocks) {
		return "", tuirender.CellCache{}
	}
	result := tuirender.NewCellRenderer().Render(tuirender.CellRenderInput{
		Cache:    m.richBlockCache(m.blocks[i]),
		CacheKey: m.richTerminalCacheKeyInput(i, m.blocks[i]),
		Render: func() string {
			return m.renderBlock(i, m.blocks[i])
		},
	})
	return result.Text, result.Cache
}

func (m Model) richBlockCache(b transcript.Cell) tuirender.CellCache {
	id := richBlockCacheID(b)
	if id == "" || m.richRenderCache == nil {
		return tuirender.CellCache{}
	}
	return m.richRenderCache[id]
}

func (m *Model) setRichBlockCache(b transcript.Cell, cache tuirender.CellCache) {
	id := richBlockCacheID(b)
	if id == "" {
		return
	}
	if m.richRenderCache == nil {
		m.richRenderCache = map[string]tuirender.CellCache{}
	}
	if cache.Key == "" && cache.Text == "" {
		delete(m.richRenderCache, id)
		return
	}
	m.richRenderCache[id] = cache
}

func (m *Model) clearRichRenderCache() {
	m.richRenderCache = map[string]tuirender.CellCache{}
}

func richBlockCacheID(b transcript.Cell) string {
	if id := strings.TrimSpace(b.ID); id != "" {
		return id
	}
	return strings.TrimSpace(b.RenderCacheKey)
}

func (m Model) richTerminalCacheKeyInput(i int, b transcript.Cell) tuirender.RichCacheKeyInput {
	return tuirender.RichCacheKeyInput{
		BlockCacheKey:  b.RenderCacheKey,
		Width:          m.width,
		Theme:          m.theme,
		ToolView:       m.toolView,
		ThinkView:      m.thinkView,
		BlockCollapsed: m.blockCollapsed(i),
		ToolCollapsed:  m.toolCollapsed(i),
	}
}

func (m Model) renderBlock(i int, b transcript.Cell) string {
	styles := m.styles()
	style := styles.block
	switch b.Kind {
	case "user":
		style = styles.user
	case "assistant":
		style = styles.assistant
	case "reasoning":
		style = styles.reasoning
	case "tool":
		style = styles.tool
	case "error":
		style = styles.error
	case "status":
		style = styles.statusBlock
	case "audit":
		style = styles.statusBlock
	}
	body := strings.TrimRight(b.Content, "\n")
	if b.Kind == "assistant" && b.Live {
		body = b.Content
	}
	switch {
	case b.Kind == "tool" && m.toolCollapsed(i):
		body = ""
	case b.Kind == "tool" && m.toolView == "auto" && m.blockCollapsed(i):
		body = collapsedPreview(b.Content, 8, 1000)
	case b.Kind == "reasoning" && m.thinkView == "collapsed":
		body = collapsedSummary(b.Content)
	case m.blockCollapsed(i):
		body = collapsedPreview(b.Content, 8, 1000)
	}
	width := max(20, m.width-style.GetHorizontalFrameSize())
	if b.Kind == "assistant" {
		body = tuirender.RenderAssistantMarkdown(body, width, styles.markdown, b.Live)
	}
	if b.Kind == "user" || b.Kind == "assistant" {
		return style.Width(width).Render(body)
	}
	return tuirender.RenderActivityBlock(tuirender.ActivityCell{
		Kind:  b.Kind,
		Title: b.Title,
		Body:  body,
	}, width, styles.activity)
}

func (m Model) toolCollapsed(i int) bool {
	if i < 0 || i >= len(m.blocks) || m.blocks[i].Kind != "tool" {
		return false
	}
	switch m.toolView {
	case "collapsed", "current":
		if !m.blocks[i].CollapseSet {
			return true
		}
		return m.blocks[i].Collapsed
	case "hidden":
		return true
	default:
		return false
	}
}

func (m Model) blockCollapsed(i int) bool {
	if i < 0 || i >= len(m.blocks) {
		return false
	}
	if m.blocks[i].CollapseSet {
		return m.blocks[i].Collapsed
	}
	return m.collapsed[i]
}

func (m *Model) setBlockCollapsed(i int, collapsed bool) {
	if i < 0 || i >= len(m.blocks) {
		return
	}
	m.blocks[i].Collapsed = collapsed
	m.blocks[i].CollapseSet = true
	m.blocks[i].Updated = time.Now().UTC()
	m.refreshBlockDerivedFields(i)
	m.markTranscriptProjectorStale()
	if m.collapsed == nil {
		m.collapsed = map[int]bool{}
	}
	m.collapsed[i] = collapsed
}

func (m *Model) toggleSelectedBlock() {
	if m.selected < 0 || m.selected >= len(m.blocks) {
		return
	}
	if m.blocks[m.selected].Kind == "tool" && m.toolView == "collapsed" {
		m.setBlockCollapsed(m.selected, !m.toolCollapsed(m.selected))
		return
	}
	m.setBlockCollapsed(m.selected, !m.blockCollapsed(m.selected))
}

func blockTitle(b transcript.Cell) string {
	label := strings.ToLower(b.Title)
	switch b.Kind {
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	case "reasoning":
		return "thinking"
	case "tool":
		return strings.ToLower(oneLinePreview(b.Title, 72))
	case "error":
		return "error"
	case "status":
		return strings.ToLower(oneLinePreview(b.Title, 72))
	case "audit":
		return strings.ToLower(oneLinePreview(b.Title, 72))
	default:
		return label
	}
}

func appendIfMissing(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

func toolName(value any) string {
	return toolrender.CallName(value)
}

func auditToolName(value any) string {
	fields := mapFromAny(value)
	if name := stringField(fields, "name"); name != "" {
		return name
	}
	return "tool"
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

func collapsedPreview(text string, maxLines, maxChars int) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return "[collapsed: empty]"
	}
	lines := strings.Split(trimmed, "\n")
	limited := lines
	if len(limited) > maxLines {
		limited = limited[:maxLines]
	}
	preview := strings.Join(limited, "\n")
	preview = truncateRunes(preview, maxChars)
	more := len(lines) > len(limited) || len(trimmed) > len(preview)
	if more {
		preview += "\n..."
	}
	return fmt.Sprintf("[collapsed: %d chars, Ctrl+E expand]\n%s", len(text), preview)
}

func collapsedSummary(text string) string {
	return fmt.Sprintf("[collapsed: %d chars, Ctrl+E expand]", len(text))
}

func oneLinePreview(text string, maxChars int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}

func truncateRunes(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars])
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func shortModel(model string) string {
	model = strings.TrimPrefix(model, "deepseek-")
	model = strings.TrimPrefix(model, "deepseek/")
	model = strings.TrimPrefix(model, "gpt-")
	if strings.HasPrefix(model, "v4-") {
		return model
	}
	return truncateRunes(model, 18)
}

func isCodexModel(model string) bool {
	return modelinfo.IsCodexModel(model)
}

func padRight(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-lipgloss.Width(text))
}

func fitSegments(width int, sep string, segments ...string) string {
	var clean []string
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			clean = append(clean, segment)
		}
	}
	if len(clean) == 0 || width <= 0 {
		return ""
	}
	for keep := len(clean); keep > 0; keep-- {
		candidate := strings.Join(clean[:keep], sep)
		if keep < len(clean) {
			candidate += sep + "..."
		}
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return truncateRunes(clean[0], width)
}

func renderStatusSegments(width int, segments []statusSegment, separator lipgloss.Style) string {
	var clean []statusSegment
	for _, segment := range segments {
		segment.text = strings.TrimSpace(segment.text)
		if segment.text != "" {
			clean = append(clean, segment)
		}
	}
	if width <= 0 || len(clean) == 0 {
		return ""
	}
	sep := separator.Render(" · ")
	for keep := len(clean); keep > 0; keep-- {
		rendered := renderStatusParts(clean[:keep], sep)
		if keep < len(clean) {
			rendered += sep + separator.Render("...")
		}
		if lipgloss.Width(rendered) <= width {
			return rendered
		}
	}
	return clean[0].style.Render(truncateRunes(clean[0].text, width))
}

func renderStatusParts(segments []statusSegment, sep string) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		parts = append(parts, segment.style.Render(segment.text))
	}
	return strings.Join(parts, sep)
}

func compactNumber(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%.0fk", float64(value)/1_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func compactDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func compactEventText(value any) string {
	return transcript.CompactEventText(value)
}

func contextThresholdEventText(value any) string {
	return transcript.ContextThresholdEventText(value)
}

func (m Model) selectionViewport() tuiselection.Viewport {
	return tuiselection.Viewport{
		YOffset: m.viewport.YOffset(),
		XOffset: m.viewport.XOffset(),
		Width:   m.viewport.Width(),
		Height:  m.viewport.Height(),
	}
}

func (m Model) mouseInViewport(x, y int) bool {
	return tuiselection.MouseInViewport(m.selectionViewport(), x, y)
}

func (m Model) selectionPointFromMouseClamped(x, y int) tuiselection.Point {
	viewport := m.selectionViewport()
	return tuiselection.PointFromMouseClamped(viewport.YOffset, viewport.XOffset, viewport.Width, viewport.Height, x, y)
}

func (m Model) selectedTranscriptText() string {
	return m.selection.SelectedText(m.baseViewportContent())
}

func (m Model) hasSelection() bool {
	return m.selection.HasSelection()
}

func (m *Model) applySelectionHighlight() {
	m.viewport.SetContent(m.selectionHighlightedContent())
}

func (m Model) baseViewportContent() string {
	if m.viewportContent != "" {
		return m.viewportContent
	}
	return m.viewport.GetContent()
}

func (m Model) selectionHighlightedContent() string {
	content := m.baseViewportContent()
	styles := m.styles()
	return m.selection.HighlightedContent(content, styles.selection)
}

func (m Model) selectionByteRange() (int, int) {
	return m.selection.ByteRange(m.baseViewportContent())
}

func copySelectionCmd(text string) tea.Cmd {
	return func() tea.Msg {
		result := tuiselection.Copy(text, tuiselection.CopyOptions{})
		return clipboardCopiedMsg{chars: result.Chars, method: result.Method, err: result.Err}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
