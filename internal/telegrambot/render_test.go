package telegrambot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestRendererFinalChunksAreTelegramSizedAndEscaped(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: "**bold** " + strings.Repeat("<tag>&", 900)})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      1200,
		"output_tokens":     300,
		"cache_hit_tokens":  700,
		"cache_miss_tokens": 500,
	}})

	chunks := r.FinalChunks("deepseek-v4-flash", "high")
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want split", len(chunks))
	}
	for _, chunk := range chunks {
		if telegramUTF16Len(chunk) > telegramLimit {
			t.Fatalf("chunk exceeds Telegram limit: %d", telegramUTF16Len(chunk))
		}
		if strings.Contains(chunk, "<tag>") {
			t.Fatalf("chunk was not escaped: %q", chunk[:80])
		}
	}
	if !strings.Contains(chunks[0], "<b>bold</b>") {
		t.Fatalf("markdown bold was not rendered: %q", chunks[0][:120])
	}
	if !strings.Contains(chunks[len(chunks)-1], "💾 hit") {
		t.Fatalf("last chunk missing footer: %q", chunks[len(chunks)-1])
	}
}

func TestGoldenTraceRendersTelegram(t *testing.T) {
	r := NewRenderer()
	var progress []RenderEvent
	for _, event := range goldenTraceEvents(t) {
		progress = append(progress, r.Apply(event)...)
	}
	if !r.Done || r.ModelCalls != 2 || r.ToolCalls != 2 {
		t.Fatalf("renderer state done=%v model=%d tools=%d", r.Done, r.ModelCalls, r.ToolCalls)
	}
	if r.InputTokens != 2100 || r.OutputTokens != 135 || r.CacheHit != 1100 || r.CacheMiss != 1000 || r.Reasoning != 20 {
		t.Fatalf("usage = input %d output %d hit %d miss %d reasoning %d", r.InputTokens, r.OutputTokens, r.CacheHit, r.CacheMiss, r.Reasoning)
	}
	progressText := renderEventsText(progress)
	for _, want := range []string{"web_fetch", "mcp call", "ref web_fetch.txt", "Context"} {
		if !strings.Contains(progressText, want) {
			t.Fatalf("progress missing %q in:\n%s", want, progressText)
		}
	}
	chunks := r.FinalChunks("deepseek-v4-flash", "high")
	if len(chunks) == 0 {
		t.Fatal("final chunks empty")
	}
	finalText := strings.Join(chunks, "\n")
	for _, want := range []string{"Final answer: web context", "agent turns 2", "tools 2", "💾 hit"} {
		if !strings.Contains(finalText, want) {
			t.Fatalf("final output missing %q in:\n%s", want, finalText)
		}
	}
}

func TestMarkdownToTelegramHTMLSupportsTelegramSubset(t *testing.T) {
	html := markdownToTelegramHTML(`# Заголовок

Привет **жирный** и *курсив*, ` + "`код`" + `, [ссылка](https://example.com?a=1&b=2), <b>сырой</b>.

> цитата **важная**

- первый
- второй с ` + "`кодом`" + `

| Параметр | Значение |
| --- | --- |
| **Температура** | +19 °C |
| Формула | $$\frac{\bar X - \mu}{\sigma/\sqrt n} \approx N(0,1)$$ |

` + "```go\nfmt.Println(\"<hi>\")\n```")

	for _, want := range []string{
		"<b>Заголовок</b>",
		"<b>жирный</b>",
		"<i>курсив</i>",
		"<code>код</code>",
		`<a href="https://example.com?a=1&amp;b=2">ссылка</a>`,
		"&lt;b&gt;сырой&lt;/b&gt;",
		"<blockquote>цитата <b>важная</b></blockquote>",
		"• первый",
		"• второй с <code>кодом</code>",
		"• <b>Температура</b>: +19 °C",
		"• Формула: $$\\frac{\\bar X - \\mu}{\\sigma/\\sqrt n} \\approx N(0,1)$$",
		"<pre>fmt.Println(&#34;&lt;hi&gt;&#34;)</pre>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("telegram HTML missing %q in:\n%s", want, html)
		}
	}
	for _, bad := range []string{"| --- |", "| Параметр |", "<b>сырой</b>"} {
		if strings.Contains(html, bad) {
			t.Fatalf("telegram HTML should not contain %q:\n%s", bad, html)
		}
	}
}

