package telegrambot

import (
	"strings"
	"unicode/utf8"
)

func splitTelegramPlain(text string, limit int) []string {
	if limit <= 0 {
		limit = telegramLimit - 64
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return splitTelegramUTF16Raw(text, limit)
}

func splitTelegramEscaped(text string, limit int) []string {
	if limit <= 0 {
		limit = telegramLimit - 64
	}
	var chunks []string
	var b strings.Builder
	used := 0
	flush := func() {
		if b.Len() == 0 {
			return
		}
		chunks = append(chunks, b.String())
		b.Reset()
		used = 0
	}
	for _, r := range text {
		part := esc(string(r))
		partLen := telegramUTF16Len(part)
		if used > 0 && used+partLen > limit {
			flush()
		}
		b.WriteString(part)
		used += partLen
	}
	flush()
	return chunks
}

func splitTelegramFormatted(text string, limit int) []string {
	if limit <= 0 {
		limit = telegramLimit - 64
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var chunks []string
	var raw strings.Builder
	lastGoodRaw := ""
	lastGoodHTML := ""
	for _, r := range text {
		raw.WriteRune(r)
		html := markdownToTelegramHTML(raw.String())
		if telegramUTF16Len(html) <= limit {
			lastGoodRaw = raw.String()
			lastGoodHTML = html
			continue
		}
		if lastGoodHTML != "" {
			currentRaw := raw.String()
			goodRaw := lastGoodRaw
			chunks = append(chunks, lastGoodHTML)
			raw.Reset()
			lastGoodRaw = ""
			lastGoodHTML = ""
			remainder := strings.TrimPrefix(currentRaw, goodRaw)
			raw.WriteString(remainder)
		} else {
			escaped := esc(string(r))
			chunks = append(chunks, trimTelegram(escaped))
			raw.Reset()
		}
	}
	if raw.Len() > 0 {
		chunks = append(chunks, markdownToTelegramHTML(raw.String()))
	}
	return chunks
}

func splitRichMarkdown(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 {
		limit = telegramRichLimit - 512
	}
	if limit < 1 {
		limit = 1
	}
	var chunks []string
	var current strings.Builder
	flush := func() {
		part := strings.TrimSpace(current.String())
		if part != "" {
			chunks = append(chunks, part)
		}
		current.Reset()
	}
	for _, block := range markdownBlocks(text) {
		block = strings.TrimRight(block, "\n")
		if block == "" {
			continue
		}
		for _, part := range splitRichMarkdownBlock(block, limit) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			partLen := telegramUTF16Len(part)
			if partLen > limit {
				if current.Len() > 0 {
					flush()
				}
				for _, fallbackPart := range splitRichMarkdownPlain(part, limit) {
					if strings.TrimSpace(fallbackPart) != "" {
						chunks = append(chunks, strings.TrimSpace(fallbackPart))
					}
				}
				continue
			}
			if current.Len() > 0 {
				candidateLen := telegramUTF16Len(current.String()) + telegramUTF16Len("\n\n") + partLen
				if candidateLen > limit {
					flush()
				}
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(part)
		}
	}
	flush()
	return chunks
}

func splitRichMarkdownBlock(block string, limit int) []string {
	if telegramUTF16Len(block) <= limit {
		return []string{block}
	}
	if isFencedMarkdownBlock(block) {
		if parts := splitRichMarkdownFence(block, limit); len(parts) > 0 {
			return parts
		}
	}
	if isMarkdownTableBlock(block) {
		if parts := splitRichMarkdownTable(block, limit); len(parts) > 0 {
			return parts
		}
	}
	return splitRichMarkdownPlain(block, limit)
}

func splitRichMarkdownPlain(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return splitTelegramUTF16Raw(text, limit)
}

func splitRichMarkdownFence(block string, limit int) []string {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return nil
	}
	opener := lines[0]
	closer := "```"
	bodyLines := lines[1:]
	if len(bodyLines) > 0 && strings.HasPrefix(strings.TrimSpace(bodyLines[len(bodyLines)-1]), "```") {
		closer = bodyLines[len(bodyLines)-1]
		bodyLines = bodyLines[:len(bodyLines)-1]
	}
	prefix := opener + "\n"
	suffix := "\n" + closer
	budget := limit - telegramUTF16Len(prefix) - telegramUTF16Len(suffix)
	if budget < 1 {
		return splitRichMarkdownPlain(block, limit)
	}
	body := strings.Join(bodyLines, "\n")
	if body == "" {
		chunk := prefix + suffix[1:]
		if telegramUTF16Len(chunk) <= limit {
			return []string{chunk}
		}
		return splitRichMarkdownPlain(block, limit)
	}
	parts := splitRichCodeContent(body, budget)
	chunks := make([]string, 0, len(parts))
	for _, part := range parts {
		chunk := prefix + strings.TrimRight(part, "\n") + suffix
		if strings.TrimSpace(part) == "" {
			chunk = prefix + suffix[1:]
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func splitRichCodeContent(text string, limit int) []string {
	if text == "" {
		return []string{""}
	}
	var chunks []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, current.String())
		current.Reset()
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		lineLen := telegramUTF16Len(line)
		if lineLen > limit {
			flush()
			chunks = append(chunks, splitTelegramUTF16Raw(line, limit)...)
			continue
		}
		if current.Len() > 0 && telegramUTF16Len(current.String())+lineLen > limit {
			flush()
		}
		current.WriteString(line)
	}
	flush()
	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

func splitRichMarkdownTable(block string, limit int) []string {
	lines := strings.Split(block, "\n")
	if len(lines) < 2 {
		return nil
	}
	header := []string{lines[0], lines[1]}
	base := strings.Join(header, "\n")
	if telegramUTF16Len(base) > limit {
		return splitRichMarkdownPlain(block, limit)
	}
	var chunks []string
	current := append([]string{}, header...)
	reset := func() {
		current = append([]string{}, header...)
	}
	flush := func() {
		if len(current) > len(header) {
			chunks = append(chunks, strings.Join(current, "\n"))
		}
		reset()
	}
	for _, row := range lines[2:] {
		if strings.TrimSpace(row) == "" {
			continue
		}
		candidateLines := append(append([]string{}, current...), row)
		if telegramUTF16Len(strings.Join(candidateLines, "\n")) <= limit {
			current = append(current, row)
			continue
		}
		if len(current) > len(header) {
			flush()
		}
		rowCandidate := base + "\n" + row
		if telegramUTF16Len(rowCandidate) <= limit {
			current = append(current, row)
			continue
		}
		available := limit - telegramUTF16Len(base+"\n")
		if available < 1 {
			for _, part := range splitRichMarkdownPlain(row, limit) {
				chunks = append(chunks, part)
			}
			continue
		}
		for _, part := range splitTelegramUTF16Raw(row, available) {
			part = strings.TrimSpace(part)
			if part != "" {
				chunks = append(chunks, base+"\n"+part)
			}
		}
		reset()
	}
	flush()
	return chunks
}

func isFencedMarkdownBlock(block string) bool {
	lines := strings.Split(block, "\n")
	return len(lines) > 0 && isMarkdownFenceLine(strings.TrimSpace(lines[0]))
}

func isMarkdownTableBlock(block string) bool {
	lines := strings.Split(block, "\n")
	return len(lines) >= 2 && strings.Contains(lines[0], "|") && isMarkdownTableSeparator(lines[1])
}

func isMarkdownTableSeparator(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") || !strings.Contains(line, "-") {
		return false
	}
	cells := strings.Split(strings.Trim(line, "|"), "|")
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return false
		}
		hasDash := false
		for _, r := range cell {
			switch r {
			case '-':
				hasDash = true
			case ':', ' ':
			default:
				return false
			}
		}
		if !hasDash {
			return false
		}
	}
	return true
}

