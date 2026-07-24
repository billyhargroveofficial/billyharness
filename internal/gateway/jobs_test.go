package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobservice"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestCompileJobSpecClampsAuthorityAndIsolatesWriter(t *testing.T) {
	serverAuthority := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"edit", "read"},
		ReadRoots:    []string{"/workspace"},
		WriteRoots:   []string{"/workspace"},
		NetworkHosts: []string{"docs.example.com"},
		Providers:    []string{"other", "qwen"},
	}
	server := &Server{jobAuthority: cloneValidJobAuthority(serverAuthority)}
	request := validGatewayCreateJobRequest()
	request.Preset = jobs.PresetCoding
	request.Workers = 2
	request.Authority = jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"edit", "read", "unavailable"},
		ReadRoots:    []string{"/outside", "/workspace/project"},
		WriteRoots:   []string{"/outside", "/workspace/project"},
		NetworkHosts: []string{"docs.example.com", "other.example.com"},
		Providers:    []string{"qwen", "rogue"},
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	spec, err := server.compileJobSpec(request, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("compiled spec invalid: %v", err)
	}
	if err := jobstore.ValidatePortableID(spec.ID); err != nil || !strings.HasPrefix(spec.ID, "j-") {
		t.Fatalf("generated id = %q err=%v, want portable j- prefix", spec.ID, err)
	}
	if spec.Route != request.Route || !spec.AdmittedAt.Equal(now) || spec.AdmittedAt.Location() != time.UTC || !spec.Deadline.Equal(now.Add(time.Hour)) {
		t.Fatalf("route/admitted_at/deadline = %#v %s %s", spec.Route, spec.AdmittedAt, spec.Deadline)
	}
	if got := spec.Authority; !equalAuthority(got, jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"edit", "read"},
		ReadRoots:    []string{"/workspace/project"},
		WriteRoots:   []string{"/workspace/project"},
		NetworkHosts: []string{"docs.example.com"},
		Providers:    []string{"qwen"},
	}) {
		t.Fatalf("clamped job authority = %#v", got)
	}
	writerCount := 0
	for _, role := range spec.Roles {
		if !equalStrings(role.Authority.Providers, []string{"qwen"}) {
			t.Fatalf("role %q providers = %v", role.ID, role.Authority.Providers)
		}
		if role.Writer {
			writerCount++
			if !equalStrings(role.Authority.WriteRoots, []string{"/workspace/project"}) {
				t.Fatalf("writer %q write roots = %v", role.ID, role.Authority.WriteRoots)
			}
			continue
		}
		if len(role.Authority.WriteRoots) != 0 {
			t.Fatalf("non-writer %q inherited mutation roots %v", role.ID, role.Authority.WriteRoots)
		}
	}
	if writerCount != 1 {
		t.Fatalf("writer count = %d, want 1", writerCount)
	}

	// The stored authority must not alias constructor/request slices.
	serverAuthority.Tools[0] = "mutated"
	request.Authority.ReadRoots[1] = "/mutated"
	if spec.Authority.Tools[0] != "edit" || spec.Authority.ReadRoots[0] != "/workspace/project" {
		t.Fatalf("compiled authority aliases caller input: %#v", spec.Authority)
	}
}

func TestCompileJobSpecFailsClosedWhenRouteProviderDoesNotSurviveClamp(t *testing.T) {
	request := validGatewayCreateJobRequest()
	tests := []struct {
		name      string
		server    jobs.Authority
		authority jobs.Authority
	}{
		{name: "zero server authority", server: jobs.Authority{}, authority: request.Authority},
		{name: "server denies provider", server: providerAuthority("other"), authority: request.Authority},
		{name: "request denies provider", server: providerAuthority("qwen"), authority: jobs.DenyAllAuthority()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{jobAuthority: cloneValidJobAuthority(test.server)}
			request.Authority = test.authority
			_, err := server.compileJobSpec(request, time.Now().UTC())
			var admission *jobAdmissionError
			if !errors.As(err, &admission) || admission.status != http.StatusForbidden {
				t.Fatalf("compile error = %T %v, want forbidden admission", err, err)
			}
		})
	}
}