func goldenTraceEvents(t *testing.T) []protocol.Event {
	t.Helper()
	records := testkit.ReadTraceRecords(t, testkit.CanonicalAgentLoopTracePath(t))
	events := make([]protocol.Event, 0, len(records))
	for _, record := range records {
		var event protocol.Event
		if err := json.Unmarshal(record.Event, &event); err != nil {
			t.Fatalf("decode event seq %d: %v", record.Seq, err)
		}
		events = append(events, event)
	}
	return events
}

func renderEventsText(events []RenderEvent) string {
	var b strings.Builder
	for _, event := range events {
		b.WriteString(event.Title)
		b.WriteString(" ")
		b.WriteString(event.Body)
		b.WriteString("\n")
	}
	return b.String()
}

func TestRendererFinalChunksConvertTablesInHTMLFallback(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: `| Параметр | Значение |
| --- | --- |
| **Температура** | +19 °C |
| Ветер | 4 м/с |`})
	r.Apply(protocol.Event{Type: protocol.EventRunCompleted})

	chunks := r.FinalChunks("deepseek-v4-flash", "high")
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	got := chunks[0]
	for _, want := range []string{"• <b>Температура</b>: +19 °C", "• Ветер: 4 м/с"} {
		if !strings.Contains(got, want) {
			t.Fatalf("final HTML fallback missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"| --- |", "| Параметр |"} {
		if strings.Contains(got, bad) {
			t.Fatalf("final HTML fallback should not contain table markdown %q:\n%s", bad, got)
		}
	}
}

func TestRendererProviderUsageDeduplicatesCumulativeSnapshots(t *testing.T) {
	r := NewRendererWithContextWindow(1000)
	r.Apply(protocol.Event{Type: protocol.EventRunStarted})
	r.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      200,
		"output_tokens":     40,
		"cache_hit_tokens":  150,
		"cache_miss_tokens": 50,
		"reasoning_tokens":  8,
	}})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      200,
		"output_tokens":     40,
		"cache_hit_tokens":  150,
		"cache_miss_tokens": 50,
		"reasoning_tokens":  8,
	}})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      230,
		"output_tokens":     45,
		"cache_hit_tokens":  160,
		"cache_miss_tokens": 70,
		"reasoning_tokens":  9,
	}})

	if r.InputTokens != 230 || r.OutputTokens != 45 || r.CacheHit != 160 || r.CacheMiss != 70 || r.Reasoning != 9 {
		t.Fatalf("usage totals = in:%d out:%d hit:%d miss:%d reasoning:%d",
			r.InputTokens, r.OutputTokens, r.CacheHit, r.CacheMiss, r.Reasoning)
	}
	if r.LastInputTokens != 230 || r.LastOutputTokens != 45 {
		t.Fatalf("last context usage = in:%d out:%d", r.LastInputTokens, r.LastOutputTokens)
	}
	if footer := r.footerLine(); !strings.Contains(footer, "🪟 ctx 275/1.0k 28%") {
		t.Fatalf("footer missing context usage: %q", footer)
	} else if strings.Contains(footer, "🧠 9") {
		t.Fatalf("footer should not show cumulative reasoning tokens: %q", footer)
	}
}

func TestRendererContextShowsLastModelCallNotCumulativeSpend(t *testing.T) {
	r := NewRendererWithContextWindow(10_000)
	r.Apply(protocol.Event{Type: protocol.EventRunStarted})
	r.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      1000,
		"output_tokens":     100,
		"cache_hit_tokens":  900,
		"cache_miss_tokens": 100,
	}})
	r.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      1300,
		"output_tokens":     200,
		"cache_hit_tokens":  1100,
		"cache_miss_tokens": 200,
	}})

	footer := r.footerLine()
	for _, want := range []string{"🪟 ctx 1.5k/10.0k 15%", "💾 hit 1.1k miss 200"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q: %q", want, footer)
		}
	}
	if strings.Contains(footer, "hit 2.0k") || strings.Contains(footer, "miss 300") {
		t.Fatalf("footer should not show cumulative cache totals: %q", footer)
	}
	if strings.Contains(footer, "📥") || strings.Contains(footer, "📤") {
		t.Fatalf("footer should not show cumulative token spend: %q", footer)
	}
}