func markdownBlocks(text string) []string {
	lines := strings.Split(text, "\n")
	var blocks []string
	var current []string
	inFence := false
	flush := func() {
		if len(current) == 0 {
			return
		}
		blocks = append(blocks, strings.Join(current, "\n"))
		current = nil
	}
	for _, line := range lines {
		if isMarkdownFenceLine(strings.TrimSpace(line)) {
			current = append(current, line)
			inFence = !inFence
			if !inFence {
				flush()
			}
			continue
		}
		if inFence {
			current = append(current, line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

func markdownInlineEscape(text string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`_`, `\_`,
		`*`, `\*`,
		"`", "\\`",
		`[`, `\[`,
		`]`, `\]`,
	)
	return replacer.Replace(text)
}

func markdownToTelegramHTML(text string) string {
	blocks := markdownBlocks(text)
	if len(blocks) == 0 {
		return ""
	}
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		block = strings.Trim(block, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		switch {
		case isFencedMarkdownBlock(block):
			out = append(out, renderMarkdownFenceTelegramHTML(block))
		case isMarkdownTableBlock(block):
			out = append(out, renderMarkdownTableTelegramHTML(block))
		case isMarkdownQuoteBlock(block):
			out = append(out, renderMarkdownQuoteTelegramHTML(block))
		case isMarkdownListBlock(block):
			out = append(out, renderMarkdownListTelegramHTML(block))
		case isMarkdownHeadingBlock(block):
			out = append(out, renderMarkdownHeadingTelegramHTML(block))
		default:
			out = append(out, markdownInlineToTelegramHTML(block))
		}
	}
	return strings.Join(out, "\n\n")
}

func renderMarkdownFenceTelegramHTML(block string) string {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return ""
	}
	body := lines[1:]
	if len(body) > 0 && isMarkdownFenceLine(strings.TrimSpace(body[len(body)-1])) {
		body = body[:len(body)-1]
	}
	return "<pre>" + esc(strings.Trim(strings.Join(body, "\n"), "\n")) + "</pre>"
}

func renderMarkdownHeadingTelegramHTML(block string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level > 0 && level <= 6 && level < len(trimmed) && trimmed[level] == ' ' {
			lines[i] = "<b>" + markdownInlineToTelegramHTML(strings.TrimSpace(trimmed[level:])) + "</b>"
			continue
		}
		lines[i] = markdownInlineToTelegramHTML(line)
	}
	return strings.Join(lines, "\n")
}

func renderMarkdownQuoteTelegramHTML(block string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		lines[i] = markdownInlineToTelegramHTML(trimmed)
	}
	return "<blockquote>" + strings.Join(lines, "\n") + "</blockquote>"
}

