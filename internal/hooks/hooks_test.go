package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestRunnerRunsHookWithRedactedCappedOutput(t *testing.T) {
	t.Setenv("BILLY_SECRET_TOKEN", "env-secret-value")
	runner := New(config.HookSettings{
		Enabled: true,
		Hooks: []config.Hook{{
			Name:           "capture",
			Event:          "before_tool",
			Command:        "sh",
			Args:           []string{"-c", `printf 'token env-secret-value trailing-output'`},
			MaxOutputBytes: 18,
			Timeout:        time.Second,
			Enabled:        true,
		}},
	})
	var events []protocol.Event
	err := runner.Run(context.Background(), "before_tool", map[string]any{
		"call_id":    "call-1",
		"attempt_id": "attempt-1",
		"tool_name":  "time_now",
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != protocol.EventHookStarted || events[1].Type != protocol.EventHookFinished {
		t.Fatalf("events = %#v", events)
	}
	data := hookEventMap(t, events[1])
	if fmt.Sprint(data["stdout"]) == "" || strings.Contains(fmt.Sprint(data["stdout"]), "env-secret-value") {
		t.Fatalf("stdout was not redacted: %#v", data)
	}
	if data["stdout_truncated"] != true || hookEventInt(data["exit_code"]) != 0 {
		t.Fatalf("hook output metadata = %#v", data)
	}
	if data["call_id"] != "call-1" || data["attempt_id"] != "attempt-1" || data["tool_name"] != "time_now" {
		t.Fatalf("hook ids = %#v", data)
	}
}

func TestRunnerNonFatalFailureEmitsFailedAndContinues(t *testing.T) {
	runner := New(config.HookSettings{
		Enabled: true,
		Hooks: []config.Hook{{
			Name:           "fails",
			Event:          "after_tool",
			Command:        "sh",
			Args:           []string{"-c", "echo nope >&2; exit 7"},
			MaxOutputBytes: 1024,
			Timeout:        time.Second,
			Enabled:        true,
		}},
	})
	var events []protocol.Event
	err := runner.Run(context.Background(), "after_tool", nil, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != protocol.EventHookFailed {
		t.Fatalf("events = %#v", events)
	}
	data := hookEventMap(t, events[1])
	if hookEventInt(data["exit_code"]) != 7 || !strings.Contains(fmt.Sprint(data["stderr"]), "nope") {
		t.Fatalf("failed hook data = %#v", data)
	}
}

func TestRunnerFatalFailureReturnsError(t *testing.T) {
	runner := New(config.HookSettings{
		Enabled: true,
		Hooks: []config.Hook{{
			Name:           "fatal",
			Event:          "session_start",
			Command:        "sh",
			Args:           []string{"-c", "exit 9"},
			MaxOutputBytes: 1024,
			Timeout:        time.Second,
			Fatal:          true,
			Enabled:        true,
		}},
	})
	err := runner.Run(context.Background(), "session_start", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "fatal") {
		t.Fatalf("err = %v", err)
	}
}

func hookEventMap(t *testing.T, event protocol.Event) map[string]any {
	t.Helper()
	bytes, err := json.Marshal(event.Data)
	if err != nil {
		t.Fatalf("event data marshal: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(bytes, &data); err != nil {
		t.Fatalf("event data unmarshal: %v", err)
	}
	return data
}

func hookEventInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
