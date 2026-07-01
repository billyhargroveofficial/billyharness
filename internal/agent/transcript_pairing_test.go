package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunMessagesRejectsTranscriptPairingBeforeProviderCall(t *testing.T) {
	cases := []struct {
		name     string
		messages []protocol.Message
		want     string
	}{
		{
			name: "orphan tool result",
			messages: []protocol.Message{
				{Role: protocol.RoleSystem, Content: "system"},
				{Role: protocol.RoleUser, Content: "hello"},
				{Role: protocol.RoleTool, ToolCallID: "missing", Name: "time_now", Content: "orphan"},
			},
			want: "orphan tool result",
		},
		{
			name: "missing tool result before next user",
			messages: []protocol.Message{
				{Role: protocol.RoleSystem, Content: "system"},
				{Role: protocol.RoleUser, Content: "hello"},
				{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{ID: "call_1", Name: "time_now", Arguments: jsonRaw(`{}`)}}},
				{Role: protocol.RoleUser, Content: "next"},
			},
			want: "before tool result",
		},
		{
			name: "missing tool result before next assistant",
			messages: []protocol.Message{
				{Role: protocol.RoleSystem, Content: "system"},
				{Role: protocol.RoleUser, Content: "hello"},
				{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{ID: "call_1", Name: "time_now", Arguments: jsonRaw(`{}`)}}},
				{Role: protocol.RoleAssistant, Content: "next"},
			},
			want: "assistant message",
		},
		{
			name: "duplicate tool result",
			messages: []protocol.Message{
				{Role: protocol.RoleSystem, Content: "system"},
				{Role: protocol.RoleUser, Content: "hello"},
				{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{ID: "call_1", Name: "time_now", Arguments: jsonRaw(`{}`)}}},
				{Role: protocol.RoleTool, ToolCallID: "call_1", Name: "time_now", Content: "first"},
				{Role: protocol.RoleTool, ToolCallID: "call_1", Name: "time_now", Content: "second"},
			},
			want: "duplicate tool result",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Provider = "mock"
			cfg.Model = "mock"
			prov := &scriptedProvider{}
			a := New(cfg, prov, tools.NewRegistry(cfg))
			var events []protocol.Event
			_, err := a.RunMessages(context.Background(), tc.messages, func(event protocol.Event) {
				events = append(events, event)
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
			if prov.calls != 0 {
				t.Fatalf("provider was called %d times before transcript rejection", prov.calls)
			}
			if sawEvent(events, protocol.EventModelCallStarted) {
				t.Fatalf("model call started despite malformed transcript: %#v", events)
			}
			if !sawEvent(events, protocol.EventRunFailed) {
				t.Fatalf("run failed event missing: %#v", events)
			}
			assertAgentLifecycleValid(t, events)
		})
	}
}

func jsonRaw(value string) []byte {
	return []byte(value)
}
