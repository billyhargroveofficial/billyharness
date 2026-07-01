package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestTUIUserInputAnsweredSubmitsToGateway(t *testing.T) {
	var got gatewayapi.UserInputAnswerRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/user_input/request-1/answer" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(gatewayapi.UserInputResponse{RequestID: "request-1", Status: "answered"})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	m.pendingUserInput = &protocol.UserInputRequestEvent{RequestID: "request-1"}
	m.textarea.SetValue("Blue")

	model, cmd := m.send()
	if cmd == nil {
		t.Fatal("expected answer command")
	}
	updated := model.(Model)
	if updated.textarea.Value() != "" || updated.status != "sending answer" {
		t.Fatalf("updated textarea/status = %q/%q", updated.textarea.Value(), updated.status)
	}

	msg := cmd().(userInputAnswerMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.requestID != "request-1" || msg.status != "answered" {
		t.Fatalf("answer msg = %#v", msg)
	}
	if got.Text != "Blue" || got.Source != "tui" {
		t.Fatalf("answer request = %#v", got)
	}
}
