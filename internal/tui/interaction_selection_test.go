package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	tuiselection "github.com/billyhargroveofficial/billyharness/internal/tui/selection"
)

func TestTranscriptSelectionText(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)
	m.selection.Start = tuiselection.Point{Row: 0, Col: 1}
	m.selection.End = tuiselection.Point{Row: 1, Col: 4}
	got := stripANSITest(m.selectedTranscriptText())
	if !strings.Contains(got, "lpha") || !strings.Contains(got, "bet") {
		t.Fatalf("selected text = %q", got)
	}
	start, end := m.selectionByteRange()
	if start < 0 || end <= start {
		t.Fatalf("selection byte range = %d:%d, want visible highlight range", start, end)
	}
}

func TestTranscriptSelectionClampKeepsRightEdgeExclusive(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(3)
	m.viewport.SetHeight(1)
	m.viewportContent = "abc"
	m.viewport.SetContent("abc")
	m.selection.Start = tuiselection.Point{Row: 0, Col: 0}
	m.selection.End = m.selectionPointFromMouseClamped(999, 0)

	if m.selection.End.Col != 3 {
		t.Fatalf("end col = %d, want exclusive right edge 3", m.selection.End.Col)
	}
	if got := m.selectedTranscriptText(); got != "abc" {
		t.Fatalf("selected text = %q, want abc", got)
	}
}

func TestTranscriptSelectionIsVisiblyHighlighted(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)
	base := m.viewport.View()
	m.selection.Start = tuiselection.Point{Row: 0, Col: 1}
	m.selection.End = tuiselection.Point{Row: 0, Col: 4}
	m.applySelectionHighlight()
	highlighted := m.viewport.View()
	if highlighted == base {
		t.Fatalf("selection should alter rendered viewport")
	}
	if stripANSITest(highlighted) != stripANSITest(base) {
		t.Fatalf("selection highlight should preserve visible text")
	}
	if !strings.Contains(highlighted, "48;2;255;209;102") {
		t.Fatalf("selection highlight should use visible yellow background, rendered=%q", highlighted)
	}
}

func TestTranscriptSelectionHighlightBothThemes(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			m := newTestModel(t)
			m.theme = theme
			m.width = 80
			m.height = 24
			m.addBlock("assistant", "ASSISTANT", "alpha\nbeta")
			m.resize(true)
			firstLine := strings.Split(m.viewport.GetContent(), "\n")[0]
			visible := stripANSITest(firstLine)
			startByte := strings.Index(visible, "alpha")
			if startByte < 0 {
				t.Fatalf("rendered line missing alpha: %q", visible)
			}
			startCol := xansi.StringWidth(visible[:startByte])
			m.selection.Start = tuiselection.Point{Row: 0, Col: startCol}
			m.selection.End = tuiselection.Point{Row: 0, Col: startCol + len("alpha")}

			highlighted := m.selectionHighlightedContent()
			if got := selectionBackgroundText(highlighted); got != "alpha" {
				t.Fatalf("highlighted selection = %q, want alpha; rendered=%q", got, highlighted)
			}
			if !strings.Contains(highlighted, "48;2;255;209;102") {
				t.Fatalf("theme %s should use visible yellow selection, rendered=%q", theme, highlighted)
			}
		})
	}
}

func TestTranscriptSelectionCopiesRenderedTableCellWithoutMarkdownDecorations(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "| Parameter | Value |\n| --- | --- |\n| **Temperature** | `+19 C` |\n")
	m.resize(true)

	lines := strings.Split(m.viewport.GetContent(), "\n")
	targetRow := -1
	targetCol := 0
	for row, line := range lines {
		visible := stripANSITest(line)
		startByte := strings.Index(visible, "Temperature")
		if startByte < 0 {
			continue
		}
		targetRow = row
		targetCol = xansi.StringWidth(visible[:startByte])
		break
	}
	if targetRow < 0 {
		t.Fatalf("rendered table missing target cell: %q", stripANSITest(m.viewport.GetContent()))
	}
	m.selection.Start = tuiselection.Point{Row: targetRow, Col: targetCol}
	m.selection.End = tuiselection.Point{Row: targetRow, Col: targetCol + len("Temperature")}

	if got := m.selectedTranscriptText(); got != "Temperature" {
		t.Fatalf("selected table cell = %q, want Temperature", got)
	}
	highlighted := m.selectionHighlightedContent()
	if got := selectionBackgroundText(highlighted); got != "Temperature" {
		t.Fatalf("highlighted table cell = %q, want Temperature; rendered=%q", got, highlighted)
	}
	if strings.Contains(m.selectedTranscriptText(), "**") {
		t.Fatalf("selected table cell should not include markdown decorations")
	}
}

func TestTranscriptSelectionCannotCopyHiddenThinkingOrTools(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "visible answer")
	m.addBlock("reasoning", "THINKING", "secret reasoning")
	m.addBlock("tool", "TOOL", "secret tool output")
	m.addBlock("assistant", "ASSISTANT", "visible tail")
	m.thinkView = "hidden"
	m.toolView = "hidden"
	m.reflow(true)

	m.selection.Start = tuiselection.Point{Row: 0, Col: 0}
	m.selection.End = tuiselection.Point{Row: 99, Col: 999}
	selected := m.selectedTranscriptText()
	for _, want := range []string{"visible answer", "visible tail"} {
		if !strings.Contains(selected, want) {
			t.Fatalf("selection should include visible text %q, got %q", want, selected)
		}
	}
	for _, hidden := range []string{"secret reasoning", "secret tool output"} {
		if strings.Contains(selected, hidden) {
			t.Fatalf("selection leaked hidden content %q in %q", hidden, selected)
		}
		if strings.Contains(stripANSITest(m.selectionHighlightedContent()), hidden) {
			t.Fatalf("highlight leaked hidden content %q", hidden)
		}
	}
}

