package agent

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const compactionMarker = "Conversation summary (auto-compact)"

func compactMessages(messages []protocol.Message, cfg config.Config, observedPromptTokens int64) ([]protocol.Message, bool) {
	threshold := int64(cfg.ContextCompactTokens)
	if threshold <= 0 || len(messages) < 4 {
		return messages, false
	}
	triggerTokens := observedPromptTokens
	if triggerTokens <= 0 {
		triggerTokens = estimateMessagesTokens(messages)
	}
	if triggerTokens < threshold {
		return messages, false
	}
	prefixEnd := 0
	prefixEnd = protectedPrefixEnd(messages)
	keep := cfg.ContextCompactKeep
	if keep <= 0 {
		keep = 32
	}
	cut := compactCutIndex(messages, prefixEnd, keep)
	if cut <= prefixEnd || cut >= len(messages) {
		return messages, false
	}
	maxChars := cfg.ContextCompactMaxChars
	if maxChars <= 0 {
		maxChars = 120_000
	}
	summary := buildCompactionSummary(messages[prefixEnd:cut], maxChars, triggerTokens, threshold)
	out := make([]protocol.Message, 0, prefixEnd+1+len(messages)-cut)
	out = append(out, messages[:prefixEnd]...)
	out = append(out, summary)
	out = append(out, messages[cut:]...)
	return out, true
}

func protectedPrefixEnd(messages []protocol.Message) int {
	end := 0
	for end < len(messages) && messages[end].Role == protocol.RoleSystem && !isCompactionSummary(messages[end]) {
		end++
	}
	for end < len(messages) && isContextInstructionMessage(messages[end]) {
		end++
	}
	return end
}

func isCompactionSummary(msg protocol.Message) bool {
	return msg.Role == protocol.RoleSystem && strings.HasPrefix(strings.TrimSpace(msg.Content), compactionMarker)
}

func isContextInstructionMessage(msg protocol.Message) bool {
	if msg.Role != protocol.RoleUser {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, "# AGENTS.md instructions") ||
		strings.HasPrefix(content, "# MCP server instructions")
}

func compactCutIndex(messages []protocol.Message, prefixEnd, keep int) int {
	cut := len(messages) - keep
	if cut <= prefixEnd {
		return -1
	}
	for cut > prefixEnd && messages[cut].Role == protocol.RoleTool {
		cut--
	}
	if cut <= prefixEnd {
		return -1
	}
	return cut
}

func buildCompactionSummary(messages []protocol.Message, maxChars int, triggerTokens, threshold int64) protocol.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", compactionMarker)
	fmt.Fprintf(&b, "Earlier conversation context was compacted before the next model call.\n")
	fmt.Fprintf(&b, "Trigger prompt tokens: %d; threshold: %d; compacted messages: %d.\n", triggerTokens, threshold, len(messages))
	b.WriteString("Do not treat omitted assistant reasoning as missing user intent. Continue from the preserved recent messages.\n\n")
	b.WriteString("Compacted transcript:\n")
	for i, msg := range messages {
		entry := compactMessageEntry(i+1, msg)
		if b.Len()+len(entry)+1 > maxChars {
			fmt.Fprintf(&b, "... truncated compacted transcript at %d chars\n", maxChars)
			break
		}
		b.WriteString(entry)
		b.WriteByte('\n')
	}
	return protocol.Message{Role: protocol.RoleSystem, Content: b.String()}
}

func compactMessageEntry(index int, msg protocol.Message) string {
	role := string(msg.Role)
	if msg.Name != "" {
		role += "/" + msg.Name
	}
	var parts []string
	if content := strings.TrimSpace(msg.Content); content != "" {
		parts = append(parts, truncateForCompaction(content, 1200))
	}
	if len(msg.ToolCalls) > 0 {
		var calls []string
		for _, call := range msg.ToolCalls {
			args := strings.TrimSpace(string(call.Arguments))
			if args != "" {
				calls = append(calls, call.Name+" "+truncateForCompaction(args, 400))
			} else {
				calls = append(calls, call.Name)
			}
		}
		parts = append(parts, "tool calls: "+strings.Join(calls, "; "))
	}
	if len(parts) == 0 {
		parts = append(parts, "(empty)")
	}
	return fmt.Sprintf("%03d %s: %s", index, role, strings.Join(parts, " | "))
}

func truncateForCompaction(text string, maxChars int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return text[:maxChars]
	}
	return text[:maxChars-3] + "..."
}

func estimateMessagesTokens(messages []protocol.Message) int64 {
	var chars int64
	for _, msg := range messages {
		chars += int64(len(msg.Content) + len(msg.Name) + len(msg.ToolCallID) + len(string(msg.Role)))
		for _, call := range msg.ToolCalls {
			chars += int64(len(call.ID) + len(call.Name) + len(call.Arguments))
		}
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

func compactionEventData(messages []protocol.Message) map[string]any {
	data := map[string]any{"active_messages": len(messages)}
	for _, msg := range messages {
		if msg.Role == protocol.RoleSystem && strings.HasPrefix(msg.Content, compactionMarker) {
			data["summary_chars"] = len(msg.Content)
			for _, line := range strings.Split(msg.Content, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Trigger prompt tokens:") {
					data["detail"] = line
					break
				}
			}
			return data
		}
	}
	return data
}
