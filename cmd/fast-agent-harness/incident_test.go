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
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestIncidentCollectWritesRedactedBundle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	outDir := filepath.Join(root, "incident")
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_PROVIDER", "mock")
	t.Setenv("FAST_AGENT_MODEL", "mock")
	writeTestFile(t, repo, "cmd/fast-agent-harness/main.go", "package main\n")

	resolved, err := config.ResolveStrict()
	if err != nil {
		t.Fatal(err)
	}
	cfg := resolved.Config
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MCPEnabled = false
	storeDir := gateway.DefaultSessionStoreDir()
	sessionID := createIncidentTestSession(t, cfg, storeDir)

	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
		doctorRunnerKey(repo, "git", "ls-files", "--", "*.go"): {
			out: "cmd/fast-agent-harness/main.go\n",
		},
		doctorRunnerKey(repo, "git", "status", "--short"): {
			out: "",
		},
	}}
	report, err := collectIncidentBundleFromResolved(context.Background(), resolved, incidentCollectOptions{
		SessionID:      sessionID,
		OutDir:         outDir,
		SessionDir:     storeDir,
		RepoDir:        repo,
		IncludeLogs:    false,
		IncludeMCP:     false,
		DoctorBuild:    false,
		DoctorServices: false,
		DoctorGateway:  false,
		Timeout:        time.Second,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutDir != outDir || report.SessionID != sessionID {
		t.Fatalf("report = %#v", report)
	}

	for _, rel := range []string{
		"incident-manifest.json",
		"doctor.json",
		"config.json",
		"auth.txt",
		"session-inspect.json",
		"session-context.json",
		"session-transcript-rich.md",
		"session-events.redacted.jsonl",
	} {
		path := filepath.Join(outDir, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing bundle file %s: %v", rel, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", rel, info.Mode().Perm())
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		assertIncidentRedacted(t, rel, string(body))
	}

	events, err := os.ReadFile(filepath.Join(outDir, "session-events.redacted.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "[redacted]") {
		t.Fatalf("redacted event export missing marker:\n%s", events)
	}
}

func createIncidentTestSession(t *testing.T, cfg config.Config, storeDir string) string {
	t.Helper()
	server := gateway.NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), gateway.ServerOptions{SessionStoreDir: storeDir})
	createBody, err := json.Marshal(gateway.CreateSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(create, req)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created gateway.SessionResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	secretPrompt := strings.Join([]string{
		"please echo sk-12345678901234567890",
		"https://alice:secret@example.test/path?token=supersecret",
		"Authorization: Bearer abcdefghijklmnop",
		"X-Api-Key: hiddenvalue123456",
		"Cookie: sessionid=verysecretcookie",
	}, "\n")
	runBody, err := json.Marshal(gateway.RunRequest{Prompt: secretPrompt})
	if err != nil {
		t.Fatal(err)
	}
	run := httptest.NewRecorder()
	runReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewReader(runBody))
	runReq.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(run, runReq)
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	return created.ID
}

func assertIncidentRedacted(t *testing.T, rel string, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"sk-12345678901234567890",
		"alice:secret@",
		"supersecret",
		"Bearer abcdefghijklmnop",
		"hiddenvalue123456",
		"verysecretcookie",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("%s leaked %q:\n%s", rel, forbidden, body)
		}
	}
}
