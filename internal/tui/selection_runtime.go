package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
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

func (m *Model) handleExportCommand(value string) (bool, tea.Cmd) {
	opts, err := parseExportCommand(value)
	if err != nil {
		m.status = "export failed: " + err.Error()
		return false, nil
	}
	text := strings.TrimSpace(m.transcriptExportArtifact(opts, time.Now().UTC()))
	if text == "" {
		m.status = "export empty"
		return false, nil
	}
	if opts.Path == "" {
		m.addInfoBlock("EXPORT "+strings.ToUpper(opts.Mode), text)
		m.status = "export " + opts.Mode + " " + opts.Source + " shown"
		return true, nil
	}
	if err := os.WriteFile(opts.Path, []byte(text+"\n"), 0o600); err != nil {
		m.status = fmt.Sprintf("export failed: %v", err)
		return false, nil
	}
	m.status = "exported " + opts.Mode + " " + opts.Source + " transcript to " + opts.Path
	return true, nil
}

type exportCommandOptions struct {
	Mode   string
	Source string
	Path   string
}

func parseExportCommand(value string) (exportCommandOptions, error) {
	opts := exportCommandOptions{
		Mode:   transcript.ExportModeRich,
		Source: clientux.TranscriptExportSourceCells,
	}
	args, err := splitExportArgs(value)
	if err != nil {
		return opts, err
	}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if key, val, ok := strings.Cut(arg, "="); ok {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "mode":
				mode := transcript.NormalizeExportMode(val)
				if mode == "" {
					return opts, fmt.Errorf("unknown export mode %q", val)
				}
				opts.Mode = mode
				continue
			case "source":
				source := clientux.NormalizeTranscriptExportSource(val)
				if source == "" {
					return opts, fmt.Errorf("unknown export source %q", val)
				}
				opts.Source = source
				continue
			case "path":
				opts.Path = strings.TrimSpace(val)
				continue
			}
		}
		if mode := transcript.NormalizeExportMode(arg); mode != "" {
			opts.Mode = mode
			continue
		}
		if source := clientux.NormalizeTranscriptExportSource(arg); source != "" {
			opts.Source = source
			continue
		}
		opts.Path = strings.Join(args[i:], " ")
		break
	}
	return opts, nil
}

func splitExportArgs(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var args []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		args = append(args, b.String())
		b.Reset()
	}
	for _, r := range value {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted path")
	}
	flush()
	return args, nil
}

func (m Model) transcriptExportArtifact(opts exportCommandOptions, exportedAt time.Time) string {
	body, warnings := m.transcriptExportBody(opts)
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	meta := clientux.TranscriptExportMetadata{
		SourceStore:         m.transcriptExportSourceStore(opts.Source),
		SourceMode:          opts.Source,
		TranscriptMode:      opts.Mode,
		RuntimeMode:         debugMode(m.gatewayURL),
		LocalChatID:         m.localChatID,
		GatewaySessionID:    m.sessionID,
		LastGatewayEventSeq: m.lastGatewayEventSeq,
		SeqRange: clientux.TranscriptExportSeqRange{
			LastKnown: m.lastGatewayEventSeq > 0,
			Last:      m.lastGatewayEventSeq,
		},
		Provider:        m.currentProvider(),
		Model:           m.currentModel(),
		Profile:         m.currentProfile(),
		AccessMode:      m.currentAccessMode(),
		ReasoningKind:   m.currentThinking().kind,
		ReasoningEffort: m.currentThinking().effort,
		ExportedAt:      exportedAt,
		RedactionMode:   "none_body_unredacted",
		Warnings:        warnings,
		Extra: map[string]string{
			"body_bytes":    strconv.Itoa(len(body)),
			"body_sha256":   clientux.DebugHash(body),
			"block_count":   strconv.Itoa(len(m.blocks)),
			"message_count": strconv.Itoa(len(m.messages)),
		},
	}
	return clientux.FormatTranscriptExportArtifact(meta, body, m.gatewayURL, m.settingsPath, m.sessionsDir)
}

