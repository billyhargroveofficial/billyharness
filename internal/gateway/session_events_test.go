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
	"strings"
	"testing"
	"time"

	agentpkg "github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
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

func TestGatewaySessionRunFailsClosedWhenFirstEventPersistenceFails(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	eventsPath := filepath.Join(storeDir, sessionID, sessionEventsJSONLName)
	if err := os.Mkdir(eventsPath, 0o700); err != nil {
		t.Fatal(err)
	}

	events := runGatewaySessionForTest(t, server, sessionID, `{"prompt":"persist fail"}`)
	if len(events) != 1 || events[0].Type != protocol.EventRunFailed {
		t.Fatalf("events = %#v, want only surfaced run.failed", events)
	}
	if events[0].Seq != 0 || !strings.Contains(fmt.Sprint(events[0].Data), "persistence failed") {
		t.Fatalf("run.failed = %#v", events[0])
	}
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("session missing")
	}
	status := session.Status()
	if status.Running || status.LastEvent != "persistence_failed" || !strings.Contains(status.LastError, "persistence failed") {
		t.Fatalf("status = %#v", status)
	}
}

func TestGatewaySessionObserveFailsClosedOnMidRunPersistenceFailure(t *testing.T) {
	session := newGatewaySession("persist-mid-run", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	session.eventRecorder = failingSessionRecorder(protocol.EventAssistantDelta)

	if err := session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := session.observeRunEvent(protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}); err != nil || !ok {
		t.Fatalf("run.started ok=%v err=%v", ok, err)
	}
	if event, ok, err := session.observeRunEvent(protocol.Event{Type: protocol.EventAssistantDelta, RunID: "run-1", TurnID: "turn-1", StepID: "step-1", Data: "non durable"}); err == nil || ok || event.Seq != 0 {
		t.Fatalf("assistant delta event=%#v ok=%v err=%v, want failed closed seq=0", event, ok, err)
	}
	status := session.Status()
	if status.Running || status.LastEvent != "persistence_failed" || !strings.Contains(status.LastError, "append failed for assistant.content_delta") {
		t.Fatalf("status = %#v", status)
	}
}

func TestGatewaySessionObserveFailsClosedOnTerminalPersistenceFailure(t *testing.T) {
	session := newGatewaySession("persist-terminal", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	session.eventRecorder = failingSessionRecorder(protocol.EventRunCompleted)

	if err := session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := session.observeRunEvent(protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}); err != nil || !ok {
		t.Fatalf("run.started ok=%v err=%v", ok, err)
	}
	if event, ok, err := session.observeRunEvent(protocol.Event{Type: protocol.EventRunCompleted, RunID: "run-1"}); err == nil || ok || event.Type != protocol.EventRunCompleted {
		t.Fatalf("run.completed event=%#v ok=%v err=%v, want failed closed", event, ok, err)
	}
	status := session.Status()
	if status.Running || status.LastEvent != "persistence_failed" || !strings.Contains(status.LastError, "append failed for run.completed") {
		t.Fatalf("status = %#v", status)
	}
}

