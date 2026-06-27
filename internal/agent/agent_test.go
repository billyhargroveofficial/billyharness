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
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunWithMockProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
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

func TestRunMessagesCompactsBeforeProviderCall(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.ContextCompactTokens = 10
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	capture := &captureProvider{}
	a := New(cfg, capture, tools.NewRegistry(cfg))
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old user ", 200)},
		{Role: protocol.RoleAssistant, Content: "old assistant", ReasoningContent: strings.Repeat("reasoning ", 100)},
		{Role: protocol.RoleUser, Content: "latest prompt"},
	}
	var compacted bool
	next, err := a.RunMessages(context.Background(), messages, func(event protocol.Event) {
		if event.Type == protocol.EventContextCompacted {
			compacted = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !compacted {
		t.Fatalf("expected context compaction event")
	}
	if len(capture.messages) < 3 {
		t.Fatalf("provider messages were not compacted: %#v", capture.messages)
	}
	if capture.messages[0].Role != protocol.RoleSystem || capture.messages[0].Content != "system" {
		t.Fatalf("system prompt not preserved: %#v", capture.messages[0])
	}
	if !strings.HasPrefix(capture.messages[1].Content, compactionMarker) {
		t.Fatalf("summary message missing: %#v", capture.messages[1])
	}
	if strings.Contains(capture.messages[1].Content, "reasoning reasoning") {
		t.Fatalf("summary should not include reasoning content")
	}
	if capture.messages[len(capture.messages)-1].Content != "latest prompt" {
		t.Fatalf("latest prompt not preserved: %#v", capture.messages)
	}
	if len(next) < len(capture.messages) {
		t.Fatalf("returned messages should include compacted context and answer")
	}
}

func TestCompactMessagesPreservesAgentsContextPrefix(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nproject rules\n</INSTRUCTIONS>"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest"},
	}
	compacted, ok := compactMessages(messages, cfg, 100)
	if !ok {
		t.Fatal("expected compaction")
	}
	if len(compacted) < 4 || compacted[1].Content != messages[1].Content {
		t.Fatalf("AGENTS context not preserved: %#v", compacted)
	}
	if !strings.HasPrefix(compacted[2].Content, compactionMarker) {
		t.Fatalf("summary should be after AGENTS context: %#v", compacted)
	}
}

func TestCompactMessagesPreservesProfileSystemPrefix(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "base system"},
		{Role: protocol.RoleSystem, Content: "# Billyharness profile: billy\n\n<SOUL>\nprofile rules\n</SOUL>"},
		{Role: protocol.RoleUser, Content: "# AGENTS.md instructions\n\n<INSTRUCTIONS>\nproject rules\n</INSTRUCTIONS>"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest"},
	}
	compacted, ok := compactMessages(messages, cfg, 100)
	if !ok {
		t.Fatal("expected compaction")
	}
	if len(compacted) < 5 ||
		compacted[0].Content != messages[0].Content ||
		compacted[1].Content != messages[1].Content ||
		compacted[2].Content != messages[2].Content {
		t.Fatalf("protected prefix not preserved: %#v", compacted)
	}
	if !strings.HasPrefix(compacted[3].Content, compactionMarker) {
		t.Fatalf("summary should be after protected prefix: %#v", compacted)
	}
}

func TestCompactMessagesPreservesToolAdjacency(t *testing.T) {
	cfg := config.Default()
	cfg.ContextCompactTokens = 1
	cfg.ContextCompactKeep = 2
	cfg.ContextCompactMaxChars = 2000
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: protocol.RoleAssistant, ToolCalls: []protocol.ToolCall{{
			ID:        "call_1",
			Name:      "fs_read",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		}}},
		{Role: protocol.RoleTool, ToolCallID: "call_1", Name: "fs_read", Content: "readme"},
		{Role: protocol.RoleUser, Content: "continue"},
	}
	compacted, ok := compactMessages(messages, cfg, 10)
	if !ok {
		t.Fatalf("expected compaction")
	}
	var assistantWithTool int
	for i, msg := range compacted {
		if msg.Role == protocol.RoleAssistant && len(msg.ToolCalls) > 0 {
			assistantWithTool = i
			break
		}
	}
	if assistantWithTool == 0 {
		t.Fatalf("assistant tool call should be preserved in tail: %#v", compacted)
	}
	if assistantWithTool+1 >= len(compacted) || compacted[assistantWithTool+1].Role != protocol.RoleTool {
		t.Fatalf("tool result should stay adjacent to assistant tool call: %#v", compacted)
	}
}

