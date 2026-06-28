package hooks

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestRunnerRunsHookWithRedactedCappedOutput(t *testing.T) {
	t.Setenv("BILLY_SECRET_TOKEN", "env-secret-value")
	runner := New(config.Config{
		HooksEnabled: true,
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
	if data["stdout_truncated"] != true || data["exit_code"] != 0 {
		t.Fatalf("hook output metadata = %#v", data)
	}
	if data["call_id"] != "call-1" || data["attempt_id"] != "attempt-1" || data["tool_name"] != "time_now" {
		t.Fatalf("hook ids = %#v", data)
	}
}

func TestRunnerNonFatalFailureEmitsFailedAndContinues(t *testing.T) {
	runner := New(config.Config{
		HooksEnabled: true,
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
	if data["exit_code"] != 7 || !strings.Contains(fmt.Sprint(data["stderr"]), "nope") {
		t.Fatalf("failed hook data = %#v", data)
	}
}

func TestRunnerFatalFailureReturnsError(t *testing.T) {
	runner := New(config.Config{
		HooksEnabled: true,
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
	data, ok := event.Data.(map[string]any)
	if !ok {
		t.Fatalf("event data = %#v", event.Data)
	}
	return data
}
