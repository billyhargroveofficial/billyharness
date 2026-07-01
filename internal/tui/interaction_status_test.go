package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	tuiselection "github.com/billyhargroveofficial/billyharness/internal/tui/selection"
)

func TestAltEnterInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("first")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "first\n" {
		t.Fatalf("textarea value = %q, want first newline", got)
	}
}

func TestPrintableKeysReachTextarea(t *testing.T) {
	m := newTestModel(t)

	next, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/" {
		t.Fatalf("textarea value = %q, want /", got)
	}
}

func TestMouseScrollDisablesFollowOutput(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", strings.Repeat("line\n", 80))
	m.resize(true)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should start at bottom")
	}

	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	updated := next.(Model)
	if updated.followOutput {
		t.Fatalf("mouse wheel up should disable followOutput")
	}
	if updated.viewport.AtBottom() {
		t.Fatalf("mouse wheel up should scroll away from bottom")
	}

	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
	updated = next.(Model)
	if !updated.followOutput {
		t.Fatalf("end key should restore followOutput")
	}
	if !updated.viewport.AtBottom() {
		t.Fatalf("end key should move to bottom")
	}
}

func TestLiveUpdateKeepsViewportAnchoredAtBottom(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 18
	m.addBlock("assistant", "ASSISTANT", strings.Repeat("old line\n", 80))
	m.resize(true)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should start at bottom")
	}
	m.followOutput = true
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Repeat("new line\n", 8)})
	m.reflow(m.followOutput)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should stay anchored at bottom during live update")
	}
}

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

func TestSlashPopupCompletesCommand(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/the")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/theme " {
		t.Fatalf("textarea value = %q, want /theme space", got)
	}
}

func TestCommandPaletteOpensSlashRegistry(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/" {
		t.Fatalf("Ctrl+K textarea = %q, want /", got)
	}
	if updated.status != "command palette" {
		t.Fatalf("status = %q, want command palette", updated.status)
	}
	popup := stripANSITest(updated.slashPopupView())
	for _, want := range []string{"/help", "/status", "/diff"} {
		if !strings.Contains(popup, want) {
			t.Fatalf("command palette missing %q:\n%s", want, popup)
		}
	}

	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated = next.(Model)
	if len(updated.blocks) == 0 || updated.blocks[len(updated.blocks)-1].Title != "HELP" {
		t.Fatalf("Enter on default command should run /help, blocks=%#v", updated.blocks)
	}
}

func TestSlashPopupCompletesArgument(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/theme")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated := next.(Model)
	if got := updated.theme; got != "light" {
		t.Fatalf("theme = %q, want light", got)
	}
	if got := updated.textarea.Value(); got != "" {
		t.Fatalf("textarea value = %q, want cleared after command runs", got)
	}
}

func TestSlashPopupTabCompletesArgumentWithoutRunning(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/theme")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/theme light" {
		t.Fatalf("textarea value = %q, want /theme light", got)
	}
	if got := updated.theme; got != "dark" {
		t.Fatalf("theme should not change on tab, got %q", got)
	}
}

func TestSlashPopupCompletesResumeArgument(t *testing.T) {
	m := newTestModel(t)
	original := m.localChatID
	m.textarea.SetValue("/resume")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	updated := next.(Model)
	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated = next.(Model)

	want := "/resume " + shortID(original)
	if got := updated.textarea.Value(); got != want {
		t.Fatalf("textarea value = %q, want %q", got, want)
	}
}

func TestSlashPopupKeepsSelectedCommandVisiblePastFirstPage(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.textarea.SetValue("/")
	for i, command := range slashCommands() {
		if command.name == "/thinkview" {
			m.slashIndex = i
			break
		}
	}

	view := stripANSITest(m.slashPopupView())
	if !strings.Contains(view, "/thinkview") {
		t.Fatalf("selected command should be visible, popup=%q", view)
	}
	if strings.Contains(view, "/help") {
		t.Fatalf("popup should scroll past first page, popup=%q", view)
	}
	if !strings.Contains(view, "previous matches") {
		t.Fatalf("popup should show previous count, popup=%q", view)
	}
}

