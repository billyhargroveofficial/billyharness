package eventlog

import (
	"encoding/json"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func FuzzValidateEventEnvelopeJSON(f *testing.F) {
	seeds := []string{
		`{"schema_version":1,"seq":1,"type":"run.started","source":"gateway","run_id":"run-1","ts":"2026-07-04T00:00:00Z"}`,
		`{"schema_version":1,"seq":2,"type":"tool.call_requested","source":"gateway","run_id":"run-1","call_id":"call-1","ts":"2026-07-04T00:00:01Z"}`,
		`{"type":"assistant.content_delta","data":"missing envelope"}`,
		`{"schema_version":999,"seq":-1,"type":"run.started","source":"gateway","run_id":"","ts":"bad"}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		var event protocol.Event
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return
		}
		_ = ValidateEnvelope(event)
		_ = ValidateLifecycle([]protocol.Event{event})
		_ = ValidateClosedLifecycle([]protocol.Event{event})
	})
}
