package jobs

import (
	"strings"
	"testing"
	"time"
)

func TestJobSpecStrictValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*JobSpec){
		"missing id": func(spec *JobSpec) {
			spec.ID = ""
		},
		"missing goal": func(spec *JobSpec) {
			spec.Goal = " "
		},
		"invalid create request hash": func(spec *JobSpec) {
			spec.CreateRequestHash = "not-a-sha256"
		},
		"workers below cap": func(spec *JobSpec) {
			spec.Workers = 0
		},
		"workers above cap": func(spec *JobSpec) {
			spec.Workers = MaxWorkers + 1
		},
		"missing deadline": func(spec *JobSpec) {
			spec.Deadline = time.Time{}
		},
		"unbounded cycles": func(spec *JobSpec) {
			spec.Budget.MaxCycles = 0
		},
		"implicit authority": func(spec *JobSpec) {
			spec.Authority = Authority{}
		},
		"duplicate role": func(spec *JobSpec) {
			spec.Roles = append(spec.Roles, spec.Roles[0])
		},
		"dangling stage role": func(spec *JobSpec) {
			spec.Stages[0].RoleIDs[0] = "missing-role"
		},
	}

	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			spec := validDomainSpec(t, 2)
			mutate(&spec)
			if err := spec.Validate(); err == nil {
				t.Fatal("Validate() succeeded, want strict rejection")
			}
		})
	}
}

func TestJobSpecAdmittedAtValidationAllowsLegacyZero(t *testing.T) {
	t.Parallel()

	legacy := validDomainSpec(t, 2)
	if !legacy.AdmittedAt.IsZero() {
		t.Fatalf("legacy admitted_at = %s, want zero", legacy.AdmittedAt)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy zero admitted_at: %v", err)
	}

	valid := legacy
	valid.AdmittedAt = valid.Deadline.Add(-time.Hour)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid admitted_at: %v", err)
	}

	nonUTC := valid
	nonUTC.AdmittedAt = time.Date(2026, time.July, 24, 11, 0, 0, 0, time.FixedZone("admission", 3*60*60))
	if err := nonUTC.Validate(); err == nil || !strings.Contains(err.Error(), "admitted_at must be UTC") {
		t.Fatalf("non-UTC admitted_at error = %v", err)
	}

	atDeadline := valid
	atDeadline.AdmittedAt = atDeadline.Deadline
	if err := atDeadline.Validate(); err == nil || !strings.Contains(err.Error(), "admitted_at must be before deadline") {
		t.Fatalf("late admitted_at error = %v", err)
	}

	afterDeadline := valid
	afterDeadline.AdmittedAt = afterDeadline.Deadline.Add(time.Second)
	if err := afterDeadline.Validate(); err == nil || !strings.Contains(err.Error(), "admitted_at must be before deadline") {
		t.Fatalf("future admitted_at error = %v", err)
	}
}

func TestPortableIdentifiersAllowDottedRoleAndStageIDs(t *testing.T) {
	t.Parallel()

	role := RoleSpec{
		ID:        "coding.codebase",
		Purpose:   "Inspect code.",
		Authority: DenyAllAuthority(),
	}
	if err := role.Validate(); err != nil {
		t.Fatalf("dotted role id: %v", err)
	}
	stage := StageSpec{
		ID:         "control.reducer",
		RoleIDs:    []string{"coding.codebase"},
		MaxWorkers: 1,
		Barrier:    BarrierAll,
	}
	if err := stage.Validate(); err != nil {
		t.Fatalf("dotted stage id: %v", err)
	}
}

