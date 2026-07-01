package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewayRunSendsFullRunRequest(t *testing.T) {
	var captured gateway.RunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/v1/run" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{Type: protocol.EventRunCompleted})
	}))
	t.Cleanup(server.Close)

	err := gatewayRun(context.Background(), server.URL, "/v1/run", gateway.RunRequest{
		Prompt:          "ping",
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		Profile:         "billy",
		Thinking:        "enabled",
		ReasoningEffort: "xhigh",
		MaxToolRounds:   42,
		AccessMode:      "plan",
	}, func(protocol.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Provider != "openai-codex" ||
		captured.Model != "gpt-5.5" ||
		captured.Profile != "billy" ||
		captured.ReasoningEffort != "xhigh" ||
		captured.AccessMode != "plan" ||
		captured.MaxToolRounds != 42 ||
		captured.Prompt != "ping" {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestGatewayRunReturnsStreamedFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{Type: protocol.EventRunFailed, Data: "boom"})
	}))
	t.Cleanup(server.Close)

	err := gatewayRun(context.Background(), server.URL, "/v1/run", gateway.RunRequest{Prompt: "ping"}, func(protocol.Event) {})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverGatewayURLUsesConfiguredHealthyGateway(t *testing.T) {
	t.Setenv("FAST_AGENT_GATEWAY_URL", "")
	t.Setenv("BILLYHARNESS_GATEWAY_URL", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	addr := strings.TrimPrefix(server.URL, "http://")
	got, ok := discoverGatewayURL(context.Background(), config.Config{GatewayAddr: addr})
	if !ok {
		t.Fatal("gateway was not discovered")
	}
	if got != server.URL {
		t.Fatalf("gateway URL = %q, want %q", got, server.URL)
	}
}

func TestNormalizeGatewayURL(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:8765":       "http://127.0.0.1:8765",
		":8765":                "http://127.0.0.1:8765",
		"0.0.0.0:8765":         "http://127.0.0.1:8765",
		"http://localhost:80/": "http://localhost:80",
	}
	for input, want := range tests {
		if got := normalizeGatewayURL(input); got != want {
			t.Fatalf("normalizeGatewayURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConfigInspectJSONDoesNotLeakDotenvSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("DEEPSEEK_API_KEY=sk-config-inspect-secret\nFAST_AGENT_MODEL=deepseek-v4-pro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := configCommand([]string{"inspect", "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sk-config-inspect-secret") {
		t.Fatalf("config inspect leaked secret: %s", out.String())
	}
	if !strings.Contains(out.String(), `"model"`) || !strings.Contains(out.String(), `deepseek-v4-pro`) {
		t.Fatalf("config inspect missing model: %s", out.String())
	}
	if !strings.Contains(out.String(), `"diagnostics"`) ||
		!strings.Contains(out.String(), `"provider_auth"`) ||
		!strings.Contains(out.String(), `"provider_capability"`) ||
		!strings.Contains(out.String(), `"max_output_tokens"`) ||
		!strings.Contains(out.String(), `"runtime_tool"`) {
		t.Fatalf("config inspect missing diagnostics: %s", out.String())
	}
}

func TestMemoryCommandAddAndList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	out := captureStdout(t, func() {
		if err := memoryCmd([]string{"add", "type=user", "topic=style", `summary="Prefers concise output"`, "path=topics/style.md", `body="Concise body"`, "confirm=true"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "written=true") {
		t.Fatalf("add output = %s", out)
	}
	out = captureStdout(t, func() {
		if err := memoryCmd([]string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "topic=style") || !strings.Contains(out, "Prefers concise output") {
		t.Fatalf("list output = %s", out)
	}
}

func TestSessionsImportCommandConvertsExternalTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	body := strings.Join([]string{
		`{"role":"user","content":"import me"}`,
		`{"role":"assistant","content":"imported","tool_calls":[{"name":"shell"}]}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := sessionsCommand([]string{"import", "-input", path}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"billyharness session import", "messages: 2 imported + 1 marker = 3", "unsupported_tool_call"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("import output missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := sessionsCommand([]string{"import", "-input", path, "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Messages []protocol.Message `json:"messages"`
		Events   []protocol.Event   `json:"events"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 3 || result.Messages[0].Role != protocol.RoleSystem || result.Events[0].Type != protocol.EventSessionImported {
		t.Fatalf("json import result = %#v", result)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	defer func() { os.Stdout = old }()
	fn()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(read); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSessionsCommandListsAndInspectsStore(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := gateway.NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), gateway.ServerOptions{SessionStoreDir: storeDir})

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created gateway.SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", strings.NewReader(`{"prompt":"inspect me"}`)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}

	var listOut bytes.Buffer
	if err := sessionsCommand([]string{"list", "-dir", storeDir}, &listOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOut.String(), created.ID) || !strings.Contains(listOut.String(), "replay=true") {
		t.Fatalf("list output missing session/replay:\n%s", listOut.String())
	}

	var inspectOut bytes.Buffer
	if err := sessionsCommand([]string{"inspect", "-dir", storeDir, created.ID}, &inspectOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"offline replay: true", "config_snapshot exists=true", "model_provider_snapshot exists=true", "mcp_snapshot exists=true"} {
		if !strings.Contains(inspectOut.String(), want) {
			t.Fatalf("inspect output missing %q:\n%s", want, inspectOut.String())
		}
	}

	var jsonOut bytes.Buffer
	if err := sessionsCommand([]string{"inspect", "-dir", storeDir, "-json", created.ID}, &jsonOut); err != nil {
		t.Fatal(err)
	}
	var inspection gateway.StoredSessionInspection
	if err := json.Unmarshal(jsonOut.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.SessionID != created.ID || !inspection.OfflineReplayReady || inspection.Events.Records == 0 {
		t.Fatalf("inspection = %#v", inspection)
	}

	var contextOut bytes.Buffer
	if err := sessionsCommand([]string{"context", "-dir", storeDir, created.ID}, &contextOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"active context:", "runtime: model=mock", "prompt sections:", "prompt cache:"} {
		if !strings.Contains(contextOut.String(), want) {
			t.Fatalf("context output missing %q:\n%s", want, contextOut.String())
		}
	}

	var indexOut bytes.Buffer
	if err := sessionsCommand([]string{"index", "rebuild", "-dir", storeDir}, &indexOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexOut.String(), "session index") || !strings.Contains(indexOut.String(), created.ID) {
		t.Fatalf("index rebuild output:\n%s", indexOut.String())
	}
	var indexJSON bytes.Buffer
	if err := sessionsCommand([]string{"index", "show", "-dir", storeDir, "-json"}, &indexJSON); err != nil {
		t.Fatal(err)
	}
	var index gateway.StoredSessionIndex
	if err := json.Unmarshal(indexJSON.Bytes(), &index); err != nil {
		t.Fatal(err)
	}
	if index.SessionCount != 1 || index.Sessions[0].ID != created.ID {
		t.Fatalf("index = %#v", index)
	}
	var deleteOut bytes.Buffer
	if err := sessionsCommand([]string{"index", "delete", "-dir", storeDir}, &deleteOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deleteOut.String(), "deleted") {
		t.Fatalf("index delete output: %s", deleteOut.String())
	}
}

func TestSessionsSearchToolsErrorsUsageRunsCommands(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	writeTestDiagnosticsIndex(t, storeDir)

	var searchOut bytes.Buffer
	if err := sessionsCommand([]string{"search", "-dir", storeDir, "-limit", "1", "auth"}, &searchOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchOut.String(), "session search") ||
		!strings.Contains(searchOut.String(), "session-a") ||
		strings.Contains(searchOut.String(), "session-b") {
		t.Fatalf("search output:\n%s", searchOut.String())
	}

	var searchJSON bytes.Buffer
	if err := sessionsCommand([]string{"search", "-dir", storeDir, "-json", "auth"}, &searchJSON); err != nil {
		t.Fatal(err)
	}
	var searchResult struct {
		Total int                            `json:"total"`
		Rows  []gateway.StoredSessionTextRow `json:"rows"`
	}
	if err := json.Unmarshal(searchJSON.Bytes(), &searchResult); err != nil {
		t.Fatal(err)
	}
	if searchResult.Total != 1 || len(searchResult.Rows) != 1 || searchResult.Rows[0].SessionID != "session-a" {
		t.Fatalf("search json = %#v", searchResult)
	}

	var toolsOut bytes.Buffer
	if err := sessionsCommand([]string{"tools", "-dir", storeDir, "-name", "shell", "-status", "failed"}, &toolsOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toolsOut.String(), "shell_exec") ||
		!strings.Contains(toolsOut.String(), "exit_status") ||
		strings.Contains(toolsOut.String(), "fs_read_file") {
		t.Fatalf("tools output:\n%s", toolsOut.String())
	}

	var errorsJSON bytes.Buffer
	if err := sessionsCommand([]string{"errors", "-dir", storeDir, "-query", "exit", "-json"}, &errorsJSON); err != nil {
		t.Fatal(err)
	}
	var errorsResult struct {
		Rows []gateway.StoredSessionErrorRow `json:"rows"`
	}
	if err := json.Unmarshal(errorsJSON.Bytes(), &errorsResult); err != nil {
		t.Fatal(err)
	}
	if len(errorsResult.Rows) != 1 || errorsResult.Rows[0].CallID != "call-shell" {
		t.Fatalf("errors json = %#v", errorsResult)
	}

	var usageOut bytes.Buffer
	if err := sessionsCommand([]string{"usage", "-dir", storeDir, "-limit", "1"}, &usageOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(usageOut.String(), "session-b") ||
		!strings.Contains(usageOut.String(), "total=60") ||
		strings.Contains(usageOut.String(), "session-a") {
		t.Fatalf("usage output:\n%s", usageOut.String())
	}

	var runsJSON bytes.Buffer
	if err := sessionsCommand([]string{"runs", "-dir", storeDir, "-status", "failed", "-json"}, &runsJSON); err != nil {
		t.Fatal(err)
	}
	var runsResult struct {
		Rows []gateway.StoredSessionRunRow `json:"rows"`
	}
	if err := json.Unmarshal(runsJSON.Bytes(), &runsResult); err != nil {
		t.Fatal(err)
	}
	if len(runsResult.Rows) != 1 || runsResult.Rows[0].SessionID != "session-a" || runsResult.Rows[0].Status != "failed" {
		t.Fatalf("runs json = %#v", runsResult)
	}

	var missingOut bytes.Buffer
	err := sessionsCommand([]string{"search", "-dir", filepath.Join(t.TempDir(), "missing"), "auth"}, &missingOut)
	if err == nil || !strings.Contains(err.Error(), "sessions index rebuild") {
		t.Fatalf("missing index err = %v", err)
	}
}

func TestParseChatIDs(t *testing.T) {
	got, err := parseChatIDs("-100123, 42\n99")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{-100123, 42, 99} {
		if !got[id] {
			t.Fatalf("missing chat id %d in %#v", id, got)
		}
	}
	if _, err := parseChatIDs("bad"); err == nil {
		t.Fatal("expected invalid chat id error")
	}
}

func writeTestDiagnosticsIndex(t *testing.T, storeDir string) {
	t.Helper()
	index := gateway.StoredSessionDiagnosticsIndex{
		SchemaVersion: 1,
		BuiltAt:       time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		Dir:           storeDir,
		SessionCount:  2,
		TextRowCount:  2,
		ToolRowCount:  2,
		ErrorRowCount: 1,
		RunRowCount:   2,
		UsageRowCount: 2,
		TextRows: []gateway.StoredSessionTextRow{
			{SessionID: "session-a", MessageIndex: 2, Role: string(protocol.RoleUser), Text: "auth token failed during build", TextBytes: 30},
			{SessionID: "session-b", MessageIndex: 3, Role: string(protocol.RoleAssistant), Text: "release note ready", TextBytes: 18},
		},
		ToolRows: []gateway.StoredSessionToolRow{
			{SessionID: "session-a", Seq: 8, RunID: "run-a", CallID: "call-shell", AttemptID: "attempt-shell", Name: "shell_exec", Status: "failed", Error: "exit_status", ArgsPreview: `{"cmd":"go test ./..."}`},
			{SessionID: "session-b", Seq: 4, RunID: "run-b", CallID: "call-read", AttemptID: "attempt-read", Name: "fs_read_file", Status: "finished"},
		},
		ErrorRows: []gateway.StoredSessionErrorRow{
			{SessionID: "session-a", Seq: 8, EventType: string(protocol.EventToolCallFinished), RunID: "run-a", CallID: "call-shell", AttemptID: "attempt-shell", Name: "shell_exec", Status: "failed", Error: "exit_status"},
		},
		RunRows: []gateway.StoredSessionRunRow{
			{SessionID: "session-a", RunID: "run-a", StartSeq: 1, EndSeq: 9, Status: "failed", Error: "exit_status"},
			{SessionID: "session-b", RunID: "run-b", StartSeq: 1, EndSeq: 5, Status: "completed"},
		},
		UsageRows: []gateway.StoredSessionUsageRow{
			{SessionID: "session-a", RunID: "run-a", Status: "failed", InputTokens: 3, OutputTokens: 1, ModelCalls: 1, ToolCalls: 1},
			{SessionID: "session-b", RunID: "run-b", Status: "completed", InputTokens: 40, OutputTokens: 10, CacheHitTokens: 5, CacheMissTokens: 5, ModelCalls: 1},
		},
	}
	body, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(storeDir, "index")
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "diagnostics.json"), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
