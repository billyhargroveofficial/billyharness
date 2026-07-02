package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

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
