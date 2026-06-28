package telegrambot

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
)

const telegramLimit = 4096
const telegramRichLimit = 32768
const defaultContextWindowTokens = 1_000_000

type Renderer struct {
	Content          strings.Builder
	ThinkingChars    int
	ModelCalls       int
	ToolCalls        int
	BaseAgentTurns   int
	BaseToolCalls    int
	InputTokens      int64
	OutputTokens     int64
	LastInputTokens  int64
	LastOutputTokens int64
	CacheHit         int64
	CacheMiss        int64
	LastCacheHit     int64
	LastCacheMiss    int64
	Reasoning        int64
	ToolSummaryIn    int64
	ToolSummaryOut   int64
	ToolSummaryAPI   int64
	ContextWindow    int64
	usageAccounting  usageAccumulator
	Started          time.Time
	LastError        string
	Done             bool
	toolSummaries    map[string]string
}

type RenderEvent struct {
	Kind  string
	Title string
	Body  string
	Key   string
}

func NewRenderer() *Renderer {
	return NewRendererWithContextWindow(defaultContextWindowTokens)
}

func NewRendererWithContextWindow(contextWindow int64) *Renderer {
	return NewRendererWithContextWindowAndTotals(contextWindow, 0, 0)
}

func NewRendererWithContextWindowAndTotals(contextWindow int64, agentTurns, toolCalls int) *Renderer {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowTokens
	}
	return &Renderer{
		Started:        time.Now(),
		ContextWindow:  contextWindow,
		BaseAgentTurns: max(0, agentTurns),
		BaseToolCalls:  max(0, toolCalls),
		toolSummaries:  map[string]string{},
	}
}

func (r *Renderer) Apply(event protocol.Event) []RenderEvent {
	switch event.Type {
	case protocol.EventRunStarted:
		r.usageAccounting.Reset()
		return []RenderEvent{{Kind: "status", Title: "Run", Body: "started"}}
	case protocol.EventModelCallStarted:
		r.ModelCalls++
		r.usageAccounting.Reset()
	case protocol.EventAssistantReasoning:
		r.ThinkingChars += len(fmt.Sprint(event.Data))
	case protocol.EventAssistantDelta:
		r.Content.WriteString(fmt.Sprint(event.Data))
	case protocol.EventToolCallRequested:
		r.ToolCalls++
		key, summary := toolCallKeyAndSummary(event.Data)
		if key != "" {
			r.toolSummaries[key] = summary
		}
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: "⏳ " + summary, Key: key}}
	case protocol.EventToolCallFinished:
		r.observeToolSummary(event.Data)
		key, summary := r.toolResultSummary(event.Data)
		if summary == "" {
			return nil
		}
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: summary, Key: key}}
	case protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		r.observeToolSummary(event.Data)
		key, summary := toolFailureSummary(event)
		if summary == "" {
			return nil
		}
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: summary, Key: key}}
	case protocol.EventContextThreshold:
		return []RenderEvent{{Kind: "status", Title: "Context", Body: telegramContextThresholdText(event.Data), Key: telegramContextThresholdKey(event.Data)}}
	case protocol.EventProviderUsageUpdate:
		delta := r.usageAccounting.Apply(usage(event.Data))
		current := r.usageAccounting.Current()
		r.InputTokens += delta.InputTokens
		r.OutputTokens += delta.OutputTokens
		r.LastInputTokens = current.InputTokens
		r.LastOutputTokens = current.OutputTokens
		r.CacheHit += delta.CacheHitTokens
		r.CacheMiss += delta.CacheMissTokens
		r.LastCacheHit = current.CacheHitTokens
		r.LastCacheMiss = current.CacheMissTokens
		r.Reasoning += delta.ReasoningTokens
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
	budget := telegramRichLimit - telegramUTF16Len(header) - telegramUTF16Len(footer) - 128
	if budget < 1 {
		budget = 1
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
	return r.footerLineWithContext(true)
}

func (r *Renderer) footerLineWithoutContext() string {
	return r.footerLineWithContext(false)
}

