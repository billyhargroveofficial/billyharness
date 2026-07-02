package displayfmt

import (
	"fmt"
	"strconv"
	"strings"
)

type compactRule struct {
	minAbs            int64
	scale             float64
	decimals          int
	suffix            string
	trimTrailingZeros bool
}

var contextCompactRules = []compactRule{
	{minAbs: 1_000_000, scale: 1_000_000, decimals: 2, suffix: "M"},
	{minAbs: 1_000, scale: 1_000, decimals: 1, suffix: "k"},
}

var terminalCompactRules = []compactRule{
	{minAbs: 1_000_000, scale: 1_000_000, decimals: 1, suffix: "m"},
	{minAbs: 10_000, scale: 1_000, decimals: 0, suffix: "k"},
	{minAbs: 1_000, scale: 1_000, decimals: 1, suffix: "k"},
}

var toolCompactRules = []compactRule{
	{minAbs: 1_000_000, scale: 1_000_000, decimals: 1, suffix: "M", trimTrailingZeros: true},
	{minAbs: 1_000, scale: 1_000, decimals: 1, suffix: "k", trimTrailingZeros: true},
}

// CompactContext formats token counts for shared status and diagnostics text.
func CompactContext(value int64) string {
	return compact(value, contextCompactRules)
}

// CompactTerminal formats token counts for dense terminal status surfaces.
func CompactTerminal(value int64) string {
	return compact(value, terminalCompactRules)
}

// CompactTool formats compact tool metadata without unnecessary .0 suffixes.
func CompactTool(value int64) string {
	return compact(value, toolCompactRules)
}

func compact(value int64, rules []compactRule) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	for _, rule := range rules {
		if abs < rule.minAbs {
			continue
		}
		text := fmt.Sprintf("%.*f", rule.decimals, float64(value)/rule.scale)
		if rule.trimTrailingZeros && rule.decimals > 0 {
			text = strings.TrimSuffix(strings.TrimSuffix(text, "0"), ".")
		}
		return text + rule.suffix
	}
	return strconv.FormatInt(value, 10)
}

// ContextPercent formats compact context percentages for status lines.
func ContextPercent(used, window int64) string {
	if window <= 0 {
		return "0%"
	}
	return ContextPercentValue(float64(used) / float64(window) * 100)
}

// ContextPercentValue formats compact context percentages for status lines.
func ContextPercentValue(percent float64) string {
	if percent < 10 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
}

// FixedPercentValue formats diagnostic percentages with a fixed precision.
func FixedPercentValue(percent float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	return fmt.Sprintf("%.*f%%", decimals, percent)
}
