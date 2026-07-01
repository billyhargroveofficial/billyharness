package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestUserInputAnsweredEndpointUnblocksPendingAskUser(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{})
	session := newGatewaySession("session-1", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	server.mu.Lock()
	server.sessions[session.ID] = session
	server.mu.Unlock()

	request := testUserInputRequest("request-1")
	events := make(chan protocol.Event, 4)
	result := make(chan struct {
		answer protocol.UserInputAnswerEvent
		err    error
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		answer, err := session.askUser(ctx, request, func(event protocol.Event) {
			events <- event
		})
		result <- struct {
			answer protocol.UserInputAnswerEvent
			err    error
		}{answer: answer, err: err}
	}()

	requested := receiveGatewayEvent(t, events, protocol.EventUserInputRequested)
	if got, ok := protocol.DecodeUserInputRequest(requested.Data); !ok || got.RequestID != "request-1" || got.SessionID != "session-1" {
		t.Fatalf("requested event = %#v ok=%v", requested, ok)
	}

	body, err := json.Marshal(UserInputAnswerRequest{Text: "Blue", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/session-1/user_input/request-1/answer", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("answer status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp UserInputResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.RequestID != "request-1" || resp.Status != "answered" {
		t.Fatalf("response = %#v", resp)
	}

	got := receiveAskUserResult(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.answer.Answers) != 1 || got.answer.Answers[0].OptionID != "blue" || got.answer.Answers[0].OptionLabel != "Blue" || got.answer.Source != "test" {
		t.Fatalf("answer = %#v", got.answer)
	}
	answered := receiveGatewayEvent(t, events, protocol.EventUserInputAnswered)
	if decoded, ok := protocol.DecodeUserInputAnswer(answered.Data); !ok || decoded.RequestID != "request-1" || decoded.Answers[0].OptionID != "blue" {
		t.Fatalf("answered event = %#v ok=%v", answered, ok)
	}
}

func TestUserInputTerminalRunClearsPendingRequest(t *testing.T) {
	session := newGatewaySession("session-1", time.Now().UTC(), nil)
	session.pendingInput = &pendingUserInput{
		request: testUserInputRequest("stale-request"),
		reply:   make(chan userInputResolution, 1),
	}
	session.observeRunEvent(protocol.Event{Type: protocol.EventRunCompleted, RunID: "run-1"})

	if _, err := session.answerUserInput("stale-request", UserInputAnswerRequest{Text: "Blue"}); err != errNoPendingUserInput {
		t.Fatalf("answer err = %v, want no pending request", err)
	}
}

func testUserInputRequest(requestID string) protocol.UserInputRequestEvent {
	return protocol.UserInputRequestEvent{
		RequestID: requestID,
		RunID:     "run-1",
		TurnID:    "turn-1",
		StepID:    "step-1",
		CallID:    "call-1",
		AttemptID: "attempt-1",
		Source:    "tool",
		Questions: []protocol.UserInputQuestion{{
			ID:       "color",
			Question: "Pick a color",
			Options: []protocol.UserInputOption{
				{ID: "blue", Label: "Blue", Description: "Use blue"},
				{ID: "red", Label: "Red", Description: "Use red"},
			},
			AllowFreeform: true,
		}},
	}
}

func receiveGatewayEvent(t *testing.T, ch <-chan protocol.Event, eventType protocol.EventType) protocol.Event {
	t.Helper()
	select {
	case event := <-ch:
		if event.Type != eventType {
			t.Fatalf("event type = %s, want %s (%#v)", event.Type, eventType, event)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", eventType)
		return protocol.Event{}
	}
}

func receiveAskUserResult(t *testing.T, ch <-chan struct {
	answer protocol.UserInputAnswerEvent
	err    error
}) struct {
	answer protocol.UserInputAnswerEvent
	err    error
} {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ask_user result")
		return struct {
			answer protocol.UserInputAnswerEvent
			err    error
		}{}
	}
}