func (r *Renderer) footerLineWithContext(includeContext bool) string {
	var parts []string
	if turns := r.BaseAgentTurns + r.ModelCalls; turns > 0 {
		parts = append(parts, fmt.Sprintf("🔁 agent turns %d", turns))
	}
	if tools := r.BaseToolCalls + r.ToolCalls; tools > 0 {
		parts = append(parts, fmt.Sprintf("🛠 tools %d", tools))
	}
	if context := r.contextLine(); includeContext && context != "" {
		parts = append(parts, context)
	}
	if r.LastCacheHit+r.LastCacheMiss > 0 {
		parts = append(parts, fmt.Sprintf("💾 hit %s miss %s", compactInt(r.LastCacheHit), compactInt(r.LastCacheMiss)))
	}
	if r.ToolSummaryIn+r.ToolSummaryOut > 0 {
		parts = append(parts, fmt.Sprintf("🧩 websum %s→%s", compactInt(r.ToolSummaryIn), compactInt(r.ToolSummaryOut)))
		parts = append(parts, fmt.Sprintf("sumapi %s", compactInt(r.ToolSummaryAPI)))
	}
	if len(parts) == 0 {
		return "⚡ streaming"
	}
	return strings.Join(parts, " · ")
}

func (r *Renderer) contextLine() string {
	used := r.LastInputTokens + r.LastOutputTokens
	if used <= 0 {
		return ""
	}
	if r.ContextWindow <= 0 {
		return "🪟 ctx " + compactInt(used)
	}
	return fmt.Sprintf("🪟 ctx %s/%s %s", compactInt(used), compactInt(r.ContextWindow), percentText(used, r.ContextWindow))
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
	lines   []toolProgressLine
	index   map[string]int
	seen    map[string]bool
}

type toolProgressLine struct {
	key  string
	text string
}

func NewToolProgress() *ToolProgress {
	return &ToolProgress{
		Started: time.Now(),
		index:   map[string]int{},
		seen:    map[string]bool{},
	}
}

