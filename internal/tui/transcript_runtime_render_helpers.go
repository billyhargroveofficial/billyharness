package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/billyhargroveofficial/billyharness/internal/displayfmt"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func blockTitle(b transcript.Cell) string {
	label := strings.ToLower(b.Title)
	switch b.Kind {
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	case "reasoning":
		return "thinking"
	case "tool":
		return strings.ToLower(oneLinePreview(b.Title, 72))
	case "error":
		return "error"
	case "status":
		return strings.ToLower(oneLinePreview(b.Title, 72))
	case "audit":
		return strings.ToLower(oneLinePreview(b.Title, 72))
	default:
		return label
	}
}

func appendIfMissing(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

func toolName(value any) string {
	return toolrender.CallName(value)
}

func auditToolName(value any) string {
	fields := mapFromAny(value)
	if name := stringField(fields, "name"); name != "" {
		return name
	}
	return "tool"
}

func mapFromAny(value any) map[string]any {
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(bytes, &fields); err != nil {
		return nil
	}
	return fields
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	switch value := fields[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func collapsedPreview(text string, maxLines, maxChars int) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return "[collapsed: empty]"
	}
	lines := strings.Split(trimmed, "\n")
	limited := lines
	if len(limited) > maxLines {
		limited = limited[:maxLines]
	}
	preview := strings.Join(limited, "\n")
	preview = truncateRunes(preview, maxChars)
	more := len(lines) > len(limited) || len(trimmed) > len(preview)
	if more {
		preview += "\n..."
	}
	return fmt.Sprintf("[collapsed: %d chars, Ctrl+E expand]\n%s", len(text), preview)
}

func collapsedSummary(text string) string {
	return fmt.Sprintf("[collapsed: %d chars, Ctrl+E expand]", len(text))
}

func oneLinePreview(text string, maxChars int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}

func truncateRunes(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars])
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func shortModel(model string) string {
	model = strings.TrimPrefix(model, "deepseek-")
	model = strings.TrimPrefix(model, "deepseek/")
	model = strings.TrimPrefix(model, "gpt-")
	if strings.HasPrefix(model, "v4-") {
		return model
	}
	return truncateRunes(model, 18)
}

func isCodexModel(model string) bool {
	return modelinfo.IsCodexModel(model)
}

func padRight(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-lipgloss.Width(text))
}

func fitSegments(width int, sep string, segments ...string) string {
	var clean []string
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			clean = append(clean, segment)
		}
	}
	if len(clean) == 0 || width <= 0 {
		return ""
	}
	for keep := len(clean); keep > 0; keep-- {
		candidate := strings.Join(clean[:keep], sep)
		if keep < len(clean) {
			candidate += sep + "..."
		}
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return truncateRunes(clean[0], width)
}

func renderStatusSegments(width int, segments []statusSegment, separator lipgloss.Style) string {
	var clean []statusSegment
	for _, segment := range segments {
		segment.text = strings.TrimSpace(segment.text)
		if segment.text != "" {
			clean = append(clean, segment)
		}
	}
	if width <= 0 || len(clean) == 0 {
		return ""
	}
	sep := separator.Render(" · ")
	for keep := len(clean); keep > 0; keep-- {
		rendered := renderStatusParts(clean[:keep], sep)
		if keep < len(clean) {
			rendered += sep + separator.Render("...")
		}
		if lipgloss.Width(rendered) <= width {
			return rendered
		}
	}
	return clean[0].style.Render(truncateRunes(clean[0].text, width))
}

func renderStatusParts(segments []statusSegment, sep string) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		parts = append(parts, segment.style.Render(segment.text))
	}
	return strings.Join(parts, sep)
}

func compactNumber(value int64) string {
	return displayfmt.CompactTerminal(value)
}

func compactDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func compactEventText(value any) string {
	return transcript.CompactEventText(value)
}

func contextThresholdEventText(value any) string {
	return transcript.ContextThresholdEventText(value)
}
