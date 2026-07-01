package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	sessionDiagnosticsIndexFileName = "diagnostics.json"
	sessionIndexTextPreviewBytes    = 4096
	sessionIndexArgsPreviewBytes    = 2048
	sessionIndexErrorPreviewBytes   = 2048
)

type StoredSessionDiagnosticsIndex struct {
	SchemaVersion int                     `json:"schema_version"`
	BuiltAt       time.Time               `json:"built_at"`
	Dir           string                  `json:"dir"`
	SessionCount  int                     `json:"session_count"`
	TextRowCount  int                     `json:"text_row_count"`
	ToolRowCount  int                     `json:"tool_row_count"`
	ErrorRowCount int                     `json:"error_row_count"`
	RunRowCount   int                     `json:"run_row_count"`
	UsageRowCount int                     `json:"usage_row_count"`
	TextRows      []StoredSessionTextRow  `json:"text_rows,omitempty"`
	ToolRows      []StoredSessionToolRow  `json:"tool_rows,omitempty"`
	ErrorRows     []StoredSessionErrorRow `json:"error_rows,omitempty"`
	RunRows       []StoredSessionRunRow   `json:"run_rows,omitempty"`
	UsageRows     []StoredSessionUsageRow `json:"usage_rows,omitempty"`
	Warnings      []string                `json:"warnings,omitempty"`
}

type StoredSessionTextRow struct {
	SessionID    string `json:"session_id"`
	MessageIndex int    `json:"message_index"`
	Role         string `json:"role"`
	Text         string `json:"text"`
	TextBytes    int    `json:"text_bytes"`
	Truncated    bool   `json:"truncated,omitempty"`
}

type StoredSessionToolRow struct {
	SessionID     string `json:"session_id"`
	Seq           int64  `json:"seq,omitempty"`
	TS            string `json:"ts,omitempty"`
	EventType     string `json:"event_type,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	TurnID        string `json:"turn_id,omitempty"`
	StepID        string `json:"step_id,omitempty"`
	CallID        string `json:"call_id,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	Name          string `json:"name,omitempty"`
	Status        string `json:"status,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	Error         string `json:"error,omitempty"`
	OutputRef     string `json:"output_ref,omitempty"`
	OutputRefID   string `json:"output_ref_id,omitempty"`
	ArgsPreview   string `json:"args_preview,omitempty"`
	ArgsTruncated bool   `json:"args_truncated,omitempty"`
}

type StoredSessionErrorRow struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq,omitempty"`
	TS        string `json:"ts,omitempty"`
	EventType string `json:"event_type,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	StepID    string `json:"step_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	Error     string `json:"error"`
	Truncated bool   `json:"truncated,omitempty"`
}

type StoredSessionRunRow struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	StartSeq  int64  `json:"start_seq,omitempty"`
	EndSeq    int64  `json:"end_seq,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

type StoredSessionUsageRow struct {
	SessionID       string `json:"session_id"`
	RunID           string `json:"run_id"`
	StartSeq        int64  `json:"start_seq,omitempty"`
	EndSeq          int64  `json:"end_seq,omitempty"`
	Status          string `json:"status,omitempty"`
	InputTokens     int64  `json:"input_tokens,omitempty"`
	OutputTokens    int64  `json:"output_tokens,omitempty"`
	CacheHitTokens  int64  `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens int64  `json:"cache_miss_tokens,omitempty"`
	ReasoningTokens int64  `json:"reasoning_tokens,omitempty"`
	ModelCalls      int    `json:"model_calls,omitempty"`
	ToolCalls       int    `json:"tool_calls,omitempty"`
}

