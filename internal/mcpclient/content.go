package mcpclient

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

func renderContent(out callToolResponse, limit int) string {
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
		appendPart(renderContentItemSummary(item))
	}
	if b.Len() == 0 && omitted == 0 && out.StructuredContent != nil {
		bytes, _ := json.MarshalIndent(out.StructuredContent, "", "  ")
		return truncateMCPOutput(string(bytes), limit)
	}
	return withMCPTruncationNote(b.String(), omitted)
}

func renderContentItemSummary(item map[string]any) string {
	typ, _ := item["type"].(string)
	typ = strings.TrimSpace(typ)
	if typ == "" {
		typ = "unknown"
	}
	var details []string
	for _, key := range []string{"mimeType", "mime_type", "uri", "name", "title"} {
		if value, ok := item[key].(string); ok && strings.TrimSpace(value) != "" {
			details = append(details, fmt.Sprintf("%s=%q", key, truncateMCPOutput(value, 160)))
		}
	}
	if resource, ok := item["resource"].(map[string]any); ok {
		for _, key := range []string{"uri", "mimeType", "mime_type", "name", "title"} {
			if value, ok := resource[key].(string); ok && strings.TrimSpace(value) != "" {
				details = append(details, fmt.Sprintf("resource.%s=%q", key, truncateMCPOutput(value, 160)))
			}
		}
	}
	if len(details) == 0 {
		return fmt.Sprintf("[MCP %s content omitted; see mcp_result_content metadata]", typ)
	}
	return fmt.Sprintf("[MCP %s content omitted; %s; see mcp_result_content metadata]", typ, strings.Join(details, " "))
}

func mcpToolResultMetadata(out callToolResponse, extraSecrets []string) map[string]any {
	metadata := map[string]any{
		"mcp_result_is_error":      out.IsError,
		"mcp_result_content_count": len(out.Content),
	}
	if types := mcpContentTypes(out.Content); len(types) > 0 {
		metadata["mcp_result_content_types"] = types
	}
	if len(out.Content) > 0 {
		items := make([]any, 0, len(out.Content))
		for _, item := range out.Content {
			items = append(items, item)
		}
		metadata["mcp_result_content"] = secrets.RedactValue(items, extraSecrets...)
	}
	if out.StructuredContent != nil {
		metadata["mcp_structured_content"] = secrets.RedactValue(out.StructuredContent, extraSecrets...)
	}
	if len(out.Meta) > 0 {
		metadata["mcp_result_meta"] = secrets.RedactValue(out.Meta, extraSecrets...)
	}
	return metadata
}

func mcpContentTypes(content []map[string]any) []string {
	seen := map[string]struct{}{}
	for _, item := range content {
		typ, _ := item["type"].(string)
		typ = strings.TrimSpace(typ)
		if typ == "" {
			typ = "unknown"
		}
		seen[typ] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for typ := range seen {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
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
