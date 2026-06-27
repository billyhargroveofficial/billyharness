package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/instructions"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Agent struct {
	cfg      config.Config
	provider provider.Provider
	tools    *tools.Registry
}

func New(cfg config.Config, provider provider.Provider, registry *tools.Registry) *Agent {
	return &Agent{cfg: cfg, provider: provider, tools: registry}
}

func (a *Agent) Run(ctx context.Context, prompt string, emit func(protocol.Event)) error {
	messages := InitialMessages(a.cfg)
	messages = append(messages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	_, err := a.RunMessages(ctx, messages, emit)
	return err
}

func InitialMessages(cfgs ...config.Config) []protocol.Message {
	messages := []protocol.Message{{Role: protocol.RoleSystem, Content: systemPrompt()}}
	if len(cfgs) > 0 {
		if msg, ok := instructions.ProfileMessage(cfgs[0]); ok {
			messages = append(messages, msg)
		}
		if msg, ok := instructions.Message(cfgs[0]); ok {
			messages = append(messages, msg)
		}
	}
	return messages
}

func (a *Agent) RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
	emit(protocol.Event{Type: protocol.EventRunStarted})
	messages = a.withMCPInstructions(messages)
	var lastPromptTokens int64
	for round := 0; round < a.cfg.MaxToolRounds; round++ {
		var compacted bool
		messages, compacted = compactMessages(messages, a.cfg, lastPromptTokens)
		if compacted {
			lastPromptTokens = 0
			emit(protocol.Event{Type: protocol.EventContextCompacted, Data: compactionEventData(messages)})
		}
		emit(protocol.Event{Type: protocol.EventModelCallStarted, Data: map[string]any{"round": round + 1}})
		events, errs := a.provider.Stream(ctx, provider.Request{
			Model:    a.cfg.Model,
			Messages: messages,
			Tools:    a.tools.Specs(),
		})
		var content string
		var reasoning string
		var acc provider.ToolAccumulator
		for event := range events {
			switch event.Kind {
			case provider.EventContent:
				content += event.Text
				emit(protocol.Event{Type: protocol.EventAssistantDelta, Data: event.Text})
			case provider.EventReasoning:
				reasoning += event.Text
				emit(protocol.Event{Type: protocol.EventAssistantReasoning, Data: event.Text})
			case provider.EventToolCallDelta:
				acc.Push(event)
			case provider.EventUsage:
				if event.Usage.InputTokens > 0 {
					lastPromptTokens = event.Usage.InputTokens
				}
				emit(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: event.Usage})
			case provider.EventDone:
			}
		}
		if err := <-errs; err != nil {
			emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
			return messages, err
		}
		emit(protocol.Event{Type: protocol.EventModelCallFinished, Data: map[string]any{"round": round + 1}})
		calls, err := acc.Finish()
		if err != nil {
			emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
			return messages, err
		}
		if len(calls) == 0 {
			messages = append(messages, protocol.Message{
				Role:             protocol.RoleAssistant,
				Content:          content,
				ReasoningContent: optionalReasoning(a.cfg, reasoning),
			})
			emit(protocol.Event{Type: protocol.EventRunCompleted})
			return messages, nil
		}
		messages = append(messages, protocol.Message{
			Role:             protocol.RoleAssistant,
			Content:          content,
			ReasoningContent: optionalReasoning(a.cfg, reasoning),
			ToolCalls:        calls,
		})
		results := a.executeToolCalls(ctx, calls, emit)
		for _, result := range results {
			messages = append(messages, protocol.Message{
				Role:       protocol.RoleTool,
				Content:    result.Result.Content,
				ToolCallID: result.Call.ID,
				Name:       result.Call.Name,
			})
		}
	}
	err := fmt.Errorf("exceeded max tool rounds: %d", a.cfg.MaxToolRounds)
	emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
	return messages, err
}

type toolExecutionResult struct {
	Index  int
	Call   protocol.ToolCall
	Result protocol.ToolResult
}

func (a *Agent) executeToolCalls(ctx context.Context, calls []protocol.ToolCall, emit func(protocol.Event)) []toolExecutionResult {
	results := make([]toolExecutionResult, len(calls))
	for _, call := range calls {
		emit(protocol.Event{Type: protocol.EventToolCallRequested, Data: call})
	}
	for i := 0; i < len(calls); {
		if !a.canRunToolParallel(calls[i]) {
			results[i] = a.executeOneTool(ctx, i, calls[i], emit)
			i++
			continue
		}
		j := i + 1
		for j < len(calls) && a.canRunToolParallel(calls[j]) {
			j++
		}
		a.executeParallelToolBatch(ctx, calls, i, j, results, emit)
		i = j
	}
	return results
}