func TestWorkBatchValidationRequiresBoundedStableFlatBatch(t *testing.T) {
	t.Parallel()

	valid := WorkBatch{
		ID:      "batch-1",
		StageID: "explore",
		Cycle:   1,
		Barrier: BarrierAll,
		Items: []WorkItem{
			{ID: "work-a", RoleID: "role-a", Objective: "A", Authority: DenyAllAuthority()},
			{ID: "work-b", RoleID: "role-b", Objective: "B", Authority: DenyAllAuthority()},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid batch: %v", err)
	}

	unsorted := valid
	unsorted.Items = []WorkItem{valid.Items[1], valid.Items[0]}
	if err := unsorted.Validate(); err == nil || !strings.Contains(err.Error(), "ordered") {
		t.Fatalf("unsorted Validate() error = %v", err)
	}

	oversized := valid
	oversized.Items = make([]WorkItem, MaxWorkers+1)
	for i := range oversized.Items {
		oversized.Items[i] = WorkItem{
			ID:        "work-" + string(rune('a'+i)),
			RoleID:    "role-" + string(rune('a'+i)),
			Objective: "bounded",
			Authority: DenyAllAuthority(),
		}
	}
	if err := oversized.Validate(); err == nil {
		t.Fatal("oversized batch validated")
	}
}

func TestValidateBatchForSpecRejectsUnknownRoleAndAuthorityExpansion(t *testing.T) {
	t.Parallel()

	spec := validDomainSpec(t, 1)
	stage := spec.Stages[0]
	roleID := stage.RoleIDs[0]
	batch := WorkBatch{
		ID:      "batch-1",
		StageID: stage.ID,
		Cycle:   1,
		Barrier: BarrierAll,
		Items: []WorkItem{{
			ID:        "work-1",
			RoleID:    roleID,
			Objective: "Do one bounded task.",
			Authority: DenyAllAuthority(),
		}},
	}
	if err := ValidateBatchForSpec(spec, batch); err != nil {
		t.Fatalf("valid batch for spec: %v", err)
	}

	unknown := batch
	unknown.Items = append([]WorkItem(nil), batch.Items...)
	unknown.Items[0].RoleID = "undeclared"
	if err := ValidateBatchForSpec(spec, unknown); err == nil || !strings.Contains(err.Error(), "undeclared role") {
		t.Fatalf("unknown role error = %v", err)
	}

	expanded := batch
	expanded.Items = append([]WorkItem(nil), batch.Items...)
	expanded.Items[0].Authority = Authority{
		Mode:  AuthorityModeAllowList,
		Tools: []string{"shell"},
	}
	if err := ValidateBatchForSpec(spec, expanded); err == nil || !strings.Contains(err.Error(), "broadens") {
		t.Fatalf("expanded authority error = %v", err)
	}
}

func TestWriterMustBeIsolatedInPersistedStageAndRuntimeBatch(t *testing.T) {
	t.Parallel()

	spec := validDomainSpecForPreset(t, PresetCoding, 2)
	writerStageIndex := -1
	for index := range spec.Stages {
		if spec.Stages[index].ID == "implement" {
			writerStageIndex = index
			break
		}
	}
	if writerStageIndex < 0 {
		t.Fatal("coding preset has no implement stage")
	}

	sharedStage := cloneJobSpec(spec)
	sharedStage.Stages[writerStageIndex].RoleIDs = append(
		sharedStage.Stages[writerStageIndex].RoleIDs,
		sharedStage.Workflow.WorkerRoleIDs[0],
	)
	sharedStage.Stages[writerStageIndex].MaxWorkers = 2
	if err := sharedStage.Validate(); err == nil || !strings.Contains(err.Error(), "isolate its writer") {
		t.Fatalf("writer-sharing stage error = %v", err)
	}

	writerRoleID := spec.Stages[writerStageIndex].RoleIDs[0]
	readerRoleID := ""
	for _, candidate := range spec.Workflow.WorkerRoleIDs {
		if candidate != writerRoleID {
			readerRoleID = candidate
			break
		}
	}
	if readerRoleID == "" {
		t.Fatal("coding preset has no non-writer role")
	}
	sharedBatch := WorkBatch{
		ID: "batch-writer-shared", StageID: spec.Stages[writerStageIndex].ID, Cycle: 1, Barrier: BarrierAll,
		Items: []WorkItem{
			{ID: "work-reader", RoleID: readerRoleID, Objective: "Read.", Authority: DenyAllAuthority()},
			{ID: "work-writer", RoleID: writerRoleID, Objective: "Write.", Authority: DenyAllAuthority()},
		},
	}
	if err := ValidateBatchForSpec(spec, sharedBatch); err == nil {
		t.Fatal("writer-sharing runtime batch validated")
	}
}

func TestJobStateValidationRejectsTerminalRunningOrPendingStop(t *testing.T) {
	t.Parallel()

	spec := validDomainSpec(t, 1)
	stageID := spec.Workflow.StageOrder[0]
	var stage StageSpec
	for _, candidate := range spec.Stages {
		if candidate.ID == stageID {
			stage = candidate
			break
		}
	}
	batch := WorkBatch{
		ID: "batch-active", StageID: stage.ID, Cycle: 1, Barrier: BarrierAll,
		Items: []WorkItem{{
			ID: "work-active", RoleID: stage.RoleIDs[0], Objective: "Run.", Authority: DenyAllAuthority(),
		}},
	}
	running := Attempt{
		ID: "attempt-active", BatchID: batch.ID, WorkItemID: batch.Items[0].ID, RoleID: batch.Items[0].RoleID,
		AttemptNo: 1, Cycle: 1, StageID: batch.StageID,
		Reservation: AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
		Status:      AttemptStatusRunning,
	}
	draining := JobState{
		Spec: spec, Status: JobStatusRunning, Cycle: 1, CurrentBatch: &batch,
		Attempts: []Attempt{running}, PendingStop: TerminalReasonDeadline,
	}
	if err := draining.Validate(); err != nil {
		t.Fatalf("valid nonterminal drain rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*JobState)
		want   string
	}{
		{
			name: "terminal with running attempt",
			mutate: func(state *JobState) {
				state.Status = JobStatusFailed
				state.TerminalReason = TerminalReasonDeadline
				state.PendingStop = ""
			},
			want: "only a running job may have a current batch",
		},
		{
			name: "terminal with pending stop",
			mutate: func(state *JobState) {
				state.Status = JobStatusFailed
				state.TerminalReason = TerminalReasonDeadline
				state.CurrentBatch = nil
				state.Attempts = nil
			},
			want: "terminal job cannot retain pending stop",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cloneJobState(draining)
			test.mutate(&state)
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAttemptDispatchProvenanceFailsClosed(t *testing.T) {
	t.Parallel()

	base := Attempt{
		ID: "attempt-1", BatchID: "batch-1", WorkItemID: "work-1", RoleID: "role-1",
		AttemptNo: 1, Cycle: 1, StageID: "stage-1",
		Reservation: AttemptReservation{ModelCalls: 2, Tokens: 100, MaxOutputTokens: 50},
		Status:      AttemptStatusRunning,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid undispatched running attempt: %v", err)
	}

	runningDispatched := base
	runningDispatched.Dispatched = true
	if err := runningDispatched.Validate(); err == nil || !strings.Contains(err.Error(), "running attempt") {
		t.Fatalf("dispatched running error = %v", err)
	}

	undispatchedSuccess := base
	undispatchedSuccess.Status = AttemptStatusSucceeded
	if err := undispatchedSuccess.Validate(); err == nil || !strings.Contains(err.Error(), "must have crossed dispatch") {
		t.Fatalf("undispatched success error = %v", err)
	}

	undispatchedUsage := base
	undispatchedUsage.Status = AttemptStatusFailed
	undispatchedUsage.Error = "failed before dispatch"
	undispatchedUsage.Usage = Usage{ModelCalls: 1, InputTokens: 1}
	if err := undispatchedUsage.Validate(); err == nil || !strings.Contains(err.Error(), "cannot report provider usage") {
		t.Fatalf("undispatched usage error = %v", err)
	}

	ambiguous := base
	ambiguous.Status = AttemptStatusAmbiguous
	ambiguous.Error = "unknown writer outcome"
	if err := ambiguous.Validate(); err == nil || !strings.Contains(err.Error(), "must have crossed dispatch") {
		t.Fatalf("undispatched ambiguity error = %v", err)
	}
}

func TestBudgetIsExhaustedAtHardCap(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxCycles: 2, MaxAttempts: 3, MaxModelCalls: 4, MaxTokens: 10}
	if exceeded, dimension := budget.ExceededBy(Usage{Cycles: 1, Attempts: 2, ModelCalls: 3, InputTokens: 4, OutputTokens: 5}); exceeded {
		t.Fatalf("budget unexpectedly exhausted by %q", dimension)
	}
	if exceeded, dimension := budget.ExceededBy(Usage{Cycles: 2}); !exceeded || dimension != "cycles" {
		t.Fatalf("at cycle cap = (%t, %q), want (true, cycles)", exceeded, dimension)
	}
	if exceeded, dimension := budget.ExceededBy(Usage{InputTokens: 4, OutputTokens: 6}); !exceeded || dimension != "tokens" {
		t.Fatalf("at token cap = (%t, %q), want (true, tokens)", exceeded, dimension)
	}
}

func validDomainSpec(t *testing.T, workers int) JobSpec {
	t.Helper()
	return validDomainSpecForPreset(t, PresetGeneral, workers)
}

func validDomainSpecForPreset(t *testing.T, preset string, workers int) JobSpec {
	t.Helper()
	workflow, err := CompilePreset(preset, workers)
	if err != nil {
		t.Fatalf("CompilePreset(): %v", err)
	}
	return JobSpec{
		ID:        "job-1",
		Goal:      "Produce and verify a bounded result.",
		Preset:    workflow.Name,
		Workers:   workflow.Workers,
		Deadline:  time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		Budget:    Budget{MaxCycles: 8, MaxAttempts: 32, MaxModelCalls: 128, MaxTokens: 1_000_000},
		Route:     ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview"},
		Workflow:  WorkflowControlFromWorkflow(workflow),
		Authority: DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}
}
