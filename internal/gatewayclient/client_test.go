package gatewayclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestStatusErrorMatchesSessionNotFound(t *testing.T) {
	err := &StatusError{Method: http.MethodGet, Path: "/v1/sessions/missing", StatusCode: http.StatusNotFound}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("StatusError should match ErrSessionNotFound: %v", err)
	}
}

func TestStatusErrorMatchesReplayErrorClasses(t *testing.T) {
	tests := []struct {
		name   string
		err    *StatusError
		target error
	}{
		{
			name:   "corrupt session",
			err:    &StatusError{Method: http.MethodGet, Path: "/v1/sessions/session-1/events", StatusCode: http.StatusConflict, Body: `{"error":"corrupt session event history: bad seq"}`},
			target: ErrSessionCorrupt,
		},
		{
			name:   "no session store",
			err:    &StatusError{Method: http.MethodGet, Path: "/v1/sessions/session-1/events", StatusCode: http.StatusConflict, Body: `{"error":"session history unavailable: no session store"}`},
			target: ErrNoSessionStore,
		},
		{
			name:   "replay failed",
			err:    &StatusError{Method: http.MethodGet, Path: "/v1/sessions/session-1/events", StatusCode: http.StatusInternalServerError, Body: `{"error":"session event replay failed: disk unavailable"}`},
			target: ErrSessionReplayFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.target) {
				t.Fatalf("StatusError %v should match %v", tt.err, tt.target)
			}
			if errors.Is(tt.err, ErrSessionNotFound) {
				t.Fatalf("replay error should not match ErrSessionNotFound: %v", tt.err)
			}
		})
	}
}

func TestFormatSessionContextLabelsExplicitContextWindowOverride(t *testing.T) {
	text := FormatSessionContext(gatewayapi.SessionContextResponse{
		ID:                  "session-1",
		EstimatedTokens:     128,
		ContextWindowTokens: 1_000_000,
		ContextWindowSource: "override",
		PercentUsed:         0.0128,
	})
	if !strings.Contains(text, "active context: 128 / 1.00M (0.0%, override)") {
		t.Fatalf("context report should label override:\n%s", text)
	}
}

func TestFormatSessionContextExplainsProviderCacheCounters(t *testing.T) {
	text := FormatSessionContext(gatewayapi.SessionContextResponse{
		ID:                  "session-1",
		EstimatedTokens:     120,
		ContextWindowTokens: 1_000,
		PercentUsed:         12,
		Usage: gatewayapi.ContextUsage{
			InputTokens:        100,
			OutputTokens:       20,
			CacheHitTokens:     900,
			CacheMissTokens:    50,
			LastCacheHitTokens: 900,
		},
	})
	for _, want := range []string{
		"active context: 120 / 1.0k (12.0%)",
		"provider usage: input=100 output=20 reasoning=0",
		"provider cache: hit=900 miss=50 last_hit=900 last_miss=0",
		"provider cache counters are billing/cache accounting, not active context",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("context report missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\ncache: hit=") || strings.Contains(text, "\nusage: input=") {
		t.Fatalf("context report should label provider counters explicitly:\n%s", text)
	}
}

func TestFormatSessionContextLabelsHelperUsageLikeClientFooters(t *testing.T) {
	text := FormatSessionContext(gatewayapi.SessionContextResponse{
		ID: "session-helpers",
		Usage: gatewayapi.ContextUsage{
			WebSummaryInputTokens:   20_000,
			WebSummaryOutputTokens:  900,
			HelperModelInputTokens:  1_200,
			HelperModelOutputTokens: 80,
			HelperModelAPITokens:    1_280,
			HelperAPICalls:          2,
			HelperCostUSD:           0.0045,
		},
	})
	for _, want := range []string{"helper usage: websum=20.0k→900", "helper=1.2k→80", "sumapi=1.3k", "helper API calls=2", "helper API cost=$0.004500"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context report missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "provider_api_calls") || strings.Contains(text, "provider_cost") || strings.Contains(text, "helper_api") || strings.Contains(text, " api cost=") {
		t.Fatalf("context report used legacy helper labels:\n%s", text)
	}
}

func TestCreateSessionWithOwnerSendsOwnerMetadata(t *testing.T) {
	var got gatewayapi.CreateSessionRequest
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			if !testkit.DecodeJSON(t, r, &got) {
				return
			}
			testkit.WriteJSON(t, w, gatewayapi.SessionResponse{ID: "session-1"})
		},
	})

	owner := gatewayapi.SessionOwner{
		ClientID:         "telegram:123:u1001",
		ClientType:       "telegram",
		TelegramChatID:   123,
		TelegramThreadID: 7,
		TelegramUserID:   1001,
		Profile:          "billy",
		Model:            "deepseek-v4-flash",
	}
	id, err := New(server.URL).CreateSessionWithOwner(context.Background(), "billy", owner)
	if err != nil {
		t.Fatal(err)
	}
	if id != "session-1" {
		t.Fatalf("id = %q", id)
	}
	if got.Profile != "billy" || got.Owner != owner {
		t.Fatalf("request = %#v, want owner %#v", got, owner)
	}
}

