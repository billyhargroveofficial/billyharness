package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

func renderContent(out callToolResult, limit int) string {
	limit = mcpToolOutputLimit(limit)
	var b strings.Builder
	omitted := 0
	appendPart := func(text string) {
		if text == "" {
			return
		}
		if b.Len() > 0 {
			if omitted > 0 {
				omitted++
			} else {
				omitted += appendLimitedUTF8(&b, "\n", limit)
			}
		}
		if omitted > 0 {
			omitted += len(text)
			return
		}
		omitted += appendLimitedUTF8(&b, text, limit)
	}
	for _, item := range out.Content {
		if item["type"] == "text" {
			if text, ok := item["text"].(string); ok {
				appendPart(text)
				continue
			}
		}
		bytes, _ := json.Marshal(item)
		if len(bytes) > 0 {
			appendPart(string(bytes))
		}
	}
	if b.Len() == 0 && omitted == 0 && out.StructuredContent != nil {
		bytes, _ := json.MarshalIndent(out.StructuredContent, "", "  ")
		return truncateMCPOutput(string(bytes), limit)
	}
	return withMCPTruncationNote(b.String(), omitted)
}

func mcpToolOutputLimit(limit int) int {
	if limit <= 0 {
		return defaultMCPToolOutputBytes
	}
	return limit
}

func truncateMCPOutput(text string, limit int) string {
	limit = mcpToolOutputLimit(limit)
	if len(text) <= limit {
		return text
	}
	trimmed := trimUTF8Bytes(text, limit)
	return withMCPTruncationNote(trimmed, len(text)-len(trimmed))
}

func withMCPTruncationNote(text string, omitted int) string {
	if omitted <= 0 {
		return text
	}
	return text + fmt.Sprintf("\n...[truncated %d bytes from MCP tool output]", omitted)
}

func appendLimitedUTF8(b *strings.Builder, text string, limit int) int {
	if limit <= 0 {
		limit = defaultMCPToolOutputBytes
	}
	remaining := limit - b.Len()
	if remaining <= 0 {
		return len(text)
	}
	if len(text) <= remaining {
		b.WriteString(text)
		return 0
	}
	trimmed := trimUTF8Bytes(text, remaining)
	b.WriteString(trimmed)
	return len(text) - len(trimmed)
}

func trimUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	text = text[:maxBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
