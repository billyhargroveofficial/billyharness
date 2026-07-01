package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	clientprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

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
	inputsPath := filepath.Join(sessionDir, sessionInputsJSONLName)
	assertPerm(t, storeDir, 0o700)
	assertPerm(t, sessionDir, 0o700)
	assertPerm(t, manifestPath, 0o600)
	assertPerm(t, historyPath, 0o600)
	assertPerm(t, eventsPath, 0o600)
	assertPerm(t, inputsPath, 0o600)
	assertPerm(t, filepath.Join(storeDir, created.ID+".json"), 0o600)
	assertPerm(t, filepath.Join(sessionDir, sessionConfigSnapshotName), 0o600)
	assertPerm(t, filepath.Join(sessionDir, sessionModelSnapshotName), 0o600)
	assertPerm(t, filepath.Join(sessionDir, sessionMCPSnapshotName), 0o600)

	var manifest sessionManifest
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SessionID != created.ID || manifest.HistoryJSONL != sessionHistoryJSONLName || manifest.EventsJSONL != sessionEventsJSONLName || manifest.InputsJSONL != sessionInputsJSONLName {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.ConfigSnapshotJSON != sessionConfigSnapshotName ||
		manifest.ModelProviderSnapshotJSON != sessionModelSnapshotName ||
		manifest.MCPSnapshotJSON != sessionMCPSnapshotName {
		t.Fatalf("manifest missing snapshots: %#v", manifest)
	}
	if manifest.HistorySeq < 2 || manifest.EventSeq == 0 || manifest.MessageCount < 3 || manifest.HistorySHA256 == "" {
		t.Fatalf("manifest missing replay metadata: %#v", manifest)
	}
	for _, file := range []string{sessionConfigSnapshotName, sessionModelSnapshotName, sessionMCPSnapshotName} {
		body, err := os.ReadFile(filepath.Join(sessionDir, file))
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(body) {
			t.Fatalf("%s is not JSON: %s", file, body)
		}
		if strings.Contains(string(body), "sk-") {
			t.Fatalf("%s leaked token-like content: %s", file, body)
		}
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
	inspection, err := InspectStoredSession(storeDir, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.OfflineReplayReady ||
		inspection.MessageCount != manifest.MessageCount ||
		inspection.Manifest.SchemaVersion != gatewaySessionSchemaVersion ||
		inspection.Events.EventTypes[string(protocol.EventRunCompleted)] == 0 ||
		!hasExistingFile(inspection.Files, "config_snapshot") ||
		!hasExistingFile(inspection.Files, "model_provider_snapshot") ||
		!hasExistingFile(inspection.Files, "mcp_snapshot") {
		t.Fatalf("inspection = %#v", inspection)
	}
	listed, err := ListStoredSessions(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 ||
		listed.Sessions[0].ID != created.ID ||
		!listed.Sessions[0].OfflineReplayReady {
		t.Fatalf("listed sessions = %#v warnings=%#v", listed.Sessions, listed.Warnings)
	}
	index, err := RebuildStoredSessionIndex(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if index.SessionCount != len(listed.Sessions) || index.Sessions[0].ID != listed.Sessions[0].ID {
		t.Fatalf("index = %#v listed = %#v", index, listed)
	}
	indexFile := filepath.Join(storeDir, sessionIndexDirName, sessionIndexFileName)
	assertPerm(t, filepath.Dir(indexFile), 0o700)
	assertPerm(t, indexFile, 0o600)
	if err := DeleteStoredSessionIndex(storeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index file after delete err = %v", err)
	}
	if err := os.WriteFile(indexFile, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	listedAfterCorruption, err := ListStoredSessions(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAfterCorruption.Sessions) != len(listed.Sessions) || listedAfterCorruption.Sessions[0].ID != created.ID {
		t.Fatalf("canonical list after corrupt index = %#v", listedAfterCorruption)
	}
	rebuilt, err := RebuildStoredSessionIndex(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	readBack, err := ReadStoredSessionIndex(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.SessionCount != readBack.SessionCount || readBack.Sessions[0].ID != created.ID {
		t.Fatalf("rebuilt=%#v readBack=%#v", rebuilt, readBack)
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
	statusAfterRestart := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(statusAfterRestart, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/status", nil))
	if statusAfterRestart.Code != http.StatusOK {
		t.Fatalf("status after restart = %d body=%s", statusAfterRestart.Code, statusAfterRestart.Body.String())
	}
	var restoredStatus SessionStatus
	if err := json.Unmarshal(statusAfterRestart.Body.Bytes(), &restoredStatus); err != nil {
		t.Fatal(err)
	}
	if restoredStatus.RunSeq != 1 || restoredStatus.Running {
		t.Fatalf("restored status = %#v, want run_seq 1 and not running", restoredStatus)
	}
	secondRun := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(secondRun, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"persist me again"}`)))
	if secondRun.Code != http.StatusOK {
		t.Fatalf("second run status = %d body=%s", secondRun.Code, secondRun.Body.String())
	}
	eventsAfterRestart := readSessionEventRecords(t, eventsPath)
	var maxRunSeq int64
	var sawRun2Start bool
	var sawRun2Status bool
	for _, record := range eventsAfterRestart {
		if record.RunSeq > maxRunSeq {
			maxRunSeq = record.RunSeq
		}
		if record.RunSeq == 2 && record.Event.Type == protocol.EventRunStarted {
			sawRun2Start = true
		}
		if record.RunSeq == 2 && record.Event.Type == protocol.EventSessionStatus {
			sawRun2Status = true
			if !strings.HasSuffix(record.Event.RunID, ":run-2") {
				t.Fatalf("run-2 status event has run_id %q", record.Event.RunID)
			}
		}
	}
	if maxRunSeq != 2 || !sawRun2Start || !sawRun2Status {
		t.Fatalf("events after restart should continue at run_seq 2, max=%d sawStart=%v sawStatus=%v records=%#v", maxRunSeq, sawRun2Start, sawRun2Status, eventsAfterRestart)
	}
}

func TestSessionStoreRedoStateClearsOnNewTurnChange(t *testing.T) {
	store := newSessionStore(t.TempDir())
	session := newGatewaySession("redo-state", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	changeA := protocol.TurnChangeEvent{ChangeID: "change-a", Status: "recorded", FileCount: 1, Modified: 1, Reversible: true}
	changeB := protocol.TurnChangeEvent{ChangeID: "change-b", Status: "recorded", FileCount: 1, Added: 1, Reversible: true}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventTurnChangeRecorded, Data: changeA}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventTurnChangeReverted, Data: changeA}); err != nil {
		t.Fatal(err)
	}
	redo, ok, err := store.FindRedoTurnChange(session.ID)
	if err != nil || !ok || redo.Data.ChangeID != "change-a" {
		t.Fatalf("redo after revert = %#v ok=%v err=%v", redo, ok, err)
	}
	if undo, ok, err := store.FindUndoableTurnChange(session.ID, "change-a"); err != nil || ok || undo.Data.ChangeID != "" {
		t.Fatalf("reverted change should not be undoable: undo=%#v ok=%v err=%v", undo, ok, err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventTurnChangeRecorded, Data: changeB}); err != nil {
		t.Fatal(err)
	}
	if redo, ok, err := store.FindRedoTurnChange(session.ID); err != nil || ok || redo.Data.ChangeID != "" {
		t.Fatalf("redo after new change = %#v ok=%v err=%v", redo, ok, err)
	}
	undo, ok, err := store.FindUndoableTurnChange(session.ID, "")
	if err != nil || !ok || undo.Data.ChangeID != "change-b" {
		t.Fatalf("latest undoable = %#v ok=%v err=%v", undo, ok, err)
	}
}

func TestGatewaySessionProjectContextEpochReusedAfterRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.ProjectContextMaxBytes = 2048
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID       string             `json:"id"`
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if contexts := projectContextMessages(created.Messages); len(contexts) != 0 {
		t.Fatalf("create response should omit messages by default: %#v", contexts)
	}
	getCreated := httptest.NewRecorder()
	server.Handler().ServeHTTP(getCreated, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))
	if getCreated.Code != http.StatusOK {
		t.Fatalf("get created status = %d body=%s", getCreated.Code, getCreated.Body.String())
	}
	var createdSession struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(getCreated.Body.Bytes(), &createdSession); err != nil {
		t.Fatal(err)
	}
	if contexts := projectContextMessages(createdSession.Messages); len(contexts) != 1 || !strings.HasPrefix(contexts[0].Content, "# Project context") {
		t.Fatalf("created project contexts = %#v", contexts)
	}

	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("NEW_FLAG=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstRun := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstRun, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"notice context"}`)))
	if firstRun.Code != http.StatusOK {
		t.Fatalf("first run status = %d body=%s", firstRun.Code, firstRun.Body.String())
	}
	restarted := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	secondRun := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(secondRun, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewBufferString(`{"prompt":"reuse context"}`)))
	if secondRun.Code != http.StatusOK {
		t.Fatalf("second run status = %d body=%s", secondRun.Code, secondRun.Body.String())
	}
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
	contexts := projectContextMessages(got.Messages)
	if len(contexts) != 1 || !strings.HasPrefix(contexts[0].Content, "# Project context updated") || !strings.Contains(contexts[0].Content, "NEW_FLAG") {
		t.Fatalf("stored project contexts after restart = %#v", contexts)
	}
}

func TestStoredSessionDiagnosticsIndexUsageCumulativeMatchesProjector(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	store := newSessionStore(storeDir)
	sessionID := "session-diagnostics"
	session := newGatewaySessionWithOwner(sessionID, time.Now().UTC(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "profile/system noise"},
		{Role: protocol.RoleUser, Content: "please inspect the failing command"},
		{Role: protocol.RoleAssistant, Content: "I found one failing shell command."},
		{Role: protocol.RoleTool, Content: "internal tool payload"},
	}, gatewayapi.SessionOwner{ClientType: "test"})
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}

	runID := "run-diagnostics"
	turnID := "turn-001"
	modelStepID := "turn-001:model-call-001"
	toolBatchID := "turn-001:tool-batch-001"
	callID := "call-shell"
	attemptID := "turn-001:tool-call-001:attempt-001"
	outputRef := filepath.Join(storeDir, "tool-output", "missing.txt")
	events := []protocol.Event{
		{Type: protocol.EventRunStarted, RunID: runID},
		{Type: protocol.EventTurnStarted, RunID: runID, Data: protocol.TurnEvent{TurnID: turnID, Round: 1, Status: protocol.TurnStatusStarted}},
		{Type: protocol.EventStepStarted, RunID: runID, Data: protocol.StepEvent{TurnID: turnID, StepID: modelStepID, Round: 1, Kind: protocol.StepKindModelCall, Status: protocol.StepStatusStarted}},
		{Type: protocol.EventModelCallStarted, RunID: runID, TurnID: turnID, StepID: modelStepID, Data: protocol.ModelCallEvent{RequestID: "request-1", Status: protocol.StepStatusStarted}},
		{Type: protocol.EventProviderUsageUpdate, RunID: runID, TurnID: turnID, StepID: modelStepID, Data: map[string]any{
			"turn_id":           turnID,
			"step_id":           modelStepID,
			"input_tokens":      10,
			"output_tokens":     1,
			"cache_hit_tokens":  2,
			"cache_miss_tokens": 8,
			"reasoning_tokens":  1,
		}},
		{Type: protocol.EventProviderUsageUpdate, RunID: runID, TurnID: turnID, StepID: modelStepID, Data: map[string]any{
			"turn_id":           turnID,
			"step_id":           modelStepID,
			"input_tokens":      15,
			"output_tokens":     3,
			"cache_hit_tokens":  2,
			"cache_miss_tokens": 13,
			"reasoning_tokens":  2,
		}},
		{Type: protocol.EventModelCallFinished, RunID: runID, TurnID: turnID, StepID: modelStepID, Data: protocol.ModelCallEvent{RequestID: "request-1", Status: protocol.StepStatusCompleted}},
		{Type: protocol.EventStepCompleted, RunID: runID, Data: protocol.StepEvent{TurnID: turnID, StepID: modelStepID, Round: 1, Kind: protocol.StepKindModelCall, Status: protocol.StepStatusCompleted}},
		{Type: protocol.EventStepStarted, RunID: runID, Data: protocol.StepEvent{TurnID: turnID, StepID: toolBatchID, Round: 1, Kind: protocol.StepKindToolBatch, Status: protocol.StepStatusStarted, BatchSize: 1}},
		{Type: protocol.EventToolCallRequested, RunID: runID, Data: protocol.ToolCall{ID: callID, Name: "shell_exec", Arguments: json.RawMessage(`{"cmd":"go test ./..."}`)}},
		{Type: protocol.EventToolCallStarted, RunID: runID, CallID: callID, AttemptID: attemptID, Data: "shell_exec"},
		{Type: protocol.EventToolOutputRefCreated, RunID: runID, Data: protocol.ToolOutputRefEvent{
			CallID:         callID,
			Name:           "shell_exec",
			AttemptID:      attemptID,
			OutputRef:      outputRef,
			OutputRefID:    "output-ref-1",
			OutputRefBytes: 123,
			Truncated:      true,
		}},
		{Type: protocol.EventToolCallFinished, RunID: runID, CallID: callID, AttemptID: attemptID, Data: protocol.ToolResult{
			CallID:    callID,
			Name:      "shell_exec",
			Content:   "exit status 1",
			IsError:   true,
			ErrorCode: "exit_status",
			OutputRef: outputRef,
			Metadata: map[string]any{
				"attempt_id":    attemptID,
				"output_ref_id": "output-ref-1",
			},
		}},
		{Type: protocol.EventStepCompleted, RunID: runID, Data: protocol.StepEvent{TurnID: turnID, StepID: toolBatchID, Round: 1, Kind: protocol.StepKindToolBatch, Status: protocol.StepStatusCompleted, BatchSize: 1}},
		{Type: protocol.EventTurnCompleted, RunID: runID, Data: protocol.TurnEvent{TurnID: turnID, Round: 1, Status: protocol.TurnStatusCompleted, StopReason: protocol.TurnStopFinalAnswer}},
		{Type: protocol.EventRunCompleted, RunID: runID},
	}
	var storedEvents []protocol.Event
	for _, event := range events {
		stored, err := store.AppendEvent(session, event)
		if err != nil {
			t.Fatal(err)
		}
		storedEvents = append(storedEvents, stored)
	}

	index, err := RebuildStoredSessionDiagnosticsIndex(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if index.SessionCount != 1 ||
		index.TextRowCount != 2 ||
		index.ToolRowCount == 0 ||
		index.ErrorRowCount != 1 ||
		index.RunRowCount != 1 ||
		index.UsageRowCount != 1 {
		t.Fatalf("diagnostics index counts = %#v", index)
	}
	if index.TextRows[0].Role != string(protocol.RoleUser) || index.TextRows[1].Role != string(protocol.RoleAssistant) {
		t.Fatalf("text rows should include only visible user/assistant content: %#v", index.TextRows)
	}
	if !containsToolRow(index.ToolRows, callID, "requested", "shell_exec") ||
		!containsToolRow(index.ToolRows, callID, "output_ref_created", "shell_exec") ||
		!containsToolRow(index.ToolRows, callID, "failed", "shell_exec") {
		t.Fatalf("tool rows = %#v", index.ToolRows)
	}
	var requestedArgs string
	for _, row := range index.ToolRows {
		if row.CallID == callID && row.Status == "requested" {
			requestedArgs = row.ArgsPreview
		}
	}
	if !strings.Contains(requestedArgs, "go test") {
		t.Fatalf("requested args preview = %q", requestedArgs)
	}
	if index.ErrorRows[0].CallID != callID || !strings.Contains(index.ErrorRows[0].Error, "exit_status") {
		t.Fatalf("error rows = %#v", index.ErrorRows)
	}
	if index.RunRows[0].RunID != runID || index.RunRows[0].Status != "completed" || index.RunRows[0].StartSeq == 0 || index.RunRows[0].EndSeq == 0 {
		t.Fatalf("run rows = %#v", index.RunRows)
	}
	usage := index.UsageRows[0]
	if usage.RunID != runID ||
		usage.InputTokens != 15 ||
		usage.OutputTokens != 3 ||
		usage.CacheHitTokens != 2 ||
		usage.CacheMissTokens != 13 ||
		usage.ReasoningTokens != 2 ||
		usage.ModelCalls != 1 ||
		usage.ToolCalls != 1 {
		t.Fatalf("usage row = %#v", usage)
	}
	projector := clientprojector.New()
	var snapshot clientprojector.Snapshot
	for _, event := range storedEvents {
		snapshot = projector.Apply(event)
	}
	if usage.InputTokens != snapshot.InputTokens ||
		usage.OutputTokens != snapshot.OutputTokens ||
		usage.CacheHitTokens != snapshot.CacheHitTokens ||
		usage.CacheMissTokens != snapshot.CacheMissTokens ||
		usage.ReasoningTokens != snapshot.ReasoningTokens {
		t.Fatalf("usage row %#v does not match projector snapshot %#v", usage, snapshot)
	}
	readBack, err := ReadStoredSessionDiagnosticsIndex(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if readBack.UsageRowCount != 1 || readBack.UsageRows[0].InputTokens != 15 {
		t.Fatalf("read diagnostics index = %#v", readBack)
	}

	if err := os.WriteFile(diagnosticsIndexPath(storeDir), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStoredSessionDiagnosticsIndex(storeDir); err == nil {
		t.Fatal("expected corrupt diagnostics index read to fail")
	}
	listed, err := ListStoredSessions(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != sessionID {
		t.Fatalf("canonical list after corrupt diagnostics index = %#v", listed)
	}
	inspection, err := InspectStoredSession(storeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SessionID != sessionID || inspection.Events.Records == 0 {
		t.Fatalf("inspection after corrupt diagnostics index = %#v", inspection)
	}
	if err := DeleteStoredSessionIndex(storeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(diagnosticsIndexPath(storeDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("diagnostics index after delete err = %v", err)
	}
}

func projectContextMessages(messages []protocol.Message) []protocol.Message {
	var out []protocol.Message
	for _, msg := range messages {
		if strings.Contains(msg.Content, "<PROJECT_CONTEXT>") {
			out = append(out, msg)
		}
	}
	return out
}

func TestGatewaySessionInputAdmissionDurableAndIdempotent(t *testing.T) {
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

	admitBody := `{"input_id":"input-1","prompt":"hello","client_id":"test-client"}`
	admit := httptest.NewRecorder()
	server.Handler().ServeHTTP(admit, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", strings.NewReader(admitBody)))
	if admit.Code != http.StatusCreated {
		t.Fatalf("admit status = %d body=%s", admit.Code, admit.Body.String())
	}
	var admitted gatewayapi.SessionInputResponse
	if err := json.Unmarshal(admit.Body.Bytes(), &admitted); err != nil {
		t.Fatal(err)
	}
	if admitted.InputID != "input-1" || admitted.State != sessionInputAdmitted || admitted.Duplicate {
		t.Fatalf("admitted = %#v", admitted)
	}
	inputsPath := filepath.Join(storeDir, created.ID, sessionInputsJSONLName)
	assertPerm(t, inputsPath, 0o600)
	records := readSessionInputRecords(t, inputsPath)
	if len(records) != 1 || records[0].Kind != sessionInputAdmitted || records[0].Prompt != "hello" || records[0].BodySHA256 == "" {
		t.Fatalf("input records = %#v", records)
	}

	duplicate := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", strings.NewReader(admitBody)))
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
	var dupResp gatewayapi.SessionInputResponse
	if err := json.Unmarshal(duplicate.Body.Bytes(), &dupResp); err != nil {
		t.Fatal(err)
	}
	if !dupResp.Duplicate || dupResp.State != sessionInputAdmitted || dupResp.Seq != 1 {
		t.Fatalf("duplicate response = %#v", dupResp)
	}

	restarted := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	duplicateAfterRestart := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(duplicateAfterRestart, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", strings.NewReader(admitBody)))
	if duplicateAfterRestart.Code != http.StatusOK {
		t.Fatalf("duplicate after restart status = %d body=%s", duplicateAfterRestart.Code, duplicateAfterRestart.Body.String())
	}
	var restartResp gatewayapi.SessionInputResponse
	if err := json.Unmarshal(duplicateAfterRestart.Body.Bytes(), &restartResp); err != nil {
		t.Fatal(err)
	}
	if !restartResp.Duplicate || restartResp.State != sessionInputAdmitted {
		t.Fatalf("restart duplicate response = %#v", restartResp)
	}

	conflict := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", strings.NewReader(`{"input_id":"input-1","prompt":"different"}`)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestGatewaySessionRunRecordsInputPromotionAndCompletion(t *testing.T) {
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
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(`{"input_id":"run-input-1","prompt":"record input"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	inputsPath := filepath.Join(storeDir, created.ID, sessionInputsJSONLName)
	records := readSessionInputRecords(t, inputsPath)
	if len(records) != 3 {
		t.Fatalf("input record count = %d records=%#v", len(records), records)
	}
	if records[0].Kind != sessionInputAdmitted || records[1].Kind != sessionInputPromoted || records[2].Kind != sessionInputCompleted {
		t.Fatalf("input record kinds = %#v", records)
	}
	if records[1].RunSeq != 1 || records[2].RunSeq != 1 || records[2].TerminalStatus != "completed" {
		t.Fatalf("input promotion/completion = %#v", records)
	}
}

func TestGatewaySessionInputsMarkPromotedIncompleteAmbiguousOnRestart(t *testing.T) {
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
	session, ok := server.session(created.ID)
	if !ok {
		t.Fatal("created session missing from server")
	}
	admitted, err := server.store.AdmitInput(session, gatewayapi.SessionInputRequest{InputID: "ambiguous-input", Prompt: "maybe"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.PromoteInput(session, admitted.InputID, 1); err != nil {
		t.Fatal(err)
	}
	inputsPath := filepath.Join(storeDir, created.ID, sessionInputsJSONLName)
	before, err := replaySessionInputs(inputsPath, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state := before.inputs["ambiguous-input"]; state.State != sessionInputPromoted {
		t.Fatalf("state before restart = %#v", state)
	}

	_ = NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	after, err := replaySessionInputs(inputsPath, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := after.inputs["ambiguous-input"]
	if state.State != sessionInputAmbiguous || state.TerminalStatus != "ambiguous_after_restart" {
		t.Fatalf("state after restart = %#v", state)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/sessions/"+created.ID+"/events?after_seq="+strconv.FormatInt(afterSeq, 10)+"&follow=false", nil)
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
	var extra protocol.Event
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("one-shot replay decode after stored events = %v event=%#v, want EOF", err, extra)
	}
}

func TestGatewaySessionEventsReplayRejectsLifecycleViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), sessionEventsJSONLName)
	records := []sessionEventRecord{
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           1,
			SessionID:     "session-1",
			EventType:     string(protocol.EventRunStarted),
			Event: protocol.Event{
				Type:  protocol.EventRunStarted,
				RunID: "run-1",
			},
		},
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           2,
			SessionID:     "session-1",
			EventType:     string(protocol.EventToolCallFinished),
			Event: protocol.Event{
				Type:      protocol.EventToolCallFinished,
				RunID:     "run-1",
				CallID:    "call-1",
				AttemptID: "attempt-1",
				Data: protocol.ToolResult{
					CallID:  "call-1",
					Content: "ok",
					Metadata: map[string]any{
						"attempt_id": "attempt-1",
					},
				},
			},
		},
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := replaySessionEventsAfter(path, "session-1", 0)
	if err == nil || !strings.Contains(err.Error(), "matching call_id") {
		t.Fatalf("expected lifecycle call_id error, got %v", err)
	}
	var corrupt *eventlog.CorruptionError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error %T does not expose CorruptionError", err)
	}
	if corrupt.Path != path || corrupt.Line != 2 || corrupt.RecordNo != 2 || corrupt.Kind != "lifecycle" {
		t.Fatalf("corruption error = %#v", corrupt)
	}
}

func TestGatewaySessionEventsReplayRejectsDuplicateTerminalRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), sessionEventsJSONLName)
	records := []sessionEventRecord{
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           1,
			SessionID:     "session-1",
			EventType:     string(protocol.EventRunStarted),
			Event:         protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"},
		},
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           2,
			SessionID:     "session-1",
			EventType:     string(protocol.EventRunCompleted),
			Event:         protocol.Event{Type: protocol.EventRunCompleted, RunID: "run-1"},
		},
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           3,
			SessionID:     "session-1",
			EventType:     string(protocol.EventRunFailed),
			Event:         protocol.Event{Type: protocol.EventRunFailed, RunID: "run-1", Data: "late failure"},
		},
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := replaySessionEventsAfter(path, "session-1", 0)
	if err == nil || !strings.Contains(err.Error(), "duplicate terminal run event") {
		t.Fatalf("expected duplicate terminal error, got %v", err)
	}
	var corrupt *eventlog.CorruptionError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error %T does not expose CorruptionError", err)
	}
	if corrupt.Path != path || corrupt.Line != 3 || corrupt.RecordNo != 3 || corrupt.Kind != "lifecycle" {
		t.Fatalf("corruption error = %#v", corrupt)
	}
}

func readSessionInputRecords(t *testing.T, path string) []sessionInputRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []sessionInputRecord
	dec := json.NewDecoder(file)
	for {
		var record sessionInputRecord
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

func TestInspectStoredSessionReturnsStructuredEventCorruption(t *testing.T) {
	root := t.TempDir()
	sessionID := "session-1"
	sessionDir := filepath.Join(root, sessionID)
	now := time.Unix(10, 0).UTC()
	if err := writeSessionManifest(filepath.Join(sessionDir, sessionManifestName), sessionManifest{
		SchemaVersion: gatewaySessionSchemaVersion,
		SessionID:     sessionID,
		CreatedAt:     now,
		UpdatedAt:     now,
		HistoryJSONL:  sessionHistoryJSONLName,
		EventsJSONL:   sessionEventsJSONLName,
		SnapshotJSON:  sessionID + ".json",
		MessageCount:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := eventlog.AppendJSONL(filepath.Join(sessionDir, sessionHistoryJSONLName), sessionHistoryRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           1,
		SessionID:     sessionID,
		Timestamp:     now,
		Kind:          sessionHistoryCreated,
		CreatedAt:     now,
		UpdatedAt:     now,
		MessageCount:  1,
		Messages:      []protocol.Message{{Role: protocol.RoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(sessionDir, sessionEventsJSONLName)
	for _, record := range []sessionEventRecord{
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           1,
			SessionID:     sessionID,
			EventType:     string(protocol.EventRunStarted),
			Event:         protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"},
		},
		{
			SchemaVersion: gatewaySessionSchemaVersion,
			Seq:           2,
			SessionID:     sessionID,
			EventType:     string(protocol.EventToolCallFinished),
			Event: protocol.Event{
				Type:      protocol.EventToolCallFinished,
				RunID:     "run-1",
				CallID:    "call-1",
				AttemptID: "attempt-1",
			},
		},
	} {
		if err := eventlog.AppendJSONL(eventsPath, record); err != nil {
			t.Fatal(err)
		}
	}

	_, err := InspectStoredSession(root, sessionID)
	if err == nil || !strings.Contains(err.Error(), "matching call_id") {
		t.Fatalf("expected lifecycle call_id error, got %v", err)
	}
	var corrupt *eventlog.CorruptionError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error %T does not expose CorruptionError", err)
	}
	if corrupt.Path != eventsPath || corrupt.Line != 2 || corrupt.RecordNo != 2 || corrupt.Kind != "lifecycle" {
		t.Fatalf("corruption error = %#v", corrupt)
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

	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/events?follow=maybe", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "follow") {
		t.Fatalf("follow response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGatewaySessionInspectorVerifiesOutputRefs(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	session := newGatewaySession("with-output-ref", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	server.attachSessionStore(session)
	server.sessions[session.ID] = session
	if err := server.saveSession(session); err != nil {
		t.Fatal(err)
	}

	refDir := filepath.Join(t.TempDir(), "tool-output")
	if err := os.MkdirAll(refDir, 0o700); err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(refDir, "large-output.txt")
	body := []byte("large output payload")
	if err := os.WriteFile(refPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	session.publish(protocol.Event{Type: protocol.EventToolOutputRefCreated, Data: map[string]any{
		"call_id":                "call-1",
		"name":                   "big_output",
		"attempt_id":             "turn-001:tool-call-001:attempt-001",
		"output_ref":             refPath,
		"output_ref_id":          filepath.Base(refPath),
		"output_ref_bytes":       int64(len(body)),
		"output_ref_sha256":      hex.EncodeToString(sum[:]),
		"output_ref_permissions": "0600",
		"output_ref_plaintext":   true,
		"truncated":              true,
	}})

	inspection, err := InspectStoredSession(storeDir, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Events.OutputRefs != 1 ||
		!inspection.Events.OutputRefsVerified ||
		inspection.Events.MissingOutputRefs != 0 ||
		inspection.Events.OutputRefHashMismatch != 0 ||
		inspection.Events.OutputRefBytes != int64(len(body)) ||
		len(inspection.Events.OutputRefWarnings) != 0 {
		t.Fatalf("inspection events = %#v", inspection.Events)
	}

	if err := os.Remove(refPath); err != nil {
		t.Fatal(err)
	}
	missingInspection, err := InspectStoredSession(storeDir, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if missingInspection.Events.MissingOutputRefs != 1 ||
		missingInspection.Events.OutputRefsVerified ||
		len(missingInspection.Events.OutputRefWarnings) != 1 ||
		missingInspection.Events.OutputRefWarnings[0].Reason != "missing" ||
		!strings.Contains(strings.Join(missingInspection.Warnings, "\n"), "output_ref") {
		t.Fatalf("missing ref inspection = %#v warnings=%#v", missingInspection.Events, missingInspection.Warnings)
	}

	if err := os.WriteFile(refPath, []byte("corrupt payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	mismatchInspection, err := InspectStoredSession(storeDir, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mismatchInspection.Events.OutputRefHashMismatch != 1 ||
		mismatchInspection.Events.OutputRefsVerified ||
		len(mismatchInspection.Events.OutputRefWarnings) != 1 ||
		mismatchInspection.Events.OutputRefWarnings[0].Reason != "size_mismatch" {
		t.Fatalf("mismatched ref inspection = %#v", mismatchInspection.Events)
	}
}

func TestStoredSessionResumeKeepsLargeOutputRefPreviewAndWarnsMissingArtifact(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	refDir := filepath.Join(t.TempDir(), "tool-output")
	if err := os.MkdirAll(refDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fullOutput := strings.Repeat("fs-output-", 56_000)
	if len(fullOutput) < 500_000 {
		t.Fatalf("test fixture must exercise at least 500k chars, got %d", len(fullOutput))
	}
	refPath := filepath.Join(refDir, "fs-read-large.txt")
	if err := os.WriteFile(refPath, []byte(fullOutput), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(fullOutput))
	preview := fullOutput[:512] + "\n...[truncated; full tool output saved as plaintext to " + refPath + "]"
	session := newGatewaySession("resume-output-ref", time.Now().UTC(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "read large file"},
		{Role: protocol.RoleAssistant, Content: "", ToolCalls: []protocol.ToolCall{{ID: "call-fs", Name: "fs_read_file", Arguments: json.RawMessage(`{"path":"large.txt"}`)}}},
		{Role: protocol.RoleTool, ToolCallID: "call-fs", Name: "fs_read_file", Content: preview},
	})
	store := newSessionStore(storeDir)
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventToolOutputRefCreated, Data: protocol.ToolOutputRefEvent{
		CallID:               "call-fs",
		Name:                 "fs_read_file",
		AttemptID:            "turn-001:tool-call-001:attempt-001",
		OutputRef:            refPath,
		OutputRefID:          filepath.Base(refPath),
		OutputRefBytes:       int64(len(fullOutput)),
		OutputRefSHA256:      hex.EncodeToString(sum[:]),
		OutputRefPermissions: "0600",
		OutputRefPlaintext:   true,
		Truncated:            true,
	}}); err != nil {
		t.Fatal(err)
	}

	loaded, err := newSessionStore(storeDir).LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded sessions = %d", len(loaded))
	}
	messages := loaded[0].messages()
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	toolContent := messages[3].Content
	if !strings.Contains(toolContent, refPath) || strings.Contains(toolContent, fullOutput) || len(toolContent) >= len(fullOutput) {
		t.Fatalf("resumed tool content should be bounded preview with ref, len=%d ref=%q", len(toolContent), refPath)
	}

	if err := os.Remove(refPath); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectStoredSession(storeDir, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Events.MissingOutputRefs != 1 ||
		len(inspection.Events.OutputRefWarnings) != 1 ||
		inspection.Events.OutputRefWarnings[0].CallID != "call-fs" ||
		inspection.Events.OutputRefWarnings[0].Reason != "missing" {
		t.Fatalf("inspection events = %#v", inspection.Events)
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
	inspection, err := InspectStoredSession(storeDir, "legacy-session")
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Legacy || !inspection.OfflineReplayReady || inspection.MessageCount != 2 {
		t.Fatalf("legacy inspection = %#v", inspection)
	}
}

func containsToolRow(rows []StoredSessionToolRow, callID, status, name string) bool {
	for _, row := range rows {
		if row.CallID == callID && row.Status == status && row.Name == name {
			return true
		}
	}
	return false
}
