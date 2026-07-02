package gatewayclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		"":                   "",
		":8765":              "http://127.0.0.1:8765",
		"127.0.0.1:8765/":    "http://127.0.0.1:8765",
		"http://0.0.0.0:80/": "http://127.0.0.1:80",
	}
	for input, want := range tests {
		if got := NormalizeBaseURL(input); got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStatusErrorMatchesSessionNotFound(t *testing.T) {
	err := &StatusError{Method: http.MethodGet, Path: "/v1/sessions/missing", StatusCode: http.StatusNotFound}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("StatusError should match ErrSessionNotFound: %v", err)
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
	for _, want := range []string{"helper usage: websum=20.0k→900", "helper=1.2k→80", "sumapi=1.3k", "api calls=2", "api cost=$0.004500"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context report missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "provider_api_calls") || strings.Contains(text, "provider_cost") || strings.Contains(text, "helper_api") {
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
	t.Setenv(GatewayAuthTokenEnv, "test-token")

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
