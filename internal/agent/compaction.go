package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const compactionMarker = "Conversation summary (auto-compact)"

const (
	defaultContextCompactKeep     = 32
	defaultContextCompactMaxChars = 120_000
)

type compactionPolicy struct {
	ThresholdTokens int64
	KeepMessages    int
	MaxSummaryChars int
}

type compactionReport struct {
	CompactionID             string                          `json:"compaction_id"`
	Reason                   string                          `json:"reason"`
	TriggerSource            string                          `json:"trigger_source"`
	TriggerPromptTokens      int64                           `json:"trigger_prompt_tokens"`
	ThresholdTokens          int64                           `json:"threshold_tokens"`
	KeepMessages             int                             `json:"keep_messages"`
	MaxSummaryChars          int                             `json:"max_summary_chars"`
	ProtectedPrefix          compactionProtectedPrefixReport `json:"protected_prefix"`
	ProtectedPrefixMessages  int                             `json:"protected_prefix_messages"`
	ProtectedPrefixChars     int                             `json:"protected_prefix_chars"`
	ProtectedPrefixTokens    int64                           `json:"protected_prefix_estimated_tokens"`
	CompactedMessages        int                             `json:"compacted_messages"`
	CompactedChars           int                             `json:"compacted_chars"`
	CompactedEstimatedTokens int64                           `json:"compacted_estimated_tokens"`
	ActiveMessages           int                             `json:"active_messages"`
	ActiveChars              int                             `json:"active_chars"`
	ActiveEstimatedTokens    int64                           `json:"active_estimated_tokens"`
	SummaryChars             int                             `json:"summary_chars"`
	SummaryEstimatedTokens   int64                           `json:"summary_estimated_tokens"`
	TopContextContributors   []compactionContributor         `json:"top_context_contributors,omitempty"`
}

type compactionProtectedPrefixReport struct {
	EndIndex        int                             `json:"end_index"`
	Messages        int                             `json:"messages"`
	Chars           int                             `json:"chars"`
	EstimatedTokens int64                           `json:"estimated_tokens"`
	Reasons         map[string]int                  `json:"reasons,omitempty"`
	Entries         []compactionProtectedPrefixItem `json:"entries,omitempty"`
}

type compactionProtectedPrefixItem struct {
	Index           int    `json:"index"`
	Role            string `json:"role"`
	Reason          string `json:"reason"`
	Chars           int    `json:"chars"`
	EstimatedTokens int64  `json:"estimated_tokens"`
}

