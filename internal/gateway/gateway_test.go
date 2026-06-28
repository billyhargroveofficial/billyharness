package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewaySessionRunStreamsEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.ID == "" {
		t.Fatal("empty session id")
	}

	body := bytes.NewBufferString(`{"prompt":"through gateway"}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	var content strings.Builder
	scanner := bufio.NewScanner(run.Body)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == protocol.EventAssistantDelta {
			content.WriteString(event.Data.(string))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := content.String(); got != "mock: through gateway" {
		t.Fatalf("content = %q", got)
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
	if len(got.TopContributors[0].Preview) > 120 {
		t.Fatalf("preview too long: %q", got.TopContributors[0].Preview)
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

func TestGatewaySessionStoreRestoresSessionAfterRestart(t *testing.T) {
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
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"persist me"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}

	sessionDir := filepath.Join(storeDir, created.ID)
	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	historyPath := filepath.Join(sessionDir, sessionHistoryJSONLName)
	eventsPath := filepath.Join(sessionDir, sessionEventsJSONLName)
	assertPerm(t, storeDir, 0o700)
	assertPerm(t, sessionDir, 0o700)
	assertPerm(t, manifestPath, 0o600)
	assertPerm(t, historyPath, 0o600)
	assertPerm(t, eventsPath, 0o600)
	assertPerm(t, filepath.Join(storeDir, created.ID+".json"), 0o600)

	var manifest sessionManifest
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SessionID != created.ID || manifest.HistoryJSONL != sessionHistoryJSONLName || manifest.EventsJSONL != sessionEventsJSONLName {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.HistorySeq < 2 || manifest.EventSeq == 0 || manifest.MessageCount < 3 || manifest.HistorySHA256 == "" {
		t.Fatalf("manifest missing replay metadata: %#v", manifest)
	}

	history := readSessionHistoryRecords(t, historyPath)
	if len(history) != int(manifest.HistorySeq) || len(history) < 2 {
		t.Fatalf("history len = %d manifest seq = %d", len(history), manifest.HistorySeq)
	}
	if history[0].Kind != sessionHistoryCreated || history[len(history)-1].Kind != sessionHistorySnapshot {
		t.Fatalf("history kinds = first %q last %q", history[0].Kind, history[len(history)-1].Kind)
	}
	lastMessages := history[len(history)-1].Messages
	if len(lastMessages) == 0 || lastMessages[len(lastMessages)-1].Content != "mock: persist me" {
		t.Fatalf("history did not capture latest messages: %#v", lastMessages)
	}

	events := readSessionEventRecords(t, eventsPath)
	if len(events) != int(manifest.EventSeq) {
		t.Fatalf("events len = %d manifest seq = %d", len(events), manifest.EventSeq)
	}
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("event seq[%d] = %d", i, event.Seq)
		}
	}
	for _, typ := range []protocol.EventType{protocol.EventSessionStatus, protocol.EventRunStarted, protocol.EventAssistantDelta, protocol.EventRunCompleted} {
		if !sawSessionEvent(events, typ) {
			t.Fatalf("events missing %s: %#v", typ, events)
		}
	}

	if err := writeLegacySnapshot(filepath.Join(storeDir, created.ID+".json"), storedSession{
		ID:       created.ID,
		Created:  time.Now().UTC(),
		Updated:  time.Now().UTC(),
		Messages: []protocol.Message{{Role: protocol.RoleSystem, Content: "stale legacy snapshot"}},
	}); err != nil {
		t.Fatal(err)
	}

	restarted := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	get := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) == 0 || got.Messages[len(got.Messages)-1].Content != "mock: persist me" {
		t.Fatalf("restored messages = %#v", got.Messages)
	}
}

func TestGatewaySessionEventsReplayAfterSeqAcrossRestart(t *testing.T) {
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
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"replay me"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}

	stored := readSessionEventRecords(t, filepath.Join(storeDir, created.ID, sessionEventsJSONLName))
	if len(stored) < 3 {
		t.Fatalf("stored events too short: %#v", stored)
	}
	afterSeq := stored[0].Seq

	restarted := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	httpServer := httptest.NewServer(restarted.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/sessions/"+created.ID+"/events?after_seq="+strconv.FormatInt(afterSeq, 10), nil)
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
	dec := json.NewDecoder(resp.Body)
	for i := 1; i < len(stored); i++ {
		var got protocol.Event
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("decode replay event %d: %v", i, err)
		}
		want := stored[i].Event
		if got.Seq <= afterSeq || got.Seq != want.Seq || got.Type != want.Type {
			t.Fatalf("replayed event %d = seq %d type %s, want seq %d type %s after %d", i, got.Seq, got.Type, want.Seq, want.Type, afterSeq)
		}
	}
}

func TestGatewaySessionEventsRejectsInvalidAfterSeq(t *testing.T) {
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

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/events?after_seq=not-a-number", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "after_seq") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGatewaySessionStoreLoadsLegacySnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	if err := writeLegacySnapshot(filepath.Join(storeDir, "legacy-session.json"), storedSession{
		ID:      "legacy-session",
		Created: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
		Updated: time.Date(2026, 6, 28, 12, 1, 0, 0, time.UTC),
		Messages: []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "old prompt"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/legacy-session", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[1].Content != "old prompt" {
		t.Fatalf("legacy messages = %#v", got.Messages)
	}
}

func TestGatewaySessionCancelEndpointCancelsActiveThread(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	thread := sessionpkg.New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	server.sessions["test-session"] = &Session{
		ID:      "test-session",
		Created: time.Now().UTC(),
		Thread:  thread,
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- thread.Run(context.Background(), sessionpkg.RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
			close(started)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "wait", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("thread did not start")
	}

	cancel := httptest.NewRecorder()
	server.Handler().ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/sessions/test-session/cancel", nil))
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancel.Code, cancel.Body.String())
	}
	if !strings.Contains(cancel.Body.String(), `"cancelled":true`) {
		t.Fatalf("cancel response = %s", cancel.Body.String())
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("thread error = %v, want context.Canceled", err)
	}
}

func TestGatewayRunAcceptsModelOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"prompt":"override","model":"mock","thinking":"disabled","reasoning_effort":"high","max_tool_rounds":3}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("unexpected body=%s", run.Body.String())
	}
}

func TestGatewayRunProviderOverrideWorksWithoutDefaultCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.APIKeyEnv = "BILLYHARNESS_TEST_MISSING_KEY"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"prompt":"override","provider":"mock","model":"mock","max_tool_rounds":2}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("override was not used, body=%s", run.Body.String())
	}
	if strings.Contains(run.Body.String(), string(protocol.EventRunFailed)) {
		t.Fatalf("run failed: %s", run.Body.String())
	}
}

func TestGatewayAuthEndpointsSaveDeepSeekAndImportCodex(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	deepseek := httptest.NewRecorder()
	server.Handler().ServeHTTP(deepseek, httptest.NewRequest(http.MethodPost, "/v1/auth/deepseek", bytes.NewBufferString(`{"api_key":"sk-test-secret"}`)))
	if deepseek.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", deepseek.Code, deepseek.Body.String())
	}
	if strings.Contains(deepseek.Body.String(), "sk-test-secret") {
		t.Fatalf("response leaked key: %s", deepseek.Body.String())
	}
	if body, err := os.ReadFile(filepath.Join(root, ".env")); err != nil || !strings.Contains(string(body), "DEEPSEEK_API_KEY=sk-test-secret") {
		t.Fatalf(".env body=%q err=%v", body, err)
	}

	sourceDir := filepath.Join(root, "codex")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "auth.json")
	if err := os.WriteFile(source, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"a.b.c","refresh_token":"rt-test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := httptest.NewRecorder()
	server.Handler().ServeHTTP(codex, httptest.NewRequest(http.MethodPost, "/v1/auth/codex/import", bytes.NewBufferString(`{"source_path":"`+source+`"}`)))
	if codex.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", codex.Code, codex.Body.String())
	}
	if strings.Contains(codex.Body.String(), "rt-test") {
		t.Fatalf("response leaked refresh token: %s", codex.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "auth", "codex.json")); err != nil {
		t.Fatal(err)
	}

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/auth/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"configured":true`) {
		t.Fatalf("status = %d body=%s", status.Code, status.Body.String())
	}
}

