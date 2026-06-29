package render

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

type TerminalMarkdownStyles struct {
	heading     lipgloss.Style
	strong      lipgloss.Style
	emphasis    lipgloss.Style
	code        lipgloss.Style
	codeBlock   lipgloss.Style
	tableBorder lipgloss.Style
	tableHeader lipgloss.Style
	quote       lipgloss.Style
	listMarker  lipgloss.Style
	link        lipgloss.Style
	rule        lipgloss.Style
	omitted     lipgloss.Style
}

type MarkdownTheme struct {
	AssistantForeground string
	ToolForeground      string
	ToolBackground      string
	ToolBorder          string
	BlockBorder         string
	MutedForeground     string
}

func RenderAssistantMarkdown(body string, width int, styles TerminalMarkdownStyles, live bool) string {
	state := newStreamingMarkdownState(body, live)
	if state.finalCanonical {
		return RenderTerminalMarkdown(state.rawMarkdown, width, styles)
	}
	stable, tail := state.stableCommitted, state.mutableLiveTail
	switch {
	case stable == "":
		return state.rawMarkdown
	case tail == "":
		return RenderTerminalMarkdown(stable, width, styles)
	default:
		rendered := strings.TrimRight(RenderTerminalMarkdown(stable, width, styles), "\n")
		if rendered == "" {
			return tail
		}
		return rendered + "\n" + tail
	}
}

func RenderTerminalMarkdown(text string, width int, styles TerminalMarkdownStyles) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	fenceMarker := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if marker, ok := markdownFenceMarker(trimmed); ok {
			if !inFence {
				inFence = true
				fenceMarker = marker
				continue
			}
			if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
				continue
			}
		}
		if inFence {
			out = append(out, styles.codeBlock.Width(max(1, width)).Render(line))
			continue
		}
		if table, next, ok := parseMarkdownTable(lines, i); ok {
			out = append(out, renderMarkdownTable(table, width, styles))
			i = next - 1
			continue
		}
		out = append(out, renderMarkdownLine(line, width, styles))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

type streamingMarkdownState struct {
	rawMarkdown     string
	stableCommitted string
	mutableLiveTail string
	holdbackKind    string
	finalCanonical  bool
}

const (
	markdownHoldbackCodeFence = "code_fence"
	markdownHoldbackTable     = "table"
)

func newStreamingMarkdownState(text string, live bool) streamingMarkdownState {
	state := streamingMarkdownState{rawMarkdown: text, finalCanonical: !live}
	if !live {
		state.stableCommitted = text
		return state
	}
	cut, holdback := liveMarkdownStablePrefix(text)
	state.stableCommitted = text[:cut]
	state.mutableLiveTail = text[cut:]
	state.holdbackKind = holdback
	return state
}

func liveMarkdownStablePrefixLen(text string) int {
	cut, _ := liveMarkdownStablePrefix(text)
	return cut
}

func liveMarkdownStablePrefix(text string) (int, string) {
	if text == "" {
		return 0, ""
	}
	newline := strings.LastIndexByte(text, '\n')
	if newline < 0 {
		return 0, ""
	}
	cut := newline + 1
	prefix := text[:cut]
	if start, ok := liveOpenFenceStart(prefix); ok {
		return start, markdownHoldbackCodeFence
	}
	if start, ok := liveUnboundedTableStart(prefix); ok {
		return start, markdownHoldbackTable
	}
	return cut, ""
}

type markdownLineSpan struct {
	text  string
	start int
}

func markdownLineSpans(text string) []markdownLineSpan {
	spans := make([]markdownLineSpan, 0, strings.Count(text, "\n")+1)
	for start := 0; start < len(text); {
		rel := strings.IndexByte(text[start:], '\n')
		if rel < 0 {
			spans = append(spans, markdownLineSpan{text: text[start:], start: start})
			break
		}
		newline := start + rel
		lineEnd := newline
		if lineEnd > start && text[lineEnd-1] == '\r' {
			lineEnd--
		}
		spans = append(spans, markdownLineSpan{text: text[start:lineEnd], start: start})
		start = newline + 1
	}
	return spans
}