func TestRendererFooterShowsChatTotalsForTurnsAndTools(t *testing.T) {
	r := NewRendererWithContextWindowAndTotals(10_000, 7, 9)
	r.Apply(protocol.Event{Type: protocol.EventRunStarted})
	r.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	r.Apply(protocol.Event{Type: protocol.EventAssistantReasoning, Data: strings.Repeat("hidden ", 400)})
	r.Apply(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{Name: "web_search", Arguments: []byte(`{"query":"one"}`)}})
	r.Apply(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{Name: "web_fetch", Arguments: []byte(`{"url":"https://example.com"}`)}})

	footer := r.footerLine()
	for _, want := range []string{"🔁 agent turns 8", "🛠 tools 11"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q: %q", want, footer)
		}
	}
	for _, bad := range []string{"🤖 LLM", "💭", "hidden"} {
		if strings.Contains(footer, bad) {
			t.Fatalf("footer should not contain %q: %q", bad, footer)
		}
	}
}

func TestStreamPlainTextShowsContextAboveProgress(t *testing.T) {
	renderer := NewRendererWithContextWindow(10_000)
	renderer.Apply(protocol.Event{Type: protocol.EventRunStarted})
	renderer.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	renderer.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      5700,
		"output_tokens":     300,
		"cache_hit_tokens":  5000,
		"cache_miss_tokens": 700,
	}})
	progress := NewToolProgress()
	_ = progress.Add(RenderEvent{Kind: "tool", Title: "Tool", Body: "🔨 mcp call read_history", Key: "read"})

	text := renderer.StreamPlainText("deepseek-v4-pro", "max", progress)
	for _, want := range []string{"🪟 ctx 6.0k/10.0k 60%", "Tools running", "💾 hit 5.0k miss 700"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "📥") || strings.Contains(text, "📤") {
		t.Fatalf("stream text should not show cumulative token spend:\n%s", text)
	}
	ctxPos := strings.Index(text, "🪟 ctx")
	toolsPos := strings.Index(text, "Tools running")
	if ctxPos < 0 || toolsPos < 0 || ctxPos > toolsPos {
		t.Fatalf("context should appear above tool progress:\n%s", text)
	}
	if strings.Count(text, "🪟 ctx") != 1 {
		t.Fatalf("running stream should show context once:\n%s", text)
	}
}

func TestRendererContextThresholdAddsProgressLine(t *testing.T) {
	r := NewRendererWithContextWindow(1_000_000)
	events := r.Apply(protocol.Event{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
		Percent:             70,
		EstimatedTokens:     705000,
		ContextWindowTokens: 1000000,
		Stage:               "after_tool_results",
	}})
	if len(events) != 1 || events[0].Kind != "status" || events[0].Key != "context-threshold-70" {
		t.Fatalf("render events = %#v", events)
	}
	progress := NewToolProgress()
	if !progress.Add(events[0]) {
		t.Fatalf("expected progress line")
	}
	if progress.Add(events[0]) {
		t.Fatalf("duplicate threshold should not change progress")
	}
	text := progress.PlainTextLimit(1000)
	for _, want := range []string{"context 70%", "705.0k/1.00M", "after_tool_results"} {
		if !strings.Contains(text, want) {
			t.Fatalf("progress missing %q: %q", want, text)
		}
	}
}

func TestRendererFooterShowsToolSummaryTokens(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		Name:    "web_fetch",
		Content: `{"summary":"compact"}`,
		Metadata: map[string]any{
			"tool_summary_input_tokens":        int64(20000),
			"tool_summary_output_tokens":       int64(900),
			"tool_summary_api_total_tokens":    int64(0),
			"tool_summary_external_model_used": false,
		},
	}})

	footer := r.footerLine()
	for _, want := range []string{"🧩 websum 20.0k→900", "sumapi 0"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q: %q", want, footer)
		}
	}
}

