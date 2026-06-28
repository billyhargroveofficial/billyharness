package telegrambot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
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
	}
}

func TestRendererContextShowsLastModelCallNotCumulativeSpend(t *testing.T) {
	r := NewRendererWithContextWindow(10_000)
	r.Apply(protocol.Event{Type: protocol.EventRunStarted})
	r.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":  1000,
		"output_tokens": 100,
	}})
	r.Apply(protocol.Event{Type: protocol.EventModelCallStarted})
	r.Apply(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":  1300,
		"output_tokens": 200,
	}})

	footer := r.footerLine()
	for _, want := range []string{"📥 2.3k 📤 300", "🪟 ctx 1.5k/10.0k 15%"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q: %q", want, footer)
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
	for _, want := range []string{"⚡ Billyharness · Running", "Looking it up...", "Tools running", "• 🌐 web_search Moscow weather"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<b>") || strings.Contains(text, "&lt;") {
		t.Fatalf("stream text should stay plain, got:\n%s", text)
	}
}

func TestStreamPlainTextKeepsToolsVisibleWhenContentIsLong(t *testing.T) {
	renderer := NewRenderer()
	renderer.Content.WriteString(strings.Repeat("old content ", 900))
	renderer.Content.WriteString("fresh tail")
	progress := NewToolProgress()
	_ = progress.Add(RenderEvent{Kind: "tool", Title: "Tool", Body: "🌐 web_fetch example.com/forecast", Key: "fetch"})

	text := renderer.StreamPlainText("deepseek-v4-flash", "high", progress)
	if got := telegramUTF16Len(text); got > telegramLimit {
		t.Fatalf("stream text exceeds telegram limit: %d", got)
	}
	for _, want := range []string{"…", "fresh tail", "Tools running", "🌐 web_fetch example.com/forecast"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream text missing %q:\n%s", want, text)
		}
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
