package jobagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/jobruntime"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestAdapterExactQwenHTTPPath(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer qwen-test-key" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "qwen-job-request")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"qwen result\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":4,\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)
	t.Setenv("JOBAGENT_QWEN_KEY", "qwen-test-key")

	binding := baseBinding()
	binding.Provider.BaseURL = server.URL
	binding.Auth.APIKeyEnv = "JOBAGENT_QWEN_KEY"
	binding.Limits.RequestTimeout = time.Second
	binding.Limits.StreamIdleTimeout = time.Second
	adapter, err := New(StaticBinding(binding))
	if err != nil {
		t.Fatal(err)
	}
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Limits.MaxOutputTokens = 19
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Result != "qwen result" || result.Usage != (jobs.Usage{ModelCalls: 1, InputTokens: 11, OutputTokens: 4}) {
		t.Fatalf("result = %#v", result)
	}
	if requestBody["model"] != "qwen3.8-max-preview" || requestBody["max_completion_tokens"] != float64(9) ||
		requestBody["enable_thinking"] != true || requestBody["reasoning_effort"] != "xhigh" {
		t.Fatalf("qwen request body = %#v", requestBody)
	}
	if _, exists := requestBody["max_tokens"]; exists {
		t.Fatalf("reasoning Qwen request must not send deprecated max_tokens: %#v", requestBody)
	}
	if tools, exists := requestBody["tools"]; exists && tools != nil {
		t.Fatalf("provider-only Qwen request exposed tools: %#v", tools)
	}
}

func TestAdapterPinsRouteBoundsPromptAndUsage(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.PriorAttempts = []jobs.Attempt{validPriorAttempt()}
	invocation.Artifacts = []jobs.ArtifactRef{{
		ID: "artifact-current", URI: "artifact://current/report", SHA256: "abc", MediaType: "text/markdown",
	}}
	fake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: "final answer"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 7, OutputTokens: 3, ReasoningTokens: 2}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural, RawReason: "stop"}},
	})
	var gotBinding config.ProviderBinding
	adapter := newTestAdapter(t, fake, func(binding config.ProviderBinding) {
		gotBinding = binding
	})

	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Status != jobs.AttemptStatusSucceeded || result.Result != "final answer" || result.Proposal != nil {
		t.Fatalf("result = %#v", result)
	}
	wantUsage := jobs.Usage{ModelCalls: 1, InputTokens: 7, OutputTokens: 3}
	if result.Usage != wantUsage {
		t.Fatalf("usage = %#v, want %#v", result.Usage, wantUsage)
	}
	if err := result.ValidateFor(invocation); err != nil {
		t.Fatalf("result contract: %v", err)
	}
	if gotBinding.Provider.Provider != invocation.Route.ProviderID ||
		gotBinding.Model.Model != invocation.Route.ModelID ||
		gotBinding.Model.Thinking != invocation.Route.Thinking ||
		gotBinding.Model.ReasoningEffort != invocation.Route.ReasoningEffort ||
		gotBinding.Model.MaxTokens != int(invocation.Limits.MaxOutputTokens-qwenCompletionTokenTolerance) {
		t.Fatalf("pinned binding = %#v", gotBinding)
	}
	if gotBinding.Limits.ProviderMaxRetries != 0 || gotBinding.Limits.MaxToolRounds != int(invocation.Limits.ModelCalls) ||
		gotBinding.Limits.MaxParallelTools != 1 {
		t.Fatalf("bounded binding limits = %#v", gotBinding.Limits)
	}

	requests := fake.Requests()
	if len(requests) != 1 || requests[0].Model != invocation.Route.ModelID || len(requests[0].Tools) != 0 {
		t.Fatalf("provider requests = %#v", requests)
	}
	if len(requests[0].Messages) != 2 || requests[0].Messages[0].Role != protocol.RoleSystem || requests[0].Messages[1].Role != protocol.RoleUser {
		t.Fatalf("isolated messages = %#v", requests[0].Messages)
	}
	prompt := requests[0].Messages[1].Content
	for _, want := range []string{
		invocation.RoleID,
		invocation.RolePurpose,
		invocation.Goal,
		invocation.Objective,
		"prior evidence",
		"artifact://prior/report",
		"artifact://current/report",
		"runtime, not you, owns",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, runtimeOwnedID := range []string{invocation.JobID, invocation.AttemptID, invocation.BatchID, invocation.WorkItemID} {
		if strings.Contains(prompt, runtimeOwnedID) {
			t.Fatalf("prompt leaked runtime-owned id %q:\n%s", runtimeOwnedID, prompt)
		}
	}
}