func TestRendererFinalRichMarkdownPreservesRichMarkdown(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: `## Погода

| Параметр | Значение |
|---|---|
| Температура | +18°C |
| Ветер | 6 м/с |

- Облачно
- Без сильного дождя

Формула: $$\frac{\bar X - \mu}{\sigma/\sqrt n} \approx N(0,1)$$`})
	r.Apply(protocol.Event{Type: protocol.EventRunCompleted})

	chunks := r.FinalRichMarkdownChunks("deepseek-v4-flash", "high")
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	got := chunks[0]
	for _, want := range []string{"**✅ Billyharness · Done**", "_🧬 deepseek-v4-flash · 🧠 high", "## Погода", "| Параметр | Значение |", "- Облачно", "$$\\frac{\\bar X - \\mu}{\\sigma/\\sqrt n} \\approx N(0,1)$$", "_⚡ streaming_"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rich markdown missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "| 🧬 Model |") || strings.Contains(got, "| 🤖 Calls |") {
		t.Fatalf("metadata should stay inline, not render as tables:\n%s", got)
	}
	if strings.Contains(got, "&lt;") || strings.Contains(got, "<b>") {
		t.Fatalf("rich markdown should not be HTML escaped/rendered:\n%s", got)
	}
}

func TestRendererFinalRichMarkdownSplitsOversizedCodeBlock(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: "```go\n" + strings.Repeat("fmt.Println(\"🙂 telegram-safe\")\n", 3000) + "```"})
	r.Apply(protocol.Event{Type: protocol.EventRunCompleted})

	chunks := r.FinalRichMarkdownChunks("deepseek-v4-flash", "high")
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want split", len(chunks))
	}
	for _, chunk := range chunks {
		if got := telegramUTF16Len(chunk); got > telegramRichLimit {
			t.Fatalf("chunk exceeds Telegram rich limit: %d", got)
		}
		if !strings.Contains(chunk, "```go\n") {
			t.Fatalf("chunk missing reopened code fence:\n%s", chunk[:min(len(chunk), 200)])
		}
		if count := strings.Count(chunk, "```"); count != 2 {
			t.Fatalf("chunk has %d code fences, want 2", count)
		}
	}
}

func TestRendererFinalRichMarkdownSplitsOversizedTableByRows(t *testing.T) {
	var table strings.Builder
	table.WriteString("| Row | Value |\n|---|---|\n")
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&table, "| %04d | %s |\n", i, strings.Repeat("🙂", 8))
	}
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: table.String()})
	r.Apply(protocol.Event{Type: protocol.EventRunCompleted})

	chunks := r.FinalRichMarkdownChunks("deepseek-v4-flash", "high")
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want split", len(chunks))
	}
	for _, chunk := range chunks {
		if got := telegramUTF16Len(chunk); got > telegramRichLimit {
			t.Fatalf("chunk exceeds Telegram rich limit: %d", got)
		}
		if !strings.Contains(chunk, "| Row | Value |") || !strings.Contains(chunk, "|---|---|") {
			t.Fatalf("chunk missing repeated table header:\n%s", chunk[:min(len(chunk), 200)])
		}
	}
}

func TestRichStreamUsesSeparateLiveAndFinalLimits(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: "## Live preview\n\n" + strings.Repeat("rich stream payload ", 300)})
	r.Apply(protocol.Event{Type: protocol.EventRunCompleted})

	stream := RichStream{LiveLimit: 700, FinalLimit: 1600}
	preview := stream.LivePreview(r, "deepseek-v4-flash", "high")
	if preview == "" {
		t.Fatal("live rich preview is empty")
	}
	if got := telegramUTF16Len(preview); got > stream.LiveLimit {
		t.Fatalf("live preview length = %d, want <= %d", got, stream.LiveLimit)
	}
	final := stream.FinalChunks(r, "deepseek-v4-flash", "high")
	if len(final) < 2 {
		t.Fatalf("final chunks = %d, want split under smaller final limit", len(final))
	}
	for _, chunk := range final {
		if got := telegramUTF16Len(chunk); got > stream.FinalLimit {
			t.Fatalf("final chunk length = %d, want <= %d", got, stream.FinalLimit)
		}
	}
	if !strings.Contains(preview, "## Live preview") || !strings.Contains(final[0], "## Live preview") {
		t.Fatalf("rich markdown heading missing preview=%q final=%q", preview, final[0])
	}
}

