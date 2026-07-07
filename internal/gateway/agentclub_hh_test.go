package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub/hhapplicant"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestHHReviewQueueRouteAdmitsIngressInputWithoutDispatch(t *testing.T) {
	server, storeDir := newHHReviewQueueTestServer(t)
	repo := t.TempDir()
	stdout := []byte("Pending: 1 (показано 1)\n\n#101 neg=202 | Vacancy | class=other | reason\n    SECRET recruiter text\n")
	runner := &recordingHHRunner{stdout: stdout}
	server.hhApplicantReviewQueue = hhapplicant.Adapter{
		Runner:           runner,
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
		Timeout:          time.Second,
		OutputLimitBytes: 4096,
	}
	owner := gatewayapi.SessionOwner{ClientID: "ingress:hh-applicant-tool:prod", ClientType: "ingress"}
	sessionID := createScopedTestSession(t, server, owner)

	resp, status := postHHReviewQueue(t, server, sessionID, gatewayapi.HHReviewQueueRequest{
		Profile:  "prod",
		Limit:    1,
		RepoRoot: repo,
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d response=%#v", status, resp)
	}
	if resp.InputID == "" || resp.Input.InputID != resp.InputID || resp.State != sessionInputAdmitted || resp.Duplicate {
		t.Fatalf("response = %#v", resp)
	}
	if !resp.Admitted || resp.AuditStatus != ingressAuditAdmitted || resp.RunDispatched {
		t.Fatalf("response = %#v", resp)
	}
	if resp.ClientType != "ingress" || resp.ClientID != owner.ClientID || resp.Profile != "prod" {
		t.Fatalf("response owner/profile = %#v", resp)
	}
	if resp.OutputSHA256 == "" || resp.PayloadSHA256 != resp.OutputSHA256 || resp.ExternalEventIDHash == "" || resp.ReviewItemCount != 1 {
		t.Fatalf("response hashes/count = %#v", resp)
	}
	if !hasString(resp.MetadataKeys, "hh.output_sha256") || !hasString(resp.MetadataKeys, "ingress.policy") {
		t.Fatalf("metadata keys = %#v", resp.MetadataKeys)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("runner calls = %d", len(runner.specs))
	}
	spec := runner.specs[0]
	if spec.Name != hhapplicant.ReviewQueueCommand || strings.Join(spec.Args, " ") != "cohort-review --limit 1" {
		t.Fatalf("command spec = %#v", spec)
	}
	if spec.Dir != repo || !hasString(spec.Env, "HH_PROFILE_ID=prod") {
		t.Fatalf("command spec = %#v", spec)
	}

	statusSnapshot := server.sessions[sessionID].Status()
	if statusSnapshot.Running || statusSnapshot.LastEvent != "" {
		t.Fatalf("route should not dispatch a run: %#v", statusSnapshot)
	}
	if _, err := os.Stat(filepath.Join(storeDir, sessionID, sessionEventsJSONLName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("route should not create session events, stat err=%v", err)
	}
	inputs := readSessionInputRecords(t, filepath.Join(storeDir, sessionID, sessionInputsJSONLName))
	if len(inputs) != 1 || inputs[0].InputID != resp.InputID || !strings.Contains(inputs[0].Prompt, "SECRET recruiter text") {
		t.Fatalf("inputs = %#v", inputs)
	}
	audit, err := server.store.ReplayIngressAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 || audit[0].Decision != ingressAuditReceived || audit[1].Decision != ingressAuditAdmitted || audit[1].InputID != resp.InputID {
		t.Fatalf("audit = %#v", audit)
	}
	auditJSON, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRET recruiter text", owner.ClientID, "hh-review-queue-"} {
		if strings.Contains(string(auditJSON), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, auditJSON)
		}
	}

	duplicate, duplicateStatus := postHHReviewQueue(t, server, sessionID, gatewayapi.HHReviewQueueRequest{
		Profile:  "prod",
		Limit:    1,
		RepoRoot: repo,
	})
	if duplicateStatus != http.StatusOK || !duplicate.Duplicate || duplicate.InputID != resp.InputID {
		t.Fatalf("duplicate status=%d response=%#v", duplicateStatus, duplicate)
	}
}

