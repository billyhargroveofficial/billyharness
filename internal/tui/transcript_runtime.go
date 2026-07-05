package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	tuirender "github.com/billyhargroveofficial/billyharness/internal/tui/render"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func (m *Model) saveSettings() error {
	if m.settingsPath == "" {
		return nil
	}
	m.settings.Theme = m.theme
	m.settings.ToolView = m.toolView
	m.settings.ThinkView = m.thinkView
	m.settings.TranscriptMode = m.transcriptMode
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
	return transcript.EncodeCells(filterRoutineRunSummaryBlocks(blocks))
}

func decodeBlocks(blocks []savedBlock) []transcript.Cell {
	return filterRoutineRunSummaryBlocks(transcript.DecodeCells(blocks))
}

func filterRoutineRunSummaryBlocks(blocks []transcript.Cell) []transcript.Cell {
	var out []transcript.Cell
	for _, block := range blocks {
		if isRoutineRunSummaryBlock(block) {
			continue
		}
		out = append(out, block)
	}
	if len(out) == len(blocks) {
		return blocks
	}
	return out
}

func isRoutineRunSummaryBlock(block transcript.Cell) bool {
	if block.CellType != cellTypeRunSummary {
		return false
	}
	if block.EventType == protocol.EventRunFailed {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(block.Title + "\n" + block.Content))
	return !strings.Contains(text, "run failed") && !strings.Contains(text, "error:")
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

func (m *Model) syncLastAssistantMessage(messages []protocol.Message) bool {
	content := lastAssistantMessageContent(messages)
	if content == "" {
		return false
	}
	lastUser := lastBlockIndexByKind(m.blocks, "user")
	lastAssistant := lastBlockIndexByKind(m.blocks, "assistant")
	if lastAssistant > lastUser {
		if strings.TrimSpace(m.blocks[lastAssistant].Content) == content {
			return false
		}
		current := strings.TrimSpace(m.blocks[lastAssistant].Content)
		if current != "" && strings.HasPrefix(content, current) {
			m.blocks[lastAssistant].Content = content
			m.blocks[lastAssistant].RawCopy = content
			m.blocks[lastAssistant].Live = false
			m.blocks[lastAssistant].Updated = time.Now().UTC()
			m.refreshBlockDerivedFields(lastAssistant)
			m.markTranscriptProjectorStale()
			return true
		}
	}
	b := m.newBlock("assistant", "ASSISTANT", content)
	b.CellType = cellTypeAssistantFinal
	b.Live = false
	refreshBlockDerivedFields(&b)
	m.blocks = append(m.blocks, b)
	m.selected = len(m.blocks) - 1
	m.markTranscriptProjectorStale()
	return true
}

func lastAssistantMessageContent(messages []protocol.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != protocol.RoleAssistant {
			continue
		}
		if content := strings.TrimSpace(messages[i].Content); content != "" {
			return content
		}
	}
	return ""
}

func lastBlockIndexByKind(blocks []transcript.Cell, kind string) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind == kind {
			return i
		}
	}
	return -1
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
	maxRounds := "unlimited"
	if m.maxRounds > 0 {
		maxRounds = fmt.Sprintf("%d", m.maxRounds)
	}
	return fmt.Sprintf(
		"mode: %s\nchat: %s\nprovider: %s\nselected model: %s\nactive runtime model: %s\nprofile: %s\naccess mode: %s\nreasoning: %s / %s\nthinking blocks: %s\ntool blocks: %s\ntranscript: %s\ntheme: %s\ngateway: %s\ngateway session: %s\nlocal settings: %s\ntools: %s, max rounds %s\ncalls: model %d, tools %d\ntokens: input %d, output %d\ncontext: %s\ncost: %s\nfollow output: %s",
		mode,
		m.localChatID,
		m.currentProvider(),
		m.currentModel(),
		m.activeRuntimeModelText(),
		m.currentProfile(),
		m.currentAccessMode(),
		m.currentThinking().kind,
		m.currentThinking().effort,
		thinkingDisplay,
		m.toolView,
		m.transcriptMode,
		m.theme,
		gateway,
		session,
		m.settingsPath,
		toolsMode,
		maxRounds,
		m.modelCalls,
		m.toolCalls,
		m.inputTok,
		m.outputTok,
		m.contextText(),
		m.costText(),
		follow,
	)
}

