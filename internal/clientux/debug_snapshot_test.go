package clientux

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatTUIDebugSnapshotRedactsSecretsAndKeepsHashes(t *testing.T) {
	snapshot := TUIDebugSnapshot{
		SchemaVersion: TUIDebugSnapshotSchemaVersion,
		Session: TUIDebugSession{
			LocalChatID:         "chat-1",
			GatewaySessionID:    "sess-1",
			GatewayURL:          "https://debug-user:debug-password@example.test/run?api_key=query-secret-value",
			LastGatewayEventSeq: 7,
			SettingsPath:        "/tmp/settings-with-secret-token",
		},
		Runtime: TUIDebugRuntime{
			Mode:          "gateway",
			Provider:      "codex",
			SelectedModel: "gpt-test",
			ActiveModel:   "gpt-test",
			Status:        "Authorization: Bearer sk-runtime-secret-value",
			Error:         "api_key=error-secret-value",
		},
		Stream: TUIDebugStream{PendingEvents: 2, BatchScheduled: true, ChannelBuffered: 1, ChannelCapacity: 8},
		Projector: TUIDebugProjector{
			Available:    true,
			RunState:     "running",
			LastSeq:      7,
			ModelCalls:   1,
			ToolCalls:    1,
			TrackedTools: 1,
			ErrorCount:   1,
			LastError:    "password=projector-secret-value",
		},
		Viewport: TUIDebugViewport{AppWidth: 120, AppHeight: 40, Width: 100, Height: 24},
		Selection: TUIDebugSelection{
			Active:        true,
			Start:         TUIDebugPoint{Row: 0, Col: 1},
			End:           TUIDebugPoint{Row: 0, Col: 4},
			SelectedBytes: 12,
			SelectedRunes: 4,
			SelectedHash:  DebugHash("secret selected text"),
		},
		Transcript: TUIDebugTranscript{
			Blocks:        3,
			SelectedIndex: 1,
			ViewportBytes: 42,
			ViewportHash:  DebugHash("secret transcript text"),
		},
		Export: TUIDebugExport{
			Mode:      "rich",
			Target:    "info_block_or_path_argument",
			RawBytes:  55,
			RawHash:   DebugHash("raw transcript"),
			RichBytes: 60,
			RichHash:  DebugHash("rich transcript"),
		},
		BlockKinds: map[string]int{"assistant": 1, "tool": 1},
		CellTypes:  map[string]int{"assistant_final": 1, "tool_call": 1},
		Hints:      []string{"file search has token=hint-secret-value"},
	}

	redacted := RedactTUIDebugSnapshot(snapshot, snapshot.Session.GatewayURL, snapshot.Session.SettingsPath)
	body, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	formatted := FormatTUIDebugSnapshot(snapshot)

	for _, text := range []string{encoded, formatted} {
		for _, notWant := range []string{
			"debug-password",
			"query-secret-value",
			"/tmp/settings-with-secret-token",
			"sk-runtime-secret-value",
			"error-secret-value",
			"projector-secret-value",
			"hint-secret-value",
			"secret selected text",
			"secret transcript text",
		} {
			if strings.Contains(text, notWant) {
				t.Fatalf("debug snapshot leaked %q:\n%s", notWant, text)
			}
		}
	}
	for _, want := range []string{
		"schema: 1",
		"session: local=chat-1 gateway=sess-1 last_seq=7 mode=gateway title=0:none",
		"stream: pending=2 scheduled=true channel=1/8",
		"selection: active=true",
		"transcript: blocks=3 selected=1",
		"export: mode=rich target=info_block_or_path_argument",
		DebugHash("secret selected text"),
		DebugHash("secret transcript text"),
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("debug snapshot missing %q:\n%s", want, formatted)
		}
	}
}
