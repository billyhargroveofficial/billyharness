package projector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestCanonicalEdgeCaseFixturesProjectClientSnapshots(t *testing.T) {
	catalog := testkit.ReadCanonicalEdgeCaseCatalog(t)
	for _, fixture := range catalog.Fixtures {
		events := decodeCanonicalProjectorFixtureEvents(t, fixture.Events)
		p := New()
		var snap Snapshot
		for _, event := range events {
			snap = p.Apply(event)
		}
		switch fixture.Name {
		case "stream_gap":
			if snap.SeqGap == nil || snap.SeqGap.AfterSeq != 1 || snap.SeqGap.GotSeq != 3 {
				t.Fatalf("stream gap snapshot = %#v", snap.SeqGap)
			}
		case "parallel_cancellation":
			if snap.RunState != RunStateFailed || snap.ToolsByCallID["call-slow"].Status != "aborted" {
				t.Fatalf("parallel cancellation snapshot = %#v", snap)
			}
		case "late_output_ref":
			tool := snap.ToolsByCallID["call-ref"]
			if snap.RunState != RunStateCompleted || tool.Status != "finished" || tool.Compact == nil || tool.Compact.OutputRefID != "late-ref" {
				t.Fatalf("late output-ref snapshot = %#v tool=%#v", snap, tool)
			}
		case "provider_error_after_partial_stream":
			if snap.RunState != RunStateFailed || !strings.Contains(snap.AssistantText, "partial answer") {
				t.Fatalf("provider error snapshot = %#v", snap)
			}
		case "mcp_catalog_change":
			if snap.RunState != RunStateCompleted || snap.ToolCalls != 1 || !strings.Contains(snap.ToolsByCallID["call-mcp"].Content, "catalog refreshed") {
				t.Fatalf("mcp catalog snapshot = %#v", snap)
			}
		case "telegram_interruption":
			if snap.RunState != RunStateFailed || snap.ToolsByCallID["call-ask"].Status != "aborted" {
				t.Fatalf("telegram interruption snapshot = %#v", snap)
			}
		}
	}
}

func decodeCanonicalProjectorFixtureEvents(t *testing.T, raw []json.RawMessage) []protocol.Event {
	t.Helper()
	events := make([]protocol.Event, 0, len(raw))
	for i, body := range raw {
		var event protocol.Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatalf("decode projector fixture event %d: %v", i, err)
		}
		events = append(events, event)
	}
	return events
}
