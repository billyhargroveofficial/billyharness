package jobruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestInvocationKindValid(t *testing.T) {
	t.Parallel()
	for _, kind := range []InvocationKind{InvocationKindWorker, InvocationKindReducer, InvocationKindSupervisor} {
		if !kind.Valid() {
			t.Fatalf("kind %q is invalid", kind)
		}
	}
	for _, kind := range []InvocationKind{"", "writer", "provider"} {
		if kind.Valid() {
			t.Fatalf("kind %q unexpectedly valid", kind)
		}
	}
}

func TestInvocationFailureProvenancePreservesCause(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		dispatch DispatchProvenance
		usage    UsageProvenance
	}{
		{name: "preflight", dispatch: DispatchNotDispatched, usage: UsageUnknown},
		{name: "unknown usage", dispatch: DispatchDispatched, usage: UsageUnknown},
		{name: "factual usage", dispatch: DispatchDispatched, usage: UsageFactual},
		{name: "no generation", dispatch: DispatchDispatched, usage: UsageNoGeneration},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := context.Canceled
			err := NewInvocationFailure(cause, test.dispatch, test.usage)
			if !errors.Is(err, cause) {
				t.Fatalf("errors.Is(%v) = false", cause)
			}
			dispatch, usage, ok := InvocationFailureFromError(errors.Join(errors.New("outer"), err))
			if !ok || dispatch != test.dispatch || usage != test.usage {
				t.Fatalf("provenance = %q/%q/%v, want %q/%q/true", dispatch, usage, ok, test.dispatch, test.usage)
			}
		})
	}
	invalid := NewInvocationFailure(errors.New("bad"), DispatchNotDispatched, UsageFactual)
	if _, _, ok := InvocationFailureFromError(invalid); ok {
		t.Fatal("invalid provenance was accepted")
	}
}

func TestTransientInvocationFailureCarriesRetryAfter(t *testing.T) {
	t.Parallel()
	for _, usage := range []UsageProvenance{UsageNoGeneration, UsageUnknown} {
		cause := errors.New("provider retryable failure")
		err := NewTransientInvocationFailure(cause, DispatchDispatched, usage, 3*time.Second)
		if !errors.Is(err, cause) {
			t.Fatalf("%s transient cause was not preserved: %v", usage, err)
		}
		delay, ok := TransientInvocationFailureFromError(errors.Join(errors.New("outer"), err))
		if !ok || delay != 3*time.Second {
			t.Fatalf("%s transient retry = %t/%s", usage, ok, delay)
		}
	}
}

func TestFatalPreflightFailureIsTyped(t *testing.T) {
	t.Parallel()
	cause := errors.New("unsupported persisted route")
	err := NewFatalPreflightFailure(cause)
	if !errors.Is(err, cause) || !FatalPreflightFailureFromError(err) {
		t.Fatalf("fatal preflight typing failed: %v", err)
	}
	dispatch, usage, ok := InvocationFailureFromError(err)
	if !ok || dispatch != DispatchNotDispatched || usage != UsageUnknown {
		t.Fatalf("fatal preflight provenance = %q/%q/%t", dispatch, usage, ok)
	}
}

