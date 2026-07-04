package clientux

import (
	"strings"
	"testing"
	"time"
)

func TestFormatTranscriptExportArtifactIncludesMetadataWarningsAndBody(t *testing.T) {
	out := FormatTranscriptExportArtifact(TranscriptExportMetadata{
		SourceStore:         "tui.client_state.cells",
		SourceMode:          TranscriptExportSourceEvents,
		TranscriptMode:      "raw",
		RuntimeMode:         "gateway",
		LocalChatID:         "local-1",
		GatewaySessionID:    "sess-1",
		LastGatewayEventSeq: 42,
		SeqRange:            TranscriptExportSeqRange{LastKnown: true, Last: 42},
		Provider:            "codex",
		Model:               "gpt-test",
		Profile:             "billy",
		AccessMode:          "write",
		ReasoningKind:       "effort",
		ReasoningEffort:     "high",
		ExportedAt:          time.Date(2026, 7, 4, 12, 30, 0, 0, time.UTC),
		RedactionMode:       "none_body_unredacted",
		Warnings: []string{
			"partial_replay: Authorization: Bearer sk-secret-secret-secret",
			"projector_mismatch",
		},
		Extra: map[string]string{"body_sha256": "abc123"},
	}, "hello transcript")

	for _, want := range []string{
		"# Billyharness Transcript Export",
		`- exported_at: "2026-07-04T12:30:00Z"`,
		`- source_store: "tui.client_state.cells"`,
		`- source_mode: "events"`,
		`- transcript_mode: "raw"`,
		`- runtime_mode: "gateway"`,
		`- gateway_session_id: "sess-1"`,
		`- seq_range: "unknown..42"`,
		`- redaction_mode: "none_body_unredacted"`,
		`- body_sha256: "abc123"`,
		"projector_mismatch",
		"--- transcript ---",
		"hello transcript",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("artifact missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-secret-secret-secret") {
		t.Fatalf("artifact warning leaked secret:\n%s", out)
	}
}

func TestNormalizeTranscriptExportSource(t *testing.T) {
	for input, want := range map[string]string{
		"":         TranscriptExportSourceCells,
		"cells":    TranscriptExportSourceCells,
		"message":  TranscriptExportSourceMessages,
		"messages": TranscriptExportSourceMessages,
		"event":    TranscriptExportSourceEvents,
		"events":   TranscriptExportSourceEvents,
		"session":  TranscriptExportSourceCombined,
		"combined": TranscriptExportSourceCombined,
		"weird":    "",
	} {
		if got := NormalizeTranscriptExportSource(input); got != want {
			t.Fatalf("NormalizeTranscriptExportSource(%q) = %q, want %q", input, got, want)
		}
	}
}
