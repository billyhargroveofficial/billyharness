package telegrambot

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestGatewayClientMCPStatusUsesSharedFormatter(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/mcp",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSON(t, w, map[string]any{
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
		},
	})

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

func TestGatewayClientContextStatusUsesSharedFormatter(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/context",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSON(t, w, gatewayapi.SessionContextResponse{
				ID:                   "session-1",
				MessageCount:         3,
				EstimatedTokens:      580000,
				ContextWindowTokens:  1000000,
				ContextCompactTokens: 600000,
				PercentUsed:          58,
				Runtime:              gatewayapi.ContextRuntime{Model: "deepseek-v4-flash", ReasoningMode: "high"},
				Usage:                gatewayapi.ContextUsage{CacheHitTokens: 700, CacheMissTokens: 300, WebSummaryInputTokens: 20000, WebSummaryOutputTokens: 900, HelperModelAPITokens: 20900},
				Prompt:               gatewayapi.ContextPrompt{SectionCount: 2, ApproxTokens: 1200, TotalBytes: 4800, CacheStatus: "changed", CacheReason: "tool_schema_changed"},
				Sources: []gatewayapi.ContextSource{{
					Source:          "web_summaries",
					MessageCount:    1,
					EstimatedTokens: 400000,
					Percent:         69,
				}},
				Thresholds: []gatewayapi.ContextThreshold{
					{Percent: 50, Tokens: 500000, Crossed: true},
					{Percent: 70, Tokens: 700000, RemainingTokens: 120000},
				},
			})
		},
	})

	text, err := NewGatewayClient(server.URL).ContextStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"active context: 580.0k / 1.00M", "runtime: model=deepseek-v4-flash", "cache: hit=700", "helper usage: websum=20.0k", "prompt cache: status=changed", "thresholds: ●50% ○70%", "web_summaries"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context status missing %q: %q", want, text)
		}
	}
}

func TestGatewayClientReplaySessionEventsUsesOneShotCursor(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/events",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("after_seq"); got != "17" {
				t.Fatalf("after_seq = %q, want 17", got)
			}
			if got := r.URL.Query().Get("follow"); got != "false" {
				t.Fatalf("follow = %q, want false", got)
			}
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 18, Type: protocol.EventRunStarted},
				protocol.Event{Seq: 19, Type: protocol.EventRunCompleted},
			)
		},
	})

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

func TestGatewayClientReplaySessionEventsDropsStaleSequences(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/events",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 17, Type: protocol.EventRunStarted},
				protocol.Event{Seq: 18, Type: protocol.EventAssistantDelta, Data: "fresh"},
				protocol.Event{Seq: 18, Type: protocol.EventAssistantDelta, Data: "duplicate"},
				protocol.Event{Seq: 19, Type: protocol.EventRunCompleted},
			)
		},
	})

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
