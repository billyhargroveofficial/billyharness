package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/trace"
)

func TestGatewaySessionRunStreamsStoredSequencedEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"sequenced stream"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	var streamed []protocol.Event
	dec := json.NewDecoder(run.Body)
	for {
		var event protocol.Event
		err := dec.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		streamed = append(streamed, event)
	}
	if len(streamed) == 0 {
		t.Fatal("run streamed no events")
	}

	stored := readSessionEventRecords(t, filepath.Join(storeDir, created.ID, sessionEventsJSONLName))
	storedBySeq := make(map[int64]protocol.Event, len(stored))
	for _, record := range stored {
		storedBySeq[record.Seq] = record.Event
	}
	var lastSeq int64
	for _, event := range streamed {
		if event.Seq == 0 {
			t.Fatalf("streamed event has zero seq: %#v", event)
		}
		if event.Seq <= lastSeq {
			t.Fatalf("streamed event seq = %d after %d", event.Seq, lastSeq)
		}
		lastSeq = event.Seq
		storedEvent, ok := storedBySeq[event.Seq]
		if !ok {
			t.Fatalf("streamed event seq %d not found in stored events %#v", event.Seq, stored)
		}
		if !reflect.DeepEqual(event, storedEvent) {
			t.Fatalf("streamed event seq %d = %#v, stored = %#v", event.Seq, event, storedEvent)
		}
	}

	replay := httptest.NewRecorder()
	path := "/v1/sessions/" + created.ID + "/events?after_seq=" + strconv.FormatInt(lastSeq, 10) + "&follow=false"
	server.Handler().ServeHTTP(replay, httptest.NewRequest(http.MethodGet, path, nil))
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d body=%s", replay.Code, replay.Body.String())
	}
	dec = json.NewDecoder(replay.Body)
	for {
		var event protocol.Event
		err := dec.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Seq <= lastSeq {
			t.Fatalf("replay after final streamed seq returned duplicate event: seq %d after %d", event.Seq, lastSeq)
		}
	}
}

func TestGatewaySessionRunPersistsHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"one", "two"} {
		run := httptest.NewRecorder()
		server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"`+prompt+`"}`)))
		if run.Code != http.StatusOK {
			t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
		}
	}

	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		MessageCount int                `json:"message_count"`
		Messages     []protocol.Message `json:"messages"`
		Running      bool               `json:"running"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Running {
		t.Fatal("session reports running after completed runs")
	}
	if got.MessageCount != len(got.Messages) || got.MessageCount < 5 {
		t.Fatalf("unexpected message count: %+v", got)
	}
	if got.Messages[len(got.Messages)-4].Content != "one" ||
		got.Messages[len(got.Messages)-3].Content != "mock: one" ||
		got.Messages[len(got.Messages)-2].Content != "two" ||
		got.Messages[len(got.Messages)-1].Content != "mock: two" {
		t.Fatalf("unexpected history tail: %+v", got.Messages)
	}
}

func TestGatewaySessionStatusEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", status.Code, status.Body.String())
	}
	var got SessionStatus
	if err := json.Unmarshal(status.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Running || got.MessageCount == 0 {
		t.Fatalf("status = %#v", got)
	}
}