func TestAdapterQwenPromptFitRetainsLargePriorEvidence(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Limits.Tokens = 700_000
	invocation.PriorAttempts = largePromptPriorAttempts(96, 5_000)
	if err := invocation.Validate(); err != nil {
		t.Fatalf("large Qwen invocation: %v", err)
	}
	fake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: "qwen bounded result"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 7, OutputTokens: 3}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
	})
	result, err := newTestAdapter(t, fake, nil).Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("Qwen large-prior result=%#v err=%v", result, err)
	}
	request := fake.Requests()[0]
	prompt := request.Messages[1].Content
	if !strings.Contains(prompt, "EVIDENCE_000_BEGIN") || !strings.Contains(prompt, "EVIDENCE_095_BEGIN") {
		t.Fatal("Qwen prompt did not retain the bounded 480KiB prior evidence set")
	}
	if strings.Contains(prompt, `"prior_attempts_omitted"`) || strings.Contains(prompt, `"prior_evidence_truncated"`) {
		t.Fatal("Qwen prompt unexpectedly truncated evidence that fits its route context")
	}
	pinned, err := pinBinding(baseBinding(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := promptByteBudget(pinned, invocation, request.Messages[:1], request.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt) > budget {
		t.Fatalf("Qwen prompt bytes=%d exceed conservative budget=%d", len(prompt), budget)
	}
}

func TestAdapterSparkPromptFitSelectsNewestPriorEvidenceDeterministically(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Route = jobs.ExecutionRoute{
		ProviderID:      modelinfo.ProviderOpenAICodex,
		ModelID:         "gpt-5.3-codex-spark",
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}
	invocation.Authority.Providers = []string{modelinfo.ProviderOpenAICodex}
	invocation.Limits = jobruntime.RemainingLimits{ModelCalls: 3, Tokens: 100_000, MaxOutputTokens: 8_192}
	invocation.PriorAttempts = largePromptPriorAttempts(96, 5_000)
	if err := invocation.Validate(); err != nil {
		t.Fatalf("large Spark invocation: %v", err)
	}
	binding := baseBinding()
	binding.Provider.Provider = modelinfo.ProviderOpenAICodex
	binding.Provider.CodexBaseURL = "https://codex.invalid/backend"
	fake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: "spark bounded result"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 7, OutputTokens: 3}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
	})
	var pinned config.ProviderBinding
	adapter, err := New(StaticBinding(binding), WithProviderFactory(func(got config.ProviderBinding) (provider.Provider, error) {
		pinned = got
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("Spark large-prior result=%#v err=%v", result, err)
	}
	request := fake.Requests()[0]
	prompt := request.Messages[1].Content
	budget, err := promptByteBudget(pinned, invocation, request.Messages[:1], request.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt) > budget || budget >= int(modelinfo.Lookup(invocation.Route.ModelID).ContextWindowTokens) {
		t.Fatalf("Spark prompt bytes=%d budget=%d context=%d", len(prompt), budget, modelinfo.Lookup(invocation.Route.ModelID).ContextWindowTokens)
	}
	if !strings.Contains(prompt, "EVIDENCE_095_BEGIN") || strings.Contains(prompt, "EVIDENCE_000_BEGIN") {
		t.Fatal("Spark prompt selection did not prefer the newest canonical evidence")
	}
	if !strings.Contains(prompt, `"prior_attempts_omitted":`) || !strings.Contains(prompt, `"prior_evidence_truncated":true`) {
		t.Fatal("Spark prompt did not disclose bounded evidence omission/truncation")
	}
	second, err := buildPromptWithinBudget(invocation, budget)
	if err != nil {
		t.Fatal(err)
	}
	if second != prompt {
		t.Fatal("Spark prompt selection is not deterministic")
	}
}

func TestAdapterSparkPromptFitDropsWholeOversizedArtifactRefs(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Route = jobs.ExecutionRoute{
		ProviderID:      modelinfo.ProviderOpenAICodex,
		ModelID:         "gpt-5.3-codex-spark",
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}
	invocation.Authority.Providers = []string{modelinfo.ProviderOpenAICodex}
	invocation.Limits = jobruntime.RemainingLimits{ModelCalls: 3, Tokens: 100_000, MaxOutputTokens: 8_192}
	invocation.Artifacts = make([]jobs.ArtifactRef, jobruntime.MaxInvocationArtifacts)
	for index := range invocation.Artifacts {
		prefix := fmt.Sprintf("artifact://bounded/%03d/", index)
		invocation.Artifacts[index] = jobs.ArtifactRef{
			ID:  fmt.Sprintf("artifact-%03d", index),
			URI: prefix + strings.Repeat("u", 8_000-len(prefix)),
		}
	}
	if err := invocation.Validate(); err != nil {
		t.Fatalf("large artifact invocation: %v", err)
	}
	binding := baseBinding()
	binding.Provider.Provider = modelinfo.ProviderOpenAICodex
	binding.Provider.CodexBaseURL = "https://codex.invalid/backend"
	fake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: "artifact-bounded result"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 7, OutputTokens: 3}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
	})
	adapter, err := New(StaticBinding(binding), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("Spark artifact-bounded result=%#v err=%v", result, err)
	}
	prompt := fake.Requests()[0].Messages[1].Content
	newest := invocation.Artifacts[len(invocation.Artifacts)-1]
	if !strings.Contains(prompt, newest.URI) || !strings.Contains(prompt, newest.ID) {
		t.Fatal("Spark artifact selection did not retain the newest whole reference")
	}
	if strings.Contains(prompt, invocation.Artifacts[0].ID) {
		t.Fatal("Spark artifact selection retained the oldest reference after exhausting context")
	}
	if !strings.Contains(prompt, `"artifacts_omitted":`) {
		t.Fatal("Spark prompt did not disclose omitted artifact references")
	}
}