func TestCompileJobSpecCanonicalizesRouteAliasesAndProviderAuthority(t *testing.T) {
	server := &Server{jobAuthority: cloneValidJobAuthority(providerAuthority("qwen-cloud"))}
	request := validGatewayCreateJobRequest()
	request.Route.ProviderID = "qn"
	request.Route.ModelID = "qwen max"
	request.Authority = providerAuthority("qwencloud")

	spec, err := server.compileJobSpec(request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	want := jobs.ExecutionRoute{
		ProviderID:      "qwen",
		ModelID:         "qwen3.8-max-preview",
		Thinking:        request.Route.Thinking,
		ReasoningEffort: request.Route.ReasoningEffort,
	}
	if spec.Route != want || !equalStrings(spec.Authority.Providers, []string{"qwen"}) {
		t.Fatalf("canonical route/authority = %#v %#v, want %#v [qwen]", spec.Route, spec.Authority, want)
	}
	for _, role := range spec.Roles {
		if !equalStrings(role.Authority.Providers, []string{"qwen"}) {
			t.Fatalf("role %q provider grants = %v", role.ID, role.Authority.Providers)
		}
	}
}

func TestCompileJobSpecPersistsAbsoluteRuntimeFloorAndCadence(t *testing.T) {
	server := &Server{jobAuthority: cloneValidJobAuthority(providerAuthority("qwen"))}
	request := validGatewayCreateJobRequest()
	request.MinRuntimeSeconds = 30 * 60
	request.CadenceSeconds = 5 * 60
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.FixedZone("admission", 3*60*60))

	spec, err := server.compileJobSpec(request, now)
	if err != nil {
		t.Fatal(err)
	}
	if !spec.NotBeforeComplete.Equal(now.UTC().Add(30*time.Minute)) || spec.NotBeforeComplete.Location() != time.UTC {
		t.Fatalf("not_before_complete = %s", spec.NotBeforeComplete)
	}
	if !spec.AdmittedAt.Equal(now.UTC()) || spec.AdmittedAt.Location() != time.UTC {
		t.Fatalf("admitted_at = %s, want UTC %s", spec.AdmittedAt, now.UTC())
	}
	if spec.CycleCadenceSeconds != 5*60 || !spec.Deadline.Equal(now.UTC().Add(time.Hour)) {
		t.Fatalf("schedule = floor:%s cadence:%d deadline:%s", spec.NotBeforeComplete, spec.CycleCadenceSeconds, spec.Deadline)
	}
}

func TestCompileJobSpecPreservesClientIDAndStableCreateHashAcrossAdmissionTimes(t *testing.T) {
	server := &Server{jobAuthority: cloneValidJobAuthority(providerAuthority("qwen"))}
	request := validGatewayCreateJobRequest()
	request.JobID = "j-client-stable-create"
	first, err := server.compileJobSpec(request, time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.compileJobSpec(request, time.Date(2026, 7, 24, 12, 0, 1, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != request.JobID || second.ID != request.JobID || first.CreateRequestHash == "" ||
		first.CreateRequestHash != second.CreateRequestHash {
		t.Fatalf("compiled identities/hash = %#v %#v", first, second)
	}
	if first.Deadline.Equal(second.Deadline) {
		t.Fatalf("relative admission deadlines unexpectedly equal: %s", first.Deadline)
	}
	if first.AdmittedAt.Equal(second.AdmittedAt) || !first.AdmittedAt.Equal(first.Deadline.Add(-time.Hour)) || !second.AdmittedAt.Equal(second.Deadline.Add(-time.Hour)) {
		t.Fatalf("admission times/deadlines = %s/%s and %s/%s", first.AdmittedAt, first.Deadline, second.AdmittedAt, second.Deadline)
	}
}

func TestCompileJobSpecRejectsMismatchedKnownModelProvider(t *testing.T) {
	server := &Server{jobAuthority: cloneValidJobAuthority(jobs.Authority{
		Mode:      jobs.AuthorityModeAllowList,
		Providers: []string{"qwen", "openai-codex"},
	})}
	request := validGatewayCreateJobRequest()
	request.Route.ModelID = "gpt-5.5"
	request.Authority.Providers = []string{"qwen", "openai-codex"}

	_, err := server.compileJobSpec(request, time.Now().UTC())
	var admission *jobAdmissionError
	if !errors.As(err, &admission) || admission.status != http.StatusBadRequest || !strings.Contains(admission.message, "provider/model conflict") {
		t.Fatalf("compile error = %T %v, want provider/model bad request", err, err)
	}
}

func TestCreateJobRejectsUnsupportedRouteCapabilitiesBeforeDurableCreate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gatewayapi.CreateJobRequest)
		want   string
	}{
		{
			name: "unsupported reasoning",
			mutate: func(request *gatewayapi.CreateJobRequest) {
				request.Route.ReasoningEffort = "warp"
			},
			want: "unsupported reasoning_effort",
		},
		{
			name: "unknown built-in model",
			mutate: func(request *gatewayapi.CreateJobRequest) {
				request.Route.ModelID = "totally-unknown-model"
			},
			want: "capabilities are unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := newFakeJobController()
			server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
			request := validGatewayCreateJobRequest()
			request.AutoStart = false
			test.mutate(&request)
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			recorder := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("status=%d body=%s, want 400 containing %q", recorder.Code, recorder.Body.String(), test.want)
			}
			if len(controller.createdSpecs) != 0 || controller.startCalls != 0 {
				t.Fatalf("unsupported route reached controller: creates=%d starts=%d", len(controller.createdSpecs), controller.startCalls)
			}
		})
	}
}

