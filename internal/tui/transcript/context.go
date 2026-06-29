package transcript

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func CompactEventText(value any) string {
	bytes, _ := json.Marshal(value)
	type protectedPrefixData struct {
		Messages        int            `json:"messages"`
		Chars           int            `json:"chars"`
		EstimatedTokens int64          `json:"estimated_tokens"`
		Reasons         map[string]int `json:"reasons"`
	}
	type contributorData struct {
		Index           int    `json:"index"`
		Role            string `json:"role"`
		Source          string `json:"source"`
		Name            string `json:"name"`
		EstimatedTokens int64  `json:"estimated_tokens"`
		Preview         string `json:"preview"`
	}
	var data struct {
		ActiveMessages           int                 `json:"active_messages"`
		SummaryChars             int                 `json:"summary_chars"`
		Detail                   string              `json:"detail"`
		CompactionID             string              `json:"compaction_id"`
		Reason                   string              `json:"reason"`
		TriggerSource            string              `json:"trigger_source"`
		TriggerPromptToks        int64               `json:"trigger_prompt_tokens"`
		ThresholdTokens          int64               `json:"threshold_tokens"`
		BeforeEstimatedTokens    int64               `json:"before_estimated_tokens"`
		AfterEstimatedTokens     int64               `json:"after_estimated_tokens"`
		CutStartIndex            int                 `json:"cut_start_index"`
		CutEndIndex              int                 `json:"cut_end_index"`
		ReplacementIndex         int                 `json:"replacement_index"`
		KeepMessages             int                 `json:"keep_messages"`
		MaxSummaryChars          int                 `json:"max_summary_chars"`
		SummaryStrategy          string              `json:"summary_strategy"`
		SummaryProvider          string              `json:"summary_provider"`
		SummaryModel             string              `json:"summary_model"`
		SummaryError             string              `json:"summary_error"`
		ModelSummaryInputTokens  int64               `json:"model_summary_input_tokens"`
		ModelSummaryOutputTokens int64               `json:"model_summary_output_tokens"`
		CompactedMessages        int                 `json:"compacted_messages"`
		CompactedChars           int                 `json:"compacted_chars"`
		CompactedEstimatedTokens int64               `json:"compacted_estimated_tokens"`
		ProtectedPrefix          protectedPrefixData `json:"protected_prefix"`
		TopContextContributors   []contributorData   `json:"top_context_contributors"`
	}
	_ = json.Unmarshal(bytes, &data)
	var lines []string
	if data.CompactionID != "" {
		lines = append(lines, "id: "+data.CompactionID)
	}
	if data.Reason != "" {
		line := "reason: " + data.Reason
		if data.TriggerSource != "" {
			line += " (" + data.TriggerSource + ")"
		}
		lines = append(lines, line)
	}
	if data.TriggerPromptToks > 0 || data.ThresholdTokens > 0 {
		lines = append(lines, fmt.Sprintf("trigger: %d / threshold %d tokens", data.TriggerPromptToks, data.ThresholdTokens))
	} else if data.Detail != "" {
		lines = append(lines, data.Detail)
	}
	if data.BeforeEstimatedTokens > 0 || data.AfterEstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("context: before ~%s / after ~%s", compactNumber(data.BeforeEstimatedTokens), compactNumber(data.AfterEstimatedTokens)))
	}
	if data.CutEndIndex > data.CutStartIndex {
		lines = append(lines, fmt.Sprintf("cut: [%d:%d) -> replacement index %d", data.CutStartIndex, data.CutEndIndex, data.ReplacementIndex))
	}
	if data.KeepMessages > 0 || data.MaxSummaryChars > 0 {
		lines = append(lines, fmt.Sprintf("policy: keep %d messages / summary cap %d chars", data.KeepMessages, data.MaxSummaryChars))
	}
	if data.SummaryStrategy != "" {
		line := "summary: " + data.SummaryStrategy
		if data.SummaryProvider != "" || data.SummaryModel != "" {
			line += " " + strings.TrimSpace(data.SummaryProvider+"/"+data.SummaryModel)
		}
		lines = append(lines, line)
	}
	if data.ModelSummaryInputTokens > 0 || data.ModelSummaryOutputTokens > 0 {
		lines = append(lines, fmt.Sprintf("summary usage: in %s / out %s", compactNumber(data.ModelSummaryInputTokens), compactNumber(data.ModelSummaryOutputTokens)))
	}
	if data.SummaryError != "" {
		lines = append(lines, "summary error: "+data.SummaryError)
	}
	if data.CompactedMessages > 0 {
		lines = append(lines, fmt.Sprintf("compacted messages: %d", data.CompactedMessages))
	}
	if data.CompactedChars > 0 || data.CompactedEstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("compacted budget: %d chars / ~%d tokens", data.CompactedChars, data.CompactedEstimatedTokens))
	}
	if data.ProtectedPrefix.Messages > 0 {
		line := fmt.Sprintf("protected prefix: %d messages, %d chars, ~%d tokens", data.ProtectedPrefix.Messages, data.ProtectedPrefix.Chars, data.ProtectedPrefix.EstimatedTokens)
		if reasonText := compactReasonCounts(data.ProtectedPrefix.Reasons); reasonText != "" {
			line += " (" + reasonText + ")"
		}
		lines = append(lines, line)
	}
	if data.ActiveMessages > 0 {
		lines = append(lines, fmt.Sprintf("active messages: %d", data.ActiveMessages))
	}
	if data.SummaryChars > 0 {
		lines = append(lines, fmt.Sprintf("summary chars: %d", data.SummaryChars))
	}
	if len(data.TopContextContributors) > 0 {
		lines = append(lines, "top contributors:")
		for _, contributor := range data.TopContextContributors {
			label := contributor.Source
			if contributor.Name != "" {
				label += "/" + contributor.Name
			}
			preview := contributor.Preview
			if preview == "" {
				preview = "(no text)"
			}
			lines = append(lines, fmt.Sprintf("  #%d %s %s ~%s - %s", contributor.Index, contributor.Role, label, compactNumber(contributor.EstimatedTokens), preview))
		}
	}
	if len(lines) == 0 {
		return "context compacted"
	}
	return strings.Join(lines, "\n")
}

