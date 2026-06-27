package telegrambot

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const telegramLimit = 4096
const telegramRichLimit = 32768

type Renderer struct {
	Content       strings.Builder
	ThinkingChars int
	ModelCalls    int
	ToolCalls     int
	InputTokens   int64
	OutputTokens  int64
	CacheHit      int64
	CacheMiss     int64
	Reasoning     int64
	Started       time.Time
	LastError     string
	Done          bool
}

type RenderEvent struct {
	Kind  string
	Title string
	Body  string
}

func NewRenderer() *Renderer {
	return &Renderer{Started: time.Now()}
}

func (r *Renderer) Apply(event protocol.Event) []RenderEvent {
	switch event.Type {
	case protocol.EventRunStarted:
		return []RenderEvent{{Kind: "status", Title: "Run", Body: "started"}}
	case protocol.EventModelCallStarted:
		r.ModelCalls++
	case protocol.EventAssistantReasoning:
		r.ThinkingChars += len(fmt.Sprint(event.Data))
	case protocol.EventAssistantDelta:
		r.Content.WriteString(fmt.Sprint(event.Data))
	case protocol.EventToolCallRequested:
		r.ToolCalls++
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: toolSummary(event.Data)}}
	case protocol.EventToolCallFinished:
		return nil
	case protocol.EventProviderUsageUpdate:
		in, out, hit, miss, reasoning := usage(event.Data)
		r.InputTokens += in
		r.OutputTokens += out
		r.CacheHit += hit
		r.CacheMiss += miss
		r.Reasoning += reasoning
	case protocol.EventRunCompleted:
		r.Done = true
	case protocol.EventRunFailed:
		errText := fmt.Sprint(event.Data)
		if errText != "" && errText != r.LastError {
			r.LastError = errText
			return []RenderEvent{{Kind: "error", Title: "Error", Body: errText}}
		}
	}
	return nil
}

func (r *Renderer) StatusText(model, reasoning string) string {
	chunks := r.FinalChunks(model, reasoning)
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0]
}

func (r *Renderer) FinalChunks(model, reasoning string) []string {
	elapsed := time.Since(r.Started).Round(time.Second)
	state := r.state()
	content := strings.TrimSpace(r.Content.String())
	if content == "" {
		content = "Working..."
	}
	header := "<b>" + esc(statusEmoji(state)+" Billyharness · "+titleState(state)) + "</b>\n" +
		esc("🧬 "+model+" · 🧠 "+reasoning+" · ⏱ "+elapsed.String()) + "\n\n"
	footer := "\n\n<i>" + esc(r.footerLine()) + "</i>"
	budget := telegramLimit - telegramUTF16Len(header) - telegramUTF16Len(footer) - 64
	if budget < 1000 {
		budget = 1000
	}
	parts := splitTelegramFormatted(content, budget)
	if len(parts) == 0 {
		parts = []string{markdownToTelegramHTML(content)}
	}
	chunks := make([]string, 0, len(parts))
	for i, part := range parts {
		body := header + part
		if i == len(parts)-1 {
			body += footer
		}
		chunks = append(chunks, body)
	}
	return chunks
}

func (r *Renderer) FinalRichMarkdownChunks(model, reasoning string) []string {
	elapsed := time.Since(r.Started).Round(time.Second)
	content := strings.TrimSpace(r.Content.String())
	if content == "" {
		content = "Working..."
	}
	header := r.richHeaderInline(model, reasoning, elapsed)
	footer := "\n\n_" + markdownInlineEscape(r.footerLine()) + "_"
	budget := telegramRichLimit - len([]rune(header)) - len([]rune(footer)) - 128
	if budget < 4096 {
		budget = 4096
	}
	parts := splitRichMarkdown(content, budget)
	if len(parts) == 0 {
		parts = []string{content}
	}
	chunks := make([]string, 0, len(parts))
	for i, part := range parts {
		body := header + part
		if i == len(parts)-1 {
			body += footer
		}
		chunks = append(chunks, body)
	}
	return chunks
}

func (r *Renderer) state() string {
	if r.LastError != "" {
		return "failed"
	}
	if r.Done {
		return "done"
	}
	return "running"
}

