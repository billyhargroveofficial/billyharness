package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	answered  chan telegramUserInputAnswer
}

type telegramUserInputAnswer struct {
	SessionID string
	RequestID string
	Request   gatewayapi.UserInputAnswerRequest
}

func newTelegramAdmissionHarness() *telegramAdmissionHarness {
	return &telegramAdmissionHarness{
		admitted: make(chan gatewayapi.SessionInputRequest, 4),
		ran:      make(chan gatewayapi.RunRequest, 4),
		answered: make(chan telegramUserInputAnswer, 4),
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

func (h *telegramAdmissionHarness) AnswerUserInput(_ context.Context, sessionID, requestID string, answer gatewayapi.UserInputAnswerRequest) (gatewayapi.UserInputResponse, error) {
	h.answered <- telegramUserInputAnswer{SessionID: sessionID, RequestID: requestID, Request: answer}
	return gatewayapi.UserInputResponse{RequestID: requestID, Status: "answered"}, nil
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

func TestTelegramUpdateParsesPhotoDocumentCaptionAndThread(t *testing.T) {
	body := []byte(`{
		"update_id": 100,
		"message": {
			"message_id": 200,
			"message_thread_id": 9,
			"caption": "look at this",
			"photo": [
				{"file_id": "small", "file_unique_id": "us", "width": 64, "height": 64, "file_size": 123},
				{"file_id": "large", "file_unique_id": "ul", "width": 640, "height": 480, "file_size": 4567}
			],
			"document": {
				"file_id": "doc",
				"file_unique_id": "ud",
				"file_name": "scan.png",
				"mime_type": "image/png",
				"file_size": 789
			},
			"chat": {"id": 123},
			"from": {"id": 1001}
		}
	}`)
	var update Update
	if err := json.Unmarshal(body, &update); err != nil {
		t.Fatal(err)
	}
	msg := update.Message
	if update.UpdateID != 100 || msg == nil || msg.ThreadID != 9 || msg.Caption != "look at this" {
		t.Fatalf("parsed update = %#v", update)
	}
	if len(msg.Photo) != 2 || msg.Photo[1].FileID != "large" || msg.Photo[1].FileUniqueID != "ul" || msg.Photo[1].FileSize != 4567 {
		t.Fatalf("photo parse = %#v", msg.Photo)
	}
	if msg.Document == nil || msg.Document.FileID != "doc" || msg.Document.FileUniqueID != "ud" || msg.Document.MIMEType != "image/png" {
		t.Fatalf("document parse = %#v", msg.Document)
	}
}

func TestTelegramPhotoCaptionAdmissionDownloadsAttachment(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	image := telegramPNGBytes(t, 2, 3)
	client := newTelegramMediaAPIClient(t, "bottoken", map[string]telegramMediaFixture{
		"photo-large": {
			File: TelegramFile{FileID: "photo-large", FileUniqueID: "unique-large", FileSize: int64(len(image)), FilePath: "photos/photo.png"},
			Data: image,
		},
	}, nil)
	bot := newAdmissionMediaTestBot(t, statePath, harness, client, "gpt-5.4", false, map[int64]bool{123: true})
	update := telegramPhotoUpdate(50, 123, 1001, "what is this?", PhotoSize{FileID: "photo-small", FileUniqueID: "small", Width: 1, Height: 1, FileSize: 1}, PhotoSize{
		FileID:       "photo-large",
		FileUniqueID: "unique-large",
		Width:        2,
		Height:       3,
		FileSize:     int64(len(image)),
	})
	update.Message.ThreadID = 8

	bot.handlePolledUpdate(context.Background(), update)

	admitted := receive(t, harness.admitted, "photo admission")
	if admitted.Prompt != "what is this?" || len(admitted.Attachments) != 1 {
		t.Fatalf("admitted = %#v", admitted)
	}
	ref := admitted.Attachments[0]
	if ref.Kind != protocol.AttachmentKindImage || ref.FileName != "telegram-photo-unique-large.jpg" || ref.MIMEType != "image/png" || ref.Width != 2 || ref.Height != 3 {
		t.Fatalf("attachment ref = %#v", ref)
	}
	if admitted.Metadata["attachment_count"] != "1" || admitted.Metadata["vision_input"] != "true" || admitted.Metadata["thread_id"] != "8" {
		t.Fatalf("metadata = %#v", admitted.Metadata)
	}
	stored := filepath.Join(os.Getenv("BILLYHARNESS_HOME"), "attachments", ref.StorageRef)
	if info, err := os.Stat(stored); err != nil || info.Size() == 0 {
		t.Fatalf("stored attachment stat=%v err=%v", info, err)
	}

	run := receive(t, harness.ran, "photo run")
	if run.Prompt != admitted.Prompt || len(run.Attachments) != 1 || run.Attachments[0].ID != ref.ID {
		t.Fatalf("run = %#v", run)
	}
	waitForState(t, statePath, func(state State) bool {
		chat := state.Chats[userChatKey(123, 8, 1001)]
		return state.Offset == 51 && chat.PendingInputID == "" && chat.LastEventSeq == 2
	})

	records := readTelegramAdmissionRecords(t, admissionPathForState(statePath))
	if len(records) != 1 || records[0].AttachmentCount != 1 || records[0].PromptSHA256 == "" {
		t.Fatalf("records = %#v", records)
	}
}

func TestTelegramDocumentImageAdmissionDownloadsAttachment(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	image := telegramPNGBytes(t, 4, 5)
	client := newTelegramMediaAPIClient(t, "bottoken", map[string]telegramMediaFixture{
		"doc-1": {
			File: TelegramFile{FileID: "doc-1", FileUniqueID: "doc-unique", FileSize: int64(len(image)), FilePath: "documents/uploaded.png"},
			Data: image,
		},
	}, nil)
	bot := newAdmissionMediaTestBot(t, statePath, harness, client, "gpt-5.4", false, map[int64]bool{123: true})
	update := telegramTextUpdate(51, "")
	update.Message.Caption = "inspect the upload"
	update.Message.Document = &Document{
		FileID:       "doc-1",
		FileUniqueID: "doc-unique",
		FileName:     "screen.png",
		MIMEType:     "image/png",
		FileSize:     int64(len(image)),
	}

	bot.handlePolledUpdate(context.Background(), update)

	admitted := receive(t, harness.admitted, "document admission")
	if admitted.Prompt != "inspect the upload" || len(admitted.Attachments) != 1 {
		t.Fatalf("admitted = %#v", admitted)
	}
	ref := admitted.Attachments[0]
	if ref.FileName != "screen.png" || ref.Width != 4 || ref.Height != 5 {
		t.Fatalf("document ref = %#v", ref)
	}
	run := receive(t, harness.ran, "document run")
	if len(run.Attachments) != 1 || run.Attachments[0].ID != ref.ID {
		t.Fatalf("run = %#v", run)
	}
	waitForState(t, statePath, func(state State) bool {
		chat := state.Chats[userChatKey(123, 0, 1001)]
		return state.Offset == 52 && chat.PendingInputID == "" && chat.LastEventSeq == 2
	})
}

func TestTelegramImageOnlyPhotoAdmittedForVisionModel(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	image := telegramPNGBytes(t, 1, 1)
	client := newTelegramMediaAPIClient(t, "bottoken", map[string]telegramMediaFixture{
		"photo-only": {
			File: TelegramFile{FileID: "photo-only", FileUniqueID: "only", FileSize: int64(len(image)), FilePath: "photos/only.png"},
			Data: image,
		},
	}, nil)
	bot := newAdmissionMediaTestBot(t, statePath, harness, client, "gpt-5.4", false, map[int64]bool{123: true})

	bot.handlePolledUpdate(context.Background(), telegramPhotoUpdate(52, 123, 1001, "", PhotoSize{FileID: "photo-only", FileUniqueID: "only", Width: 1, Height: 1, FileSize: int64(len(image))}))

	admitted := receive(t, harness.admitted, "image-only admission")
	if admitted.Prompt != "" || len(admitted.Attachments) != 1 {
		t.Fatalf("admitted = %#v", admitted)
	}
	run := receive(t, harness.ran, "image-only run")
	if run.Prompt != "" || len(run.Attachments) != 1 {
		t.Fatalf("run = %#v", run)
	}
	waitForState(t, statePath, func(state State) bool {
		chat := state.Chats[userChatKey(123, 0, 1001)]
		return state.Offset == 53 && chat.PendingInputID == "" && chat.LastEventSeq == 2
	})
}

func TestTelegramVisionUnsupportedModelRepliesAndAcks(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	var sentText string
	client := newTelegramMediaAPIClient(t, "bottoken", nil, func(_ http.ResponseWriter, _ *http.Request, payload map[string]any) {
		sentText, _ = payload["text"].(string)
	})
	bot := newAdmissionMediaTestBot(t, statePath, harness, client, "deepseek-v4-flash", true, map[int64]bool{123: true})

	bot.handlePolledUpdate(context.Background(), telegramPhotoUpdate(53, 123, 1001, "describe", PhotoSize{FileID: "photo-unsupported", FileUniqueID: "unsupported", Width: 2, Height: 2, FileSize: 12}))

	if !strings.Contains(sentText, "Image input is unsupported") || !strings.Contains(sentText, "deepseek-v4-flash") {
		t.Fatalf("unsupported reply = %q", sentText)
	}
	select {
	case admitted := <-harness.admitted:
		t.Fatalf("unsupported vision should not admit: %#v", admitted)
	case <-time.After(50 * time.Millisecond):
	}
	state := loadTelegramState(t, statePath)
	if state.Offset != 54 {
		t.Fatalf("offset = %d, want 54", state.Offset)
	}
	records := readTelegramAdmissionRecords(t, admissionPathForState(statePath))
	if len(records) != 1 || records[0].Kind != "ignored" || records[0].Reason != "vision_unsupported" {
		t.Fatalf("records = %#v", records)
	}
}

func TestTelegramDownloadFailureDoesNotAdvanceOffsetOrAdmit(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	client := newTelegramMediaAPIClient(t, "bottoken", nil, nil)
	bot := newAdmissionMediaTestBot(t, statePath, harness, client, "gpt-5.4", false, map[int64]bool{123: true})

	bot.handlePolledUpdate(context.Background(), telegramPhotoUpdate(54, 123, 1001, "retry after download", PhotoSize{FileID: "missing-file", FileUniqueID: "missing", Width: 1, Height: 1, FileSize: 12}))

	select {
	case admitted := <-harness.admitted:
		t.Fatalf("download failure should not admit: %#v", admitted)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case run := <-harness.ran:
		t.Fatalf("download failure should not run: %#v", run)
	case <-time.After(50 * time.Millisecond):
	}
	state := loadTelegramState(t, statePath)
	if state.Offset != 0 {
		t.Fatalf("offset = %d, want unchanged", state.Offset)
	}
}

func TestTelegramConcurrentPhotoChatsRemainIsolated(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	harness := newTelegramAdmissionHarness()
	image := telegramPNGBytes(t, 2, 2)
	client := newTelegramMediaAPIClient(t, "bottoken", map[string]telegramMediaFixture{
		"photo-a": {
			File: TelegramFile{FileID: "photo-a", FileUniqueID: "unique-a", FileSize: int64(len(image)), FilePath: "photos/a.png"},
			Data: image,
		},
		"photo-b": {
			File: TelegramFile{FileID: "photo-b", FileUniqueID: "unique-b", FileSize: int64(len(image)), FilePath: "photos/b.png"},
			Data: image,
		},
	}, nil)
	bot := newAdmissionMediaTestBot(t, statePath, harness, client, "gpt-5.4", false, map[int64]bool{123: true, 456: true})

	var wg sync.WaitGroup
	for _, update := range []Update{
		telegramPhotoUpdate(70, 123, 1001, "chat a", PhotoSize{FileID: "photo-a", FileUniqueID: "unique-a", Width: 2, Height: 2, FileSize: int64(len(image))}),
		telegramPhotoUpdate(71, 456, 2002, "chat b", PhotoSize{FileID: "photo-b", FileUniqueID: "unique-b", Width: 2, Height: 2, FileSize: int64(len(image))}),
	} {
		wg.Add(1)
		go func(update Update) {
			defer wg.Done()
			bot.handlePolledUpdate(context.Background(), update)
		}(update)
	}
	wg.Wait()

	first := receive(t, harness.admitted, "first concurrent admission")
	second := receive(t, harness.admitted, "second concurrent admission")
	clientIDs := map[string]bool{first.ClientID: true, second.ClientID: true}
	if !clientIDs["telegram:123:u1001"] || !clientIDs["telegram:456:u2002"] {
		t.Fatalf("client IDs = %#v", clientIDs)
	}
	_ = receive(t, harness.ran, "first concurrent run")
	_ = receive(t, harness.ran, "second concurrent run")
	waitForState(t, statePath, func(state State) bool {
		a := state.Chats[userChatKey(123, 0, 1001)]
		b := state.Chats[userChatKey(456, 0, 2002)]
		return state.Offset == 72 && a.LastEventSeq == 2 && b.LastEventSeq == 2
	})
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

func TestTelegramPendingQuestionMessageAnswersWithoutAdmissionOrRun(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "telegram-state.json")
	if err := (Store{Path: statePath}).Save(State{Chats: map[string]ChatState{
		userChatKey(123, 0, 1001): {
			SessionID:          "session-1",
			PendingUserInputID: "request-1",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	harness := newTelegramAdmissionHarness()
	bot := newAdmissionTestBot(t, statePath, harness)

	bot.handlePolledUpdate(context.Background(), telegramTextUpdate(46, "Blue"))

	answer := receive(t, harness.answered, "pending question answer")
	if answer.SessionID != "session-1" || answer.RequestID != "request-1" {
		t.Fatalf("answer identity = %#v", answer)
	}
	if answer.Request.Text != "Blue" || answer.Request.Source != "telegram" || answer.Request.Metadata["update_id"] != "46" {
		t.Fatalf("answer request = %#v", answer.Request)
	}
	select {
	case admitted := <-harness.admitted:
		t.Fatalf("pending question should not admit prompt: %#v", admitted)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case run := <-harness.ran:
		t.Fatalf("pending question should not start run: %#v", run)
	case <-time.After(50 * time.Millisecond):
	}
	state := loadTelegramState(t, statePath)
	if state.Offset != 47 {
		t.Fatalf("offset = %d, want 47", state.Offset)
	}
	if chat := state.Chats[userChatKey(123, 0, 1001)]; chat.PendingUserInputID != "" {
		t.Fatalf("pending request = %q, want cleared", chat.PendingUserInputID)
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
	return newAdmissionMediaTestBot(t, statePath, harness, nil, "deepseek-v4-flash", false, map[int64]bool{123: true})
}

func newAdmissionMediaTestBot(t *testing.T, statePath string, harness *telegramAdmissionHarness, client *Client, model string, sendEnabled bool, allowed map[int64]bool) *Bot {
	t.Helper()
	if allowed == nil {
		allowed = map[int64]bool{123: true}
	}
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       statePath,
		Model:           model,
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    time.Millisecond,
		AllowedChatIDs:  allowed,
		SendEnabled:     sendEnabled,
		DryRunDefault:   !sendEnabled,
	}, client, harness)
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

func telegramPhotoUpdate(updateID int, chatID, userID int64, caption string, photos ...PhotoSize) Update {
	return Update{
		UpdateID: updateID,
		Message: &Message{
			MessageID: 77,
			From:      &User{ID: userID},
			Chat:      Chat{ID: chatID},
			Caption:   caption,
			Photo:     photos,
		},
	}
}

type telegramMediaFixture struct {
	File TelegramFile
	Data []byte
}

func newTelegramMediaAPIClient(t *testing.T, token string, files map[string]telegramMediaFixture, sendMessage func(http.ResponseWriter, *http.Request, map[string]any)) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodPost && requestPath == "/bot"+token+"/getFile":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode getFile payload: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fileID, _ := payload["file_id"].(string)
			fixture, ok := files[fileID]
			if !ok {
				writeTelegramError(w, http.StatusInternalServerError, "temporary getFile failure")
				return
			}
			writeTelegramResult(w, fixture.File)
		case r.Method == http.MethodGet && strings.HasPrefix(requestPath, "/file/bot"+token+"/"):
			filePath := strings.TrimPrefix(requestPath, "/file/bot"+token+"/")
			for _, fixture := range files {
				escaped, err := escapeTelegramFilePath(fixture.File.FilePath)
				if err != nil {
					t.Errorf("escape fixture path: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if filePath == escaped {
					_, _ = w.Write(fixture.Data)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && requestPath == "/bot"+token+"/sendMessage":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode sendMessage payload: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if sendMessage != nil {
				sendMessage(w, r, payload)
			}
			writeTelegramResult(w, SentMessage{MessageID: 99, Chat: Chat{ID: 123}})
		default:
			t.Errorf("unexpected telegram path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return NewClient(ClientOptions{BaseURL: server.URL, Token: token, MinInterval: time.Nanosecond})
}

func telegramPNGBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 80, B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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