func renderMarkdownListTelegramHTML(block string) string {
	lines := strings.Split(block, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		marker, body, ok := parseMarkdownListItem(line)
		if !ok {
			out = append(out, markdownInlineToTelegramHTML(line))
			continue
		}
		out = append(out, esc(marker)+" "+markdownInlineToTelegramHTML(body))
	}
	return strings.Join(out, "\n")
}

func renderMarkdownTableTelegramHTML(block string) string {
	rows := parseTelegramMarkdownTable(block)
	if len(rows) < 3 {
		return markdownInlineToTelegramHTML(block)
	}
	header := rows[0]
	body := rows[2:]
	lines := make([]string, 0, len(body))
	for _, row := range body {
		if len(row) != len(header) {
			continue
		}
		switch len(row) {
		case 0:
			continue
		case 1:
			lines = append(lines, "• "+markdownInlineToTelegramHTML(row[0]))
		case 2:
			lines = append(lines, "• "+markdownInlineToTelegramHTML(row[0])+": "+markdownInlineToTelegramHTML(row[1]))
		default:
			parts := make([]string, 0, len(row))
			for i := range row {
				parts = append(parts, markdownInlineToTelegramHTML(header[i])+": "+markdownInlineToTelegramHTML(row[i]))
			}
			lines = append(lines, "• "+strings.Join(parts, " · "))
		}
	}
	if len(lines) == 0 {
		return markdownInlineToTelegramHTML(block)
	}
	return strings.Join(lines, "\n")
}

func isMarkdownFenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func isMarkdownHeadingBlock(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		return level > 0 && level <= 6 && level < len(trimmed) && trimmed[level] == ' '
	}
	return false
}

func isMarkdownQuoteBlock(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), ">") {
			return false
		}
	}
	return strings.TrimSpace(block) != ""
}

func isMarkdownListBlock(block string) bool {
	seen := false
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if _, _, ok := parseMarkdownListItem(line); !ok {
			return false
		}
		seen = true
	}
	return seen
}