func TestGatewayConfigStatusIsSanitized(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DEEPSEEK_API_KEY=sk-gateway-config-secret\nFAST_AGENT_MODEL=deepseek-v4-pro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "sk-gateway-config-secret") {
		t.Fatalf("config endpoint leaked secret: %s", body)
	}
	if !strings.Contains(body, `"key":"model"`) || !strings.Contains(body, config.SourceGateway) {
		t.Fatalf("config endpoint missing runtime model provenance: %s", body)
	}
}

func TestGatewayAuthMiddlewareProtectsNonLoopbackClients(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{AuthToken: "secret"})

	health := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthReq.RemoteAddr = "203.0.113.10:4444"
	server.Handler().ServeHTTP(health, healthReq)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", health.Code, health.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	unauthorizedReq.RemoteAddr = "203.0.113.10:4444"
	server.Handler().ServeHTTP(unauthorized, unauthorizedReq)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	if unauthorized.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate header")
	}

	authorized := httptest.NewRecorder()
	authorizedReq := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	authorizedReq.RemoteAddr = "203.0.113.10:4444"
	authorizedReq.Header.Set("Authorization", "Bearer secret")
	server.Handler().ServeHTTP(authorized, authorizedReq)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", authorized.Code, authorized.Body.String())
	}

	local := httptest.NewRecorder()
	localReq := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	localReq.RemoteAddr = "127.0.0.1:4444"
	server.Handler().ServeHTTP(local, localReq)
	if local.Code != http.StatusOK {
		t.Fatalf("local status = %d body=%s", local.Code, local.Body.String())
	}
}