func RebuildStoredSessionDiagnosticsIndex(dir string) (StoredSessionDiagnosticsIndex, error) {
	dir = cleanSessionStoreDir(dir)
	list, err := ListStoredSessions(dir)
	if err != nil {
		return StoredSessionDiagnosticsIndex{}, err
	}
	index := StoredSessionDiagnosticsIndex{
		SchemaVersion: gatewaySessionSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		Dir:           list.Dir,
		SessionCount:  len(list.Sessions),
		Warnings:      append([]string(nil), list.Warnings...),
	}
	for _, summary := range list.Sessions {
		rows, err := buildStoredSessionDiagnosticsRows(dir, summary)
		if err != nil {
			index.Warnings = append(index.Warnings, fmt.Sprintf("%s: %v", summary.ID, err))
			continue
		}
		index.TextRows = append(index.TextRows, rows.TextRows...)
		index.ToolRows = append(index.ToolRows, rows.ToolRows...)
		index.ErrorRows = append(index.ErrorRows, rows.ErrorRows...)
		index.RunRows = append(index.RunRows, rows.RunRows...)
		index.UsageRows = append(index.UsageRows, rows.UsageRows...)
		index.Warnings = append(index.Warnings, rows.Warnings...)
	}
	sortStoredSessionDiagnosticsRows(&index)
	index.TextRowCount = len(index.TextRows)
	index.ToolRowCount = len(index.ToolRows)
	index.ErrorRowCount = len(index.ErrorRows)
	index.RunRowCount = len(index.RunRows)
	index.UsageRowCount = len(index.UsageRows)
	if err := writeStoredSessionDiagnosticsIndex(diagnosticsIndexPath(dir), index); err != nil {
		return StoredSessionDiagnosticsIndex{}, err
	}
	return index, nil
}

func ReadStoredSessionDiagnosticsIndex(dir string) (StoredSessionDiagnosticsIndex, error) {
	dir = cleanSessionStoreDir(dir)
	body, err := os.ReadFile(diagnosticsIndexPath(dir))
	if err != nil {
		return StoredSessionDiagnosticsIndex{}, err
	}
	var index StoredSessionDiagnosticsIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return StoredSessionDiagnosticsIndex{}, err
	}
	if index.SchemaVersion != 0 && index.SchemaVersion != gatewaySessionSchemaVersion {
		return StoredSessionDiagnosticsIndex{}, errors.New("unsupported session diagnostics index schema_version")
	}
	return index, nil
}

func DeleteStoredSessionDiagnosticsIndex(dir string) error {
	dir = cleanSessionStoreDir(dir)
	err := os.Remove(diagnosticsIndexPath(dir))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type storedSessionDiagnosticsRows struct {
	TextRows  []StoredSessionTextRow
	ToolRows  []StoredSessionToolRow
	ErrorRows []StoredSessionErrorRow
	RunRows   []StoredSessionRunRow
	UsageRows []StoredSessionUsageRow
	Warnings  []string
}

func buildStoredSessionDiagnosticsRows(dir string, summary StoredSessionSummary) (storedSessionDiagnosticsRows, error) {
	sessionID := strings.TrimSpace(summary.ID)
	if sessionID == "" {
		return storedSessionDiagnosticsRows{}, fmt.Errorf("missing session id")
	}
	if summary.Legacy {
		record, err := readLegacySessionRecord(filepath.Join(dir, sessionID+".json"))
		if err != nil {
			return storedSessionDiagnosticsRows{}, err
		}
		return storedSessionDiagnosticsRows{
			TextRows: diagnosticsTextRows(sessionID, record.Messages),
			Warnings: []string{
				fmt.Sprintf("%s: legacy snapshot has no event JSONL diagnostics", sessionID),
			},
		}, nil
	}
	sessionDir := filepath.Join(dir, sessionID)
	manifest, err := readSessionManifest(filepath.Join(sessionDir, sessionManifestName))
	if err != nil {
		return storedSessionDiagnosticsRows{}, err
	}
	historyPath := filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName))
	history, err := replaySessionHistory(historyPath, sessionID)
	if err != nil {
		return storedSessionDiagnosticsRows{}, err
	}
	rows := storedSessionDiagnosticsRows{
		TextRows: diagnosticsTextRows(sessionID, history.messages),
	}
	eventsPath := filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	events, err := replaySessionEventsAfter(eventsPath, sessionID, 0)
	if err != nil {
		rows.Warnings = append(rows.Warnings, fmt.Sprintf("%s: events replay failed: %v", sessionID, err))
		return rows, nil
	}
	eventRows := diagnosticsRowsFromEvents(sessionID, events)
	rows.ToolRows = eventRows.ToolRows
	rows.ErrorRows = eventRows.ErrorRows
	rows.RunRows = eventRows.RunRows
	rows.UsageRows = eventRows.UsageRows
	return rows, nil
}

