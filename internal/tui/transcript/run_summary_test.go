package transcript

import (
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestNewRunSummaryCellBuildsCanonicalRunSummary(t *testing.T) {
	cell := NewRunSummaryCell(RunSummary{
		EventType:           protocol.EventRunCompleted,
		State:               "completed",
		Model:               "deepseek-v4-flash",
		Reasoning:           "high",
		Elapsed:             2*time.Second + 300*time.Millisecond,
		RunModelCalls:       -2,
		SessionModelCalls:   5,
		RunToolCalls:        1,
		SessionToolCalls:    8,
		ContextTokens:       1234,
		ContextWindowTokens: 1_000_000,
		Cost:                "cost: $0.01",
		Error:               "ignored here",
	})

	if cell.Kind != "status" || cell.CellType != CellTypeRunSummary || cell.EventType != protocol.EventRunCompleted {
		t.Fatalf("summary cell metadata = %#v", cell)
	}
	for _, want := range []string{"Run done", "deepseek-v4-flash", "high", "2s"} {
		if !strings.Contains(cell.Title, want) {
			t.Fatalf("summary title missing %q: %q", want, cell.Title)
		}
	}
	for _, want := range []string{
		"state: completed",
		"elapsed: 2s",
		"agent turns: 0 / session 5",
		"tools: 1 / session 8",
		"context: 1.2k / 1.0m",
		"cost: $0.01",
		"error: ignored here",
	} {
		if !strings.Contains(cell.Content, want) {
			t.Fatalf("summary content missing %q:\n%s", want, cell.Content)
		}
	}
	if cell.RawCopy != cell.Content {
		t.Fatalf("raw copy should match content")
	}
}