func TestGatewaySessionEventsAfterSeqRequiresSessionStore(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{})
	sessionID := createGatewaySessionForTest(t, server)

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/events?after_seq=0&follow=false", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no session store") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGatewaySessionEventsAfterSeqReportsCorruptHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	eventsPath := filepath.Join(storeDir, sessionID, sessionEventsJSONLName)
	if err := os.WriteFile(eventsPath, []byte("{bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/events?after_seq=0&follow=false", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "corrupt session event history") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGatewaySessionRunPersistsCoalescedDeltasReplayValid(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	const chunks = 2000
	var want strings.Builder
	providerEvents := make([]provider.Event, 0, chunks+1)
	for i := 0; i < chunks; i++ {
		text := fmt.Sprintf("delta-%04d ", i)
		want.WriteString(text)
		providerEvents = append(providerEvents, provider.Event{Kind: provider.EventContent, Text: text})
	}
	providerEvents = append(providerEvents, provider.Event{Kind: provider.EventDone})
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{providerEvents}}
	registry := tools.NewRegistry(cfg)
	server := NewServerWithOptions(cfg, provider.Mock{}, registry, ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)

	streamed := runGatewaySessionAgentForTest(t, server, sessionID, agentpkg.New(cfg, prov, registry), "long stream")
	var streamedContent strings.Builder
	var streamedDeltaEvents int
	for _, event := range streamed {
		if event.Type == protocol.EventAssistantDelta {
			streamedDeltaEvents++
			streamedContent.WriteString(fmt.Sprint(event.Data))
		}
	}
	if streamedContent.String() != want.String() {
		t.Fatalf("streamed content len got=%d want=%d", streamedContent.Len(), want.Len())
	}
	if streamedDeltaEvents >= chunks/10 {
		t.Fatalf("streamed delta events = %d, want far fewer than chunks %d", streamedDeltaEvents, chunks)
	}

	replayed, err := server.store.ReplayEventsAfter(sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lastSeq int64
	var replayedDeltaEvents int
	projected := projector.New()
	var snap projector.Snapshot
	for _, event := range replayed {
		if event.Seq <= lastSeq {
			t.Fatalf("replayed seq not increasing: previous=%d event=%#v", lastSeq, event)
		}
		lastSeq = event.Seq
		if event.Type == protocol.EventAssistantDelta {
			replayedDeltaEvents++
		}
		snap = projected.Apply(event)
	}
	if replayedDeltaEvents != streamedDeltaEvents {
		t.Fatalf("replayed delta events = %d streamed=%d", replayedDeltaEvents, streamedDeltaEvents)
	}
	if snap.AssistantText != want.String() || snap.RunState != projector.RunStateCompleted {
		t.Fatalf("replayed snapshot state=%s content len got=%d want=%d", snap.RunState, len(snap.AssistantText), want.Len())
	}
}

func TestGatewaySessionUndoPreviewAndRestoreCheckpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	cfg.MaxToolRounds = 2
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"agent\n"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "done"},
			{Kind: provider.EventDone},
		},
	}}
	registry := tools.NewRegistry(cfg)
	server := NewServerWithOptions(cfg, provider.Mock{}, registry, ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	runGatewaySessionAgentForTest(t, server, sessionID, agentpkg.New(cfg, prov, registry), "write")
	path := filepath.Join(root, "out.txt")
	if got := readFileString(t, path); got != "agent\n" {
		t.Fatalf("written file = %q", got)
	}
	preview := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{"preview":true}`, http.StatusOK)
	if !preview.Preview || preview.ChangeID == "" || !strings.Contains(preview.Patch, "+agent") {
		t.Fatalf("preview = %#v", preview)
	}
	if got := readFileString(t, path); got != "agent\n" {
		t.Fatalf("preview mutated file: %q", got)
	}
	undo := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{}`, http.StatusOK)
	if undo.ChangeID != preview.ChangeID || len(undo.RestoredFiles) == 0 {
		t.Fatalf("undo = %#v preview=%#v", undo, preview)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("undo should remove newly-created file, stat err=%v", err)
	}
	inspectionAfterUndo, err := InspectStoredSession(storeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !inspectionAfterUndo.Events.RedoAvailable || inspectionAfterUndo.Events.RedoChangeID != undo.ChangeID ||
		!storedInspectionHasTurnChangeStatus(inspectionAfterUndo, undo.ChangeID, "reverted") {
		t.Fatalf("inspection after undo = %#v", inspectionAfterUndo.Events)
	}
	redo := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/redo", ``, http.StatusOK)
	if redo.ChangeID != undo.ChangeID || redo.Change.Status != "redone" || len(redo.RestoredFiles) == 0 {
		t.Fatalf("redo = %#v undo=%#v", redo, undo)
	}
	if got := readFileString(t, path); got != "agent\n" {
		t.Fatalf("redo restored file = %q", got)
	}
	postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/redo", ``, http.StatusNotFound)
	inspectionAfterRedo, err := InspectStoredSession(storeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspectionAfterRedo.Events.RedoAvailable ||
		!storedInspectionHasTurnChangeStatus(inspectionAfterRedo, undo.ChangeID, "redone") {
		t.Fatalf("inspection after redo = %#v", inspectionAfterRedo.Events)
	}
	replayed, err := server.store.ReplayEventsAfter(sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !sawEvent(replayed, protocol.EventTurnChangeRecorded) || !sawEvent(replayed, protocol.EventTurnChangeReverted) {
		t.Fatalf("stored events missing turn change/revert: %#v", replayed)
	}
}

func TestGatewaySessionUndoConflictDoesNotPartiallyRestore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	cfg.MaxToolRounds = 2
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"agent\n"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "done"},
			{Kind: provider.EventDone},
		},
	}}
	registry := tools.NewRegistry(cfg)
	server := NewServerWithOptions(cfg, provider.Mock{}, registry, ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	runGatewaySessionAgentForTest(t, server, sessionID, agentpkg.New(cfg, prov, registry), "write")
	path := filepath.Join(root, "out.txt")
	if err := os.WriteFile(path, []byte("user after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{}`, http.StatusConflict)
	if len(resp.Conflicts) == 0 {
		t.Fatalf("expected conflicts: %#v", resp)
	}
	if got := readFileString(t, path); got != "user after\n" {
		t.Fatalf("conflict restore should not modify file, got %q", got)
	}
}

func TestGatewaySessionUndoRejectsTamperedPatchArtifact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	cfg.MaxToolRounds = 2
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"agent\n"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "done"},
			{Kind: provider.EventDone},
		},
	}}
	registry := tools.NewRegistry(cfg)
	server := NewServerWithOptions(cfg, provider.Mock{}, registry, ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	runGatewaySessionAgentForTest(t, server, sessionID, agentpkg.New(cfg, prov, registry), "write")
	path := filepath.Join(root, "out.txt")
	preview := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{"preview":true}`, http.StatusOK)
	if preview.Change.PatchOutputRef == "" || preview.Change.PatchOutputRefSHA256 == "" {
		t.Fatalf("preview missing patch artifact metadata: %#v", preview.Change)
	}
	if err := os.WriteFile(preview.Change.PatchOutputRef, []byte(`{"tampered":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/undo", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "sha256 mismatch") {
		t.Fatalf("tampered undo status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := readFileString(t, path); got != "agent\n" {
		t.Fatalf("tampered undo mutated file: %q", got)
	}
}

func TestGatewaySessionUndoRollsBackRestoreWhenEventPersistenceFails(t *testing.T) {
	server, storeDir, sessionID, path := gatewayCheckpointSessionForTest(t)
	preview := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{"preview":true}`, http.StatusOK)
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("created session missing")
	}
	failSessionEventOnce(t, server, session, protocol.EventTurnChangeReverted)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/undo", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "restore rolled back") {
		t.Fatalf("undo status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := readFileString(t, path); got != "agent\n" {
		t.Fatalf("failed undo should roll workspace back to agent state, got %q", got)
	}
	inspection, err := InspectStoredSession(storeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if storedInspectionHasTurnChangeStatus(inspection, preview.ChangeID, "reverted") || inspection.Events.RedoAvailable {
		t.Fatalf("failed undo should not persist revert: %#v", inspection.Events)
	}
}

func TestGatewaySessionRedoRollsBackRestoreWhenEventPersistenceFails(t *testing.T) {
	server, storeDir, sessionID, path := gatewayCheckpointSessionForTest(t)
	undo := postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{}`, http.StatusOK)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("undo should remove file before redo test, stat err=%v", err)
	}
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("created session missing")
	}
	failSessionEventOnce(t, server, session, protocol.EventTurnChangeRecorded)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/redo", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "redo rolled back") {
		t.Fatalf("redo status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed redo should roll workspace back to undone state, stat err=%v", err)
	}
	inspection, err := InspectStoredSession(storeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Events.RedoAvailable || inspection.Events.RedoChangeID != undo.ChangeID ||
		storedInspectionHasTurnChangeStatus(inspection, undo.ChangeID, "redone") {
		t.Fatalf("failed redo should preserve redo availability: %#v", inspection.Events)
	}
}