func TestCompileJobSpecAllowsUnknownModelForCustomProvider(t *testing.T) {
	server := &Server{jobAuthority: cloneValidJobAuthority(providerAuthority("my-compatible"))}
	request := validGatewayCreateJobRequest()
	request.Route.ProviderID = "my-compatible"
	request.Route.ModelID = "private-model-v7"
	request.Authority = providerAuthority("my-compatible")
	if _, err := server.compileJobSpec(request, time.Now().UTC()); err != nil {
		t.Fatalf("custom provider unknown model: %v", err)
	}
}

func TestJobRoutesCreateListGetAndActions(t *testing.T) {
	controller := newFakeJobController()
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	request := validGatewayCreateJobRequest()
	request.AutoStart = false
	body, _ := json.Marshal(request)

	createdRecorder := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if createdRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createdRecorder.Code, createdRecorder.Body.String())
	}
	created := decodeJobResponse(t, createdRecorder)
	if created.Active || created.State.Status != jobs.JobStatusQueued || created.State.Spec.Route != request.Route || created.State.Spec.AdmittedAt.IsZero() {
		t.Fatalf("created = %#v", created)
	}
	admittedAt := created.State.Spec.AdmittedAt
	if admittedAt.Location() != time.UTC || !admittedAt.Before(created.State.Spec.Deadline) {
		t.Fatalf("created admitted_at/deadline = %s/%s", admittedAt, created.State.Spec.Deadline)
	}
	jobID := created.State.Spec.ID
	if controller.startCalls != 0 {
		t.Fatalf("auto_start=false start calls = %d", controller.startCalls)
	}

	listedRecorder := serveJobRequest(server, http.MethodGet, "/v1/jobs", nil, "")
	if listedRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listedRecorder.Code, listedRecorder.Body.String())
	}
	var listed gatewayapi.JobListResponse
	if err := json.Unmarshal(listedRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Jobs) != 1 || listed.Jobs[0].ID != jobID || !listed.Jobs[0].AdmittedAt.Equal(admittedAt) {
		t.Fatalf("listed = %#v", listed)
	}

	getRecorder := serveJobRequest(server, http.MethodGet, "/v1/jobs/"+jobID, nil, "")
	shown := decodeJobResponse(t, getRecorder)
	if getRecorder.Code != http.StatusOK || shown.State.Spec.ID != jobID || !shown.State.Spec.AdmittedAt.Equal(admittedAt) {
		t.Fatalf("get status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}

	actions := []struct {
		name   string
		status jobs.JobStatus
		active bool
	}{
		{name: "run", status: jobs.JobStatusRunning, active: true},
		{name: "pause", status: jobs.JobStatusPaused},
		{name: "resume", status: jobs.JobStatusRunning, active: true},
		{name: "cancel", status: jobs.JobStatusCancelled},
	}
	for _, action := range actions {
		recorder := serveJobRequest(server, http.MethodPost, "/v1/jobs/"+jobID+"/"+action.name, []byte(`{}`), "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", action.name, recorder.Code, recorder.Body.String())
		}
		response := decodeJobResponse(t, recorder)
		if response.State.Status != action.status || response.Active != action.active {
			t.Fatalf("%s response = %#v", action.name, response)
		}
	}
}