func (m Model) transcriptExportBody(opts exportCommandOptions) (string, []string) {
	warnings := m.transcriptExportWarnings(opts.Source)
	switch opts.Source {
	case clientux.TranscriptExportSourceMessages:
		return transcript.FormatMessages(m.messages, opts.Mode), warnings
	case clientux.TranscriptExportSourceEvents:
		warnings = append(warnings, "events_projection: TUI exports projected transcript cells because durable JSONL events are gateway-owned")
		return transcript.FormatCells(m.blocks, opts.Mode), warnings
	case clientux.TranscriptExportSourceCombined:
		warnings = append(warnings, "combined_projection: TUI combines messages with projected transcript cells, not raw durable JSONL events")
		return joinTranscriptExportSections(
			transcript.FormatMessages(m.messages, opts.Mode),
			transcript.FormatCells(m.blocks, opts.Mode),
			opts.Mode,
		), warnings
	default:
		return transcript.FormatCells(m.blocks, opts.Mode), warnings
	}
}

func (m Model) transcriptExportWarnings(source string) []string {
	warnings := []string{"body_unredacted: transcript body may contain user, provider, or tool secrets"}
	switch source {
	case clientux.TranscriptExportSourceCells, clientux.TranscriptExportSourceEvents, clientux.TranscriptExportSourceCombined:
		warnings = append(warnings, "legacy_snapshot: source is current TUI projection, not a durable gateway event replay")
	}
	if m.gatewayURL != "" && m.sessionID == "" {
		warnings = append(warnings, "missing_gateway_session: gateway mode has no active session id")
	}
	if m.transcriptStale {
		warnings = append(warnings, "stale_diagnostics_index: transcript projector is marked stale")
	}
	if m.uxProjector == nil {
		warnings = append(warnings, "projector_missing: client UX projector is unavailable")
		return warnings
	}
	snapshot := m.uxProjector.Snapshot()
	if snapshot.SeqGap != nil {
		warnings = append(warnings, fmt.Sprintf("partial_replay: projector sequence gap after=%d got=%d", snapshot.SeqGap.AfterSeq, snapshot.SeqGap.GotSeq))
	}
	if m.lastGatewayEventSeq > 0 && snapshot.LastSeq > 0 && snapshot.LastSeq != m.lastGatewayEventSeq {
		warnings = append(warnings, fmt.Sprintf("projector_mismatch: projector_last_seq=%d gateway_last_seq=%d", snapshot.LastSeq, m.lastGatewayEventSeq))
	}
	return warnings
}

func (m Model) transcriptExportSourceStore(source string) string {
	switch source {
	case clientux.TranscriptExportSourceMessages:
		return "tui.client_state.messages"
	case clientux.TranscriptExportSourceEvents:
		return "tui.client_state.projected_events"
	case clientux.TranscriptExportSourceCombined:
		return "tui.client_state.messages+projected_events"
	default:
		return "tui.client_state.cells"
	}
}

func joinTranscriptExportSections(messagesText, eventText, mode string) string {
	messagesText = strings.TrimSpace(messagesText)
	eventText = strings.TrimSpace(eventText)
	switch {
	case messagesText == "":
		return eventText
	case eventText == "":
		return messagesText
	case mode == transcript.ExportModeRich:
		return messagesText + "\n\nPROJECTED EVENTS\n" + eventText
	default:
		return messagesText + "\n\n" + eventText
	}
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
		text := transcript.FormatCells(m.blocks, transcript.ExportModeRaw)
		return text, "raw transcript", strings.TrimSpace(text) != ""
	case "transcript-rich", "rich-transcript", "rich":
		text := transcript.FormatCells(m.blocks, transcript.ExportModeRich)
		return text, "rich transcript", strings.TrimSpace(text) != ""
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
	return m.selection.SelectedTextWithLineFilter(m.baseViewportContent(), m.viewportSelectableLines)
}

func (m Model) hasSelection() bool {
	return m.selection.HasSelection()
}

func (m *Model) clearTranscriptSelection() {
	if !m.selection.HasSelection() && !m.selection.Selecting {
		return
	}
	m.selection = tuiselection.Controller{}
	if m.viewportContent != "" {
		m.viewport.SetContent(m.viewportContent)
	}
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
	return m.selection.HighlightedContentWithLineFilter(content, styles.selection, m.viewportSelectableLines)
}

func (m Model) selectionByteRange() (int, int) {
	return m.selection.ByteRangeWithLineFilter(m.baseViewportContent(), m.viewportSelectableLines)
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
