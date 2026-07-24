package jobruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

func TestMaterializeAllBuiltInPresetsAtEveryWorkerCount(t *testing.T) {
	t.Parallel()

	for _, preset := range jobs.BuiltInPresetNames() {
		preset := preset
		for workers := jobs.MinWorkers; workers <= jobs.MaxWorkers; workers++ {
			workers := workers
			t.Run(fmt.Sprintf("%s/%d", preset, workers), func(t *testing.T) {
				t.Parallel()
				spec := materializeTestSpec(t, preset, workers)
				if err := ValidateWorkflowBinding(spec); err != nil {
					t.Fatalf("ValidateWorkflowBinding(): %v", err)
				}

				for index, stageID := range spec.Workflow.StageOrder {
					stage, err := StageAt(spec, index)
					if err != nil {
						t.Fatalf("StageAt(%d): %v", index, err)
					}
					if stage.ID != stageID {
						t.Fatalf("StageAt(%d).ID = %q, want %q", index, stage.ID, stageID)
					}
					for _, roleID := range stage.RoleIDs {
						role, err := RoleByID(spec, roleID)
						if err != nil {
							t.Fatalf("RoleByID(%q): %v", roleID, err)
						}
						if role.ID != roleID || strings.TrimSpace(role.Purpose) == "" {
							t.Fatalf("role = %#v", role)
						}
					}

					state := jobs.JobState{
						Spec:           spec,
						Status:         jobs.JobStatusRunning,
						Cycle:          1,
						NextStageIndex: index,
					}
					if index == 0 {
						state.Cycle = 0
					}
					first, err := MaterializeStageBatch(state, nil)
					if err != nil {
						t.Fatalf("MaterializeStageBatch(%d): %v", index, err)
					}
					second, err := MaterializeStageBatch(state, nil)
					if err != nil {
						t.Fatalf("second MaterializeStageBatch(%d): %v", index, err)
					}
					if !reflect.DeepEqual(first, second) {
						t.Fatalf("materialization is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
					}
					if first.StageID != stageID || first.Cycle != 1 || first.Barrier != jobs.BarrierAll {
						t.Fatalf("batch identity = %#v", first)
					}
					assertExactBatchRoles(t, first, stage.RoleIDs)
					if err := jobs.ValidateBatchForSpec(spec, first); err != nil {
						t.Fatalf("ValidateBatchForSpec(): %v", err)
					}
				}

				firstStage, err := StageAt(spec, 0)
				if err != nil {
					t.Fatal(err)
				}
				proposal := materializeContinueProposal(firstStage.RoleIDs, "cycle two")
				later, err := MaterializeStageBatch(jobs.JobState{
					Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 0,
				}, &proposal)
				if err != nil {
					t.Fatalf("later first stage: %v", err)
				}
				if later.Cycle != 2 {
					t.Fatalf("later cycle = %d, want 2", later.Cycle)
				}
				for _, item := range later.Items {
					if item.Objective != proposal.NextObjectives[item.RoleID] {
						t.Fatalf("objective for %q = %q", item.RoleID, item.Objective)
					}
				}
			})
		}
	}
}

func TestMaterializeStageBatchNarrowsAuthorityAndOwnsRuntimeFields(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetResearch, 2)
	spec.Authority = jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"read", "search"},
		ReadRoots:    []string{"/workspace"},
		WriteRoots:   []string{"/workspace/output"},
		NetworkHosts: []string{"docs.example", "search.example"},
		Providers:    []string{"qwen"},
	}
	roleAuthority := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/notes"},
		WriteRoots:   nil,
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"*"},
	}
	for index := range spec.Roles {
		spec.Roles[index].Authority = roleAuthority
	}
	if err := ValidateWorkflowBinding(spec); err != nil {
		t.Fatalf("explicit authority binding: %v", err)
	}

	stage, err := StageAt(spec, 0)
	if err != nil {
		t.Fatal(err)
	}
	proposal := materializeContinueProposal(stage.RoleIDs, `batch_id="evil" authority="*" provider="other"`)
	state := jobs.JobState{Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 0}
	first, err := MaterializeStageBatch(state, &proposal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MaterializeStageBatch(state, &proposal)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same proposal produced different batches")
	}
	wantAuthority, err := jobs.IntersectAuthority(spec.Authority, roleAuthority)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range first.Items {
		if !reflect.DeepEqual(item.Authority, wantAuthority) {
			t.Fatalf("authority for %q = %#v, want %#v", item.RoleID, item.Authority, wantAuthority)
		}
		if !strings.HasPrefix(item.ID, "item-") || strings.Contains(item.ID, "evil") {
			t.Fatalf("provider influenced item ID %q", item.ID)
		}
	}
	if !strings.HasPrefix(first.ID, "batch-") || strings.Contains(first.ID, "evil") {
		t.Fatalf("provider influenced batch ID %q", first.ID)
	}

	first.Items[0].Authority.Tools[0] = "mutated"
	first.Items[0].Objective = "mutated"
	proposal.NextObjectives[first.Items[0].RoleID] = "mutated proposal"
	if second.Items[0].Authority.Tools[0] != "read" || second.Items[0].Objective == "mutated" {
		t.Fatal("materialized batches alias each other")
	}
	if spec.Roles[0].Authority.Tools[0] != "read" {
		t.Fatal("materialized authority aliases persisted spec")
	}
}

