package eventlog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestCanonicalEdgeCaseFixturesValidateEventContracts(t *testing.T) {
	catalog := testkit.ReadCanonicalEdgeCaseCatalog(t)
	wantNames := map[string]bool{
		"stream_gap":                          false,
		"duplicate_tool_call_id":              false,
		"parallel_cancellation":               false,
		"late_output_ref":                     false,
		"provider_error_after_partial_stream": false,
		"invalid_tool_args":                   false,
		"mcp_catalog_change":                  false,
		"telegram_interruption":               false,
		"corrupted_replay_envelope":           false,
	}
	for _, fixture := range catalog.Fixtures {
		wantNames[fixture.Name] = true
		events := decodeCanonicalFixtureEvents(t, fixture.Events)
		err := validateCanonicalFixtureEvents(events)
		if fixture.Valid && err != nil {
			t.Fatalf("%s should validate: %v", fixture.Name, err)
		}
		if !fixture.Valid {
			if err == nil {
				t.Fatalf("%s should fail validation", fixture.Name)
			}
			if fixture.ExpectError != "" && !strings.Contains(err.Error(), fixture.ExpectError) {
				t.Fatalf("%s error = %v, want %q", fixture.Name, err, fixture.ExpectError)
			}
		}
	}
	for name, saw := range wantNames {
		if !saw {
			t.Fatalf("missing canonical fixture %q", name)
		}
	}
}

func decodeCanonicalFixtureEvents(t *testing.T, raw []json.RawMessage) []protocol.Event {
	t.Helper()
	events := make([]protocol.Event, 0, len(raw))
	for i, body := range raw {
		var event protocol.Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatalf("decode fixture event %d: %v", i, err)
		}
		events = append(events, event)
	}
	return events
}

func validateCanonicalFixtureEvents(events []protocol.Event) error {
	runID := ""
	for _, event := range events {
		if strings.TrimSpace(event.RunID) != "" {
			runID = event.RunID
			break
		}
	}
	validator := NewRecordValidator(RecordValidatorOptions{
		SchemaVersion:    1,
		ScopeName:        "run_id",
		ExpectedScopeID:  runID,
		ValidateEnvelope: true,
		RequireEnvelope:  true,
	})
	lifecycle := NewLifecycleValidator()
	for _, event := range events {
		if err := validator.Validate(Record{
			SchemaVersion: event.SchemaVersion,
			Seq:           event.Seq,
			ScopeID:       event.RunID,
			EventType:     string(event.Type),
			Event:         event,
			HasEvent:      true,
		}); err != nil {
			return err
		}
		if err := lifecycle.Observe(event); err != nil {
			return err
		}
	}
	return nil
}