func liveOpenFenceStart(text string) (int, bool) {
	inFence := false
	fenceStart := 0
	fenceMarker := ""
	for _, span := range markdownLineSpans(text) {
		marker, ok := markdownFenceMarker(strings.TrimSpace(span.text))
		if !ok {
			continue
		}
		if !inFence {
			inFence = true
			fenceStart = span.start
			fenceMarker = marker
			continue
		}
		if marker == fenceMarker {
			inFence = false
			fenceMarker = ""
		}
	}
	return fenceStart, inFence
}

func liveUnboundedTableStart(text string) (int, bool) {
	spans := markdownLineSpans(text)
	if len(spans) == 0 || strings.TrimSpace(spans[len(spans)-1].text) == "" {
		return 0, false
	}
	start := len(spans)
	for start > 0 {
		trimmed := strings.TrimSpace(spans[start-1].text)
		if trimmed == "" || isFenceLine(trimmed) {
			break
		}
		if _, ok := parseMarkdownTableRow(spans[start-1].text); !ok {
			break
		}
		start--
	}
	if start == len(spans) {
		return 0, false
	}
	return spans[start].start, true
}

type markdownTable struct {
	header []string
	rows   [][]string
}

func parseMarkdownTable(lines []string, start int) (markdownTable, int, bool) {
	if start+1 >= len(lines) {
		return markdownTable{}, start, false
	}
	header, ok := parseMarkdownTableRow(lines[start])
	if !ok || len(header) < 2 {
		return markdownTable{}, start, false
	}
	separator, ok := parseMarkdownTableRow(lines[start+1])
	if !ok || !isMarkdownTableSeparator(separator) || len(separator) != len(header) {
		return markdownTable{}, start, false
	}
	table := markdownTable{header: header}
	i := start + 2
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" || isFenceLine(strings.TrimSpace(lines[i])) {
			break
		}
		row, ok := parseMarkdownTableRow(lines[i])
		if !ok || len(row) != len(header) || isMarkdownTableSeparator(row) {
			break
		}
		table.rows = append(table.rows, row)
	}
	return table, i, true
}

func parseMarkdownTableRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") {
		return nil, false
	}
	cells := splitMarkdownTableCells(trimmed)
	if len(cells) > 0 && strings.TrimSpace(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && strings.TrimSpace(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	if len(cells) < 2 {
		return nil, false
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells, true
}

func splitMarkdownTableCells(line string) []string {
	cells := []string{}
	var cell strings.Builder
	inCode := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if i+1 < len(line) && line[i+1] == '|' {
				cell.WriteByte('|')
				i++
				continue
			}
			cell.WriteByte(line[i])
		case '`':
			inCode = !inCode
			cell.WriteByte(line[i])
		case '|':
			if !inCode {
				cells = append(cells, cell.String())
				cell.Reset()
				continue
			}
			cell.WriteByte(line[i])
		default:
			cell.WriteByte(line[i])
		}
	}
	cells = append(cells, cell.String())
	return cells
}

func isMarkdownTableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 {
			return false
		}
		for _, r := range cell {
			if r != '-' {
				return false
			}
		}
	}
	return true
}

func renderMarkdownTable(table markdownTable, width int, styles TerminalMarkdownStyles) string {
	if len(table.header) == 0 {
		return ""
	}
	colWidths := markdownTableWidths(table, width, styles)
	var lines []string
	lines = append(lines, markdownTableBorder("┌", "┬", "┐", colWidths, styles))
	lines = append(lines, markdownTableRow(table.header, colWidths, styles, true))
	lines = append(lines, markdownTableBorder("├", "┼", "┤", colWidths, styles))
	for _, row := range table.rows {
		lines = append(lines, markdownTableRow(row, colWidths, styles, false))
	}
	lines = append(lines, markdownTableBorder("└", "┴", "┘", colWidths, styles))
	return strings.Join(lines, "\n")
}

func markdownTableWidths(table markdownTable, width int, styles TerminalMarkdownStyles) []int {
	cols := len(table.header)
	widths := make([]int, cols)
	for i, cell := range table.header {
		widths[i] = max(widths[i], min(48, lipgloss.Width(renderInlineMarkdown(cell, styles))))
	}
	for _, row := range table.rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			widths[i] = max(widths[i], min(48, lipgloss.Width(renderInlineMarkdown(cell, styles))))
		}
	}
	for i := range widths {
		widths[i] = max(3, widths[i])
	}
	maxTableWidth := max(16, width)
	for markdownTableWidth(widths) > maxTableWidth {
		idx := widestColumn(widths)
		if idx < 0 || widths[idx] <= 3 {
			break
		}
		widths[idx]--
	}
	return widths
}