func TestTranscriptSelectionSkipsSemanticNoSelectRows(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha")
	m.addBlock("status", "STATUS", "\x1b[90mstatus line should not copy\x1b[m")
	m.addBlock("reasoning", "THINKING", "secret thought")
	m.addBlock("tool", "Done Read README.md", "tool output should not copy when collapsed")
	m.addBlock("assistant", "ASSISTANT", "привет 🏳️‍🌈 中 omega")
	m.thinkView = "collapsed"
	m.toolView = "collapsed"
	m.reflow(true)

	m.selection.Start = tuiselection.Point{Row: 0, Col: 0}
	m.selection.End = tuiselection.Point{Row: 999, Col: 999}
	selected := m.selectedTranscriptText()
	for _, want := range []string{"alpha", "привет 🏳️‍🌈 中 omega"} {
		if !strings.Contains(selected, want) {
			t.Fatalf("selection should include selectable text %q, got %q", want, selected)
		}
	}
	for _, bad := range []string{"status line should not copy", "STATUS", "secret thought", "THINKING", "collapsed", "Done Read", "tool output should not copy"} {
		if strings.Contains(selected, bad) {
			t.Fatalf("selection copied no-select text %q from %q", bad, selected)
		}
	}

	highlighted := m.selectionHighlightedContent()
	if stripANSITest(highlighted) != stripANSITest(m.viewportContent) {
		t.Fatalf("highlight should preserve visible transcript")
	}
	highlightedText := selectionBackgroundText(highlighted)
	for _, want := range []string{"alpha", "привет 🏳️‍🌈 中 omega"} {
		if !strings.Contains(highlightedText, want) {
			t.Fatalf("highlight missing selectable text %q, highlighted=%q", want, highlightedText)
		}
	}
	for _, bad := range []string{"status line should not copy", "Done Read", "collapsed", "secret thought"} {
		if strings.Contains(highlightedText, bad) {
			t.Fatalf("highlight included no-select text %q: %q", bad, highlightedText)
		}
	}
}

func TestReflowPreservesSelectionHighlightDuringLiveUpdate(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)
	target := "bet"
	m.selection.Start = tuiselection.Point{Row: 1, Col: 1}
	m.selection.End = tuiselection.Point{Row: 1, Col: 1 + len(target)}
	m.applySelectionHighlight()

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "\ndelta"})
	m.reflow(false)
	highlighted := m.viewport.GetContent()
	if got := selectionBackgroundText(highlighted); got != target {
		t.Fatalf("highlighted selection after live update = %q, want %q; rendered=%q", got, target, highlighted)
	}
}

func TestTranscriptSelectionHighlightsCorrectStyledLine(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)

	base := m.viewport.GetContent()
	baseLines := strings.Split(base, "\n")
	betaRow := -1
	betaCol := 0
	for row, line := range baseLines {
		visible := stripANSITest(line)
		idx := strings.Index(visible, "beta")
		if idx >= 0 {
			betaRow = row
			betaCol = len([]rune(visible[:idx]))
			break
		}
	}
	if betaRow < 0 {
		t.Fatalf("rendered transcript should contain beta, content=%q", stripANSITest(base))
	}

	m.selection.Start = tuiselection.Point{Row: betaRow, Col: betaCol}
	m.selection.End = tuiselection.Point{Row: betaRow, Col: betaCol + 3}
	m.applySelectionHighlight()

	highlighted := m.viewport.GetContent()
	if stripANSITest(highlighted) != stripANSITest(base) {
		t.Fatalf("selection highlight should preserve visible text")
	}
	highlightedLines := strings.Split(highlighted, "\n")
	if !strings.Contains(highlightedLines[betaRow], "48;2;255;209;102") {
		t.Fatalf("beta line should be highlighted, line=%q", highlightedLines[betaRow])
	}
	for row, line := range highlightedLines {
		if row != betaRow && strings.Contains(stripANSITest(line), "alpha") && strings.Contains(line, "48;2;255;209;102") {
			t.Fatalf("selection highlight landed on wrong line %d: %q", row, line)
		}
	}
}

func TestTranscriptSelectionHighlightMatchesSelectedGraphemes(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 10
	line := "\x1b[90mmeta\x1b[m привет 🏳️‍🌈 中 done"
	target := "привет 🏳️‍🌈 中"
	visible := xansi.Strip(line)
	startByte := strings.Index(visible, target)
	if startByte < 0 {
		t.Fatalf("test target missing from %q", visible)
	}
	startCol := xansi.StringWidth(visible[:startByte])
	endCol := startCol + xansi.StringWidth(target)
	m.viewportContent = line
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(m.height)
	m.viewport.SetContent(line)
	m.selection.Start = tuiselection.Point{Row: 0, Col: startCol}
	m.selection.End = tuiselection.Point{Row: 0, Col: endCol}

	if got := m.selectedTranscriptText(); got != target {
		t.Fatalf("selected text = %q, want %q", got, target)
	}
	highlighted := m.selectionHighlightedContent()
	if got := selectionBackgroundText(highlighted); got != target {
		t.Fatalf("highlighted selection = %q, want %q; rendered=%q", got, target, highlighted)
	}
}