func TestSlashArgPopupKeepsSelectedArgumentVisiblePastFirstPage(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.textarea.SetValue("/reasoning ")
	m.slashIndex = 6

	view := stripANSITest(m.slashPopupView())
	if !strings.Contains(view, "toggle") {
		t.Fatalf("selected argument should be visible, popup=%q", view)
	}
	if strings.Contains(view, "xhigh") {
		t.Fatalf("popup should scroll past first argument page, popup=%q", view)
	}
	if !strings.Contains(view, "previous matches") {
		t.Fatalf("popup should show previous count, popup=%q", view)
	}
}

func TestSlashPopupEscDismissesUntilTextChanges(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/the")
	if m.slashPopupView() == "" {
		t.Fatalf("slash popup should render")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	updated := next.(Model)
	if got := updated.slashPopupView(); got != "" {
		t.Fatalf("slash popup should be dismissed, got %q", got)
	}
}

func TestInlineStatusShowsModelAccessCacheCostAndSession(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	m.version = "0.1.0"
	m.dangerous = true
	m.inputTok = 1000
	m.outputTok = 500
	m.cacheHitTok = 700
	m.cacheMissTok = 300
	m.lastInputTok = 1000
	m.lastOutputTok = 500
	m.lastCacheHitTok = 700
	m.lastCacheMissTok = 300
	m.toolSummaryInTok = 20000
	m.toolSummaryOutTok = 900
	m.toolSummaryAPITok = 0

	status := m.inlineStatusView()
	for _, want := range []string{
		"deepseek-v4-flash",
		"🧠 high",
		"Full Access",
		"Context",
		"cost $",
		"cache hit",
		"websum",
		"20k→900",
		"sumapi 0",
		"agent turns",
		"v0.1.0",
		"theme dark",
		"profile billy",
		"Main [",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q does not contain %q", status, want)
		}
	}
	if strings.Count(status, "\n") != 1 {
		t.Fatalf("status should be two lines, got %q", status)
	}
	for _, bad := range []string{"reasoning 0", "1.5k used", "cached "} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not contain raw provider counter %q: %q", bad, status)
		}
	}
}

func TestInlineStatusIsWidthAware(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		m := newTestModel(t)
		m.width = width
		m.version = "0.1.0"
		m.dangerous = true
		m.status = "completed"
		m.modelCalls = 12
		m.toolCalls = 34
		m.lastInputTok = 420000
		m.lastOutputTok = 1200
		m.lastCacheHitTok = 390000
		m.lastCacheMissTok = 30000
		m.toolSummaryInTok = 37000
		m.toolSummaryOutTok = 2500
		status := m.inlineStatusView()
		lines := strings.Split(status, "\n")
		if len(lines) != 2 {
			t.Fatalf("width %d: status should render as two lines, got %q", width, status)
		}
		for _, line := range lines {
			if got := xansi.StringWidth(stripANSITest(line)); got > width {
				t.Fatalf("width %d: status line width=%d exceeds viewport: %q", width, got, line)
			}
		}
		for _, want := range []string{"completed", "deepseek-v4-flash", "Context"} {
			if !strings.Contains(status, want) {
				t.Fatalf("width %d: status missing priority segment %q: %q", width, want, status)
			}
		}
	}
}

func TestStatusCommandShowsDetailedStatusBlock(t *testing.T) {
	m := newTestModel(t)
	handled, cmd := m.handleSlashCommand("/status")
	if !handled || cmd != nil {
		t.Fatalf("/status handled=%v cmd=%v, want handled without async command", handled, cmd)
	}
	if len(m.blocks) != 1 || m.blocks[0].Title != "STATUS" {
		t.Fatalf("/status should add one STATUS block, got %#v", m.blocks)
	}
	for _, want := range []string{"provider:", "model:", "profile:", "context:", "calls:"} {
		if !strings.Contains(m.blocks[0].Content, want) {
			t.Fatalf("/status block missing %q:\n%s", want, m.blocks[0].Content)
		}
	}
}

