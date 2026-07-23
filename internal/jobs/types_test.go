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
	workflow, err := CompilePreset(PresetGeneral, workers)
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
		Authority: DenyAllAuthority(),
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}
}