func TestGatewaySessionStatusIncludesAccessMode(t *testing.T) {
	session := newGatewaySession("access-mode-status", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	session.beginRunStatus(RunRequest{
		Provider:        "mock",
		Model:           "mock",
		Profile:         "billy",
		ReasoningEffort: "high",
		AccessMode:      "read-only",
	})
	status := session.Status()
	if status.AccessMode != config.AccessModePlan {
		t.Fatalf("status access mode = %q", status.AccessMode)
	}
	summary := sessionSummary(session)
	if summary.AccessMode != config.AccessModePlan {
		t.Fatalf("summary access mode = %q", summary.AccessMode)
	}
}

func TestGatewaySessionListEndpointReturnsTypedSummaries(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	var ids []string
	for i := 0; i < 2; i++ {
		create := httptest.NewRecorder()
		server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
		if create.Code != http.StatusCreated {
			t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
		}
		var created SessionResponse
		if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if created.ID == "" || created.MessageCount == 0 {
			t.Fatalf("created = %#v", created)
		}
		ids = append(ids, created.ID)
	}
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+ids[0]+"/run", bytes.NewBufferString(`{"prompt":"list me"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"messages"`) {
		t.Fatalf("session list should not include full messages: %s", rec.Body.String())
	}
	var listed SessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 2 {
		t.Fatalf("listed sessions = %#v", listed.Sessions)
	}
	byID := map[string]SessionSummary{}
	for _, summary := range listed.Sessions {
		byID[summary.ID] = summary
	}
	if byID[ids[0]].LastEvent != string(protocol.EventRunCompleted) || byID[ids[0]].MessageCount < 3 {
		t.Fatalf("run session summary = %#v", byID[ids[0]])
	}
	if byID[ids[1]].MessageCount == 0 {
		t.Fatalf("idle session summary = %#v", byID[ids[1]])
	}
}

func TestGatewaySessionOwnerMetadataPersistsAndLists(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := t.TempDir()
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	owner := SessionOwner{
		ClientType:       "telegram",
		TelegramChatID:   123,
		TelegramThreadID: 7,
		TelegramUserID:   1001,
		Profile:          "billy",
		Model:            "deepseek-v4-flash",
	}
	body, _ := json.Marshal(CreateSessionRequest{Profile: "billy", Owner: owner})
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Owner != owner || created.Status.Owner != owner {
		t.Fatalf("created owner = response:%#v status:%#v want %#v", created.Owner, created.Status.Owner, owner)
	}

	reloaded := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	list := httptest.NewRecorder()
	reloaded.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	var listed SessionListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].Owner != owner {
		t.Fatalf("listed sessions = %#v, want owner %#v", listed.Sessions, owner)
	}
	status := httptest.NewRecorder()
	reloaded.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", status.Code, status.Body.String())
	}
	var gotStatus SessionStatus
	if err := json.Unmarshal(status.Body.Bytes(), &gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus.Owner != owner {
		t.Fatalf("status owner = %#v, want %#v", gotStatus.Owner, owner)
	}
}

func TestGatewaySessionOwnerScopeFiltersAndDeniesCrossOwner(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	ownOwner := SessionOwner{ClientType: "telegram", TelegramChatID: 123, TelegramUserID: 1001}
	otherOwner := SessionOwner{ClientType: "telegram", TelegramChatID: 123, TelegramUserID: 2002}
	ownID := createScopedTestSession(t, server, ownOwner)
	otherID := createScopedTestSession(t, server, otherOwner)
	legacyID := createScopedTestSession(t, server, SessionOwner{})

	list := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	setScopedTestOwnerHeaders(listReq, ownOwner)
	server.Handler().ServeHTTP(list, listReq)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	var listed SessionListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	for _, summary := range listed.Sessions {
		visible[summary.ID] = true
	}
	if !visible[ownID] || !visible[legacyID] || visible[otherID] {
		t.Fatalf("visible sessions = %#v, want own and legacy only", visible)
	}

	for _, path := range []string{
		"/v1/sessions/" + ownID,
		"/v1/sessions/" + ownID + "/status",
		"/v1/sessions/" + ownID + "/context",
		"/v1/sessions/" + ownID + "/events?follow=false",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		setScopedTestOwnerHeaders(req, ownOwner)
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("own read %s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{
		"/v1/sessions/" + otherID,
		"/v1/sessions/" + otherID + "/status",
		"/v1/sessions/" + otherID + "/context",
		"/v1/sessions/" + otherID + "/events?follow=false",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		setScopedTestOwnerHeaders(req, ownOwner)
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-owner read %s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}

	for _, targetID := range []string{otherID, legacyID} {
		for _, tc := range []struct {
			method string
			path   string
			body   string
		}{
			{http.MethodPost, "/v1/sessions/" + targetID + "/inputs", `{"prompt":"nope"}`},
			{http.MethodPost, "/v1/sessions/" + targetID + "/run", `{"prompt":"nope"}`},
			{http.MethodPost, "/v1/sessions/" + targetID + "/user_input/request-1/answer", `{"text":"yes"}`},
			{http.MethodPost, "/v1/sessions/" + targetID + "/user_input/request-1/reject", `{}`},
			{http.MethodPost, "/v1/sessions/" + targetID + "/undo", `{}`},
			{http.MethodPost, "/v1/sessions/" + targetID + "/redo", ``},
			{http.MethodPost, "/v1/sessions/" + targetID + "/cancel", ``},
		} {
			rec := httptest.NewRecorder()
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			setScopedTestOwnerHeaders(req, ownOwner)
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("scoped mutation %s status = %d body=%s", tc.path, rec.Code, rec.Body.String())
			}
		}
	}

	create := httptest.NewRecorder()
	body, _ := json.Marshal(CreateSessionRequest{Owner: otherOwner})
	createReq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	setScopedTestOwnerHeaders(createReq, ownOwner)
	server.Handler().ServeHTTP(create, createReq)
	if create.Code != http.StatusForbidden {
		t.Fatalf("scoped create with mismatched owner status = %d body=%s", create.Code, create.Body.String())
	}
}