func TestInvokerContract(t *testing.T) {
	t.Parallel()
	var invoker Invoker = invocationTestInvoker{}
	invocation := validTestInvocation(InvocationKindWorker)
	result, err := invoker.Invoke(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.ValidateFor(invocation); err != nil {
		t.Fatalf("normalized result: %v", err)
	}
}

func TestInvocationValidateFailsClosedAndBoundsInputs(t *testing.T) {
	t.Parallel()
	valid := validTestInvocation(InvocationKindWorker)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid invocation: %v", err)
	}
	writer := valid
	writer.Writer = true
	writer.Authority.WriteRoots = []string{"/workspace"}
	if err := writer.Validate(); err != nil {
		t.Fatalf("valid writer invocation: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Invocation)
		want   string
	}{
		{name: "zero value", mutate: func(i *Invocation) { *i = Invocation{} }, want: "job id"},
		{name: "zero attempt number", mutate: func(i *Invocation) { i.AttemptNo = 0 }, want: "attempt number"},
		{name: "zero cycle", mutate: func(i *Invocation) { i.Cycle = 0 }, want: "cycle"},
		{name: "unknown kind", mutate: func(i *Invocation) { i.Kind = "writer" }, want: "kind"},
		{name: "writer supervisor", mutate: func(i *Invocation) { i.Kind = InvocationKindSupervisor; i.Writer = true }, want: "cannot be a writer"},
		{name: "empty goal", mutate: func(i *Invocation) { i.Goal = " " }, want: "goal"},
		{name: "oversized objective", mutate: func(i *Invocation) { i.Objective = strings.Repeat("x", MaxInvocationTextBytes+1) }, want: "objective"},
		{name: "invalid route", mutate: func(i *Invocation) { i.Route.ProviderID = "" }, want: "route"},
		{name: "zero authority", mutate: func(i *Invocation) { i.Authority = jobs.Authority{} }, want: "authority"},
		{name: "provider missing", mutate: func(i *Invocation) { i.Authority.Providers = nil }, want: "provider"},
		{name: "provider wildcard not narrowed", mutate: func(i *Invocation) { i.Authority.Providers = []string{"*"} }, want: "narrowed"},
		{name: "wrong provider", mutate: func(i *Invocation) { i.Authority.Providers = []string{"other"} }, want: "does not allow"},
		{name: "write root on reader", mutate: func(i *Invocation) { i.Authority.WriteRoots = []string{"/workspace"} }, want: "non-writer"},
		{name: "missing deadline", mutate: func(i *Invocation) { i.Deadline = time.Time{} }, want: "deadline"},
		{name: "missing observed at", mutate: func(i *Invocation) { i.ObservedAt = time.Time{} }, want: "observed_at"},
		{name: "zero remaining model calls", mutate: func(i *Invocation) { i.Limits.ModelCalls = 0 }, want: "model calls"},
		{name: "zero remaining tokens", mutate: func(i *Invocation) { i.Limits.Tokens = 0 }, want: "remaining tokens"},
		{name: "zero max output tokens", mutate: func(i *Invocation) { i.Limits.MaxOutputTokens = 0 }, want: "max output tokens"},
		{name: "max output exceeds remaining", mutate: func(i *Invocation) { i.Limits.MaxOutputTokens = i.Limits.Tokens + 1 }, want: "cannot exceed"},
		{name: "oversized authority entry", mutate: func(i *Invocation) {
			i.Authority.Tools = []string{strings.Repeat("x", MaxInvocationAuthorityEntryBytes+1)}
		}, want: "authority"},
		{name: "too many prior attempts", mutate: func(i *Invocation) { i.PriorAttempts = make([]jobs.Attempt, MaxInvocationPriorAttempts+1) }, want: "prior attempts"},
		{name: "nonterminal prior attempt", mutate: func(i *Invocation) {
			i.PriorAttempts = []jobs.Attempt{validTestPriorAttempt(jobs.AttemptStatusRunning)}
		}, want: "not terminal"},
		{name: "duplicate artifact", mutate: func(i *Invocation) { i.Artifacts = []jobs.ArtifactRef{validTestArtifact(), validTestArtifact()} }, want: "duplicate artifact"},
		{name: "oversized artifact field", mutate: func(i *Invocation) {
			artifact := validTestArtifact()
			artifact.URI = strings.Repeat("u", MaxInvocationArtifactFieldBytes+1)
			i.Artifacts = []jobs.ArtifactRef{artifact}
		}, want: "exceeds limit"},
		{name: "worker next roles", mutate: func(i *Invocation) { i.AllowedNextRoleIDs = []string{"role.next"} }, want: "cannot declare"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			invocation := valid
			test.mutate(&invocation)
			if err := invocation.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestInvocationResultValidateFor(t *testing.T) {
	t.Parallel()
	worker := validTestInvocation(InvocationKindWorker)
	valid := InvocationResult{
		Status:      jobs.AttemptStatusSucceeded,
		Result:      "bounded result",
		Fingerprint: "sha256:result",
		Usage:       jobs.Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 3},
	}
	if err := valid.ValidateFor(worker); err != nil {
		t.Fatalf("valid result: %v", err)
	}

	tests := []struct {
		name       string
		invocation Invocation
		mutate     func(*InvocationResult)
		want       string
	}{
		{name: "zero status", invocation: worker, mutate: func(r *InvocationResult) { r.Status = "" }, want: "non-terminal"},
		{name: "running status", invocation: worker, mutate: func(r *InvocationResult) { r.Status = jobs.AttemptStatusRunning }, want: "non-terminal"},
		{name: "recovery status", invocation: worker, mutate: func(r *InvocationResult) { r.Status = jobs.AttemptStatus("ambiguous") }, want: "recovery-only"},
		{name: "success with error", invocation: worker, mutate: func(r *InvocationResult) { r.Error = "bad" }, want: "cannot contain error"},
		{name: "failed without error", invocation: worker, mutate: func(r *InvocationResult) { r.Status = jobs.AttemptStatusFailed }, want: "error is required"},
		{name: "oversized result", invocation: worker, mutate: func(r *InvocationResult) { r.Result = strings.Repeat("x", MaxInvocationResultBytes+1) }, want: "result exceeds"},
		{name: "control fingerprint", invocation: worker, mutate: func(r *InvocationResult) { r.Fingerprint = "bad\nvalue" }, want: "fingerprint"},
		{name: "attempt usage", invocation: worker, mutate: func(r *InvocationResult) { r.Usage.Attempts = 1 }, want: "cycles or attempts"},
		{name: "missing model usage", invocation: worker, mutate: func(r *InvocationResult) { r.Usage = jobs.Usage{} }, want: "at least one model call"},
		{name: "missing token usage", invocation: worker, mutate: func(r *InvocationResult) { r.Usage.InputTokens = 0; r.Usage.OutputTokens = 0 }, want: "token counts"},
		{name: "model calls exceed remaining", invocation: worker, mutate: func(r *InvocationResult) { r.Usage.ModelCalls = worker.Limits.ModelCalls + 1 }, want: "model call usage exceeds"},
		{name: "tokens exceed remaining", invocation: worker, mutate: func(r *InvocationResult) { r.Usage.InputTokens = worker.Limits.Tokens; r.Usage.OutputTokens = 1 }, want: "token usage exceeds"},
		{name: "output exceeds per call", invocation: worker, mutate: func(r *InvocationResult) {
			r.Usage.ModelCalls = 2
			r.Usage.OutputTokens = 2*worker.Limits.MaxOutputTokens + 1
		}, want: "per-call limit"},
		{name: "token overflow", invocation: worker, mutate: func(r *InvocationResult) { r.Usage.InputTokens = ^uint64(0); r.Usage.OutputTokens = 1 }, want: "overflows"},
		{name: "worker proposal", invocation: worker, mutate: func(r *InvocationResult) { r.Proposal = validTestProposal() }, want: "only a successful supervisor"},
		{name: "supervisor missing proposal", invocation: validTestInvocation(InvocationKindSupervisor), mutate: func(r *InvocationResult) {}, want: "requires proposal"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := valid
			test.mutate(&result)
			if err := result.ValidateFor(test.invocation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateFor() error = %v, want containing %q", err, test.want)
			}
		})
	}

	supervisor := validTestInvocation(InvocationKindSupervisor)
	supervisorResult := valid
	supervisorResult.Proposal = validTestProposal()
	if err := supervisorResult.ValidateFor(supervisor); err != nil {
		t.Fatalf("valid supervisor result: %v", err)
	}
}

func TestSupervisorProposalValidateIsNarrowAndBounded(t *testing.T) {
	t.Parallel()
	allowed := []string{"role.a", "role.b"}
	if err := validTestProposal().Validate(allowed); err != nil {
		t.Fatalf("valid proposal: %v", err)
	}
	for _, kind := range []jobs.DecisionKind{jobs.DecisionComplete, jobs.DecisionWait, jobs.DecisionBlocked} {
		proposal := SupervisorProposal{Kind: kind, Reason: "bounded reason"}
		if err := proposal.Validate(allowed); err != nil {
			t.Fatalf("valid %s proposal: %v", kind, err)
		}
	}

	tests := []struct {
		name     string
		proposal SupervisorProposal
		roles    []string
		want     string
	}{
		{name: "zero value", proposal: SupervisorProposal{}, roles: allowed, want: "reason"},
		{name: "unknown kind", proposal: SupervisorProposal{Kind: "expand", Reason: "reason"}, roles: allowed, want: "kind"},
		{name: "empty reason", proposal: SupervisorProposal{Kind: jobs.DecisionComplete}, roles: allowed, want: "reason"},
		{name: "oversized reason", proposal: SupervisorProposal{Kind: jobs.DecisionComplete, Reason: strings.Repeat("r", MaxSupervisorReasonBytes+1)}, roles: allowed, want: "limit"},
		{name: "reason control", proposal: SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "bad\u0000reason"}, roles: allowed, want: "control"},
		{name: "continue without objectives", proposal: SupervisorProposal{Kind: jobs.DecisionContinue, Reason: "again"}, roles: allowed, want: "exactly one"},
		{name: "continue with role subset", proposal: SupervisorProposal{Kind: jobs.DecisionContinue, Reason: "again", NextObjectives: map[string]string{"role.a": "work"}}, roles: allowed, want: "exactly one"},
		{name: "undeclared role", proposal: SupervisorProposal{Kind: jobs.DecisionContinue, Reason: "again", NextObjectives: map[string]string{"role.other": "work"}}, roles: allowed, want: "undeclared"},
		{name: "empty objective", proposal: SupervisorProposal{Kind: jobs.DecisionContinue, Reason: "again", NextObjectives: map[string]string{"role.a": " "}}, roles: allowed, want: "required"},
		{name: "oversized objective", proposal: SupervisorProposal{Kind: jobs.DecisionContinue, Reason: "again", NextObjectives: map[string]string{"role.a": strings.Repeat("x", MaxSupervisorObjectiveBytes+1)}}, roles: allowed, want: "limit"},
		{name: "objective control", proposal: SupervisorProposal{Kind: jobs.DecisionContinue, Reason: "again", NextObjectives: map[string]string{"role.a": "bad\nobjective"}}, roles: allowed, want: "control"},
		{name: "terminal with objectives", proposal: SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "done", NextObjectives: map[string]string{"role.a": "work"}}, roles: allowed, want: "cannot contain"},
		{name: "missing allowed roles", proposal: *validTestProposal(), roles: nil, want: "allowed roles"},
		{name: "duplicate allowed roles", proposal: *validTestProposal(), roles: []string{"role.a", "role.a"}, want: "duplicate"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.proposal.Validate(test.roles); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseSupervisorProposalRejectsRuntimeOwnedAndTrailingFields(t *testing.T) {
	t.Parallel()
	allowed := []string{"role.a"}
	validJSON := []byte(`{"kind":"continue","reason":"one more pass","next_objectives":{"role.a":"verify claim"}}`)
	proposal, err := ParseSupervisorProposal(validJSON, allowed)
	if err != nil {
		t.Fatalf("valid JSON: %v", err)
	}
	if proposal.Kind != jobs.DecisionContinue || proposal.NextObjectives["role.a"] != "verify claim" {
		t.Fatalf("proposal = %#v", proposal)
	}

	for _, field := range []string{"stage_id", "batch_id", "item_id", "authority", "budget", "provider_id", "model_id", "deadline", "fingerprint"} {
		body := []byte(`{"kind":"complete","reason":"done","` + field + `":"forbidden"}`)
		if _, err := ParseSupervisorProposal(body, allowed); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("field %q error = %v, want unknown field", field, err)
		}
	}
	for name, body := range map[string][]byte{
		"empty":           nil,
		"malformed":       []byte(`{"kind":`),
		"multiple values": []byte(`{"kind":"complete","reason":"done"} {}`),
		"duplicate kind":  []byte(`{"kind":"complete","kind":"continue","reason":"done"}`),
		"duplicate role":  []byte(`{"kind":"continue","reason":"again","next_objectives":{"role.a":"first","role.a":"second"}}`),
		"control":         []byte(`{"kind":"complete","reason":"bad\u0000reason"}`),
		"oversized":       []byte(strings.Repeat("x", MaxSupervisorProposalBytes+1)),
	} {
		if _, err := ParseSupervisorProposal(body, allowed); err == nil {
			t.Fatalf("%s ParseSupervisorProposal() error = nil", name)
		}
	}
}

type invocationTestInvoker struct{}

func (invocationTestInvoker) Invoke(context.Context, Invocation) (InvocationResult, error) {
	return InvocationResult{Status: jobs.AttemptStatusSucceeded, Result: "ok", Usage: jobs.Usage{ModelCalls: 1, InputTokens: 1}}, nil
}

func validTestInvocation(kind InvocationKind) Invocation {
	observedAt := time.Date(2030, time.January, 2, 2, 4, 5, 0, time.UTC)
	invocation := Invocation{
		JobID:       "job-1",
		AttemptID:   "attempt-1",
		AttemptNo:   1,
		BatchID:     "batch-1",
		WorkItemID:  "work-1",
		Cycle:       1,
		StageID:     "stage-1",
		RoleID:      "role.a",
		Kind:        kind,
		Goal:        "Produce one bounded result.",
		Objective:   "Perform the assigned bounded work.",
		RolePurpose: "Produce an independent result.",
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen",
			ModelID:    "qwen3.8-max-preview",
		},
		Authority: jobs.Authority{
			Mode:      jobs.AuthorityModeAllowList,
			Providers: []string{"qwen"},
		},
		ObservedAt: observedAt,
		Deadline:   observedAt.Add(time.Hour),
		JobRemainingBudget: JobRemainingBudget{
			Cycles: 4, Attempts: 10, ModelCalls: 40, Tokens: 80_000,
		},
		Limits: RemainingLimits{
			ModelCalls:      8,
			Tokens:          100_000,
			MaxOutputTokens: 8_000,
		},
	}
	if kind == InvocationKindSupervisor {
		invocation.AllowedNextRoleIDs = []string{"role.a", "role.b"}
	}
	return invocation
}

func validTestPriorAttempt(status jobs.AttemptStatus) jobs.Attempt {
	return jobs.Attempt{
		ID:          "prior-attempt",
		AttemptNo:   1,
		BatchID:     "prior-batch",
		WorkItemID:  "prior-work",
		RoleID:      "role.a",
		Cycle:       1,
		StageID:     "prior-stage",
		Reservation: jobs.AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
		Dispatched:  status != jobs.AttemptStatusRunning,
		Status:      status,
		Result:      "prior result",
		Usage:       jobs.Usage{ModelCalls: 1, InputTokens: 4, OutputTokens: 2},
	}
}

func validTestArtifact() jobs.ArtifactRef {
	return jobs.ArtifactRef{ID: "artifact-1", URI: "job://job-1/artifacts/artifact-1"}
}

func validTestProposal() *SupervisorProposal {
	return &SupervisorProposal{
		Kind:   jobs.DecisionContinue,
		Reason: "One bounded verification pass remains.",
		NextObjectives: map[string]string{
			"role.a": "Verify the remaining claim.",
			"role.b": "Try to falsify the remaining claim.",
		},
	}
}
