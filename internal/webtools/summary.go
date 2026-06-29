package webtools

import (
	"context"
	"strings"
	"time"
)

const SummarySystemPrompt = "You produce compact factual web-page summaries for another coding agent. Return only the summary text. Preserve concrete dates, numbers, names, and source-specific caveats. Do not include raw page dumps."

type SummarySource struct {
	URL   string
	Title string
	Query string
	Text  string
}

type SummaryRequest struct {
	RequestID       string
	ToolName        string
	Source          SummarySource
	Provider        string
	Model           string
	MaxInputTokens  int
	MaxOutputTokens int
	Timeout         time.Duration
	AllowTools      bool
}

type SummaryResult struct {
	Text         string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CacheHit     int64
	CacheMiss    int64
	CostUSD      float64
}

type Summarizer interface {
	SummarizeWeb(context.Context, SummaryRequest) (SummaryResult, error)
}

func SummaryInput(text string, maxTokens int) (string, bool) {
	if maxTokens <= 0 {
		maxTokens = 12_000
	}
	if maxTokens > 24_000 {
		maxTokens = 24_000
	}
	return TruncateRunesWithMarker(strings.TrimSpace(text), maxTokens*4)
}

func SummaryPrompt(src SummarySource, text string, truncated bool) string {
	var b strings.Builder
	b.WriteString("URL: " + strings.TrimSpace(src.URL) + "\n")
	if strings.TrimSpace(src.Title) != "" {
		b.WriteString("Title: " + strings.TrimSpace(src.Title) + "\n")
	}
	if strings.TrimSpace(src.Query) != "" {
		b.WriteString("Focus query: " + strings.TrimSpace(src.Query) + "\n")
	}
	if truncated {
		b.WriteString("Input note: extracted text was capped before summarization.\n")
	}
	b.WriteString("\nSummarize this extracted page text in 4-8 compact sentences. Avoid filler and do not invent facts.\n\n")
	b.WriteString(text)
	return b.String()
}

func NormalizeSummaryOutput(text string, maxTokens int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if maxTokens <= 0 {
		maxTokens = 700
	}
	maxChars := max(400, min(1400, maxTokens*4))
	out, _ := TruncateRunesWithMarker(text, maxChars)
	return strings.TrimSpace(out)
}

func EstimateTokens(text string) int {
	return EstimateTokensByChars(len([]rune(text)))
}

func EstimateTokensByChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func TruncateRunesWithMarker(text string, maxRunes int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text, false
	}
	if maxRunes < 32 {
		maxRunes = 32
	}
	return string(runes[:maxRunes]) + "\n...[truncated; call web_fetch with full_text=true only if exact full page text is required]", true
}