func readLegacySessionRecord(path string) (storedSession, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return storedSession{}, err
	}
	var record storedSession
	if err := json.Unmarshal(bytes, &record); err != nil {
		return storedSession{}, err
	}
	return record, nil
}

func diagnosticsTextRows(sessionID string, messages []protocol.Message) []StoredSessionTextRow {
	rows := make([]StoredSessionTextRow, 0, len(messages))
	for i, message := range messages {
		switch message.Role {
		case protocol.RoleUser, protocol.RoleAssistant:
		default:
			continue
		}
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		preview, truncated := capStringBytes(text, sessionIndexTextPreviewBytes)
		rows = append(rows, StoredSessionTextRow{
			SessionID:    sessionID,
			MessageIndex: i + 1,
			Role:         string(message.Role),
			Text:         preview,
			TextBytes:    len([]byte(text)),
			Truncated:    truncated,
		})
	}
	return rows
}

func diagnosticsRowsFromEvents(sessionID string, events []protocol.Event) storedSessionDiagnosticsRows {
	builder := sessionDiagnosticsBuilder{
		sessionID: sessionID,
		runs:      map[string]*StoredSessionRunRow{},
		usage:     map[string]*sessionIndexUsageState{},
	}
	for _, event := range events {
		builder.observe(event)
	}
	return builder.finish()
}

type sessionDiagnosticsBuilder struct {
	sessionID  string
	toolRows   []StoredSessionToolRow
	errorRows  []StoredSessionErrorRow
	runRows    []StoredSessionRunRow
	usageRows  []StoredSessionUsageRow
	runs       map[string]*StoredSessionRunRow
	runOrder   []string
	usage      map[string]*sessionIndexUsageState
	usageOrder []string
}

func (b *sessionDiagnosticsBuilder) observe(event protocol.Event) {
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	b.observeRun(event)
	b.observeUsage(event)
	if row, ok := toolRowFromEvent(b.sessionID, event); ok {
		b.toolRows = append(b.toolRows, row)
	}
	if row, ok := errorRowFromEvent(b.sessionID, event); ok {
		b.errorRows = append(b.errorRows, row)
	}
}

func (b *sessionDiagnosticsBuilder) observeRun(event protocol.Event) {
	runID := strings.TrimSpace(event.RunID)
	if runID == "" {
		return
	}
	switch event.Type {
	case protocol.EventRunStarted:
		row := b.runFor(runID)
		row.StartSeq = firstInt64(row.StartSeq, event.Seq)
		row.StartedAt = firstNonEmpty(row.StartedAt, event.TS)
		row.Status = "running"
	case protocol.EventRunCompleted:
		row := b.runFor(runID)
		row.EndSeq = firstInt64(event.Seq, row.EndSeq)
		row.EndedAt = firstNonEmpty(event.TS, row.EndedAt)
		row.Status = "completed"
	case protocol.EventRunFailed:
		row := b.runFor(runID)
		row.EndSeq = firstInt64(event.Seq, row.EndSeq)
		row.EndedAt = firstNonEmpty(event.TS, row.EndedAt)
		row.Status = "failed"
		row.Error, _ = capStringBytes(eventErrorText(event), sessionIndexErrorPreviewBytes)
	}
}

func (b *sessionDiagnosticsBuilder) observeUsage(event protocol.Event) {
	runID := strings.TrimSpace(event.RunID)
	if runID == "" {
		return
	}
	state := b.usageFor(runID)
	state.StartSeq = firstInt64(state.StartSeq, event.Seq)
	state.EndSeq = firstInt64(event.Seq, state.EndSeq)
	switch event.Type {
	case protocol.EventRunStarted:
		state.Status = "running"
		state.StartSeq = firstInt64(event.Seq, state.StartSeq)
	case protocol.EventModelCallStarted:
		state.ModelCalls++
		state.accumulator.Reset()
	case protocol.EventToolCallRequested:
		state.ToolCalls++
	case protocol.EventProviderUsageUpdate:
		delta := state.accumulator.Apply(parseSessionIndexUsage(event.Data))
		state.InputTokens += delta.InputTokens
		state.OutputTokens += delta.OutputTokens
		state.CacheHitTokens += delta.CacheHitTokens
		state.CacheMissTokens += delta.CacheMissTokens
		state.ReasoningTokens += delta.ReasoningTokens
	case protocol.EventRunCompleted:
		state.Status = "completed"
	case protocol.EventRunFailed:
		state.Status = "failed"
	}
}