func TestGatewaySessionUndoDeniedDuringActiveRun(t *testing.T) {
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
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- session.Thread.Run(context.Background(), RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
			close(started)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "hold", func(protocol.Event) {})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session run did not start")
	}
	postGatewayJSON[gatewayapi.SessionUndoResponse](t, server, "/v1/sessions/"+sessionID+"/undo", `{}`, http.StatusConflict)
	session.Thread.Cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run err = %v", err)
	}
}

func TestGatewaySessionRunReportsSaveFailureAfterVisibleRun(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	forceLegacySnapshotWriteFailure(t, storeDir, sessionID)

	events := runGatewaySessionForTest(t, server, sessionID, `{"prompt":"save failure"}`)
	var sawCompleted, sawPersistenceStatus bool
	for _, event := range events {
		switch event.Type {
		case protocol.EventRunCompleted:
			sawCompleted = true
		case protocol.EventSessionStatus:
			status := eventStatus(t, event)
			if status.LastEvent == "persistence_failed" && strings.Contains(status.LastError, "session save failed after run") {
				sawPersistenceStatus = true
			}
		}
	}
	if !sawCompleted || !sawPersistenceStatus {
		t.Fatalf("events missing completed=%v persistence_status=%v: %#v", sawCompleted, sawPersistenceStatus, events)
	}
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("created session missing")
	}
	status := session.Status()
	if status.LastEvent != "persistence_failed" || !strings.Contains(status.LastError, "session save failed after run") {
		t.Fatalf("status = %#v", status)
	}
}