func TestCreateJobAutoStartAndDuplicateRunAreSuccessful(t *testing.T) {
	controller := newFakeJobController()
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	request := validGatewayCreateJobRequest()
	body, _ := json.Marshal(request)
	recorder := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeJobResponse(t, recorder)
	if !response.Active || response.State.Status != jobs.JobStatusRunning || controller.startCalls != 1 {
		t.Fatalf("auto-start response=%#v calls=%d", response, controller.startCalls)
	}

	// Manager's duplicate active Start is an idempotent success. The gateway
	// preserves that result instead of manufacturing a conflict.
	duplicate := serveJobRequest(server, http.MethodPost, "/v1/jobs/"+response.State.Spec.ID+"/run", []byte(`{}`), "")
	if duplicate.Code != http.StatusOK || !decodeJobResponse(t, duplicate).Active || controller.startCalls != 2 {
		t.Fatalf("duplicate run status=%d body=%s calls=%d", duplicate.Code, duplicate.Body.String(), controller.startCalls)
	}
}

func TestCreateJobClientIDRetryReturnsWinnerAndMismatchConflicts(t *testing.T) {
	controller := newFakeJobController()
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	request := validGatewayCreateJobRequest()
	request.JobID = "j-idempotent-http-create"
	request.AutoStart = false
	body, _ := json.Marshal(request)

	for attempt := 0; attempt < 2; attempt++ {
		recorder := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
		if recorder.Code != http.StatusCreated {
			t.Fatalf("retry %d status=%d body=%s", attempt, recorder.Code, recorder.Body.String())
		}
		if got := decodeJobResponse(t, recorder).State.Spec.ID; got != request.JobID {
			t.Fatalf("retry %d job id=%q want=%q", attempt, got, request.JobID)
		}
	}
	if len(controller.createdSpecs) != 1 {
		t.Fatalf("durable create count = %d, want 1", len(controller.createdSpecs))
	}

	request.Goal = "reuse the same id for a different request"
	body, _ = json.Marshal(request)
	conflict := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "different create request") {
		t.Fatalf("mismatch status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestCreateJobIdenticalRetryReturnsWinnerAfterAbsoluteDeadlineExpires(t *testing.T) {
	controller := newFakeJobController()
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	request := validGatewayCreateJobRequest()
	request.JobID = "j-idempotent-expired-deadline"
	request.AutoStart = false
	request.DurationSeconds = 0
	expired := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	request.Deadline = &expired

	// Seed the durable winner using the earlier admission instant at which the
	// exact absolute deadline request was valid.
	spec, err := server.compileJobSpec(request, expired.Add(-time.Hour))
	if err != nil {
		t.Fatalf("compile historical create: %v", err)
	}
	if _, err := controller.CreateIdempotent(context.Background(), spec); err != nil {
		t.Fatalf("seed durable winner: %v", err)
	}
	if _, err := server.compileJobSpec(request, time.Now().UTC()); err == nil {
		t.Fatal("normal admission unexpectedly accepted expired absolute deadline")
	}
	body, _ := json.Marshal(request)

	retry := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if retry.Code != http.StatusCreated {
		t.Fatalf("identical expired retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	if got := decodeJobResponse(t, retry).State.Spec.ID; got != request.JobID {
		t.Fatalf("identical expired retry job=%q want=%q", got, request.JobID)
	}
	if len(controller.createdSpecs) != 1 {
		t.Fatalf("identical expired retry created %d durable jobs, want 1", len(controller.createdSpecs))
	}

	request.Goal = "a different expired request must conflict with the durable winner"
	body, _ = json.Marshal(request)
	mismatch := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("expired mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}

	request.JobID = "j-new-expired-deadline"
	body, _ = json.Marshal(request)
	fresh := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if fresh.Code != http.StatusBadRequest {
		t.Fatalf("new expired create status=%d body=%s", fresh.Code, fresh.Body.String())
	}
}

func TestCreateJobAutoStartRetryReturnsAlreadyTerminalWinner(t *testing.T) {
	controller := newFakeJobController()
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	request := validGatewayCreateJobRequest()
	request.JobID = "j-idempotent-terminal"
	body, _ := json.Marshal(request)
	first := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	view := controller.views[request.JobID]
	view.State.Status = jobs.JobStatusCompleted
	view.State.TerminalReason = jobs.TerminalReasonSuccess
	view.Active = false
	controller.views[request.JobID] = view
	startCalls := controller.startCalls

	retry := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "")
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	response := decodeJobResponse(t, retry)
	if response.State.Status != jobs.JobStatusCompleted || controller.startCalls != startCalls {
		t.Fatalf("terminal retry response=%#v start_calls=%d want=%d", response, controller.startCalls, startCalls)
	}
}

func TestJobRoutesMapNotFoundTerminalConflictUnavailableAndInternalErrors(t *testing.T) {
	tests := []struct {
		name       string
		controller *fakeJobController
		path       string
		want       int
	}{
		{name: "not found", controller: fakeControllerWithError("get", fmt.Errorf("wrapped: %w", jobstore.ErrNotFound)), path: "/v1/jobs/j-missing", want: http.StatusNotFound},
		{name: "terminal run", controller: fakeControllerWithError("start", fmt.Errorf("%w: completed", jobservice.ErrNotStartable)), path: "/v1/jobs/j-terminal/run", want: http.StatusConflict},
		{name: "terminal pause", controller: fakeControllerWithError("pause", fmt.Errorf("%w: completed", jobservice.ErrPauseFailed)), path: "/v1/jobs/j-terminal/pause", want: http.StatusConflict},
		{name: "resume still draining", controller: fakeControllerWithError("resume", fmt.Errorf("%w: runner active", jobservice.ErrNotControllable)), path: "/v1/jobs/j-draining/resume", want: http.StatusConflict},
		{name: "closed", controller: fakeControllerWithError("resume", jobservice.ErrClosed), path: "/v1/jobs/j-closed/resume", want: http.StatusServiceUnavailable},
		{name: "corrupt", controller: fakeControllerWithError("cancel", jobstore.ErrCorrupt), path: "/v1/jobs/j-corrupt/cancel", want: http.StatusConflict},
		{name: "internal", controller: fakeControllerWithError("cancel", errors.New("provider exploded")), path: "/v1/jobs/j-broken/cancel", want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newJobRouteTestServer(t, test.controller, providerAuthority("qwen"), ServerOptions{})
			method := http.MethodPost
			body := []byte(`{}`)
			if !strings.Contains(test.path, "/run") && !strings.Contains(test.path, "/pause") &&
				!strings.Contains(test.path, "/resume") && !strings.Contains(test.path, "/cancel") {
				method = http.MethodGet
				body = nil
			}
			recorder := serveJobRequest(server, method, test.path, body, "")
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}

	unavailable := newJobRouteTestServer(t, nil, providerAuthority("qwen"), ServerOptions{})
	recorder := serveJobRequest(unavailable, http.MethodGet, "/v1/jobs", nil, "")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil controller status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestJobRoutesRejectMalformedDuplicateTrailingOversizedBodiesAndIDs(t *testing.T) {
	controller := newFakeJobController()
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	valid := validGatewayCreateJobRequest()
	validBody, _ := json.Marshal(valid)
	duplicateGoal := append([]byte(`{"goal":"first",`), bytes.TrimPrefix(validBody, []byte(`{`))...)
	nestedDuplicate := bytes.Replace(validBody,
		[]byte(`"provider_id":"qwen"`),
		[]byte(`"provider_id":"qwen","provider_id":"qwen"`), 1)
	tests := []struct {
		name string
		body []byte
		want int
	}{
		{name: "empty", body: nil, want: http.StatusBadRequest},
		{name: "syntax", body: []byte(`{`), want: http.StatusBadRequest},
		{name: "array", body: []byte(`[]`), want: http.StatusBadRequest},
		{name: "unknown", body: []byte(`{"unknown":true}`), want: http.StatusBadRequest},
		{name: "noncanonical field", body: bytes.Replace(validBody, []byte(`"goal"`), []byte(`"Goal"`), 1), want: http.StatusBadRequest},
		{name: "duplicate", body: duplicateGoal, want: http.StatusBadRequest},
		{name: "nested duplicate", body: nestedDuplicate, want: http.StatusBadRequest},
		{name: "nested null", body: bytes.Replace(validBody, mustJSONField(t, validBody, "route"), []byte(`"route":null`), 1), want: http.StatusBadRequest},
		{name: "trailing", body: append(append([]byte(nil), validBody...), []byte(` {}`)...), want: http.StatusBadRequest},
		{name: "oversized", body: bytes.Repeat([]byte("x"), int(maxCreateJobRequestBodyBytes+1)), want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveJobRequest(server, http.MethodPost, "/v1/jobs", test.body, "")
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
	if len(controller.createdSpecs) != 0 {
		t.Fatalf("malformed requests reached controller: %d", len(controller.createdSpecs))
	}

	for _, path := range []string{"/v1/jobs/1bad", "/v1/jobs/%20", "/v1/jobs/a%2Fb"} {
		recorder := serveJobRequest(server, http.MethodGet, path, nil, "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid id %q status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}

	actionTests := []struct {
		body []byte
		want int
	}{
		{body: []byte(`{"force":true}`), want: http.StatusBadRequest},
		{body: []byte(`{} {}`), want: http.StatusBadRequest},
		{body: bytes.Repeat([]byte("x"), int(maxJobActionRequestBodyBytes+1)), want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range actionTests {
		recorder := serveJobRequest(server, http.MethodPost, "/v1/jobs/j-valid/run", test.body, "")
		if recorder.Code != test.want {
			t.Fatalf("action body status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), test.want)
		}
	}
}

func TestJobRoutesUseGatewayAuthAndContentTypeBoundary(t *testing.T) {
	controller := newFakeJobController()
	extra := ServerOptions{AuthToken: "gateway-secret", RequireMutationAuth: true}
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), extra)
	body, _ := json.Marshal(validGatewayCreateJobRequest())

	missingType := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	missingType.Header.Set("Authorization", "Bearer gateway-secret")
	missingTypeRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingTypeRecorder, missingType)
	if missingTypeRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content type status=%d body=%s", missingTypeRecorder.Code, missingTypeRecorder.Body.String())
	}

	missingAuth := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	missingAuth.Header.Set("Content-Type", "application/json")
	missingAuthRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingAuthRecorder, missingAuth)
	if missingAuthRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", missingAuthRecorder.Code, missingAuthRecorder.Body.String())
	}

	authorized := serveJobRequest(server, http.MethodPost, "/v1/jobs", body, "gateway-secret")
	if authorized.Code != http.StatusCreated {
		t.Fatalf("authorized status=%d body=%s", authorized.Code, authorized.Body.String())
	}

	readWithoutBearer := serveJobRequest(server, http.MethodGet, "/v1/jobs", nil, "")
	if readWithoutBearer.Code != http.StatusUnauthorized {
		t.Fatalf("read without bearer status=%d body=%s", readWithoutBearer.Code, readWithoutBearer.Body.String())
	}
}

func TestJobResponsesAndErrorsRedactSecrets(t *testing.T) {
	const secret = "sk-jobroutes-secret-1234567890"
	controller := newFakeJobController()
	controller.getErr = fmt.Errorf("wrapped %w: api_key=%s", jobservice.ErrNotStartable, secret)
	server := newJobRouteTestServer(t, controller, providerAuthority("qwen"), ServerOptions{})
	recorder := serveJobRequest(server, http.MethodGet, "/v1/jobs/j-secret", nil, "")
	if recorder.Code != http.StatusConflict || strings.Contains(recorder.Body.String(), secret) || !strings.Contains(recorder.Body.String(), "[redacted]") {
		t.Fatalf("known error redaction status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	controller.getErr = nil
	controller.views["j-secret"] = jobservice.View{
		State:     jobs.JobState{Spec: jobs.JobSpec{ID: "j-secret"}},
		LastError: "Authorization: Bearer " + secret,
	}
	recorder = serveJobRequest(server, http.MethodGet, "/v1/jobs/j-secret", nil, "")
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), secret) || !strings.Contains(recorder.Body.String(), "[redacted]") {
		t.Fatalf("view redaction status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestJobRouteDocsExposeExpectedAuthClasses(t *testing.T) {
	docs := (&Server{}).RouteDocs()
	want := map[string]string{
		"GET /v1/jobs":              "local-read",
		"POST /v1/jobs":             "bearer-mutation",
		"GET /v1/jobs/{id}":         "local-read",
		"POST /v1/jobs/{id}/run":    "bearer-mutation",
		"POST /v1/jobs/{id}/pause":  "bearer-mutation",
		"POST /v1/jobs/{id}/resume": "bearer-mutation",
		"POST /v1/jobs/{id}/cancel": "bearer-mutation",
	}
	for _, doc := range docs {
		key := doc.Method + " " + doc.Pattern
		if auth, ok := want[key]; ok {
			if doc.AuthClass != auth {
				t.Fatalf("%s auth=%s want=%s", key, doc.AuthClass, auth)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing job route docs: %v", want)
	}
}

type fakeJobController struct {
	createdSpecs []jobs.JobSpec
	views        map[string]jobservice.View
	startCalls   int

	createErr error
	getErr    error
	listErr   error
	startErr  error
	pauseErr  error
	resumeErr error
	cancelErr error
}

func newFakeJobController() *fakeJobController {
	return &fakeJobController{views: make(map[string]jobservice.View)}
}

func fakeControllerWithError(operation string, err error) *fakeJobController {
	controller := newFakeJobController()
	switch operation {
	case "create":
		controller.createErr = err
	case "get":
		controller.getErr = err
	case "list":
		controller.listErr = err
	case "start":
		controller.startErr = err
	case "pause":
		controller.pauseErr = err
	case "resume":
		controller.resumeErr = err
	case "cancel":
		controller.cancelErr = err
	}
	return controller
}

func (f *fakeJobController) Create(_ context.Context, spec jobs.JobSpec) (jobservice.View, error) {
	if f.createErr != nil {
		return jobservice.View{}, f.createErr
	}
	f.createdSpecs = append(f.createdSpecs, spec)
	view := jobservice.View{State: jobs.JobState{Spec: spec, Status: jobs.JobStatusQueued}}
	f.views[spec.ID] = view
	return view, nil
}

func (f *fakeJobController) CreateIdempotent(ctx context.Context, spec jobs.JobSpec) (jobservice.View, error) {
	if f.createErr != nil {
		return jobservice.View{}, f.createErr
	}
	if existing, ok := f.views[spec.ID]; ok {
		if existing.State.Spec.CreateRequestHash == spec.CreateRequestHash {
			return existing, nil
		}
		return jobservice.View{}, &jobservice.CreateConflictError{JobID: spec.ID}
	}
	return f.Create(ctx, spec)
}

func (f *fakeJobController) Get(_ context.Context, jobID string) (jobservice.View, error) {
	if f.getErr != nil {
		return jobservice.View{}, f.getErr
	}
	view, ok := f.views[jobID]
	if !ok {
		return jobservice.View{}, jobstore.ErrNotFound
	}
	return view, nil
}

func (f *fakeJobController) List(_ context.Context) ([]jobservice.Summary, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]jobservice.Summary, 0, len(f.views))
	for id, view := range f.views {
		out = append(out, jobservice.Summary{Job: jobstore.JobSummary{
			ID:             id,
			Goal:           view.State.Spec.Goal,
			Preset:         view.State.Spec.Preset,
			Status:         view.State.Status,
			TerminalReason: view.State.TerminalReason,
			Revision:       view.State.Revision,
			Cycle:          view.State.Cycle,
			Usage:          view.State.Usage,
			AdmittedAt:     view.State.Spec.AdmittedAt,
			Deadline:       view.State.Spec.Deadline,
		}})
	}
	return out, nil
}

func (f *fakeJobController) Start(_ context.Context, jobID string) (jobservice.View, error) {
	f.startCalls++
	if f.startErr != nil {
		return jobservice.View{}, f.startErr
	}
	view := f.viewForAction(jobID)
	view.State.Status = jobs.JobStatusRunning
	view.Active = true
	f.views[jobID] = view
	return view, nil
}

func (f *fakeJobController) Pause(_ context.Context, jobID string) (jobservice.View, error) {
	if f.pauseErr != nil {
		return jobservice.View{}, f.pauseErr
	}
	view := f.viewForAction(jobID)
	view.State.Status = jobs.JobStatusPaused
	view.Active = false
	f.views[jobID] = view
	return view, nil
}

func (f *fakeJobController) Resume(_ context.Context, jobID string) (jobservice.View, error) {
	if f.resumeErr != nil {
		return jobservice.View{}, f.resumeErr
	}
	view := f.viewForAction(jobID)
	view.State.Status = jobs.JobStatusRunning
	view.Active = true
	f.views[jobID] = view
	return view, nil
}

func (f *fakeJobController) Cancel(_ context.Context, jobID string) (jobservice.View, error) {
	if f.cancelErr != nil {
		return jobservice.View{}, f.cancelErr
	}
	view := f.viewForAction(jobID)
	view.State.Status = jobs.JobStatusCancelled
	view.Active = false
	f.views[jobID] = view
	return view, nil
}

func (f *fakeJobController) viewForAction(jobID string) jobservice.View {
	if view, ok := f.views[jobID]; ok {
		return view
	}
	return jobservice.View{State: jobs.JobState{Spec: jobs.JobSpec{ID: jobID}}}
}

func newJobRouteTestServer(t *testing.T, controller JobController, authority jobs.Authority, extra ServerOptions) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	extra.JobController = controller
	extra.JobAuthority = authority
	return NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), extra)
}

func serveJobRequest(server *Server, method, path string, body []byte, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil && method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func decodeJobResponse(t *testing.T, recorder *httptest.ResponseRecorder) gatewayapi.JobResponse {
	t.Helper()
	var response gatewayapi.JobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func validGatewayCreateJobRequest() gatewayapi.CreateJobRequest {
	return gatewayapi.CreateJobRequest{
		Goal:            "Investigate and verify a bounded question.",
		Preset:          jobs.PresetResearch,
		Workers:         2,
		Route:           jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview", Thinking: "enabled", ReasoningEffort: "high"},
		DurationSeconds: 3600,
		Budget:          jobs.Budget{MaxCycles: 8, MaxAttempts: 64, MaxModelCalls: 64, MaxTokens: 100_000},
		Authority:       providerAuthority("qwen"),
		AutoStart:       true,
	}
}

func providerAuthority(providerID string) jobs.Authority {
	return jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{providerID}}
}

func equalAuthority(left, right jobs.Authority) bool {
	return left.Mode == right.Mode &&
		equalStrings(left.Tools, right.Tools) &&
		equalStrings(left.ReadRoots, right.ReadRoots) &&
		equalStrings(left.WriteRoots, right.WriteRoots) &&
		equalStrings(left.NetworkHosts, right.NetworkHosts) &&
		equalStrings(left.Providers, right.Providers)
}

func equalStrings(left, right []string) bool {
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

func mustJSONField(t *testing.T, raw []byte, field string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	value, ok := object[field]
	if !ok {
		t.Fatalf("field %q missing", field)
	}
	return append(append([]byte(`"`+field+`":`), value...), nil...)
}

var _ JobController = (*fakeJobController)(nil)
var _ JobController = (*jobservice.Manager)(nil)
