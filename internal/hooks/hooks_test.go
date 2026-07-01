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

func TestRunPromptSubmitJSONUpdatesPromptContextAndEventMetadata(t *testing.T) {
	runner := New(config.HookSettings{
		Enabled: true,
		Hooks: []config.Hook{{
			Name:           "prompt",
			Event:          PromptSubmitEvent,
			Command:        "sh",
			Args:           []string{"-c", `printf '%s' '{"additional_context":"prefer package-local tests","updated_prompt":"run focused tests"}'`},
			MaxOutputBytes: 4096,
			Timeout:        time.Second,
			Enabled:        true,
		}},
	})
	var events []protocol.Event
	result, err := runner.RunPromptSubmit(context.Background(), PromptSubmitInput{
		Prompt:       "old prompt",
		CWD:          "/repo",
		ModelID:      "mock",
		Profile:      "billy",
		SubmissionID: "submission-1",
		RunID:        "run-1",
		Source:       "tui",
		AccessMode:   "plan",
		Metadata: map[string]string{
			"prompt_command":                 "review",
			"prompt_command_original":        "/review internal/tui",
			"prompt_command_source":          "/repo/.billyharness/commands/review.md",
			"prompt_command_scope":           "workspace",
			"prompt_command_expanded_bytes":  "19",
			"prompt_command_expanded_sha256": "abc123",
		},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Prompt != "run focused tests" || result.AdditionalContext != "prefer package-local tests" || !result.Updated || result.Blocked {
		t.Fatalf("result = %#v", result)
	}
	if len(events) != 2 || events[0].Type != protocol.EventHookStarted || events[1].Type != protocol.EventHookFinished {
		t.Fatalf("events = %#v", events)
	}
	data := hookEventMap(t, events[1])
	if data["hook_event"] != PromptSubmitEvent ||
		data["decision"] != "allow" ||
		hookEventInt(data["additional_context_bytes"]) != len("prefer package-local tests") ||
		hookEventInt(data["updated_prompt_bytes"]) != len("run focused tests") ||
		data["updated_prompt_sha256"] != sha256Hex("run focused tests") ||
		hookEventInt(data["prompt_hook_context_cap_bytes"]) != MaxPromptHookAdditionalContextBytes ||
		hookEventInt(data["prompt_hook_prompt_cap_bytes"]) != MaxPromptHookUpdatedPromptBytes ||
		hookEventInt(data["prompt_hook_reason_cap_bytes"]) != MaxPromptHookReasonBytes {
		t.Fatalf("prompt hook metadata = %#v", data)
	}
	payload, _ := data["payload"].(map[string]any)
	if payload["prompt"] != "old prompt" ||
		payload["cwd"] != "/repo" ||
		payload["model_id"] != "mock" ||
		payload["profile"] != "billy" ||
		payload["submission_id"] != "submission-1" ||
		payload["run_id"] != "run-1" ||
		payload["source"] != "tui" ||
		payload["access_mode"] != "plan" {
		t.Fatalf("payload = %#v", payload)
	}
	slash, _ := payload["slash_command"].(map[string]any)
	if slash["prompt_command"] != "review" || slash["prompt_command_original"] != "/review internal/tui" {
		t.Fatalf("slash metadata = %#v payload=%#v", slash, payload)
	}
}

func TestRunPromptSubmitBlockAndNonJSONStdoutContext(t *testing.T) {
	runner := New(config.HookSettings{
		Enabled: true,
		Hooks: []config.Hook{
			{
				Name:           "context",
				Event:          PromptSubmitEvent,
				Command:        "sh",
				Args:           []string{"-c", `printf '%s' '{plain text from hook'`},
				MaxOutputBytes: 4096,
				Timeout:        time.Second,
				Enabled:        true,
			},
			{
				Name:           "block",
				Event:          PromptSubmitEvent,
				Command:        "sh",
				Args:           []string{"-c", `printf '%s' '{"decision":"block","reason":"missing ticket id"}'`},
				MaxOutputBytes: 4096,
				Timeout:        time.Second,
				Enabled:        true,
			},
		},
	})
	var events []protocol.Event
	result, err := runner.RunPromptSubmit(context.Background(), PromptSubmitInput{Prompt: "ship it"}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked || result.Reason != "missing ticket id" || !strings.Contains(result.AdditionalContext, "{plain text from hook") {
		t.Fatalf("result = %#v", result)
	}
	var sawContext, sawBlock bool
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := hookEventMap(t, event)
		switch data["name"] {
		case "context":
			sawContext = data["decision"] == "allow" && hookEventInt(data["additional_context_bytes"]) == len("{plain text from hook")
		case "block":
			sawBlock = data["decision"] == "block" && data["block_reason"] == "missing ticket id"
		}
	}
	if !sawContext || !sawBlock {
		t.Fatalf("missing prompt hook events context=%t block=%t events=%#v", sawContext, sawBlock, events)
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