func TestHHReviewQueueRouteDeniesCrossProfileBeforeRunner(t *testing.T) {
	server, _ := newHHReviewQueueTestServer(t)
	repo := t.TempDir()
	runner := &recordingHHRunner{stdout: []byte("Pending: 1\n#1 neg=2 | Vacancy | class=other | reason\n")}
	server.hhApplicantReviewQueue = hhapplicant.Adapter{
		Runner:           runner,
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
		OutputLimitBytes: 1024,
	}
	sessionID := createScopedTestSession(t, server, gatewayapi.SessionOwner{ClientID: "ingress:hh-applicant-tool:prod", ClientType: "ingress"})

	body, _ := json.Marshal(gatewayapi.HHReviewQueueRequest{Profile: "other", Limit: 1, RepoRoot: repo})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/hh/review-queue", bytes.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(runner.specs) != 0 {
		t.Fatalf("runner should not run on cross-profile denial: %#v", runner.specs)
	}
}

func TestHHReviewQueueRouteMapsAdapterFailures(t *testing.T) {
	cases := []struct {
		name   string
		req    gatewayapi.HHReviewQueueRequest
		runner *recordingHHRunner
		status int
	}{
		{
			name:   "invalid limit",
			req:    gatewayapi.HHReviewQueueRequest{Profile: "prod", Limit: 0},
			runner: &recordingHHRunner{stdout: []byte("unused")},
			status: http.StatusBadRequest,
		},
		{
			name:   "invalid repo",
			req:    gatewayapi.HHReviewQueueRequest{Profile: "prod", Limit: 1, RepoRoot: filepath.Join(t.TempDir(), "missing")},
			runner: &recordingHHRunner{stdout: []byte("unused")},
			status: http.StatusBadRequest,
		},
		{
			name:   "command failed",
			req:    gatewayapi.HHReviewQueueRequest{Profile: "prod", Limit: 1},
			runner: &recordingHHRunner{err: &hhapplicant.CommandError{ExitCode: 7}},
			status: http.StatusBadGateway,
		},
		{
			name:   "timeout",
			req:    gatewayapi.HHReviewQueueRequest{Profile: "prod", Limit: 1},
			runner: &recordingHHRunner{err: hhapplicant.ErrCommandTimeout},
			status: http.StatusGatewayTimeout,
		},
		{
			name:   "output cap",
			req:    gatewayapi.HHReviewQueueRequest{Profile: "prod", Limit: 1},
			runner: &recordingHHRunner{result: hhapplicant.CommandResult{StdoutTruncated: true}},
			status: http.StatusRequestEntityTooLarge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newHHReviewQueueTestServer(t)
			repo := t.TempDir()
			if tc.req.RepoRoot == "" {
				tc.req.RepoRoot = repo
			}
			server.hhApplicantReviewQueue = hhapplicant.Adapter{
				Runner:           tc.runner,
				RepoRoot:         repo,
				AllowedRepoRoots: []string{repo},
				OutputLimitBytes: 1024,
			}
			sessionID := createScopedTestSession(t, server, gatewayapi.SessionOwner{ClientID: "ingress:hh-applicant-tool:prod", ClientType: "ingress"})
			body, _ := json.Marshal(tc.req)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/hh/review-queue", bytes.NewReader(body)))
			if rec.Code != tc.status {
				t.Fatalf("status = %d body=%s want %d", rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
}

type recordingHHRunner struct {
	stdout []byte
	result hhapplicant.CommandResult
	err    error
	specs  []hhapplicant.CommandSpec
}

func (r *recordingHHRunner) Run(ctx context.Context, spec hhapplicant.CommandSpec) (hhapplicant.CommandResult, error) {
	r.specs = append(r.specs, spec)
	if r.err != nil {
		return r.result, r.err
	}
	if r.result.Stdout != nil || r.result.Stderr != nil || r.result.StdoutTruncated || r.result.StderrTruncated {
		return r.result, nil
	}
	return hhapplicant.CommandResult{Stdout: append([]byte(nil), r.stdout...)}, nil
}

func newHHReviewQueueTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	return server, storeDir
}

func postHHReviewQueue(t *testing.T, server *Server, sessionID string, req gatewayapi.HHReviewQueueRequest) (gatewayapi.HHReviewQueueResponse, int) {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/agentclub/hh/review-queue", bytes.NewReader(body)))
	var resp gatewayapi.HHReviewQueueResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return resp, rec.Code
}
