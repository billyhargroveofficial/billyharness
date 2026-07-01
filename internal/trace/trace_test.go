package trace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
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
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{ID: "call-1", Name: "fs_read_file"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Record("task-1", protocol.Event{
		Type:      protocol.EventToolCallStarted,
		CallID:    "call-1",
		AttemptID: "turn-001:tool-call-001:attempt-001",
		Data:      "fs_read_file",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Record("task-1", protocol.Event{
		Type: protocol.EventToolCallFinished,
		Data: protocol.ToolResult{
			CallID:  "call-1",
			Name:    "fs_read_file",
			Content: "large",
			Metadata: map[string]any{
				"attempt_id": "turn-001:tool-call-001:attempt-001",
			},
		},
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
	if summary.Records != 4 || summary.FirstSeq != 1 || summary.LastSeq != 4 || summary.PayloadRefs != 1 || summary.PayloadBytes == 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.RunStarted != 1 || summary.ToolCallsStarted != 1 || summary.ToolCallsFinished != 1 {
		t.Fatalf("event counters = %#v", summary)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var fourth EventRecord
	if err := json.Unmarshal([]byte(lines[3]), &fourth); err != nil {
		t.Fatal(err)
	}
	if len(fourth.PayloadRefs) != 1 {
		t.Fatalf("payload refs = %#v", fourth.PayloadRefs)
	}
	if _, err := os.Stat(fourth.PayloadRefs[0].Path); err != nil {
		t.Fatal(err)
	}
}

func TestReplayEventsCountsImportedSessionMarker(t *testing.T) {
	var out bytes.Buffer
	writer := NewEventWriter("import-1", &out, WithNow(func() time.Time { return time.Unix(10, 0).UTC() }))
	if _, err := writer.Record("", protocol.Event{
		Type: protocol.EventSessionImported,
		Data: protocol.SessionImportedEvent{Source: "codex.jsonl", Format: "jsonl", ImportedMessages: 2, MessageCount: 3, ApproxTokens: 12},
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
	if summary.EventTypes[string(protocol.EventSessionImported)] != 1 || summary.RunStarted != 0 || summary.RunCompleted != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestEventWriterConcurrentRecordsStayContiguous(t *testing.T) {
	var out bytes.Buffer
	writer := NewEventWriter("run-1", &out)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := writer.Record("task-1", protocol.Event{Type: protocol.EventRunStarted})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
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
	if summary.Records != 64 || summary.FirstSeq != 1 || summary.LastSeq != 64 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestEventWriterPreservesNestedRunIDForAggregateReplay(t *testing.T) {
	var out bytes.Buffer
	writer := NewEventWriter("suite-run", &out)
	events := []struct {
		task  string
		event protocol.Event
	}{
		{task: "task-1", event: protocol.Event{Type: protocol.EventRunStarted, RunID: "agent-run-1"}},
		{task: "task-1", event: protocol.Event{Type: protocol.EventRunCompleted, RunID: "agent-run-1"}},
		{task: "task-2", event: protocol.Event{Type: protocol.EventRunStarted, RunID: "agent-run-2"}},
		{task: "task-2", event: protocol.Event{Type: protocol.EventRunCompleted, RunID: "agent-run-2"}},
	}
	for _, item := range events {
		if _, err := writer.Record(item.task, item.event); err != nil {
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
	if summary.RunID != "suite-run" || summary.RunStarted != 2 || summary.RunCompleted != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(summary.Timeline) != 4 || summary.Timeline[0].RunID != "agent-run-1" || summary.Timeline[2].RunID != "agent-run-2" {
		t.Fatalf("timeline = %#v", summary.Timeline)
	}
}

func TestReplayEventsRejectsPayloadHashMismatch(t *testing.T) {
	root := t.TempDir()
	payloadPath := filepath.Join(root, "payload.json")
	payload := []byte(`{"event":{"type":"tool.call_finished"}}`)
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	record := EventRecord{
		SchemaVersion: CurrentManifestVersion,
		Seq:           1,
		RunID:         "run-1",
		EventType:     string(protocol.EventToolCallFinished),
		Event:         protocol.Event{Type: protocol.EventToolCallFinished},
		PayloadRefs: []PayloadRef{{
			PayloadID: "payload:1",
			Kind:      "protocol_event",
			Path:      payloadPath,
			SHA256:    "bad-sha",
			Bytes:     int64(len(payload)),
		}},
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(root, "events.jsonl")
	if err := os.WriteFile(eventsPath, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ReplayEvents(eventsPath)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}
}

func TestReplayEventsAuditsToolOutputRefs(t *testing.T) {
	root := t.TempDir()
	validPath := filepath.Join(root, "large-tool-output.txt")
	body := []byte(strings.Repeat("trace-output-", 42_000))
	if len(body) < 500_000 {
		t.Fatalf("test fixture must exercise at least 500k chars, got %d", len(body))
	}
	if err := os.WriteFile(validPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	missingPath := filepath.Join(root, "missing-output.txt")
	var out bytes.Buffer
	writer := NewEventWriter("run-refs", &out)
	events := []protocol.Event{
		{
			Type: protocol.EventToolOutputRefCreated,
			Data: protocol.ToolOutputRefEvent{
				CallID:          "call-valid",
				Name:            "fs_read_file",
				AttemptID:       "turn-001:tool-call-001:attempt-001",
				OutputRef:       validPath,
				OutputRefID:     filepath.Base(validPath),
				OutputRefBytes:  int64(len(body)),
				OutputRefSHA256: hex.EncodeToString(sum[:]),
				Truncated:       true,
			},
		},
		{
			Type: protocol.EventToolOutputRefCreated,
			Data: protocol.ToolOutputRefEvent{
				CallID:          "call-missing",
				Name:            "mcp_call",
				AttemptID:       "turn-001:tool-call-002:attempt-001",
				OutputRef:       missingPath,
				OutputRefID:     filepath.Base(missingPath),
				OutputRefBytes:  123,
				OutputRefSHA256: strings.Repeat("0", 64),
				Truncated:       true,
			},
		},
	}
	for _, event := range events {
		if _, err := writer.Record("task-refs", event); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "events.jsonl")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := ReplayEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OutputRefs != 2 ||
		summary.OutputRefBytes != int64(len(body)) ||
		summary.MissingOutputRefs != 1 ||
		summary.OutputRefHashMismatch != 0 ||
		len(summary.OutputRefWarnings) != 1 ||
		summary.OutputRefWarnings[0].CallID != "call-missing" ||
		summary.OutputRefWarnings[0].Reason != "missing" {
		t.Fatalf("summary output refs = %#v warnings=%#v", summary, summary.OutputRefWarnings)
	}
}

func TestReplayEventsAggregatesUsageCumulativeAndEventCounters(t *testing.T) {
	var out bytes.Buffer
	writer := NewEventWriter("run-1", &out)
	profileHash := "profile-sha"
	events := []protocol.Event{
		{Type: protocol.EventRunStarted},
		{Type: protocol.EventTurnStarted, Data: protocol.TurnEvent{TurnID: "turn-001", Round: 1, Status: protocol.TurnStatusStarted, Metadata: map[string]any{"profile_instruction_hash": profileHash}}},
		{Type: protocol.EventStepStarted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:model-call-001", Round: 1, Kind: protocol.StepKindModelCall, Status: protocol.StepStatusStarted}},
		{Type: protocol.EventModelCallStarted, TurnID: "turn-001", StepID: "turn-001:model-call-001", Data: protocol.ModelCallEvent{
			RequestID:           "request-1",
			Status:              protocol.StepStatusStarted,
			PromptInventoryHash: "prompt-inventory-sha",
			PromptInventory: &protocol.PromptInventory{
				Hash:         "prompt-inventory-sha",
				TotalBytes:   120,
				ApproxTokens: 30,
				Sections: []protocol.PromptSection{{
					Name:         "system_prompt",
					Role:         protocol.RoleSystem,
					Index:        0,
					ByteCount:    80,
					ApproxTokens: 20,
					SHA256:       "system-sha",
				}},
			},
			PromptCacheBreak: &protocol.PromptCacheBreak{
				Status:           "changed",
				Reason:           "tool_schema_changed",
				ChangedFields:    []string{"tool_schema_changed"},
				CurrentSignature: "cache-signature",
			},
		}},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      100,
			"output_tokens":     7,
			"cache_hit_tokens":  80,
			"cache_miss_tokens": 20,
			"turn_id":           "turn-001",
			"step_id":           "turn-001:model-call-001",
		}},
		{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
			"input_tokens":      125,
			"output_tokens":     9,
			"cache_hit_tokens":  85,
			"cache_miss_tokens": 40,
			"turn_id":           "turn-001",
			"step_id":           "turn-001:model-call-001",
		}},
		{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{Percent: 50, EstimatedTokens: 500000, ContextWindowTokens: 1000000, ThresholdTokens: 500000, Stage: "before_turn"}},
		{Type: protocol.EventContextCompacted},
		{Type: protocol.EventProviderHelperUsage, Data: protocol.ProviderHelperUsageEvent{
			Kind:            "context_compact",
			Provider:        "mock",
			Model:           "mock-summary",
			RunID:           "run-1",
			CompactionID:    "compact-1",
			InputTokens:     50,
			OutputTokens:    5,
			CacheHitTokens:  20,
			CacheMissTokens: 30,
			APITokens:       55,
		}},
		{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:model-call-001", Round: 1, Kind: protocol.StepKindModelCall, Status: protocol.StepStatusCompleted, DurationMS: 11, Metadata: map[string]any{"first_delta_ms": 4}}},
		{Type: protocol.EventStepStarted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:tool-batch-001", Round: 1, Kind: protocol.StepKindToolBatch, Status: protocol.StepStatusStarted, Parallel: true, BatchSize: 2}},
		{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{ID: "call-1", Name: "time_now"}},
		{Type: protocol.EventToolCallStarted, CallID: "call-1", AttemptID: "turn-001:tool-call-001:attempt-001", Data: "time_now"},
		{Type: protocol.EventToolCallProgress, Data: protocol.ToolProgressEvent{
			CallID:    "call-1",
			Name:      "time_now",
			AttemptID: "turn-001:tool-call-001:attempt-001",
			Phase:     "executing",
			Status:    protocol.StepStatusStarted,
		}},
		{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
			CallID:  "call-1",
			Name:    "time_now",
			Content: "ok",
			Metadata: map[string]any{
				"attempt_id": "turn-001:tool-call-001:attempt-001",
			},
		}},
		{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{TurnID: "turn-001", StepID: "turn-001:tool-batch-001", Round: 1, Kind: protocol.StepKindToolBatch, Status: protocol.StepStatusCompleted, Parallel: true, BatchSize: 2, DurationMS: 7}},
		{Type: protocol.EventModelCallFinished, TurnID: "turn-001", StepID: "turn-001:model-call-001"},
		{Type: protocol.EventTurnCompleted, Data: protocol.TurnEvent{TurnID: "turn-001", Round: 1, Status: protocol.TurnStatusCompleted, StopReason: protocol.TurnStopToolResults}},
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
		summary.TurnsStarted != 1 || summary.TurnsCompleted != 1 || summary.TurnsFailed != 0 ||
		summary.StepsStarted != 2 || summary.StepsCompleted != 2 || summary.StepsFailed != 0 ||
		summary.ParallelBatches != 1 ||
		summary.ModelCallsStarted != 1 || summary.ModelCallsFinished != 1 ||
		summary.PromptInventories != 1 || summary.PromptCacheBreaks != 1 ||
		summary.ToolCallsStarted != 1 || summary.ToolCallProgress != 1 || summary.ToolCallsFinished != 1 ||
		summary.ContextThresholds != 1 || summary.ContextCompactions != 1 {
		t.Fatalf("event counters = %#v", summary)
	}
	if summary.FirstDeltaSamples != 1 || summary.FirstDeltaTotalMS != 4 || summary.ModelLatencyMS != 11 || summary.ParallelBatchLatencyMS != 7 {
		t.Fatalf("latency counters = %#v", summary)
	}
	if summary.InputTokens != 125 || summary.OutputTokens != 9 ||
		summary.CacheHitTokens != 85 || summary.CacheMissTokens != 40 {
		t.Fatalf("usage counters = %#v", summary)
	}
	if summary.HelperModelCalls != 1 || summary.HelperInputTokens != 50 || summary.HelperOutputTokens != 5 ||
		summary.HelperCacheHitTokens != 20 || summary.HelperCacheMissTokens != 30 || summary.HelperAPITokens != 55 {
		t.Fatalf("helper usage counters = %#v", summary)
	}
	if got := summary.HelperUsageByKind["context_compact"]; got.Calls != 1 || got.APITokens != 55 {
		t.Fatalf("helper usage by kind = %#v", summary.HelperUsageByKind)
	}
	if len(summary.ProfileHashes) != 1 || summary.ProfileHashes[0] != profileHash {
		t.Fatalf("profile hashes = %#v", summary.ProfileHashes)
	}
	wantTimeline := []string{
		string(protocol.EventRunStarted),
		string(protocol.EventTurnStarted),
		string(protocol.EventStepStarted),
		string(protocol.EventModelCallStarted),
		string(protocol.EventContextThreshold),
		string(protocol.EventContextCompacted),
		string(protocol.EventProviderHelperUsage),
		string(protocol.EventStepCompleted),
		string(protocol.EventStepStarted),
		string(protocol.EventToolCallRequested),
		string(protocol.EventToolCallStarted),
		string(protocol.EventToolCallProgress),
		string(protocol.EventToolCallFinished),
		string(protocol.EventStepCompleted),
		string(protocol.EventModelCallFinished),
		string(protocol.EventTurnCompleted),
		string(protocol.EventRunCompleted),
	}
	if len(summary.Timeline) != len(wantTimeline) {
		t.Fatalf("timeline length = %d, want %d: %#v", len(summary.Timeline), len(wantTimeline), summary.Timeline)
	}
	for i, want := range wantTimeline {
		if summary.Timeline[i].EventType != want {
			t.Fatalf("timeline[%d].event_type = %q, want %q: %#v", i, summary.Timeline[i].EventType, want, summary.Timeline)
		}
	}
	if summary.Timeline[1].TurnID != "turn-001" || summary.Timeline[1].Round != 1 || summary.Timeline[1].Status != protocol.TurnStatusStarted {
		t.Fatalf("turn timeline item = %#v", summary.Timeline[1])
	}
	if summary.Timeline[1].ProfileHash != profileHash {
		t.Fatalf("turn timeline profile hash = %#v", summary.Timeline[1])
	}
	if summary.Timeline[2].StepID != "turn-001:model-call-001" ||
		summary.Timeline[2].Kind != protocol.StepKindModelCall ||
		summary.Timeline[2].Status != protocol.StepStatusStarted {
		t.Fatalf("model step timeline item = %#v", summary.Timeline[2])
	}
	if summary.Timeline[3].PromptInventoryHash != "prompt-inventory-sha" ||
		summary.Timeline[3].PromptCacheStatus != "changed" ||
		summary.Timeline[3].PromptCacheReason != "tool_schema_changed" {
		t.Fatalf("model prompt diagnostic timeline item = %#v", summary.Timeline[3])
	}
	if summary.Timeline[4].Seq != 7 || summary.Timeline[4].Kind != "context_threshold" {
		t.Fatalf("threshold timeline item = %#v", summary.Timeline[4])
	}
	if summary.Timeline[5].Seq != 8 || summary.Timeline[5].Kind != "context_compaction" {
		t.Fatalf("compaction timeline item = %#v", summary.Timeline[5])
	}
	if summary.Timeline[6].Kind != "helper_usage" || summary.Timeline[6].Name != "context_compact" {
		t.Fatalf("helper usage timeline item = %#v", summary.Timeline[6])
	}
	if summary.Timeline[9].CallID != "call-1" ||
		summary.Timeline[9].Name != "time_now" {
		t.Fatalf("tool request timeline item = %#v", summary.Timeline[9])
	}
	if summary.Timeline[10].CallID != "call-1" ||
		summary.Timeline[10].AttemptID != "turn-001:tool-call-001:attempt-001" ||
		summary.Timeline[10].Name != "time_now" ||
		summary.Timeline[10].Status != protocol.StepStatusStarted {
		t.Fatalf("tool start timeline item = %#v", summary.Timeline[10])
	}
	if summary.Timeline[11].CallID != "call-1" ||
		summary.Timeline[11].AttemptID != "turn-001:tool-call-001:attempt-001" ||
		summary.Timeline[11].Name != "time_now" ||
		summary.Timeline[11].Phase != "executing" ||
		summary.Timeline[11].Status != protocol.StepStatusStarted {
		t.Fatalf("tool progress timeline item = %#v", summary.Timeline[11])
	}
	if summary.Timeline[12].CallID != "call-1" ||
		summary.Timeline[12].AttemptID != "turn-001:tool-call-001:attempt-001" ||
		summary.Timeline[12].Name != "time_now" {
		t.Fatalf("tool finish timeline item = %#v", summary.Timeline[12])
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var turnRecord EventRecord
	if err := json.Unmarshal([]byte(lines[1]), &turnRecord); err != nil {
		t.Fatal(err)
	}
	if turnRecord.ProfileHash != profileHash {
		t.Fatalf("event record profile hash = %q, want %q", turnRecord.ProfileHash, profileHash)
	}
}

func TestGoldenTraceReplayCanonicalAgentLoop(t *testing.T) {
	summary, err := ReplayEvents(testkit.CanonicalAgentLoopTracePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if summary.RunID != "run-golden-agent-loop" || summary.Records != 39 || summary.FirstSeq != 1 || summary.LastSeq != 39 {
		t.Fatalf("summary identity = %#v", summary)
	}
	if summary.RunStarted != 1 || summary.RunCompleted != 1 || summary.RunFailed != 0 ||
		summary.TurnsStarted != 2 || summary.TurnsCompleted != 2 || summary.TurnsFailed != 0 ||
		summary.StepsStarted != 5 || summary.StepsCompleted != 5 || summary.StepsFailed != 0 ||
		summary.ParallelBatches != 1 ||
		summary.ModelCallsStarted != 2 || summary.ModelCallsFinished != 2 ||
		summary.ToolCallsStarted != 3 || summary.ToolCallProgress != 2 || summary.ToolCallsFinished != 2 ||
		summary.ContextThresholds != 1 || summary.ContextCompactions != 1 {
		t.Fatalf("summary counters = %#v", summary)
	}
	if summary.InputTokens != 2100 || summary.OutputTokens != 135 ||
		summary.CacheHitTokens != 1100 || summary.CacheMissTokens != 1000 {
		t.Fatalf("usage counters = %#v", summary)
	}
	if summary.FirstDeltaSamples != 2 || summary.FirstDeltaTotalMS != 8 ||
		summary.ModelLatencyMS != 50 || summary.ToolLatencyMS != 40 || summary.ParallelBatchLatencyMS != 25 {
		t.Fatalf("latency counters = %#v", summary)
	}
	if len(summary.ProfileHashes) != 1 || summary.ProfileHashes[0] != "profile-golden" {
		t.Fatalf("profile hashes = %#v", summary.ProfileHashes)
	}
	if len(summary.Timeline) == 0 {
		t.Fatal("timeline is empty")
	}
	var aborted bool
	for _, item := range summary.Timeline {
		if item.EventType == string(protocol.EventToolCallAborted) && item.CallID == "call-shell" && item.Status == "aborted" {
			aborted = true
			break
		}
	}
	if !aborted {
		t.Fatalf("timeline missing aborted shell interruption: %#v", summary.Timeline)
	}
	if summary.OutputRefs != 1 || summary.OutputRefBytes != 0 || summary.MissingOutputRefs != 1 {
		t.Fatalf("output ref audit = %#v", summary)
	}
	if len(summary.OutputRefWarnings) != 1 ||
		summary.OutputRefWarnings[0].ExpectedBytes != 524288 ||
		summary.OutputRefWarnings[0].Reason != "missing" {
		t.Fatalf("output ref warnings = %#v", summary.OutputRefWarnings)
	}
}

func TestGoldenRunBundleIncludesReplayInputs(t *testing.T) {
	bundle := testkit.ReadCanonicalAgentLoopBundle(t)
	if bundle.Name != "canonical-agent-loop" || bundle.Trace != testkit.CanonicalAgentLoopTrace || !bundle.OfflineReplay {
		t.Fatalf("bundle identity = %#v", bundle)
	}
	if len(bundle.Messages) != 2 ||
		bundle.Messages[0].Role != string(protocol.RoleSystem) ||
		bundle.Messages[1].Role != string(protocol.RoleUser) ||
		!strings.Contains(bundle.Messages[1].Content, "web context") {
		t.Fatalf("bundle messages = %#v", bundle.Messages)
	}
	coverage := strings.Join(bundle.Coverage, "\n")
	for _, want := range []string{"assistant streaming", "web summary", "aborted shell", "compaction", "final provider usage"} {
		if !strings.Contains(coverage, want) {
			t.Fatalf("bundle coverage missing %q: %#v", want, bundle.Coverage)
		}
	}
	records := testkit.ReadTraceRecords(t, filepath.Join(filepath.Dir(testkit.CanonicalAgentLoopBundlePath(t)), bundle.Trace))
	if len(records) != 39 || records[len(records)-1].Seq != 39 {
		t.Fatalf("bundle trace records = %d last=%#v", len(records), records[len(records)-1])
	}
}

func TestReplayEventsRejectsLifecycleViolation(t *testing.T) {
	var out bytes.Buffer
	writer := NewEventWriter("run-1", &out)
	events := []protocol.Event{
		{Type: protocol.EventRunStarted},
		{
			Type:      protocol.EventToolCallFinished,
			CallID:    "call-1",
			AttemptID: "attempt-1",
			Data: protocol.ToolResult{
				CallID:  "call-1",
				Name:    "time_now",
				Content: "ok",
				Metadata: map[string]any{
					"attempt_id": "attempt-1",
				},
			},
		},
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

	_, err := ReplayEvents(path)
	if err == nil || !strings.Contains(err.Error(), "matching call_id") {
		t.Fatalf("expected lifecycle call_id error, got %v", err)
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
	var corrupt *eventlog.CorruptionError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error %T does not expose CorruptionError", err)
	}
	if corrupt.Path != path || corrupt.Line != 2 || corrupt.RecordNo != 2 {
		t.Fatalf("corruption error = %#v", corrupt)
	}
}

func TestReplayEventsIncludesRealAgentCompactionBoundary(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.ContextCompactTokens = 50
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	cfg.MaxToolRounds = 1
	runner := agent.New(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old context ", 120)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest task"},
	}
	var out bytes.Buffer
	writer := NewEventWriter("replay-compaction", &out, WithNow(func() time.Time { return time.Unix(20, 0).UTC() }))
	var recordErr error
	_, runErr := runner.RunMessages(context.Background(), messages, func(event protocol.Event) {
		if _, err := writer.Record("task-compaction", event); err != nil && recordErr == nil {
			recordErr = err
		}
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := ReplayEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ContextCompactions != 1 {
		t.Fatalf("summary should include compaction: %#v", summary)
	}
	found := false
	for _, item := range summary.Timeline {
		if item.EventType == string(protocol.EventContextCompacted) && item.Kind == "context_compaction" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("timeline missing compaction boundary: %#v", summary.Timeline)
	}
}

func TestReplayEventsRejectsInvalidNewEventEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	data := strings.Join([]string{
		`{"schema_version":1,"seq":1,"run_id":"run-1","event_type":"tool.call_started","event":{"schema_version":1,"seq":1,"source":"agent","ts":"2026-06-28T10:00:00Z","run_id":"run-1","type":"tool.call_started","data":"fs_read_file"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReplayEvents(path)
	if err == nil || !strings.Contains(err.Error(), "missing call_id") {
		t.Fatalf("expected missing call_id error, got %v", err)
	}
}

func TestWriteManifestUsesPrivateAtomicJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace", "manifest.json")
	err := WriteManifest(path, Manifest{
		RunID:                 "run-1",
		Harness:               "fast-agent-harness-go",
		ProfileHash:           "profile-sha",
		ResultsJSONL:          "results.jsonl",
		EventsJSONL:           "events.jsonl",
		ConfigSnapshot:        map[string]any{"model": "mock"},
		ProviderModelMetadata: map[string]any{"provider": "mock"},
		MCPStatus:             map[string]any{"enabled": false},
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
	if manifest.SchemaVersion != CurrentManifestVersion || manifest.RunID != "run-1" || manifest.ProfileHash != "profile-sha" || manifest.StartedAtMS == 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.ConfigSnapshot["model"] != "mock" ||
		manifest.ProviderModelMetadata["provider"] != "mock" ||
		manifest.MCPStatus["enabled"] != false {
		t.Fatalf("manifest snapshots = %#v", manifest)
	}
}