func TestAccessModeSlashCommandUpdatesRunRequest(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	handled, cmd := m.handleSlashCommand("/mode plan")
	if !handled || cmd != nil {
		t.Fatalf("/mode handled=%v cmd=%v, want handled without async command", handled, cmd)
	}
	if m.currentAccessMode() != config.AccessModePlan || m.toolPolicy.AccessMode != config.AccessModePlan {
		t.Fatalf("access mode=%q tool policy=%q", m.currentAccessMode(), m.toolPolicy.AccessMode)
	}
	if got := m.gatewayRunRequest("inspect").AccessMode; got != config.AccessModePlan {
		t.Fatalf("gateway run access mode = %q", got)
	}
	if status := m.inlineStatusView(); !strings.Contains(status, "Plan") {
		t.Fatalf("status missing Plan: %q", status)
	}
}

func TestCompactEventTextShowsStructuredCompactionFields(t *testing.T) {
	text := compactEventText(map[string]any{
		"compaction_id":               "abc123",
		"reason":                      "prompt_tokens_at_or_above_threshold",
		"trigger_source":              "provider_usage",
		"trigger_prompt_tokens":       610000,
		"threshold_tokens":            600000,
		"before_estimated_tokens":     610000,
		"after_estimated_tokens":      98000,
		"cut_start_index":             4,
		"cut_end_index":               46,
		"replacement_index":           4,
		"keep_messages":               32,
		"max_summary_chars":           120000,
		"summary_strategy":            "model",
		"summary_provider":            "mock",
		"summary_model":               "mock-summary",
		"model_summary_input_tokens":  1234,
		"model_summary_output_tokens": 56,
		"compacted_messages":          42,
		"compacted_chars":             240000,
		"compacted_estimated_tokens":  60000,
		"protected_prefix": map[string]any{
			"messages":         3,
			"chars":            9000,
			"estimated_tokens": 2250,
			"reasons": map[string]any{
				"system_prompt":       1,
				"profile_soul":        1,
				"agents_instructions": 1,
			},
		},
		"active_messages": 35,
		"summary_chars":   12000,
		"top_context_contributors": []map[string]any{{
			"index":            6,
			"role":             "tool",
			"source":           "web_summaries",
			"name":             "web_fetch",
			"estimated_tokens": 42000,
			"preview":          "large web summary",
		}},
	})
	for _, want := range []string{
		"id: abc123",
		"reason: prompt_tokens_at_or_above_threshold (provider_usage)",
		"trigger: 610000 / threshold 600000 tokens",
		"context: before ~610k / after ~98k",
		"cut: [4:46) -> replacement index 4",
		"policy: keep 32 messages / summary cap 120000 chars",
		"summary: model mock/mock-summary",
		"summary usage: in 1.2k / out 56",
		"compacted messages: 42",
		"compacted budget: 240000 chars / ~60000 tokens",
		"protected prefix: 3 messages, 9000 chars, ~2250 tokens",
		"agents_instructions=1",
		"profile_soul=1",
		"system_prompt=1",
		"active messages: 35",
		"summary chars: 12000",
		"top contributors:",
		"#6 tool web_summaries/web_fetch ~42k - large web summary",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact text %q missing %q", text, want)
		}
	}
}

