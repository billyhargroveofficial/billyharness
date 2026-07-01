package testkit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const CanonicalAgentLoopTrace = "agent_loop_full.jsonl"
const CanonicalAgentLoopBundle = "agent_loop_full.bundle.json"

type TraceRecord struct {
	SchemaVersion int             `json:"schema_version"`
	Seq           int64           `json:"seq"`
	RunID         string          `json:"run_id"`
	TaskID        string          `json:"task_id,omitempty"`
	EventType     string          `json:"event_type,omitempty"`
	ProfileHash   string          `json:"profile_hash,omitempty"`
	Event         json.RawMessage `json:"event"`
}

type GoldenRunBundle struct {
	Name          string             `json:"name"`
	Trace         string             `json:"trace"`
	OfflineReplay bool               `json:"offline_replay"`
	Messages      []GoldenRunMessage `json:"messages"`
	Coverage      []string           `json:"coverage,omitempty"`
}

type GoldenRunMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func CanonicalAgentLoopTracePath(t testing.TB) string {
	t.Helper()
	return canonicalTraceFilePath(t, CanonicalAgentLoopTrace)
}

func CanonicalAgentLoopBundlePath(t testing.TB) string {
	t.Helper()
	return canonicalTraceFilePath(t, CanonicalAgentLoopBundle)
}

func canonicalTraceFilePath(t testing.TB, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating testkit events.go")
	}
	return filepath.Join(filepath.Dir(file), "testdata", "traces", name)
}

func ReadCanonicalAgentLoopBundle(t testing.TB) GoldenRunBundle {
	t.Helper()
	path := CanonicalAgentLoopBundlePath(t)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var bundle GoldenRunBundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return bundle
}

func ReadTraceRecords(t testing.TB, path string) []TraceRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var records []TraceRecord
	for line := 1; scanner.Scan(); line++ {
		var record TraceRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode %s:%d: %v", path, line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}