func createScopedTestSession(t *testing.T, server *Server, owner SessionOwner) string {
	t.Helper()
	body, _ := json.Marshal(CreateSessionRequest{Owner: owner})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func setScopedTestOwnerHeaders(req *http.Request, owner SessionOwner) {
	if owner.ClientType != "" {
		req.Header.Set(gatewayapi.HeaderSessionClientType, owner.ClientType)
	}
	if owner.TelegramChatID != 0 {
		req.Header.Set(gatewayapi.HeaderSessionTelegramChatID, strconv.FormatInt(owner.TelegramChatID, 10))
	}
	if owner.TelegramThreadID != 0 {
		req.Header.Set(gatewayapi.HeaderSessionTelegramThreadID, strconv.Itoa(owner.TelegramThreadID))
	}
	if owner.TelegramUserID != 0 {
		req.Header.Set(gatewayapi.HeaderSessionTelegramUserID, strconv.FormatInt(owner.TelegramUserID, 10))
	}
	if owner.TUIChatID != "" {
		req.Header.Set(gatewayapi.HeaderSessionTUIChatID, owner.TUIChatID)
	}
}

func TestGatewaySessionContextStatusEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.ContextWindowTokens = 1000
	cfg.ContextCompactTokens = 600
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	longText := strings.Repeat("context-heavy ", 80)
	body, _ := json.Marshal(CreateSessionRequest{Messages: []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: longText},
		{Role: protocol.RoleAssistant, Content: "short"},
	}})
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/context", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got SessionContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.MessageCount != 3 || got.ContextWindowTokens != 1000 || got.ContextCompactTokens != 600 {
		t.Fatalf("context status = %#v", got)
	}
	if got.EstimatedTokens <= 0 || got.PercentUsed <= 0 || got.CompactThresholdPercent != 60 {
		t.Fatalf("context usage fields = %#v", got)
	}
	if len(got.TopContributors) == 0 || got.TopContributors[0].Role != string(protocol.RoleUser) || got.TopContributors[0].EstimatedTokens <= 0 {
		t.Fatalf("top contributors = %#v", got.TopContributors)
	}
	if got.TopContributors[0].Source != "user_messages" {
		t.Fatalf("top contributor source = %#v", got.TopContributors[0])
	}
	if len(got.TopContributors[0].Preview) > 120 {
		t.Fatalf("preview too long: %q", got.TopContributors[0].Preview)
	}
}