func debugMode(gatewayURL string) string {
	if strings.TrimSpace(gatewayURL) == "" {
		return "local"
	}
	return "gateway"
}

func debugText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func debugBlockKindCounts(blocks []transcript.Cell) string {
	counts := map[string]int{}
	for _, block := range blocks {
		counts[debugText(block.Kind)]++
	}
	return debugCounts(counts)
}

func debugBlockCellCounts(blocks []transcript.Cell) string {
	counts := map[string]int{}
	for _, block := range blocks {
		counts[debugText(string(block.CellType))]++
	}
	return debugCounts(counts)
}

func debugCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
}

func (m Model) activeRuntimeModelText() string {
	if strings.TrimSpace(m.activeRuntimeModel) == "" {
		return "unknown"
	}
	return strings.TrimSpace(m.activeRuntimeModel)
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
	m.runStartHelperIn = m.helperModelInTok
	m.runStartHelperOut = m.helperModelOutTok
	m.runStartHelperHit = m.helperModelCacheHit
	m.runStartHelperMiss = m.helperModelCacheMiss
	m.runStartHelperAPI = m.helperModelAPITok
	m.runStartHelperModelCalls = m.helperModelCalls
	m.runStartHelperCalls = m.helperAPICalls
	m.runStartHelperCost = m.helperCostUSD
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
	m.helperModelInTok = m.runStartHelperIn + snapshot.HelperModelInputTokens
	m.helperModelOutTok = m.runStartHelperOut + snapshot.HelperModelOutputTokens
	m.helperModelCacheHit = m.runStartHelperHit + snapshot.HelperModelCacheHitTokens
	m.helperModelCacheMiss = m.runStartHelperMiss + snapshot.HelperModelCacheMissTokens
	m.helperModelAPITok = m.runStartHelperAPI + snapshot.HelperModelAPITokens
	m.helperModelCalls = m.runStartHelperModelCalls + snapshot.HelperModelCalls
	m.helperAPICalls = m.runStartHelperCalls + snapshot.HelperAPICalls
	m.helperCostUSD = m.runStartHelperCost + snapshot.HelperCostUSD
	return snapshot
}

func (m *Model) applyProjectedTranscript(event protocol.Event) {
	projector := m.ensureTranscriptProjector()
	previous := append([]transcript.Cell(nil), m.blocks...)
	selected := m.selected
	m.blocks = refreshedTranscriptCells(projector.Apply(event))
	if transcriptStructureChanged(previous, m.blocks) {
		m.clearTranscriptSelection()
	}
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

func transcriptStructureChanged(before, after []transcript.Cell) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		if before[i].Kind != after[i].Kind ||
			before[i].CallID != after[i].CallID ||
			before[i].StepID != after[i].StepID ||
			before[i].TurnID != after[i].TurnID {
			return true
		}
	}
	return false
}

func transcriptProjectsEvent(eventType protocol.EventType) bool {
	return uxprojector.EventPresentationPolicy(eventType).Transcript
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
	case protocol.EventSessionStatus:
		m.applySessionStatus(event.Data)
	case protocol.EventRunStarted:
		m.status = "run started"
		if m.runStartedAt.IsZero() {
			m.runStartedAt = time.Now()
		}
		m.removeRoutineRunSummaryBlocks()
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
		m.collapseToolBlockIfLarge(protocol.EventCallID(event))
		m.upsertContextToolGroup(m.turnIDForToolEvent(event))
	case protocol.EventStepStarted, protocol.EventStepCompleted:
		m.applyStepStatus(event)
	case protocol.EventContextCompacted:
		m.status = "context compacted"
	case protocol.EventContextThreshold:
		m.status = "context threshold crossed"
	case protocol.EventStreamStillRunning:
		m.status = stillRunningStatus(event.Data)
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
		m.removeRoutineRunSummaryBlocks()
	case protocol.EventRunFailed:
		m.pendingUserInput = nil
		m.upsertRunSummaryBlock(event.Type, "failed", fmt.Sprint(event.Data))
		m.addEventBlock(event.Type, "ERROR", fmt.Sprint(event.Data))
		m.status = "failed"
	}
}

