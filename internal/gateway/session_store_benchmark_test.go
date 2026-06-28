package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func BenchmarkGatewaySessionJSONLAppend(b *testing.B) {
	store := newSessionStore(b.TempDir())
	session := newGatewaySession("bench-append", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%06d", i)}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGatewaySessionJSONLReplay(b *testing.B) {
	for _, tc := range []struct {
		name     string
		events   int
		afterSeq int64
		want     int
	}{
		{name: "full_1000", events: 1000, afterSeq: 0, want: 1000},
		{name: "tail_1000_last100", events: 1000, afterSeq: 900, want: 100},
	} {
		b.Run(tc.name, func(b *testing.B) {
			store := newSessionStore(b.TempDir())
			session := newGatewaySession("bench-replay-"+tc.name, time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
			for i := 0; i < tc.events; i++ {
				if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%06d", i)}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				events, err := store.ReplayEventsAfter(session.ID, tc.afterSeq)
				if err != nil {
					b.Fatal(err)
				}
				if len(events) != tc.want {
					b.Fatalf("replayed events = %d, want %d", len(events), tc.want)
				}
			}
		})
	}
}
