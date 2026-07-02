package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestGatewayEnterSubmitsPromptThroughKeyboardPath(t *testing.T) {
	runReqCh := make(chan gatewayapi.RunRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions/session-1/run":
			var req gatewayapi.RunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			runReqCh <- req
			_ = json.NewEncoder(w).Encode(protocol.Event{Seq: 1, Type: protocol.EventRunCompleted})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions/session-1":
			_ = json.NewEncoder(w).Encode(gatewayapi.SessionResponse{
				ID: "session-1",
				Messages: []protocol.Message{
					{Role: protocol.RoleUser, Content: "ssh prompt"},
					{Role: protocol.RoleAssistant, Content: "done"},
				},
			})
		default:
			t.Fatalf("unexpected gateway request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	m.textarea.SetValue("ssh prompt")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated := next.(Model)
	if cmd == nil {
		t.Fatal("Enter should return wait/tick command after gateway submit")
	}
	if updated.textarea.Value() != "" || !updated.busy || updated.status != "running" {
		t.Fatalf("updated input/busy/status = %q/%v/%q", updated.textarea.Value(), updated.busy, updated.status)
	}
	if len(updated.blocks) == 0 || updated.blocks[len(updated.blocks)-1].Title != "USER" ||
		!strings.Contains(updated.blocks[len(updated.blocks)-1].Content, "ssh prompt") {
		t.Fatalf("user block missing after Enter: %#v", updated.blocks)
	}
	select {
	case req := <-runReqCh:
		if req.Prompt != "ssh prompt" || req.AccessMode != m.currentAccessMode() || req.Model != m.currentModel() {
			t.Fatalf("gateway run request = %#v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("gateway run request was not sent")
	}
	deadline := time.After(time.Second)
	for {
		select {
		case msg := <-updated.events:
			if _, ok := msg.(runDoneMsg); ok {
				return
			}
		case <-deadline:
			t.Fatal("gateway run did not finish")
		}
	}
}
