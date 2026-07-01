package agent

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/instructions"
	"github.com/billyhargroveofficial/billyharness/internal/memory"
	"github.com/billyhargroveofficial/billyharness/internal/projectcontext"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func InitialMessages(cfgs ...config.Config) []protocol.Message {
	if len(cfgs) > 0 {
		return InitialMessagesFromSettings(cfgs[0].InstructionSettings())
	}
	return InitialMessagesFromSettings(config.InstructionSettings{})
}

func InitialMessagesFromSettings(settings config.InstructionSettings) []protocol.Message {
	messages := []protocol.Message{{Role: protocol.RoleSystem, Content: systemPrompt()}}
	if msg, ok := instructions.ProfileMessage(settings); ok {
		messages = append(messages, msg)
	}
	if msg, ok := memory.Message(settings); ok {
		messages = append(messages, msg)
	}
	if msg, ok := projectcontext.Message(settings); ok {
		messages = append(messages, msg)
	}
	if msg, ok := instructions.Message(settings); ok {
		messages = append(messages, msg)
	}
	return messages
}

func (a *Agent) appendModelResponse(messages []protocol.Message, step modelCallStepResult) []protocol.Message {
	msg := protocol.Message{
		Role:             protocol.RoleAssistant,
		Content:          step.Content,
		ReasoningContent: optionalReasoning(a.toolPolicy.StoreReasoningContent, step.Reasoning),
	}
	if len(step.ToolCalls) > 0 {
		msg.ToolCalls = step.ToolCalls
	}
	return append(messages, msg)
}

func appendToolResultMessages(messages []protocol.Message, results []toolExecutionResult) []protocol.Message {
	for _, result := range results {
		messages = append(messages, protocol.Message{
			Role:       protocol.RoleTool,
			Content:    result.Result.Content,
			ToolCallID: result.Call.ID,
			Name:       result.Call.Name,
		})
	}
	return messages
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
		"Native web_fetch, web_extract, and web_crawl return compact digests plus output_ref files for full extracted text. Prefer the digest/extract fields. Large shell, filesystem, diagnostics, and MCP tool outputs may also return bounded previews with output_ref files. Read output_ref only when exact quotes, exact source text, logs, or deeper evidence are necessary. Do not request include_text/full_text unless the user explicitly needs exact source text.",
	}, "\n")
}

func optionalReasoning(store bool, reasoning string) string {
	if store {
		return reasoning
	}
	return ""
}