func (m *Model) applySessionStatus(value any) {
	var status gatewayapi.SessionStatus
	bytes, _ := json.Marshal(value)
	if err := json.Unmarshal(bytes, &status); err != nil {
		return
	}
	if model := strings.TrimSpace(status.Model); model != "" {
		m.activeRuntimeModel = model
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
	return uxprojector.EventPresentationPolicy(event.Type).FlushesStreamQueue()
}

func stillRunningStatus(value any) string {
	var event protocol.StreamStillRunningEvent
	bytes, _ := json.Marshal(value)
	_ = json.Unmarshal(bytes, &event)
	phase := strings.TrimSpace(event.Phase)
	if phase == "" {
		phase = "run"
	}
	var parts []string
	parts = append(parts, "still running", phase)
	if event.IdleMS > 0 {
		parts = append(parts, "idle "+compactDuration(time.Duration(event.IdleMS)*time.Millisecond))
	}
	if event.ElapsedMS > 0 {
		parts = append(parts, "elapsed "+compactDuration(time.Duration(event.ElapsedMS)*time.Millisecond))
	}
	return strings.Join(parts, " · ")
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

func (m *Model) removeRoutineRunSummaryBlocks() bool {
	selectedID := ""
	if m.selected >= 0 && m.selected < len(m.blocks) {
		selectedID = m.blocks[m.selected].ID
	}
	filtered := filterRoutineRunSummaryBlocks(m.blocks)
	if len(filtered) == len(m.blocks) {
		return false
	}
	m.clearTranscriptSelection()
	m.blocks = filtered
	if selectedID != "" {
		for i, block := range m.blocks {
			if block.ID == selectedID {
				m.selected = i
				m.markTranscriptProjectorStale()
				return true
			}
		}
	}
	m.selected = min(max(0, m.selected), max(0, len(m.blocks)-1))
	m.markTranscriptProjectorStale()
	return true
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
		ContextWindowTokens: m.runtime.ContextWindowTokens,
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
	case protocol.StepStatusCompletedWithErrors:
		m.status = "tool batch completed with errors"
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
	if i, ok := m.toolBlockIndex(protocol.EventCallID(event)); ok {
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
	case "skill_list", "skill_read", "skill_view", "skill_import":
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

func (m *Model) collapseToolBlockIfLarge(callID string) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	i, ok := m.toolBlockIndex(callID)
	if !ok {
		return
	}
	if len(m.blocks[i].Content) > 8000 || strings.Count(m.blocks[i].Content, "\n") > 40 {
		m.setBlockCollapsed(i, true)
	}
}

func (m *Model) addBlock(kind, title, content string) {
	m.clearTranscriptSelection()
	b := m.newBlock(kind, title, content)
	m.blocks = append(m.blocks, b)
	m.selected = len(m.blocks) - 1
	m.markTranscriptProjectorStale()
}

func (m *Model) addEventBlock(eventType protocol.EventType, title, content string) {
	m.clearTranscriptSelection()
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
	items, signature := m.reflowVisibleItems()
	parts, selectableLines, ok := m.reflowFastPath(items, signature)
	if !ok {
		parts, selectableLines = m.reflowFull(items)
	}
	m.reflowSignature = signature
	m.reflowVisibleKeys = reflowItemKeys(items)
	m.reflowParts = parts
	m.lastReflowBlockCount = len(m.blocks)
	m.viewportContent = strings.Join(parts, "\n")
	m.viewportSelectableLines = selectableLines
	m.viewport.SetContent(m.viewportContent)
	m.reapplyFindQuery()
	if m.hasSelection() {
		m.applySelectionHighlight()
	}
	if gotoBottom {
		m.viewport.GotoBottom()
	}
}

type reflowItem struct {
	index      int
	key        string
	selectable bool
}

func (m *Model) reflowVisibleItems() ([]reflowItem, string) {
	currentToolTurnID := ""
	if m.toolView == "current" {
		currentToolTurnID = m.currentToolTurnID()
	}
	signature := strings.Join([]string{
		m.toolView,
		m.thinkView,
		m.transcriptMode,
		currentToolTurnID,
	}, "\x00")
	items := make([]reflowItem, 0, len(m.blocks))
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
		items = append(items, reflowItem{
			index:      i,
			key:        tuirender.RichCacheKey(m.richTerminalCacheKeyInput(i, b)),
			selectable: m.blockSelectableForCopy(i, b),
		})
	}
	return items, signature
}

func (m *Model) reflowFastPath(items []reflowItem, signature string) ([]string, []bool, bool) {
	if signature != m.reflowSignature || len(m.reflowParts) == 0 || len(m.reflowVisibleKeys) == 0 {
		return nil, nil, false
	}
	if len(items) == len(m.reflowVisibleKeys) {
		diff := -1
		for i, item := range items {
			if item.key == m.reflowVisibleKeys[i] {
				continue
			}
			if diff >= 0 {
				return nil, nil, false
			}
			diff = i
		}
		if diff < 0 {
			return m.reflowParts, m.viewportSelectableLines, true
		}
		if diff != len(items)-1 {
			return nil, nil, false
		}
		parts := append([]string(nil), m.reflowParts...)
		rendered, cache := m.renderBlockCached(items[diff].index)
		m.setRichBlockCache(m.blocks[items[diff].index], cache)
		parts[diff] = rendered
		return parts, selectableLinesForReflowParts(parts, items), true
	}
	if len(items) == len(m.reflowVisibleKeys)+1 && len(m.blocks) == m.lastReflowBlockCount+1 {
		for i, key := range m.reflowVisibleKeys {
			if items[i].key != key {
				return nil, nil, false
			}
		}
		parts := append([]string(nil), m.reflowParts...)
		last := items[len(items)-1]
		rendered, cache := m.renderBlockCached(last.index)
		m.setRichBlockCache(m.blocks[last.index], cache)
		parts = append(parts, rendered)
		lines := append([]bool(nil), m.viewportSelectableLines...)
		lines = appendSelectableLines(lines, rendered, last.selectable)
		return parts, lines, true
	}
	return nil, nil, false
}

func (m *Model) reflowFull(items []reflowItem) ([]string, []bool) {
	parts := make([]string, 0, len(items))
	var selectableLines []bool
	for _, item := range items {
		rendered, cache := m.renderBlockCached(item.index)
		m.setRichBlockCache(m.blocks[item.index], cache)
		parts = append(parts, rendered)
		selectableLines = appendSelectableLines(selectableLines, rendered, item.selectable)
	}
	return parts, selectableLines
}

func reflowItemKeys(items []reflowItem) []string {
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = item.key
	}
	return keys
}

func selectableLinesForReflowParts(parts []string, items []reflowItem) []bool {
	var lines []bool
	for i, part := range parts {
		selectable := i < len(items) && items[i].selectable
		lines = appendSelectableLines(lines, part, selectable)
	}
	return lines
}

func appendSelectableLines(lines []bool, rendered string, selectable bool) []bool {
	count := strings.Count(rendered, "\n") + 1
	for i := 0; i < count; i++ {
		lines = append(lines, selectable)
	}
	return lines
}

func (m Model) blockSelectableForCopy(i int, b transcript.Cell) bool {
	switch {
	case b.Kind == "status" || b.Kind == "audit":
		return false
	case b.CellType == cellTypeStatus || b.CellType == cellTypeRunSummary || b.CellType == cellTypeCompaction || b.CellType == cellTypeMCPStatus:
		return false
	case b.Kind == "tool" && (m.toolCollapsed(i) || (m.toolView == "auto" && m.blockCollapsed(i)) || b.CellType == cellTypeToolGroup):
		return false
	case b.Kind == "reasoning" && (m.thinkView == "collapsed" || m.blockCollapsed(i)):
		return false
	default:
		return true
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
	width := max(20, m.width-style.GetHorizontalFrameSize())
	if m.transcriptMode == transcript.ExportModeRaw {
		raw := strings.TrimSpace(b.RawCopy)
		if raw == "" {
			raw = strings.TrimSpace(b.Content)
		}
		if raw == "" {
			raw = strings.TrimSpace(b.Title)
		}
		return style.Width(width).Render(raw)
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
	if b.Kind == "assistant" {
		body = tuirender.RenderAssistantMarkdown(body, width, styles.markdown, b.Live)
	}
	if b.Kind == "user" {
		return renderDialogueBlock("❯ you", body, width, style, styles.statusAccess)
	}
	if b.Kind == "assistant" {
		return renderDialogueBlock("● assistant", body, width, style, styles.statusModel)
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

func renderDialogueBlock(header, body string, width int, bodyStyle, headerStyle lipgloss.Style) string {
	header = strings.TrimSpace(header)
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return headerStyle.Render(header)
	}
	return headerStyle.Render(header) + "\n" + bodyStyle.Width(width).Render(body)
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