func (b *sessionDiagnosticsBuilder) finish() storedSessionDiagnosticsRows {
	for _, runID := range b.runOrder {
		row := *b.runs[runID]
		if row.Status == "" {
			row.Status = "unknown"
		}
		b.runRows = append(b.runRows, row)
	}
	for _, runID := range b.usageOrder {
		state := b.usage[runID]
		if state.empty() {
			continue
		}
		status := strings.TrimSpace(state.Status)
		if status == "" {
			status = "running"
		}
		b.usageRows = append(b.usageRows, StoredSessionUsageRow{
			SessionID:       b.sessionID,
			RunID:           runID,
			StartSeq:        state.StartSeq,
			EndSeq:          state.EndSeq,
			Status:          status,
			InputTokens:     state.InputTokens,
			OutputTokens:    state.OutputTokens,
			CacheHitTokens:  state.CacheHitTokens,
			CacheMissTokens: state.CacheMissTokens,
			ReasoningTokens: state.ReasoningTokens,
			ModelCalls:      state.ModelCalls,
			ToolCalls:       state.ToolCalls,
		})
	}
	return storedSessionDiagnosticsRows{
		ToolRows:  b.toolRows,
		ErrorRows: b.errorRows,
		RunRows:   b.runRows,
		UsageRows: b.usageRows,
	}
}

func (b *sessionDiagnosticsBuilder) runFor(runID string) *StoredSessionRunRow {
	if row := b.runs[runID]; row != nil {
		return row
	}
	row := &StoredSessionRunRow{
		SessionID: b.sessionID,
		RunID:     runID,
		Status:    "running",
	}
	b.runs[runID] = row
	b.runOrder = append(b.runOrder, runID)
	return row
}

func (b *sessionDiagnosticsBuilder) usageFor(runID string) *sessionIndexUsageState {
	if state := b.usage[runID]; state != nil {
		return state
	}
	state := &sessionIndexUsageState{}
	b.usage[runID] = state
	b.usageOrder = append(b.usageOrder, runID)
	return state
}

func toolRowFromEvent(sessionID string, event protocol.Event) (StoredSessionToolRow, bool) {
	row := StoredSessionToolRow{
		SessionID:  sessionID,
		Seq:        event.Seq,
		TS:         event.TS,
		EventType:  string(event.Type),
		RunID:      event.RunID,
		TurnID:     event.TurnID,
		StepID:     event.StepID,
		CallID:     event.CallID,
		AttemptID:  event.AttemptID,
		DurationMS: event.DurationMS,
	}
	switch event.Type {
	case protocol.EventToolCallRequested:
		call, ok := decodeSessionIndexData[protocol.ToolCall](event.Data)
		if !ok {
			return row, false
		}
		row.Name = call.Name
		row.Status = "requested"
		row.ArgsPreview, row.ArgsTruncated = capStringBytes(string(call.Arguments), sessionIndexArgsPreviewBytes)
	case protocol.EventToolPermissionRequested:
		permission, _ := decodeSessionIndexData[protocol.ToolPermissionEvent](event.Data)
		row.Name = permission.Name
		row.Status = "permission_requested"
	case protocol.EventToolPermissionDecided:
		permission, _ := decodeSessionIndexData[protocol.ToolPermissionEvent](event.Data)
		row.Name = permission.Name
		row.Status = firstNonEmpty(permission.Decision, "permission_decided")
	case protocol.EventToolAudit:
		row.Name = eventDataName(event.Data)
		row.Status = "audit"
	case protocol.EventToolCallStarted:
		row.Name = eventDataName(event.Data)
		row.Status = "started"
	case protocol.EventToolCallProgress:
		progress, _ := decodeSessionIndexData[protocol.ToolProgressEvent](event.Data)
		row.Name = progress.Name
		row.Status = firstNonEmpty(progress.Status, progress.Phase, "progress")
	case protocol.EventToolOutputRefCreated:
		ref, ok := decodeSessionIndexData[protocol.ToolOutputRefEvent](event.Data)
		if !ok {
			return row, false
		}
		row.Name = ref.Name
		row.Status = "output_ref_created"
		row.OutputRef = ref.OutputRef
		row.OutputRefID = ref.OutputRefID
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		result, ok := decodeSessionIndexData[protocol.ToolResult](event.Data)
		if !ok {
			return row, false
		}
		row.Name = result.Name
		row.Status = statusForToolResult(event.Type, result)
		row.OutputRef = result.OutputRef
		row.OutputRefID = mapString(result.Metadata, "output_ref_id")
		if result.IsError || event.Type != protocol.EventToolCallFinished {
			row.Error, _ = capStringBytes(firstNonEmpty(result.ErrorCode, result.Content), sessionIndexErrorPreviewBytes)
		}
	default:
		return StoredSessionToolRow{}, false
	}
	row.Name = firstNonEmpty(row.Name, eventDataName(event.Data))
	row.CallID = firstNonEmpty(row.CallID, event.CallID)
	row.AttemptID = firstNonEmpty(row.AttemptID, event.AttemptID)
	return row, true
}