func TestResumeChatDoesNotTreatLifetimeTokensAsContextUsage(t *testing.T) {
	m := newTestModel(t)
	session := chatSession{
		ID:              "20260628-120000-feedfacecafe",
		Title:           "saved chat",
		CreatedAt:       time.Now().UTC().Add(-time.Hour),
		UpdatedAt:       time.Now().UTC(),
		Model:           m.currentModel(),
		ReasoningKind:   m.currentThinking().kind,
		ReasoningEffort: m.currentThinking().effort,
		InputTokens:     900000,
		OutputTokens:    100000,
		CacheHitTokens:  200000,
		CacheMissTokens: 700000,
		ReasoningTokens: 50000,
	}
	if err := saveChatSession(m.sessionsDir, session); err != nil {
		t.Fatal(err)
	}

	if cmd := m.resumeChat(session.ID); cmd != nil {
		t.Fatalf("resumeChat returned unexpected command: %#v", cmd)
	}
	if got := m.inputTok; got != session.InputTokens {
		t.Fatalf("inputTok = %d, want saved lifetime %d", got, session.InputTokens)
	}
	if got := m.outputTok; got != session.OutputTokens {
		t.Fatalf("outputTok = %d, want saved lifetime %d", got, session.OutputTokens)
	}
	if got := m.contextTokens(); got != 0 {
		t.Fatalf("contextTokens = %d, want 0 after resume without live usage snapshot", got)
	}
}

func TestProviderUsageUpdateDeduplicatesCumulativeSnapshots(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	m.applyEvent(protocol.Event{Type: protocol.EventRunStarted})
	m.applyEvent(protocol.Event{Type: protocol.EventModelCallStarted})

	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      100,
		"output_tokens":     20,
		"cache_hit_tokens":  40,
		"cache_miss_tokens": 60,
		"reasoning_tokens":  5,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      100,
		"output_tokens":     20,
		"cache_hit_tokens":  40,
		"cache_miss_tokens": 60,
		"reasoning_tokens":  5,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      125,
		"output_tokens":     25,
		"cache_hit_tokens":  50,
		"cache_miss_tokens": 75,
		"reasoning_tokens":  7,
	}})

	if m.inputTok != 125 || m.outputTok != 25 || m.cacheHitTok != 50 || m.cacheMissTok != 75 || m.reasoningTok != 7 {
		t.Fatalf("usage totals = in:%d out:%d hit:%d miss:%d reasoning:%d",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok, m.reasoningTok)
	}
	if got := m.contextTokens(); got != 150 {
		t.Fatalf("contextTokens = %d, want current snapshot input+output 150", got)
	}
	status := m.inlineStatusView()
	if !strings.Contains(status, "cache hit 50") || !strings.Contains(status, "miss 75") {
		t.Fatalf("status should show last cache snapshot, got %q", status)
	}
	for _, bad := range []string{"cache hit 90", "miss 135", "reasoning 7", "157 used"} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not show cumulative raw counter %q: %q", bad, status)
		}
	}
}

func TestTUIAccountingMatchesClientUXProjector(t *testing.T) {
	events := []protocol.Event{
		{Type: protocol.EventRunStarted},
		{Type: protocol.EventModelCallStarted},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      100,
			"output_tokens":     20,
			"cache_hit_tokens":  40,
			"cache_miss_tokens": 60,
			"reasoning_tokens":  5,
		}},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      125,
			"output_tokens":     25,
			"cache_hit_tokens":  50,
			"cache_miss_tokens": 75,
			"reasoning_tokens":  7,
		}},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-1", Name: "web_fetch"}},
		{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
			CallID:  "call-1",
			Name:    "web_fetch",
			Content: "ok",
			Metadata: map[string]any{
				"tool_summary_input_tokens":      int64(30),
				"tool_summary_output_tokens":     int64(8),
				"tool_summary_api_input_tokens":  int64(40),
				"tool_summary_api_output_tokens": int64(10),
			},
		}},
		{Type: protocol.EventRunCompleted},
	}

	m := newTestModel(t)
	p := uxprojector.New()
	for _, event := range events {
		p.Apply(event)
		m.applyEvent(event)
	}
	snapshot := p.Snapshot()
	if m.modelCalls != snapshot.ModelCalls || m.toolCalls != snapshot.ToolCalls {
		t.Fatalf("counts = model:%d tools:%d, projector model:%d tools:%d",
			m.modelCalls, m.toolCalls, snapshot.ModelCalls, snapshot.ToolCalls)
	}
	if m.inputTok != snapshot.InputTokens || m.outputTok != snapshot.OutputTokens ||
		m.cacheHitTok != snapshot.CacheHitTokens || m.cacheMissTok != snapshot.CacheMissTokens ||
		m.reasoningTok != snapshot.ReasoningTokens {
		t.Fatalf("usage = in:%d out:%d hit:%d miss:%d reasoning:%d, projector=%#v",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok, m.reasoningTok, snapshot)
	}
	if m.contextTokens() != snapshot.LastInputTokens+snapshot.LastOutputTokens {
		t.Fatalf("context tokens = %d, projector last context = %d",
			m.contextTokens(), snapshot.LastInputTokens+snapshot.LastOutputTokens)
	}
	if m.toolSummaryInTok != snapshot.ToolSummaryInputTokens ||
		m.toolSummaryOutTok != snapshot.ToolSummaryOutputTokens ||
		m.toolSummaryAPITok != snapshot.ToolSummaryAPITokens {
		t.Fatalf("tool summary = in:%d out:%d api:%d, projector=%#v",
			m.toolSummaryInTok, m.toolSummaryOutTok, m.toolSummaryAPITok, snapshot)
	}
	if m.status != "completed" || snapshot.RunState != uxprojector.RunStateCompleted {
		t.Fatalf("terminal state = tui:%q projector:%q", m.status, snapshot.RunState)
	}
}

