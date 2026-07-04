package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
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
	if !inspection.MessageSnapshotReady ||
		!inspection.EventReplayReady ||
		!inspection.OfflineReplayReady ||
		inspection.MessageCount != manifest.MessageCount ||
		inspection.Manifest.SchemaVersion != gatewaySessionSchemaVersion ||
		inspection.Events.EventTypes[string(protocol.EventRunCompleted)] == 0 ||
		!hasExistingFile(inspection.Files, "config_snapshot") ||
		!hasExistingFile(inspection.Files, "model_provider_snapshot") ||
		!hasExistingFile(inspection.Files, "mcp_snapshot") {
		t.Fatalf("inspection = %#v", inspection)
	}
	if !storedSessionHasReadiness(inspection.Readiness, storedSessionReadinessMessageSnapshotReady) ||
		!storedSessionHasReadiness(inspection.Readiness, storedSessionReadinessEventReplayReady) {
		t.Fatalf("inspection readiness = %#v", inspection.Readiness)
	}
	listed, err := ListStoredSessions(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 ||
		listed.Sessions[0].ID != created.ID ||
		!listed.Sessions[0].MessageSnapshotReady ||
		!listed.Sessions[0].EventReplayReady ||
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

func TestStoredSessionContextReconstructsCompactionEpochs(t *testing.T) {
	cfg := config.Default()
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	store := newSessionStore(storeDir)
	session := newGatewaySession("compaction-epochs", time.Now().UTC(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
	})
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	runID := gatewaySessionRunID(session.ID, 1)
	for _, event := range []protocol.Event{
		{Type: protocol.EventRunStarted, RunID: runID, Data: map[string]any{"run_id": runID, "status": "started"}},
		{Type: protocol.EventContextCompacted, RunID: runID, Data: map[string]any{
			"compaction_id":           "compact-1",
			"context_epoch":           1,
			"before_estimated_tokens": 1000,
			"after_estimated_tokens":  600,
			"post_history_hash":       "post-1",
		}},
		{Type: protocol.EventContextCompacted, RunID: runID, Data: map[string]any{
			"compaction_id":           "compact-2",
			"context_epoch":           2,
			"before_estimated_tokens": 900,
			"after_estimated_tokens":  500,
			"post_history_hash":       "post-2",
		}},
		{Type: protocol.EventRunCompleted, RunID: runID},
	} {
		if _, err := store.AppendEvent(session, event); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := StoredSessionContext(storeDir, session.ID, cfg.RuntimeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if resp.LastCompaction == nil ||
		resp.LastCompaction.CompactionID != "compact-2" ||
		resp.LastCompaction.ContextEpoch != 2 ||
		resp.LastCompaction.PostHistoryHash != "post-2" {
		t.Fatalf("last compaction = %#v warnings=%v", resp.LastCompaction, resp.Warnings)
	}
}

func TestSessionStoreAppendRejectsInvalidEventsBeforeDurableWrite(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	store := newSessionStore(storeDir)
	session := newGatewaySession("strict-append", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	runID := gatewaySessionRunID(session.ID, 1)
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventRunStarted, RunID: runID}); err != nil {
		t.Fatal(err)
	}

	_, err := store.AppendEvent(session, protocol.Event{
		Type:      protocol.EventToolCallFinished,
		RunID:     runID,
		CallID:    "call-missing",
		AttemptID: "attempt-missing",
		Data:      protocol.ToolResult{CallID: "call-missing", Content: "should not persist", Metadata: map[string]any{"attempt_id": "attempt-missing"}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid event lifecycle") || !strings.Contains(err.Error(), "matching call_id") {
		t.Fatalf("expected lifecycle rejection, got %v", err)
	}
	eventsPath := filepath.Join(storeDir, session.ID, sessionEventsJSONLName)
	records := readSessionEventRecords(t, eventsPath)
	if len(records) != 1 || records[0].Seq != 1 || records[0].Event.Type != protocol.EventRunStarted {
		t.Fatalf("invalid event was persisted: %#v", records)
	}

	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventRunCompleted, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	records = readSessionEventRecords(t, eventsPath)
	if len(records) != 2 || records[1].Seq != 2 || records[1].Event.Type != protocol.EventRunCompleted {
		t.Fatalf("valid append after rejection should stay gapless: %#v", records)
	}
}

func TestSessionStoreAppendRejectsMalformedEnvelopeBeforeDurableWrite(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	store := newSessionStore(storeDir)
	session := newGatewaySession("strict-envelope", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	_, err := store.AppendEvent(session, protocol.Event{
		SchemaVersion: protocol.EventSchemaVersion + 1,
		Type:          protocol.EventRunStarted,
		RunID:         gatewaySessionRunID(session.ID, 1),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported event schema_version") {
		t.Fatalf("expected malformed envelope rejection, got %v", err)
	}
	eventsPath := filepath.Join(storeDir, session.ID, sessionEventsJSONLName)
	if _, statErr := os.Stat(eventsPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("malformed envelope should not create event log, stat err=%v", statErr)
	}

	stored, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventRunStarted, RunID: gatewaySessionRunID(session.ID, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Seq != 1 {
		t.Fatalf("first valid append seq = %d, want 1", stored.Seq)
	}
}

func TestSessionStoreLoadAllSurfacesCorruptSessions(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	goodSession := newGatewaySession("good-session", time.Now().UTC(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
	})
	store := newSessionStore(storeDir)
	if err := store.Save(goodSession); err != nil {
		t.Fatal(err)
	}
	corruptID := "corrupt-session"
	writeCorruptStoredSessionHistory(t, storeDir, corruptID)

	loaded, diagnostics, err := newSessionStore(storeDir).LoadAllWithDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != goodSession.ID {
		t.Fatalf("loaded sessions = %#v", loaded)
	}
	if !diagnostics.Enabled || diagnostics.LoadedCount != 1 || diagnostics.ErrorCount != 1 || diagnostics.CorruptCount != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	got := diagnostics.Errors[0]
	if got.SessionID != corruptID || got.SessionIDHash != hashSessionIDForDiagnostics(corruptID) || !got.Corrupt {
		t.Fatalf("load error identity = %#v", got)
	}
	if got.Entry != corruptID || got.EntryType != "session_dir" || got.Line != 1 || got.RecordNo != 1 {
		t.Fatalf("load error metadata = %#v", got)
	}
	if strings.Contains(got.Error, storeDir) {
		t.Fatalf("load error leaked store path: %#v", got)
	}
}

func TestGatewayReadinessSurfacesStartupSessionStoreCorruption(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	goodSession := newGatewaySession("good-session", time.Now().UTC(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
	})
	store := newSessionStore(storeDir)
	if err := store.Save(goodSession); err != nil {
		t.Fatal(err)
	}
	corruptID := "corrupt-session"
	writeCorruptStoredSessionHistory(t, storeDir, corruptID)

	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	var listed SessionListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != goodSession.ID {
		t.Fatalf("listed sessions = %#v", listed.Sessions)
	}

	health := httptest.NewRecorder()
	server.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", health.Code, health.Body.String())
	}
	var got HealthResponse
	if err := json.Unmarshal(health.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(health.Body.String(), "session_store") {
		t.Fatalf("health leaked readiness details: %s", health.Body.String())
	}

	ready := httptest.NewRecorder()
	server.Handler().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d body=%s", ready.Code, ready.Body.String())
	}
	var readiness ReadinessResponse
	if err := json.Unmarshal(ready.Body.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.OK {
		t.Fatalf("readiness OK with corrupt startup store: %#v", readiness)
	}
	if readiness.SessionStore == nil || readiness.SessionStore.LoadedCount != 1 || readiness.SessionStore.ErrorCount != 1 || readiness.SessionStore.CorruptCount != 1 {
		t.Fatalf("ready session store = %#v", readiness.SessionStore)
	}
	if len(readiness.SessionStore.Errors) != 1 || readiness.SessionStore.Errors[0].SessionID != corruptID {
		t.Fatalf("ready errors = %#v", readiness.SessionStore.Errors)
	}
	if strings.Contains(ready.Body.String(), storeDir) {
		t.Fatalf("ready leaked store path: %s", ready.Body.String())
	}
}

func TestInspectStoredSessionReportsEventValidationFailure(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	session := newGatewaySession("inspect-corrupt-events", time.Now().UTC(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "hello"},
	})
	store := newSessionStore(storeDir)
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	record := sessionEventRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           1,
		SessionID:     session.ID,
		EventType:     string(protocol.EventAssistantDelta),
		Event: protocol.Event{
			Seq:  1,
			Type: protocol.EventAssistantDelta,
			Data: "provider payload sk-inspect-secret",
		},
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(storeDir, session.ID, sessionEventsJSONLName)
	if err := os.WriteFile(eventsPath, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	inspection, err := InspectStoredSession(storeDir, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.OfflineReplayReady || inspection.Events.Validation.Valid || inspection.Events.Validation.EnvelopeValid {
		t.Fatalf("inspection validation = %#v offline=%t", inspection.Events.Validation, inspection.OfflineReplayReady)
	}
	if inspection.Events.Validation.Line != 1 || inspection.Events.Validation.RecordNo != 1 || inspection.Events.Validation.Error == "" {
		t.Fatalf("inspection validation metadata = %#v", inspection.Events.Validation)
	}
	body, err = json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sk-inspect-secret") {
		t.Fatalf("inspection leaked raw provider payload: %s", body)
	}
	if len(inspection.Warnings) == 0 || !strings.Contains(inspection.Warnings[0], "event replay invalid") {
		t.Fatalf("warnings = %#v", inspection.Warnings)
	}
}

func writeCorruptStoredSessionHistory(t *testing.T, storeDir, sessionID string) {
	t.Helper()
	sessionDir := filepath.Join(storeDir, sessionID)
	manifest := sessionManifest{
		SchemaVersion: gatewaySessionSchemaVersion,
		SessionID:     sessionID,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		HistoryJSONL:  sessionHistoryJSONLName,
		EventsJSONL:   sessionEventsJSONLName,
		InputsJSONL:   sessionInputsJSONLName,
		SnapshotJSON:  sessionID + ".json",
	}
	if err := writeSessionManifest(filepath.Join(sessionDir, sessionManifestName), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, sessionHistoryJSONLName), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStoreRedoStateClearsOnNewTurnChange(t *testing.T) {
	store := newSessionStore(t.TempDir())
	session := newGatewaySession("redo-state", time.Now().UTC(), []protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	changeA := protocol.TurnChangeEvent{ChangeID: "change-a", RunID: "run-1", TurnID: "turn-1", Status: "recorded", FileCount: 1, Modified: 1, Reversible: true}
	changeB := protocol.TurnChangeEvent{ChangeID: "change-b", RunID: "run-1", TurnID: "turn-1", Status: "recorded", FileCount: 1, Added: 1, Reversible: true}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventTurnStarted, RunID: "run-1", TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
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
	projectorInspectionResult, err := InspectStoredSession(storeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	projectorInspection := projectorInspectionResult.Events.Projector
	if !projectorInspection.ParityOK ||
		projectorInspection.SessionID != sessionID ||
		projectorInspection.SeqRange != "1-16" ||
		projectorInspection.LastSeq != int64(len(storedEvents)) ||
		projectorInspection.SnapshotLastSeq != snapshot.LastSeq ||
		projectorInspection.ToolCallsRaw != 1 ||
		projectorInspection.ToolCallsProjected != snapshot.ToolCalls ||
		projectorInspection.ProjectionHash == "" ||
		!strings.Contains(projectorInspection.LastEventID, "type=run.completed") {
		t.Fatalf("projector inspection = %#v snapshot=%#v", projectorInspection, snapshot)
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
	if _, err := session.publish(protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.publish(protocol.Event{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-1", Data: protocol.ToolCall{ID: "call-1", Name: "big_output"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.publish(protocol.Event{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-1", AttemptID: "turn-001:tool-call-001:attempt-001", Data: "big_output"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.publish(protocol.Event{Type: protocol.EventToolOutputRefCreated, RunID: "run-1", Data: map[string]any{
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
	}}); err != nil {
		t.Fatal(err)
	}

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

func TestGatewaySessionInspectionUsesCanonicalProjectorFixtures(t *testing.T) {
	catalog := testkit.ReadCanonicalEdgeCaseCatalog(t)
	covered := map[string]bool{
		"stream_gap":            false,
		"parallel_cancellation": false,
		"late_output_ref":       false,
	}
	for _, fixture := range catalog.Fixtures {
		if _, ok := covered[fixture.Name]; !ok {
			continue
		}
		covered[fixture.Name] = true
		events := decodeGatewayCanonicalFixtureEvents(t, fixture)
		lastSeq := int64(0)
		for _, event := range events {
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
		}
		inspection := inspectSessionProjector("fixture-"+fixture.Name, events, lastSeq)
		if inspection.ProjectionHash == "" || inspection.SessionID != "fixture-"+fixture.Name {
			t.Fatalf("%s projector identity = %#v", fixture.Name, inspection)
		}
		switch fixture.Name {
		case "stream_gap":
			if inspection.ParityOK || inspection.SeqGap == nil || len(inspection.MismatchReasons) == 0 {
				t.Fatalf("stream gap should report projector mismatch: %#v", inspection)
			}
		case "parallel_cancellation":
			if !inspection.ParityOK || inspection.RunState != storedSessionRunStateFailed || inspection.ToolCallsRaw != 2 || inspection.ToolCallsProjected != 2 {
				t.Fatalf("parallel cancellation projector inspection = %#v", inspection)
			}
		case "late_output_ref":
			if !inspection.ParityOK || inspection.RunState != storedSessionRunStateCompleted || inspection.ToolCallsRaw != 1 || inspection.ToolCallsProjected != 1 {
				t.Fatalf("late output ref projector inspection = %#v", inspection)
			}
		}
	}
	for name, ok := range covered {
		if !ok {
			t.Fatalf("canonical projector fixture %q was not exercised", name)
		}
	}
}

func decodeGatewayCanonicalFixtureEvents(t *testing.T, fixture testkit.GoldenEdgeCaseFixture) []protocol.Event {
	t.Helper()
	events := make([]protocol.Event, 0, len(fixture.Events))
	for i, body := range fixture.Events {
		var event protocol.Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatalf("decode gateway fixture %s event %d: %v", fixture.Name, i, err)
		}
		events = append(events, event)
	}
	return events
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
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventToolCallRequested, RunID: "run-1", CallID: "call-fs", Data: protocol.ToolCall{ID: "call-fs", Name: "fs_read_file"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventToolCallStarted, RunID: "run-1", CallID: "call-fs", AttemptID: "turn-001:tool-call-001:attempt-001", Data: "fs_read_file"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session, protocol.Event{Type: protocol.EventToolOutputRefCreated, RunID: "run-1", Data: protocol.ToolOutputRefEvent{
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
