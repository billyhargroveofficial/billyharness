package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/billyhargroveofficial/billyharness/internal/filesearch"
)

const fileMentionSearchTimeout = 750 * time.Millisecond

type fileMentionToken struct {
	Line  int
	Start int
	End   int
	Query string
	Value string
}

type fileMentionResultsMsg struct {
	Seq     int64
	Token   fileMentionToken
	Results []filesearch.Match
	Err     error
}

func (m *Model) handleFileMentionNavigation(msg tea.KeyPressMsg) bool {
	if !m.fileMentionOpen() {
		return false
	}
	switch msg.String() {
	case "tab", "enter":
		if len(m.fileMentionResults) == 0 {
			return true
		}
		m.clampFileMentionIndex()
		m.insertFileMentionPath(m.fileMentionResults[m.fileMentionIndex].Path)
		return true
	case "down", "ctrl+n":
		if len(m.fileMentionResults) > 0 {
			m.fileMentionIndex = (m.fileMentionIndex + 1) % len(m.fileMentionResults)
		}
		return true
	case "up", "ctrl+p":
		if len(m.fileMentionResults) > 0 {
			m.fileMentionIndex--
			if m.fileMentionIndex < 0 {
				m.fileMentionIndex = len(m.fileMentionResults) - 1
			}
		}
		return true
	case "esc":
		m.fileMentionDismissed = m.textarea.Value()
		m.clearFileMention()
		return true
	}
	return false
}

func (m *Model) updateFileMentionSearch() tea.Cmd {
	token, ok := m.activeFileMentionToken()
	if !ok {
		m.clearFileMention()
		return nil
	}
	if token.Value == m.fileMentionDismissed {
		m.clearFileMention()
		return nil
	}
	if token.Query == m.fileMentionToken.Query && token.Line == m.fileMentionToken.Line && token.Start == m.fileMentionToken.Start && (m.fileMentionPending != 0 || m.fileMentionSearching || len(m.fileMentionResults) > 0 || m.fileMentionErr != "") {
		return nil
	}
	m.fileMentionSeq++
	seq := m.fileMentionSeq
	m.fileMentionPending = seq
	m.fileMentionToken = token
	m.fileMentionIndex = 0
	m.fileMentionResults = nil
	m.fileMentionErr = ""
	m.fileMentionSearching = true
	return m.fileMentionSearchCmd(seq, token)
}

func (m Model) fileMentionSearchCmd(seq int64, token fileMentionToken) tea.Cmd {
	roots := append([]string(nil), m.toolPolicy.WorkspaceRoots...)
	resolver := m.fileResolver
	if resolver == nil {
		resolver = filesearch.NewResolver(filesearch.DefaultCacheTTL)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fileMentionSearchTimeout)
		defer cancel()
		result, err := resolver.Find(ctx, filesearch.Options{
			Roots: roots,
			Query: token.Query,
			Limit: 8,
		})
		return fileMentionResultsMsg{Seq: seq, Token: token, Results: result.Matches, Err: err}
	}
}

func (m *Model) applyFileMentionResults(msg fileMentionResultsMsg) {
	if msg.Seq != m.fileMentionPending {
		return
	}
	token, ok := m.activeFileMentionToken()
	if !ok || token.Value != msg.Token.Value || token.Query != msg.Token.Query || token.Line != msg.Token.Line || token.Start != msg.Token.Start {
		return
	}
	m.fileMentionSearching = false
	m.fileMentionErr = ""
	if msg.Err != nil {
		m.fileMentionErr = msg.Err.Error()
		m.fileMentionResults = nil
		m.fileMentionIndex = 0
		return
	}
	m.fileMentionToken = token
	m.fileMentionResults = append([]filesearch.Match(nil), msg.Results...)
	m.clampFileMentionIndex()
}

func (m Model) activeFileMentionToken() (fileMentionToken, bool) {
	value := m.textarea.Value()
	if value == "" || m.slashLikeInput(value) {
		return fileMentionToken{}, false
	}
	lines := strings.Split(value, "\n")
	lineNo := m.textarea.Line()
	if lineNo < 0 || lineNo >= len(lines) {
		lineNo = len(lines) - 1
	}
	lineRunes := []rune(lines[lineNo])
	col := m.textarea.Column()
	if col < 0 || col > len(lineRunes) {
		col = len(lineRunes)
	}
	prefix := lineRunes[:col]
	at := -1
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] == '@' {
			at = i
			break
		}
		if unicode.IsSpace(prefix[i]) {
			break
		}
	}
	if at < 0 {
		return fileMentionToken{}, false
	}
	if at > 0 && !fileMentionBoundary(prefix[at-1]) {
		return fileMentionToken{}, false
	}
	query := string(prefix[at+1:])
	if strings.ContainsFunc(query, unicode.IsSpace) {
		return fileMentionToken{}, false
	}
	return fileMentionToken{Line: lineNo, Start: at, End: col, Query: query, Value: value}, true
}

