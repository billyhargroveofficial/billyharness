package config

import (
	"fmt"
	"sort"
	"strings"
)

var SummaryKeys = []string{
	"provider",
	"model",
	"profile",
	"thinking",
	"reasoning_effort",
	"context_window_tokens",
	"context_compact_tokens",
	"context_compact_strategy",
	"context_compact_summary_provider",
	"context_compact_summary_model",
	"max_tool_rounds",
	"max_parallel_tools",
	"max_tool_output_bytes",
	"auto_approve_dangerous",
	"web_summary_mode",
	"web_summary_provider",
	"web_summary_model",
	"web_summary_max_input_tokens",
	"web_summary_max_output_tokens",
	"mcp_enabled",
	"mcp_allowed_servers",
	"gateway_addr",
}

func FormatSummary(values []ResolvedValue, warnings []string) string {
	byKey := map[string]ResolvedValue{}
	for _, value := range values {
		byKey[value.Key] = value
	}
	lines := []string{"billyharness config"}
	for _, key := range SummaryKeys {
		value, ok := byKey[key]
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-29s %s", key+":", summaryValue(value)))
	}
	if len(warnings) > 0 {
		lines = append(lines, "", "warnings:")
		for _, warning := range warnings {
			if strings.TrimSpace(warning) != "" {
				lines = append(lines, "- "+strings.TrimSpace(warning))
			}
		}
	}
	if len(lines) == 1 {
		var keys []string
		for key := range byKey {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("%-29s %s", key+":", summaryValue(byKey[key])))
		}
	}
	return strings.Join(lines, "\n")
}

func summaryValue(value ResolvedValue) string {
	rendered := fmt.Sprint(value.Value)
	if value.Redacted {
		rendered = "[redacted]"
	}
	rendered = strings.Join(strings.Fields(rendered), " ")
	source := value.Source
	location := value.SourceKey
	if value.SourcePath != "" {
		if location != "" {
			location += " @ "
		}
		location += value.SourcePath
	}
	var suffix []string
	if source != "" {
		if location != "" {
			suffix = append(suffix, source+" "+location)
		} else {
			suffix = append(suffix, source)
		}
	}
	if value.Warning != "" {
		suffix = append(suffix, "warning "+value.Warning)
	}
	if value.Error != "" {
		suffix = append(suffix, "error "+value.Error)
	}
	if len(suffix) == 0 {
		return rendered
	}
	return rendered + "  [" + strings.Join(suffix, "; ") + "]"
}