func (r *Renderer) richHeaderInline(model, reasoning string, elapsed time.Duration) string {
	state := r.state()
	return "**" + statusEmoji(state) + " Billyharness · " + markdownInlineEscape(titleState(state)) + "**\n" +
		"_" + markdownInlineEscape("🧬 "+model+" · 🧠 "+reasoning+" · ⏱ "+elapsed.String()) + "_\n\n"
}

func (r *Renderer) footerLine() string {
	var parts []string
	if r.ModelCalls > 0 {
		parts = append(parts, fmt.Sprintf("🤖 LLM %d", r.ModelCalls))
	}
	if r.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("🛠 tools %d", r.ToolCalls))
	}
	if r.InputTokens+r.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("📥 %s 📤 %s", compactInt(r.InputTokens), compactInt(r.OutputTokens)))
	}
	if r.CacheHit+r.CacheMiss > 0 {
		parts = append(parts, fmt.Sprintf("💾 hit %s miss %s", compactInt(r.CacheHit), compactInt(r.CacheMiss)))
	}
	if r.Reasoning > 0 {
		parts = append(parts, fmt.Sprintf("🧠 %s", compactInt(r.Reasoning)))
	}
	if r.ThinkingChars > 0 {
		parts = append(parts, fmt.Sprintf("💭 %s hidden", compactInt(int64(r.ThinkingChars))))
	}
	if len(parts) == 0 {
		return "⚡ streaming"
	}
	return strings.Join(parts, " · ")
}

func ToolMessageHTML(event RenderEvent) string {
	title := event.Title
	if title == "" {
		title = "Tool"
	}
	return trimTelegram("<b>" + esc(title) + "</b>\n" + esc(event.Body))
}

type ToolProgress struct {
	Started time.Time
	Done    bool
	lines   []string
	seen    map[string]bool
}

func NewToolProgress() *ToolProgress {
	return &ToolProgress{
		Started: time.Now(),
		seen:    map[string]bool{},
	}
}

func (p *ToolProgress) Add(event RenderEvent) bool {
	if event.Kind != "tool" {
		return false
	}
	prefix := "•"
	line := strings.TrimSpace(prefix + " " + event.Body)
	if line == "" || p.seen[line] {
		return false
	}
	p.seen[line] = true
	p.lines = append(p.lines, line)
	if len(p.lines) > 24 {
		p.lines = p.lines[len(p.lines)-24:]
	}
	return true
}

func (p *ToolProgress) HTML() string {
	if p == nil || len(p.lines) == 0 {
		return ""
	}
	state := "running"
	if p.Done {
		state = "done"
	}
	elapsed := time.Since(p.Started).Round(time.Second)
	body := strings.Join(p.lines, "\n")
	text := "<b>Tools</b> " + esc(state) + " · <i>" + esc(elapsed.String()) + "</i>\n" + esc(body)
	return trimTelegram(text)
}

func ErrorMessageHTML(text string) string {
	return trimTelegram("<b>Error</b>\n" + esc(text))
}

func PlainMessageHTML(text string) string {
	return trimTelegram(esc(text))
}

func toolSummary(data any) string {
	bytes, _ := json.Marshal(data)
	var call protocol.ToolCall
	if err := json.Unmarshal(bytes, &call); err != nil || call.Name == "" {
		return resultPreview(string(bytes), 600)
	}
	args := map[string]any{}
	_ = json.Unmarshal(call.Arguments, &args)
	switch call.Name {
	case "web_search":
		return "🔎 web_search " + compactArg(args["query"], 96)
	case "web_fetch":
		return "🌐 web_fetch " + compactURL(args["url"], 88)
	case "web_crawl":
		return "🕸 web_crawl " + compactURL(args["url"], 88)
	case "fs_read_file":
		return "📖 read " + compactArg(args["path"], 96)
	case "fs_list":
		return "📁 list " + compactArg(args["path"], 96)
	case "fs_search":
		return "🔍 search " + compactArg(args["query"], 56) + " in " + compactArg(args["path"], 56)
	case "fs_write_file":
		return "✍️ write " + compactArg(args["path"], 96)
	case "shell_exec":
		return "⚙️ shell " + compactArg(args["argv"], 120)
	case "mcp_list_tools":
		return "🔌 mcp tools " + joinToolParts("server="+compactArg(args["server"], 32), optionalToolPart("query", args["query"], 48))
	case "mcp_call":
		return "🔌 mcp call " + compactArg(args["name"], 80)
	default:
		return "🛠 " + call.Name + " " + resultPreview(string(call.Arguments), 160)
	}
}

func resultPreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "empty"
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	head := string(runes[:max(1, limit-80)])
	return head + "\n...[truncated " + fmt.Sprint(len(runes)) + " chars]"
}

func splitTelegramPlain(text string, limit int) []string {
	if limit <= 0 {
		limit = telegramLimit - 64
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		n := telegramRunePrefixLen(runes, limit)
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
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
		extra := block
		if current.Len() > 0 {
			extra = "\n\n" + block
		}
		if current.Len() > 0 && len([]rune(current.String()+extra)) > limit {
			flush()
		}
		if len([]rune(extra)) <= limit {
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(block)
			continue
		}
		for _, part := range splitTelegramPlain(block, limit) {
			if current.Len() > 0 {
				flush()
			}
			chunks = append(chunks, part)
		}
	}
	flush()
	return chunks
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
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
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
				out.WriteString(markdownToTelegramHTML(text[i+2 : i+2+end]))
				out.WriteString("</b>")
				i += 2 + end + 2
				continue
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
							out.WriteString(esc(text[i+1 : labelEnd]))
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

func trimTelegram(text string) string {
	if telegramUTF16Len(text) <= telegramLimit {
		return text
	}
	runes := []rune(text)
	n := telegramRunePrefixLen(runes, telegramLimit-64)
	return string(runes[:n]) + "\n...[truncated]"
}

func esc(text string) string {
	return html.EscapeString(text)
}

func usage(data any) (int64, int64, int64, int64, int64) {
	bytes, _ := json.Marshal(data)
	var u struct {
		InputTokens     int64 `json:"input_tokens"`
		OutputTokens    int64 `json:"output_tokens"`
		CacheHitTokens  int64 `json:"cache_hit_tokens"`
		CacheMissTokens int64 `json:"cache_miss_tokens"`
		ReasoningTokens int64 `json:"reasoning_tokens"`
	}
	_ = json.Unmarshal(bytes, &u)
	return u.InputTokens, u.OutputTokens, u.CacheHitTokens, u.CacheMissTokens, u.ReasoningTokens
}

func compactInt(value int64) string {
	if value >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(value)/1_000_000)
	}
	if value >= 1000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return fmt.Sprint(value)
}

func statusEmoji(state string) string {
	switch state {
	case "done":
		return "✅"
	case "failed":
		return "⛔"
	default:
		return "⚡"
	}
}

func titleState(state string) string {
	switch state {
	case "done":
		return "Done"
	case "failed":
		return "Failed"
	default:
		return "Running"
	}
}

func compactArg(value any, limit int) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return "-"
	}
	text = strings.Join(strings.Fields(text), " ")
	return compactText(text, limit)
}

func compactURL(value any, limit int) string {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return "-"
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		path := parsed.EscapedPath()
		if path == "" {
			path = "/"
		}
		if parsed.RawQuery != "" {
			path += "?…"
		}
		raw = parsed.Host + path
	}
	return compactText(raw, limit)
}

func compactText(text string, limit int) string {
	if limit <= 0 {
		limit = 80
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit < 12 {
		return string(runes[:limit]) + "…"
	}
	head := (limit - 1) * 2 / 3
	tail := limit - 1 - head
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func optionalToolPart(name string, value any, limit int) string {
	text := compactArg(value, limit)
	if text == "-" {
		return ""
	}
	return name + "=" + text
}

func joinToolParts(parts ...string) string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " ")
}

func telegramUTF16Len(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func telegramRunePrefixLen(runes []rune, limit int) int {
	if len(runes) == 0 {
		return 0
	}
	used := 0
	for i, r := range runes {
		next := 1
		if r > 0xFFFF {
			next = 2
		}
		if used+next > limit {
			return max(1, i)
		}
		used += next
	}
	return len(runes)
}