func TestAdapterPromptFitRejectsOnlyOversizedMandatorySparkEnvelopeBeforeProvider(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Route = jobs.ExecutionRoute{
		ProviderID:      modelinfo.ProviderOpenAICodex,
		ModelID:         "gpt-5.3-codex-spark",
		Thinking:        "enabled",
		ReasoningEffort: "high",
	}
	invocation.Authority.Providers = []string{modelinfo.ProviderOpenAICodex}
	invocation.Limits = jobruntime.RemainingLimits{ModelCalls: 3, Tokens: 100_000, MaxOutputTokens: 8_192}
	invocation.Goal = strings.Repeat("G", 200_000)
	if err := invocation.Validate(); err != nil {
		t.Fatalf("oversized-for-Spark invocation contract: %v", err)
	}
	binding := baseBinding()
	binding.Provider.Provider = modelinfo.ProviderOpenAICodex
	binding.Provider.CodexBaseURL = "https://codex.invalid/backend"
	factoryCalls := 0
	adapter, err := New(StaticBinding(binding), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
		factoryCalls++
		return newScriptedProvider(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Invoke(t.Context(), invocation)
	if !errors.Is(err, ErrPromptContext) {
		t.Fatalf("Spark mandatory-envelope error = %v, want ErrPromptContext", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("oversized prompt reached provider factory %d times", factoryCalls)
	}
	dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
	if !ok || dispatch != jobruntime.DispatchNotDispatched || usage != jobruntime.UsageUnknown {
		t.Fatalf("prompt-fit provenance=%q/%q/%v", dispatch, usage, ok)
	}
}

func TestPromptIncludesCycleEnvelopeAndForbidsEarlySupervisorCompletion(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindSupervisor)
	invocation.Cycle = 2
	invocation.MinimumCycles = 4
	invocation.MaximumCycles = 8
	invocation.StageID = "supervise"
	invocation.ObservedAt = time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	invocation.Deadline = invocation.ObservedAt.Add(90 * time.Minute)
	invocation.JobRemainingBudget = jobruntime.JobRemainingBudget{
		Cycles: 6, Attempts: 14, ModelCalls: 40, Tokens: 80_000,
	}
	prompt, err := buildPrompt(invocation)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"cycle":2`,
		`"minimum_cycles":4`,
		`"maximum_cycles":8`,
		`"stage_id":"supervise"`,
		`"observed_at":"2030-01-02T03:04:05Z"`,
		`"deadline":`,
		`"remaining_wall_seconds":5400`,
		`"job_remaining_budget":{"cycles":6,"attempts":14,"model_calls":40,"tokens":80000}`,
		`"remaining_limits":`,
		"hard upper cutoffs, not target durations",
		"indefinite durable pause until an operator explicitly resumes",
		"Autonomous rechecking, further research, critique, coding, testing, or iteration must use kind=continue",
		"kind=complete is forbidden",
		"Return kind=continue with next_objectives containing exactly the required role IDs",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("supervisor prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPromptDistinguishesRuntimeFloorFromHardDeadline(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindSupervisor)
	invocation.ObservedAt = time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	invocation.NotBeforeComplete = invocation.ObservedAt.Add(30 * time.Minute)
	invocation.Deadline = invocation.ObservedAt.Add(90 * time.Minute)
	invocation.CycleCadenceSeconds = 300
	prompt, err := buildPrompt(invocation)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"not_before_complete":"2030-01-02T03:34:05Z"`,
		`"remaining_min_runtime_seconds":1800`,
		`"cycle_cadence_seconds":300`,
		"hard upper cutoffs",
		"fixed admission-relative wall-clock earliest-success boundary",
		"queueing, cadence waits, operator pauses, and daemon downtime count",
		"kind=complete is forbidden",
		"paced durably",
		"do not simulate waiting with tool calls or busy work",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("runtime-floor prompt missing %q: %s", want, prompt)
		}
	}
}

func TestAdapterPromptFitHonorsPerAttemptTokenReservationBeforeProvider(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Limits = jobruntime.RemainingLimits{
		ModelCalls: 3, Tokens: 16_000, MaxOutputTokens: 8_000,
	}
	// This envelope is valid and easily fits the Qwen route context, but it
	// cannot fit alongside output/system/safety reserves inside this attempt.
	invocation.Goal = strings.Repeat("G", 20_000)
	if err := invocation.Validate(); err != nil {
		t.Fatalf("small-reservation invocation contract: %v", err)
	}
	factoryCalls := 0
	adapter, err := New(StaticBinding(baseBinding()), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
		factoryCalls++
		return newScriptedProvider(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Invoke(t.Context(), invocation)
	if !errors.Is(err, ErrPromptContext) {
		t.Fatalf("per-attempt prompt-fit error = %v, want ErrPromptContext", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("attempt-reservation overflow reached provider factory %d times", factoryCalls)
	}
	dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
	if !ok || dispatch != jobruntime.DispatchNotDispatched || usage != jobruntime.UsageUnknown {
		t.Fatalf("prompt-fit provenance=%q/%q/%v", dispatch, usage, ok)
	}
}

func TestPromptByteBudgetAccountsForToolSchemaHeadroom(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	pinned, err := pinBinding(baseBinding(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	initial := []protocol.Message{{Role: protocol.RoleSystem, Content: "bounded system"}}
	withoutTools, err := promptByteBudget(pinned, invocation, initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	specs := []protocol.ToolSpec{{
		Name: "bounded_tool", Description: strings.Repeat("schema", 1_000),
		Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Risk: protocol.RiskLocalRead,
	}}
	withTools, err := promptByteBudget(pinned, invocation, initial, specs)
	if err != nil {
		t.Fatal(err)
	}
	if withTools >= withoutTools || withoutTools-withTools < len(specs[0].Description) {
		t.Fatalf("tool schema did not reduce prompt budget enough: without=%d with=%d", withoutTools, withTools)
	}
}

func TestAdapterAggregatesLogicalCallsAndStopsAtCallCap(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Limits.ModelCalls = 2
	fake := newScriptedProvider(
		[]provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call-1", ToolName: "time_now", ArgsDelta: `{}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls, RawReason: "tool_calls"}},
		},
		[]provider.Event{
			{Kind: provider.EventContent, Text: "after bounded retry"},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 6, OutputTokens: 2}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural, RawReason: "stop"}},
		},
	)
	adapter := newTestAdapter(t, fake, nil)

	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Result != "after bounded retry" || result.Usage != (jobs.Usage{ModelCalls: 2, InputTokens: 10, OutputTokens: 3}) {
		t.Fatalf("result = %#v", result)
	}

	capped := validInvocation(t, jobruntime.InvocationKindWorker)
	capped.Limits.ModelCalls = 1
	capFake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call-cap", ToolName: "time_now", ArgsDelta: `{}`},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls, RawReason: "tool_calls"}},
	})
	capAdapter := newTestAdapter(t, capFake, nil)
	partial, err := capAdapter.Invoke(t.Context(), capped)
	if err == nil || !strings.Contains(err.Error(), "exceeded max tool rounds: 1") {
		t.Fatalf("call-cap error = %v", err)
	}
	if capFake.CallCount() != 1 || partial.Usage != (jobs.Usage{ModelCalls: 1, InputTokens: 4, OutputTokens: 1}) {
		t.Fatalf("call-cap partial = %#v calls=%d", partial, capFake.CallCount())
	}
}