func errorRowFromEvent(sessionID string, event protocol.Event) (StoredSessionErrorRow, bool) {
	status := ""
	name := eventDataName(event.Data)
	errorText := eventErrorText(event)
	switch event.Type {
	case protocol.EventRunFailed:
		status = "failed"
	case protocol.EventTurnCompleted:
		turn, ok := decodeSessionIndexData[protocol.TurnEvent](event.Data)
		if !ok || turn.Status != protocol.TurnStatusFailed {
			return StoredSessionErrorRow{}, false
		}
		status = turn.Status
		name = firstNonEmpty(name, turn.Model)
	case protocol.EventStepCompleted:
		step, ok := decodeSessionIndexData[protocol.StepEvent](event.Data)
		if !ok || step.Status != protocol.StepStatusFailed {
			return StoredSessionErrorRow{}, false
		}
		status = step.Status
		name = firstNonEmpty(name, step.Name)
	case protocol.EventModelCallFinished:
		model, ok := decodeSessionIndexData[protocol.ModelCallEvent](event.Data)
		if !ok || (strings.TrimSpace(model.Error) == "" && !isErrorStatus(model.Status)) {
			return StoredSessionErrorRow{}, false
		}
		status = firstNonEmpty(model.Status, "failed")
		name = firstNonEmpty(name, model.ModelID, model.ProviderID)
	case protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		result, _ := decodeSessionIndexData[protocol.ToolResult](event.Data)
		status = statusForToolResult(event.Type, result)
		name = firstNonEmpty(name, result.Name)
	case protocol.EventToolCallFinished:
		result, ok := decodeSessionIndexData[protocol.ToolResult](event.Data)
		if !ok || !result.IsError {
			return StoredSessionErrorRow{}, false
		}
		status = statusForToolResult(event.Type, result)
		name = firstNonEmpty(name, result.Name)
	case protocol.EventHookFailed:
		hook, _ := decodeSessionIndexData[protocol.HookEvent](event.Data)
		status = firstNonEmpty(hook.Status, "failed")
		name = firstNonEmpty(hook.Name, hook.HookName, hook.HookEvent)
	default:
		return StoredSessionErrorRow{}, false
	}
	if strings.TrimSpace(errorText) == "" {
		errorText = "error"
	}
	errorPreview, truncated := capStringBytes(errorText, sessionIndexErrorPreviewBytes)
	return StoredSessionErrorRow{
		SessionID: sessionID,
		Seq:       event.Seq,
		TS:        event.TS,
		EventType: string(event.Type),
		RunID:     event.RunID,
		TurnID:    event.TurnID,
		StepID:    event.StepID,
		CallID:    event.CallID,
		AttemptID: event.AttemptID,
		Name:      name,
		Status:    status,
		Error:     errorPreview,
		Truncated: truncated,
	}, true
}

func eventErrorText(event protocol.Event) string {
	switch event.Type {
	case protocol.EventRunFailed:
		return stringFromAny(event.Data)
	case protocol.EventTurnCompleted:
		turn, _ := decodeSessionIndexData[protocol.TurnEvent](event.Data)
		return firstNonEmpty(turn.Error, mapString(turn.Metadata, "error"), mapString(turn.Metadata, "reason"))
	case protocol.EventStepCompleted:
		step, _ := decodeSessionIndexData[protocol.StepEvent](event.Data)
		return firstNonEmpty(step.Error, mapString(step.Metadata, "error"), mapString(step.Metadata, "reason"))
	case protocol.EventModelCallFinished:
		model, _ := decodeSessionIndexData[protocol.ModelCallEvent](event.Data)
		return model.Error
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		result, _ := decodeSessionIndexData[protocol.ToolResult](event.Data)
		return firstNonEmpty(result.ErrorCode, result.Content)
	case protocol.EventHookFailed:
		hook, _ := decodeSessionIndexData[protocol.HookEvent](event.Data)
		return firstNonEmpty(hook.Error, hook.Stderr, hook.Stdout)
	default:
		return stringFromAny(event.Data)
	}
}

