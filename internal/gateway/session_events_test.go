package gateway

import (
	"bufio"
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
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/trace"
)

func TestGatewaySessionRunStreamsEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
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

func TestGatewaySessionRunInterruptPolicyCancelsActiveRunAndStartsReplacement(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
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

	session, ok := server.session(created.ID)
	if !ok {
		t.Fatal("created session missing from server")
	}
	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- session.Thread.Run(context.Background(), sessionpkg.RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
			emit(protocol.Event{Type: protocol.EventRunStarted, RunID: "run-old"})
			close(firstStarted)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "old prompt", func(event protocol.Event) {
			if event.Type == protocol.EventRunStarted {
				session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"})
			}
			session.observeRunEvent(event)
		})
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first session run did not start")
	}

	replacement := bytes.NewBufferString(`{"prompt":"new prompt","interrupt_policy":"interrupt"}`)
	secondResp, err := http.Post(httpServer.URL+"/v1/sessions/"+created.ID+"/run", "application/json", replacement)
	if err != nil {
		t.Fatal(err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("second run status = %d body=%s", secondResp.StatusCode, body)
	}
	secondEvents := readProtocolEvents(t, secondResp.Body)
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var sawNewDelta, sawNewCompleted bool
	for _, event := range secondEvents {
		switch event.Type {
		case protocol.EventAssistantDelta:
			if fmt.Sprint(event.Data) == "mock: new prompt" {
				sawNewDelta = true
			}
		case protocol.EventRunCompleted:
			sawNewCompleted = true
		}
		if strings.Contains(fmt.Sprint(event.Data), "old prompt") {
			t.Fatalf("replacement stream leaked old prompt event: %#v", event)
		}
	}
	if !sawNewDelta || !sawNewCompleted {
		t.Fatalf("replacement events missing delta/completion: %#v", secondEvents)
	}
	replayed, err := server.store.ReplayEventsAfter(created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var oldFailed bool
	for _, event := range replayed {
		if event.Type == protocol.EventRunFailed && fmt.Sprint(event.Data) == "interrupted by newer session run" {
			oldFailed = true
		}
	}
	if !oldFailed {
		t.Fatalf("stored events missing interrupted old run failure: %#v", replayed)
	}
}

func TestStreamEventsDoesNotDuplicateEmittedRunFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	streamEvents(rec, func(emit func(protocol.Event)) error {
		emit(protocol.Event{Type: protocol.EventRunStarted})
		emit(protocol.Event{Type: protocol.EventRunFailed, Data: "provider boom"})
		return errors.New("provider boom")
	})

	events := readProtocolEvents(t, rec.Body)
	failed := 0
	for _, event := range events {
		if event.Type == protocol.EventRunFailed {
			failed++
			if got := fmt.Sprint(event.Data); got != "provider boom" {
				t.Fatalf("failure data = %q", got)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("run.failed count = %d, events=%v", failed, events)
	}
}

func TestStreamEventsSynthesizesFailureForSetupError(t *testing.T) {
	rec := httptest.NewRecorder()
	streamEvents(rec, func(emit func(protocol.Event)) error {
		return errors.New("setup boom")
	})

	events := readProtocolEvents(t, rec.Body)
	if len(events) != 1 {
		t.Fatalf("event count = %d, events=%v", len(events), events)
	}
	if events[0].Type != protocol.EventRunFailed {
		t.Fatalf("event type = %s", events[0].Type)
	}
	if got := fmt.Sprint(events[0].Data); got != "setup boom" {
		t.Fatalf("failure data = %q", got)
	}
}

func TestStreamEventsDoesNotAppendFailureAfterRunCompleted(t *testing.T) {
	rec := httptest.NewRecorder()
	streamEvents(rec, func(emit func(protocol.Event)) error {
		emit(protocol.Event{Type: protocol.EventRunStarted})
		emit(protocol.Event{Type: protocol.EventRunCompleted})
		return errors.New("late cleanup boom")
	})

	events := readProtocolEvents(t, rec.Body)
	for _, event := range events {
		if event.Type == protocol.EventRunFailed {
			t.Fatalf("unexpected run.failed after completed run: events=%v", events)
		}
	}
}

func TestStreamEventsDoesNotBlockRunWhenClientWriterStalls(t *testing.T) {
	writer := newBlockingResponseWriter()
	runDone := make(chan struct{})
	streamDone := make(chan struct{})
	go func() {
		streamEvents(writer, func(emit func(protocol.Event)) error {
			emit(protocol.Event{Seq: 1, Type: protocol.EventRunStarted})
			select {
			case <-writer.writeStarted:
			case <-time.After(time.Second):
				t.Error("writer did not start")
			}
			for i := 0; i < liveRunStreamBuffer+20; i++ {
				emit(protocol.Event{Seq: int64(i + 2), Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%03d", i)})
			}
			emit(protocol.Event{Seq: int64(liveRunStreamBuffer + 22), Type: protocol.EventRunCompleted})
			close(runDone)
			return nil
		})
		close(streamDone)
	}()

	select {
	case <-writer.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not block on first write")
	}
	select {
	case <-runDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("run was blocked by stalled response writer")
	}
	select {
	case <-streamDone:
		t.Fatal("stream finished before writer was unblocked")
	default:
	}
	close(writer.unblock)
	select {
	case <-streamDone:
	case <-time.After(time.Second):
		t.Fatal("stream did not finish after writer unblocked")
	}

	events := readProtocolEvents(t, bytes.NewReader(writer.bytes()))
	var sawGap bool
	for _, event := range events {
		if event.Type == protocol.EventGatewayStreamGap {
			sawGap = true
			break
		}
	}
	if !sawGap {
		t.Fatalf("stream events missing gap hint: %#v", events)
	}
}

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

type blockingResponseWriter struct {
	header       http.Header
	writeStarted chan struct{}
	unblock      chan struct{}
	once         sync.Once
	mu           sync.Mutex
	body         bytes.Buffer
	status       int
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:       http.Header{},
		writeStarted: make(chan struct{}),
		unblock:      make(chan struct{}),
	}
}

func (w *blockingResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *blockingResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.writeStarted)
		<-w.unblock
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}

func (w *blockingResponseWriter) Flush() {}

func (w *blockingResponseWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.body.Bytes()...)
}
