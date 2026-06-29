package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunWithMockProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(cfg)
	a := New(cfg, prov, registry)

	var content string
	if err := a.Run(context.Background(), "hello", func(event protocol.Event) {
		if event.Type == protocol.EventAssistantDelta {
			content += fmt.Sprint(event.Data)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if content != "mock: hello" {
		t.Fatalf("content = %q", content)
	}
}

func TestRunMessagesEmitsTypedTurnAndModelStepEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 3
	a := New(cfg, &captureProvider{}, tools.NewRegistry(cfg))
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentLifecycleValid(t, events)
	runStarted, ok := firstEventData(events, protocol.EventRunStarted)
	if !ok || runStarted["submission_id"] == "" || runStarted["run_id"] == "" {
		t.Fatalf("run started = %#v ok=%v", runStarted, ok)
	}
	for _, event := range events {
		if event.Type == "" {
			continue
		}
		if event.SubmissionID == "" || event.SubmissionID != runStarted["submission_id"] {
			t.Fatalf("event missing stable submission id: %#v run=%#v", event, runStarted)
		}
	}
	started, ok := firstTurnEvent(events, protocol.EventTurnStarted)
	if !ok || started.TurnID != "turn-001" || started.Round != 1 || started.Status != protocol.TurnStatusStarted || started.Model != "mock" {
		t.Fatalf("turn started = %#v ok=%v", started, ok)
	}
	for _, key := range []string{"provider_id", "model_id", "tool_snapshot_hash", "mcp_status_snapshot_hash", "profile_instruction_hash", "dangerous_permission_mode"} {
		if started.Metadata[key] == nil {
			t.Fatalf("turn snapshot missing %s: %#v", key, started.Metadata)
		}
	}
	completed, ok := firstTurnEvent(events, protocol.EventTurnCompleted)
	if !ok || completed.TurnID != "turn-001" || completed.Status != protocol.TurnStatusCompleted || completed.StopReason != protocol.TurnStopFinalAnswer || completed.DurationMS < 0 {
		t.Fatalf("turn completed = %#v ok=%v", completed, ok)
	}
	modelStarted, ok := firstStepEvent(events, protocol.EventStepStarted, protocol.StepKindModelCall)
	if !ok || modelStarted.StepID != "turn-001:model-call-001" || modelStarted.Status != protocol.StepStatusStarted || modelStarted.MessageCount != 2 {
		t.Fatalf("model step started = %#v ok=%v", modelStarted, ok)
	}
	callStarted, ok := firstEventData(events, protocol.EventModelCallStarted)
	if !ok || callStarted["request_id"] == "" || callStarted["provider_id"] != "mock" || callStarted["model_id"] != "mock" || callStarted["status"] != protocol.StepStatusStarted {
		t.Fatalf("model call started = %#v ok=%v", callStarted, ok)
	}
	callFinished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		callFinished["request_id"] != callStarted["request_id"] ||
		callFinished["provider_id"] != "mock" ||
		callFinished["model_id"] != "mock" ||
		callFinished["provider_request_id"] != "mock-request" ||
		callFinished["status"] != protocol.StepStatusCompleted ||
		callFinished["retries"] == nil ||
		callFinished["first_delta_ms"] == nil ||
		callFinished["total_latency_ms"] == nil ||
		callFinished["input_tokens"] == nil ||
		callFinished["output_tokens"] == nil {
		t.Fatalf("model call finished = %#v ok=%v", callFinished, ok)
	}
	modelCompleted, ok := firstStepEvent(events, protocol.EventStepCompleted, protocol.StepKindModelCall)
	if _, hasFirstDelta := modelCompleted.Metadata["first_delta_ms"]; !ok || modelCompleted.StepID != modelStarted.StepID || modelCompleted.Status != protocol.StepStatusCompleted || modelCompleted.Metadata["tool_call_count"] == nil || !hasFirstDelta {
		t.Fatalf("model step completed = %#v ok=%v", modelCompleted, ok)
	}
	if modelCompleted.Metadata["request_id"] != callStarted["request_id"] || modelCompleted.Metadata["input_tokens"] == nil {
		t.Fatalf("model step metadata missing request/usage: %#v", modelCompleted.Metadata)
	}
}

func TestSystemPromptDocumentsTerminalSafeMarkdown(t *testing.T) {
	prompt := systemPrompt()
	for _, want := range []string{
		"simple Markdown",
		"fenced code blocks",
		"simple pipe tables",
		"Do not put math formulas in code fences",
		"HTML",
		"images",
		"Mermaid",
		"LaTeX",
		"парилка",
		"telegram-parilka",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, prompt)
		}
	}
}

func TestInitialMessagesInjectProfileAsSystemContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	messages := InitialMessages(config.Config{
		Profile:            "billy",
		ProjectDocMaxBytes: 0,
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != protocol.RoleSystem || messages[1].Role != protocol.RoleSystem {
		t.Fatalf("roles = %#v", messages)
	}
	if !strings.Contains(messages[1].Content, "# Billyharness profile: billy") ||
		!strings.Contains(messages[1].Content, "<SOUL>") ||
		!strings.Contains(messages[1].Content, "Формулы пиши в LaTeX") {
		t.Fatalf("profile message = %s", messages[1].Content)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles", "billy", "SOUL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestInitialMessagesInjectAgentsAsUserContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("project rules"), 0o600); err != nil {
		t.Fatal(err)
	}

	messages := InitialMessages(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != protocol.RoleSystem {
		t.Fatalf("first role = %q", messages[0].Role)
	}
	if messages[1].Role != protocol.RoleUser || !strings.Contains(messages[1].Content, "# AGENTS.md instructions") {
		t.Fatalf("agents message = %#v", messages[1])
	}
}

func TestRunMessagesReturnsMaxRoundsError(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	prov := &scriptedProvider{repeat: []provider.Event{
		{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{}`},
		{Kind: provider.EventDone},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "loop"},
	}, func(event protocol.Event) {
		if event.Type == protocol.EventRunFailed {
			failed = true
		}
	})
	if err == nil || !strings.Contains(err.Error(), "exceeded max tool rounds: 1") {
		t.Fatalf("err = %v", err)
	}
	if !failed {
		t.Fatalf("run.failed event not emitted")
	}
}

func TestRunMessagesTurnsInvalidToolArgumentsIntoToolError(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 2
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{bad`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "recovered"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	var toolFailed bool
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "bad tool"},
	}, func(event protocol.Event) {
		if event.Type == protocol.EventRunFailed {
			failed = true
		}
		if event.Type == protocol.EventToolCallFailed {
			result, ok := event.Data.(protocol.ToolResult)
			if ok && result.ErrorCode == "invalid_json_args" && strings.Contains(result.Content, "not valid JSON") {
				toolFailed = true
			}
		}
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if failed {
		t.Fatalf("run.failed should not be emitted for malformed tool args")
	}
	if !toolFailed {
		t.Fatalf("tool.call_failed invalid_json_args not emitted")
	}
	if len(next) == 0 || next[len(next)-1].Content != "recovered" {
		t.Fatalf("messages = %#v", next)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("requests = %d", len(prov.requests))
	}
	second := prov.requests[1].Messages
	if len(second) < 2 {
		t.Fatalf("second request messages = %#v", second)
	}
	assistant := second[len(second)-2]
	tool := second[len(second)-1]
	if assistant.Role != protocol.RoleAssistant || len(assistant.ToolCalls) != 1 || string(assistant.ToolCalls[0].Arguments) != "{}" {
		t.Fatalf("assistant malformed call should be sanitized: %#v", assistant)
	}
	if assistant.ToolCalls[0].InvalidArguments != "{bad" {
		t.Fatalf("invalid args should be retained in memory: %#v", assistant.ToolCalls[0])
	}
	if tool.Role != protocol.RoleTool || tool.ToolCallID != "call_1" || !strings.Contains(tool.Content, "not valid JSON") {
		t.Fatalf("tool result message = %#v", tool)
	}
}

func TestRunMessagesEmitsRunFailedOnProviderError(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.MaxToolRounds = 1
	wantErr := &provider.ProviderError{
		Provider:  "deepseek",
		ModelID:   "deepseek-v4-flash",
		Kind:      provider.ErrorServer,
		Status:    503,
		Message:   "provider exploded",
		RequestID: "deepseek-request-3",
		Attempts:  3,
		Retries:   2,
	}
	prov := &scriptedProvider{err: wantErr}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	var events []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "fail"},
	}, func(event protocol.Event) {
		events = append(events, event)
		if event.Type == protocol.EventRunFailed && fmt.Sprint(event.Data) == wantErr.Error() {
			failed = true
		}
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v", err)
	}
	if !failed {
		t.Fatalf("run.failed event not emitted")
	}
	finished, ok := firstEventData(events, protocol.EventModelCallFinished)
	if !ok ||
		finished["status"] != protocol.StepStatusFailed ||
		finished["provider_id"] != "deepseek" ||
		finished["model_id"] != "deepseek-v4-flash" ||
		finished["provider_request_id"] != "deepseek-request-3" ||
		finished["status_code"] != float64(503) ||
		finished["attempts"] != float64(3) ||
		finished["retries"] != float64(2) ||
		!strings.Contains(fmt.Sprint(finished["error"]), "provider exploded") {
		t.Fatalf("model.call_finished = %#v ok=%v", finished, ok)
	}
}

type captureProvider struct {
	messages []protocol.Message
}

func (p *captureProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.messages = append([]protocol.Message(nil), req.Messages...)
	events := make(chan provider.Event, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		events <- provider.Event{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
			RequestID:         req.RequestID,
			ProviderID:        "mock",
			ModelID:           req.Model,
			ProviderRequestID: "mock-request",
			Attempts:          1,
			Retries:           0,
			StatusCode:        200,
		}}
		events <- provider.Event{Kind: provider.EventContent, Text: "done"}
		events <- provider.Event{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 12, OutputTokens: 3, CacheHitTokens: 7, CacheMissTokens: 5}}
		events <- provider.Event{Kind: provider.EventDone}
	}()
	return events, errs
}