func markdownTableWidth(widths []int) int {
	total := 1
	for _, width := range widths {
		total += width + 3
	}
	return total
}

func widestColumn(widths []int) int {
	idx := -1
	best := 0
	for i, width := range widths {
		if width > best {
			best = width
			idx = i
		}
	}
	return idx
}

func markdownTableBorder(left, sep, right string, widths []int, styles TerminalMarkdownStyles) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("─", width+2))
	}
	return styles.tableBorder.Render(left + strings.Join(parts, sep) + right)
}

func markdownTableRow(cells []string, widths []int, styles TerminalMarkdownStyles, header bool) string {
	var b strings.Builder
	b.WriteString(styles.tableBorder.Render("│"))
	for i, width := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		rendered := renderInlineMarkdown(cell, styles)
		rendered = xansi.Truncate(rendered, width, "…")
		if header {
			rendered = styles.tableHeader.Render(rendered)
		}
		b.WriteByte(' ')
		b.WriteString(rendered)
		b.WriteString(strings.Repeat(" ", max(0, width-lipgloss.Width(rendered))))
		b.WriteByte(' ')
		b.WriteString(styles.tableBorder.Render("│"))
	}
	return b.String()
}

func truncateDisplay(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range text {
		next := b.String() + string(r)
		if lipgloss.Width(next+"…") > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

func renderMarkdownLine(line string, width int, styles TerminalMarkdownStyles) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if isHorizontalRule(trimmed) {
		ruleWidth := min(max(8, width-2), 96)
		return styles.rule.Render(strings.Repeat("─", ruleWidth))
	}
	if heading, ok := markdownHeading(trimmed); ok {
		return styles.heading.Render(heading)
	}
	if quote, ok := strings.CutPrefix(trimmed, ">"); ok {
		return styles.quote.Render("│ " + renderInlineMarkdown(strings.TrimSpace(quote), styles))
	}
	if marker, rest, ok := unorderedListItem(line); ok {
		return styles.listMarker.Render(marker) + " " + renderInlineMarkdown(rest, styles)
	}
	if marker, rest, ok := orderedListItem(line); ok {
		return styles.listMarker.Render(marker) + " " + renderInlineMarkdown(rest, styles)
	}
	return renderInlineMarkdown(line, styles)
}

func renderInlineMarkdown(text string, styles TerminalMarkdownStyles) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch {
		case strings.HasPrefix(text[i:], "!["):
			if alt, _, next, ok := parseBracketParen(text, i+1); ok {
				label := strings.TrimSpace(alt)
				if label == "" {
					label = "image omitted"
				} else {
					label = "image omitted: " + label
				}
				b.WriteString(styles.omitted.Render("[" + label + "]"))
				i = next
				continue
			}
		case text[i] == '[':
			if label, url, next, ok := parseBracketParen(text, i); ok {
				b.WriteString(styles.link.Render(renderInlineMarkdown(label, styles)))
				if strings.TrimSpace(url) != "" {
					b.WriteString(" ")
					b.WriteString(styles.emphasis.Render("(" + strings.TrimSpace(url) + ")"))
				}
				i = next
				continue
			}
		case text[i] == '`':
			if inner, next, ok := parseDelimited(text, i, "`"); ok {
				b.WriteString(styles.code.Render(inner))
				i = next
				continue
			}
		case strings.HasPrefix(text[i:], "**"):
			if inner, next, ok := parseDelimited(text, i, "**"); ok {
				b.WriteString(styles.strong.Render(renderInlineMarkdown(inner, styles)))
				i = next
				continue
			}
		case strings.HasPrefix(text[i:], "__"):
			if inner, next, ok := parseDelimited(text, i, "__"); ok {
				b.WriteString(styles.strong.Render(renderInlineMarkdown(inner, styles)))
				i = next
				continue
			}
		case text[i] == '*':
			if inner, next, ok := parseDelimited(text, i, "*"); ok {
				b.WriteString(styles.emphasis.Render(renderInlineMarkdown(inner, styles)))
				i = next
				continue
			}
		case text[i] == '_':
			if inner, next, ok := parseDelimited(text, i, "_"); ok {
				b.WriteString(styles.emphasis.Render(renderInlineMarkdown(inner, styles)))
				i = next
				continue
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

func isFenceLine(trimmed string) bool {
	_, ok := markdownFenceMarker(trimmed)
	return ok
}

func LastFencedCodeBlock(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	inFence := false
	fenceMarker := ""
	var current []string
	last := ""
	found := false
	for _, line := range lines {
		marker, ok := markdownFenceMarker(strings.TrimSpace(line))
		if ok {
			if !inFence {
				inFence = true
				fenceMarker = marker
				current = current[:0]
				continue
			}
			if marker == fenceMarker {
				last = strings.Trim(strings.Join(current, "\n"), "\n")
				found = true
				inFence = false
				fenceMarker = ""
				current = nil
				continue
			}
		}
		if inFence {
			current = append(current, line)
		}
	}
	return last, found
}

func markdownFenceMarker(trimmed string) (string, bool) {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```", true
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~", true
	default:
		return "", false
	}
}

func isHorizontalRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	var marker rune
	for _, r := range trimmed {
		if r == ' ' || r == '\t' {
			continue
		}
		if marker == 0 {
			switch r {
			case '-', '*', '_':
				marker = r
			default:
				return false
			}
			continue
		}
		if r != marker {
			return false
		}
	}
	return marker != 0
}

func markdownHeading(trimmed string) (string, bool) {
	level := 0
	for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[level:]), true
}

func unorderedListItem(line string) (string, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 2 || trimmed[1] != ' ' {
		return "", "", false
	}
	switch trimmed[0] {
	case '-', '*', '+':
		return "•", strings.TrimSpace(trimmed[2:]), true
	default:
		return "", "", false
	}
}

func orderedListItem(line string) (string, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	i := 0
	for i < len(trimmed) && unicode.IsDigit(rune(trimmed[i])) {
		i++
	}
	if i == 0 || i+1 >= len(trimmed) {
		return "", "", false
	}
	if (trimmed[i] != '.' && trimmed[i] != ')') || trimmed[i+1] != ' ' {
		return "", "", false
	}
	return trimmed[:i+1], strings.TrimSpace(trimmed[i+2:]), true
}

func parseDelimited(text string, start int, delim string) (string, int, bool) {
	bodyStart := start + len(delim)
	if bodyStart >= len(text) {
		return "", start, false
	}
	end := strings.Index(text[bodyStart:], delim)
	if end < 0 {
		return "", start, false
	}
	bodyEnd := bodyStart + end
	if bodyEnd == bodyStart {
		return "", start, false
	}
	return text[bodyStart:bodyEnd], bodyEnd + len(delim), true
}

func parseBracketParen(text string, start int) (label, url string, next int, ok bool) {
	if start >= len(text) || text[start] != '[' {
		return "", "", start, false
	}
	closeBracket := strings.IndexByte(text[start+1:], ']')
	if closeBracket < 0 {
		return "", "", start, false
	}
	labelEnd := start + 1 + closeBracket
	if labelEnd+1 >= len(text) || text[labelEnd+1] != '(' {
		return "", "", start, false
	}
	closeParen := strings.IndexByte(text[labelEnd+2:], ')')
	if closeParen < 0 {
		return "", "", start, false
	}
	urlEnd := labelEnd + 2 + closeParen
	return text[start+1 : labelEnd], text[labelEnd+2 : urlEnd], urlEnd + 1, true
}

func TerminalMarkdownStyleSet(theme MarkdownTheme) TerminalMarkdownStyles {
	return TerminalMarkdownStyles{
		heading: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.AssistantForeground)).
			Bold(true),
		strong: lipgloss.NewStyle().
			Bold(true),
		emphasis: lipgloss.NewStyle().
			Italic(true),
		code: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.ToolForeground)).
			Background(lipgloss.Color(theme.ToolBackground)),
		codeBlock: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.ToolForeground)).
			Background(lipgloss.Color(theme.ToolBackground)).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color(theme.ToolBorder)).
			PaddingLeft(1),
		tableBorder: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.BlockBorder)),
		tableHeader: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.AssistantForeground)).
			Bold(true),
		quote: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.MutedForeground)),
		listMarker: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.ToolBorder)).
			Bold(true),
		link: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#BFE7FF")).
			Underline(true),
		rule: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.BlockBorder)),
		omitted: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.MutedForeground)).
			Italic(true),
	}
}