func parseMarkdownListItem(line string) (marker, body string, ok bool) {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, prefix) {
			return "•", strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), true
		}
	}
	dot := strings.Index(trimmed, ". ")
	if dot <= 0 {
		return "", "", false
	}
	for _, r := range trimmed[:dot] {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return trimmed[:dot+1], strings.TrimSpace(trimmed[dot+2:]), true
}

func parseTelegramMarkdownTable(block string) [][]string {
	lines := strings.Split(block, "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells, ok := splitTelegramMarkdownTableRow(line)
		if !ok {
			return nil
		}
		rows = append(rows, cells)
	}
	return rows
}

func splitTelegramMarkdownTableRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") {
		return nil, false
	}
	var cells []string
	var cell strings.Builder
	inCode := false
	for i := 0; i < len(trimmed); i++ {
		switch trimmed[i] {
		case '\\':
			if i+1 < len(trimmed) && trimmed[i+1] == '|' {
				cell.WriteByte('|')
				i++
				continue
			}
			cell.WriteByte(trimmed[i])
		case '`':
			inCode = !inCode
			cell.WriteByte(trimmed[i])
		case '|':
			if !inCode {
				cells = append(cells, strings.TrimSpace(cell.String()))
				cell.Reset()
				continue
			}
			cell.WriteByte(trimmed[i])
		default:
			cell.WriteByte(trimmed[i])
		}
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	if len(cells) > 0 && cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells, len(cells) > 0
}

func markdownInlineToTelegramHTML(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "```") {
			end := strings.Index(text[i+3:], "```")
			if end >= 0 {
				code := text[i+3 : i+3+end]
				if nl := strings.IndexByte(code, '\n'); nl >= 0 && nl <= 32 {
					first := strings.TrimSpace(code[:nl])
					if first != "" && !strings.ContainsAny(first, " \t<>") {
						code = code[nl+1:]
					}
				}
				out.WriteString("<pre>")
				out.WriteString(esc(strings.Trim(code, "\n")))
				out.WriteString("</pre>")
				i += 3 + end + 3
				continue
			}
		}
		if text[i] == '`' {
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				out.WriteString("<code>")
				out.WriteString(esc(text[i+1 : i+1+end]))
				out.WriteString("</code>")
				i += 1 + end + 1
				continue
			}
		}
		if strings.HasPrefix(text[i:], "**") {
			if end := strings.Index(text[i+2:], "**"); end >= 0 {
				out.WriteString("<b>")
				out.WriteString(markdownInlineToTelegramHTML(text[i+2 : i+2+end]))
				out.WriteString("</b>")
				i += 2 + end + 2
				continue
			}
		}
		if text[i] == '*' && !strings.HasPrefix(text[i:], "**") {
			if end := strings.IndexByte(text[i+1:], '*'); end > 0 {
				inner := text[i+1 : i+1+end]
				if strings.TrimSpace(inner) != "" {
					out.WriteString("<i>")
					out.WriteString(markdownInlineToTelegramHTML(inner))
					out.WriteString("</i>")
					i += 1 + end + 1
					continue
				}
			}
		}
		if text[i] == '[' {
			closeText := strings.IndexByte(text[i+1:], ']')
			if closeText >= 0 {
				labelEnd := i + 1 + closeText
				if labelEnd+1 < len(text) && text[labelEnd+1] == '(' {
					closeURL := strings.IndexByte(text[labelEnd+2:], ')')
					if closeURL >= 0 {
						url := text[labelEnd+2 : labelEnd+2+closeURL]
						if safeURL(url) {
							out.WriteString(`<a href="`)
							out.WriteString(esc(url))
							out.WriteString(`">`)
							out.WriteString(markdownInlineToTelegramHTML(text[i+1 : labelEnd]))
							out.WriteString("</a>")
							i = labelEnd + 2 + closeURL + 1
							continue
						}
					}
				}
			}
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		out.WriteString(esc(string(r)))
		i += size
	}
	return out.String()
}

func safeURL(url string) bool {
	url = strings.TrimSpace(strings.ToLower(url))
	return strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://")
}