func TestContextSessionOwnerSendsScopeHeaders(t *testing.T) {
	var got http.Header
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/status",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			testkit.WriteJSON(t, w, gatewayapi.SessionStatus{ID: "session-1"})
		},
	})

	owner := gatewayapi.SessionOwner{
		ClientID:         "telegram:123:u1001",
		ClientType:       "telegram",
		TelegramChatID:   123,
		TelegramThreadID: 7,
		TelegramUserID:   1001,
		TUIChatID:        "local-tui",
	}
	ctx := WithSessionOwner(context.Background(), owner)
	if _, err := New(server.URL).SessionStatus(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	for header, want := range map[string]string{
		gatewayapi.HeaderSessionClientID:         "telegram:123:u1001",
		gatewayapi.HeaderSessionClientType:       "telegram",
		gatewayapi.HeaderSessionTelegramChatID:   "123",
		gatewayapi.HeaderSessionTelegramThreadID: "7",
		gatewayapi.HeaderSessionTelegramUserID:   "1001",
		gatewayapi.HeaderSessionTUIChatID:        "local-tui",
	} {
		if got.Get(header) != want {
			t.Fatalf("%s = %q, want %q", header, got.Get(header), want)
		}
	}
}

func TestSessionStatusFetchesRuntimeModel(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/status",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			testkit.WriteJSON(t, w, gatewayapi.SessionStatus{ID: "session-1", Model: "deepseek-v4-flash"})
		},
	})

	status, err := New(server.URL).SessionStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Model != "deepseek-v4-flash" {
		t.Fatalf("status model = %q", status.Model)
	}
}

