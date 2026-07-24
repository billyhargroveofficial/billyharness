package jobruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

var (
	ErrMinimumCyclesNotReached  = errors.New("minimum workflow cycles not reached")
	ErrMinimumRuntimeNotReached = errors.New("minimum job runtime not reached")
)

// ValidateWorkflowBinding proves that a persisted job still describes the
// built-in preset it names. Role authority is deliberately excluded from the
// comparison: authority bindings are supplied when the job is created and are
// narrowed again while materializing each item.
func ValidateWorkflowBinding(spec jobs.JobSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("job spec: %w", err)
	}
	compiled, err := jobs.CompilePreset(spec.Preset, spec.Workers)
	if err != nil {
		return fmt.Errorf("compile persisted preset: %w", err)
	}
	wantControl := jobs.WorkflowControlFromWorkflow(compiled)
	if spec.Workflow.Version != wantControl.Version ||
		spec.Workflow.SupervisorRoleID != wantControl.SupervisorRoleID ||
		spec.Workflow.ReducerRoleID != wantControl.ReducerRoleID ||
		!slices.Equal(spec.Workflow.StageOrder, wantControl.StageOrder) ||
		!slices.Equal(spec.Workflow.WorkerRoleIDs, wantControl.WorkerRoleIDs) {
		return errors.New("persisted workflow metadata differs from compiled preset")
	}

	if len(spec.Roles) != len(compiled.Roles) {
		return fmt.Errorf("persisted role catalog has %d roles, compiled preset has %d", len(spec.Roles), len(compiled.Roles))
	}
	for _, want := range compiled.Roles {
		got, ok := roleByIDUnchecked(spec, want.ID)
		if !ok {
			return fmt.Errorf("persisted workflow is missing role %q", want.ID)
		}
		if got.Purpose != want.Purpose || got.Writer != want.Writer {
			return fmt.Errorf("persisted role %q purpose or writer flag differs from compiled preset", want.ID)
		}
	}

	if len(spec.Stages) != len(compiled.Stages) {
		return fmt.Errorf("persisted stage catalog has %d stages, compiled preset has %d", len(spec.Stages), len(compiled.Stages))
	}
	for _, want := range compiled.Stages {
		got, ok := stageByIDUnchecked(spec, want.ID)
		if !ok {
			return fmt.Errorf("persisted workflow is missing stage %q", want.ID)
		}
		if got.MaxWorkers != want.MaxWorkers || got.Barrier != want.Barrier || !slices.Equal(got.RoleIDs, want.RoleIDs) {
			return fmt.Errorf("persisted stage %q differs from compiled preset", want.ID)
		}
	}
	return nil
}

// StageAt returns a detached copy of the stage at the persisted workflow
// cursor, never relying on declaration or map iteration order.
func StageAt(spec jobs.JobSpec, index int) (jobs.StageSpec, error) {
	if err := ValidateWorkflowBinding(spec); err != nil {
		return jobs.StageSpec{}, err
	}
	if index < 0 || index >= len(spec.Workflow.StageOrder) {
		return jobs.StageSpec{}, fmt.Errorf("stage index %d is outside workflow", index)
	}
	stageID := spec.Workflow.StageOrder[index]
	stage, ok := stageByIDUnchecked(spec, stageID)
	if !ok {
		return jobs.StageSpec{}, fmt.Errorf("workflow stage %q is missing from the catalog", stageID)
	}
	stage.RoleIDs = slices.Clone(stage.RoleIDs)
	return stage, nil
}

// RoleByID returns a detached copy of a role from the validated persisted
// catalog.
func RoleByID(spec jobs.JobSpec, roleID string) (jobs.RoleSpec, error) {
	if err := ValidateWorkflowBinding(spec); err != nil {
		return jobs.RoleSpec{}, err
	}
	role, ok := roleByIDUnchecked(spec, roleID)
	if !ok {
		return jobs.RoleSpec{}, fmt.Errorf("role %q is not declared by the workflow", roleID)
	}
	role.Authority = cloneMaterializedAuthority(role.Authority)
	return role, nil
}