type scriptedProvider struct {
	steps     [][]provider.Event
	repeat    []provider.Event
	err       error
	calls     int
	lastTools []protocol.ToolSpec
	requests  []provider.Request
}

type staticContentProvider struct {
	text  string
	usage provider.Usage
}

func (p staticContentProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, 3)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		select {
		case events <- provider.Event{Kind: provider.EventContent, Text: p.text}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		if p.usage != (provider.Usage{}) {
			events <- provider.Event{Kind: provider.EventUsage, Usage: p.usage}
		}
		events <- provider.Event{Kind: provider.EventDone}
	}()
	return events, errs
}

func (p *scriptedProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.calls++
	p.lastTools = req.Tools
	p.requests = append(p.requests, req)
	events := make(chan provider.Event, 8)
	errs := make(chan error, 1)
	call := p.calls
	go func() {
		var streamErr error
		defer func() {
			if streamErr != nil {
				errs <- streamErr
			}
			close(errs)
		}()
		defer close(events)
		if p.err != nil {
			streamErr = p.err
			return
		}
		step := p.repeat
		if call-1 < len(p.steps) {
			step = p.steps[call-1]
		}
		for _, event := range step {
			select {
			case events <- event:
			case <-ctx.Done():
				streamErr = ctx.Err()
				return
			}
		}
	}()
	return events, errs
}

type cancelAfterToolProvider struct {
	calls int
}

type cancelDuringModelProvider struct {
	started chan struct{}
}

func (p *cancelDuringModelProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event)
	errs := make(chan error, 1)
	go func() {
		var streamErr error
		defer func() {
			if streamErr != nil {
				errs <- streamErr
			}
			close(errs)
		}()
		defer close(events)
		close(p.started)
		<-ctx.Done()
		streamErr = ctx.Err()
	}()
	return events, errs
}

