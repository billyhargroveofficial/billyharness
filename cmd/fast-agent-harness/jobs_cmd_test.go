package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestJobsCreateBuildsExplicitBoundedRequest(t *testing.T) {
	setJobsTestConfig(t)
	readRoot := filepath.Join(t.TempDir(), "notes")
	writeRoot := filepath.Join(t.TempDir(), "worktree")
	var captured gatewayapi.CreateJobRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/jobs" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode create request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		writeJobsTestJSON(t, w, jobCommandTestResponse("j-created", jobs.JobStatusRunning, true))
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := jobsCommand([]string{
		"create",
		"-gateway", server.URL,
		"-json",
		"-job-id", "j-explicit-cli-create",
		"-provider", "qwen",
		"-model", "qwen",
		"-thinking", "enabled",
		"-reasoning", "high",
		"-preset", "coding",
		"-workers", "3",
		"-duration", "6h",
		"-min-runtime", "4h",
		"-cadence", "1h",
		"-max-cycles", "5",
		"-max-attempts", "70",
		"-max-model-calls", "60",
		"-max-tokens", "900000",
		"-tool", "fs_read_file",
		"-tool", "fs_write_file",
		"-read-root", readRoot,
		"-write-root", writeRoot,
		"-network-host", "docs.example.com",
		"Investigate", "and", "implement", "the", "bounded", "change",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if captured.JobID != "j-explicit-cli-create" || captured.Goal != "Investigate and implement the bounded change" ||
		captured.Preset != jobs.PresetCoding || captured.Workers != 3 ||
		captured.DurationSeconds != 6*60*60 || captured.Deadline != nil ||
		captured.MinRuntimeSeconds != 4*60*60 || captured.CadenceSeconds != 60*60 || !captured.AutoStart {
		t.Fatalf("create request basics = %#v", captured)
	}
	if captured.Route != (jobs.ExecutionRoute{
		ProviderID:      "qwen",
		ModelID:         "qwen3.8-max-preview",
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}) {
		t.Fatalf("route = %#v", captured.Route)
	}
	if captured.Budget != (jobs.Budget{MaxCycles: 5, MaxAttempts: 70, MaxModelCalls: 60, MaxTokens: 900_000}) {
		t.Fatalf("budget = %#v", captured.Budget)
	}
	if captured.Authority.Mode != jobs.AuthorityModeAllowList ||
		!equalJobStrings(captured.Authority.Tools, []string{"fs_read_file", "fs_write_file"}) ||
		!equalJobStrings(captured.Authority.ReadRoots, []string{readRoot}) ||
		!equalJobStrings(captured.Authority.WriteRoots, []string{writeRoot}) ||
		!equalJobStrings(captured.Authority.NetworkHosts, []string{"docs.example.com"}) ||
		!equalJobStrings(captured.Authority.Providers, []string{"qwen"}) {
		t.Fatalf("authority = %#v", captured.Authority)
	}
	var response gatewayapi.JobResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, out.String())
	}
	if response.State.Spec.ID != "j-created" {
		t.Fatalf("response = %#v", response)
	}
}