func ContextThresholdEventText(value any) string {
	bytes, _ := json.Marshal(value)
	var data protocol.ContextThresholdEvent
	_ = json.Unmarshal(bytes, &data)
	if data.Percent <= 0 {
		return "context threshold crossed"
	}
	window := data.ContextWindowTokens
	var lines []string
	lines = append(lines, fmt.Sprintf("threshold: %d%%", data.Percent))
	if window > 0 {
		lines = append(lines, fmt.Sprintf("active: %s / %s", compactNumber(data.EstimatedTokens), compactNumber(window)))
	} else {
		lines = append(lines, fmt.Sprintf("active: %s", compactNumber(data.EstimatedTokens)))
	}
	if data.ThresholdTokens > 0 {
		lines = append(lines, fmt.Sprintf("threshold tokens: %s", compactNumber(data.ThresholdTokens)))
	}
	if data.RemainingTokens > 0 {
		lines = append(lines, fmt.Sprintf("remaining window: %s", compactNumber(data.RemainingTokens)))
	}
	if data.MessageCount > 0 {
		lines = append(lines, fmt.Sprintf("messages: %d", data.MessageCount))
	}
	if data.Stage != "" {
		lines = append(lines, "stage: "+data.Stage)
	}
	if data.Round > 0 {
		lines = append(lines, fmt.Sprintf("round: %d", data.Round))
	}
	return strings.Join(lines, "\n")
}

func compactReasonCounts(reasons map[string]int) string {
	if len(reasons) == 0 {
		return ""
	}
	keys := make([]string, 0, len(reasons))
	for key, count := range reasons {
		if key != "" && count > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, reasons[key]))
	}
	return strings.Join(parts, ", ")
}