func (p *ToolProgress) Add(event RenderEvent) bool {
	if event.Kind != "tool" && event.Kind != "status" {
		return false
	}
	prefix := "•"
	line := strings.TrimSpace(prefix + " " + event.Body)
	if line == "" {
		return false
	}
	key := strings.TrimSpace(event.Key)
	if key == "" {
		key = line
	}
	if idx, ok := p.index[key]; ok {
		if p.lines[idx].text == line {
			return false
		}
		p.lines[idx].text = line
		return true
	}
	if p.seen[line] {
		return false
	}
	p.seen[line] = true
	p.index[key] = len(p.lines)
	p.lines = append(p.lines, toolProgressLine{key: key, text: line})
	if len(p.lines) > 24 {
		removed := p.lines[0]
		delete(p.index, removed.key)
		p.lines = p.lines[1:]
		for i, entry := range p.lines {
			p.index[entry.key] = i
		}
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
	lines := make([]string, 0, len(p.lines))
	for _, line := range p.lines {
		lines = append(lines, line.text)
	}
	body := strings.Join(lines, "\n")
	text := "<b>Tools</b> " + esc(state) + " · <i>" + esc(elapsed.String()) + "</i>\n" + esc(body)
	return trimTelegram(text)
}

func (p *ToolProgress) PlainText() string {
	if p == nil || len(p.lines) == 0 {
		return ""
	}
	state := "running"
	if p.Done {
		state = "done"
	}
	elapsed := time.Since(p.Started).Round(time.Second)
	lines := make([]string, 0, len(p.lines))
	for _, line := range p.lines {
		lines = append(lines, line.text)
	}
	return "Tools " + state + " · " + elapsed.String() + "\n" + strings.Join(lines, "\n")
}

func (p *ToolProgress) PlainTextLimit(limit int) string {
	if p == nil || len(p.lines) == 0 || limit <= 0 {
		return ""
	}
	text := p.PlainText()
	if telegramUTF16Len(text) <= limit {
		return text
	}
	state := "running"
	if p.Done {
		state = "done"
	}
	elapsed := time.Since(p.Started).Round(time.Second)
	header := "Tools " + state + " · " + elapsed.String()
	marker := "…[truncated]"
	budget := limit - telegramUTF16Len(header) - telegramUTF16Len(marker) - 2
	if budget <= 0 {
		return trimToUTF16Tail(header+"\n"+marker, limit)
	}
	var selected []string
	used := 0
	for i := len(p.lines) - 1; i >= 0; i-- {
		line := p.lines[i].text
		lineLen := telegramUTF16Len(line)
		if len(selected) > 0 {
			lineLen++
		}
		if used+lineLen <= budget {
			selected = append([]string{line}, selected...)
			used += lineLen
			continue
		}
		if len(selected) == 0 {
			selected = append(selected, trimToUTF16Tail(line, budget))
		}
		break
	}
	if len(selected) == 0 {
		return header + "\n" + marker
	}
	return header + "\n" + marker + "\n" + strings.Join(selected, "\n")
}

func ErrorMessageHTML(text string) string {
	return trimTelegram("<b>Error</b>\n" + esc(text))
}

func PlainMessageHTML(text string) string {
	return trimTelegram(esc(text))
}

func toolCallKeyAndSummary(data any) (string, string) {
	return toolrender.CallKeyAndLine(data, toolrender.StyleTelegram)
}

func (r *Renderer) toolResultSummary(data any) (string, string) {
	key, _ := toolrender.ResultKeyAndLine(data, "", toolrender.StyleTelegram)
	if key == "" {
		return "", ""
	}
	return toolrender.ResultKeyAndLine(data, r.toolSummaries[key], toolrender.StyleTelegram)
}

func toolFailureSummary(event protocol.Event) (string, string) {
	bytes, _ := json.Marshal(event.Data)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err == nil && toolResultHasFailureDetails(result) {
		if result.CallID == "" {
			result.CallID = event.CallID
		}
		if result.Name == "" {
			result.Name = "tool"
		}
		result.IsError = true
		return toolrender.ResultKeyAndLine(result, "", toolrender.StyleTelegram)
	}
	return toolLifecycleFailureSummary(event)
}

func toolResultHasFailureDetails(result protocol.ToolResult) bool {
	return result.Content != "" ||
		result.ErrorCode != "" ||
		result.OutputRef != "" ||
		result.Truncated ||
		len(result.Metadata) > 0 ||
		result.IsError
}

func toolLifecycleFailureSummary(event protocol.Event) (string, string) {
	key := strings.TrimSpace(event.CallID)
	var progress protocol.ToolProgressEvent
	bytes, _ := json.Marshal(event.Data)
	_ = json.Unmarshal(bytes, &progress)
	if key == "" {
		key = strings.TrimSpace(progress.CallID)
	}
	name := strings.TrimSpace(progress.Name)
	if name == "" {
		name = "tool"
	}
	status := "failed"
	if event.Type == protocol.EventToolCallAborted {
		status = "aborted"
	}
	message := strings.TrimSpace(progress.Message)
	if message == "" {
		message = strings.TrimSpace(progress.Status)
	}
	line := "⛔ " + name + " " + status
	if message != "" {
		line += " · " + toolrender.CompactText(message, 96)
	}
	if key == "" {
		key = name + ":" + status
	}
	return key, line
}

func (r *Renderer) observeToolSummary(data any) {
	inTok, outTok, apiTok := toolSummaryTokens(data)
	if inTok <= 0 && outTok <= 0 && apiTok <= 0 {
		return
	}
	r.ToolSummaryIn += inTok
	r.ToolSummaryOut += outTok
	r.ToolSummaryAPI += apiTok
}

func toolSummaryTokens(data any) (inputTokens, outputTokens, apiTokens int64) {
	bytes, _ := json.Marshal(data)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err != nil || result.Metadata == nil {
		return 0, 0, 0
	}
	inputTokens = metadataInt64(result.Metadata, "tool_summary_input_tokens")
	outputTokens = metadataInt64(result.Metadata, "tool_summary_output_tokens")
	apiTokens = metadataInt64(result.Metadata, "tool_summary_api_total_tokens")
	if apiTokens == 0 {
		apiTokens = metadataInt64(result.Metadata, "tool_summary_api_input_tokens") + metadataInt64(result.Metadata, "tool_summary_api_output_tokens")
	}
	return inputTokens, outputTokens, apiTokens
}

func metadataInt64(metadata map[string]any, key string) int64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case float64:
		if value > 0 {
			return int64(value)
		}
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	}
	return 0
}

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

func trimTelegram(text string) string {
	if telegramUTF16Len(text) <= telegramLimit {
		return text
	}
	runes := []rune(text)
	n := telegramRunePrefixLen(runes, telegramLimit-64)
	return string(runes[:n]) + "\n...[truncated]"
}

func trimTelegramTail(text string) string {
	if telegramUTF16Len(text) <= telegramLimit {
		return text
	}
	marker := "…[truncated]\n"
	budget := telegramLimit - telegramUTF16Len(marker)
	if budget <= 0 {
		return marker
	}
	return marker + trimToUTF16Tail(text, budget)
}