func TestLightThemeStatusLineUsesThemeBackground(t *testing.T) {
	styles := newThemeStyles(tuiThemes["light"])
	rendered := styles.status.Render("status")
	if !strings.Contains(rendered, "48;2;221;232;215") {
		t.Fatalf("light status should use theme status bg, rendered=%q", rendered)
	}
}

func TestFormatMCPStatusShowsOwnConfigAndNativeWebTools(t *testing.T) {
	startedAt := time.Date(2026, 6, 28, 8, 0, 0, 0, time.Local)
	connectedAt := time.Date(2026, 6, 28, 8, 0, 2, 0, time.Local)
	eventAt := time.Date(2026, 6, 28, 8, 0, 3, 0, time.Local)
	lastErrorAt := time.Date(2026, 6, 28, 8, 1, 0, 0, time.Local)
	nextRetryAt := time.Date(2026, 6, 28, 8, 1, 5, 0, time.Local)
	text := formatMCPStatus(mcpStatusResponse{
		ConfigFiles: []string{"/root/billyharness/mcp.config.toml"},
		Allowed:     []string{"telegram", "telegram-parilka", "github", "context7"},
		Enabled:     true,
		Servers: []mcpclient.ServerStatus{{
			Name:            "github",
			Transport:       "stdio",
			Command:         "npx",
			Enabled:         true,
			Connected:       true,
			State:           "reconnected",
			ToolCount:       7,
			PID:             4242,
			StartedAt:       &startedAt,
			LastConnectedAt: &connectedAt,
			LastEventAt:     &eventAt,
			RestartCount:    1,
			RetryCount:      1,
		}, {
			Name:           "context7",
			Transport:      "stdio",
			Command:        "npx",
			Enabled:        true,
			Connected:      false,
			State:          "failed",
			Error:          "MCP context7 transport: EOF",
			LastError:      "MCP context7 transport: EOF",
			LastErrorAt:    &lastErrorAt,
			StderrTail:     "server closed",
			RetryBackoffMS: 5000,
			NextRetryAt:    &nextRetryAt,
		}, {
			Name:              "remote",
			Transport:         "streamable-http",
			URL:               "https://example.com/mcp",
			Enabled:           true,
			State:             "unsupported",
			UnsupportedReason: "streamable HTTP MCP is not implemented in billyharness yet; use stdio MCP or remove the url server",
			Error:             "MCP server remote unsupported: streamable HTTP MCP is not implemented in billyharness yet; use stdio MCP or remove the url server",
		}},
	})
	for _, want := range []string{
		"/root/billyharness/mcp.config.toml",
		"allowed: telegram, telegram-parilka, github, context7",
		"native: web_search, web_fetch, web_extract, web_crawl",
		"github",
		"reconnected",
		"command:npx",
		"tools:7",
		"pid:4242",
		"restarts:1",
		"retries:1",
		"connected_at:08:00:02",
		"event_at:08:00:03",
		"context7",
		"failed",
		"MCP context7 transport: EOF",
		"backoff:5000ms",
		"next_retry:08:01:05",
		"last_error_at: 2026-06-28 08:01:00",
		"stderr: server closed",
		"remote",
		"unsupported",
		"streamable-http",
		"url:https://example.com/mcp",
		"unsupported: streamable HTTP MCP is not implemented",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("mcp status missing %q: %q", want, text)
		}
	}
}

