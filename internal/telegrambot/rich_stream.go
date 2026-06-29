package telegrambot

import (
	"strings"
	"time"
)

const telegramRichPreviewLimit = telegramLiveProgressLimit

type RichStream struct {
	LiveLimit  int
	FinalLimit int
}

func DefaultRichStream() RichStream {
	return RichStream{
		LiveLimit:  telegramRichPreviewLimit,
		FinalLimit: telegramRichLimit,
	}
}

func (s RichStream) LivePreview(renderer *Renderer, model, reasoning string) string {
	chunks := s.markdownChunks(renderer, model, reasoning, s.liveLimit(), false)
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0]
}

func (s RichStream) FinalChunks(renderer *Renderer, model, reasoning string) []string {
	return s.markdownChunks(renderer, model, reasoning, s.finalLimit(), true)
}

func (s RichStream) liveLimit() int {
	if s.LiveLimit > 0 {
		return s.LiveLimit
	}
	return telegramRichPreviewLimit
}

func (s RichStream) finalLimit() int {
	if s.FinalLimit > 0 {
		return s.FinalLimit
	}
	return telegramRichLimit
}

func (s RichStream) markdownChunks(renderer *Renderer, model, reasoning string, limit int, includeFallback bool) []string {
	if renderer == nil {
		return nil
	}
	elapsed := time.Since(renderer.Started).Round(time.Second)
	content := strings.TrimSpace(renderer.assistantText())
	if content == "" {
		if !includeFallback {
			return nil
		}
		content = "Working..."
	}
	header := renderer.richHeaderInline(model, reasoning, elapsed)
	footer := "\n\n_" + markdownInlineEscape(renderer.footerLine()) + "_"
	budget := limit - telegramUTF16Len(header) - telegramUTF16Len(footer) - 128
	if budget < 1 {
		budget = 1
	}
	parts := splitRichMarkdown(content, budget)
	if len(parts) == 0 && includeFallback {
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