func TestRichStreamLivePreviewIsOptionalWithoutAssistantContent(t *testing.T) {
	r := NewRenderer()
	stream := RichStream{LiveLimit: 700, FinalLimit: 1600}
	if preview := stream.LivePreview(r, "deepseek-v4-flash", "high"); preview != "" {
		t.Fatalf("preview = %q, want empty before assistant content", preview)
	}
	final := stream.FinalChunks(r, "deepseek-v4-flash", "high")
	if len(final) != 1 || !strings.Contains(final[0], "Working...") {
		t.Fatalf("final fallback chunks = %#v", final)
	}
}

func TestToolProgressDeduplicatesToolLines(t *testing.T) {
	progress := NewToolProgress()
	event := RenderEvent{Kind: "tool", Title: "Tool", Body: "Searched web: Moscow weather"}
	if !progress.Add(event) {
		t.Fatal("first Add returned false")
	}
	if progress.Add(event) {
		t.Fatal("duplicate Add returned true")
	}
	html := progress.HTML()
	if strings.Contains(html, "blockquote") {
		t.Fatalf("tool progress should avoid blockquote for Telegram compatibility: %q", html)
	}
	if strings.Count(html, "Searched web") != 1 {
		t.Fatalf("tool line was not deduped: %q", html)
	}
}

func TestStreamPlainTextEmbedsToolProgress(t *testing.T) {
	renderer := NewRenderer()
	renderer.Content.WriteString("Looking it up...")
	progress := NewToolProgress()
	if !progress.Add(RenderEvent{Kind: "tool", Title: "Tool", Body: "🌐 web_search Moscow weather", Key: "search"}) {
		t.Fatal("tool progress add returned false")
	}

	text := renderer.StreamPlainText("deepseek-v4-flash", "high", progress)
	for _, want := range []string{"Billyharness · Running", "Looking it up...", "Tools running", "• 🌐 web_search Moscow weather"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<b>") || strings.Contains(text, "&lt;") {
		t.Fatalf("stream text should stay plain, got:\n%s", text)
	}
}

func TestRendererSeparatesAssistantContentAcrossModelTurns(t *testing.T) {
	renderer := NewRenderer()
	renderer.Apply(protocol.Event{Type: protocol.EventModelCallStarted, TurnID: "turn-001"})
	renderer.Apply(protocol.Event{Type: protocol.EventAssistantDelta, TurnID: "turn-001", Data: "first turn."})
	renderer.Apply(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call_1", Name: "time_now", Arguments: []byte(`{}`)}})
	renderer.Apply(protocol.Event{Type: protocol.EventModelCallStarted, TurnID: "turn-002"})
	renderer.Apply(protocol.Event{Type: protocol.EventAssistantDelta, TurnID: "turn-002", Data: "Second turn."})

	content := renderer.Content.String()
	if !strings.Contains(content, "first turn.\n\nSecond turn.") {
		t.Fatalf("assistant turns should be separated, got %q", content)
	}
	if strings.Contains(content, "turn.Second") {
		t.Fatalf("assistant turns were glued together: %q", content)
	}
}

func TestRendererSeparatesAssistantContentAcrossToolBoundaries(t *testing.T) {
	renderer := NewRenderer()
	progress := NewToolProgress()
	events := []protocol.Event{
		{Type: protocol.EventModelCallStarted, TurnID: "turn-001"},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-001", Data: "before tool."},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call_search", Name: "web_search", Arguments: []byte(`{"query":"weather"}`)}},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-001", Data: "after first tool."},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call_fetch", Name: "web_fetch", Arguments: []byte(`{"url":"https://example.com"}`)}},
		{Type: protocol.EventAssistantDelta, TurnID: "turn-001", Data: "after second tool."},
	}
	for _, event := range events {
		for _, rendered := range renderer.Apply(event) {
			if rendered.Kind == "tool" {
				progress.Add(rendered)
			}
		}
	}

	content := renderer.Content.String()
	wantText := "before tool.\n\nafter first tool.\n\nafter second tool."
	if content != wantText {
		t.Fatalf("assistant content = %q, want %q", content, wantText)
	}
	live := renderer.StreamPlainText("deepseek-v4-flash", "high", progress)
	final := strings.Join(renderer.FinalChunks("deepseek-v4-flash", "high"), "\n")
	for _, body := range []string{live, final} {
		if !strings.Contains(body, "before tool.\n\nafter first tool.\n\nafter second tool.") {
			t.Fatalf("assistant text not separated in output:\n%s", body)
		}
		for _, wantTool := range []string{"web_search", "web_fetch"} {
			if !strings.Contains(body, wantTool) && body == live {
				t.Fatalf("live progress missing tool %q:\n%s", wantTool, body)
			}
		}
	}
}

