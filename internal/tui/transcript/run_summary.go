package transcript

import (
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type RunSummary struct {
	EventType           protocol.EventType
	State               string
	Model               string
	Reasoning           string
	Elapsed             time.Duration
	RunModelCalls       int
	SessionModelCalls   int
	RunToolCalls        int
	SessionToolCalls    int
	ContextTokens       int64
	ContextWindowTokens int64
	Cost                string
	Error               string
}

func NewRunSummaryCell(summary RunSummary) Cell {
	body := RunSummaryBody(summary)
	return Cell{
		Kind:      "status",
		CellType:  CellTypeRunSummary,
		Title:     RunSummaryTitle(summary),
		Content:   body,
		EventType: summary.EventType,
		RawCopy:   body,
	}
}

func RunSummaryTitle(summary RunSummary) string {
	label := "Run " + strings.TrimSpace(summary.State)
	switch strings.ToLower(strings.TrimSpace(summary.State)) {
	case "completed":
		label = "Run done"
	case "failed":
		label = "Run failed"
	case "running":
		label = "Run running"
	}
	parts := []string{label}
	if model := strings.TrimSpace(summary.Model); model != "" {
		parts = append(parts, model)
	}
	if reasoning := strings.TrimSpace(summary.Reasoning); reasoning != "" {
		parts = append(parts, reasoning)
	}
	if summary.Elapsed > 0 {
		parts = append(parts, compactDuration(summary.Elapsed))
	}
	return strings.Join(parts, " · ")
}

func RunSummaryBody(summary RunSummary) string {
	state := strings.TrimSpace(summary.State)
	if state == "" {
		state = "unknown"
	}
	var lines []string
	lines = append(lines, "state: "+state)
	if summary.Elapsed > 0 {
		lines = append(lines, "elapsed: "+compactDuration(summary.Elapsed))
	}
	lines = append(lines, fmt.Sprintf("agent turns: %d / session %d", maxInt(0, summary.RunModelCalls), summary.SessionModelCalls))
	lines = append(lines, fmt.Sprintf("tools: %d / session %d", maxInt(0, summary.RunToolCalls), summary.SessionToolCalls))
	lines = append(lines, "context: "+compactNumber(summary.ContextTokens)+" / "+compactNumber(summary.ContextWindowTokens))
	if cost := strings.TrimSpace(summary.Cost); cost != "" {
		lines = append(lines, cost)
	}
	if errText := strings.TrimSpace(summary.Error); errText != "" {
		lines = append(lines, "error: "+errText)
	}
	return strings.Join(lines, "\n")
}

func compactNumber(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%.0fk", float64(value)/1_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