func TestAdapterDurableToolsReadNotesWithoutWriteOrAmbientLeak(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	ambientDir := filepath.Join(home, "tool-output")
	if err := os.MkdirAll(ambientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ambientPath := filepath.Join(ambientDir, "other-job.txt")
	if err := os.WriteFile(ambientPath, []byte("AMBIENT_OUTPUT_MUST_NOT_LEAK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readRoot := t.TempDir()
	notePath := filepath.Join(readRoot, "mobilization-notes.md")
	if err := os.WriteFile(notePath, []byte("NOTE_EVIDENCE_228\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Limits.ModelCalls = 3
	invocation.Authority.Tools = []string{"fs_read_file", "fs_write_file"}
	invocation.Authority.ReadRoots = []string{readRoot}
	if err := invocation.Validate(); err != nil {
		t.Fatalf("tool invocation: %v", err)
	}
	fake := newScriptedProvider(
		[]provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "read-note", ToolName: "fs_read_file", ArgsDelta: `{"path":` + mustJSON(t, notePath) + `}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls}},
		},
		[]provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "read-ambient", ToolName: "fs_read_file", ArgsDelta: `{"path":` + mustJSON(t, ambientPath) + `}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 6, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls}},
		},
		[]provider.Event{
			{Kind: provider.EventContent, Text: "bounded note result"},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 8, OutputTokens: 2}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
		},
	)
	registry := tools.NewRegistry(config.Default())
	adapter := newToolTestAdapter(t, fake, registry)
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("tool invocation result=%#v err=%v", result, err)
	}
	requests := fake.Requests()
	if len(requests) != 3 {
		t.Fatalf("provider requests = %d", len(requests))
	}
	for index, request := range requests {
		if got := toolSpecNames(request.Tools); len(got) != 1 || got[0] != "fs_read_file" {
			t.Fatalf("request %d tool schemas = %#v", index, got)
		}
	}
	if !messagesContain(requests[1].Messages, "NOTE_EVIDENCE_228") {
		t.Fatalf("authorized note was not returned to provider: %#v", requests[1].Messages)
	}
	if messagesContain(requests[2].Messages, "AMBIENT_OUTPUT_MUST_NOT_LEAK") || !messagesContain(requests[2].Messages, "outside workspace roots") {
		t.Fatalf("ambient output isolation failed: %#v", requests[2].Messages)
	}
}

func TestAdapterDurableWriterWritesOnlyWriteRoot(t *testing.T) {
	readRoot := t.TempDir()
	writeRoot := t.TempDir()
	outputPath := filepath.Join(writeRoot, "result.md")
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Writer = true
	invocation.Limits.ModelCalls = 3
	invocation.Authority.Tools = []string{"fs_read_file", "fs_write_file"}
	invocation.Authority.ReadRoots = []string{readRoot}
	invocation.Authority.WriteRoots = []string{writeRoot}
	if err := invocation.Validate(); err != nil {
		t.Fatalf("writer invocation: %v", err)
	}
	fake := newScriptedProvider(
		[]provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "write-result", ToolName: "fs_write_file", ArgsDelta: `{"path":` + mustJSON(t, outputPath) + `,"content":"WRITER_RESULT_228"}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls}},
		},
		[]provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "read-write-only", ToolName: "fs_read_file", ArgsDelta: `{"path":` + mustJSON(t, outputPath) + `}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 6, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls}},
		},
		[]provider.Event{
			{Kind: provider.EventContent, Text: "writer finished"},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 8, OutputTokens: 2}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
		},
	)
	result, err := newToolTestAdapter(t, fake, tools.NewRegistry(config.Default())).Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded {
		t.Fatalf("writer result=%#v err=%v", result, err)
	}
	if got, err := os.ReadFile(outputPath); err != nil || string(got) != "WRITER_RESULT_228" {
		t.Fatalf("writer output=%q err=%v", got, err)
	}
	requests := fake.Requests()
	if got := toolSpecNames(requests[0].Tools); len(got) != 2 || got[0] != "fs_read_file" || got[1] != "fs_write_file" {
		t.Fatalf("writer schemas = %#v", got)
	}
	if !messagesContain(requests[2].Messages, "outside workspace roots") {
		t.Fatalf("write-only root was readable: %#v", requests[2].Messages)
	}
}

