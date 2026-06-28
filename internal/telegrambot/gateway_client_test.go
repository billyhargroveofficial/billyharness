package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayClientMCPStatusUsesSharedFormatter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mcp" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"config_files": []string{"/root/billyharness/mcp.config.toml"},
			"allowed":      []string{"github"},
			"enabled":      true,
			"servers": []map[string]any{{
				"name":       "github",
				"transport":  "stdio",
				"command":    "npx",
				"enabled":    true,
				"connected":  true,
				"state":      "connected",
				"tool_count": 7,
			}},
		})
	}))
	t.Cleanup(server.Close)

	text, err := NewGatewayClient(server.URL).MCPStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"config: /root/billyharness/mcp.config.toml", "allowed: github", "github", "connected", "command:npx", "tools:7"} {
		if !strings.Contains(text, want) {
			t.Fatalf("MCP status missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, `"servers"`) || strings.Contains(text, `"tool_count"`) {
		t.Fatalf("MCP status should be formatted text, got JSON-ish output: %q", text)
	}
}