type compactionContributor struct {
	Index           int    `json:"index"`
	Role            string `json:"role"`
	Source          string `json:"source"`
	Name            string `json:"name,omitempty"`
	Chars           int    `json:"chars"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	Preview         string `json:"preview,omitempty"`
}

func compactMessages(messages []protocol.Message, cfg config.Config, observedPromptTokens int64) ([]protocol.Message, *compactionReport, bool) {
	policy := effectiveCompactionPolicy(cfg)
	if policy.ThresholdTokens <= 0 || len(messages) < 4 {
		return messages, nil, false
	}
	triggerTokens := observedPromptTokens
	triggerSource := "provider_usage"
	if triggerTokens <= 0 {
		triggerTokens = estimateMessagesTokens(messages)
		triggerSource = "estimated_messages"
	}
	if triggerTokens < policy.ThresholdTokens {
		return messages, nil, false
	}
	protected := protectedPrefix(messages)
	cut := compactCutIndex(messages, protected.EndIndex, policy.KeepMessages)
	if cut <= protected.EndIndex || cut >= len(messages) {
		return messages, nil, false
	}
	compacted := messages[protected.EndIndex:cut]
	id := compactionID(compacted, triggerTokens, policy.ThresholdTokens)
	summary := buildCompactionSummary(compacted, policy.MaxSummaryChars, id)
	out := make([]protocol.Message, 0, protected.EndIndex+1+len(messages)-cut)
	out = append(out, messages[:protected.EndIndex]...)
	out = append(out, summary)
	out = append(out, messages[cut:]...)
	report := newCompactionReport(id, policy, triggerTokens, triggerSource, protected, compacted, summary, out, messages)
	return out, report, true
}

func effectiveCompactionPolicy(cfg config.Config) compactionPolicy {
	keep := cfg.ContextCompactKeep
	if keep <= 0 {
		keep = defaultContextCompactKeep
	}
	maxChars := cfg.ContextCompactMaxChars
	if maxChars <= 0 {
		maxChars = defaultContextCompactMaxChars
	}
	return compactionPolicy{
		ThresholdTokens: int64(cfg.ContextCompactTokens),
		KeepMessages:    keep,
		MaxSummaryChars: maxChars,
	}
}

func protectedPrefixEnd(messages []protocol.Message) int {
	return protectedPrefix(messages).EndIndex
}

func protectedPrefix(messages []protocol.Message) compactionProtectedPrefixReport {
	report := compactionProtectedPrefixReport{Reasons: map[string]int{}}
	end := 0
	for end < len(messages) && messages[end].Role == protocol.RoleSystem && !isCompactionSummary(messages[end]) {
		report.add(end, messages[end], protectedSystemReason(end, messages[end]))
		end++
	}
	for end < len(messages) && isContextInstructionMessage(messages[end]) {
		report.add(end, messages[end], protectedContextInstructionReason(messages[end]))
		end++
	}
	report.EndIndex = end
	if len(report.Reasons) == 0 {
		report.Reasons = nil
	}
	return report
}

func isCompactionSummary(msg protocol.Message) bool {
	return msg.Role == protocol.RoleSystem && strings.HasPrefix(strings.TrimSpace(msg.Content), compactionMarker)
}

func isContextInstructionMessage(msg protocol.Message) bool {
	return protectedContextInstructionReason(msg) != ""
}

func protectedSystemReason(index int, msg protocol.Message) string {
	content := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(content, "# Billyharness profile:") || strings.Contains(content, "<SOUL>") {
		return "profile_soul"
	}
	if index == 0 {
		return "system_prompt"
	}
	return "system_context"
}

func protectedContextInstructionReason(msg protocol.Message) string {
	if msg.Role != protocol.RoleUser {
		return ""
	}
	content := strings.TrimSpace(msg.Content)
	switch {
	case strings.HasPrefix(content, "# AGENTS.md instructions"):
		return "agents_instructions"
	case strings.HasPrefix(content, "# MCP server instructions"):
		return "mcp_instructions"
	default:
		return ""
	}
}

func (r *compactionProtectedPrefixReport) add(index int, msg protocol.Message, reason string) {
	if reason == "" {
		reason = "protected_prefix"
	}
	chars := messageCompactionChars(msg)
	tokens := estimateMessagesTokens([]protocol.Message{msg})
	r.Messages++
	r.Chars += chars
	r.EstimatedTokens += tokens
	r.Reasons[reason]++
	r.Entries = append(r.Entries, compactionProtectedPrefixItem{
		Index:           index,
		Role:            string(msg.Role),
		Reason:          reason,
		Chars:           chars,
		EstimatedTokens: tokens,
	})
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

func buildCompactionSummary(messages []protocol.Message, maxChars int, id string) protocol.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", compactionMarker)
	fmt.Fprintf(&b, "Earlier conversation context was compacted before the next model call.\n")
	fmt.Fprintf(&b, "Compaction id: %s; compacted messages: %d.\n", id, len(messages))
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

func compactionID(messages []protocol.Message, triggerTokens, threshold int64) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "trigger=%d threshold=%d messages=%d\n", triggerTokens, threshold, len(messages))
	for _, msg := range messages {
		fmt.Fprintf(hash, "role=%s name=%s tool_call_id=%s content=%s\n", msg.Role, msg.Name, msg.ToolCallID, msg.Content)
		for _, call := range msg.ToolCalls {
			fmt.Fprintf(hash, "call=%s name=%s args=%s\n", call.ID, call.Name, string(call.Arguments))
		}
	}
	sum := hash.Sum(nil)
	return hex.EncodeToString(sum[:6])
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
		chars += int64(messageCompactionChars(msg))
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

func messageCompactionChars(msg protocol.Message) int {
	chars := len(msg.Content) + len(msg.Name) + len(msg.ToolCallID) + len(string(msg.Role))
	for _, call := range msg.ToolCalls {
		chars += len(call.ID) + len(call.Name) + len(call.Arguments)
	}
	return chars
}

func messagesCompactionChars(messages []protocol.Message) int {
	var chars int
	for _, msg := range messages {
		chars += messageCompactionChars(msg)
	}
	return chars
}

func newCompactionReport(
	id string,
	policy compactionPolicy,
	triggerTokens int64,
	triggerSource string,
	protected compactionProtectedPrefixReport,
	compacted []protocol.Message,
	summary protocol.Message,
	active []protocol.Message,
	before []protocol.Message,
) *compactionReport {
	return &compactionReport{
		CompactionID:             id,
		Reason:                   "prompt_tokens_at_or_above_threshold",
		TriggerSource:            triggerSource,
		TriggerPromptTokens:      triggerTokens,
		ThresholdTokens:          policy.ThresholdTokens,
		KeepMessages:             policy.KeepMessages,
		MaxSummaryChars:          policy.MaxSummaryChars,
		ProtectedPrefix:          protected,
		ProtectedPrefixMessages:  protected.Messages,
		ProtectedPrefixChars:     protected.Chars,
		ProtectedPrefixTokens:    protected.EstimatedTokens,
		CompactedMessages:        len(compacted),
		CompactedChars:           messagesCompactionChars(compacted),
		CompactedEstimatedTokens: estimateMessagesTokens(compacted),
		ActiveMessages:           len(active),
		ActiveChars:              messagesCompactionChars(active),
		ActiveEstimatedTokens:    estimateMessagesTokens(active),
		SummaryChars:             len(summary.Content),
		SummaryEstimatedTokens:   estimateMessagesTokens([]protocol.Message{summary}),
		TopContextContributors:   topCompactionContributors(before, 5),
	}
}

func topCompactionContributors(messages []protocol.Message, limit int) []compactionContributor {
	if limit <= 0 {
		return nil
	}
	out := make([]compactionContributor, 0, len(messages))
	for i, msg := range messages {
		chars := messageCompactionChars(msg)
		if chars == 0 {
			continue
		}
		out = append(out, compactionContributor{
			Index:           i,
			Role:            string(msg.Role),
			Source:          compactionMessageSource(msg),
			Name:            msg.Name,
			Chars:           chars,
			EstimatedTokens: int64((chars + 3) / 4),
			Preview:         truncateForCompaction(compactionMessagePreview(msg), 120),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EstimatedTokens == out[j].EstimatedTokens {
			return out[i].Index < out[j].Index
		}
		return out[i].EstimatedTokens > out[j].EstimatedTokens
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func compactionMessageSource(msg protocol.Message) string {
	switch msg.Role {
	case protocol.RoleSystem:
		if isCompactionSummary(msg) {
			return "compaction_summary"
		}
		return "system_instructions"
	case protocol.RoleUser:
		return "user_messages"
	case protocol.RoleAssistant:
		if len(msg.ToolCalls) > 0 {
			return "assistant_tool_calls"
		}
		return "assistant_messages"
	case protocol.RoleTool:
		name := strings.ToLower(strings.TrimSpace(msg.Name))
		switch {
		case strings.HasPrefix(name, "web_"):
			return "web_summaries"
		case strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp "):
			return "mcp_outputs"
		default:
			return "tool_outputs"
		}
	default:
		return string(msg.Role)
	}
}

func compactionMessagePreview(msg protocol.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}
	if len(msg.ToolCalls) == 0 {
		return ""
	}
	calls := make([]string, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		args := strings.TrimSpace(string(call.Arguments))
		if args != "" {
			calls = append(calls, call.Name+" "+args)
		} else {
			calls = append(calls, call.Name)
		}
	}
	return strings.Join(calls, "; ")
}
