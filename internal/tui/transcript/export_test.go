package transcript

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestFormatCellsRawAndRichTranscriptModes(t *testing.T) {
	cells := []Cell{
		{Kind: "assistant", Title: "ASSISTANT", Content: "rendered **answer**", RawCopy: "raw **answer**"},
		{Kind: "tool", Title: "Called shell_exec", Content: "compact result", RawCopy: "raw tool output_ref=tool-output/shell.txt"},
	}
	raw := FormatCells(cells, ExportModeRaw)
	if !strings.Contains(raw, "raw **answer**") || !strings.Contains(raw, "raw tool output_ref=tool-output/shell.txt") || strings.Contains(raw, "Called shell_exec") {
		t.Fatalf("raw transcript = %q", raw)
	}
	rich := FormatCells(cells, ExportModeRich)
	if !strings.Contains(rich, "ASSISTANT\nrendered **answer**") || !strings.Contains(rich, "Called shell_exec\ncompact result") || strings.Contains(rich, "raw tool output_ref") {
		t.Fatalf("rich transcript = %q", rich)
	}
}

func TestFormatMessagesTranscriptFallback(t *testing.T) {
	out := FormatMessages([]protocol.Message{
		{Role: protocol.RoleUser, Content: "hello"},
		{Role: protocol.RoleAssistant, Content: "answer"},
	}, ExportModeRich)
	if !strings.Contains(out, "USER\nhello") || !strings.Contains(out, "ASSISTANT\nanswer") {
		t.Fatalf("message transcript = %q", out)
	}
}

func TestFormatSessionCombinesMessagesAndEvents(t *testing.T) {
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "inspect me"},
		{Role: protocol.RoleAssistant, Content: "done"},
	}
	events := []protocol.Event{
		{
			Type: protocol.EventToolOutputRefCreated,
			Data: protocol.ToolOutputRefEvent{
				Name:      "shell_exec",
				OutputRef: "tool-output/shell.txt",
				Compact: &protocol.ToolCompact{
					Name:      "shell_exec",
					OutputRef: "tool-output/shell.txt",
					Preview:   "go test ./...",
				},
			},
		},
	}

	raw := FormatSession(messages, events, ExportModeRaw)
	if !strings.Contains(raw, "inspect me") || !strings.Contains(raw, "done") || !strings.Contains(raw, "tool-output/shell.txt") {
		t.Fatalf("raw session transcript = %q", raw)
	}
	if strings.Contains(raw, "EVENTS") {
		t.Fatalf("raw session transcript should avoid rich section labels: %q", raw)
	}

	rich := FormatSession(messages, events, ExportModeRich)
	for _, want := range []string{"USER\ninspect me", "ASSISTANT\ndone", "EVENTS", "shell_exec", "shell.txt"} {
		if !strings.Contains(rich, want) {
			t.Fatalf("rich session transcript missing %q: %q", want, rich)
		}
	}
}

func TestNormalizeExportMode(t *testing.T) {
	if NormalizeExportMode("") != ExportModeRich || NormalizeExportMode("RAW") != ExportModeRaw || NormalizeExportMode("weird") != "" {
		t.Fatalf("unexpected normalized modes")
	}
}