func TestMaterializeStageBatchProposalScopeAndExactRoles(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetGeneral, 2)
	first, err := StageAt(spec, 0)
	if err != nil {
		t.Fatal(err)
	}
	valid := materializeContinueProposal(first.RoleIDs, "next")

	tests := []struct {
		name     string
		state    jobs.JobState
		proposal SupervisorProposal
		want     string
	}{
		{
			name:     "initial cycle",
			state:    jobs.JobState{Spec: spec, Status: jobs.JobStatusRunning, NextStageIndex: 0},
			proposal: valid,
			want:     "only for stage zero",
		},
		{
			name:     "intermediate stage",
			state:    jobs.JobState{Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 1},
			proposal: valid,
			want:     "only for stage zero",
		},
		{
			name:  "missing role",
			state: jobs.JobState{Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 0},
			proposal: SupervisorProposal{
				Kind: jobs.DecisionContinue, Reason: "next",
				NextObjectives: map[string]string{first.RoleIDs[0]: "one"},
			},
			want: "every allowed role",
		},
		{
			name:  "extra role",
			state: jobs.JobState{Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 0},
			proposal: SupervisorProposal{
				Kind: jobs.DecisionContinue, Reason: "next",
				NextObjectives: map[string]string{first.RoleIDs[0]: "one", first.RoleIDs[1]: "two", "evil.role": "three"},
			},
			want: "undeclared",
		},
		{
			name:     "non-continue",
			state:    jobs.JobState{Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 0},
			proposal: SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "done"},
			want:     "only a continue",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := MaterializeStageBatch(test.state, &test.proposal)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMaterializeDecisionOwnsNextBatchAndCanonicalFingerprint(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetCoding, 2)
	finalIndex := len(spec.Workflow.StageOrder) - 1
	base := jobs.JobState{
		Spec:           spec,
		Status:         jobs.JobStatusRunning,
		Cycle:          1,
		NextStageIndex: finalIndex,
		Attempts: []jobs.Attempt{
			{
				ID: "attempt-one", BatchID: "batch-old", WorkItemID: "item-old", RoleID: spec.Workflow.WorkerRoleIDs[0],
				AttemptNo: 1, Cycle: 1, StageID: spec.Workflow.StageOrder[0], Dispatched: true, Status: jobs.AttemptStatusSucceeded,
				Reservation: jobs.AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
				Result:      "same result\r\n", Fingerprint: "provider-controlled-one", Usage: jobs.Usage{ModelCalls: 1, OutputTokens: 10},
			},
		},
	}
	batch, err := MaterializeStageBatch(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.CurrentBatch = &batch
	firstStage, err := StageAt(spec, 0)
	if err != nil {
		t.Fatal(err)
	}
	proposal := materializeContinueProposal(firstStage.RoleIDs, `attempt_id="evil" provider_id="other"`)
	first, err := MaterializeDecision(base, proposal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MaterializeDecision(base, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("decision is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.NextBatch == nil || first.NextBatch.Cycle != 2 || first.NextBatch.StageID != firstStage.ID {
		t.Fatalf("next batch = %#v", first.NextBatch)
	}
	if !strings.HasPrefix(first.Fingerprint, "sha256:") || len(first.Fingerprint) != len("sha256:")+64 {
		t.Fatalf("fingerprint = %q", first.Fingerprint)
	}
	for _, item := range first.NextBatch.Items {
		if item.Objective != proposal.NextObjectives[item.RoleID] || strings.Contains(item.ID, "evil") {
			t.Fatalf("next item = %#v", item)
		}
	}

	providerMutated := base
	providerMutated.Attempts = slices.Clone(base.Attempts)
	providerMutated.Attempts[0].Fingerprint = "completely-different-provider-fingerprint"
	providerMutated.Attempts[0].Usage = jobs.Usage{ModelCalls: 99, InputTokens: 999}
	providerMutated.Attempts[0].Result = "  same result\n\n"
	providerDecision, err := MaterializeDecision(providerMutated, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if providerDecision.Fingerprint != first.Fingerprint {
		t.Fatalf("provider metadata changed fingerprint: %q != %q", providerDecision.Fingerprint, first.Fingerprint)
	}

	changed := base
	changed.Attempts = slices.Clone(base.Attempts)
	changed.Attempts[0].Result = "different result"
	changedDecision, err := MaterializeDecision(changed, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if changedDecision.Fingerprint == first.Fingerprint {
		t.Fatal("semantic result change did not change fingerprint")
	}

	afterSupervisorFinish := base
	afterSupervisorFinish.CurrentBatch = nil
	afterSupervisorFinish.NextStageIndex = len(spec.Workflow.StageOrder)
	afterSupervisorFinish.CompletedBatches = []jobs.CompletedBatch{
		{ID: batch.ID, StageID: batch.StageID, Cycle: batch.Cycle},
	}
	afterSupervisorFinish.Attempts = append(slices.Clone(base.Attempts), jobs.Attempt{
		ID: "attempt-supervisor", BatchID: batch.ID, WorkItemID: batch.Items[0].ID, RoleID: spec.Workflow.SupervisorRoleID,
		AttemptNo: 1, Cycle: 1, StageID: batch.StageID, Dispatched: true, Status: jobs.AttemptStatusSucceeded,
		Reservation: jobs.AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
		Result:      "provider supervisor output", Fingerprint: "provider-supervisor-fingerprint", Decision: &first,
	})
	recovered, err := MaterializeDecision(afterSupervisorFinish, proposal)
	if err != nil {
		t.Fatalf("recovery materialization: %v", err)
	}
	if !reflect.DeepEqual(recovered, first) {
		t.Fatalf("post-finish recovery changed decision:\nrecovered=%#v\nfirst=%#v", recovered, first)
	}

	afterContinue := jobs.JobState{
		Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: 0, LastDecision: &first,
	}
	rematerialized, err := MaterializeStageBatch(afterContinue, nil)
	if err != nil {
		t.Fatalf("rematerialize persisted continue: %v", err)
	}
	if !reflect.DeepEqual(rematerialized, *first.NextBatch) {
		t.Fatal("rematerialized batch differs from persisted decision")
	}

	first.NextBatch.Items[0].Objective = "mutated"
	if rematerialized.Items[0].Objective == "mutated" {
		t.Fatal("decision and returned batch alias each other")
	}

	complete, err := MaterializeDecision(base, SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if complete.NextBatch != nil || complete.Fingerprint == "" {
		t.Fatalf("complete decision = %#v", complete)
	}
}

func TestMaterializeDecisionCanPersistContinueAtCycleCap(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetGeneral, 1)
	finalIndex := len(spec.Workflow.StageOrder) - 1
	state := jobs.JobState{
		Spec: spec, Status: jobs.JobStatusRunning, Cycle: spec.Budget.MaxCycles, NextStageIndex: finalIndex,
	}
	batch, err := MaterializeStageBatch(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	state.CurrentBatch = &batch
	firstStage, err := StageAt(spec, 0)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := MaterializeDecision(state, materializeContinueProposal(firstStage.RoleIDs, "one more"))
	if err != nil {
		t.Fatalf("MaterializeDecision at cap: %v", err)
	}
	if decision.NextBatch == nil || decision.NextBatch.Cycle != spec.Budget.MaxCycles+1 {
		t.Fatalf("next batch = %#v", decision.NextBatch)
	}
}

func TestMaterializeDecisionRejectsEarlyCompletionBeforeMinimumCycles(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetResearch, 1)
	spec.MinCycles = 2
	finalIndex := len(spec.Workflow.StageOrder) - 1
	state := jobs.JobState{
		Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: finalIndex,
	}
	batch, err := MaterializeStageBatch(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	state.CurrentBatch = &batch

	_, err = MaterializeDecision(state, SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "too early"})
	if err == nil || !strings.Contains(err.Error(), "before min_cycles 2") {
		t.Fatalf("early complete error = %v", err)
	}
}

func TestMaterializeDecisionRejectsCompletionBeforeRuntimeFloor(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetResearch, 1)
	spec.NotBeforeComplete = spec.Deadline.Add(-2 * time.Hour)
	spec.CycleCadenceSeconds = uint64(time.Hour / time.Second)
	finalIndex := len(spec.Workflow.StageOrder) - 1
	state := jobs.JobState{
		Spec: spec, Status: jobs.JobStatusRunning, Cycle: 1, NextStageIndex: finalIndex,
	}
	batch, err := MaterializeStageBatch(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	state.CurrentBatch = &batch
	proposal := SupervisorProposal{Kind: jobs.DecisionComplete, Reason: "too early"}

	if _, err := MaterializeDecision(state, proposal); !errors.Is(err, ErrMinimumRuntimeNotReached) {
		t.Fatalf("completion without observation error = %v", err)
	}
	if _, err := MaterializeDecisionAt(state, proposal, spec.NotBeforeComplete.Add(-time.Nanosecond)); !errors.Is(err, ErrMinimumRuntimeNotReached) {
		t.Fatalf("early completion error = %v", err)
	}
	decision, err := MaterializeDecisionAt(state, proposal, spec.NotBeforeComplete)
	if err != nil {
		t.Fatalf("completion at floor: %v", err)
	}
	if decision.Kind != jobs.DecisionComplete {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestCanonicalPriorAttemptsIsStableDetachedAndBounded(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetGeneral, 1)
	attempts := make([]jobs.Attempt, 0, 142)
	for index := 1; index <= 140; index++ {
		attempts = append(attempts, jobs.Attempt{
			ID:         fmt.Sprintf("attempt-%03d", index),
			BatchID:    fmt.Sprintf("batch-%03d", index),
			WorkItemID: fmt.Sprintf("item-%03d", index),
			RoleID:     "general.primary",
			AttemptNo:  1,
			Cycle:      uint64(index),
			StageID:    spec.Workflow.StageOrder[0],
			Reservation: jobs.AttemptReservation{
				ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50,
			},
			Dispatched: true,
			Status:     jobs.AttemptStatusSucceeded,
			Result:     strings.Repeat("r", 32),
			Artifacts: []jobs.ArtifactRef{
				{ID: fmt.Sprintf("artifact-%03d", index), URI: fmt.Sprintf("file:///result/%03d", index)},
			},
		})
	}
	attempts = append(attempts,
		jobs.Attempt{
			ID: "attempt-oversized", BatchID: "batch-oversized", WorkItemID: "item-oversized", RoleID: "general.primary",
			AttemptNo: 1, Cycle: 141, StageID: spec.Workflow.StageOrder[0], Dispatched: true, Status: jobs.AttemptStatusSucceeded,
			Reservation: jobs.AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
			Result:      strings.Repeat("x", MaxInvocationPriorResultBytes+1),
		},
		jobs.Attempt{
			ID: "attempt-running", BatchID: "batch-running", WorkItemID: "item-running", RoleID: "general.primary",
			AttemptNo: 1, Cycle: 142, StageID: spec.Workflow.StageOrder[0], Status: jobs.AttemptStatusRunning,
			Reservation: jobs.AttemptReservation{ModelCalls: 1, Tokens: 100, MaxOutputTokens: 50},
		},
	)
	forward := jobs.JobState{Spec: spec, Attempts: attempts}
	reversed := jobs.JobState{Spec: spec, Attempts: slices.Clone(attempts)}
	slices.Reverse(reversed.Attempts)

	got := CanonicalPriorAttempts(forward)
	want := CanonicalPriorAttempts(reversed)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("canonical attempts depend on persisted slice order")
	}
	if len(got) != MaxInvocationPriorAttempts {
		t.Fatalf("attempt count = %d, want %d", len(got), MaxInvocationPriorAttempts)
	}
	if got[0].Cycle != 13 || got[len(got)-1].Cycle != 140 {
		t.Fatalf("retained cycle range = %d..%d, want 13..140", got[0].Cycle, got[len(got)-1].Cycle)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > MaxInvocationPriorPayloadBytes {
		t.Fatalf("prior payload = %d", len(payload))
	}
	invocation := validTestInvocation(InvocationKindWorker)
	invocation.PriorAttempts = got
	if err := invocation.Validate(); err != nil {
		t.Fatalf("bounded prior attempts fail invocation contract: %v", err)
	}

	got[0].Artifacts[0].URI = "mutated"
	if forward.Attempts[12].Artifacts[0].URI == "mutated" {
		t.Fatal("canonical attempts alias persisted state")
	}
}

func TestValidateWorkflowBindingRejectsStructuralDriftButAllowsBindingsAndCatalogOrder(t *testing.T) {
	t.Parallel()

	accepted := materializeTestSpec(t, jobs.PresetCoding, 2)
	accepted.Authority = jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{"qwen"}}
	for index := range accepted.Roles {
		accepted.Roles[index].Authority = jobs.Authority{Mode: jobs.AuthorityModeAllowList, Providers: []string{"qwen"}}
	}
	slices.Reverse(accepted.Roles)
	slices.Reverse(accepted.Stages)
	if err := ValidateWorkflowBinding(accepted); err != nil {
		t.Fatalf("explicit bindings or catalog declaration order rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*jobs.JobSpec)
	}{
		{
			name: "stage order",
			mutate: func(spec *jobs.JobSpec) {
				spec.Workflow.StageOrder[0], spec.Workflow.StageOrder[1] = spec.Workflow.StageOrder[1], spec.Workflow.StageOrder[0]
			},
		},
		{
			name: "worker role order",
			mutate: func(spec *jobs.JobSpec) {
				spec.Workflow.WorkerRoleIDs[0], spec.Workflow.WorkerRoleIDs[1] = spec.Workflow.WorkerRoleIDs[1], spec.Workflow.WorkerRoleIDs[0]
			},
		},
		{
			name: "role purpose",
			mutate: func(spec *jobs.JobSpec) {
				spec.Roles[0].Purpose += " drift"
			},
		},
		{
			name: "writer flag",
			mutate: func(spec *jobs.JobSpec) {
				for index := range spec.Roles {
					if spec.Roles[index].ID == "coding.implementer" {
						spec.Roles[index].Writer = false
					}
				}
			},
		},
		{
			name: "stage max workers",
			mutate: func(spec *jobs.JobSpec) {
				for index := range spec.Stages {
					if spec.Stages[index].ID == "implement" {
						spec.Stages[index].MaxWorkers = 2
					}
				}
			},
		},
		{
			name: "stage roles",
			mutate: func(spec *jobs.JobSpec) {
				for index := range spec.Stages {
					if spec.Stages[index].ID == "analyze" {
						spec.Stages[index].RoleIDs[0], spec.Stages[index].RoleIDs[1] = spec.Stages[index].RoleIDs[1], spec.Stages[index].RoleIDs[0]
					}
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := materializeTestSpec(t, jobs.PresetCoding, 2)
			test.mutate(&spec)
			if err := ValidateWorkflowBinding(spec); err == nil {
				t.Fatal("ValidateWorkflowBinding() accepted structural drift")
			}
		})
	}
}

func TestStageAtAndRoleByIDReturnDetachedValuesAndRejectUnknowns(t *testing.T) {
	t.Parallel()

	spec := materializeTestSpec(t, jobs.PresetGeneral, 2)
	stage, err := StageAt(spec, 0)
	if err != nil {
		t.Fatal(err)
	}
	originalRoleID := spec.Stages[0].RoleIDs[0]
	stage.RoleIDs[0] = "mutated"
	if spec.Stages[0].RoleIDs[0] != originalRoleID {
		t.Fatal("StageAt result aliases spec")
	}
	role, err := RoleByID(spec, originalRoleID)
	if err != nil {
		t.Fatal(err)
	}
	role.Authority = jobs.DenyAllAuthority()
	if _, err := StageAt(spec, -1); err == nil {
		t.Fatal("StageAt accepted negative index")
	}
	if _, err := StageAt(spec, len(spec.Workflow.StageOrder)); err == nil {
		t.Fatal("StageAt accepted end index")
	}
	if _, err := RoleByID(spec, "unknown.role"); err == nil {
		t.Fatal("RoleByID accepted unknown role")
	}
}

func materializeTestSpec(t *testing.T, preset string, workers int) jobs.JobSpec {
	t.Helper()
	workflow, err := jobs.CompilePreset(preset, workers)
	if err != nil {
		t.Fatal(err)
	}
	return jobs.JobSpec{
		ID:       "job-materialize",
		Goal:     "Produce one deterministic, bounded result.",
		Preset:   preset,
		Workers:  workers,
		Deadline: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
		Budget: jobs.Budget{
			MaxCycles: jobs.DefaultPresetMaxCycles, MaxAttempts: 1_000, MaxModelCalls: 1_000, MaxTokens: 10_000_000,
		},
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen",
			ModelID:    "qwen3.8-max-preview",
		},
		Workflow: jobs.WorkflowControlFromWorkflow(workflow),
		Authority: jobs.Authority{
			Mode:      jobs.AuthorityModeAllowList,
			Providers: []string{"qwen"},
		},
		Roles:  workflow.Roles,
		Stages: workflow.Stages,
	}
}

func materializeContinueProposal(roleIDs []string, objective string) SupervisorProposal {
	objectives := make(map[string]string, len(roleIDs))
	for _, roleID := range roleIDs {
		objectives[roleID] = objective + " for " + roleID
	}
	return SupervisorProposal{
		Kind:           jobs.DecisionContinue,
		Reason:         "continue with one bounded cycle",
		NextObjectives: objectives,
	}
}

func assertExactBatchRoles(t *testing.T, batch jobs.WorkBatch, want []string) {
	t.Helper()
	got := make([]string, len(batch.Items))
	for index, item := range batch.Items {
		got[index] = item.RoleID
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("batch roles = %v, want %v", got, want)
	}
}