func eventDataName(data any) string {
	switch value := data.(type) {
	case string:
		return strings.TrimSpace(value)
	}
	if call, ok := decodeSessionIndexData[protocol.ToolCall](data); ok {
		return strings.TrimSpace(call.Name)
	}
	if result, ok := decodeSessionIndexData[protocol.ToolResult](data); ok {
		return strings.TrimSpace(result.Name)
	}
	if progress, ok := decodeSessionIndexData[protocol.ToolProgressEvent](data); ok {
		return strings.TrimSpace(progress.Name)
	}
	if ref, ok := decodeSessionIndexData[protocol.ToolOutputRefEvent](data); ok {
		return strings.TrimSpace(ref.Name)
	}
	if m, ok := decodeSessionIndexMap(data); ok {
		return firstNonEmpty(mapString(m, "name"), mapString(m, "tool_name"), mapString(m, "model"), mapString(m, "model_id"), mapString(m, "provider_id"))
	}
	return ""
}

func statusForToolResult(eventType protocol.EventType, result protocol.ToolResult) string {
	switch eventType {
	case protocol.EventToolCallFailed:
		return "failed"
	case protocol.EventToolCallAborted:
		return "aborted"
	default:
		if result.IsError {
			return "failed"
		}
		return "finished"
	}
}

func isErrorStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "errored", "aborted":
		return true
	default:
		return false
	}
}

type sessionIndexUsage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64
}

type sessionIndexUsageAccumulator struct {
	last    sessionIndexUsage
	hasLast bool
}

type sessionIndexUsageState struct {
	StartSeq        int64
	EndSeq          int64
	Status          string
	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64
	ModelCalls      int
	ToolCalls       int
	accumulator     sessionIndexUsageAccumulator
}

func (s sessionIndexUsageState) empty() bool {
	return s.InputTokens == 0 &&
		s.OutputTokens == 0 &&
		s.CacheHitTokens == 0 &&
		s.CacheMissTokens == 0 &&
		s.ReasoningTokens == 0 &&
		s.ModelCalls == 0 &&
		s.ToolCalls == 0
}

func parseSessionIndexUsage(data any) sessionIndexUsage {
	var usage struct {
		InputTokens     int64 `json:"input_tokens"`
		OutputTokens    int64 `json:"output_tokens"`
		CacheHitTokens  int64 `json:"cache_hit_tokens"`
		CacheMissTokens int64 `json:"cache_miss_tokens"`
		ReasoningTokens int64 `json:"reasoning_tokens"`
	}
	bytes, _ := json.Marshal(data)
	_ = json.Unmarshal(bytes, &usage)
	return sessionIndexUsage{
		InputTokens:     nonNegativeInt64(usage.InputTokens),
		OutputTokens:    nonNegativeInt64(usage.OutputTokens),
		CacheHitTokens:  nonNegativeInt64(usage.CacheHitTokens),
		CacheMissTokens: nonNegativeInt64(usage.CacheMissTokens),
		ReasoningTokens: nonNegativeInt64(usage.ReasoningTokens),
	}
}

func (a *sessionIndexUsageAccumulator) Reset() {
	a.last = sessionIndexUsage{}
	a.hasLast = false
}

func (a *sessionIndexUsageAccumulator) Apply(update sessionIndexUsage) sessionIndexUsage {
	if update == (sessionIndexUsage{}) {
		return sessionIndexUsage{}
	}
	if !a.hasLast {
		a.last = update
		a.hasLast = true
		return update
	}
	if update == a.last {
		return sessionIndexUsage{}
	}
	if update.atLeast(a.last) {
		delta := update.minus(a.last)
		a.last = update
		return delta
	}
	a.last = update
	return update
}

