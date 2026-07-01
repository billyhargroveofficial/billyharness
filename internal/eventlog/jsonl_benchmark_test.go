package eventlog

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

const eventJSONLBenchmarkSchemaVersion = 1

type benchmarkEventJSONLRecord struct {
	SchemaVersion int            `json:"schema_version"`
	Seq           int64          `json:"seq"`
	ScopeID       string         `json:"scope_id"`
	Timestamp     time.Time      `json:"ts"`
	EventType     string         `json:"event_type"`
	Event         protocol.Event `json:"event"`
}

func BenchmarkEventJSONLAppend(b *testing.B) {
	for _, tc := range []struct {
		name     string
		existing int
		event    func(int) protocol.Event
	}{
		{name: "deltas_existing_10000", existing: 10_000, event: benchmarkEventJSONLDeltaEvent},
		{name: "deltas_existing_100000", existing: 100_000, event: benchmarkEventJSONLDeltaEvent},
		{name: "output_refs_existing_10000", existing: 10_000, event: benchmarkEventJSONLOutputRefEvent},
		{name: "coalesced_stream_existing_100000_chunks", existing: 500, event: benchmarkEventJSONLCoalescedDeltaEvent},
	} {
		b.Run(tc.name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "events.jsonl")
			scopeID := "event-jsonl-append-" + tc.name
			seedEventJSONLRecords(b, path, scopeID, tc.existing, tc.event)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := AppendJSONL(path, benchmarkEventJSONLRecordFor(scopeID, tc.existing+i+1, tc.event(tc.existing+i))); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(tc.existing), "initial_events")
		})
	}
}

func BenchmarkEventJSONLReplay(b *testing.B) {
	for _, tc := range []struct {
		name     string
		events   int
		afterSeq int64
		want     int
		event    func(int) protocol.Event
	}{
		{name: "deltas_10000_tail100", events: 10_000, afterSeq: 9_900, want: 100, event: benchmarkEventJSONLDeltaEvent},
		{name: "deltas_100000_tail100", events: 100_000, afterSeq: 99_900, want: 100, event: benchmarkEventJSONLDeltaEvent},
		{name: "output_refs_10000_tail100", events: 10_000, afterSeq: 9_900, want: 100, event: benchmarkEventJSONLOutputRefEvent},
		{name: "coalesced_stream_100000_chunks_tail100", events: 500, afterSeq: 400, want: 100, event: benchmarkEventJSONLCoalescedDeltaEvent},
	} {
		b.Run(tc.name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "events.jsonl")
			scopeID := "event-jsonl-replay-" + tc.name
			seedEventJSONLRecords(b, path, scopeID, tc.events, tc.event)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := 0
				err := ReplayJSONL[benchmarkEventJSONLRecord](path, JSONLOptions{}, func(item JSONLRecord[benchmarkEventJSONLRecord]) error {
					if item.Value.Seq > tc.afterSeq {
						got++
					}
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
				if got != tc.want {
					b.Fatalf("replayed tail records = %d, want %d", got, tc.want)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(tc.events), "log_events")
			b.ReportMetric(float64(tc.want), "tail_events")
		})
	}
}

func seedEventJSONLRecords(b *testing.B, path, scopeID string, count int, event func(int) protocol.Event) {
	b.Helper()
	if count <= 0 {
		return
	}
	if err := writeEventJSONLBenchmarkFixture(path, scopeID, count, event); err != nil {
		b.Fatal(err)
	}
}

func writeEventJSONLBenchmarkFixture(path, scopeID string, count int, event func(int) protocol.Event) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), defaultJSONLDirMode); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, defaultJSONLFileMode)
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
		if err := encoder.Encode(benchmarkEventJSONLRecordFor(scopeID, i+1, event(i))); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Chmod(defaultJSONLFileMode)
}

func benchmarkEventJSONLRecordFor(scopeID string, seq int, event protocol.Event) benchmarkEventJSONLRecord {
	ts := time.Unix(1_700_000_000, int64(seq)*int64(time.Microsecond)).UTC()
	enriched := protocol.EnrichEvent(event, protocol.EventEnvelope{
		Seq:    int64(seq),
		Source: protocol.EventSourceGateway,
		RunID:  scopeID + ":run-0",
		TS:     ts.Format(time.RFC3339Nano),
	})
	enriched.Seq = int64(seq)
	return benchmarkEventJSONLRecord{
		SchemaVersion: eventJSONLBenchmarkSchemaVersion,
		Seq:           int64(seq),
		ScopeID:       scopeID,
		Timestamp:     ts,
		EventType:     string(enriched.Type),
		Event:         enriched,
	}
}

func benchmarkEventJSONLDeltaEvent(i int) protocol.Event {
	return protocol.Event{Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%06d", i)}
}

func benchmarkEventJSONLCoalescedDeltaEvent(i int) protocol.Event {
	return protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Repeat(fmt.Sprintf("delta-%06d ", i), 200)}
}

func benchmarkEventJSONLOutputRefEvent(i int) protocol.Event {
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
