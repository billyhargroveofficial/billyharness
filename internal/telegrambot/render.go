package telegrambot

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
)

const telegramLimit = 4096
const telegramRichLimit = 32768
const telegramLiveProgressLimit = 1900
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
	HelperModelIn    int64
	HelperModelOut   int64
	HelperModelHit   int64
	HelperModelMiss  int64
	HelperModelAPI   int64
	HelperAPICalls   int
	HelperCostUSD    float64
	ContextWindow    int64
	Started          time.Time
	LastEventAt      time.Time
	LastEventType    protocol.EventType
	LastError        string
	Done             bool
	projector        *projector.Projector
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
		projector:      projector.New(),
	}
}

func (r *Renderer) Apply(event protocol.Event) []RenderEvent {
	if r.projector == nil {
		r.projector = projector.New()
	}
	r.LastEventAt = time.Now()
	r.LastEventType = event.Type
	previousError := r.LastError
	snapshot := r.projector.Apply(event)
	r.applySnapshot(snapshot)
	switch event.Type {
	case protocol.EventRunStarted:
		return []RenderEvent{{Kind: "status", Title: "Run", Body: "started"}}
	case protocol.EventModelCallStarted:
	case protocol.EventAssistantReasoning:
	case protocol.EventAssistantDelta:
	case protocol.EventToolCallRequested:
		key, summary := toolCallKeyAndSummary(event.Data)
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: "⏳ " + summary, Key: key}}
	case protocol.EventToolCallFinished:
		key, summary := toolResultSummary(snapshot, event.Data)
		if summary == "" {
			return nil
		}
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: summary, Key: key}}
	case protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		key, summary := toolFailureSummary(event)
		if summary == "" {
			return nil
		}
		return []RenderEvent{{Kind: "tool", Title: "Tool", Body: summary, Key: key}}
	case protocol.EventContextThreshold:
		return []RenderEvent{{Kind: "status", Title: "Context", Body: telegramContextThresholdText(event.Data), Key: telegramContextThresholdKey(event.Data)}}
	case protocol.EventStreamStillRunning:
		return []RenderEvent{{Kind: "status", Title: "Still Running", Body: telegramStillRunningText(event.Data), Key: "stream.still_running"}}
	case protocol.EventTurnChangeRecorded:
		if change, ok := toolrender.DecodeTurnChange(event.Data); ok {
			return []RenderEvent{{Kind: "status", Title: "Changes", Body: toolrender.TurnChangeSummary(change), Key: change.ChangeID}}
		}
	case protocol.EventTurnChangeReverted:
		if change, ok := toolrender.DecodeTurnChange(event.Data); ok {
			return []RenderEvent{{Kind: "status", Title: "Reverted", Body: toolrender.TurnChangeSummary(change), Key: change.ChangeID}}
		}
	case protocol.EventUserInputRequested:
		if req, ok := protocol.DecodeUserInputRequest(event.Data); ok {
			return []RenderEvent{{Kind: "status", Title: "Question", Body: telegramUserInputQuestionText(req), Key: req.RequestID}}
		}
	case protocol.EventUserInputAnswered:
		if answer, ok := protocol.DecodeUserInputAnswer(event.Data); ok {
			return []RenderEvent{{Kind: "status", Title: "Question", Body: "answered", Key: answer.RequestID}}
		}
	case protocol.EventUserInputRejected:
		if reject, ok := protocol.DecodeUserInputReject(event.Data); ok {
			body := "rejected"
			if reject.Reason != "" {
				body += ": " + reject.Reason
			}
			return []RenderEvent{{Kind: "status", Title: "Question", Body: body, Key: reject.RequestID}}
		}
	case protocol.EventProviderUsageUpdate:
	case protocol.EventRunCompleted:
	case protocol.EventRunFailed:
		errText := fmt.Sprint(event.Data)
		if errText != "" && errText != previousError {
			return []RenderEvent{{Kind: "error", Title: "Error", Body: errText}}
		}
	}
	return nil
}

