package gatewayapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestCreateJobRequestResolveDeadline(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.FixedZone("test", 3*60*60))
	request := validCreateJobRequest()
	request.DurationSeconds = 6 * 60 * 60

	deadline, err := request.ResolveDeadline(now)
	if err != nil {
		t.Fatal(err)
	}
	want := now.UTC().Add(6 * time.Hour)
	if !deadline.Equal(want) || deadline.Location() != time.UTC {
		t.Fatalf("deadline = %s, want UTC %s", deadline, want)
	}

	absolute := now.UTC().Add(24 * time.Hour)
	request.DurationSeconds = 0
	request.Deadline = &absolute
	deadline, err = request.ResolveDeadline(now)
	if err != nil {
		t.Fatal(err)
	}
	if !deadline.Equal(absolute) {
		t.Fatalf("absolute deadline = %s, want %s", deadline, absolute)
	}
}

func TestCreateJobRequestResolveSchedule(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.FixedZone("test", 3*60*60))
	request := validCreateJobRequest()
	request.MinRuntimeSeconds = uint64((6 * time.Hour) / time.Second)
	request.CadenceSeconds = uint64(time.Hour / time.Second)

	deadline, notBefore, err := request.ResolveSchedule(now)
	if err != nil {
		t.Fatal(err)
	}
	if !deadline.Equal(now.UTC().Add(24*time.Hour)) || deadline.Location() != time.UTC {
		t.Fatalf("deadline = %s", deadline)
	}
	if !notBefore.Equal(now.UTC().Add(6*time.Hour)) || notBefore.Location() != time.UTC {
		t.Fatalf("not_before_complete = %s", notBefore)
	}
}

func TestCreateJobRequestRejectsBudgetsBelowPresetInvocationFloor(t *testing.T) {
	tests := []struct {
		name                string
		mutate              func(*CreateJobRequest)
		requiredCycles      uint64
		requiredInvocations uint64
	}{
		{
			name: "general four workers",
			mutate: func(request *CreateJobRequest) {
				request.Preset = jobs.PresetGeneral
				request.Workers = 4
			},
			requiredCycles:      1,
			requiredInvocations: 6,
		},
		{
			name: "coding includes both worker stages and writer",
			mutate: func(request *CreateJobRequest) {
				request.Preset = jobs.PresetCoding
				request.Workers = 4
				request.MinCycles = 2
			},
			requiredCycles:      2,
			requiredInvocations: 22,
		},
		{
			name: "runtime floor rounds up cadence intervals",
			mutate: func(request *CreateJobRequest) {
				request.Preset = jobs.PresetGeneral
				request.Workers = 4
				request.MinCycles = 2
				request.MinRuntimeSeconds = 10
				request.CadenceSeconds = 3
			},
			requiredCycles:      5,
			requiredInvocations: 30,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCreateJobRequest()
			test.mutate(&request)
			request.Budget = jobs.Budget{
				MaxCycles:     test.requiredCycles,
				MaxAttempts:   test.requiredInvocations,
				MaxModelCalls: test.requiredInvocations,
				MaxTokens:     test.requiredInvocations,
			}
			if err := request.Validate(); err != nil {
				t.Fatalf("Validate() at exact arithmetic lower bound: %v", err)
			}

			for _, dimension := range []struct {
				name   string
				mutate func(*jobs.Budget)
			}{
				{name: "max_attempts", mutate: func(budget *jobs.Budget) { budget.MaxAttempts-- }},
				{name: "max_model_calls", mutate: func(budget *jobs.Budget) { budget.MaxModelCalls-- }},
				{name: "max_tokens", mutate: func(budget *jobs.Budget) { budget.MaxTokens-- }},
			} {
				t.Run(dimension.name, func(t *testing.T) {
					tooSmall := request
					dimension.mutate(&tooSmall.Budget)
					err := tooSmall.Validate()
					if err == nil || !strings.Contains(err.Error(), "budget "+dimension.name) {
						t.Fatalf("Validate() error = %v, want %s lower-bound rejection", err, dimension.name)
					}
				})
			}
		})
	}
}