func trimToUTF16Tail(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if telegramUTF16Len(text) <= limit {
		return text
	}
	marker := "…"
	budget := limit - telegramUTF16Len(marker)
	if budget <= 0 {
		return marker
	}
	runes := []rune(text)
	n := telegramRuneSuffixLen(runes, budget)
	return marker + string(runes[len(runes)-n:])
}

func esc(text string) string {
	return html.EscapeString(text)
}

type tokenUsage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64
}

type usageAccumulator struct {
	last    tokenUsage
	hasLast bool
}

func usage(data any) tokenUsage {
	bytes, _ := json.Marshal(data)
	var u struct {
		InputTokens     int64 `json:"input_tokens"`
		OutputTokens    int64 `json:"output_tokens"`
		CacheHitTokens  int64 `json:"cache_hit_tokens"`
		CacheMissTokens int64 `json:"cache_miss_tokens"`
		ReasoningTokens int64 `json:"reasoning_tokens"`
	}
	_ = json.Unmarshal(bytes, &u)
	return tokenUsage{
		InputTokens:     nonNegativeUsage(u.InputTokens),
		OutputTokens:    nonNegativeUsage(u.OutputTokens),
		CacheHitTokens:  nonNegativeUsage(u.CacheHitTokens),
		CacheMissTokens: nonNegativeUsage(u.CacheMissTokens),
		ReasoningTokens: nonNegativeUsage(u.ReasoningTokens),
	}
}

func (a *usageAccumulator) Reset() {
	a.last = tokenUsage{}
	a.hasLast = false
}

func (a *usageAccumulator) Apply(update tokenUsage) tokenUsage {
	if update.zero() {
		return tokenUsage{}
	}
	if !a.hasLast {
		a.last = update
		a.hasLast = true
		return update
	}
	if update == a.last {
		return tokenUsage{}
	}
	if update.atLeast(a.last) {
		delta := update.minus(a.last)
		a.last = update
		return delta
	}
	a.last = update
	return update
}

func (a usageAccumulator) Current() tokenUsage {
	if !a.hasLast {
		return tokenUsage{}
	}
	return a.last
}

func (u tokenUsage) zero() bool {
	return u == tokenUsage{}
}

func (u tokenUsage) atLeast(other tokenUsage) bool {
	return u.InputTokens >= other.InputTokens &&
		u.OutputTokens >= other.OutputTokens &&
		u.CacheHitTokens >= other.CacheHitTokens &&
		u.CacheMissTokens >= other.CacheMissTokens &&
		u.ReasoningTokens >= other.ReasoningTokens
}

func (u tokenUsage) minus(other tokenUsage) tokenUsage {
	return tokenUsage{
		InputTokens:     u.InputTokens - other.InputTokens,
		OutputTokens:    u.OutputTokens - other.OutputTokens,
		CacheHitTokens:  u.CacheHitTokens - other.CacheHitTokens,
		CacheMissTokens: u.CacheMissTokens - other.CacheMissTokens,
		ReasoningTokens: u.ReasoningTokens - other.ReasoningTokens,
	}
}

func nonNegativeUsage(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
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

func telegramContextThresholdText(value any) string {
	data := contextThresholdData(value)
	if data.Percent <= 0 {
		return "⚠ context threshold crossed"
	}
	body := fmt.Sprintf("⚠ context %d%% · %s", data.Percent, compactInt(data.EstimatedTokens))
	if data.ContextWindowTokens > 0 {
		body += "/" + compactInt(data.ContextWindowTokens)
	}
	if data.Stage != "" {
		body += " · " + data.Stage
	}
	return body
}

func telegramContextThresholdKey(value any) string {
	data := contextThresholdData(value)
	if data.Percent <= 0 {
		return "context-threshold"
	}
	return fmt.Sprintf("context-threshold-%d", data.Percent)
}

func contextThresholdData(value any) protocol.ContextThresholdEvent {
	bytes, _ := json.Marshal(value)
	var data protocol.ContextThresholdEvent
	_ = json.Unmarshal(bytes, &data)
	return data
}

func percentText(used, window int64) string {
	if window <= 0 {
		return "0%"
	}
	percent := float64(used) / float64(window) * 100
	if percent < 10 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
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

func telegramUTF16Len(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func splitTelegramUTF16Raw(text string, limit int) []string {
	if limit <= 0 {
		limit = 1
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
