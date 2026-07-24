package jobruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestCommandsDispatchAsynchronouslyAndReturnTypedMessages(t *testing.T) {
	fake := &fakeGateway{}
	client := WithGateway(fake)
	ctx := context.Background()

	listCmd := client.ListCmd(ctx)
	commands := []struct {
		action Action
		cmd    func() any
	}{
		{ActionShow, func() any { return client.ShowCmd(ctx, " job-1 ")() }},
		{ActionRun, func() any { return client.RunCmd(ctx, " job-1 ")() }},
		{ActionPause, func() any { return client.PauseCmd(ctx, " job-1 ")() }},
		{ActionResume, func() any { return client.ResumeCmd(ctx, " job-1 ")() }},
		{ActionCancel, func() any { return client.CancelCmd(ctx, " job-1 ")() }},
	}

	if calls := fake.callNames(); len(calls) != 0 {
		t.Fatalf("constructing commands made gateway calls: %v", calls)
	}

	listMessage, ok := listCmd().(ListResultMsg)
	if !ok {
		t.Fatalf("ListCmd() message = %T, want ListResultMsg", listCmd())
	}
	if listMessage.Err != nil || len(listMessage.Jobs) != 1 || listMessage.Jobs[0].ID != "listed" {
		t.Fatalf("list message = %#v", listMessage)
	}

	for _, command := range commands {
		message, ok := command.cmd().(JobResultMsg)
		if !ok {
			t.Fatalf("%s command returned an unexpected message", command.action)
		}
		if message.Err != nil || message.Action != command.action || message.JobID != "job-1" || message.Response.State.Spec.ID != "job-1" {
			t.Fatalf("%s message = %#v", command.action, message)
		}
	}

	wantCalls := []string{"list", "show:job-1", "run:job-1", "pause:job-1", "resume:job-1", "cancel:job-1"}
	if got := fake.callNames(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("gateway calls = %v, want %v", got, wantCalls)
	}
}

func TestCreateCmdGeneratesStableIDAndCapturesDeepSnapshot(t *testing.T) {
	fake := &fakeGateway{}
	client := WithGateway(fake)
	deadline := time.Now().UTC().Add(time.Hour)
	request := gatewayapi.CreateJobRequest{
		Goal:     "research the corpus",
		Preset:   jobs.PresetResearch,
		Workers:  4,
		Deadline: &deadline,
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen",
			ModelID:    "qwen3.8-max-preview",
		},
		Budget: jobs.Budget{MaxCycles: 2, MaxAttempts: 16, MaxModelCalls: 16, MaxTokens: 100_000},
		Authority: jobs.Authority{
			Mode:         jobs.AuthorityModeAllowList,
			Tools:        []string{"fs_read_file"},
			ReadRoots:    []string{"/notes"},
			NetworkHosts: []string{"example.com"},
			Providers:    []string{"qwen"},
		},
		AutoStart: true,
	}

	cmd := client.CreateCmd(context.Background(), request)
	request.Goal = "mutated"
	request.Deadline = nil
	request.Authority.Tools[0] = "shell"
	request.Authority.ReadRoots[0] = "/other"
	request.Authority.NetworkHosts[0] = "other.invalid"
	request.Authority.Providers[0] = "other"

	first := cmd().(JobResultMsg)
	second := cmd().(JobResultMsg)
	if first.Err != nil || second.Err != nil {
		t.Fatalf("create errors: first=%v second=%v", first.Err, second.Err)
	}
	if first.Action != ActionCreate || !strings.HasPrefix(first.JobID, "j-") || first.JobID != second.JobID {
		t.Fatalf("create ids/actions: first=%#v second=%#v", first, second)
	}

	created := fake.createRequests()
	if len(created) != 2 || created[0].JobID != first.JobID || created[1].JobID != first.JobID {
		t.Fatalf("create requests = %#v", created)
	}
	for _, got := range created {
		if got.Goal != "research the corpus" || got.Deadline == nil || !got.Deadline.Equal(deadline) ||
			!reflect.DeepEqual(got.Authority.Tools, []string{"fs_read_file"}) ||
			!reflect.DeepEqual(got.Authority.ReadRoots, []string{"/notes"}) ||
			!reflect.DeepEqual(got.Authority.NetworkHosts, []string{"example.com"}) ||
			!reflect.DeepEqual(got.Authority.Providers, []string{"qwen"}) {
			t.Fatalf("captured request changed with caller input: %#v", got)
		}
	}
}

func TestCommandsReturnValidationAndGatewayErrorsAsMessages(t *testing.T) {
	wantErr := errors.New("gateway unavailable")
	fake := &fakeGateway{err: wantErr}

	message := WithGateway(fake).RunCmd(context.Background(), "job-1")().(JobResultMsg)
	if !errors.Is(message.Err, wantErr) || message.Action != ActionRun || message.JobID != "job-1" {
		t.Fatalf("gateway error message = %#v", message)
	}
	if message := WithGateway(fake).ShowCmd(context.Background(), "  ")().(JobResultMsg); message.Err == nil || !strings.Contains(message.Err.Error(), "job id") {
		t.Fatalf("empty id message = %#v", message)
	}
	if message := (Client{}).ListCmd(context.Background())().(ListResultMsg); message.Err == nil || !strings.Contains(message.Err.Error(), "client is nil") {
		t.Fatalf("nil client message = %#v", message)
	}
	if message := WithGateway(fake).CancelCmd(nil, "job-1")().(JobResultMsg); message.Err == nil || !strings.Contains(message.Err.Error(), "context is nil") {
		t.Fatalf("nil context message = %#v", message)
	}
}

func TestNewUsesConcreteGatewayClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gatewayapi.JobListResponse{
			Jobs: []gatewayapi.JobSummaryResponse{{ID: "via-http"}},
		})
	}))
	t.Cleanup(server.Close)

	message := New(server.URL).ListCmd(context.Background())().(ListResultMsg)
	if message.Err != nil || len(message.Jobs) != 1 || message.Jobs[0].ID != "via-http" {
		t.Fatalf("ListCmd() = %#v", message)
	}
}

type fakeGateway struct {
	mu      sync.Mutex
	calls   []string
	creates []gatewayapi.CreateJobRequest
	err     error
}

func (f *fakeGateway) CreateJob(_ context.Context, request gatewayapi.CreateJobRequest) (gatewayapi.JobResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "create:"+request.JobID)
	f.creates = append(f.creates, request)
	return jobResponse(request.JobID), f.err
}

func (f *fakeGateway) ListJobs(context.Context) ([]gatewayapi.JobSummaryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "list")
	return []gatewayapi.JobSummaryResponse{{ID: "listed"}}, f.err
}

func (f *fakeGateway) GetJob(_ context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return f.record("show", jobID)
}

func (f *fakeGateway) RunJob(_ context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return f.record("run", jobID)
}

func (f *fakeGateway) PauseJob(_ context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return f.record("pause", jobID)
}

func (f *fakeGateway) ResumeJob(_ context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return f.record("resume", jobID)
}

func (f *fakeGateway) CancelJob(_ context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return f.record("cancel", jobID)
}

func (f *fakeGateway) record(action, jobID string) (gatewayapi.JobResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action+":"+jobID)
	return jobResponse(jobID), f.err
}

func (f *fakeGateway) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeGateway) createRequests() []gatewayapi.CreateJobRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gatewayapi.CreateJobRequest(nil), f.creates...)
}

func jobResponse(jobID string) gatewayapi.JobResponse {
	return gatewayapi.JobResponse{State: jobs.JobState{Spec: jobs.JobSpec{ID: jobID}}}
}