func TestRendererUsesProjectedAssistantTextForFinalAndLiveMessages(t *testing.T) {
	renderer := NewRenderer()
	renderer.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: "projected answer"})
	renderer.Content.Reset()

	final := strings.Join(renderer.FinalChunks("deepseek-v4-flash", "high"), "\n")
	if !strings.Contains(final, "projected answer") {
		t.Fatalf("final message should use projected assistant text:\n%s", final)
	}
	live := renderer.StreamPlainText("deepseek-v4-flash", "high", NewToolProgress())
	if !strings.Contains(live, "projected answer") {
		t.Fatalf("live message should use projected assistant text:\n%s", live)
	}
}

func TestStreamPlainTextShowsLastEventHeartbeat(t *testing.T) {
	renderer := NewRenderer()
	renderer.Content.WriteString("Writing...")
	renderer.LastEventAt = time.Now().Add(-17 * time.Second)
	renderer.LastEventType = protocol.EventAssistantDelta

	text := renderer.StreamPlainTextPulse("deepseek-v4-flash", "high", NewToolProgress(), 3)
	for _, want := range []string{"Billyharness · Running", "tokens ", " ago", "Writing..."} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing %q:\n%s", want, text)
		}
	}
}

func TestStreamPlainTextKeepsToolsVisibleWhenContentIsLong(t *testing.T) {
	renderer := NewRenderer()
	renderer.Content.WriteString(strings.Repeat("old content ", 900))
	renderer.Content.WriteString("fresh tail")
	progress := NewToolProgress()
	_ = progress.Add(RenderEvent{Kind: "tool", Title: "Tool", Body: "🌐 web_fetch example.com/forecast", Key: "fetch"})

	text := renderer.StreamPlainText("deepseek-v4-flash", "high", progress)
	if got := telegramUTF16Len(text); got > telegramLiveProgressLimit {
		t.Fatalf("stream text exceeds live progress limit: %d", got)
	}
	for _, want := range []string{"live tail", "fresh tail", "Tools running", "🌐 web_fetch example.com/forecast"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing %q:\n%s", want, text)
		}
	}
}

func TestStreamPlainTextTruncatesFromStartWhenToolProgressIsHuge(t *testing.T) {
	renderer := NewRenderer()
	renderer.Content.WriteString("initial answer that can be dropped")
	progress := NewToolProgress()
	for i := 0; i < 24; i++ {
		_ = progress.Add(RenderEvent{
			Kind: "tool",
			Key:  fmt.Sprintf("tool-%02d", i),
			Body: fmt.Sprintf("🌐 web_extract oldest=%02d %s newest-tool-%02d", i, strings.Repeat("payload ", 80), i),
		})
	}

	text := renderer.StreamPlainText("deepseek-v4-flash", "high", progress)
	if got := telegramUTF16Len(text); got > telegramLiveProgressLimit {
		t.Fatalf("stream text exceeds live progress limit: %d", got)
	}
	if !strings.Contains(text, "…[truncated]") {
		t.Fatalf("stream text should mark truncated progress:\n%s", text)
	}
	for _, want := range []string{"newest-tool-23", "Tools running"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing fresh suffix %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "newest-tool-00") {
		t.Fatalf("stream text kept stale beginning:\n%s", text)
	}
	if strings.HasSuffix(strings.TrimSpace(text), "...[truncated]") {
		t.Fatalf("stream text should not put truncated marker at the end:\n%s", text)
	}
}

