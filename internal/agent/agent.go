package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/billyhargroveofficial/billyharness/internal/config"
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
	messages := InitialMessages()
	messages = append(messages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	_, err := a.RunMessages(ctx, messages, emit)
	return err
}

func InitialMessages() []protocol.Message {
	return []protocol.Message{{Role: protocol.RoleSystem, Content: systemPrompt()}}
}

func (a *Agent) RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
	emit(protocol.Event{Type: protocol.EventRunStarted})
	for round := 0; round < a.cfg.MaxToolRounds; round++ {
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
			ReasoningContent: reasoning,
			ToolCalls:        calls,
		})
		for _, call := range calls {
			emit(protocol.Event{Type: protocol.EventToolCallRequested, Data: call})
			emit(protocol.Event{Type: protocol.EventToolCallStarted, Data: call.Name})
			result, err := a.tools.Call(ctx, call)
			if err != nil {
				result.Content = "tool error: " + err.Error()
			}
			emit(protocol.Event{Type: protocol.EventToolCallFinished, Data: result.Content})
			messages = append(messages, protocol.Message{
				Role:       protocol.RoleTool,
				Content:    result.Content,
				ToolCallID: call.ID,
				Name:       call.Name,
			})
		}
	}
	return messages, fmt.Errorf("exceeded max tool rounds: %d", a.cfg.MaxToolRounds)
}

func systemPrompt() string {
	return "You are a fast coding and research agent. Use tools when useful. Keep final answers concise. Never reveal secrets."
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