func TestRunMessagesExecutesToolAndContinuesLoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 3
	cfg.AutoApproveDangerous = true
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"hello"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "write file"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello" {
		t.Fatalf("written content = %q", bytes)
	}
	if len(next) < 5 || next[len(next)-1].Content != "finished" {
		t.Fatalf("messages = %#v", next)
	}
	if !sawEvent(events, protocol.EventToolCallRequested) || !sawEvent(events, protocol.EventToolCallFinished) || !sawEvent(events, protocol.EventRunCompleted) {
		t.Fatalf("events missing tool/run completion: %#v", events)
	}
	if result, ok := firstToolResult(events); !ok || result.Name != "fs_write_file" || result.CallID != "call_1" || result.IsError {
		t.Fatalf("tool result event = %#v ok=%v", result, ok)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
}

func TestRunMessagesExecutesParallelSafeToolsConcurrentlyAndPreservesOrder(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 3
	cfg.MaxParallelTools = 2
	registry := tools.NewRegistry(cfg)
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_a",
			Description: "Wait for slow_b.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(startedA)
			select {
			case <-startedB:
				return tools.Result{Content: "A"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.Tool{
		Spec: protocol.ToolSpec{
			Name:        "slow_b",
			Description: "Wait for slow_a.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			close(startedB)
			select {
			case <-startedA:
				return tools.Result{Content: "B"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_a", ToolName: "slow_a", ArgsDelta: `{}`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 1, ToolID: "call_b", ToolName: "slow_b", ArgsDelta: `{}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := a.RunMessages(ctx, []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "run tools"},
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	var toolMessages []protocol.Message
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && (msg.Name == "slow_a" || msg.Name == "slow_b") {
			toolMessages = append(toolMessages, msg)
		}
	}
	if len(toolMessages) != 2 {
		t.Fatalf("tool messages = %#v", toolMessages)
	}
	if toolMessages[0].Name != "slow_a" || toolMessages[0].Content != "A" ||
		toolMessages[1].Name != "slow_b" || toolMessages[1].Content != "B" {
		t.Fatalf("tool message order/content = %#v", toolMessages)
	}
}

func TestRunMessagesExecutesMCPToolAndContinuesLoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolRounds = 3
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestAgentFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_AGENT_MCP_HELPER": "1"},
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		Enabled:        true,
	}}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	prov := &scriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_mcp", ToolName: "mcp_call", ArgsDelta: `{"name":"mcp__fake__echo","arguments":{"text":"mcp ok"}}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "finished"},
			{Kind: provider.EventDone},
		},
	}}
	a := New(cfg, prov, registry)
	var events []protocol.Event
	next, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "use mcp"},
	}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d", prov.calls)
	}
	if hasToolSpec(prov.lastTools, "mcp__fake__echo") || !hasToolSpec(prov.lastTools, "mcp_call") {
		t.Fatalf("provider saw wrong MCP tools: %#v", prov.lastTools)
	}
	if !sawToolStarted(events, "mcp_call") {
		t.Fatalf("MCP tool start event missing: %#v", events)
	}
	var sawToolMessage bool
	for _, msg := range next {
		if msg.Role == protocol.RoleTool && msg.Name == "mcp_call" && msg.Content == "mcp ok" {
			sawToolMessage = true
		}
	}
	if !sawToolMessage {
		t.Fatalf("MCP tool result not in messages: %#v", next)
	}
	if !hasMCPInstructions(next) {
		t.Fatalf("MCP instructions not preserved in messages: %#v", next)
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

func TestRunMessagesEmitsRunFailedForInvalidToolArguments(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	prov := &scriptedProvider{repeat: []provider.Event{
		{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{bad`},
		{Kind: provider.EventDone},
	}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "bad tool"},
	}, func(event protocol.Event) {
		if event.Type == protocol.EventRunFailed {
			failed = true
		}
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v", err)
	}
	if !failed {
		t.Fatalf("run.failed event not emitted")
	}
}

func TestRunMessagesEmitsRunFailedOnProviderError(t *testing.T) {
	cfg := config.Default()
	cfg.MaxToolRounds = 1
	wantErr := errors.New("provider exploded")
	prov := &scriptedProvider{err: wantErr}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var failed bool
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "fail"},
	}, func(event protocol.Event) {
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
}

type captureProvider struct {
	messages []protocol.Message
}

func (p *captureProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.messages = append([]protocol.Message(nil), req.Messages...)
	events := make(chan provider.Event, 2)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		events <- provider.Event{Kind: provider.EventContent, Text: "done"}
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
}

func (p *scriptedProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	p.calls++
	p.lastTools = req.Tools
	events := make(chan provider.Event, 8)
	errs := make(chan error, 1)
	call := p.calls
	go func() {
		defer close(events)
		defer close(errs)
		if p.err != nil {
			errs <- p.err
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
				errs <- ctx.Err()
				return
			}
		}
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

func sawToolStarted(events []protocol.Event, name string) bool {
	for _, event := range events {
		if event.Type == protocol.EventToolCallStarted && fmt.Sprint(event.Data) == name {
			return true
		}
	}
	return false
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