func TestCreateJobRequestRejectsUnboundedOrAmbiguousInput(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*CreateJobRequest)
		want   string
	}{
		{name: "invalid job id", mutate: func(r *CreateJobRequest) { r.JobID = "../escape" }, want: "job_id"},
		{name: "missing goal", mutate: func(r *CreateJobRequest) { r.Goal = " " }, want: "goal is required"},
		{name: "large goal", mutate: func(r *CreateJobRequest) { r.Goal = strings.Repeat("x", MaxJobGoalBytes+1) }, want: "goal exceeds"},
		{name: "unknown preset", mutate: func(r *CreateJobRequest) { r.Preset = "invented" }, want: "built-in preset"},
		{name: "too many workers", mutate: func(r *CreateJobRequest) { r.Workers = jobs.MaxWorkers + 1 }, want: "workers must be between"},
		{name: "missing route", mutate: func(r *CreateJobRequest) { r.Route = jobs.ExecutionRoute{} }, want: "route:"},
		{name: "no wall clock bound", mutate: func(r *CreateJobRequest) { r.DurationSeconds = 0 }, want: "exactly one"},
		{name: "two wall clock bounds", mutate: func(r *CreateJobRequest) {
			deadline := now.Add(time.Hour)
			r.Deadline = &deadline
		}, want: "exactly one"},
		{name: "duration too long", mutate: func(r *CreateJobRequest) { r.DurationSeconds = MaxJobDurationSeconds + 1 }, want: "duration_seconds"},
		{name: "minimum runtime too long", mutate: func(r *CreateJobRequest) { r.MinRuntimeSeconds = MaxJobDurationSeconds + 1 }, want: "min_runtime_seconds"},
		{name: "cadence too long", mutate: func(r *CreateJobRequest) { r.CadenceSeconds = MaxJobDurationSeconds + 1 }, want: "cadence_seconds"},
		{name: "minimum runtime needs cadence", mutate: func(r *CreateJobRequest) { r.MinRuntimeSeconds = 6 * 60 * 60 }, want: "cadence_seconds must be greater"},
		{name: "cadence needs multiple cycles", mutate: func(r *CreateJobRequest) {
			r.CadenceSeconds = 60
			r.Budget.MaxCycles = 1
		}, want: "max_cycles of at least 2"},
		{name: "cadence cannot reach runtime floor", mutate: func(r *CreateJobRequest) {
			r.MinRuntimeSeconds = 6 * 60 * 60
			r.CadenceSeconds = 60
		}, want: "cadence_seconds must be at least"},
		{name: "missing budget", mutate: func(r *CreateJobRequest) { r.Budget = jobs.Budget{} }, want: "budget:"},
		{name: "cycles too large", mutate: func(r *CreateJobRequest) { r.Budget.MaxCycles = MaxJobBudgetCycles + 1 }, want: "max_cycles"},
		{name: "attempts too large", mutate: func(r *CreateJobRequest) { r.Budget.MaxAttempts = MaxJobBudgetAttempts + 1 }, want: "max_attempts"},
		{name: "model calls too large", mutate: func(r *CreateJobRequest) { r.Budget.MaxModelCalls = MaxJobBudgetModelCalls + 1 }, want: "max_model_calls"},
		{name: "tokens too large", mutate: func(r *CreateJobRequest) { r.Budget.MaxTokens = MaxJobBudgetTokens + 1 }, want: "max_tokens"},
		{name: "invalid authority", mutate: func(r *CreateJobRequest) { r.Authority = jobs.Authority{} }, want: "authority:"},
		{name: "too many authority entries", mutate: func(r *CreateJobRequest) {
			providers := make([]string, MaxJobAuthorityEntries+1)
			for index := range providers {
				providers[index] = "provider-" + strings.Repeat("x", index+1)
			}
			r.Authority = jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: providers}
		}, want: "exceeds 128 entries"},
		{name: "authority entry too long", mutate: func(r *CreateJobRequest) {
			r.Authority = jobs.Authority{
				Mode:      jobs.AuthorityModeAllowList,
				Providers: []string{strings.Repeat("x", MaxJobAuthorityValueBytes+1)},
			}
		}, want: "entry exceeds"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCreateJobRequest()
			test.mutate(&request)
			if err := request.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}

	t.Run("past absolute deadline", func(t *testing.T) {
		request := validCreateJobRequest()
		past := now.Add(-time.Second)
		request.DurationSeconds = 0
		request.Deadline = &past
		if _, err := request.ResolveDeadline(now); err == nil || !strings.Contains(err.Error(), "future") {
			t.Fatalf("ResolveDeadline() error = %v, want future rejection", err)
		}
	})

	t.Run("missing admission clock", func(t *testing.T) {
		request := validCreateJobRequest()
		if _, err := request.ResolveDeadline(time.Time{}); err == nil || !strings.Contains(err.Error(), "admission time") {
			t.Fatalf("ResolveDeadline() error = %v, want admission clock rejection", err)
		}
	})

	t.Run("absolute deadline beyond ceiling", func(t *testing.T) {
		request := validCreateJobRequest()
		tooLate := now.Add(time.Duration(MaxJobDurationSeconds+1) * time.Second)
		request.DurationSeconds = 0
		request.Deadline = &tooLate
		if _, err := request.ResolveDeadline(now); err == nil || !strings.Contains(err.Error(), "within") {
			t.Fatalf("ResolveDeadline() error = %v, want horizon rejection", err)
		}
	})

	t.Run("runtime floor leaves execution buffer", func(t *testing.T) {
		request := validCreateJobRequest()
		request.DurationSeconds = 6 * 60 * 60
		request.MinRuntimeSeconds = 6 * 60 * 60
		request.CadenceSeconds = 6 * 60 * 60
		if _, _, err := request.ResolveSchedule(now); err == nil || !strings.Contains(err.Error(), "leave at least one second") {
			t.Fatalf("ResolveSchedule() error = %v, want execution buffer rejection", err)
		}
	})

	t.Run("cadence leaves execution buffer", func(t *testing.T) {
		request := validCreateJobRequest()
		request.DurationSeconds = 6 * 60 * 60
		request.CadenceSeconds = 6 * 60 * 60
		if _, _, err := request.ResolveSchedule(now); err == nil || !strings.Contains(err.Error(), "cadence_seconds") {
			t.Fatalf("ResolveSchedule() error = %v, want cadence buffer rejection", err)
		}
	})
}