func TestGatewaySessionInspectEndpointReturnsDurableDiagnostics(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	owner := gatewayapi.SessionOwner{ClientType: "telegram", TelegramChatID: 1001, TelegramUserID: 2002}
	body, _ := json.Marshal(CreateSessionRequest{Owner: owner})
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(`{"input_id":"inspect-input","prompt":"inspect live"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/inspect", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect status = %d body=%s", rec.Code, rec.Body.String())
	}
	var inspection StoredSessionInspection
	if err := json.Unmarshal(rec.Body.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.SessionID != created.ID || inspection.Owner.TelegramChatID != owner.TelegramChatID || inspection.Owner.TelegramUserID != owner.TelegramUserID {
		t.Fatalf("inspection identity/owner = %#v", inspection)
	}
	if !inspection.MessageSnapshotReady || !inspection.EventReplayReady || !inspection.OfflineReplayReady {
		t.Fatalf("inspection readiness = %#v", inspection)
	}
	if inspection.Events.Lifecycle.RunsStarted != 1 || inspection.Events.Lifecycle.RunsClosed != 1 || inspection.Events.Lifecycle.RunsOpen != 0 {
		t.Fatalf("lifecycle = %#v", inspection.Events.Lifecycle)
	}
	if !inspection.Events.Projector.ParityOK || inspection.Events.Projector.SeqRange == "" {
		t.Fatalf("projector = %#v", inspection.Events.Projector)
	}
	if !inspection.Inputs.Exists || !inspection.Inputs.ValidationValid || inspection.Inputs.Completed != 1 || inspection.Inputs.Records != 3 {
		t.Fatalf("inputs = %#v", inspection.Inputs)
	}
}

func TestGatewaySessionContextReportsMemoryDriftWithoutCurrentContents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	writeTestMemoryIndex(t, home, "Prefers first evidence summary", "FIRST SECRET TOPIC BODY")

	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MemoryEnabled = true
	cfg.MemorySummaryMaxBytes = 2048
	cfg.MemoryIndexMaxBytes = 4096
	cfg.MemoryTopicMaxBytes = 4096
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	initial := readSessionContext(t, server, created.ID)
	if initial.Memory.Policy != "session_locked" || initial.Memory.Status != "current" || initial.Memory.LockedHash == "" || initial.Memory.CurrentHash == "" {
		t.Fatalf("initial memory drift status = %#v", initial.Memory)
	}
	if initial.Memory.LockedHash != initial.Memory.CurrentHash || initial.Memory.CurrentEntries != 1 {
		t.Fatalf("initial memory hashes/counts = %#v", initial.Memory)
	}

	writeTestMemoryIndex(t, home, "Prefers updated evidence summary", "SECOND SECRET TOPIC BODY")
	changed := readSessionContext(t, server, created.ID)
	if changed.Memory.Status != "changed" || changed.Memory.LockedHash == "" || changed.Memory.CurrentHash == "" || changed.Memory.LockedHash == changed.Memory.CurrentHash {
		t.Fatalf("changed memory drift status = %#v", changed.Memory)
	}
	formatted := gatewayclient.FormatSessionContext(changed)
	for _, want := range []string{"memory:", "status=changed", "policy=session_locked", "locked=", "current="} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted context missing %q:\n%s", want, formatted)
		}
	}
	for _, forbidden := range []string{"Prefers updated evidence summary", "SECOND SECRET TOPIC BODY"} {
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("formatted context leaked current memory content %q:\n%s", forbidden, formatted)
		}
	}

	offline, err := StoredSessionContext(storeDir, created.ID, cfg.RuntimeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if offline.Memory.Status != "locked" || offline.Memory.LockedHash != changed.Memory.LockedHash || offline.Memory.CurrentHash != "" {
		t.Fatalf("offline memory status = %#v, live=%#v", offline.Memory, changed.Memory)
	}
}

func TestGatewaySessionRunRecordsContextEpochDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	writeTestMemoryIndex(t, home, "Prefers first evidence summary", "FIRST SECRET TOPIC BODY")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agent-index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("project rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent-index", "docs-manifest.json"), []byte(`{"schema_version":1,"docs":["README.md"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.ProjectDocMaxBytes = 2048
	cfg.MemoryEnabled = true
	cfg.MemorySummaryMaxBytes = 2048
	cfg.MemoryIndexMaxBytes = 4096
	cfg.MemoryTopicMaxBytes = 4096
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	firstRun := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstRun, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(`{"prompt":"first"}`)))
	if firstRun.Code != http.StatusOK {
		t.Fatalf("first run status = %d body=%s", firstRun.Code, firstRun.Body.String())
	}
	writeTestMemoryIndex(t, home, "Prefers updated evidence summary", "SECOND SECRET TOPIC BODY")
	secondRun := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondRun, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(`{"prompt":"second"}`)))
	if secondRun.Code != http.StatusOK {
		t.Fatalf("second run status = %d body=%s", secondRun.Code, secondRun.Body.String())
	}

	events, err := server.store.ReplayEventsAfter(created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var secondStarted protocol.Event
	var secondStatus SessionStatus
	runStartedCount := 0
	for _, event := range events {
		if event.Type == protocol.EventRunStarted {
			runStartedCount++
			if runStartedCount == 2 {
				secondStarted = event
			}
		}
		if event.Type == protocol.EventSessionStatus {
			var status SessionStatus
			if body, err := json.Marshal(event.Data); err == nil && json.Unmarshal(body, &status) == nil && status.RunSeq == 2 {
				secondStatus = status
			}
		}
	}
	if secondStatus.ContextEpoch == nil || secondStatus.LockedEpoch == nil || secondStatus.ContextDrift == nil {
		t.Fatalf("second status missing context epoch: %#v", secondStatus)
	}
	if secondStatus.ContextEpoch.DocsIndexHash == "" || secondStatus.ContextEpoch.ConfigHash == "" || secondStatus.ContextEpoch.ToolCatalogHash == "" || secondStatus.ContextEpoch.MCPCatalogHash == "" {
		t.Fatalf("second status epoch missing required hashes: %#v", secondStatus.ContextEpoch)
	}
	if secondStatus.ContextDrift.Status != "changed" ||
		secondStatus.ContextDrift.LockedHash == "" ||
		secondStatus.ContextDrift.CurrentHash == "" ||
		!gatewayTestHasString(secondStatus.ContextDrift.ChangedFields, "memory_hash") {
		t.Fatalf("second status drift = %#v", secondStatus.ContextDrift)
	}
	startedData := mapFromEventData(secondStarted.Data)
	if startedData["context_epoch"] == nil || startedData["context_epoch_drift"] == nil || startedData["context_epoch_warning"] == "" {
		t.Fatalf("second run.started missing context epoch drift: %#v", startedData)
	}
	body, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Prefers updated evidence summary", "SECOND SECRET TOPIC BODY"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("context epoch event leaked current memory content %q: %s", forbidden, body)
		}
	}

	live := readSessionContext(t, server, created.ID)
	if live.ContextDrift == nil || live.ContextDrift.Status != "changed" || !gatewayTestHasString(live.ContextDrift.ChangedFields, "memory_hash") {
		t.Fatalf("live context drift = %#v", live.ContextDrift)
	}
	if formatted := gatewayclient.FormatSessionContext(live); !strings.Contains(formatted, "context epoch: status=changed") || !strings.Contains(formatted, "memory_hash") {
		t.Fatalf("formatted context missing epoch drift:\n%s", formatted)
	}
	offline, err := StoredSessionContext(storeDir, created.ID, cfg.RuntimeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if offline.ContextDrift == nil || offline.ContextDrift.Status != "changed" || !gatewayTestHasString(offline.ContextDrift.ChangedFields, "memory_hash") {
		t.Fatalf("offline context drift = %#v", offline.ContextDrift)
	}
}