func TestToolProgressUpdatesToolLineOnFinish(t *testing.T) {
	progress := NewToolProgress()
	renderer := NewRenderer()
	started := renderer.Apply(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call_fetch",
			Name:      "web_fetch",
			Arguments: []byte(`{"url":"https://example.com/a/really/long/path?query=secret"}`),
		},
	})
	if len(started) != 1 || !progress.Add(started[0]) {
		t.Fatalf("started = %#v", started)
	}
	finished := renderer.Apply(protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{
			CallID:    "call_fetch",
			Name:      "web_fetch",
			Content:   strings.Repeat("full payload ", 200),
			Truncated: true,
			OutputRef: "/root/billyharness/tool-output/20260627/123456-web_fetch-call_fetch-abcd.txt",
			Metadata: map[string]any{
				"estimated_text_tokens": int64(1800),
			},
		},
	})
	if len(finished) != 1 || !progress.Add(finished[0]) {
		t.Fatalf("finished = %#v", finished)
	}
	html := progress.HTML()
	if strings.Count(html, "•") != 1 || strings.Count(html, "🌐 web_fetch") != 1 {
		t.Fatalf("tool line should be updated in place, html=%q", html)
	}
	for _, want := range []string{"✅", "truncated", "123456-web_fetch-call_fetch-abcd.txt", "~1.8k tok"} {
		if !strings.Contains(html, want) {
			t.Fatalf("tool progress missing %q in %q", want, html)
		}
	}
	if strings.Contains(html, "full payload full payload") || strings.Contains(html, "query=secret") {
		t.Fatalf("tool progress leaked full payload or long URL: %q", html)
	}
}

func TestToolProgressUpdatesOutOfOrderByCallID(t *testing.T) {
	progress := NewToolProgress()
	renderer := NewRenderer()
	for _, call := range []protocol.ToolCall{
		{ID: "call-a", Name: "fs_read_file", Arguments: []byte(`{"path":"a.txt"}`)},
		{ID: "call-b", Name: "fs_read_file", Arguments: []byte(`{"path":"b.txt"}`)},
	} {
		for _, event := range renderer.Apply(protocol.Event{Type: protocol.EventToolCallRequested, Data: call}) {
			progress.Add(event)
		}
	}
	for _, result := range []protocol.ToolResult{
		{CallID: "call-b", Name: "fs_read_file", Content: "beta"},
		{CallID: "call-a", Name: "fs_read_file", Content: "alpha"},
	} {
		for _, event := range renderer.Apply(protocol.Event{Type: protocol.EventToolCallFinished, Data: result}) {
			progress.Add(event)
		}
	}
	html := progress.HTML()
	if strings.Count(html, "•") != 2 || strings.Count(html, "✅") != 2 || strings.Contains(html, "⏳") {
		t.Fatalf("tool lines should update in place, html=%q", html)
	}
	if !strings.Contains(html, "a.txt") || !strings.Contains(html, "b.txt") {
		t.Fatalf("tool lines lost call summaries: %q", html)
	}
}

func TestToolResultsDoNotRenderFullPayload(t *testing.T) {
	rendered := NewRenderer().Apply(protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: strings.Repeat(`{"tools":[{"name":"noisy"}]}`, 20),
	})
	if rendered != nil {
		t.Fatalf("tool result should not render full payload: %#v", rendered)
	}
}

func TestToolSummaryExtractsImportantArguments(t *testing.T) {
	event := protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{Name: "web_search", Arguments: []byte(`{"query":"telegram bot api"}`)},
	}
	rendered := NewRenderer().Apply(event)
	if len(rendered) != 1 {
		t.Fatalf("rendered = %#v", rendered)
	}
	if !strings.Contains(rendered[0].Body, "telegram bot api") {
		t.Fatalf("tool summary = %q", rendered[0].Body)
	}
}

func TestToolSummaryCompactsLongURL(t *testing.T) {
	event := protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{Name: "web_fetch", Arguments: []byte(`{"url":"https://example.com/some/really/long/path/that/should/not/eat/the/whole/telegram/message?with=a&lot=of&query=params"}`)},
	}
	rendered := NewRenderer().Apply(event)
	if len(rendered) != 1 {
		t.Fatalf("rendered = %#v", rendered)
	}
	body := rendered[0].Body
	if !strings.Contains(body, "🌐 web_fetch") || !strings.Contains(body, "example.com") {
		t.Fatalf("tool summary = %q", body)
	}
	if strings.Contains(body, "with=a&lot=of&query=params") {
		t.Fatalf("tool summary did not truncate query: %q", body)
	}
	if len([]rune(body)) > 120 {
		t.Fatalf("tool summary too long: %d %q", len([]rune(body)), body)
	}
}
