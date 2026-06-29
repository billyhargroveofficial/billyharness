package clientux

import (
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func BuildContextResponse(limits config.RuntimeLimits, id string, messages []protocol.Message) gatewayapi.SessionContextResponse {
	estimatedTokens := estimateMessagesTokens(messages)
	contextWindow := limits.ContextWindowTokens
	compactAt := int64(limits.ContextCompactTokens)
	var percentUsed float64
	if contextWindow > 0 {
		percentUsed = float64(estimatedTokens) / float64(contextWindow) * 100
	}
	var thresholdPercent float64
	if contextWindow > 0 && compactAt > 0 {
		thresholdPercent = float64(compactAt) / float64(contextWindow) * 100
	}
	sourceStats := map[string]*gatewayapi.ContextSource{}
	contributors := make([]gatewayapi.ContextContributor, 0, len(messages))
	for i, msg := range messages {
		chars := messageChars(msg)
		if chars == 0 {
			continue
		}
		source := contextSource(msg)
		tokens := messageTokens(msg)
		for _, contribution := range contextSourceContributions(msg) {
			stat := sourceStats[contribution.source]
			if stat == nil {
				stat = &gatewayapi.ContextSource{Source: contribution.source}
				sourceStats[contribution.source] = stat
			}
			stat.MessageCount++
			stat.Chars += contribution.chars
			stat.EstimatedTokens += int64((contribution.chars + 3) / 4)
		}
		contributors = append(contributors, gatewayapi.ContextContributor{
			Index:           i,
			Role:            string(msg.Role),
			Source:          source,
			Name:            msg.Name,
			Chars:           chars,
			EstimatedTokens: tokens,
			Preview:         previewMessage(messagePreviewText(msg), 120),
		})
	}
	sort.Slice(contributors, func(i, j int) bool {
		if contributors[i].EstimatedTokens == contributors[j].EstimatedTokens {
			return contributors[i].Index < contributors[j].Index
		}
		return contributors[i].EstimatedTokens > contributors[j].EstimatedTokens
	})
	if len(contributors) > 5 {
		contributors = contributors[:5]
	}
	sources := make([]gatewayapi.ContextSource, 0, len(sourceStats))
	for _, stat := range sourceStats {
		if estimatedTokens > 0 {
			stat.Percent = float64(stat.EstimatedTokens) / float64(estimatedTokens) * 100
		}
		sources = append(sources, *stat)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].EstimatedTokens == sources[j].EstimatedTokens {
			return sources[i].Source < sources[j].Source
		}
		return sources[i].EstimatedTokens > sources[j].EstimatedTokens
	})
	return gatewayapi.SessionContextResponse{
		ID:                      id,
		MessageCount:            len(messages),
		EstimatedTokens:         estimatedTokens,
		ContextWindowTokens:     contextWindow,
		ContextCompactTokens:    compactAt,
		PercentUsed:             percentUsed,
		CompactThresholdPercent: thresholdPercent,
		OverCompactThreshold:    compactAt > 0 && estimatedTokens >= compactAt,
		Estimator:               "chars_div_4",
		Sources:                 sources,
		Thresholds:              contextThresholds(estimatedTokens, contextWindow),
		TopContributors:         contributors,
	}
}

func contextThresholds(estimatedTokens, contextWindow int64) []gatewayapi.ContextThreshold {
	if contextWindow <= 0 {
		return nil
	}
	thresholds := []int{50, 70, 85, 95}
	out := make([]gatewayapi.ContextThreshold, 0, len(thresholds))
	for _, percent := range thresholds {
		tokens := (contextWindow*int64(percent) + 99) / 100
		remaining := tokens - estimatedTokens
		if remaining < 0 {
			remaining = 0
		}
		out = append(out, gatewayapi.ContextThreshold{
			Percent:         percent,
			Tokens:          tokens,
			Crossed:         estimatedTokens >= tokens,
			RemainingTokens: remaining,
		})
	}
	return out
}

func contextSource(msg protocol.Message) string {
	switch msg.Role {
	case protocol.RoleSystem:
		if strings.HasPrefix(strings.TrimSpace(msg.Content), "Conversation summary (auto-compact)") {
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

type contextSourceContribution struct {
	source string
	chars  int
}

func contextSourceContributions(msg protocol.Message) []contextSourceContribution {
	var out []contextSourceContribution
	baseChars := messageCharsWithoutReasoning(msg)
	if baseChars > 0 {
		out = append(out, contextSourceContribution{source: contextSource(msg), chars: baseChars})
	}
	if msg.ReasoningContent != "" {
		out = append(out, contextSourceContribution{source: "reasoning_summaries", chars: len(msg.ReasoningContent)})
	}
	return out
}

func messagePreviewText(msg protocol.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}
	if len(msg.ToolCalls) == 0 {
		return ""
	}
	var calls []string
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

func estimateMessagesTokens(messages []protocol.Message) int64 {
	var tokens int64
	for _, msg := range messages {
		tokens += messageTokens(msg)
	}
	return tokens
}

func messageTokens(msg protocol.Message) int64 {
	var tokens int64
	for _, contribution := range contextSourceContributions(msg) {
		tokens += int64((contribution.chars + 3) / 4)
	}
	return tokens
}

func messageChars(msg protocol.Message) int {
	chars := messageCharsWithoutReasoning(msg)
	if msg.ReasoningContent != "" {
		chars += len(msg.ReasoningContent)
	}
	return chars
}

func messageCharsWithoutReasoning(msg protocol.Message) int {
	chars := len(msg.Content) + len(msg.Name) + len(msg.ToolCallID) + len(string(msg.Role))
	for _, call := range msg.ToolCalls {
		chars += len(call.ID) + len(call.Name) + len(call.Arguments)
	}
	return chars
}

func previewMessage(content string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	if maxChars <= 3 {
		return content[:maxChars]
	}
	return content[:maxChars-3] + "..."
}
