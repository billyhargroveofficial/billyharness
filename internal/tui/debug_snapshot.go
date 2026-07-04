package tui

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func (m Model) debugSnapshot() clientux.TUIDebugSnapshot {
	rawExport := transcript.FormatCells(m.blocks, transcript.ExportModeRaw)
	richExport := transcript.FormatCells(m.blocks, transcript.ExportModeRich)
	selectedText := m.selectedTranscriptText()
	byteStart, byteEnd := m.selectionByteRange()
	selectedBytes := 0
	if byteStart >= 0 && byteEnd >= byteStart {
		selectedBytes = byteEnd - byteStart
	}
	projectorSnapshot := uxprojector.Snapshot{}
	projectorAvailable := m.uxProjector != nil
	if projectorAvailable {
		projectorSnapshot = m.uxProjector.Snapshot()
	}
	snapshot := clientux.TUIDebugSnapshot{
		SchemaVersion: clientux.TUIDebugSnapshotSchemaVersion,
		Session: clientux.TUIDebugSession{
			LocalChatID:         m.localChatID,
			LocalTitleBytes:     len(m.chatTitle),
			LocalTitleHash:      clientux.DebugHash(m.chatTitle),
			GatewaySessionID:    m.sessionID,
			GatewayURL:          m.gatewayURL,
			LastGatewayEventSeq: m.lastGatewayEventSeq,
			SettingsPath:        m.settingsPath,
			SessionsDir:         m.sessionsDir,
		},
		Runtime: clientux.TUIDebugRuntime{
			Mode:            debugMode(m.gatewayURL),
			Provider:        m.currentProvider(),
			SelectedModel:   m.currentModel(),
			ActiveModel:     m.activeRuntimeModelText(),
			Profile:         m.currentProfile(),
			AccessMode:      m.currentAccessMode(),
			ReasoningKind:   m.currentThinking().kind,
			ReasoningEffort: m.currentThinking().effort,
			Busy:            m.busy,
			Status:          m.status,
			Error:           m.err,
		},
		Stream: clientux.TUIDebugStream{
			PendingEvents:    len(m.pendingStreamEvents),
			BatchScheduled:   m.streamBatchScheduled,
			ChannelBuffered:  len(m.events),
			ChannelCapacity:  cap(m.events),
			PendingUserInput: m.pendingUserInput != nil,
		},
		Projector: clientux.TUIDebugProjector{
			Available:         projectorAvailable,
			RunState:          string(projectorSnapshot.RunState),
			LastSeq:           projectorSnapshot.LastSeq,
			ModelCalls:        projectorSnapshot.ModelCalls,
			ToolCalls:         projectorSnapshot.ToolCalls,
			TrackedTools:      len(projectorSnapshot.ToolsByCallID),
			ContextThresholds: len(projectorSnapshot.ContextThresholds),
			TurnChanges:       len(projectorSnapshot.TurnChanges),
			AssistantBytes:    len(projectorSnapshot.AssistantText),
			ReasoningBytes:    len(projectorSnapshot.ReasoningText),
			ErrorCount:        len(projectorSnapshot.Errors),
			LastError:         projectorSnapshot.LastError,
		},
		Viewport: clientux.TUIDebugViewport{
			AppWidth:     m.width,
			AppHeight:    m.height,
			Width:        m.viewport.Width(),
			Height:       m.viewport.Height(),
			XOffset:      m.viewport.XOffset(),
			YOffset:      m.viewport.YOffset(),
			TotalLines:   m.viewport.TotalLineCount(),
			VisibleLines: m.viewport.VisibleLineCount(),
			AtBottom:     m.viewport.AtBottom(),
			FollowOutput: m.followOutput,
			ReflowCount:  m.reflowCount,
		},
		Selection: clientux.TUIDebugSelection{
			Active:        m.hasSelection(),
			Selecting:     m.selection.Selecting,
			Start:         clientux.TUIDebugPoint{Row: m.selection.Start.Row, Col: m.selection.Start.Col},
			End:           clientux.TUIDebugPoint{Row: m.selection.End.Row, Col: m.selection.End.Col},
			ByteStart:     byteStart,
			ByteEnd:       byteEnd,
			SelectedBytes: selectedBytes,
			SelectedRunes: len([]rune(selectedText)),
			SelectedHash:  clientux.DebugHash(selectedText),
		},
		Transcript: clientux.TUIDebugTranscript{
			Blocks:              len(m.blocks),
			SelectedIndex:       m.selected,
			ViewportBytes:       len(m.baseViewportContent()),
			ViewportHash:        clientux.DebugHash(m.baseViewportContent()),
			SelectableLineCount: selectableLineCount(m.viewportSelectableLines),
			CacheEntries:        len(m.richRenderCache),
			CollapsedEntries:    len(m.collapsed),
			Stale:               m.transcriptStale,
		},
		Export: clientux.TUIDebugExport{
			Mode:      m.transcriptMode,
			Target:    "info_block_or_path_argument",
			RawBytes:  len(rawExport),
			RawHash:   clientux.DebugHash(rawExport),
			RichBytes: len(richExport),
			RichHash:  clientux.DebugHash(richExport),
		},
		Stale: clientux.TUIDebugStale{
			TranscriptProjector: m.transcriptStale,
			FileSearchActive:    m.fileMentionSearching,
			FileSearchError:     strings.TrimSpace(m.fileMentionErr) != "",
			SlashDismissed:      strings.TrimSpace(m.slashDismissed) != "",
		},
		BlockKinds: debugBlockKindCountMap(m.blocks),
		CellTypes:  debugBlockCellCountMap(m.blocks),
	}
	if projectorSnapshot.SeqGap != nil {
		snapshot.Projector.SeqGapAfter = projectorSnapshot.SeqGap.AfterSeq
		snapshot.Projector.SeqGapGot = projectorSnapshot.SeqGap.GotSeq
	}
	if len(projectorSnapshot.Errors) > 0 && snapshot.Projector.LastError == "" {
		snapshot.Projector.LastError = projectorSnapshot.Errors[len(projectorSnapshot.Errors)-1]
	}
	if m.selected >= 0 && m.selected < len(m.blocks) {
		block := m.blocks[m.selected]
		snapshot.Transcript.SelectedCellID = block.ID
		snapshot.Transcript.SelectedKind = block.Kind
		snapshot.Transcript.SelectedCellType = string(block.CellType)
		snapshot.Transcript.SelectedCallID = block.CallID
		snapshot.Transcript.SelectedStepID = block.StepID
		snapshot.Transcript.SelectedToolName = block.ToolName
	}
	snapshot.Hints = m.debugSnapshotHints(snapshot)
	return clientux.RedactTUIDebugSnapshot(snapshot, m.gatewayURL, m.settingsPath, m.sessionsDir)
}