func TestGatewayServeUsesPreboundListener(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(ctx, listener)
	}()
	if !WaitForReady(context.Background(), NormalizeBaseURL(listener.Addr().String()), time.Second) {
		cancel()
		select {
		case <-errs:
		case <-time.After(time.Second):
		}
		t.Fatal("gateway did not become ready on prebound listener")
	}
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
}

func TestGatewaySessionRunProviderOverrideWorksWithoutDefaultCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-flash"
	cfg.APIKeyEnv = "BILLYHARNESS_TEST_MISSING_KEY"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"prompt":"override","provider":"mock","model":"mock","max_tool_rounds":2}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("override was not used, body=%s", run.Body.String())
	}
}

func TestGatewayCreateSessionAcceptsMessages(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"messages":[{"role":"system","content":"system"},{"role":"user","content":"old"}]}`)
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", body))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		MessageCount int                `json:"message_count"`
		Messages     []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MessageCount != 2 || len(got.Messages) != 2 || got.Messages[1].Content != "old" {
		t.Fatalf("expected restored messages, got=%+v body=%s", got, get.Body.String())
	}
}

func TestGatewayCreateSessionUsesRequestedProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServer(cfg, provider.Mock{}, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"profile":"billy"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), "# Billyharness profile: billy") ||
		!strings.Contains(get.Body.String(), "Формулы пиши в LaTeX") {
		t.Fatalf("profile not injected: %s", get.Body.String())
	}
}

func TestGatewayToolsExposeMCPRegistry(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestGatewayFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_GATEWAY_MCP_HELPER": "1"},
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
		Enabled:        true,
	}}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	server := NewServer(cfg, provider.Mock{}, registry)

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `"name":"mcp__fake__echo"`) {
		t.Fatalf("tools body should expose lazy MCP gateway, not raw MCP tools: %s", body)
	}
	if !strings.Contains(body, `"name":"mcp_list_tools"`) || !strings.Contains(body, `"name":"mcp_call"`) || !strings.Contains(body, `"name":"time_now"`) {
		t.Fatalf("tools body missing lazy MCP/native tools: %s", body)
	}

	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"fake"`) ||
		!strings.Contains(rec.Body.String(), `"connected":true`) ||
		!strings.Contains(rec.Body.String(), `"state":"connected"`) ||
		!strings.Contains(rec.Body.String(), `"retry_count":0`) ||
		!strings.Contains(rec.Body.String(), `"restart_count":0`) {
		t.Fatalf("mcp body missing connected server status: %s", rec.Body.String())
	}
}

func TestGatewayFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_GATEWAY_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
			}})
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo text",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			}}}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		}
	}
	os.Exit(0)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func readSessionHistoryRecords(t *testing.T, path string) []sessionHistoryRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []sessionHistoryRecord
	decoder := json.NewDecoder(file)
	for {
		var record sessionHistoryRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func readSessionEventRecords(t *testing.T, path string) []sessionEventRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []sessionEventRecord
	decoder := json.NewDecoder(file)
	for {
		var record sessionEventRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func sawSessionEvent(events []sessionEventRecord, typ protocol.EventType) bool {
	for _, event := range events {
		if event.EventType == string(typ) {
			return true
		}
	}
	return false
}

func decodeProtocolEvents(r io.Reader) <-chan protocol.Event {
	out := make(chan protocol.Event, 64)
	go func() {
		defer close(out)
		dec := json.NewDecoder(r)
		for {
			var event protocol.Event
			if err := dec.Decode(&event); err != nil {
				return
			}
			out <- event
		}
	}()
	return out
}

func waitProtocolEvent(t *testing.T, events <-chan protocol.Event) protocol.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event stream closed")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return protocol.Event{}
	}
}

func eventStatus(t *testing.T, event protocol.Event) SessionStatus {
	t.Helper()
	bytes, err := json.Marshal(event.Data)
	if err != nil {
		t.Fatal(err)
	}
	var status SessionStatus
	if err := json.Unmarshal(bytes, &status); err != nil {
		t.Fatal(err)
	}
	return status
}
