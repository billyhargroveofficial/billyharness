package telegrambot

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type processWatchHarness struct {
	*telegramAdmissionHarness
	snapshots []gatewayapi.ManagedProcessResponse
	index     int
}

func (h *processWatchHarness) ProcessSnapshot(context.Context) (gatewayapi.ManagedProcessResponse, error) {
	if h.index >= len(h.snapshots) {
		return h.snapshots[len(h.snapshots)-1], nil
	}
	out := h.snapshots[h.index]
	h.index++
	return out, nil
}

func TestManagedProcessWatchSendsFinishedProcessOnce(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := &processWatchHarness{
		telegramAdmissionHarness: newTelegramAdmissionHarness(),
		snapshots: []gatewayapi.ManagedProcessResponse{
			{Processes: protocol.ManagedProcessList{Processes: []protocol.ManagedProcessStatus{{
				ID:      "proc-1",
				Running: true,
			}}}},
			{Processes: protocol.ManagedProcessList{Processes: []protocol.ManagedProcessStatus{{
				ID:                "proc-1",
				Running:           false,
				Exited:            true,
				ExitCode:          0,
				ElapsedMS:         1234,
				OutputTailPreview: "done\nAuthorization: Bearer sk-test-secret",
			}}}},
			{Processes: protocol.ManagedProcessList{Processes: []protocol.ManagedProcessStatus{{
				ID:                "proc-1",
				Running:           false,
				Exited:            true,
				ExitCode:          0,
				ElapsedMS:         1234,
				OutputTailPreview: "done",
			}}}},
		},
	}
	var sent []string
	client := newTelegramMediaAPIClient(t, "bottoken", nil, func(_ http.ResponseWriter, _ *http.Request, payload map[string]any) {
		if payload["chat_id"] != float64(123) {
			t.Errorf("chat_id = %#v, want 123", payload["chat_id"])
		}
		text, _ := payload["text"].(string)
		sent = append(sent, text)
	})
	bot, err := New(Options{
		BotToken:             "token",
		StatePath:            statePath,
		Model:                "gpt-5.4",
		Profile:              "billy",
		AllowedChatIDs:       map[int64]bool{123: true},
		SendEnabled:          true,
		ProcessWatchInterval: time.Second,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}
	recipients := bot.processWatchRecipients()
	last := map[string]bool{}
	for i := 0; i < 3; i++ {
		if err := bot.pollManagedProcessesOnce(context.Background(), harness, recipients, last); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 1 {
		t.Fatalf("sent messages = %#v", sent)
	}
	if !strings.Contains(sent[0], "Billyharness process finished") ||
		!strings.Contains(sent[0], "id: proc-1") ||
		!strings.Contains(sent[0], "exit: 0") ||
		!strings.Contains(sent[0], "elapsed: 1s") {
		t.Fatalf("process notification = %q", sent[0])
	}
	if strings.Contains(sent[0], "sk-test-secret") {
		t.Fatalf("process notification leaked secret: %q", sent[0])
	}
}