func (a *Agent) executeParallelToolBatch(ctx context.Context, calls []protocol.ToolCall, start, end int, results []toolExecutionResult, emit func(protocol.Event)) {
	limit := a.cfg.MaxParallelTools
	if limit <= 1 || end-start == 1 {
		for i := start; i < end; i++ {
			results[i] = a.executeOneTool(ctx, i, calls[i], emit)
		}
		return
	}
	if limit > end-start {
		limit = end - start
	}
	for i := start; i < end; i++ {
		emit(protocol.Event{Type: protocol.EventToolCallStarted, Data: calls[i].Name})
	}
	jobs := make(chan int)
	done := make(chan toolExecutionResult, end-start)
	var wg sync.WaitGroup
	for worker := 0; worker < limit; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				done <- a.callTool(ctx, idx, calls[idx])
			}
		}()
	}
	go func() {
		for i := start; i < end; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(done)
	}()
	for result := range done {
		results[result.Index] = result
		emit(protocol.Event{Type: protocol.EventToolCallFinished, Data: result.Result})
	}
}

func (a *Agent) executeOneTool(ctx context.Context, index int, call protocol.ToolCall, emit func(protocol.Event)) toolExecutionResult {
	emit(protocol.Event{Type: protocol.EventToolCallStarted, Data: call.Name})
	result := a.callTool(ctx, index, call)
	emit(protocol.Event{Type: protocol.EventToolCallFinished, Data: result.Result})
	return result
}

func (a *Agent) callTool(ctx context.Context, index int, call protocol.ToolCall) toolExecutionResult {
	result, err := a.tools.Call(ctx, call)
	out := protocol.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Content:   result.Content,
		IsError:   result.IsError,
		ErrorCode: result.ErrorCode,
		Metadata:  result.Metadata,
		Truncated: result.Truncated,
		OutputRef: result.OutputRef,
	}
	if err != nil {
		out.IsError = true
		if out.ErrorCode == "" {
			out.ErrorCode = "tool_error"
		}
		if out.Content == "" {
			out.Content = err.Error()
		}
	}
	return toolExecutionResult{Index: index, Call: call, Result: out}
}

func (a *Agent) canRunToolParallel(call protocol.ToolCall) bool {
	return a != nil && a.tools != nil && a.cfg.MaxParallelTools > 1 && a.tools.CanRunParallel(call.Name)
}

func (a *Agent) withMCPInstructions(messages []protocol.Message) []protocol.Message {
	if a == nil || a.tools == nil {
		return messages
	}
	instructions := a.tools.Instructions()
	if len(instructions) == 0 || hasMCPInstructions(messages) {
		return messages
	}
	content := "# MCP server instructions\n\n" + strings.Join(instructions, "\n\n")
	insertAt := protectedPrefixEnd(messages)
	next := make([]protocol.Message, 0, len(messages)+1)
	next = append(next, messages[:insertAt]...)
	next = append(next, protocol.Message{Role: protocol.RoleUser, Content: content})
	next = append(next, messages[insertAt:]...)
	return next
}

func hasMCPInstructions(messages []protocol.Message) bool {
	for _, msg := range messages {
		if msg.Role == protocol.RoleUser && strings.HasPrefix(msg.Content, "# MCP server instructions") {
			return true
		}
	}
	return false
}

func systemPrompt() string {
	return strings.Join([]string{
		"You are a fast coding and research agent. Use tools when useful. Keep final answers concise. Never reveal secrets.",
		"",
		"Format final answers with simple Markdown that remains readable in a terminal TUI and Telegram rich messages.",
		"Supported Markdown: short paragraphs, headings, bullet lists, numbered lists, blockquotes, inline code, fenced code blocks, bold, italic, plain links, simple pipe tables, and LaTeX math.",
		"Use LaTeX for mathematical formulas: prefer inline $...$ for short formulas and display $$...$$ for important formulas. Do not put math formulas in code fences.",
		"Do not use HTML, images, Mermaid diagrams, footnotes, task-list checkboxes, or other Markdown extensions unless the user explicitly asks for them.",
		"Prefer fenced code blocks with a language tag for code, logs, and commands.",
		"Keep non-math formatting simple enough to remain readable when ANSI styling is unavailable.",
		"Connected MCP servers are exposed lazily through mcp_list_tools and mcp_call; use them only when the user asks for those external services.",
		"If the user mentions Parilka, парилка, парилке, or asks what is happening there, treat it as the Telegram Parilka chat. Use mcp_list_tools with server \"telegram-parilka\" and then mcp_call. Do not search the filesystem or run shell commands for Parilka chat context.",
	}, "\n")
}

func optionalReasoning(cfg config.Config, reasoning string) string {
	if cfg.StoreReasoningContent {
		return reasoning
	}
	return ""
}

func PrettyEvent(event protocol.Event) string {
	bytes, _ := json.Marshal(event)
	return string(bytes)
}
