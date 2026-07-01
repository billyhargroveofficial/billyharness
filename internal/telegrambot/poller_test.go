package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type telegramAdmissionHarness struct {
	scriptedHarness

	admitErr  error
	admitResp gatewayapi.SessionInputResponse
	admitted  chan gatewayapi.SessionInputRequest
	ran       chan gatewayapi.RunRequest
}

func newTelegramAdmissionHarness() *telegramAdmissionHarness {
	return &telegramAdmissionHarness{
		admitted: make(chan gatewayapi.SessionInputRequest, 4),
		ran:      make(chan gatewayapi.RunRequest, 4),
	}
}

func (h *telegramAdmissionHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h *telegramAdmissionHarness) AdmitSessionInput(_ context.Context, sessionID string, input gatewayapi.SessionInputRequest) (gatewayapi.SessionInputResponse, error) {
	if sessionID != "session-1" {
		return gatewayapi.SessionInputResponse{}, fmt.Errorf("sessionID = %q", sessionID)
	}
	h.admitted <- input
	if h.admitErr != nil {
		return gatewayapi.SessionInputResponse{}, h.admitErr
	}
	resp := h.admitResp
	if resp.InputID == "" {
		resp.InputID = input.InputID
	}
	if resp.State == "" {
		resp.State = "admitted"
	}
	return resp, nil
}

func (h *telegramAdmissionHarness) RunSession(_ context.Context, sessionID string, run gatewayapi.RunRequest, emit func(protocol.Event)) error {
	if sessionID != "session-1" {
		return fmt.Errorf("sessionID = %q", sessionID)
	}
	h.ran <- run
	emit(protocol.Event{Seq: 1, Type: protocol.EventRunStarted})
	emit(protocol.Event{Seq: 2, Type: protocol.EventRunCompleted})
	return nil
}

func TestTelegramPromptAdmissionAdvancesOffsetAfterGatewayAdmission(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	bot := newAdmissionTestBot(t, statePath, harness)
	update := telegramTextUpdate(42, "hello from telegram")

	bot.handlePolledUpdate(context.Background(), update)

	admitted := receive(t, harness.admitted, "admission")
	if admitted.InputID != "telegram-update-42" {
		t.Fatalf("input_id = %q", admitted.InputID)
	}
	if admitted.Prompt != "hello from telegram" || admitted.InterruptPolicy != gatewayapi.InterruptPolicyInterrupt {
		t.Fatalf("admission request = %#v", admitted)
	}
	if admitted.ClientID != "telegram:123:u1001" || admitted.ClientType != "telegram" {
		t.Fatalf("client fields = %q %q", admitted.ClientID, admitted.ClientType)
	}

	run := receive(t, harness.ran, "run")
	if run.InputID != "telegram-update-42" || run.ClientID != "telegram:123:u1001" {
		t.Fatalf("run request = %#v", run)
	}
	waitForState(t, statePath, func(state State) bool {
		chat := state.Chats[userChatKey(123, 0, 1001)]
		return state.Offset == 43 && chat.PendingInputID == "" && chat.LastEventSeq == 2
	})

	records := readTelegramAdmissionRecords(t, admissionPathForState(statePath))
	if len(records) != 1 || records[0].Kind != "admitted" || records[0].UpdateID != 42 || records[0].InputID != "telegram-update-42" {
		t.Fatalf("admission records = %#v", records)
	}
}

func TestTelegramAdmissionFailureDoesNotAdvanceOffsetOrRun(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	harness.admitErr = errors.New("gateway unavailable")
	bot := newAdmissionTestBot(t, statePath, harness)

	bot.handlePolledUpdate(context.Background(), telegramTextUpdate(43, "retry me"))

	_ = receive(t, harness.admitted, "admission")
	select {
	case run := <-harness.ran:
		t.Fatalf("unexpected run: %#v", run)
	case <-time.After(50 * time.Millisecond):
	}
	state := loadTelegramState(t, statePath)
	if state.Offset != 0 {
		t.Fatalf("offset = %d, want unchanged", state.Offset)
	}
	if chat := state.Chats[userChatKey(123, 0, 1001)]; chat.SessionID != "session-1" {
		t.Fatalf("chat state should keep retryable session id, got %#v", chat)
	}
}

func TestTelegramDuplicateCompletedAdmissionAcksWithoutRun(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	harness.admitResp = gatewayapi.SessionInputResponse{
		InputID:   "telegram-update-44",
		State:     "completed",
		Duplicate: true,
		Seq:       3,
	}
	bot := newAdmissionTestBot(t, statePath, harness)

	bot.handlePolledUpdate(context.Background(), telegramTextUpdate(44, "already handled"))

	_ = receive(t, harness.admitted, "admission")
	select {
	case run := <-harness.ran:
		t.Fatalf("duplicate completed update should not run again: %#v", run)
	case <-time.After(50 * time.Millisecond):
	}
	state := loadTelegramState(t, statePath)
	if state.Offset != 45 {
		t.Fatalf("offset = %d, want 45", state.Offset)
	}
	if chat := state.Chats[userChatKey(123, 0, 1001)]; chat.PendingInputID != "" {
		t.Fatalf("pending input = %q, want cleared", chat.PendingInputID)
	}
}

func TestTelegramIgnoredUpdateRecordsBeforeAck(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	bot := newAdmissionTestBot(t, statePath, harness)

	bot.handlePolledUpdate(context.Background(), Update{UpdateID: 45})

	state := loadTelegramState(t, statePath)
	if state.Offset != 46 {
		t.Fatalf("offset = %d, want 46", state.Offset)
	}
	records := readTelegramAdmissionRecords(t, admissionPathForState(statePath))
	if len(records) != 1 || records[0].Kind != "ignored" || records[0].Reason != "empty_message" || records[0].UpdateID != 45 {
		t.Fatalf("ignored records = %#v", records)
	}
}

func newAdmissionTestBot(t *testing.T, statePath string, harness *telegramAdmissionHarness) *Bot {
	t.Helper()
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       statePath,
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     false,
		DryRunDefault:   true,
	}, nil, harness)
	if err != nil {
		t.Fatal(err)
	}
	return bot
}

func telegramTextUpdate(updateID int, text string) Update {
	return Update{
		UpdateID: updateID,
		Message: &Message{
			MessageID: 77,
			From:      &User{ID: 1001},
			Chat:      Chat{ID: 123},
			Text:      text,
		},
	}
}

func admissionPathForState(statePath string) string {
	return newTelegramAdmissionStore(statePath).path
}

func receive[T any](t *testing.T, ch <-chan T, label string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", label)
		return zero
	}
}

func waitForState(t *testing.T, statePath string, ok func(State) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := loadTelegramState(t, statePath)
		if ok(state) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state did not satisfy condition: %#v", loadTelegramState(t, statePath))
}

func loadTelegramState(t *testing.T, statePath string) State {
	t.Helper()
	state, err := (Store{Path: statePath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func readTelegramAdmissionRecords(t *testing.T, path string) []telegramAdmissionRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []telegramAdmissionRecord
	dec := json.NewDecoder(file)
	for {
		var record telegramAdmissionRecord
		if err := dec.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}
