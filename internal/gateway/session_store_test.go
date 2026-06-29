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

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
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
	assertPerm(t, storeDir, 0o700)
	assertPerm(t, sessionDir, 0o700)
	assertPerm(t, manifestPath, 0o600)
	assertPerm(t, historyPath, 0o600)
	assertPerm(t, eventsPath, 0o600)
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
	if manifest.SessionID != created.ID || manifest.HistoryJSONL != sessionHistoryJSONLName || manifest.EventsJSONL != sessionEventsJSONLName {
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
		inspection.Events.OutputRefBytes != int64(len(body)) {
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