// MaterializeStageBatch constructs the one exact batch selected by the
// durable stage cursor. A supervisor proposal can provide objectives only for
// the first stage of a later cycle. Every identity and authority field remains
// runtime-owned.
func MaterializeStageBatch(state jobs.JobState, proposal *SupervisorProposal) (jobs.WorkBatch, error) {
	if err := ValidateWorkflowBinding(state.Spec); err != nil {
		return jobs.WorkBatch{}, err
	}
	if state.Status != jobs.JobStatusRunning || state.CurrentBatch != nil || state.CancelRequested {
		return jobs.WorkBatch{}, errors.New("job is not ready to materialize a stage batch")
	}
	stage, err := StageAt(state.Spec, state.NextStageIndex)
	if err != nil {
		return jobs.WorkBatch{}, err
	}
	cycle := state.Cycle
	if state.NextStageIndex == 0 {
		cycle++
	}
	if cycle == 0 {
		return jobs.WorkBatch{}, errors.New("stage batch cycle must be positive")
	}
	if proposal != nil && (state.NextStageIndex != 0 || cycle <= 1) {
		return jobs.WorkBatch{}, errors.New("supervisor objectives may be used only for stage zero of a later cycle")
	}

	if state.NextStageIndex == 0 && state.Cycle > 0 && state.LastDecision != nil && state.LastDecision.Kind == jobs.DecisionContinue {
		if state.LastDecision.NextBatch == nil {
			return jobs.WorkBatch{}, errors.New("persisted continue decision has no next batch")
		}
		persisted := cloneMaterializedBatch(*state.LastDecision.NextBatch)
		effectiveProposal := proposal
		if effectiveProposal == nil {
			objectives := make(map[string]string, len(persisted.Items))
			for _, item := range persisted.Items {
				objectives[item.RoleID] = item.Objective
			}
			effectiveProposal = &SupervisorProposal{
				Kind:           jobs.DecisionContinue,
				Reason:         state.LastDecision.Reason,
				NextObjectives: objectives,
			}
		}
		batch, err := materializeBatch(state.Spec, stage, cycle, effectiveProposal, false)
		if err != nil {
			return jobs.WorkBatch{}, err
		}
		if !equalMaterializedBatch(persisted, batch) {
			return jobs.WorkBatch{}, errors.New("materialized batch differs from the persisted continue decision")
		}
		return persisted, nil
	}
	return materializeBatch(state.Spec, stage, cycle, proposal, false)
}

// MaterializeDecision converts the narrow model proposal into a runtime-owned
// durable decision. It is valid while the final supervisor batch is active or
// immediately after that exact batch has completed during recovery.
func MaterializeDecision(state jobs.JobState, proposal SupervisorProposal) (jobs.Decision, error) {
	return MaterializeDecisionAt(state, proposal, time.Time{})
}

// MaterializeDecisionAt additionally enforces the scheduler-owned wall-clock
// earliest-success gate. observedAt is required only when the persisted spec
// has a non-zero NotBeforeComplete; callers without that feature retain the
// original deterministic API through MaterializeDecision.
func MaterializeDecisionAt(state jobs.JobState, proposal SupervisorProposal, observedAt time.Time) (jobs.Decision, error) {
	if err := ValidateWorkflowBinding(state.Spec); err != nil {
		return jobs.Decision{}, err
	}
	// Cancellation/deadline may become pending while the supervisor call is in
	// flight. Its factual proposal still has to be materialized and persisted;
	// the reducer applies the higher-priority pending stop after the finish.
	if state.Status != jobs.JobStatusRunning || state.Cycle == 0 {
		return jobs.Decision{}, errors.New("job is not ready to materialize a supervisor decision")
	}
	finalIndex := len(state.Spec.Workflow.StageOrder) - 1
	finalStage, err := StageAt(state.Spec, finalIndex)
	if err != nil {
		return jobs.Decision{}, err
	}
	expectedSupervisorBatch, err := materializeBatch(state.Spec, finalStage, state.Cycle, nil, false)
	if err != nil {
		return jobs.Decision{}, err
	}
	if !atSupervisorBoundary(state, finalIndex, expectedSupervisorBatch) {
		return jobs.Decision{}, errors.New("decision requires the final supervisor boundary")
	}
	firstStage, err := StageAt(state.Spec, 0)
	if err != nil {
		return jobs.Decision{}, err
	}
	if err := proposal.Validate(firstStage.RoleIDs); err != nil {
		return jobs.Decision{}, fmt.Errorf("supervisor proposal: %w", err)
	}
	if proposal.Kind == jobs.DecisionComplete && state.Cycle < state.Spec.EffectiveMinCycles() {
		return jobs.Decision{}, fmt.Errorf(
			"%w: supervisor cannot complete at cycle %d before min_cycles %d",
			ErrMinimumCyclesNotReached,
			state.Cycle,
			state.Spec.EffectiveMinCycles(),
		)
	}
	if proposal.Kind == jobs.DecisionComplete && !state.Spec.NotBeforeComplete.IsZero() {
		if observedAt.IsZero() {
			return jobs.Decision{}, fmt.Errorf("%w: completion observation time is required", ErrMinimumRuntimeNotReached)
		}
		if observedAt.Before(state.Spec.NotBeforeComplete) {
			return jobs.Decision{}, fmt.Errorf(
				"%w: supervisor cannot complete at %s before not_before_complete %s",
				ErrMinimumRuntimeNotReached,
				observedAt.UTC().Format(time.RFC3339Nano),
				state.Spec.NotBeforeComplete.Format(time.RFC3339Nano),
			)
		}
	}

	decision := jobs.Decision{
		Kind:        proposal.Kind,
		Reason:      proposal.Reason,
		Fingerprint: stagnationFingerprint(state),
	}
	if proposal.Kind == jobs.DecisionContinue {
		next, err := materializeBatch(state.Spec, firstStage, state.Cycle+1, &proposal, true)
		if err != nil {
			return jobs.Decision{}, err
		}
		decision.NextBatch = &next
	}
	if err := decision.Validate(); err != nil {
		return jobs.Decision{}, fmt.Errorf("materialized decision: %w", err)
	}
	return decision, nil
}

