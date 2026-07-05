package mcpclient

import (
	"strings"
	"testing"
)

func TestMCPContentKeepsTranscriptCompactAndMetadataStructured(t *testing.T) {
	out := callToolResponse{
		Content: []map[string]any{
			{"type": "text", "text": "short text"},
			{"type": "image", "mimeType": "image/png", "data": "super-secret-token"},
			{"type": "resource", "resource": map[string]any{"uri": "file:///tmp/result.json", "mimeType": "application/json", "text": "payload"}},
		},
		StructuredContent: map[string]any{
			"answer": "structured",
			"secret": "super-secret-token",
		},
		Meta: map[string]any{
			"request_id": "mcp-call-1",
			"token":      "super-secret-token",
		},
	}

	rendered := renderContent(out, 4096)
	if !strings.Contains(rendered, "short text") ||
		!strings.Contains(rendered, "[MCP image content omitted") ||
		!strings.Contains(rendered, "[MCP resource content omitted") {
		t.Fatalf("rendered content should keep compact summaries: %q", rendered)
	}
	if strings.Contains(rendered, "super-secret-token") || strings.Contains(rendered, `"data"`) {
		t.Fatalf("rendered content leaked raw non-text data: %q", rendered)
	}

	metadata := mcpToolResultMetadata(out, []string{"super-secret-token"})
	if metadata["mcp_result_content_count"] != 3 || metadata["mcp_result_is_error"] != false {
		t.Fatalf("metadata counts = %#v", metadata)
	}
	types, ok := metadata["mcp_result_content_types"].([]string)
	if !ok || len(types) != 3 || types[0] != "image" || types[1] != "resource" || types[2] != "text" {
		t.Fatalf("content types = %#v", metadata["mcp_result_content_types"])
	}
	items, ok := metadata["mcp_result_content"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("content metadata = %#v", metadata["mcp_result_content"])
	}
	image, ok := items[1].(map[string]any)
	if !ok || image["data"] != "[redacted]" {
		t.Fatalf("image metadata not redacted = %#v", items[1])
	}
	structured, ok := metadata["mcp_structured_content"].(map[string]any)
	if !ok || structured["answer"] != "structured" || structured["secret"] != "[redacted]" {
		t.Fatalf("structured metadata = %#v", metadata["mcp_structured_content"])
	}
	meta, ok := metadata["mcp_result_meta"].(map[string]any)
	if !ok || meta["request_id"] != "mcp-call-1" || meta["token"] != "[redacted]" {
		t.Fatalf("result meta = %#v", metadata["mcp_result_meta"])
	}
}

func TestMCPStructuredOnlyContentStillRendersForModel(t *testing.T) {
	out := callToolResponse{StructuredContent: map[string]any{"answer": "only structured"}}
	rendered := renderContent(out, 4096)
	if !strings.Contains(rendered, `"answer": "only structured"`) {
		t.Fatalf("structured-only content should render as fallback JSON: %q", rendered)
	}
}