func (p *cancelAfterToolProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.calls++
	events := make(chan provider.Event, 2)
	errs := make(chan error, 1)
	call := p.calls
	go func() {
		defer close(events)
		defer close(errs)
		if call == 1 {
			events <- provider.Event{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_cancel", ToolName: "wait_for_cancel", ArgsDelta: `{}`}
			events <- provider.Event{Kind: provider.EventDone}
			return
		}
		<-ctx.Done()
		errs <- ctx.Err()
	}()
	return events, errs
}

func sawEvent(events []protocol.Event, typ protocol.EventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func assertAgentLifecycleValid(t *testing.T, events []protocol.Event) {
	t.Helper()
	if err := eventlog.ValidateLifecycle(events); err != nil {
		t.Fatalf("event lifecycle invalid: %v\nevents=%#v", err, events)
	}
}

func firstTurnEvent(events []protocol.Event, typ protocol.EventType) (protocol.TurnEvent, bool) {
	for _, event := range events {
		if event.Type != typ {
			continue
		}
		var turn protocol.TurnEvent
		bytes, _ := json.Marshal(event.Data)
		if err := json.Unmarshal(bytes, &turn); err == nil {
			return turn, true
		}
	}
	return protocol.TurnEvent{}, false
}

func firstStepEvent(events []protocol.Event, typ protocol.EventType, kind string) (protocol.StepEvent, bool) {
	for _, event := range events {
		step, ok := stepEvent(event, typ)
		if ok && step.Kind == kind {
			return step, true
		}
	}
	return protocol.StepEvent{}, false
}

func stepEvent(event protocol.Event, typ protocol.EventType) (protocol.StepEvent, bool) {
	if event.Type != typ {
		return protocol.StepEvent{}, false
	}
	var step protocol.StepEvent
	bytes, _ := json.Marshal(event.Data)
	if err := json.Unmarshal(bytes, &step); err != nil {
		return protocol.StepEvent{}, false
	}
	return step, true
}

func sawToolStarted(events []protocol.Event, name string) bool {
	for _, event := range events {
		if event.Type == protocol.EventToolCallStarted && fmt.Sprint(event.Data) == name {
			return true
		}
	}
	return false
}

func sawToolAudit(events []protocol.Event, name string, risk protocol.Risk, autoApproved bool) bool {
	for _, event := range events {
		if event.Type != protocol.EventToolAudit {
			continue
		}
		bytes, _ := json.Marshal(event.Data)
		var data struct {
			Name         string        `json:"name"`
			Risk         protocol.Risk `json:"risk"`
			AutoApproved bool          `json:"auto_approved"`
		}
		_ = json.Unmarshal(bytes, &data)
		if data.Name == name && data.Risk == risk && data.AutoApproved == autoApproved {
			return true
		}
	}
	return false
}

func sawHookFinished(events []protocol.Event, hookEvent string) bool {
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] == hookEvent {
			return true
		}
	}
	return false
}

func sawHookToolPayload(events []protocol.Event, hookEvent, callID, toolName string) bool {
	for _, event := range events {
		if event.Type != protocol.EventHookFinished {
			continue
		}
		data := eventDataMap(event)
		if data["hook_event"] == hookEvent && data["call_id"] == callID && data["tool_name"] == toolName {
			return true
		}
	}
	return false
}

func sawPermissionDecision(events []protocol.Event, name, decision, source, reason string) bool {
	for _, event := range events {
		if event.Type != protocol.EventToolPermissionDecided {
			continue
		}
		data := eventDataMap(event)
		if data["name"] == name &&
			data["decision"] == decision &&
			data["source"] == source &&
			data["reason"] == reason {
			return true
		}
	}
	return false
}

func toolProgressEvents(events []protocol.Event, callID string) []protocol.ToolProgressEvent {
	var progress []protocol.ToolProgressEvent
	for _, event := range events {
		if event.Type != protocol.EventToolCallProgress {
			continue
		}
		var item protocol.ToolProgressEvent
		bytes, _ := json.Marshal(event.Data)
		if err := json.Unmarshal(bytes, &item); err != nil {
			continue
		}
		if item.CallID == callID {
			progress = append(progress, item)
		}
	}
	return progress
}

func eventIndex(events []protocol.Event, typ protocol.EventType) int {
	for i, event := range events {
		if event.Type == typ {
			return i
		}
	}
	return -1
}

func firstEventData(events []protocol.Event, typ protocol.EventType) (map[string]any, bool) {
	for _, event := range events {
		if event.Type == typ {
			return eventDataMap(event), true
		}
	}
	return nil, false
}

func eventDataMap(event protocol.Event) map[string]any {
	bytes, _ := json.Marshal(event.Data)
	var data map[string]any
	_ = json.Unmarshal(bytes, &data)
	return data
}

func firstToolResult(events []protocol.Event) (protocol.ToolResult, bool) {
	for _, event := range events {
		if event.Type != protocol.EventToolCallFinished {
			continue
		}
		result, ok := event.Data.(protocol.ToolResult)
		return result, ok
	}
	return protocol.ToolResult{}, false
}

func hasToolSpec(specs []protocol.ToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestAgentFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_AGENT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
				"instructions":    "Use echo when asked to repeat text.",
			}})
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo text",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false},
			}}}})
		case "tools/call":
			var call struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &call)
			text := fmt.Sprint(call.Arguments["text"])
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}
