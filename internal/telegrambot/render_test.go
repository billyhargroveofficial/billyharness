package telegrambot

import (
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

func TestRendererFinalRichMarkdownPreservesRichMarkdown(t *testing.T) {
	r := NewRenderer()
	r.Apply(protocol.Event{Type: protocol.EventAssistantDelta, Data: `## Погода

| Параметр | Значение |
|---|---|
| Температура | +18°C |
| Ветер | 6 м/с |

- Облачно
- Без сильного дождя`})
	r.Apply(protocol.Event{Type: protocol.EventRunCompleted})

	chunks := r.FinalRichMarkdownChunks("deepseek-v4-flash", "high")
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	got := chunks[0]
	for _, want := range []string{"**✅ Billyharness · Done**", "_🧬 deepseek-v4-flash · 🧠 high", "## Погода", "| Параметр | Значение |", "- Облачно", "_⚡ streaming_"} {
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