// CanonicalPriorAttempts returns detached, terminal attempts in stable
// workflow order. It retains the newest whole attempts that fit every bound in
// the Invocation contract; oversized or invalid historical entries are not
// allowed to make a new invocation unbounded.
func CanonicalPriorAttempts(state jobs.JobState) []jobs.Attempt {
	attempts := make([]jobs.Attempt, 0, len(state.Attempts))
	for _, attempt := range state.Attempts {
		if !terminalInvocationAttemptStatus(attempt.Status) || attempt.Validate() != nil || validateArtifactRefs(attempt.Artifacts) != nil {
			continue
		}
		attempts = append(attempts, cloneMaterializedAttempt(attempt))
	}
	stageIndex := make(map[string]int, len(state.Spec.Workflow.StageOrder))
	for index, stageID := range state.Spec.Workflow.StageOrder {
		stageIndex[stageID] = index
	}
	sort.Slice(attempts, func(i, j int) bool {
		return compareCanonicalAttempts(attempts[i], attempts[j], stageIndex) < 0
	})

	selected := make([]jobs.Attempt, 0, min(len(attempts), MaxInvocationPriorAttempts))
	textBytes, artifactCount := 0, 0
	for index := len(attempts) - 1; index >= 0 && len(selected) < MaxInvocationPriorAttempts; index-- {
		candidate := attempts[index]
		candidateText := len(candidate.Result) + len(candidate.Error)
		candidateArtifacts := len(candidate.Artifacts)
		if textBytes+candidateText > MaxInvocationPriorResultBytes || artifactCount+candidateArtifacts > MaxInvocationArtifacts {
			continue
		}
		probe := append(slices.Clone(selected), candidate)
		payload, err := json.Marshal(probe)
		if err != nil || len(payload) > MaxInvocationPriorPayloadBytes {
			continue
		}
		selected = probe
		textBytes += candidateText
		artifactCount += candidateArtifacts
	}
	sort.Slice(selected, func(i, j int) bool {
		return compareCanonicalAttempts(selected[i], selected[j], stageIndex) < 0
	})
	return selected
}