func TestGatewaySessionRunInterruptReturnsSaveFailureBeforeReplacement(t *testing.T) {
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
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- session.Thread.Run(context.Background(), RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
			emit(protocol.Event{Type: protocol.EventRunStarted, RunID: "run-old"})
			close(started)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "old prompt", func(event protocol.Event) {
			if event.Type == protocol.EventRunStarted {
				if err := session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"}); err != nil {
					t.Errorf("begin status: %v", err)
					return
				}
			}
			if _, _, err := session.observeRunEvent(event); err != nil {
				t.Errorf("observe event: %v", err)
			}
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first session run did not start")
	}
	forceLegacySnapshotWriteFailure(t, storeDir, sessionID)

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/run", strings.NewReader(`{"prompt":"new prompt","interrupt_policy":"interrupt"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream status = %d body=%s", rec.Code, rec.Body.String())
	}
	events := readProtocolEvents(t, rec.Body)
	var sawFailure bool
	for _, event := range events {
		if event.Type == protocol.EventAssistantDelta && strings.Contains(fmt.Sprint(event.Data), "new prompt") {
			t.Fatalf("replacement run should not start after interrupt save failure: %#v", events)
		}
		if event.Type == protocol.EventRunFailed && strings.Contains(fmt.Sprint(event.Data), "session save failed after interrupt") {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Fatalf("stream missing interrupt save failure: %#v", events)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("first run err = %v", err)
	}
}

func TestGatewaySessionCancelReturnsSaveFailureAfterCancellation(t *testing.T) {
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
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- session.Thread.Run(context.Background(), RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
			emit(protocol.Event{Type: protocol.EventRunStarted, RunID: "run-cancel"})
			close(started)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "wait", func(event protocol.Event) {
			if event.Type == protocol.EventRunStarted {
				if err := session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"}); err != nil {
					t.Errorf("begin status: %v", err)
					return
				}
			}
			if _, _, err := session.observeRunEvent(event); err != nil {
				t.Errorf("observe event: %v", err)
			}
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session run did not start")
	}
	forceLegacySnapshotWriteFailure(t, storeDir, sessionID)

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/cancel", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "session save failed after cancel") {
		t.Fatalf("cancel status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run err = %v", err)
	}
	replayed, err := server.store.ReplayEventsAfter(sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawCancelFailure bool
	for _, event := range replayed {
		if event.Type == protocol.EventRunFailed && fmt.Sprint(event.Data) == "cancelled by session cancel endpoint" {
			sawCancelFailure = true
		}
	}
	if !sawCancelFailure {
		t.Fatalf("stored events missing cancel failure: %#v", replayed)
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
		firstDone <- session.Thread.Run(context.Background(), RunnerFunc(func(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
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

type gatewayScriptedProvider struct {
	steps [][]provider.Event
	calls int
}

func (p *gatewayScriptedProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, 16)
	errs := make(chan error, 1)
	step := p.calls
	p.calls++
	go func() {
		defer close(events)
		defer close(errs)
		if step >= len(p.steps) {
			events <- provider.Event{Kind: provider.EventDone}
			return
		}
		for _, event := range p.steps[step] {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case events <- event:
			}
		}
	}()
	return events, errs
}

func createGatewaySessionForTest(t *testing.T, server *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("empty session id")
	}
	return created.ID
}

func failingSessionRecorder(failType protocol.EventType) func(protocol.Event) (protocol.Event, error) {
	var seq int64
	return func(event protocol.Event) (protocol.Event, error) {
		if event.Type == failType {
			return event, fmt.Errorf("append failed for %s", event.Type)
		}
		seq++
		stored := protocol.EnrichEvent(event, protocol.EventEnvelope{
			Seq:    seq,
			Source: protocol.EventSourceGateway,
			RunID:  "run-1",
			TS:     time.Unix(1000+seq, 0).UTC().Format(time.RFC3339Nano),
		})
		stored.Seq = seq
		return stored, nil
	}
}

func runGatewaySessionForTest(t *testing.T, server *Server, sessionID, body string) []protocol.Event {
	t.Helper()
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/run", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", rec.Code, rec.Body.String())
	}
	return readProtocolEvents(t, rec.Body)
}

