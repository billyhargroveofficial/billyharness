package gateway

import (
	"bytes"
	"context"
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
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

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

func TestGatewaySessionRunReusesPreAdmittedTelegramInput(t *testing.T) {
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

	body := `{"input_id":"telegram-update-42","prompt":"describe image","interrupt_policy":"interrupt","client_id":"telegram:123:u1001","client_type":"telegram","metadata":{"chat_id":"123","message_id":"7","update_id":"42","user_id":"1001"}}`
	admit := httptest.NewRecorder()
	server.Handler().ServeHTTP(admit, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", strings.NewReader(body)))
	if admit.Code != http.StatusCreated {
		t.Fatalf("admit status = %d body=%s", admit.Code, admit.Body.String())
	}

	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(body)))
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
	if records[0].Request.ClientType != "telegram" || records[0].Request.Metadata["update_id"] != "42" {
		t.Fatalf("admitted request = %#v", records[0].Request)
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

func TestGatewaySessionCorruptInputLedgerQuarantinedOnRestart(t *testing.T) {
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
	if _, err := server.store.AdmitInput(session, gatewayapi.SessionInputRequest{InputID: "corrupt-input", Prompt: "maybe"}); err != nil {
		t.Fatal(err)
	}
	inputsPath := filepath.Join(storeDir, created.ID, sessionInputsJSONLName)
	if err := os.WriteFile(inputsPath, []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	if _, ok := restarted.session(created.ID); !ok {
		t.Fatal("session should load even when inputs ledger is corrupt")
	}
	matches, err := filepath.Glob(inputsPath + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("quarantine matches = %#v", matches)
	}
	admit := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(admit, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", strings.NewReader(`{"input_id":"after-quarantine","prompt":"hello"}`)))
	if admit.Code != http.StatusCreated {
		t.Fatalf("admit after quarantine status = %d body=%s", admit.Code, admit.Body.String())
	}
}

func TestGatewaySessionRunCompletesInputOnPreflightFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{
		SessionStoreDir:     storeDir,
		AuthToken:           "secret",
		RequireMutationAuth: true,
	})

	create := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	createReq.Header.Set("Authorization", "Bearer secret")
	server.Handler().ServeHTTP(create, createReq)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	runBody := `{"input_id":"preflight-input","prompt":"conflict","provider":"deepseek","model":"gpt-5.5"}`
	run := httptest.NewRecorder()
	runReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(runBody))
	runReq.Header.Set("Authorization", "Bearer secret")
	runReq.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(run, runReq)
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	events := readProtocolEvents(t, run.Body)
	if !sawEvent(events, protocol.EventRunFailed) {
		t.Fatalf("run stream missing preflight failure: %#v", events)
	}
	records := readSessionInputRecords(t, filepath.Join(storeDir, created.ID, sessionInputsJSONLName))
	if len(records) != 2 {
		t.Fatalf("input record count = %d records=%#v", len(records), records)
	}
	if records[0].Kind != sessionInputAdmitted ||
		records[1].Kind != sessionInputCompleted ||
		records[1].TerminalStatus != "preflight_failed" ||
		!strings.Contains(records[1].FailureReason, "provider/model conflict") {
		t.Fatalf("input records = %#v", records)
	}
}

func TestGatewaySessionPromoteInputForNextRunUsesInputLedgerSequence(t *testing.T) {
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
	if _, err := server.store.AdmitInput(session, gatewayapi.SessionInputRequest{InputID: "input-1", Prompt: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.AdmitInput(session, gatewayapi.SessionInputRequest{InputID: "input-2", Prompt: "two"}); err != nil {
		t.Fatal(err)
	}
	firstSeq, err := server.store.PromoteInputForNextRun(session, "input-1")
	if err != nil {
		t.Fatal(err)
	}
	secondSeq, err := server.store.PromoteInputForNextRun(session, "input-2")
	if err != nil {
		t.Fatal(err)
	}
	if firstSeq != 1 || secondSeq != 2 {
		t.Fatalf("run seqs = %d, %d; want 1, 2", firstSeq, secondSeq)
	}
	records := readSessionInputRecords(t, filepath.Join(storeDir, created.ID, sessionInputsJSONLName))
	var promoted []sessionInputRecord
	for _, record := range records {
		if record.Kind == sessionInputPromoted {
			promoted = append(promoted, record)
		}
	}
	if len(promoted) != 2 || promoted[0].RunSeq != 1 || promoted[1].RunSeq != 2 {
		t.Fatalf("promoted records = %#v", promoted)
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
		validGatewaySessionEventRecord(1, "session-1", protocol.Event{
			Type:  protocol.EventRunStarted,
			RunID: "run-1",
		}),
		validGatewaySessionEventRecord(2, "session-1", protocol.Event{
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
		}),
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

func TestGatewaySessionEventsReplayRejectsCorruptEnvelopeRecords(t *testing.T) {
	tests := []struct {
		name    string
		records []sessionEventRecord
		raw     string
		want    string
		line    int
	}{
		{
			name: "invalid JSON",
			raw:  "{bad\n",
			want: "invalid JSONL record",
			line: 1,
		},
		{
			name: "wrong session ID",
			records: []sessionEventRecord{
				validGatewaySessionEventRecord(1, "other-session", protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}),
			},
			want: "session_id = \"other-session\", want \"session-1\"",
			line: 1,
		},
		{
			name: "skipped seq",
			records: []sessionEventRecord{
				validGatewaySessionEventRecord(2, "session-1", protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}),
			},
			want: "sequence gap: got 2 want 1",
			line: 1,
		},
		{
			name: "repeated seq",
			records: []sessionEventRecord{
				validGatewaySessionEventRecord(1, "session-1", protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}),
				validGatewaySessionEventRecord(1, "session-1", protocol.Event{Type: protocol.EventRunCompleted, RunID: "run-1"}),
			},
			want: "sequence gap: got 1 want 2",
			line: 2,
		},
		{
			name: "event seq mismatch",
			records: []sessionEventRecord{
				func() sessionEventRecord {
					record := validGatewaySessionEventRecord(1, "session-1", protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"})
					record.Event.Seq = 2
					return record
				}(),
			},
			want: "event seq = 2, record seq = 1",
			line: 1,
		},
		{
			name: "unknown event type",
			records: []sessionEventRecord{
				validGatewaySessionEventRecord(1, "session-1", protocol.Event{Type: protocol.EventType("weird.event"), RunID: "run-1"}),
			},
			want: "unsupported event type",
			line: 1,
		},
		{
			name: "unsupported envelope version",
			records: []sessionEventRecord{
				func() sessionEventRecord {
					record := validGatewaySessionEventRecord(1, "session-1", protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"})
					record.Event.SchemaVersion = protocol.EventSchemaVersion + 1
					return record
				}(),
			},
			want: "unsupported event schema_version",
			line: 1,
		},
		{
			name: "missing nested envelope",
			records: []sessionEventRecord{
				{
					SchemaVersion: gatewaySessionSchemaVersion,
					Seq:           1,
					SessionID:     "session-1",
					EventType:     string(protocol.EventRunStarted),
					Event:         protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"},
				},
			},
			want: "missing event schema_version",
			line: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), sessionEventsJSONLName)
			if tt.raw != "" {
				if err := os.WriteFile(path, []byte(tt.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				writeGatewaySessionEventRecords(t, path, tt.records...)
			}

			_, err := replaySessionEventsAfter(path, "session-1", 0)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
			var corrupt *eventlog.CorruptionError
			if !errors.As(err, &corrupt) {
				t.Fatalf("error %T does not expose CorruptionError", err)
			}
			if corrupt.Path != path || corrupt.Line != tt.line {
				t.Fatalf("corruption error = %#v", corrupt)
			}
		})
	}
}

func TestGatewaySessionEventsReplayRejectsDuplicateTerminalRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), sessionEventsJSONLName)
	records := []sessionEventRecord{
		validGatewaySessionEventRecord(1, "session-1", protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}),
		validGatewaySessionEventRecord(2, "session-1", protocol.Event{Type: protocol.EventRunCompleted, RunID: "run-1"}),
		validGatewaySessionEventRecord(3, "session-1", protocol.Event{Type: protocol.EventRunFailed, RunID: "run-1", Data: "late failure"}),
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

func validGatewaySessionEventRecord(seq int64, sessionID string, event protocol.Event) sessionEventRecord {
	now := time.Unix(1000+seq, 0).UTC()
	runID := strings.TrimSpace(event.RunID)
	if runID == "" {
		runID = "run-1"
	}
	storedEvent := protocol.EnrichEvent(event, protocol.EventEnvelope{
		Seq:    seq,
		Source: protocol.EventSourceGateway,
		RunID:  runID,
		TS:     now.Format(time.RFC3339Nano),
	})
	storedEvent.Seq = seq
	return sessionEventRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           seq,
		SessionID:     sessionID,
		RunSeq:        1,
		Timestamp:     now,
		EventType:     string(storedEvent.Type),
		Event:         storedEvent,
	}
}

