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
	appendGatewayBenchmarkPrelude(b, store, session, protocol.EventAssistantDelta)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.AppendEvent(session, benchmarkEventForSession(session, benchmarkDeltaEvent(i))); err != nil {
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
				if _, err := store.AppendEvent(session, benchmarkEventForSession(session, tc.event(tc.existing+i))); err != nil {
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
			preludeCount := appendGatewayBenchmarkPrelude(b, store, session, protocol.EventAssistantDelta)
			for i := 0; i < tc.events; i++ {
				if _, err := store.AppendEvent(session, benchmarkEventForSession(session, benchmarkDeltaEvent(i))); err != nil {
					b.Fatal(err)
				}
			}
			afterSeq := tc.afterSeq
			want := tc.want
			if afterSeq == 0 {
				want += preludeCount
			} else {
				afterSeq += int64(preludeCount)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				events, err := store.ReplayEventsAfter(session.ID, afterSeq)
				if err != nil {
					b.Fatal(err)
				}
				if len(events) != want {
					b.Fatalf("replayed events = %d, want %d", len(events), want)
				}
			}
		})
	}
}

func BenchmarkGatewaySessionJSONLReplayStartupListManifestOnly(b *testing.B) {
	for _, tc := range []struct {
		name          string
		sessions      int
		eventsPerSess int
	}{
		{name: "sessions_5_events_1000", sessions: 5, eventsPerSess: 1_000},
		{name: "sessions_5_events_10000", sessions: 5, eventsPerSess: 10_000},
	} {
		b.Run(tc.name, func(b *testing.B) {
			dir := b.TempDir()
			var totalBytes int64
			for i := 0; i < tc.sessions; i++ {
				store := newSessionStore(dir)
				session := newGatewaySession(fmt.Sprintf("bench-startup-list-%d", i), time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
				if err := writeGatewayBenchmarkEventFixture(store, session, tc.eventsPerSess, benchmarkDeltaEvent); err != nil {
					b.Fatal(err)
				}
				info, err := os.Stat(filepath.Join(dir, session.ID, sessionEventsJSONLName))
				if err != nil {
					b.Fatal(err)
				}
				totalBytes += info.Size()
			}
			b.ReportAllocs()
			b.ReportMetric(float64(totalBytes), "stored_bytes")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				loaded, diagnostics, err := newSessionStore(dir).LoadAllWithDiagnostics()
				if err != nil {
					b.Fatal(err)
				}
				if diagnostics.LoadedCount != tc.sessions || len(loaded) != tc.sessions {
					b.Fatalf("loaded=%d diagnostics=%#v", len(loaded), diagnostics)
				}
				for _, session := range loaded {
					if !session.manifestOnly || session.Thread != nil {
						b.Fatalf("session should be manifest-only: %#v", session)
					}
					_ = sessionSummary(session)
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
	start := 0
	for _, prelude := range benchmarkLifecyclePrelude(event(0).Type) {
		if start >= count {
			break
		}
		seq := int64(start + 1)
		ts := created.Add(time.Duration(start) * time.Microsecond)
		if err := encodeGatewayBenchmarkEventRecord(encoder, id, seq, ts, prelude); err != nil {
			return err
		}
		start++
	}
	for i := start; i < count; i++ {
		seq := int64(i + 1)
		ts := created.Add(time.Duration(i) * time.Microsecond)
		if err := encodeGatewayBenchmarkEventRecord(encoder, id, seq, ts, event(i)); err != nil {
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

func encodeGatewayBenchmarkEventRecord(encoder *json.Encoder, sessionID string, seq int64, ts time.Time, event protocol.Event) error {
	storedEvent := protocol.EnrichEvent(event, protocol.EventEnvelope{
		Seq:    seq,
		Source: protocol.EventSourceGateway,
		RunID:  gatewaySessionRunID(sessionID, 1),
		TS:     ts.Format(time.RFC3339Nano),
	})
	storedEvent.Seq = seq
	record := sessionEventRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           seq,
		SessionID:     sessionID,
		Timestamp:     ts,
		EventType:     string(storedEvent.Type),
		Event:         storedEvent,
	}
	return encoder.Encode(record)
}

func appendGatewayBenchmarkPrelude(b *testing.B, store *sessionStore, session *Session, eventType protocol.EventType) int {
	b.Helper()
	count := 0
	for _, event := range benchmarkLifecyclePrelude(eventType) {
		if _, err := store.AppendEvent(session, benchmarkEventForSession(session, event)); err != nil {
			b.Fatal(err)
		}
		count++
	}
	return count
}

func benchmarkEventForSession(session *Session, event protocol.Event) protocol.Event {
	if event.RunID == "" {
		event.RunID = gatewaySessionRunID(session.ID, 1)
	}
	return event
}

func benchmarkLifecyclePrelude(eventType protocol.EventType) []protocol.Event {
	switch eventType {
	case protocol.EventAssistantDelta, protocol.EventAssistantReasoning, protocol.EventProviderUsageUpdate, protocol.EventModelCallFinished:
		return []protocol.Event{
			{Type: protocol.EventRunStarted},
			{Type: protocol.EventTurnStarted, TurnID: benchmarkTurnID},
			{
				Type:   protocol.EventStepStarted,
				TurnID: benchmarkTurnID,
				StepID: benchmarkModelStepID,
				Data: protocol.StepEvent{
					TurnID: benchmarkTurnID,
					StepID: benchmarkModelStepID,
					Round:  1,
					Kind:   protocol.StepKindModelCall,
					Status: protocol.StepStatusStarted,
				},
			},
			{
				Type:   protocol.EventModelCallStarted,
				TurnID: benchmarkTurnID,
				StepID: benchmarkModelStepID,
				Data: protocol.ModelCallEvent{
					RequestID: "bench-model-call",
					Status:    protocol.StepStatusStarted,
				},
			},
		}
	case protocol.EventToolOutputRefCreated:
		return []protocol.Event{
			{Type: protocol.EventRunStarted},
			{
				Type:   protocol.EventToolCallRequested,
				CallID: benchmarkOutputRefCallID,
				Data:   protocol.ToolCall{ID: benchmarkOutputRefCallID, Name: "web_fetch"},
			},
			{
				Type:      protocol.EventToolCallStarted,
				CallID:    benchmarkOutputRefCallID,
				AttemptID: benchmarkOutputRefAttemptID,
				Data:      "web_fetch",
			},
		}
	default:
		return []protocol.Event{{Type: protocol.EventRunStarted}}
	}
}

const (
	benchmarkTurnID      = "turn-bench"
	benchmarkModelStepID = "turn-bench:model-call-001"
)

func benchmarkDeltaEvent(i int) protocol.Event {
	return protocol.Event{Type: protocol.EventAssistantDelta, TurnID: benchmarkTurnID, StepID: benchmarkModelStepID, Data: fmt.Sprintf("delta-%06d", i)}
}

func benchmarkCoalescedDeltaEvent(i int) protocol.Event {
	return protocol.Event{Type: protocol.EventAssistantDelta, TurnID: benchmarkTurnID, StepID: benchmarkModelStepID, Data: strings.Repeat(fmt.Sprintf("delta-%06d ", i), 200)}
}

const (
	benchmarkOutputRefCallID    = "call-output-ref-bench"
	benchmarkOutputRefAttemptID = "call-output-ref-bench:attempt-001"
)

func benchmarkOutputRefEvent(i int) protocol.Event {
	return protocol.Event{
		Type:      protocol.EventToolOutputRefCreated,
		CallID:    benchmarkOutputRefCallID,
		AttemptID: benchmarkOutputRefAttemptID,
		Data: protocol.ToolOutputRefEvent{
			CallID:               benchmarkOutputRefCallID,
			Name:                 "web_fetch",
			AttemptID:            benchmarkOutputRefAttemptID,
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