func (r *Renderer) applySnapshot(snapshot projector.Snapshot) {
	r.Content.Reset()
	r.Content.WriteString(snapshot.AssistantText)
	r.ThinkingChars = len(snapshot.ReasoningText)
	r.ModelCalls = snapshot.ModelCalls
	r.ToolCalls = snapshot.ToolCalls
	r.InputTokens = snapshot.InputTokens
	r.OutputTokens = snapshot.OutputTokens
	r.CacheHit = snapshot.CacheHitTokens
	r.CacheMiss = snapshot.CacheMissTokens
	r.Reasoning = snapshot.ReasoningTokens
	r.LastInputTokens = snapshot.LastInputTokens
	r.LastOutputTokens = snapshot.LastOutputTokens
	r.LastCacheHit = snapshot.LastCacheHitTokens
	r.LastCacheMiss = snapshot.LastCacheMissTokens
	r.ToolSummaryIn = snapshot.ToolSummaryInputTokens
	r.ToolSummaryOut = snapshot.ToolSummaryOutputTokens
	r.ToolSummaryAPI = snapshot.ToolSummaryAPITokens
	r.HelperModelIn = snapshot.HelperModelInputTokens
	r.HelperModelOut = snapshot.HelperModelOutputTokens
	r.HelperModelHit = snapshot.HelperModelCacheHitTokens
	r.HelperModelMiss = snapshot.HelperModelCacheMissTokens
	r.HelperModelAPI = snapshot.HelperModelAPITokens
	r.HelperAPICalls = snapshot.HelperAPICalls
	r.HelperCostUSD = snapshot.HelperCostUSD
	r.LastError = snapshot.LastError
	r.Done = snapshot.RunState == projector.RunStateCompleted
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
	content := strings.TrimSpace(r.assistantText())
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
	return DefaultRichStream().FinalChunks(r, model, reasoning)
}

func (r *Renderer) LiveRichMarkdownPreview(model, reasoning string) string {
	return DefaultRichStream().LivePreview(r, model, reasoning)
}

func (r *Renderer) assistantText() string {
	if r == nil {
		return ""
	}
	if r.projector != nil {
		if text := r.projector.Snapshot().AssistantText; text != "" {
			return text
		}
	}
	return r.Content.String()
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
	}
	if r.HelperModelIn+r.HelperModelOut > 0 {
		parts = append(parts, fmt.Sprintf("helper %s→%s", compactInt(r.HelperModelIn), compactInt(r.HelperModelOut)))
	}
	if r.ToolSummaryIn+r.ToolSummaryOut > 0 || r.HelperModelAPI > 0 {
		parts = append(parts, fmt.Sprintf("sumapi %s", compactInt(r.HelperModelAPI)))
	}
	if r.HelperAPICalls > 0 {
		parts = append(parts, fmt.Sprintf("api calls %d", r.HelperAPICalls))
	}
	if r.HelperCostUSD > 0 {
		parts = append(parts, fmt.Sprintf("api cost $%.4f", r.HelperCostUSD))
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

func toolResultSummary(snapshot projector.Snapshot, data any) (string, string) {
	summary, ok := toolrender.ResultSummaryFor(data, "", toolrender.StyleTelegram)
	if !ok || summary.Key == "" {
		return "", ""
	}
	summary, ok = toolrender.ResultSummaryFor(data, telegramToolBase(snapshot.ToolsByCallID[summary.Key]), toolrender.StyleTelegram)
	if !ok {
		return "", ""
	}
	return summary.Key, summary.Line
}

func telegramToolBase(item projector.ToolItem) string {
	if strings.TrimSpace(item.Call.Name) != "" {
		return toolrender.CallLine(item.Call, toolrender.StyleTelegram)
	}
	if strings.TrimSpace(item.Name) != "" {
		return item.Name
	}
	return ""
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

func telegramUserInputQuestionText(req protocol.UserInputRequestEvent) string {
	var lines []string
	for i, question := range req.Questions {
		if question.Header != "" && (i == 0 || question.Header != req.Questions[i-1].Header) {
			lines = append(lines, question.Header)
		}
		lines = append(lines, question.Question)
		for _, option := range question.Options {
			line := option.Label
			if option.Description != "" {
				line += " - " + option.Description
			}
			lines = append(lines, line)
		}
		if question.AllowFreeform {
			lines = append(lines, "Freeform answer accepted.")
		}
	}
	if len(lines) == 0 {
		return "The agent is asking for input."
	}
	return toolrender.CompactText(strings.Join(lines, "\n"), 900)
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