func TestCreateJobRequestJSONContainsOnlyCredentialFreeRoute(t *testing.T) {
	request := validCreateJobRequest()
	request.JobID = "j-public-client-id"
	request.Route = jobs.ExecutionRoute{
		ProviderID:      "qwen",
		ModelID:         "qwen3.8-max-preview",
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}
	request.MinRuntimeSeconds = 6 * 60 * 60
	request.CadenceSeconds = 60 * 60
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`"job_id":"j-public-client-id"`,
		`"provider_id":"qwen"`,
		`"model_id":"qwen3.8-max-preview"`,
		`"thinking":"enabled"`,
		`"reasoning_effort":"high"`,
		`"min_runtime_seconds":21600`,
		`"cadence_seconds":3600`,
		`"auto_start":true`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("request JSON %s missing %s", text, want)
		}
	}
	for _, forbidden := range []string{"api_key", "access_token", "base_url", "endpoint"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("request JSON exposes forbidden binding field %q: %s", forbidden, text)
		}
	}
}

func validCreateJobRequest() CreateJobRequest {
	return CreateJobRequest{
		Goal:            "Investigate the bounded question and verify the result.",
		Preset:          jobs.PresetResearch,
		Workers:         4,
		Route:           jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview"},
		DurationSeconds: 24 * 60 * 60,
		Budget: jobs.Budget{
			MaxCycles:     8,
			MaxAttempts:   128,
			MaxModelCalls: 128,
			MaxTokens:     1_000_000,
		},
		Authority: jobs.DenyAllAuthority(),
		AutoStart: true,
	}
}
