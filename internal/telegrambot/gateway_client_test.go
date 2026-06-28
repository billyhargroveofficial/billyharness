package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
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

func TestGatewayClientReplaySessionEventsUsesOneShotCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/events" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("after_seq"); got != "17" {
			t.Fatalf("after_seq = %q, want 17", got)
		}
		if got := r.URL.Query().Get("follow"); got != "false" {
			t.Fatalf("follow = %q, want false", got)
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{Seq: 18, Type: protocol.EventRunStarted})
		_ = json.NewEncoder(w).Encode(protocol.Event{Seq: 19, Type: protocol.EventRunCompleted})
	}))
	t.Cleanup(server.Close)

	var events []protocol.Event
	if err := NewGatewayClient(server.URL).ReplaySessionEvents(context.Background(), "session-1", 17, func(event protocol.Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 18 || events[1].Seq != 19 {
		t.Fatalf("events = %#v", events)
	}
}
