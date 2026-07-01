package session

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestImportJSONLTranscriptAddsMarkerAndWarnings(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"role":"user","content":"Hello from Codex"}`,
		`{"role":"assistant","content":"Hi back","tool_calls":[{"name":"shell"}]}`,
		`{"role":"tool","content":"tool output"}`,
	}, "\n"))
	result, err := ImportTranscript(raw, ImportOptions{Source: "/tmp/codex.jsonl", Format: ImportFormatJSONL})
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != ImportFormatJSONL || len(result.Messages) != 3 {
		t.Fatalf("result = %#v", result)
	}
	if result.Messages[0].Role != protocol.RoleSystem || !strings.Contains(result.Messages[0].Content, "Imported external session") {
		t.Fatalf("marker = %#v", result.Messages[0])
	}
	if result.Messages[1].Role != protocol.RoleUser || result.Messages[2].Role != protocol.RoleAssistant {
		t.Fatalf("messages = %#v", result.Messages)
	}
	if result.Diagnostics.ImportedMessages != 2 || result.Diagnostics.MessageCount != 3 || result.Diagnostics.ApproxTokens == 0 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	if len(result.Diagnostics.Warnings) < 2 {
		t.Fatalf("expected tool warnings, got %#v", result.Diagnostics.Warnings)
	}
	if len(result.Events) != 1 || result.Events[0].Type != protocol.EventSessionImported {
		t.Fatalf("events = %#v", result.Events)
	}
}

func TestImportMarkdownTranscript(t *testing.T) {
	raw := []byte("# User\nPlease review this.\n\n# Assistant\nLooks good.\n")
	result, err := ImportTranscript(raw, ImportOptions{Source: "claude.md", Format: ImportFormatMarkdown})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages = %#v", result.Messages)
	}
	if result.Messages[1].Role != protocol.RoleUser || !strings.Contains(result.Messages[1].Content, "Please review") {
		t.Fatalf("user message = %#v", result.Messages[1])
	}
	if result.Messages[2].Role != protocol.RoleAssistant || !strings.Contains(result.Messages[2].Content, "Looks good") {
		t.Fatalf("assistant message = %#v", result.Messages[2])
	}
}

func TestImportSimpleNestedAgentFormats(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"message":{"role":"user","content":"Claude style"}}`,
		`{"type":"message","role":"assistant","content":[{"type":"text","text":"Codex style"}]}`,
		`{"speaker":"assistant","parts":[{"type":"text","text":"OpenCode style"}]}`,
	}, "\n"))
	result, err := ImportTranscript(raw, ImportOptions{Format: ImportFormatJSONL})
	if err != nil {
		t.Fatal(err)
	}
	if result.Diagnostics.ImportedMessages != 3 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	joined := result.Messages[1].Content + "\n" + result.Messages[2].Content + "\n" + result.Messages[3].Content
	for _, want := range []string{"Claude style", "Codex style", "OpenCode style"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("import missing %q in %#v", want, result.Messages)
		}
	}
}