func (m Model) slashLikeInput(value string) bool {
	if strings.Contains(value, "\n") {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(value, " \t"), "/")
}

func fileMentionBoundary(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("([{,:", r)
}

func (m Model) fileMentionOpen() bool {
	if _, ok := m.activeFileMentionToken(); !ok {
		return false
	}
	if m.textarea.Value() == m.fileMentionDismissed {
		return false
	}
	return m.fileMentionPending != 0 || m.fileMentionSearching || m.fileMentionErr != "" || len(m.fileMentionResults) > 0
}

func (m *Model) insertFileMentionPath(path string) {
	token, ok := m.activeFileMentionToken()
	if !ok || strings.TrimSpace(path) == "" {
		return
	}
	lines := strings.Split(m.textarea.Value(), "\n")
	if token.Line < 0 || token.Line >= len(lines) {
		return
	}
	lineRunes := []rune(lines[token.Line])
	if token.Start < 0 || token.End < token.Start || token.End > len(lineRunes) {
		return
	}
	lines[token.Line] = string(lineRunes[:token.Start]) + path + string(lineRunes[token.End:])
	next := strings.Join(lines, "\n")
	m.textarea.SetValue(next)
	m.fileMentionDismissed = next
	m.clearFileMention()
	m.status = "inserted " + path
}

func (m *Model) clearFileMention() {
	m.fileMentionPending = 0
	m.fileMentionSearching = false
	m.fileMentionResults = nil
	m.fileMentionIndex = 0
	m.fileMentionErr = ""
	m.fileMentionToken = fileMentionToken{}
}

func (m *Model) clampFileMentionIndex() {
	if len(m.fileMentionResults) == 0 {
		m.fileMentionIndex = 0
		return
	}
	if m.fileMentionIndex < 0 {
		m.fileMentionIndex = 0
	}
	if m.fileMentionIndex >= len(m.fileMentionResults) {
		m.fileMentionIndex = len(m.fileMentionResults) - 1
	}
}

func (m Model) inputPopupView() string {
	if popup := m.slashPopupView(); popup != "" {
		return popup
	}
	return m.fileMentionPopupView()
}

func (m Model) inputPopupHeight() int {
	view := m.inputPopupView()
	if view == "" {
		return 0
	}
	return lipgloss.Height(view)
}

func (m Model) fileMentionPopupView() string {
	if !m.fileMentionOpen() {
		return ""
	}
	styles := m.styles()
	outerW := min(max(40, m.width-4), 88)
	contentW := max(36, outerW-styles.popup.GetHorizontalFrameSize())
	var lines []string
	title := "files"
	if m.fileMentionToken.Query != "" {
		title = "files matching " + fmt.Sprintf("%q", m.fileMentionToken.Query)
	}
	lines = append(lines, styles.popupMuted.Width(contentW).Render(title))
	if m.fileMentionErr != "" {
		lines = append(lines, styles.popupMuted.Width(contentW).Render("File search failed: "+truncateRunes(m.fileMentionErr, contentW)))
		lines = append(lines, styles.popupMuted.Width(contentW).Render("Esc close"))
		return styles.popup.Width(contentW).Render(strings.Join(lines, "\n"))
	}
	if m.fileMentionSearching && len(m.fileMentionResults) == 0 {
		lines = append(lines, styles.popupMuted.Width(contentW).Render("Searching files..."))
		return styles.popup.Width(contentW).Render(strings.Join(lines, "\n"))
	}
	if len(m.fileMentionResults) == 0 {
		lines = append(lines, styles.popupMuted.Width(contentW).Render("No files match"))
		lines = append(lines, styles.popupMuted.Width(contentW).Render("Esc close"))
		return styles.popup.Width(contentW).Render(strings.Join(lines, "\n"))
	}
	index := m.fileMentionIndex
	if index < 0 || index >= len(m.fileMentionResults) {
		index = 0
	}
	limit := min(len(m.fileMentionResults), 6)
	start, end := slashPopupWindow(len(m.fileMentionResults), index, limit)
	if start > 0 {
		lines = append(lines, styles.popupMuted.Width(contentW).Render(fmt.Sprintf("%d previous matches", start)))
	}
	pathW := max(20, contentW-18)
	for i := start; i < end; i++ {
		match := m.fileMentionResults[i]
		line := padRight(truncateRunes(match.Path, pathW), pathW) + "  " + fmt.Sprintf("%s %d", match.Type, match.Score)
		if i == index {
			lines = append(lines, styles.popupSelected.Width(contentW).Render(line))
		} else {
			lines = append(lines, styles.popupLine.Width(contentW).Render(line))
		}
	}
	if end < len(m.fileMentionResults) {
		lines = append(lines, styles.popupMuted.Width(contentW).Render(fmt.Sprintf("%d more matches", len(m.fileMentionResults)-end)))
	}
	lines = append(lines, styles.popupMuted.Width(contentW).Render("Up/Down select  Tab/Enter insert  Esc close"))
	return styles.popup.Width(contentW).Render(strings.Join(lines, "\n"))
}