func (m Model) debugFullText() string {
	return clientux.FormatTUIDebugSnapshot(m.debugSnapshot())
}

func (m Model) debugSnapshotHints(snapshot clientux.TUIDebugSnapshot) []string {
	var hints []string
	if snapshot.Runtime.Mode == "gateway" && strings.TrimSpace(snapshot.Session.GatewaySessionID) == "" {
		hints = append(hints, "gateway mode has no active gateway session id")
	}
	if !snapshot.Projector.Available {
		hints = append(hints, "client UX projector is missing")
	}
	if m.transcriptProjector == nil {
		hints = append(hints, "transcript projector is missing")
	}
	if snapshot.Transcript.Blocks > 0 && snapshot.Transcript.ViewportBytes == 0 {
		hints = append(hints, "transcript blocks exist but viewport content is empty")
	}
	if snapshot.Stream.PendingEvents > 0 && !snapshot.Stream.BatchScheduled {
		hints = append(hints, "stream events are queued without a scheduled batch")
	}
	if snapshot.Selection.Active && snapshot.Selection.ByteStart < 0 {
		hints = append(hints, "selection is active but byte range is unavailable")
	}
	if snapshot.Stale.TranscriptProjector {
		hints = append(hints, "transcript projector is stale")
	}
	if snapshot.Stale.FileSearchError {
		hints = append(hints, "file mention search has an error")
	}
	return hints
}

func selectableLineCount(lines []bool) int {
	count := 0
	for _, ok := range lines {
		if ok {
			count++
		}
	}
	return count
}

func debugBlockKindCountMap(blocks []transcript.Cell) map[string]int {
	counts := map[string]int{}
	for _, block := range blocks {
		counts[debugText(block.Kind)]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func debugBlockCellCountMap(blocks []transcript.Cell) map[string]int {
	counts := map[string]int{}
	for _, block := range blocks {
		counts[debugText(string(block.CellType))]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}
