package trace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestEventWriterRecordsContiguousEventsAndPayloadRefs(t *testing.T) {
	var out bytes.Buffer
	payloadDir := filepath.Join(t.TempDir(), "payloads")
	writer := NewEventWriter("run-1", &out,
		WithNow(func() time.Time { return time.Unix(10, 0).UTC() }),
		WithPayloadDir(payloadDir, func(event protocol.Event) bool {
			return event.Type == protocol.EventToolCallFinished
		}),
	)
	if _, err := writer.Record("task-1", protocol.Event{Type: protocol.EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Record("task-1", protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{Name: "fs_read_file", Content: "large"},
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := ReplayEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Records != 2 || summary.FirstSeq != 1 || summary.LastSeq != 2 || summary.PayloadRefs != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.RunStarted != 1 || summary.ToolCallsFinished != 1 {
		t.Fatalf("event counters = %#v", summary)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var second EventRecord
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.PayloadRefs) != 1 {
		t.Fatalf("payload refs = %#v", second.PayloadRefs)
	}
	if _, err := os.Stat(second.PayloadRefs[0].Path); err != nil {
		t.Fatal(err)
	}
}

func TestReplayEventsAggregatesUsageAndEventCounters(t *testing.T) {
	var out bytes.Buffer
	writer := NewEventWriter("run-1", &out)
	events := []protocol.Event{
		{Type: protocol.EventRunStarted},
		{Type: protocol.EventModelCallStarted},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      100,
			"output_tokens":     7,
			"cache_hit_tokens":  80,
			"cache_miss_tokens": 20,
		}},
		{Type: protocol.EventContextCompacted},
		{Type: protocol.EventToolCallStarted, Data: "time_now"},
		{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{Name: "time_now", Content: "ok"}},
		{Type: protocol.EventModelCallFinished},
		{Type: protocol.EventRunCompleted},
	}
	for _, event := range events {
		if _, err := writer.Record("task-1", event); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := ReplayEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RunStarted != 1 || summary.RunCompleted != 1 ||
		summary.ModelCallsStarted != 1 || summary.ModelCallsFinished != 1 ||
		summary.ToolCallsStarted != 1 || summary.ToolCallsFinished != 1 ||
		summary.ContextCompactions != 1 {
		t.Fatalf("event counters = %#v", summary)
	}
	if summary.InputTokens != 100 || summary.OutputTokens != 7 ||
		summary.CacheHitTokens != 80 || summary.CacheMissTokens != 20 {
		t.Fatalf("usage counters = %#v", summary)
	}
}

func TestReplayEventsRejectsSequenceGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	data := strings.Join([]string{
		`{"schema_version":1,"seq":1,"run_id":"run-1","event_type":"run.started","event":{"type":"run.started"}}`,
		`{"schema_version":1,"seq":3,"run_id":"run-1","event_type":"run.completed","event":{"type":"run.completed"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReplayEvents(path)
	if err == nil || !strings.Contains(err.Error(), "sequence gap") {
		t.Fatalf("expected sequence gap error, got %v", err)
	}
}

func TestWriteManifestUsesPrivateAtomicJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace", "manifest.json")
	err := WriteManifest(path, Manifest{
		RunID:        "run-1",
		Harness:      "fast-agent-harness-go",
		ResultsJSONL: "results.jsonl",
		EventsJSONL:  "events.jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
	var manifest Manifest
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != CurrentManifestVersion || manifest.RunID != "run-1" || manifest.StartedAtMS == 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
}