func materializeBatch(spec jobs.JobSpec, stage jobs.StageSpec, cycle uint64, proposal *SupervisorProposal, allowBeyondCycleCap bool) (jobs.WorkBatch, error) {
	if cycle == 0 {
		return jobs.WorkBatch{}, errors.New("batch cycle must be positive")
	}
	if proposal != nil {
		if proposal.Kind != jobs.DecisionContinue {
			return jobs.WorkBatch{}, errors.New("only a continue proposal can supply next-stage objectives")
		}
		if err := proposal.Validate(stage.RoleIDs); err != nil {
			return jobs.WorkBatch{}, fmt.Errorf("supervisor proposal: %w", err)
		}
		if len(proposal.NextObjectives) != len(stage.RoleIDs) {
			return jobs.WorkBatch{}, errors.New("continue proposal must provide objectives for every exact next-stage role")
		}
		for _, roleID := range stage.RoleIDs {
			if _, ok := proposal.NextObjectives[roleID]; !ok {
				return jobs.WorkBatch{}, fmt.Errorf("continue proposal is missing objective for role %q", roleID)
			}
		}
	}

	batch := jobs.WorkBatch{
		ID:      portableMaterializedID("batch", spec.ID, strconv.FormatUint(cycle, 10), stage.ID),
		StageID: stage.ID,
		Cycle:   cycle,
		Barrier: jobs.BarrierAll,
		Items:   make([]jobs.WorkItem, 0, len(stage.RoleIDs)),
	}
	for _, roleID := range stage.RoleIDs {
		role, ok := roleByIDUnchecked(spec, roleID)
		if !ok {
			return jobs.WorkBatch{}, fmt.Errorf("stage %q references missing role %q", stage.ID, roleID)
		}
		authority, err := jobs.IntersectAuthority(spec.Authority, role.Authority)
		if err != nil {
			return jobs.WorkBatch{}, fmt.Errorf("intersect authority for role %q: %w", roleID, err)
		}
		objective := fmt.Sprintf("Cycle %d, stage %q: %s", cycle, stage.ID, role.Purpose)
		if proposal != nil {
			objective = proposal.NextObjectives[roleID]
		}
		batch.Items = append(batch.Items, jobs.WorkItem{
			ID:        portableMaterializedID("item", batch.ID, roleID),
			RoleID:    roleID,
			Objective: objective,
			Authority: authority,
		})
	}
	sort.Slice(batch.Items, func(i, j int) bool { return batch.Items[i].ID < batch.Items[j].ID })

	validationSpec := spec
	if allowBeyondCycleCap && cycle > validationSpec.Budget.MaxCycles {
		validationSpec.Budget.MaxCycles = cycle
	}
	if err := jobs.ValidateBatchForSpec(validationSpec, batch); err != nil {
		return jobs.WorkBatch{}, fmt.Errorf("materialized batch: %w", err)
	}
	return batch, nil
}

func atSupervisorBoundary(state jobs.JobState, finalIndex int, expected jobs.WorkBatch) bool {
	if state.CurrentBatch != nil {
		return state.NextStageIndex == finalIndex &&
			equalMaterializedBatch(*state.CurrentBatch, expected)
	}
	if state.NextStageIndex != len(state.Spec.Workflow.StageOrder) || len(state.CompletedBatches) == 0 {
		return false
	}
	last := state.CompletedBatches[len(state.CompletedBatches)-1]
	return last.ID == expected.ID && last.StageID == expected.StageID && last.Cycle == expected.Cycle
}

func stagnationFingerprint(state jobs.JobState) string {
	type canonicalArtifact struct {
		URI       string `json:"uri"`
		SHA256    string `json:"sha256,omitempty"`
		MediaType string `json:"media_type,omitempty"`
	}
	type canonicalAttempt struct {
		StageID   string              `json:"stage_id"`
		RoleID    string              `json:"role_id"`
		AttemptNo uint64              `json:"attempt_no"`
		Status    jobs.AttemptStatus  `json:"status"`
		Result    string              `json:"result,omitempty"`
		Error     string              `json:"error,omitempty"`
		Artifacts []canonicalArtifact `json:"artifacts,omitempty"`
	}

	canonical := make([]canonicalAttempt, 0)
	finalStageID := state.Spec.Workflow.StageOrder[len(state.Spec.Workflow.StageOrder)-1]
	for _, attempt := range state.Attempts {
		if attempt.Cycle != state.Cycle || !terminalInvocationAttemptStatus(attempt.Status) {
			continue
		}
		// The first materialization happens before the successful supervisor
		// finish is persisted. Excluding every supervisor-stage attempt keeps
		// replay/recovery materialization identical and prevents the model's
		// proposal text from feeding back into its own progress fingerprint.
		if attempt.StageID == finalStageID && attempt.RoleID == state.Spec.Workflow.SupervisorRoleID {
			continue
		}
		artifacts := make([]canonicalArtifact, 0, len(attempt.Artifacts))
		for _, artifact := range attempt.Artifacts {
			artifacts = append(artifacts, canonicalArtifact{
				URI:       strings.TrimSpace(artifact.URI),
				SHA256:    strings.TrimSpace(artifact.SHA256),
				MediaType: strings.TrimSpace(artifact.MediaType),
			})
		}
		sort.Slice(artifacts, func(i, j int) bool {
			left, _ := json.Marshal(artifacts[i])
			right, _ := json.Marshal(artifacts[j])
			return string(left) < string(right)
		})
		canonical = append(canonical, canonicalAttempt{
			StageID:   attempt.StageID,
			RoleID:    attempt.RoleID,
			AttemptNo: attempt.AttemptNo,
			Status:    attempt.Status,
			Result:    normalizeAttemptText(attempt.Result),
			Error:     normalizeAttemptText(attempt.Error),
			Artifacts: artifacts,
		})
	}
	stageIndex := make(map[string]int, len(state.Spec.Workflow.StageOrder))
	for index, stageID := range state.Spec.Workflow.StageOrder {
		stageIndex[stageID] = index
	}
	sort.Slice(canonical, func(i, j int) bool {
		leftStage, leftKnown := stageIndex[canonical[i].StageID]
		rightStage, rightKnown := stageIndex[canonical[j].StageID]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftStage != rightStage {
			return leftStage < rightStage
		}
		if canonical[i].StageID != canonical[j].StageID {
			return canonical[i].StageID < canonical[j].StageID
		}
		if canonical[i].RoleID != canonical[j].RoleID {
			return canonical[i].RoleID < canonical[j].RoleID
		}
		if canonical[i].AttemptNo != canonical[j].AttemptNo {
			return canonical[i].AttemptNo < canonical[j].AttemptNo
		}
		left, _ := json.Marshal(canonical[i])
		right, _ := json.Marshal(canonical[j])
		return string(left) < string(right)
	})
	payload, _ := json.Marshal(canonical)
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func normalizeAttemptText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func portableMaterializedID(kind string, parts ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("billyharness/jobruntime/materialize/v1\x00"))
	_, _ = hash.Write([]byte(kind))
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return kind + "-" + hex.EncodeToString(hash.Sum(nil))
}