func TestAdapterDurableWebHostDenialAndUnsafeShellFailClosed(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Limits.ModelCalls = 2
	invocation.Authority.Tools = []string{"web_fetch"}
	invocation.Authority.NetworkHosts = []string{"docs.example"}
	if err := invocation.Validate(); err != nil {
		t.Fatalf("web invocation: %v", err)
	}
	fake := newScriptedProvider(
		[]provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "forbidden-host", ToolName: "web_fetch", ArgsDelta: `{"url":"https://evil.example/private"}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls}},
		},
		[]provider.Event{
			{Kind: provider.EventContent, Text: "host denied"},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 6, OutputTokens: 2}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
		},
	)
	registry := tools.NewRegistry(config.Default())
	result, err := newToolTestAdapter(t, fake, registry).Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded || !messagesContain(fake.Requests()[1].Messages, "outside allowed HTTPS path prefixes") {
		t.Fatalf("host-denial result=%#v err=%v requests=%#v", result, err, fake.Requests())
	}

	shell := validInvocation(t, jobruntime.InvocationKindWorker)
	shell.Authority.Tools = []string{"shell_exec"}
	factoryCalls := 0
	adapter, err := New(
		StaticBinding(baseBinding()),
		WithRegistry(registry),
		WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
			factoryCalls++
			return newScriptedProvider(), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Invoke(t.Context(), shell)
	if !errors.Is(err, ErrUnsupportedAuthority) || !strings.Contains(err.Error(), "not enforceable") || factoryCalls != 0 {
		t.Fatalf("unsafe shell error=%v factory_calls=%d", err, factoryCalls)
	}

	invalidHost := validInvocation(t, jobruntime.InvocationKindWorker)
	invalidHost.Authority.Tools = []string{"web_fetch"}
	invalidHost.Authority.NetworkHosts = []string{"docs.example/private"}
	if _, err := adapter.Invoke(t.Context(), invalidHost); !errors.Is(err, ErrUnsupportedAuthority) ||
		!strings.Contains(err.Error(), "must be an exact host") {
		t.Fatalf("invalid network host error = %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("invalid host reached provider factory: %d", factoryCalls)
	}
}

func TestAdapterPropagatesProviderFinishErrorWithFactualUsage(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	fake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: "truncated"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 2}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishOutputLimit, RawReason: "length"}},
	})
	adapter := newTestAdapter(t, fake, nil)

	partial, err := adapter.Invoke(t.Context(), invocation)
	var finishErr *provider.FinishError
	if !errors.As(err, &finishErr) || finishErr.Finish.Kind != provider.FinishOutputLimit {
		t.Fatalf("finish error = %T %v", err, err)
	}
	if partial.Usage != (jobs.Usage{ModelCalls: 1, InputTokens: 4, OutputTokens: 2}) {
		t.Fatalf("partial usage = %#v", partial.Usage)
	}
}

func TestAdapterStrictlyParsesSupervisorProposal(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindSupervisor)
	invocation.AllowedNextRoleIDs = []string{"role.a", "role.b"}
	validJSON := `{"kind":"continue","reason":"one more pass","next_objectives":{"role.a":"check A","role.b":"check B"}}`
	valid := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: validJSON},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 9, OutputTokens: 5}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
	})
	result, err := newTestAdapter(t, valid, nil).Invoke(t.Context(), invocation)
	if err != nil || result.Status != jobs.AttemptStatusSucceeded || result.Proposal == nil || result.Proposal.Kind != jobs.DecisionContinue {
		t.Fatalf("valid supervisor result=%#v err=%v", result, err)
	}
	if prompt := valid.Requests()[0].Messages[1].Content; !strings.Contains(prompt, `["role.a","role.b"]`) || !strings.Contains(prompt, "exactly one raw JSON object") {
		t.Fatalf("supervisor prompt = %s", prompt)
	}

	invalidJSON := `{"kind":"complete","reason":"done","job_id":"model-owned"}`
	invalid := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: invalidJSON},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 8, OutputTokens: 4}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
	})
	failed, err := newTestAdapter(t, invalid, nil).Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatalf("invalid proposal returned transport error: %v", err)
	}
	if failed.Status != jobs.AttemptStatusFailed || failed.Proposal != nil || !strings.Contains(failed.Error, "unknown field") {
		t.Fatalf("invalid supervisor result = %#v", failed)
	}
	if err := failed.ValidateFor(invocation); err != nil {
		t.Fatalf("failed proposal contract: %v", err)
	}
}

func TestAdapterRejectsUnenforceableAuthorityAndRoutesBeforeProvider(t *testing.T) {
	tests := map[string]func(*jobruntime.Invocation, *config.ProviderBinding){
		"tool authority": func(invocation *jobruntime.Invocation, _ *config.ProviderBinding) {
			invocation.Authority.Tools = []string{"time_now"}
		},
		"read roots": func(invocation *jobruntime.Invocation, _ *config.ProviderBinding) {
			invocation.Authority.ReadRoots = []string{"/workspace"}
		},
		"resolver provider mismatch": func(_ *jobruntime.Invocation, binding *config.ProviderBinding) {
			binding.Provider.Provider = "deepseek"
		},
		"noncanonical provider": func(invocation *jobruntime.Invocation, binding *config.ProviderBinding) {
			invocation.Route.ProviderID = "qwen-cloud"
			invocation.Authority.Providers = []string{"qwen-cloud"}
			binding.Provider.Provider = "qwen-cloud"
		},
		"model reroutes provider": func(invocation *jobruntime.Invocation, _ *config.ProviderBinding) {
			invocation.Route.ModelID = "k3"
		},
		"qwen reasoning cap within tolerance": func(invocation *jobruntime.Invocation, _ *config.ProviderBinding) {
			invocation.Limits.MaxOutputTokens = qwenCompletionTokenTolerance
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := validInvocation(t, jobruntime.InvocationKindWorker)
			binding := baseBinding()
			mutate(&invocation, &binding)
			factoryCalls := 0
			adapter, err := New(StaticBinding(binding), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
				factoryCalls++
				return newScriptedProvider(), nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Invoke(t.Context(), invocation)
			if err == nil {
				t.Fatal("Invoke() error = nil")
			}
			if factoryCalls != 0 {
				t.Fatalf("provider factory calls = %d", factoryCalls)
			}
		})
	}
}

func TestAdapterPinsCodexResponsesOutputCap(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Route = jobs.ExecutionRoute{
		ProviderID: "openai-codex", ModelID: "gpt-5.5", Thinking: "enabled", ReasoningEffort: "high",
	}
	invocation.Authority.Providers = []string{"openai-codex"}
	binding := baseBinding()
	binding.Provider.Provider = "openai-codex"
	binding.Provider.CodexBaseURL = "https://codex.invalid/backend"

	fake := newScriptedProvider([]provider.Event{
		{Kind: provider.EventContent, Text: "codex result"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 7, OutputTokens: 4, ReasoningTokens: 2}},
		{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural, RawReason: "completed"}},
	})
	var gotBinding config.ProviderBinding
	adapter, err := New(StaticBinding(binding), WithProviderFactory(func(binding config.ProviderBinding) (provider.Provider, error) {
		gotBinding = binding
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Invoke(t.Context(), invocation)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Status != jobs.AttemptStatusSucceeded || result.Usage.OutputTokens != 4 {
		t.Fatalf("result = %#v", result)
	}
	if gotBinding.Provider.Provider != "openai-codex" || gotBinding.Model.Model != "gpt-5.5" ||
		gotBinding.Model.MaxTokens != int(invocation.Limits.MaxOutputTokens) ||
		gotBinding.Limits.MaxTokens != int(invocation.Limits.MaxOutputTokens) {
		t.Fatalf("pinned Codex binding = %#v", gotBinding)
	}
}

func TestPinBindingClampsProviderNeutralReservationToModelOutputCapability(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Route = jobs.ExecutionRoute{
		ProviderID: "openai-codex", ModelID: "gpt-5.5", Thinking: "enabled", ReasoningEffort: "high",
	}
	invocation.Authority.Providers = []string{"openai-codex"}
	invocation.Limits.Tokens = 100_000
	invocation.Limits.MaxOutputTokens = 64 << 10
	binding := baseBinding()
	binding.Provider.Provider = "openai-codex"

	pinned, err := pinBinding(binding, invocation)
	if err != nil {
		t.Fatal(err)
	}
	want := modelinfo.Lookup("gpt-5.5").MaxOutputTokens
	if pinned.Model.MaxTokens != want || pinned.Limits.MaxTokens != want {
		t.Fatalf("pinned output cap = model:%d runtime:%d, want %d", pinned.Model.MaxTokens, pinned.Limits.MaxTokens, want)
	}
}

func TestPinBindingClampsUnknownCustomModelToConfiguredOutputCapability(t *testing.T) {
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Route = jobs.ExecutionRoute{
		ProviderID: "my-openai-compatible", ModelID: "unknown-model", Thinking: "disabled", ReasoningEffort: "off",
	}
	invocation.Authority.Providers = []string{"my-openai-compatible"}
	invocation.Limits.Tokens = 100_000
	invocation.Limits.MaxOutputTokens = 64 << 10
	binding := baseBinding()
	binding.Provider.Provider = "my-openai-compatible"
	binding.Model.MaxTokens = 4_096
	binding.Limits.MaxTokens = 8_192

	pinned, err := pinBinding(binding, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Model.MaxTokens != 4_096 || pinned.Limits.MaxTokens != 4_096 {
		t.Fatalf("custom pinned output cap = model:%d runtime:%d", pinned.Model.MaxTokens, pinned.Limits.MaxTokens)
	}
}

func TestPinBindingQwenCompletionToleranceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name       string
		thinking   string
		requested  uint64
		wantPinned int
		wantErr    bool
	}{
		{name: "below tolerance", thinking: "enabled", requested: 9, wantErr: true},
		{name: "at tolerance", thinking: "enabled", requested: 10, wantErr: true},
		{name: "one above tolerance", thinking: "enabled", requested: 11, wantPinned: 1},
		{name: "disabled reasoning", thinking: "disabled", requested: 10, wantPinned: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocation := validInvocation(t, jobruntime.InvocationKindWorker)
			invocation.Route.Thinking = test.thinking
			invocation.Limits.MaxOutputTokens = test.requested
			binding, err := pinBinding(baseBinding(), invocation)
			if test.wantErr {
				if !errors.Is(err, ErrUnsupportedRoute) {
					t.Fatalf("pinBinding() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if binding.Model.MaxTokens != test.wantPinned || binding.Limits.MaxTokens != test.wantPinned {
				t.Fatalf("pinned limits = model:%d runtime:%d", binding.Model.MaxTokens, binding.Limits.MaxTokens)
			}
		})
	}
}

func TestAdapterRejectsMissingAndOverLimitUsage(t *testing.T) {
	tests := map[string]struct {
		limits          jobruntime.RemainingLimits
		step            []provider.Event
		usageProvenance jobruntime.UsageProvenance
	}{
		"missing": {
			limits:          jobruntime.RemainingLimits{ModelCalls: 1, Tokens: 8_000, MaxOutputTokens: 5},
			usageProvenance: jobruntime.UsageUnknown,
			step: []provider.Event{
				{Kind: provider.EventContent, Text: "answer"},
				{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
			},
		},
		"per call output": {
			limits:          jobruntime.RemainingLimits{ModelCalls: 1, Tokens: 8_000, MaxOutputTokens: 5},
			usageProvenance: jobruntime.UsageFactual,
			step: []provider.Event{
				{Kind: provider.EventContent, Text: "answer"},
				{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 3, OutputTokens: 6}},
				{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
			},
		},
		"aggregate tokens": {
			limits:          jobruntime.RemainingLimits{ModelCalls: 1, Tokens: 8_000, MaxOutputTokens: 5},
			usageProvenance: jobruntime.UsageFactual,
			step: []provider.Event{
				{Kind: provider.EventContent, Text: "answer"},
				{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 7_996, OutputTokens: 5}},
				{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishNatural}},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			invocation := validInvocation(t, jobruntime.InvocationKindWorker)
			invocation.Limits = test.limits
			invocation.Route.Thinking = "disabled"
			invocation.Route.ReasoningEffort = "off"
			fake := newScriptedProvider(test.step)
			partial, err := newTestAdapter(t, fake, nil).Invoke(t.Context(), invocation)
			if !errors.Is(err, ErrUsageAccounting) {
				t.Fatalf("usage error = %T %v", err, err)
			}
			if partial.Usage.ModelCalls != 1 {
				t.Fatalf("partial usage = %#v", partial.Usage)
			}
			dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
			if !ok || dispatch != jobruntime.DispatchDispatched || usage != test.usageProvenance {
				t.Fatalf("provenance = %q/%q/%v, want dispatched/%q/true", dispatch, usage, ok, test.usageProvenance)
			}
		})
	}
}

func TestAdapterClassifiesProviderErrorsBeforeUsage(t *testing.T) {
	tests := map[string]struct {
		err       error
		usage     jobruntime.UsageProvenance
		transient bool
	}{
		"cancelled": {err: context.Canceled, usage: jobruntime.UsageUnknown},
		"dns": {err: &provider.ProviderError{
			Provider: "qwen", Kind: provider.ErrorTransport,
			Err: &net.DNSError{Err: "no such host", Name: "qwen.invalid"},
		}, usage: jobruntime.UsageUnknown, transient: true},
		"rate limit": {err: &provider.ProviderError{
			Provider: "qwen", ModelID: "qwen3.8-max-preview", Kind: provider.ErrorRateLimit, Status: http.StatusTooManyRequests,
			RetryAfter: 2 * time.Second,
		}, usage: jobruntime.UsageNoGeneration, transient: true},
		"server": {err: &provider.ProviderError{
			Provider: "qwen", ModelID: "qwen3.8-max-preview", Kind: provider.ErrorServer, Status: http.StatusBadGateway,
			RetryAfter: 3 * time.Second,
		}, usage: jobruntime.UsageUnknown, transient: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			adapter, err := New(StaticBinding(baseBinding()), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
				return terminalErrorProvider{err: test.err}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			partial, err := adapter.Invoke(t.Context(), validInvocation(t, jobruntime.InvocationKindWorker))
			if err == nil || !errors.Is(err, test.err) || !errors.Is(err, ErrUsageAccounting) {
				t.Fatalf("Invoke() error = %T %v", err, err)
			}
			if partial.Usage.ModelCalls != 1 || partial.Usage.TotalTokens() != 0 {
				t.Fatalf("partial usage = %#v", partial.Usage)
			}
			dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
			if !ok || dispatch != jobruntime.DispatchDispatched || usage != test.usage {
				t.Fatalf("provenance = %q/%q/%v, want dispatched/%s/true", dispatch, usage, ok, test.usage)
			}
			retryAfter, transient := jobruntime.TransientInvocationFailureFromError(err)
			wantRetryAfter := time.Duration(0)
			if name == "rate limit" {
				wantRetryAfter = 2 * time.Second
			} else if name == "server" {
				wantRetryAfter = 3 * time.Second
			}
			if transient != test.transient || (test.transient && retryAfter != wantRetryAfter) {
				t.Fatalf("transient = %t retry_after=%s", transient, retryAfter)
			}
		})
	}
}

func TestAdapterLaterHTTPRejectionAfterWriterEffectRemainsUnknown(t *testing.T) {
	writeRoot := t.TempDir()
	outputPath := filepath.Join(writeRoot, "result.md")
	providerErr := &provider.ProviderError{
		Provider:   "qwen",
		ModelID:    "qwen3.8-max-preview",
		Kind:       provider.ErrorRateLimit,
		Status:     http.StatusTooManyRequests,
		RetryAfter: 2 * time.Second,
	}
	fake := &scriptThenErrorProvider{
		first: []provider.Event{
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "write-result", ToolName: "fs_write_file", ArgsDelta: `{"path":` + mustJSON(t, outputPath) + `,"content":"WRITER_RESULT_228"}`},
			{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishToolCalls}},
		},
		err: providerErr,
	}
	adapter, err := New(
		StaticBinding(baseBinding()),
		WithRegistry(tools.NewRegistry(config.Default())),
		WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) { return fake, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Writer = true
	invocation.Limits.ModelCalls = 2
	invocation.Authority.Tools = []string{"fs_write_file"}
	invocation.Authority.WriteRoots = []string{writeRoot}
	if err := invocation.Validate(); err != nil {
		t.Fatalf("writer invocation: %v", err)
	}

	partial, err := adapter.Invoke(t.Context(), invocation)
	if err == nil || !errors.Is(err, providerErr) || !errors.Is(err, ErrUsageAccounting) {
		t.Fatalf("Invoke() error = %T %v", err, err)
	}
	if got, readErr := os.ReadFile(outputPath); readErr != nil || string(got) != "WRITER_RESULT_228" {
		t.Fatalf("writer output=%q err=%v", got, readErr)
	}
	if fake.CallCount() != 2 || partial.Usage != (jobs.Usage{ModelCalls: 2, InputTokens: 4, OutputTokens: 1}) {
		t.Fatalf("partial usage=%#v calls=%d", partial.Usage, fake.CallCount())
	}
	dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
	if !ok || dispatch != jobruntime.DispatchDispatched || usage != jobruntime.UsageUnknown {
		t.Fatalf("provenance = %q/%q/%v, want dispatched/unknown/true", dispatch, usage, ok)
	}
	if retryAfter, transient := jobruntime.TransientInvocationFailureFromError(err); !transient || retryAfter != 2*time.Second {
		t.Fatalf("transient = %t retry_after=%s", transient, retryAfter)
	}
}

func TestAdapterHonorsCancelledContextBeforeProviderConstruction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	factoryCalls := 0
	adapter, err := New(StaticBinding(baseBinding()), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
		factoryCalls++
		return newScriptedProvider(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Invoke(ctx, validInvocation(t, jobruntime.InvocationKindWorker))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Invoke() error = %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("provider factory calls = %d", factoryCalls)
	}
	dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
	if !ok || dispatch != jobruntime.DispatchNotDispatched || usage != jobruntime.UsageUnknown {
		t.Fatalf("provenance = %q/%q/%v, want not_dispatched/unknown/true", dispatch, usage, ok)
	}
	if jobruntime.FatalPreflightFailureFromError(err) {
		t.Fatalf("operator/daemon cancellation was misclassified as fatal preflight: %v", err)
	}
}

func TestAdapterExpiredDeadlineBeforeProviderConstructionIsRecoverablePreflight(t *testing.T) {
	factoryCalls := 0
	adapter, err := New(StaticBinding(baseBinding()), WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) {
		factoryCalls++
		return newScriptedProvider(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	invocation := validInvocation(t, jobruntime.InvocationKindWorker)
	invocation.Deadline = time.Now().Add(-time.Second)
	_, err = adapter.Invoke(t.Context(), invocation)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Invoke() error = %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("provider factory calls = %d", factoryCalls)
	}
	if jobruntime.FatalPreflightFailureFromError(err) {
		t.Fatalf("deadline race was misclassified as fatal preflight: %v", err)
	}
	dispatch, usage, ok := jobruntime.InvocationFailureFromError(err)
	if !ok || dispatch != jobruntime.DispatchNotDispatched || usage != jobruntime.UsageUnknown {
		t.Fatalf("provenance = %q/%q/%v, want not_dispatched/unknown/true", dispatch, usage, ok)
	}
}

func newTestAdapter(t *testing.T, fake *scriptedProvider, inspect func(config.ProviderBinding)) *Adapter {
	t.Helper()
	adapter, err := New(StaticBinding(baseBinding()), WithProviderFactory(func(binding config.ProviderBinding) (provider.Provider, error) {
		if inspect != nil {
			inspect(binding)
		}
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func newToolTestAdapter(t *testing.T, fake *scriptedProvider, registry *tools.Registry) *Adapter {
	t.Helper()
	adapter, err := New(
		StaticBinding(baseBinding()),
		WithRegistry(registry),
		WithProviderFactory(func(config.ProviderBinding) (provider.Provider, error) { return fake, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func toolSpecNames(specs []protocol.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Name)
	}
	return out
}

func messagesContain(messages []protocol.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

func baseBinding() config.ProviderBinding {
	return config.ProviderBinding{
		Provider: config.ProviderSelection{Provider: "qwen", BaseURL: "https://qwen.invalid/v1"},
		Model: config.ModelSelection{
			Model: "ambient-model", Thinking: "ambient-thinking", ReasoningEffort: "ambient-reasoning", MaxTokens: 999,
		},
		Limits: config.RuntimeLimits{
			MaxTokens: 999, MaxToolRounds: 99, MaxParallelTools: 4, ProviderMaxRetries: 5,
			RequestTimeout: time.Minute,
		},
	}
}

func validInvocation(t *testing.T, kind jobruntime.InvocationKind) jobruntime.Invocation {
	t.Helper()
	observedAt := time.Now().UTC().Truncate(time.Second)
	invocation := jobruntime.Invocation{
		JobID:      "job-1",
		AttemptID:  "attempt-2",
		AttemptNo:  2,
		BatchID:    "batch-2",
		WorkItemID: "work-2",
		Cycle:      2,
		StageID:    "stage.work",
		RoleID:     "role.primary",
		Kind:       kind,
		Goal:       "Determine the best supported conclusion.",
		Objective:  "Check the evidence and produce the bounded result.",
		RolePurpose: "Independently verify important claims and expose " +
			"uncertainty.",
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen", ModelID: "qwen3.8-max-preview", Thinking: "enabled", ReasoningEffort: "high",
		},
		Authority:  jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{"qwen"}},
		ObservedAt: observedAt,
		Deadline:   observedAt.Add(time.Hour),
		JobRemainingBudget: jobruntime.JobRemainingBudget{
			Cycles: 6, Attempts: 20, ModelCalls: 80, Tokens: 1_000_000,
		},
		Limits: jobruntime.RemainingLimits{ModelCalls: 3, Tokens: 128 << 10, MaxOutputTokens: 20},
	}
	if kind == jobruntime.InvocationKindSupervisor {
		invocation.RoleID = "control.supervisor"
		invocation.StageID = "supervise"
		invocation.RolePurpose = "Propose only a bounded workflow disposition."
		invocation.AllowedNextRoleIDs = []string{"role.a"}
	}
	if err := invocation.Validate(); err != nil {
		t.Fatalf("test invocation: %v", err)
	}
	return invocation
}

func validPriorAttempt() jobs.Attempt {
	return jobs.Attempt{
		ID:         "prior-attempt",
		BatchID:    "prior-batch",
		WorkItemID: "prior-work",
		RoleID:     "role.prior",
		AttemptNo:  1,
		Cycle:      1,
		StageID:    "stage.prior",
		Reservation: jobs.AttemptReservation{
			ModelCalls: 1, Tokens: 20, MaxOutputTokens: 10,
		},
		Dispatched: true,
		Status:     jobs.AttemptStatusSucceeded,
		Result:     "prior evidence",
		Artifacts: []jobs.ArtifactRef{{
			ID: "artifact-prior", URI: "artifact://prior/report", CreatedByAttemptID: "prior-attempt",
		}},
		Usage: jobs.Usage{ModelCalls: 1, InputTokens: 3, OutputTokens: 2},
	}
}

func largePromptPriorAttempts(count, resultBytes int) []jobs.Attempt {
	out := make([]jobs.Attempt, count)
	for index := range out {
		attempt := validPriorAttempt()
		attempt.ID = fmt.Sprintf("prior-attempt-%03d", index)
		attempt.BatchID = fmt.Sprintf("prior-batch-%03d", index)
		attempt.WorkItemID = fmt.Sprintf("prior-work-%03d", index)
		attempt.AttemptNo = uint64(index + 1)
		attempt.Cycle = uint64(index/4 + 1)
		attempt.Artifacts = nil
		prefix := fmt.Sprintf("EVIDENCE_%03d_BEGIN_", index)
		suffix := fmt.Sprintf("_EVIDENCE_%03d_END", index)
		padding := max(0, resultBytes-len(prefix)-len(suffix))
		attempt.Result = prefix + strings.Repeat("x", padding) + suffix
		out[index] = attempt
	}
	return out
}

func TestFinalAssistantResultRejectsEmptyContent(t *testing.T) {
	for name, messages := range map[string][]protocol.Message{
		"empty":      {{Role: protocol.RoleAssistant}},
		"whitespace": {{Role: protocol.RoleAssistant, Content: " \n\t"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := finalAssistantResult(messages); err == nil || !strings.Contains(err.Error(), "empty") {
				t.Fatalf("finalAssistantResult() error = %v, want empty-result error", err)
			}
		})
	}
	if got, err := finalAssistantResult([]protocol.Message{{Role: protocol.RoleAssistant, Content: " result "}}); err != nil || got != " result " {
		t.Fatalf("finalAssistantResult() = %q, %v", got, err)
	}
}

type scriptedProvider struct {
	mu       sync.Mutex
	steps    [][]provider.Event
	requests []provider.Request
}

type terminalErrorProvider struct{ err error }

type scriptThenErrorProvider struct {
	mu    sync.Mutex
	first []provider.Event
	err   error
	calls int
}

func (p terminalErrorProvider) Stream(context.Context, provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event)
	close(events)
	errs := make(chan error, 1)
	errs <- p.err
	close(errs)
	return events, errs
}

func (p *scriptThenErrorProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	first := append([]provider.Event(nil), p.first...)
	err := p.err
	p.mu.Unlock()
	events := make(chan provider.Event, len(first))
	errs := make(chan error, 1)
	if call == 1 {
		for _, event := range first {
			events <- event
		}
	} else {
		errs <- err
	}
	close(events)
	close(errs)
	return events, errs
}

func (p *scriptThenErrorProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newScriptedProvider(steps ...[]provider.Event) *scriptedProvider {
	return &scriptedProvider{steps: steps}
}

func (p *scriptedProvider) Stream(ctx context.Context, request provider.Request) (<-chan provider.Event, <-chan error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, request)
	var step []provider.Event
	if index < len(p.steps) {
		step = append([]provider.Event(nil), p.steps[index]...)
	}
	p.mu.Unlock()
	events := make(chan provider.Event, len(step))
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		for _, event := range step {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case events <- event:
			}
		}
	}()
	return events, errs
}

func (p *scriptedProvider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}

func (p *scriptedProvider) CallCount() int {
	return len(p.Requests())
}