func TestJobsCreateDefaultsToOneWorker(t *testing.T) {
	setJobsTestConfig(t)
	var captured gatewayapi.CreateJobRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode create request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		writeJobsTestJSON(t, w, jobCommandTestResponse("j-default-worker", jobs.JobStatusRunning, true))
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := jobsCommand([]string{
		"create",
		"-gateway", server.URL,
		"-provider", "qwen",
		"-model", "qwen",
		"-duration", "1h",
		"-min-runtime", "30m",
		"Use the safe worker default",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Workers != 1 || defaultJobWorkers != 1 {
		t.Fatalf("default workers: request=%d constant=%d", captured.Workers, defaultJobWorkers)
	}
	if captured.MinRuntimeSeconds != 30*60 || captured.CadenceSeconds != 258 {
		t.Fatalf("derived runtime schedule = %d/%d", captured.MinRuntimeSeconds, captured.CadenceSeconds)
	}
	if !strings.HasPrefix(captured.JobID, "j-") || len(captured.JobID) <= 2 {
		t.Fatalf("generated client job id = %q", captured.JobID)
	}
}

func TestJobsCreateDefaultsToRouteOnlyAuthority(t *testing.T) {
	setJobsTestConfig(t)
	var captured gatewayapi.CreateJobRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeJobsTestJSON(t, w, jobCommandTestResponse("j-deny-ambient", jobs.JobStatusQueued, false))
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	if err := jobsCommand([]string{
		"create", "-gateway", server.URL,
		"-provider", "qwen", "-model", "qwen",
		"-thinking", "enabled", "-reasoning", "high",
		"-start=false", "-goal", "Bounded provider-only analysis",
	}, &out); err != nil {
		t.Fatal(err)
	}
	if captured.DurationSeconds != uint64(defaultJobDuration/time.Second) ||
		captured.Budget != (jobs.Budget{
			MaxCycles: defaultJobMaxCycles, MaxAttempts: defaultJobMaxAttempts,
			MaxModelCalls: defaultJobMaxCalls, MaxTokens: defaultJobMaxTokens,
		}) {
		t.Fatalf("defaults = duration:%d budget:%#v", captured.DurationSeconds, captured.Budget)
	}
	if captured.Authority.Mode != jobs.AuthorityModeAllowList ||
		len(captured.Authority.Tools) != 0 || len(captured.Authority.ReadRoots) != 0 ||
		len(captured.Authority.WriteRoots) != 0 || len(captured.Authority.NetworkHosts) != 0 ||
		!equalJobStrings(captured.Authority.Providers, []string{"qwen"}) {
		t.Fatalf("omitted capability flags granted ambient authority: %#v", captured.Authority)
	}
	if captured.AutoStart {
		t.Fatal("-start=false was not preserved")
	}
}

func TestJobsCreateRejectsInvalidRouteAndWallClockBeforeSending(t *testing.T) {
	setJobsTestConfig(t)
	deadline := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	base := []string{
		"create", "-gateway", server.URL,
		"-provider", "qwen", "-model", "qwen",
		"-thinking", "enabled", "-reasoning", "high",
		"-goal", "Validate locally",
	}
	var out bytes.Buffer
	err := jobsCommand(append(append([]string{}, base...), "-duration", "1h", "-deadline", deadline), &out)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("ambiguous wall clock error = %v", err)
	}
	err = jobsCommand([]string{
		"create", "-gateway", server.URL,
		"-provider", "qwen", "-model", "gpt-5.5",
		"-thinking", "enabled", "-reasoning", "high",
		"-goal", "Reject mismatched route",
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "belongs to provider") {
		t.Fatalf("mismatched route error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("invalid requests reached gateway %d times", requests)
	}
}

func TestResolveJobWallClockSupportsDefaultDurationAndAbsoluteDeadline(t *testing.T) {
	duration, deadline, err := resolveJobWallClock("", "")
	if err != nil {
		t.Fatal(err)
	}
	if duration != uint64(defaultJobDuration/time.Second) || deadline != nil {
		t.Fatalf("default wall clock = duration:%d deadline:%v", duration, deadline)
	}

	want := time.Date(2026, 7, 25, 18, 30, 0, 0, time.FixedZone("test", 3*60*60))
	duration, deadline, err = resolveJobWallClock("", want.Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if duration != 0 || deadline == nil || !deadline.Equal(want) || deadline.Location() != time.UTC {
		t.Fatalf("absolute wall clock = duration:%d deadline:%v, want UTC %v", duration, deadline, want.UTC())
	}
}

func TestResolveJobCadenceDerivesOneKnobRuntimeSchedule(t *testing.T) {
	derived, err := resolveJobCadence(uint64((6*time.Hour)/time.Second), 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	// ceil(21600 / 7) ensures seven possible inter-cycle waits can reach the
	// requested floor without hiding the derived provider-neutral API value.
	if derived != 3086 {
		t.Fatalf("derived cadence = %d, want 3086", derived)
	}
	explicit, err := resolveJobCadence(21_600, 900, 8)
	if err != nil || explicit != 900 {
		t.Fatalf("explicit cadence = %d, %v", explicit, err)
	}
	if _, err := resolveJobCadence(21_600, 0, 1); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("single-cycle derivation error = %v", err)
	}
}

func TestResolveJobRouteInfersQwenSubscriptionModelFromProvider(t *testing.T) {
	route, err := resolveJobRoute("qwen", "", "enabled", "high")
	if err != nil {
		t.Fatal(err)
	}
	want := jobs.ExecutionRoute{
		ProviderID:      "qwen",
		ModelID:         "qwen3.8-max-preview",
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}
	if route != want {
		t.Fatalf("route = %#v, want %#v", route, want)
	}
}

func TestJobsListShowAndActionsUseGatewayClient(t *testing.T) {
	setJobsTestConfig(t)
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs":
			writeJobsTestJSON(t, w, gatewayapi.JobListResponse{Jobs: []gatewayapi.JobSummaryResponse{
				{ID: "j-one", Preset: jobs.PresetResearch, Status: jobs.JobStatusRunning, Active: true},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j-one":
			response := jobCommandTestResponse("j-one", jobs.JobStatusRunning, true)
			response.State.Spec.MinCycles = 4
			response.State.Cycle = 2
			response.State.Usage.Cycles = 2
			writeJobsTestJSON(t, w, response)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/jobs/j-one/"):
			action := strings.TrimPrefix(r.URL.Path, "/v1/jobs/j-one/")
			active := action == "run" || action == "resume"
			status := jobs.JobStatusPaused
			if active {
				status = jobs.JobStatusRunning
			}
			if action == "cancel" {
				status = jobs.JobStatusCancelled
			}
			writeJobsTestJSON(t, w, jobCommandTestResponse("j-one", status, active))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	if err := jobsCommand([]string{"list", "-gateway", server.URL, "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var list gatewayapi.JobListResponse
	if err := json.Unmarshal(out.Bytes(), &list); err != nil || len(list.Jobs) != 1 || list.Jobs[0].ID != "j-one" {
		t.Fatalf("list JSON = %#v err=%v\n%s", list, err, out.String())
	}
	out.Reset()
	if err := jobsCommand([]string{"show", "-gateway", server.URL, "j-one"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"job: j-one", "status: running active=true", "goal: Exercise the CLI", "provider=qwen", "stage=investigate",
		"usage: cycles=2 min=4 max=8", "min is the earliest successful-completion cycle, not a wall-clock target",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("show output missing %q:\n%s", want, out.String())
		}
	}
	for _, action := range []string{"run", "pause", "resume", "cancel"} {
		out.Reset()
		if err := jobsCommand([]string{action, "-gateway", server.URL, "-json", "j-one"}, &out); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		var response gatewayapi.JobResponse
		if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.State.Spec.ID != "j-one" {
			t.Fatalf("%s JSON response = %#v err=%v", action, response, err)
		}
	}
	for _, want := range []string{
		"GET /v1/jobs",
		"GET /v1/jobs/j-one",
		"POST /v1/jobs/j-one/run",
		"POST /v1/jobs/j-one/pause",
		"POST /v1/jobs/j-one/resume",
		"POST /v1/jobs/j-one/cancel",
	} {
		if !containsJobString(requests, want) {
			t.Fatalf("requests %#v missing %q", requests, want)
		}
	}
}

func TestJobsHelpDocumentsFailClosedAuthorityAndPresets(t *testing.T) {
	var out bytes.Buffer
	if err := jobsCommand([]string{"help"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"jobs create", "jobs run|pause|resume|cancel", "research", "coding", "fail-closed",
		"unattended jobs require", "Token Plan Individual", "interactive programming/agent-tool use only",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := jobsCommand([]string{"create", "-h"}, &out); err != nil {
		t.Fatalf("create help: %v", err)
	}
	if !strings.Contains(out.String(), "-network-host") || !strings.Contains(out.String(), "-max-model-calls") ||
		!strings.Contains(out.String(), "Token Plan Individual") {
		t.Fatalf("create flag help incomplete:\n%s", out.String())
	}
}

func TestWarnJobProviderTermsForBuiltInQwenOnly(t *testing.T) {
	var out bytes.Buffer
	warnJobProviderTerms(jobs.ExecutionRoute{ProviderID: "qwen"}, &out)
	for _, want := range []string{
		"WARNING", "interactive programming/agent-tool use only", "non-interactive batch",
		"unless your configured endpoint", "Lite 1-2", "Standard 3-4", "Pro 6-8",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("Qwen warning missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	warnJobProviderTerms(jobs.ExecutionRoute{ProviderID: "qwen-metered"}, &out)
	if out.Len() != 0 {
		t.Fatalf("custom provider received Token Plan warning: %q", out.String())
	}
}

func setJobsTestConfig(t *testing.T) {
	t.Helper()
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
}

func jobCommandTestResponse(id string, status jobs.JobStatus, active bool) gatewayapi.JobResponse {
	return gatewayapi.JobResponse{
		State: jobs.JobState{
			Spec: jobs.JobSpec{
				ID:       id,
				Goal:     "Exercise the CLI",
				Preset:   jobs.PresetResearch,
				Workers:  2,
				Deadline: time.Now().UTC().Add(6 * time.Hour),
				Budget:   jobs.Budget{MaxCycles: 8, MaxAttempts: 128, MaxModelCalls: 128, MaxTokens: 1_000_000},
				Route: jobs.ExecutionRoute{
					ProviderID: "qwen", ModelID: "qwen3.8-max-preview", Thinking: "enabled", ReasoningEffort: "high",
				},
				Workflow:  jobs.WorkflowControl{StageOrder: []string{"investigate", "reduce", "supervise"}},
				Authority: jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{"qwen"}},
			},
			Status:         status,
			Cycle:          1,
			NextStageIndex: 0,
			Usage:          jobs.Usage{Cycles: 1, Attempts: 2, ModelCalls: 2, InputTokens: 100, OutputTokens: 50},
		},
		Active: active,
	}
}

func writeJobsTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func equalJobStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsJobString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
