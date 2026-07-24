package gatewayclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestJobClientTypedEndpointsAndEscapedIDs(t *testing.T) {
	const rawID = " job /with?reserved%chars "
	escapedID := url.PathEscape(strings.TrimSpace(rawID))
	var gotCreate gatewayapi.CreateJobRequest
	var requests []string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/jobs":
			if err := json.NewDecoder(r.Body).Decode(&gotCreate); err != nil {
				t.Errorf("decode create request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			writeJobResponse(t, w, "created", true)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/jobs":
			writeJSON(t, w, gatewayapi.JobListResponse{Jobs: []gatewayapi.JobSummaryResponse{
				{ID: "listed"},
			}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/jobs/"+escapedID:
			writeJobResponse(t, w, "fetched", false)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.EscapedPath(), "/v1/jobs/"+escapedID+"/"):
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("action %s content type = %q, want application/json", r.URL.EscapedPath(), got)
			}
			var empty map[string]any
			if err := json.NewDecoder(r.Body).Decode(&empty); err != nil || len(empty) != 0 {
				t.Errorf("action %s body = %#v err=%v, want {}", r.URL.EscapedPath(), empty, err)
			}
			action := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/jobs/"+escapedID+"/")
			switch action {
			case "run", "pause", "resume", "cancel":
				writeJobResponse(t, w, action, action == "run" || action == "resume")
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := New(server.URL)
	ctx := context.Background()

	create := gatewayapi.CreateJobRequest{
		Goal:            "Do bounded work.",
		Preset:          jobs.PresetResearch,
		Workers:         4,
		Route:           jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview", Thinking: "enabled", ReasoningEffort: "high"},
		DurationSeconds: 3600,
		Budget:          jobs.Budget{MaxCycles: 8, MaxAttempts: 64, MaxModelCalls: 64, MaxTokens: 100_000},
		Authority:       jobs.DenyAllAuthority(),
		AutoStart:       true,
	}
	created, err := client.CreateJob(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if created.State.Spec.ID != "created" || !created.Active {
		t.Fatalf("created = %#v", created)
	}
	if gotCreate.Route != create.Route || gotCreate.Workers != 4 || !gotCreate.AutoStart {
		t.Fatalf("create request = %#v, want %#v", gotCreate, create)
	}

	listed, err := client.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "listed" {
		t.Fatalf("listed = %#v", listed)
	}

	fetched, err := client.GetJob(ctx, rawID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.State.Spec.ID != "fetched" {
		t.Fatalf("fetched = %#v", fetched)
	}

	actions := []struct {
		name string
		call func(context.Context, string) (gatewayapi.JobResponse, error)
	}{
		{name: "run", call: client.RunJob},
		{name: "pause", call: client.PauseJob},
		{name: "resume", call: client.ResumeJob},
		{name: "cancel", call: client.CancelJob},
	}
	for _, action := range actions {
		response, err := action.call(ctx, rawID)
		if err != nil {
			t.Fatalf("%s: %v", action.name, err)
		}
		if response.State.Spec.ID != action.name {
			t.Fatalf("%s response = %#v", action.name, response)
		}
	}

	for _, action := range []string{"run", "pause", "resume", "cancel"} {
		want := http.MethodPost + " /v1/jobs/" + escapedID + "/" + action
		if !containsString(requests, want) {
			t.Fatalf("requests %#v missing %q", requests, want)
		}
	}
	if !containsString(requests, http.MethodGet+" /v1/jobs/"+escapedID) {
		t.Fatalf("requests %#v missing escaped get path", requests)
	}
}

func TestJobClientPropagatesStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"job is already active"}`)
	}))
	t.Cleanup(server.Close)

	_, err := New(server.URL).RunJob(context.Background(), "job conflict")
	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("RunJob() error = %T %v, want StatusError", err, err)
	}
	if status.Method != http.MethodPost ||
		status.Path != "/v1/jobs/job%20conflict/run" ||
		status.StatusCode != http.StatusConflict ||
		!strings.Contains(status.Body, "already active") {
		t.Fatalf("status error = %#v", status)
	}
}

func TestCreateJobRetriesAmbiguousLostResponseWithSameClientID(t *testing.T) {
	const jobID = "j-client-lost-response"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request gatewayapi.CreateJobRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.JobID != jobID {
			t.Errorf("request %d job_id=%q want=%q", requests, request.JobID, jobID)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if requests == 1 {
			// The durable create was accepted, but the acknowledgement body was
			// truncated before the client could decode it.
			_, _ = io.WriteString(w, `{"state":`)
			return
		}
		writeJobResponse(t, w, jobID, true)
	}))
	t.Cleanup(server.Close)

	request := gatewayapi.CreateJobRequest{
		JobID: jobID, Goal: "Do bounded idempotent work.", Preset: jobs.PresetResearch, Workers: 1,
		Route: jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview"}, DurationSeconds: 3600,
		Budget:    jobs.Budget{MaxCycles: 2, MaxAttempts: 16, MaxModelCalls: 16, MaxTokens: 100_000},
		Authority: jobs.DenyAllAuthority(), AutoStart: true,
	}
	created, err := New(server.URL).CreateJob(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || created.State.Spec.ID != jobID {
		t.Fatalf("requests=%d response=%#v", requests, created)
	}
}

func TestCreateJobWithoutClientIDDoesNotReplayAmbiguousPost(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"state":`)
	}))
	t.Cleanup(server.Close)

	request := gatewayapi.CreateJobRequest{
		Goal: "Keep legacy server-generated creates strict.", Preset: jobs.PresetGeneral, Workers: 1,
		Route: jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview"}, DurationSeconds: 3600,
		Budget:    jobs.Budget{MaxCycles: 2, MaxAttempts: 16, MaxModelCalls: 16, MaxTokens: 100_000},
		Authority: jobs.DenyAllAuthority(),
	}
	if _, err := New(server.URL).CreateJob(context.Background(), request); err == nil {
		t.Fatal("CreateJob() succeeded with a truncated response")
	}
	if requests != 1 {
		t.Fatalf("requests=%d, want no unsafe replay without job_id", requests)
	}
}

func TestCreateJobRejectsInvalidRequestBeforeSending(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, err := New(server.URL).CreateJob(context.Background(), gatewayapi.CreateJobRequest{
		Goal: "unbounded request",
	})
	if err == nil || !strings.Contains(err.Error(), "create job request") {
		t.Fatalf("CreateJob() error = %v, want local validation error", err)
	}
	if called {
		t.Fatal("invalid create request reached gateway")
	}
}

func TestJobClientRejectsOversizedJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jobs":[],"padding":"`)
		_, _ = io.WriteString(w, strings.Repeat("x", int(maxJSONResponseBodyBytes)))
		_, _ = io.WriteString(w, `"}`)
	}))
	t.Cleanup(server.Close)

	_, err := New(server.URL).ListJobs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("ListJobs() error = %v, want bounded response rejection", err)
	}
}

func writeJobResponse(t *testing.T, w http.ResponseWriter, id string, active bool) {
	t.Helper()
	writeJSON(t, w, gatewayapi.JobResponse{
		State:  jobs.JobState{Spec: jobs.JobSpec{ID: id}},
		Active: active,
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