func readSessionContext(t *testing.T, server *Server, sessionID string) SessionContextResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/context", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got SessionContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func gatewayTestHasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeTestMemoryIndex(t *testing.T, home, summary, body string) {
	t.Helper()
	memoryRoot := filepath.Join(home, "memory")
	if err := os.MkdirAll(filepath.Join(memoryRoot, "topics"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `- type=user topic=style summary="` + summary + `" path=topics/style.md` + "\n"
	if err := os.WriteFile(filepath.Join(memoryRoot, "MEMORY.md"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "topics", "style.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGatewaySessionContextUsesEffectiveSessionModelWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.ContextWindowTokens = 1_000_000
	cfg.ContextCompactTokens = 600_000
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	session := server.sessions[created.ID]
	session.beginRunStatus(RunRequest{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		Profile:         "billy",
		ReasoningEffort: "high",
		AccessMode:      config.AccessModeBuild,
	})

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/context", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got SessionContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := modelinfo.Lookup("gpt-5.5").ContextWindowTokens; got.ContextWindowTokens != want {
		t.Fatalf("context window = %d, want %d: %#v", got.ContextWindowTokens, want, got)
	}
	if got.ContextCompactTokens != int64(got.ContextWindowTokens*60/100) || got.ContextWindowSource != "model" {
		t.Fatalf("context compact/source = compact:%d source:%q response=%#v", got.ContextCompactTokens, got.ContextWindowSource, got)
	}
	if got.Runtime.Provider != "openai-codex" || got.Runtime.Model != "gpt-5.5" || got.Runtime.Profile != "billy" || got.Runtime.AccessMode != config.AccessModeBuild {
		t.Fatalf("runtime = %#v", got.Runtime)
	}
}

func TestGatewaySessionSnapshotsUseEffectiveSessionModelWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.ContextWindowTokens = 1_000_000
	cfg.ContextCompactTokens = 600_000
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	session := server.sessions[created.ID]
	session.beginRunStatus(RunRequest{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		Profile:         "billy",
		ReasoningEffort: "high",
		AccessMode:      config.AccessModeBuild,
	})
	if err := server.saveSession(session); err != nil {
		t.Fatal(err)
	}

	wantWindow := modelinfo.Lookup("gpt-5.5").ContextWindowTokens
	var configSnapshot map[string]any
	readJSONFile(t, filepath.Join(storeDir, created.ID, sessionConfigSnapshotName), &configSnapshot)
	if got := int64(configSnapshot["context_window_tokens"].(float64)); got != wantWindow {
		t.Fatalf("config snapshot context_window_tokens = %d, want %d; snapshot=%#v", got, wantWindow, configSnapshot)
	}
	if got := configSnapshot["model"]; got != "gpt-5.5" {
		t.Fatalf("config snapshot model = %#v", got)
	}

	var modelSnapshot map[string]any
	readJSONFile(t, filepath.Join(storeDir, created.ID, sessionModelSnapshotName), &modelSnapshot)
	if got := int64(modelSnapshot["context_budget_tokens"].(float64)); got != wantWindow {
		t.Fatalf("model snapshot context_budget_tokens = %d, want %d; snapshot=%#v", got, wantWindow, modelSnapshot)
	}
	if got := modelSnapshot["model_id"]; got != "gpt-5.5" {
		t.Fatalf("model snapshot model_id = %#v", got)
	}

	offline, err := StoredSessionContext(storeDir, created.ID, config.RuntimeLimits{ContextWindowTokens: 42, ContextCompactTokens: 24})
	if err != nil {
		t.Fatal(err)
	}
	if offline.ContextWindowTokens != wantWindow || offline.ContextCompactTokens != int64(wantWindow*60/100) {
		t.Fatalf("offline context limits = window:%d compact:%d want window:%d", offline.ContextWindowTokens, offline.ContextCompactTokens, wantWindow)
	}
}

func TestGatewayBenchmarksEndpointListsManifestSummaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	runDir := filepath.Join(home, "bench-runs", "smoke")
	payloadsDir := filepath.Join(runDir, "20260628T100000Z-payloads")
	if err := os.MkdirAll(payloadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	resultsPath := filepath.Join(runDir, "20260628T100000Z-results.jsonl")
	eventsPath := filepath.Join(runDir, "20260628T100000Z-events.jsonl")
	if err := os.WriteFile(resultsPath, []byte(`{"task_id":"one","outcome":"pass"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsPath, []byte(`{"seq":1,"run_id":"20260628T100000Z"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runDir, "20260628T100000Z-manifest.json")
	manifest := trace.Manifest{
		SchemaVersion: trace.CurrentManifestVersion,
		RunID:         "20260628T100000Z",
		CreatedAt:     time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
		Harness:       "fast-agent-harness-go",
		ProfileHash:   "profile123",
		TasksPath:     "tasks.jsonl",
		TaskCount:     1,
		ResultsJSONL:  resultsPath,
		EventsJSONL:   eventsPath,
		PayloadsDir:   payloadsDir,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/benchmarks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("benchmarks status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got BenchmarkListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Dir != filepath.Join(home, "bench-runs") || len(got.Runs) != 1 {
		t.Fatalf("benchmarks response = %#v", got)
	}
	run := got.Runs[0]
	if run.RunID != manifest.RunID || run.TaskCount != 1 || run.ProfileHash != "profile123" ||
		!run.ResultsPresent || !run.EventsPresent || !run.PayloadsPresent {
		t.Fatalf("benchmark run summary = %#v", run)
	}
}

func TestGatewaySessionEventsSubscribeReceivesRunEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	createResp, err := http.Post(httpServer.URL+"/v1/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d body=%s", createResp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/sessions/"+created.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(eventsResp.Body)
		t.Fatalf("events status = %d body=%s", eventsResp.StatusCode, body)
	}
	events := decodeProtocolEvents(eventsResp.Body)
	first := waitProtocolEvent(t, events)
	if first.Type != protocol.EventSessionStatus {
		t.Fatalf("first event = %#v", first)
	}
	if status := eventStatus(t, first); status.ID != created.ID || status.Running {
		t.Fatalf("initial status = %#v", status)
	}

	runBody := bytes.NewBufferString(`{"prompt":"subscribe me","model":"mock","reasoning_effort":"high"}`)
	runResp, err := http.Post(httpServer.URL+"/v1/sessions/"+created.ID+"/run", "application/json", runBody)
	if err != nil {
		t.Fatal(err)
	}
	defer runResp.Body.Close()
	if runResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(runResp.Body)
		t.Fatalf("run status = %d body=%s", runResp.StatusCode, body)
	}
	io.Copy(io.Discard, runResp.Body)

	var sawDelta, sawCompleted bool
	var seen []protocol.EventType
	for i := 0; i < 20 && !(sawDelta && sawCompleted); i++ {
		event := waitProtocolEvent(t, events)
		seen = append(seen, event.Type)
		switch event.Type {
		case protocol.EventAssistantDelta:
			if fmt.Sprint(event.Data) == "mock: subscribe me" {
				sawDelta = true
			}
		case protocol.EventRunCompleted:
			sawCompleted = true
		}
	}
	if !sawDelta || !sawCompleted {
		t.Fatalf("events missing pieces: delta=%t completed=%t seen=%v", sawDelta, sawCompleted, seen)
	}
	statusResp, err := http.Get(httpServer.URL + "/v1/sessions/" + created.ID + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var finalStatus SessionStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&finalStatus); err != nil {
		t.Fatal(err)
	}
	if finalStatus.Running || finalStatus.LastEvent != string(protocol.EventRunCompleted) || finalStatus.MessageCount < 3 {
		t.Fatalf("final status = %#v", finalStatus)
	}
}

func TestGatewaySessionEventsFollowUsesLiveHubOnlyAsStoreWake(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("created session missing")
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/sessions/"+sessionID+"/events?after_seq=0&follow=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("events status = %d body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	events := decodeProtocolEvents(resp.Body)

	runID := gatewaySessionRunID(session.ID, 1)
	turnID := "turn-1"
	stepID := "turn-1:model-call-1"
	for _, event := range []protocol.Event{
		{Type: protocol.EventRunStarted, RunID: runID},
		{Type: protocol.EventTurnStarted, RunID: runID, TurnID: turnID},
		{Type: protocol.EventStepStarted, RunID: runID, TurnID: turnID, StepID: stepID},
	} {
		if _, err := server.store.AppendEvent(session, event); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	resp.Body.Close()

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/sessions/"+sessionID+"/events?after_seq=3&follow=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("events status = %d body=%s", resp.StatusCode, body)
	}
	events = decodeProtocolEvents(resp.Body)

	stored, err := server.store.AppendEvent(session, protocol.Event{
		Type:   protocol.EventAssistantDelta,
		RunID:  runID,
		TurnID: turnID,
		StepID: stepID,
		Data:   "durable recovered",
	})
	if err != nil {
		t.Fatal(err)
	}
	session.events.Publish(protocol.Event{Seq: 999, Type: protocol.EventAssistantDelta, Data: "live wake only"})

	got := waitProtocolEvent(t, events)
	if got.Seq != stored.Seq || got.Data != "durable recovered" {
		t.Fatalf("follow emitted %#v, want durable stored event %#v", got, stored)
	}
}

func TestGatewaySessionEventsFollowEmitsNonDurableLiveEvent(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("created session missing")
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/sessions/"+sessionID+"/events?after_seq=0&follow=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("events status = %d body=%s", resp.StatusCode, body)
	}
	events := decodeProtocolEvents(resp.Body)

	session.events.Publish(protocol.Event{Type: protocol.EventRunFailed, Data: "append failed after publish"})

	got := waitProtocolEvent(t, events)
	if got.Seq != 0 || got.Type != protocol.EventRunFailed || got.Data != "append failed after publish" {
		t.Fatalf("follow emitted %#v, want non-durable live run.failed", got)
	}
}

func TestGatewaySessionEventsReportSlowSubscriberDrops(t *testing.T) {
	session := newGatewaySession("slow-subscriber", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	events, unsubscribe := session.Subscribe()
	defer unsubscribe()

	extra := 17
	start := time.Now()
	for i := 0; i < eventHubSubscriberBuffer+extra; i++ {
		session.publish(protocol.Event{Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%03d", i)})
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("publishing to full subscriber took %s", elapsed)
	}

	var buffered int
drain:
	for {
		select {
		case <-events:
			buffered++
		default:
			break drain
		}
	}
	if buffered != eventHubSubscriberBuffer {
		t.Fatalf("buffered events = %d, want %d", buffered, eventHubSubscriberBuffer)
	}
	if got := session.Status().DroppedEvents; got != int64(extra) {
		t.Fatalf("dropped events = %d, want %d", got, extra)
	}
}

func readJSONFile(t *testing.T, path string, out any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