func (u sessionIndexUsage) atLeast(other sessionIndexUsage) bool {
	return u.InputTokens >= other.InputTokens &&
		u.OutputTokens >= other.OutputTokens &&
		u.CacheHitTokens >= other.CacheHitTokens &&
		u.CacheMissTokens >= other.CacheMissTokens &&
		u.ReasoningTokens >= other.ReasoningTokens
}

func (u sessionIndexUsage) minus(other sessionIndexUsage) sessionIndexUsage {
	return sessionIndexUsage{
		InputTokens:     u.InputTokens - other.InputTokens,
		OutputTokens:    u.OutputTokens - other.OutputTokens,
		CacheHitTokens:  u.CacheHitTokens - other.CacheHitTokens,
		CacheMissTokens: u.CacheMissTokens - other.CacheMissTokens,
		ReasoningTokens: u.ReasoningTokens - other.ReasoningTokens,
	}
}

func writeStoredSessionDiagnosticsIndex(path string, index StoredSessionDiagnosticsIndex) error {
	if index.SchemaVersion == 0 {
		index.SchemaVersion = gatewaySessionSchemaVersion
	}
	if index.BuiltAt.IsZero() {
		index.BuiltAt = time.Now().UTC()
	}
	index.TextRowCount = len(index.TextRows)
	index.ToolRowCount = len(index.ToolRows)
	index.ErrorRowCount = len(index.ErrorRows)
	index.RunRowCount = len(index.RunRows)
	index.UsageRowCount = len(index.UsageRows)
	return writeJSONIndex(path, index)
}

func diagnosticsIndexPath(dir string) string {
	return filepath.Join(cleanSessionStoreDir(dir), sessionIndexDirName, sessionDiagnosticsIndexFileName)
}

func sortStoredSessionDiagnosticsRows(index *StoredSessionDiagnosticsIndex) {
	sort.Slice(index.TextRows, func(i, j int) bool {
		if index.TextRows[i].SessionID != index.TextRows[j].SessionID {
			return index.TextRows[i].SessionID < index.TextRows[j].SessionID
		}
		return index.TextRows[i].MessageIndex < index.TextRows[j].MessageIndex
	})
	sort.Slice(index.ToolRows, func(i, j int) bool {
		if index.ToolRows[i].SessionID != index.ToolRows[j].SessionID {
			return index.ToolRows[i].SessionID < index.ToolRows[j].SessionID
		}
		return index.ToolRows[i].Seq < index.ToolRows[j].Seq
	})
	sort.Slice(index.ErrorRows, func(i, j int) bool {
		if index.ErrorRows[i].SessionID != index.ErrorRows[j].SessionID {
			return index.ErrorRows[i].SessionID < index.ErrorRows[j].SessionID
		}
		return index.ErrorRows[i].Seq < index.ErrorRows[j].Seq
	})
	sort.Slice(index.RunRows, func(i, j int) bool {
		if index.RunRows[i].SessionID != index.RunRows[j].SessionID {
			return index.RunRows[i].SessionID < index.RunRows[j].SessionID
		}
		return index.RunRows[i].StartSeq < index.RunRows[j].StartSeq
	})
	sort.Slice(index.UsageRows, func(i, j int) bool {
		if index.UsageRows[i].SessionID != index.UsageRows[j].SessionID {
			return index.UsageRows[i].SessionID < index.UsageRows[j].SessionID
		}
		return index.UsageRows[i].StartSeq < index.UsageRows[j].StartSeq
	})
}

func decodeSessionIndexData[T any](data any) (T, bool) {
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

func decodeSessionIndexMap(data any) (map[string]any, bool) {
	if value, ok := data.(map[string]any); ok {
		return value, true
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, false
	}
	return out, true
}

func capStringBytes(value string, maxBytes int) (string, bool) {
	value = strings.ToValidUTF8(value, "")
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value, false
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], true
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	}
	if m, ok := decodeSessionIndexMap(value); ok {
		return firstNonEmpty(
			mapString(m, "error"),
			mapString(m, "message"),
			mapString(m, "reason"),
			mapString(m, "block_reason"),
			mapString(m, "error_code"),
			mapString(m, "status"),
		)
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes))
}

func mapString(m map[string]any, key string) string {
	if len(m) == 0 {
		return ""
	}
	switch value := m[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case json.Number:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
