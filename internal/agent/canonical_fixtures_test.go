package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestCanonicalEdgeCaseFixturesPinAgentToolCallValidation(t *testing.T) {
	duplicate := canonicalAgentFixture(t, "duplicate_tool_call_id")
	calls := canonicalAgentToolCalls(t, duplicate)
	err := validateExecutableToolCalls(calls)
	if err == nil || !strings.Contains(err.Error(), `duplicate tool call id "call-dup"`) {
		t.Fatalf("duplicate fixture validation error = %v calls=%#v", err, calls)
	}

	invalidArgs := canonicalAgentFixture(t, "invalid_tool_args")
	calls = canonicalAgentToolCalls(t, invalidArgs)
	if err := validateExecutableToolCalls(calls); err != nil {
		t.Fatalf("invalid-args fixture should not fail duplicate-ID validation: %v", err)
	}
	if len(calls) != 1 || calls[0].InvalidArguments == "" || calls[0].InvalidArgumentError == "" {
		t.Fatalf("invalid-args fixture lost raw invalid argument metadata: %#v", calls)
	}
}

func canonicalAgentFixture(t *testing.T, name string) testkit.GoldenEdgeCaseFixture {
	t.Helper()
	for _, fixture := range testkit.ReadCanonicalEdgeCaseCatalog(t).Fixtures {
		if fixture.Name == name {
			return fixture
		}
	}
	t.Fatalf("missing fixture %q", name)
	return testkit.GoldenEdgeCaseFixture{}
}

func canonicalAgentToolCalls(t *testing.T, fixture testkit.GoldenEdgeCaseFixture) []protocol.ToolCall {
	t.Helper()
	var calls []protocol.ToolCall
	for i, body := range fixture.Events {
		var event protocol.Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatalf("decode fixture %s event %d: %v", fixture.Name, i, err)
		}
		if event.Type != protocol.EventToolCallRequested {
			continue
		}
		data, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatalf("marshal tool call data: %v", err)
		}
		var call protocol.ToolCall
		if err := json.Unmarshal(data, &call); err != nil {
			t.Fatalf("decode tool call data: %v", err)
		}
		calls = append(calls, call)
	}
	return calls
}