func compareCanonicalAttempts(left, right jobs.Attempt, stageIndex map[string]int) int {
	leftStage, leftKnown := stageIndex[left.StageID]
	rightStage, rightKnown := stageIndex[right.StageID]
	leftKnownKey, rightKnownKey := "1", "1"
	if leftKnown {
		leftKnownKey = "0"
	}
	if rightKnown {
		rightKnownKey = "0"
	}
	leftKey := []string{
		fmt.Sprintf("%020d", left.Cycle),
		leftKnownKey + fmt.Sprintf("%020d", leftStage),
		left.StageID,
		left.RoleID,
		left.BatchID,
		left.WorkItemID,
		fmt.Sprintf("%020d", left.AttemptNo),
		left.ID,
	}
	rightKey := []string{
		fmt.Sprintf("%020d", right.Cycle),
		rightKnownKey + fmt.Sprintf("%020d", rightStage),
		right.StageID,
		right.RoleID,
		right.BatchID,
		right.WorkItemID,
		fmt.Sprintf("%020d", right.AttemptNo),
		right.ID,
	}
	for index := range leftKey {
		if leftKey[index] < rightKey[index] {
			return -1
		}
		if leftKey[index] > rightKey[index] {
			return 1
		}
	}
	return 0
}

func roleByIDUnchecked(spec jobs.JobSpec, roleID string) (jobs.RoleSpec, bool) {
	for _, role := range spec.Roles {
		if role.ID == roleID {
			return role, true
		}
	}
	return jobs.RoleSpec{}, false
}

func stageByIDUnchecked(spec jobs.JobSpec, stageID string) (jobs.StageSpec, bool) {
	for _, stage := range spec.Stages {
		if stage.ID == stageID {
			return stage, true
		}
	}
	return jobs.StageSpec{}, false
}

func cloneMaterializedAuthority(authority jobs.Authority) jobs.Authority {
	out := authority
	out.Tools = slices.Clone(authority.Tools)
	out.ReadRoots = slices.Clone(authority.ReadRoots)
	out.WriteRoots = slices.Clone(authority.WriteRoots)
	out.NetworkHosts = slices.Clone(authority.NetworkHosts)
	out.Providers = slices.Clone(authority.Providers)
	return out
}

func cloneMaterializedBatch(batch jobs.WorkBatch) jobs.WorkBatch {
	out := batch
	out.Items = make([]jobs.WorkItem, len(batch.Items))
	for index, item := range batch.Items {
		out.Items[index] = item
		out.Items[index].Authority = cloneMaterializedAuthority(item.Authority)
	}
	return out
}

func cloneMaterializedAttempt(attempt jobs.Attempt) jobs.Attempt {
	out := attempt
	out.Artifacts = slices.Clone(attempt.Artifacts)
	sort.Slice(out.Artifacts, func(i, j int) bool { return out.Artifacts[i].ID < out.Artifacts[j].ID })
	if attempt.Decision != nil {
		decision := *attempt.Decision
		if attempt.Decision.NextBatch != nil {
			batch := cloneMaterializedBatch(*attempt.Decision.NextBatch)
			decision.NextBatch = &batch
		}
		out.Decision = &decision
	}
	return out
}

func equalMaterializedBatch(left, right jobs.WorkBatch) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