func runGatewaySessionAgentForTest(t *testing.T, server *Server, sessionID string, a *agentpkg.Agent, prompt string) []protocol.Event {
	t.Helper()
	session, ok := server.session(sessionID)
	if !ok {
		t.Fatal("created session missing")
	}
	var events []protocol.Event
	var persistErr error
	err := session.Thread.Run(context.Background(), RunnerFunc(a.RunMessages), prompt, func(event protocol.Event) {
		if persistErr != nil {
			return
		}
		if event.Type == protocol.EventRunStarted {
			if err := session.beginRunStatus(RunRequest{Provider: "mock", Model: "mock"}); err != nil {
				persistErr = err
				return
			}
		}
		observed, ok, err := session.observeRunEvent(event)
		if err != nil {
			persistErr = err
			return
		}
		if ok {
			events = append(events, observed)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if persistErr != nil {
		t.Fatal(persistErr)
	}
	if err := server.saveSession(session); err != nil {
		t.Fatal(err)
	}
	return events
}

func postGatewayJSON[T any](t *testing.T, server *Server, path, body string, wantStatus int) T {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("POST %s status = %d want %d body=%s", path, rec.Code, wantStatus, rec.Body.String())
	}
	var out T
	if strings.TrimSpace(rec.Body.String()) != "" {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

func sawEvent(events []protocol.Event, typ protocol.EventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func storedInspectionHasTurnChangeStatus(inspection StoredSessionInspection, changeID, status string) bool {
	for _, change := range inspection.Events.TurnChanges {
		if change.ChangeID == changeID && change.Status == status && len(change.Files) > 0 && change.FileCount > 0 {
			return true
		}
	}
	return false
}

func gatewayCheckpointSessionForTest(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	cfg.MaxToolRounds = 2
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{
		{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_write", ToolName: "fs_write_file", ArgsDelta: `{"path":"out.txt","content":"agent\n"}`},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventContent, Text: "done"},
			{Kind: provider.EventDone},
		},
	}}
	registry := tools.NewRegistry(cfg)
	server := NewServerWithOptions(cfg, provider.Mock{}, registry, ServerOptions{SessionStoreDir: storeDir})
	sessionID := createGatewaySessionForTest(t, server)
	runGatewaySessionAgentForTest(t, server, sessionID, agentpkg.New(cfg, prov, registry), "write")
	path := filepath.Join(root, "out.txt")
	if got := readFileString(t, path); got != "agent\n" {
		t.Fatalf("written file = %q", got)
	}
	return server, storeDir, sessionID, path
}

func failSessionEventOnce(t *testing.T, server *Server, session *Session, failType protocol.EventType) {
	t.Helper()
	var failed bool
	session.eventRecorder = func(event protocol.Event) (protocol.Event, error) {
		if event.Type == failType && !failed {
			failed = true
			return event, fmt.Errorf("injected append failure for %s", event.Type)
		}
		return server.store.AppendEvent(session, event)
	}
}

func forceLegacySnapshotWriteFailure(t *testing.T, storeDir, sessionID string) {
	t.Helper()
	path := filepath.Join(storeDir, sessionID+".json")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