func TestAuthDeepSeekGatewayFlowDoesNotRenderSecret(t *testing.T) {
	var captured map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/deepseek" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deepseek": map[string]any{"configured": true, "path": "/root/billyharness/.env"},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/auth deepseek")
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	if m.authInputProvider != "deepseek" {
		t.Fatalf("authInputProvider = %q", m.authInputProvider)
	}
	m.textarea.SetValue("sk-secret-value")
	next, cmd := m.send()
	updated := next.(Model)
	if updated.textarea.Value() != "" {
		t.Fatalf("textarea should be cleared")
	}
	msg := cmd().(authResultMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if captured["api_key"] != "sk-secret-value" {
		t.Fatalf("captured = %#v", captured)
	}
	if strings.Contains(msg.text, "sk-secret-value") {
		t.Fatalf("auth result leaked secret: %q", msg.text)
	}
}

func TestAuthCodexGatewayImport(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/codex/import" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"codex": map[string]any{"configured": true, "path": "/root/billyharness/auth/codex.json", "account_id": "acct_123"},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/auth codex")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(authResultMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if !called || !strings.Contains(msg.text, "acct_123") {
		t.Fatalf("called=%v text=%q", called, msg.text)
	}
}

func TestConfigCommandShowsSanitizedGatewaySummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/config" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"values": []config.ResolvedValue{
				{Key: "provider", Value: "deepseek", Source: config.SourceGateway, SourceKey: "provider"},
				{Key: "model", Value: "deepseek-v4-flash", Source: config.SourceGateway, SourceKey: "model"},
				{Key: "api_key", Value: "[redacted]", Redacted: true, Source: config.SourceEnvironment, SourceKey: "DEEPSEEK_API_KEY"},
			},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/config")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(configStatusMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	for _, want := range []string{"billyharness config", "provider:", "deepseek", "model:", "deepseek-v4-flash"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("config summary missing %q:\n%s", want, msg.text)
		}
	}
	if strings.Contains(msg.text, "sk-") || strings.Contains(msg.text, "DEEPSEEK_API_KEY=") {
		t.Fatalf("config summary leaked secret-ish content:\n%s", msg.text)
	}
}

func TestContextCommandShowsGatewayContextReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/context" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(gatewayapi.SessionContextResponse{
			ID:                   "session-1",
			MessageCount:         4,
			EstimatedTokens:      580000,
			ContextWindowTokens:  1000000,
			ContextCompactTokens: 600000,
			PercentUsed:          58,
			Estimator:            "chars_div_4",
			Sources: []gatewayapi.ContextSource{
				{Source: "web_summaries", MessageCount: 2, EstimatedTokens: 320000, Percent: 55.2},
				{Source: "user_messages", MessageCount: 1, EstimatedTokens: 1000, Percent: 0.2},
			},
			Thresholds: []gatewayapi.ContextThreshold{
				{Percent: 50, Tokens: 500000, Crossed: true},
				{Percent: 70, Tokens: 700000, RemainingTokens: 120000},
			},
			TopContributors: []gatewayapi.ContextContributor{
				{Index: 2, Role: "tool", Source: "web_summaries", Name: "web_fetch", EstimatedTokens: 320000, Preview: "summary"},
			},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	handled, cmd := m.handleSlashCommand("/context")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(contextStatusMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	for _, want := range []string{"active context: 580.0k / 1.00M", "thresholds: ●50% ○70%", "web_summaries", "top contributors"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("context report missing %q:\n%s", want, msg.text)
		}
	}
}

