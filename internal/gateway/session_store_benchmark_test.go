package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func BenchmarkSessionJSONLAppend(b *testing.B) {
	for _, tc := range []struct {
		name     string
		existing int
		event    func(int) protocol.Event
	}{
		{name: "deltas_existing_10000", existing: 10_000, event: benchmarkDeltaEvent},
		{name: "deltas_existing_100000", existing: 100_000, event: benchmarkDeltaEvent},
		{name: "output_refs_existing_10000", existing: 10_000, event: benchmarkOutputRefEvent},
		{name: "coalesced_stream_existing_100000_chunks", existing: 500, event: benchmarkCoalescedDeltaEvent},
	} {
		b.Run(tc.name, func(b *testing.B) {
			store := newSessionStore(b.TempDir())
			session := newGatewaySession("bench-session-append-"+tc.name, time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
			seedGatewaySessionEvents(b, store, session, tc.existing, tc.event)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.AppendEvent(session, tc.event(tc.existing+i)); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(tc.existing), "initial_events")
		})
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

func BenchmarkReplayAfterSeq(b *testing.B) {
	for _, tc := range []struct {
		name     string
		events   int
		afterSeq int64
		want     int
		event    func(int) protocol.Event
	}{
		{name: "deltas_10000_tail100", events: 10_000, afterSeq: 9_900, want: 100, event: benchmarkDeltaEvent},
		{name: "deltas_100000_tail100", events: 100_000, afterSeq: 99_900, want: 100, event: benchmarkDeltaEvent},
		{name: "output_refs_10000_tail100", events: 10_000, afterSeq: 9_900, want: 100, event: benchmarkOutputRefEvent},
		{name: "coalesced_stream_100000_chunks_tail100", events: 500, afterSeq: 400, want: 100, event: benchmarkCoalescedDeltaEvent},
	} {
		b.Run(tc.name, func(b *testing.B) {
			store := newSessionStore(b.TempDir())
			session := newGatewaySession("bench-replay-after-seq-"+tc.name, time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
			seedGatewaySessionEvents(b, store, session, tc.events, tc.event)
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
			b.StopTimer()
			b.ReportMetric(float64(tc.events), "log_events")
			b.ReportMetric(float64(tc.want), "tail_events")
		})
	}
}

func seedGatewaySessionEvents(b *testing.B, store *sessionStore, session *Session, count int, event func(int) protocol.Event) {
	b.Helper()
	if count <= 0 {
		return
	}
	if err := writeGatewayBenchmarkEventFixture(store, session, count, event); err != nil {
		b.Fatal(err)
	}
	store.mu.Lock()
	store.eventSeq[session.ID] = int64(count)
	store.mu.Unlock()
}

func writeGatewayBenchmarkEventFixture(store *sessionStore, session *Session, count int, event func(int) protocol.Event) (err error) {
	id, err := cleanSessionID(session.ID)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(store.dir, id)
	if err := ensurePrivateGatewayDir(sessionDir); err != nil {
		return err
	}
	created := session.Created
	if created.IsZero() {
		created = time.Now().UTC()
	}
	manifest := sessionManifest{
		SchemaVersion:             gatewaySessionSchemaVersion,
		SessionID:                 id,
		CreatedAt:                 created,
		UpdatedAt:                 created,
		HistoryJSONL:              sessionHistoryJSONLName,
		EventsJSONL:               sessionEventsJSONLName,
		InputsJSONL:               sessionInputsJSONLName,
		SnapshotJSON:              id + ".json",
		ConfigSnapshotJSON:        sessionConfigSnapshotName,
		ModelProviderSnapshotJSON: sessionModelSnapshotName,
		MCPSnapshotJSON:           sessionMCPSnapshotName,
		MessageCount:              len(session.messages()),
		Owner:                     session.Owner,
		EventSeq:                  int64(count),
	}
	if err := writeSessionManifest(filepath.Join(sessionDir, sessionManifestName), manifest); err != nil {
		return err
	}
	eventsPath := filepath.Join(sessionDir, sessionEventsJSONLName)
	file, err := os.OpenFile(eventsPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	writer := bufio.NewWriterSize(file, 1<<20)
	encoder := json.NewEncoder(writer)
	for i := 0; i < count; i++ {
		seq := int64(i + 1)
		ts := created.Add(time.Duration(i) * time.Microsecond)
		storedEvent := protocol.EnrichEvent(event(i), protocol.EventEnvelope{
			Seq:    seq,
			Source: protocol.EventSourceGateway,
			RunID:  gatewaySessionRunID(id, 0),
			TS:     ts.Format(time.RFC3339Nano),
		})
		storedEvent.Seq = seq
		record := sessionEventRecord{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           seq,
			SessionID:     id,
			Timestamp:     ts,
			EventType:     string(storedEvent.Type),
			Event:         storedEvent,
		}
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Chmod(0o600)
}

func benchmarkDeltaEvent(i int) protocol.Event {
	return protocol.Event{Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%06d", i)}
}

func benchmarkCoalescedDeltaEvent(i int) protocol.Event {
	return protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Repeat(fmt.Sprintf("delta-%06d ", i), 200)}
}

func benchmarkOutputRefEvent(i int) protocol.Event {
	callID := fmt.Sprintf("call-%06d", i)
	return protocol.Event{
		Type:      protocol.EventToolOutputRefCreated,
		CallID:    callID,
		AttemptID: callID + ":attempt-001",
		Data: protocol.ToolOutputRefEvent{
			CallID:               callID,
			Name:                 "web_fetch",
			AttemptID:            callID + ":attempt-001",
			OutputRef:            fmt.Sprintf("/tmp/billyharness/tool-output/ref-%06d.txt", i),
			OutputRefID:          fmt.Sprintf("ref-%06d.txt", i),
			OutputRefBytes:       int64(64*1024 + i%1024),
			OutputRefSHA256:      fmt.Sprintf("%064x", i),
			OutputRefPermissions: "0600",
			OutputRefPlaintext:   true,
			Truncated:            true,
		},
	}
}