func TestSessionInspectRawFetchesLiveInspectEndpoint(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/inspect",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			testkit.WriteJSON(t, w, map[string]any{
				"session_id":         "session-1",
				"event_replay_ready": true,
			})
		},
	})

	raw, err := New(server.URL).SessionInspectRaw(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SessionID        string `json:"session_id"`
		EventReplayReady bool   `json:"event_replay_ready"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "session-1" || !got.EventReplayReady {
		t.Fatalf("inspect raw = %s parsed=%#v", raw, got)
	}
}

func TestListAndGetSessionsFetchTypedResponses(t *testing.T) {
	server := testkit.NewRouteServer(t,
		testkit.Route{
			Method: http.MethodGet,
			Path:   "/v1/sessions",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				testkit.WriteJSON(t, w, gatewayapi.SessionListResponse{
					Sessions: []gatewayapi.SessionSummary{{ID: "session-1", MessageCount: 2}},
				})
			},
		},
		testkit.Route{
			Method: http.MethodGet,
			Path:   "/v1/sessions/session-1",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				testkit.WriteJSON(t, w, gatewayapi.SessionResponse{
					ID:           "session-1",
					MessageCount: 2,
					Messages: []protocol.Message{
						protocol.UserMessage("hello", nil),
					},
				})
			},
		},
	)

	client := New(server.URL)
	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session-1" || sessions[0].MessageCount != 2 {
		t.Fatalf("sessions = %#v", sessions)
	}
	session, err := client.GetSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "session-1" || session.MessageCount != 2 || len(session.Messages) != 1 {
		t.Fatalf("session = %#v", session)
	}
}

func TestCompleteInputAndUndoRedoPostTypedRequests(t *testing.T) {
	var gotComplete gatewayapi.SessionInputCompleteRequest
	var gotUndo gatewayapi.SessionUndoRequest
	server := testkit.NewRouteServer(t,
		testkit.Route{
			Method: http.MethodPost,
			Path:   "/v1/sessions/session-1/inputs/input-1/complete",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if !testkit.DecodeJSON(t, r, &gotComplete) {
					return
				}
				testkit.WriteJSON(t, w, gatewayapi.SessionInputResponse{InputID: "input-1", State: "completed", TerminalStatus: gotComplete.TerminalStatus})
			},
		},
		testkit.Route{
			Method: http.MethodPost,
			Path:   "/v1/sessions/session-1/undo",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if !testkit.DecodeJSON(t, r, &gotUndo) {
					return
				}
				testkit.WriteJSON(t, w, gatewayapi.SessionUndoResponse{ChangeID: gotUndo.ChangeID, Preview: gotUndo.Preview})
			},
		},
		testkit.Route{
			Method: http.MethodPost,
			Path:   "/v1/sessions/session-1/redo",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				testkit.WriteJSON(t, w, gatewayapi.SessionUndoResponse{ChangeID: "change-1", RestoredFiles: []string{"file.txt"}})
			},
		},
	)

	client := New(server.URL)
	completed, err := client.CompleteSessionInput(context.Background(), "session-1", "input-1", gatewayapi.SessionInputCompleteRequest{TerminalStatus: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if gotComplete.TerminalStatus != "completed" || completed.InputID != "input-1" || completed.TerminalStatus != "completed" {
		t.Fatalf("complete request=%#v response=%#v", gotComplete, completed)
	}
	preview, err := client.PreviewSessionUndo(context.Background(), "session-1", "change-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotUndo.ChangeID != "change-1" || !gotUndo.Preview || preview.ChangeID != "change-1" || !preview.Preview {
		t.Fatalf("undo request=%#v response=%#v", gotUndo, preview)
	}
	redo, err := client.RedoSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if redo.ChangeID != "change-1" || len(redo.RestoredFiles) != 1 {
		t.Fatalf("redo = %#v", redo)
	}
}

func TestClientErrorHelpersDescribeFailures(t *testing.T) {
	if got := (&StatusError{Method: http.MethodGet, Path: "/v1/config", StatusCode: http.StatusForbidden, Body: " forbidden "}).Error(); got != "gateway GET /v1/config HTTP 403: forbidden" {
		t.Fatalf("status error = %q", got)
	}
	if got := (&RunFailedError{}).Error(); got != "gateway run failed" {
		t.Fatalf("empty run failed error = %q", got)
	}
	if got := (&EventSeqGapError{AfterSeq: 2, GotSeq: 4}).Error(); !strings.Contains(got, "got seq 4 after 2") {
		t.Fatalf("gap error = %q", got)
	}
	base := errors.New("connect refused")
	unavailable := &gatewayapi.UnavailableError{BaseURL: ":8765", Err: base}
	if !errors.Is(unavailable, base) || !strings.Contains(unavailable.Error(), "http://127.0.0.1:8765") {
		t.Fatalf("unavailable = %v", unavailable)
	}
}

func TestReplaySessionEventsDropsStaleCursorEvents(t *testing.T) {
	var sawAuth bool
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/events",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer test-token" {
				sawAuth = true
			}
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 2, Type: protocol.EventAssistantDelta, Data: "stale"},
				protocol.Event{Seq: 3, Type: protocol.EventAssistantDelta, Data: "fresh"},
			)
		},
	})
	t.Setenv(gatewayapi.GatewayAuthTokenEnv, "test-token")

	client := New(server.URL)
	var got []protocol.Event
	if err := client.ReplaySessionEvents(context.Background(), "session-1", 2, func(event protocol.Event) {
		got = append(got, event)
	}); err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("expected auth header")
	}
	if len(got) != 1 || got[0].Seq != 3 || got[0].Data != "fresh" {
		t.Fatalf("events = %#v", got)
	}
}

func TestFollowSessionEventsReplaysThenFollowsFromCursor(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/events",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("after_seq"); got != "9" {
				t.Fatalf("after_seq = %q, want 9", got)
			}
			if got := r.URL.Query().Get("follow"); got != "true" {
				t.Fatalf("follow = %q, want true", got)
			}
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 9, Type: protocol.EventAssistantDelta, Data: "stale replay"},
				protocol.Event{Seq: 10, Type: protocol.EventAssistantDelta, Data: "catchup"},
				protocol.Event{Seq: 10, Type: protocol.EventAssistantDelta, Data: "duplicate live"},
				protocol.Event{Seq: 11, Type: protocol.EventAssistantDelta, Data: "live"},
			)
		},
	})

	var got []protocol.Event
	if err := New(server.URL).FollowSessionEvents(context.Background(), "session-1", 9, func(event protocol.Event) {
		got = append(got, event)
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 10 || got[1].Seq != 11 {
		t.Fatalf("events = %#v", got)
	}
}

func TestAdmitSessionInputPostsTypedRequest(t *testing.T) {
	var got gatewayapi.SessionInputRequest
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions/session-1/inputs",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			if !testkit.DecodeJSON(t, r, &got) {
				return
			}
			testkit.WriteJSON(t, w, gatewayapi.SessionInputResponse{InputID: got.InputID, State: "admitted", Seq: 1})
		},
	})

	resp, err := New(server.URL).AdmitSessionInput(context.Background(), "session-1", gatewayapi.SessionInputRequest{
		InputID:         "input-1",
		Prompt:          "hello",
		Attachments:     []protocol.AttachmentRef{{ID: "att_test", Kind: protocol.AttachmentKindImage, StorageRef: "att_test.png", SHA256: "abc123"}},
		InterruptPolicy: gatewayapi.InterruptPolicyInterrupt,
		ClientID:        "telegram:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.InputID != "input-1" || resp.State != "admitted" || resp.Seq != 1 {
		t.Fatalf("response = %#v", resp)
	}
	if got.InputID != "input-1" || got.Prompt != "hello" || got.InterruptPolicy != gatewayapi.InterruptPolicyInterrupt || got.ClientID != "telegram:1" ||
		len(got.Attachments) != 1 || got.Attachments[0].ID != "att_test" {
		t.Fatalf("request = %#v", got)
	}
}

func TestAgentClubCapabilitiesFetchesTypedResponseWithOwnerHeaders(t *testing.T) {
	var got http.Header
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/agentclub/capabilities",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			testkit.WriteJSON(t, w, agentclub.CapabilityListResponse{
				SchemaVersion: agentclub.SchemaVersion,
				Capabilities: []agentclub.CapabilityView{
					{
						Descriptor: agentclub.CapabilityDescriptor{
							ID:       "event.review",
							Kind:     agentclub.CapabilityKindReview,
							Risk:     agentclub.RiskReadOnly,
							Dispatch: agentclub.DispatchAdmitOnly,
						},
					},
				},
			})
		},
	})

	owner := gatewayapi.SessionOwner{ClientID: "ingress:fixture:prod", ClientType: "ingress"}
	resp, err := New(server.URL).AgentClubCapabilities(WithSessionOwner(context.Background(), owner))
	if err != nil {
		t.Fatal(err)
	}
	if got.Get(gatewayapi.HeaderSessionClientID) != owner.ClientID || got.Get(gatewayapi.HeaderSessionClientType) != owner.ClientType {
		t.Fatalf("owner headers = %#v", got)
	}
	if resp.SchemaVersion != agentclub.SchemaVersion || len(resp.Capabilities) != 1 || resp.Capabilities[0].Descriptor.ID != "event.review" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestUserInputAnsweredAndRejectedPostTypedRequests(t *testing.T) {
	var gotAnswer gatewayapi.UserInputAnswerRequest
	var gotReject gatewayapi.UserInputRejectRequest
	server := testkit.NewRouteServer(t,
		testkit.Route{
			Method: http.MethodPost,
			Path:   "/v1/sessions/session-1/user_input/request-1/answer",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if !testkit.DecodeJSON(t, r, &gotAnswer) {
					return
				}
				testkit.WriteJSON(t, w, gatewayapi.UserInputResponse{RequestID: "request-1", Status: "answered"})
			},
		},
		testkit.Route{
			Method: http.MethodPost,
			Path:   "/v1/sessions/session-1/user_input/request-1/reject",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if !testkit.DecodeJSON(t, r, &gotReject) {
					return
				}
				testkit.WriteJSON(t, w, gatewayapi.UserInputResponse{RequestID: "request-1", Status: "rejected"})
			},
		},
	)

	client := New(server.URL)
	answerResp, err := client.AnswerUserInput(context.Background(), "session-1", "request-1", gatewayapi.UserInputAnswerRequest{
		Text:   "Blue",
		Source: "tui",
		Metadata: map[string]string{
			"client": "test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answerResp.RequestID != "request-1" || answerResp.Status != "answered" {
		t.Fatalf("answer response = %#v", answerResp)
	}
	if gotAnswer.Text != "Blue" || gotAnswer.Source != "tui" || gotAnswer.Metadata["client"] != "test" {
		t.Fatalf("answer request = %#v", gotAnswer)
	}

	rejectResp, err := client.RejectUserInput(context.Background(), "session-1", "request-1", gatewayapi.UserInputRejectRequest{
		Reason: "not now",
		Source: "telegram",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejectResp.RequestID != "request-1" || rejectResp.Status != "rejected" {
		t.Fatalf("reject response = %#v", rejectResp)
	}
	if gotReject.Reason != "not now" || gotReject.Source != "telegram" {
		t.Fatalf("reject request = %#v", gotReject)
	}
}

func TestReplaySessionEventsReportsSequenceGap(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodGet,
		Path:   "/v1/sessions/session-1/events",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 4, Type: protocol.EventAssistantDelta, Data: "gap"},
			)
		},
	})

	var got []protocol.Event
	err := New(server.URL).ReplaySessionEvents(context.Background(), "session-1", 2, func(event protocol.Event) {
		got = append(got, event)
	})
	var gap *EventSeqGapError
	if !errors.As(err, &gap) {
		t.Fatalf("err = %T %[1]v, want EventSeqGapError", err)
	}
	if gap.AfterSeq != 2 || gap.GotSeq != 4 {
		t.Fatalf("gap = %#v", gap)
	}
	if len(got) != 0 {
		t.Fatalf("events emitted across gap = %#v", got)
	}
}

func TestRunSessionResultAllowsFirstSequenceAboveOne(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions/session-1/run",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 20, Type: protocol.EventRunStarted},
				protocol.Event{Seq: 21, Type: protocol.EventRunCompleted},
			)
		},
	})

	result, err := New(server.URL).RunSessionResult(context.Background(), "session-1", gatewayapi.RunRequest{Prompt: "ping"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.LastSeq != 21 || result.EventCount != 2 || result.SeqGap != nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunSessionResultReportsTerminalFailure(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions/session-1/run",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 1, Type: protocol.EventRunStarted},
				protocol.Event{Seq: 2, Type: protocol.EventRunFailed, Data: "boom"},
			)
		},
	})

	var events []protocol.Event
	result, err := New(server.URL).RunSessionResult(context.Background(), "session-1", gatewayapi.RunRequest{Prompt: "ping"}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
	var runErr *RunFailedError
	if !errors.As(err, &runErr) {
		t.Fatalf("err = %T, want *RunFailedError", err)
	}
	if !result.Failed || result.Completed || result.LastSeq != 2 || result.EventCount != 2 || result.Failure != "boom" {
		t.Fatalf("result = %#v", result)
	}
	if len(events) != 2 || events[1].Type != protocol.EventRunFailed {
		t.Fatalf("events = %#v", events)
	}
}

func TestRunSessionResultReportsStreamGapHint(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions/session-1/run",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSONLines(t, w,
				protocol.Event{Type: protocol.EventGatewayStreamGap, Data: protocol.GatewayStreamGapEvent{DroppedEvents: 17, ReplayAfterSeq: 3}},
				protocol.Event{Seq: 4, Type: protocol.EventRunCompleted},
			)
		},
	})

	var events []protocol.Event
	result, err := New(server.URL).RunSessionResult(context.Background(), "session-1", gatewayapi.RunRequest{Prompt: "ping"}, func(event protocol.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StreamGaps != 1 || result.DroppedEvents != 17 || !result.Completed || result.LastSeq != 4 {
		t.Fatalf("result = %#v", result)
	}
	if len(events) != 2 || events[0].Type != protocol.EventGatewayStreamGap {
		t.Fatalf("events = %#v", events)
	}
}

func TestRunSessionResultDecodesLargeNDJSONEvents(t *testing.T) {
	large := strings.Repeat("x", 4*1024*1024+512)
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions/session-1/run",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSONLines(t, w,
				protocol.Event{Seq: 1, Type: protocol.EventAssistantDelta, Data: large},
				protocol.Event{Seq: 2, Type: protocol.EventRunCompleted},
			)
		},
	})

	var got string
	result, err := New(server.URL).RunSessionResult(context.Background(), "session-1", gatewayapi.RunRequest{Prompt: "ping"}, func(event protocol.Event) {
		if event.Type == protocol.EventAssistantDelta {
			got, _ = event.Data.(string)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.Failed || result.LastSeq != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(got) != len(large) {
		t.Fatalf("large event length = %d, want %d", len(got), len(large))
	}
}

func TestCancelSessionUsesTypedResponse(t *testing.T) {
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions/session-1/cancel",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			testkit.WriteJSON(t, w, gatewayapi.CancelSessionResponse{Cancelled: true})
		},
	})

	cancelled, err := New(server.URL).CancelSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("cancelled = false, want true")
	}
}