func writeGatewaySessionEventRecords(t *testing.T, path string, records ...sessionEventRecord) {
	t.Helper()
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
		validGatewaySessionEventRecord(1, sessionID, protocol.Event{Type: protocol.EventRunStarted, RunID: "run-1"}),
		validGatewaySessionEventRecord(2, sessionID, protocol.Event{
			Type:      protocol.EventToolCallFinished,
			RunID:     "run-1",
			CallID:    "call-1",
			AttemptID: "attempt-1",
		}),
	} {
		if err := eventlog.AppendJSONL(eventsPath, record); err != nil {
			t.Fatal(err)
		}
	}

	inspection, err := InspectStoredSession(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	validation := inspection.Events.Validation
	if validation.Valid || validation.LifecycleValid || validation.Line != 2 || validation.RecordNo != 2 || validation.CorruptionKind != "lifecycle" ||
		!strings.Contains(validation.Error, "matching call_id") {
		t.Fatalf("validation = %#v", validation)
	}
	if inspection.OfflineReplayReady {
		t.Fatalf("corrupt event replay should not be offline-ready: %#v", inspection)
	}
	if len(inspection.Warnings) == 0 || !strings.Contains(strings.Join(inspection.Warnings, "\n"), "event replay invalid") {
		t.Fatalf("warnings = %#v", inspection.Warnings)
	}
	if strings.Contains(validation.Error, eventsPath) {
		t.Fatalf("validation leaked event path: %#v", validation)
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