func TestDiffCommandRequestsGatewayPreview(t *testing.T) {
	var gotReq gatewayapi.SessionUndoRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/undo" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(gatewayapi.SessionUndoResponse{
			ChangeID: "change-1",
			Preview:  true,
			Patch:    "--- before\n+++ after\n@@ -1 +1 @@\n-old\n+new\n",
			Change: protocol.TurnChangeEvent{
				ChangeID:       "change-1",
				FileCount:      1,
				Modified:       1,
				Additions:      1,
				Deletions:      1,
				Reversible:     true,
				PatchOutputRef: "/root/billyharness/tool-output/change-1.json",
				Files: []protocol.TurnChangeFile{
					{RelPath: "README.md", Change: "modified", Additions: 1, Deletions: 1, Reversible: true},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	handled, cmd := m.handleSlashCommand("/diff change-1")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(turnDiffPreviewMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if gotReq.ChangeID != "change-1" || !gotReq.Preview {
		t.Fatalf("undo request = %#v", gotReq)
	}
	for _, want := range []string{"summary: 1 file", "patch_ref: /root/billyharness/tool-output/change-1.json", "preview:", "@@ -1 +1 @@", "+new"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("diff preview missing %q:\n%s", want, msg.text)
		}
	}
}

func TestUndoRedoCommandsRequestGatewayApply(t *testing.T) {
	var undoReq gatewayapi.SessionUndoRequest
	var redoCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q", r.Method)
		}
		switch r.URL.Path {
		case "/v1/sessions/session-1/undo":
			if err := json.NewDecoder(r.Body).Decode(&undoReq); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(gatewayapi.SessionUndoResponse{
				ChangeID:      "change-1",
				RestoredFiles: []string{"/workspace/README.md"},
				Change: protocol.TurnChangeEvent{
					ChangeID:   "change-1",
					Status:     "reverted",
					FileCount:  1,
					Modified:   1,
					Reversible: true,
					Files:      []protocol.TurnChangeFile{{RelPath: "README.md", Change: "modified", Reversible: true}},
				},
			})
		case "/v1/sessions/session-1/redo":
			redoCalled = true
			_ = json.NewEncoder(w).Encode(gatewayapi.SessionUndoResponse{
				ChangeID:      "change-1",
				RestoredFiles: []string{"/workspace/README.md"},
				Change: protocol.TurnChangeEvent{
					ChangeID:   "change-1",
					Status:     "redone",
					FileCount:  1,
					Modified:   1,
					Reversible: true,
					Files:      []protocol.TurnChangeFile{{RelPath: "README.md", Change: "modified", Reversible: true}},
				},
			})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	handled, cmd := m.handleSlashCommand("/undo change-1")
	if !handled || cmd == nil {
		t.Fatalf("undo handled=%v cmd=%v", handled, cmd)
	}
	undoMsg := cmd().(turnUndoMsg)
	if undoMsg.err != nil {
		t.Fatal(undoMsg.err)
	}
	if undoReq.ChangeID != "change-1" || undoReq.Preview {
		t.Fatalf("undo request = %#v", undoReq)
	}
	if !strings.Contains(undoMsg.text, "status: reverted") || !strings.Contains(undoMsg.text, "undo files:") {
		t.Fatalf("undo text = %q", undoMsg.text)
	}

	handled, cmd = m.handleSlashCommand("/redo")
	if !handled || cmd == nil {
		t.Fatalf("redo handled=%v cmd=%v", handled, cmd)
	}
	redoMsg := cmd().(turnRedoMsg)
	if redoMsg.err != nil {
		t.Fatal(redoMsg.err)
	}
	if !redoCalled || !strings.Contains(redoMsg.text, "status: redone") || !strings.Contains(redoMsg.text, "redo files:") {
		t.Fatalf("redoCalled=%v text=%q", redoCalled, redoMsg.text)
	}
}

func TestContextThresholdEventRendersContextBlock(t *testing.T) {
	m := newTestModel(t)
	m.applyEvent(protocol.Event{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
		Percent:             70,
		EstimatedTokens:     705000,
		ContextWindowTokens: 1000000,
		ThresholdTokens:     700000,
		RemainingTokens:     295000,
		MessageCount:        44,
		Round:               3,
		Stage:               "after_tool_results",
		Estimator:           "chars_div_4",
	}})
	if len(m.blocks) == 0 {
		t.Fatal("expected context threshold block")
	}
	block := m.blocks[len(m.blocks)-1]
	if block.Title != "CONTEXT" || block.EventType != protocol.EventContextThreshold {
		t.Fatalf("block = %#v", block)
	}
	for _, want := range []string{"threshold: 70%", "active: 705k / 1.0m", "remaining window: 295k", "stage: after_tool_results"} {
		if !strings.Contains(block.Content, want) {
			t.Fatalf("context threshold block missing %q:\n%s", want, block.Content)
		}
	}
}

func TestRunStatusShowsSpinnerAndInlineStatusShowsElapsedWhileBusy(t *testing.T) {
	m := newTestModel(t)
	m.width = 160
	m.busy = true
	m.status = "running tool shell"
	m.runStartedAt = time.Now().Add(-3 * time.Second)
	m.spinnerFrame = 3

	status := m.inlineStatusView()
	if !strings.Contains(status, "running 3s") {
		t.Fatalf("status %q should show elapsed seconds", status)
	}
	for _, frame := range spinnerFrames {
		if strings.Contains(status, frame) {
			t.Fatalf("inline status %q should not show spinner frame %q", status, frame)
		}
	}

	runStatus := m.runStatusView()
	if !strings.Contains(runStatus, "running tool shell") || !strings.Contains(runStatus, "3s") {
		t.Fatalf("run status %q should show live state and elapsed seconds", runStatus)
	}
	foundSpinner := false
	for _, frame := range spinnerFrames {
		if strings.Contains(runStatus, frame) {
			foundSpinner = true
			break
		}
	}
	if !foundSpinner {
		t.Fatalf("run status %q should show spinner", runStatus)
	}
}

func TestResizeDoesNotReserveHiddenSlashPopup(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 30
	m.resize(false)
	noPopupHeight := m.viewport.Height()

	m.textarea.SetValue("/the")
	m.resize(false)
	withPopupHeight := m.viewport.Height()
	if noPopupHeight <= withPopupHeight {
		t.Fatalf("hidden popup should not reserve rows: noPopup=%d withPopup=%d", noPopupHeight, withPopupHeight)
	}
	if noPopupHeight-withPopupHeight > 8 {
		t.Fatalf("popup should reserve only its rendered height, delta=%d", noPopupHeight-withPopupHeight)
	}
}

func TestChatCommands(t *testing.T) {
	m := newTestModel(t)
	original := m.localChatID
	m.addBlock("user", "USER", "hello")

	handled, cmd := m.handleSlashCommand("/new")
	if !handled || cmd != nil {
		t.Fatalf("/new handled=%v cmd=%v, want handled without command", handled, cmd)
	}
	if m.localChatID == original {
		t.Fatalf("/new should create a new local chat id")
	}
	if len(m.blocks) != 0 {
		t.Fatalf("/new should clear rendered blocks")
	}

	handled, _ = m.handleSlashCommand("/resume")
	if !handled {
		t.Fatalf("/resume should be handled")
	}
	if len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].Content, shortID(original)) {
		t.Fatalf("/resume should list saved chats")
	}
}

type testModelHelper interface {
	Helper()
	Setenv(string, string)
	TempDir() string
}
