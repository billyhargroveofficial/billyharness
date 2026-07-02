package tui

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestToolLifecycleEventsUpdateStatusWithoutTranscriptNoise(t *testing.T) {
	m := newTestModel(t)
	requested := protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-1",
		CallID: "call-1",
		Data: map[string]any{
			"name":      "web_fetch",
			"arguments": map[string]any{"url": "https://example.com/really/long/path"},
		},
	}
	m.applyEvent(requested)
	afterRequested := len(m.blocks)
	if afterRequested == 0 {
		t.Fatalf("tool request should add a compact transcript block")
	}

	for _, event := range []protocol.Event{
		{Type: protocol.EventToolCallStarted, TurnID: "turn-1", CallID: "call-1", Data: map[string]any{"name": "web_fetch"}},
		{Type: protocol.EventToolCallProgress, TurnID: "turn-1", CallID: "call-1", Data: map[string]any{"message": "downloaded bytes"}},
		{Type: protocol.EventToolOutputRefCreated, TurnID: "turn-1", CallID: "call-1", Data: map[string]any{"ref": "tool-output/ref.txt"}},
		{Type: protocol.EventToolPermissionRequested, TurnID: "turn-1", CallID: "call-1", Data: map[string]any{"name": "web_fetch"}},
		{Type: protocol.EventToolPermissionDecided, TurnID: "turn-1", CallID: "call-1", Data: map[string]any{"approved": true}},
	} {
		m.applyEvent(event)
		if len(m.blocks) != afterRequested {
			t.Fatalf("%s should not add transcript noise: before=%d after=%d blocks=%#v", event.Type, afterRequested, len(m.blocks), m.blocks)
		}
	}

	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, TurnID: "turn-1", CallID: "call-1", Data: map[string]any{"name": "web_fetch", "status": "ok"}})
	for _, block := range m.blocks {
		if strings.Contains(block.Title, "Tool event") || strings.Contains(block.Content, "tool.output_ref.created") {
			t.Fatalf("unexpected lifecycle noise block: %#v", block)
		}
	}
}
