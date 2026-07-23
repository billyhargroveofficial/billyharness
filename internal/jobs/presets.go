package jobs

import (
	"fmt"
	"slices"
)

const (
	PresetGeneral  = "general"
	PresetResearch = "research"
	PresetCoding   = "coding"
	PresetDebug    = "debug"
	PresetReview   = "review"
	PresetPlanning = "planning"
	PresetWriting  = "writing"
	PresetCompare  = "compare"

	// DefaultPresetMaxCycles bounds adaptive supervisor continuation. Runtime
	// deadline and budget checks may stop a job earlier.
	DefaultPresetMaxCycles = 8

	presetReducerRoleID    = "control.reducer"
	presetSupervisorRoleID = "control.supervisor"
)

type presetRoleTemplate struct {
	id      string
	purpose string
	writer  bool
}

type presetStageTemplate struct {
	id         string
	workerPool bool
	roleIDs    []string
}

type presetDefinition struct {
	name        string
	workerRoles []presetRoleTemplate
	extraRoles  []presetRoleTemplate
	stages      []presetStageTemplate
}

var builtInPresetDefinitions = []presetDefinition{
	{
		name: PresetGeneral,
		workerRoles: []presetRoleTemplate{
			{id: "general.primary", purpose: "Develop a complete primary solution inside the immutable job goal."},
			{id: "general.alternative", purpose: "Develop an independent alternative and expose assumptions in the primary direction."},
			{id: "general.critic", purpose: "Search adversarially for errors, omissions, and goal drift."},
			{id: "general.verifier", purpose: "Check important claims and completion criteria against available evidence."},
		},
		stages: standardReadOnlyStages("explore"),
	},
	{
		name: PresetResearch,
		workerRoles: []presetRoleTemplate{
			{id: "research.primary", purpose: "Investigate the question broadly and record evidence, uncertainty, and source provenance."},
			{id: "research.falsifier", purpose: "Seek disconfirming evidence and plausible alternative explanations."},
			{id: "research.provenance", purpose: "Audit source quality, dates, independence, and evidentiary lineage."},
			{id: "research.independent", purpose: "Run an independent research path to reduce shared anchoring."},
		},
		stages: standardReadOnlyStages("investigate"),
	},
	{
		name: PresetCoding,
		workerRoles: []presetRoleTemplate{
			{id: "coding.codebase", purpose: "Inspect the codebase and identify the smallest correct implementation boundary."},
			{id: "coding.design", purpose: "Challenge the proposed design for correctness, compatibility, and unnecessary scope."},
			{id: "coding.tests", purpose: "Design focused verification and identify regression risks before implementation."},
			{id: "coding.security", purpose: "Review authority, input, concurrency, and failure-mode risks relevant to the change."},
		},
		extraRoles: []presetRoleTemplate{
			{id: "coding.implementer", purpose: "Apply the accepted implementation in the authorized workspace and keep the patch scoped.", writer: true},
		},
		stages: []presetStageTemplate{
			{id: "analyze", workerPool: true},
			{id: "implement", roleIDs: []string{"coding.implementer"}},
			{id: "verify", workerPool: true},
			{id: "reduce", roleIDs: []string{presetReducerRoleID}},
			{id: "supervise", roleIDs: []string{presetSupervisorRoleID}},
		},
	},
	{
		name: PresetDebug,
		workerRoles: []presetRoleTemplate{
			{id: "debug.diagnostician", purpose: "Form and rank root-cause hypotheses from reproducible evidence."},
			{id: "debug.challenger", purpose: "Try to falsify the leading diagnosis and find overlooked causes."},
			{id: "debug.reproducer", purpose: "Minimize the reproduction and distinguish symptoms from the initiating fault."},
			{id: "debug.regression", purpose: "Inspect history, boundaries, and adjacent behavior for regression risk."},
		},
		extraRoles: []presetRoleTemplate{
			{id: "debug.fixer", purpose: "Apply the smallest authorized fix for the accepted diagnosis.", writer: true},
		},
		stages: []presetStageTemplate{
			{id: "diagnose", workerPool: true},
			{id: "repair", roleIDs: []string{"debug.fixer"}},
			{id: "verify", workerPool: true},
			{id: "reduce", roleIDs: []string{presetReducerRoleID}},
			{id: "supervise", roleIDs: []string{presetSupervisorRoleID}},
		},
	},
	{
		name: PresetReview,
		workerRoles: []presetRoleTemplate{
			{id: "review.correctness", purpose: "Review correctness and report only concrete, actionable findings."},
			{id: "review.tests", purpose: "Review test coverage, failure paths, and missing regression checks."},
			{id: "review.security", purpose: "Review trust boundaries, authority, unsafe inputs, and abuse paths."},
			{id: "review.maintainability", purpose: "Review clarity, compatibility, and unnecessary long-term complexity."},
		},
		stages: standardReadOnlyStages("review"),
	},
	{
		name: PresetPlanning,
		workerRoles: []presetRoleTemplate{
			{id: "planning.primary", purpose: "Produce a concrete, ordered plan with explicit outcomes and verification."},
			{id: "planning.critic", purpose: "Challenge assumptions, sequencing, and hidden scope in the primary plan."},
			{id: "planning.dependencies", purpose: "Identify dependencies, ownership boundaries, and blocking decisions."},
			{id: "planning.risks", purpose: "Identify failure modes, rollback needs, and cheap de-risking steps."},
		},
		stages: standardReadOnlyStages("plan"),
	},
	{
		name: PresetWriting,
		workerRoles: []presetRoleTemplate{
			{id: "writing.content", purpose: "Develop accurate content, structure, and supporting material for the requested artifact."},
			{id: "writing.facts", purpose: "Check factual claims, internal consistency, and unsupported certainty."},
			{id: "writing.audience", purpose: "Review usefulness, tone, and comprehension for the intended audience."},
			{id: "writing.structure", purpose: "Critique organization, omissions, repetition, and narrative flow."},
		},
		extraRoles: []presetRoleTemplate{
			{id: "writing.author", purpose: "Write the canonical artifact using the accepted evidence and review.", writer: true},
		},
		stages: []presetStageTemplate{
			{id: "develop", workerPool: true},
			{id: "draft", roleIDs: []string{"writing.author"}},
			{id: "review", workerPool: true},
			{id: "reduce", roleIDs: []string{presetReducerRoleID}},
			{id: "supervise", roleIDs: []string{presetSupervisorRoleID}},
		},
	},
	{
		name: PresetCompare,
		workerRoles: []presetRoleTemplate{
			{id: "compare.criteria", purpose: "Define decision-relevant criteria and evaluate every option consistently."},
			{id: "compare.independent", purpose: "Perform an independent comparison to reduce anchoring and ordering bias."},
			{id: "compare.risks", purpose: "Compare downside, uncertainty, reversibility, and operational risk."},
			{id: "compare.evidence", purpose: "Audit whether claimed differences are supported by comparable evidence."},
		},
		stages: standardReadOnlyStages("evaluate"),
	},
}

func standardReadOnlyStages(workStageID string) []presetStageTemplate {
	return []presetStageTemplate{
		{id: workStageID, workerPool: true},
		{id: "reduce", roleIDs: []string{presetReducerRoleID}},
		{id: "supervise", roleIDs: []string{presetSupervisorRoleID}},
	}
}

// BuiltInPresetNames returns the stable user-facing preset order.
func BuiltInPresetNames() []string {
	names := make([]string, 0, len(builtInPresetDefinitions))
	for _, definition := range builtInPresetDefinitions {
		names = append(names, definition.name)
	}
	return names
}

// CompilePreset expands a built-in preset into the common provider-neutral
// workflow model. workers limits concurrent worker attempts; reducer and
// supervisor control stages are single-role stages and do not consume it.
func CompilePreset(name string, workers int) (WorkflowSpec, error) {
	if workers < MinWorkflowWorkers || workers > MaxWorkflowWorkers {
		return WorkflowSpec{}, fmt.Errorf(
			"preset workers must be between %d and %d, got %d",
			MinWorkflowWorkers,
			MaxWorkflowWorkers,
			workers,
		)
	}

	for _, definition := range builtInPresetDefinitions {
		if definition.name == name {
			return compilePresetDefinition(definition, workers)
		}
	}
	return WorkflowSpec{}, fmt.Errorf(
		"unknown preset %q (available: %v)",
		name,
		BuiltInPresetNames(),
	)
}

func compilePresetDefinition(definition presetDefinition, workers int) (WorkflowSpec, error) {
	if len(definition.workerRoles) < workers {
		return WorkflowSpec{}, fmt.Errorf(
			"preset %q declares %d worker roles for worker limit %d",
			definition.name,
			len(definition.workerRoles),
			workers,
		)
	}

	selectedWorkers := slices.Clone(definition.workerRoles[:workers])
	roleTemplates := append(slices.Clone(selectedWorkers), definition.extraRoles...)
	roleTemplates = append(roleTemplates,
		presetRoleTemplate{
			id:      presetReducerRoleID,
			purpose: "Deterministically merge completed worker artifacts without expanding the job goal or authority.",
		},
		presetRoleTemplate{
			id:      presetSupervisorRoleID,
			purpose: "Review progress and propose only a bounded continue, complete, wait, or blocked decision.",
		},
	)

	roles := make([]RoleSpec, 0, len(roleTemplates))
	workerRoleIDs := make([]string, 0, len(selectedWorkers)+len(definition.extraRoles))
	for _, role := range roleTemplates {
		roles = append(roles, RoleSpec{
			ID:        role.id,
			Purpose:   role.purpose,
			Authority: DenyAllAuthority(),
			Writer:    role.writer,
		})
		if role.id != presetReducerRoleID && role.id != presetSupervisorRoleID {
			workerRoleIDs = append(workerRoleIDs, role.id)
		}
	}
	slices.SortFunc(roles, func(left, right RoleSpec) int {
		return compareStrings(left.ID, right.ID)
	})
	slices.Sort(workerRoleIDs)

	stages := make([]StageSpec, 0, len(definition.stages))
	stageOrder := make([]string, 0, len(definition.stages))
	selectedWorkerIDs := make([]string, 0, len(selectedWorkers))
	for _, role := range selectedWorkers {
		selectedWorkerIDs = append(selectedWorkerIDs, role.id)
	}
	slices.Sort(selectedWorkerIDs)
	for _, template := range definition.stages {
		roleIDs := slices.Clone(template.roleIDs)
		if template.workerPool {
			roleIDs = slices.Clone(selectedWorkerIDs)
		}
		slices.Sort(roleIDs)
		stages = append(stages, StageSpec{
			ID:         template.id,
			RoleIDs:    roleIDs,
			MaxWorkers: len(roleIDs),
			Barrier:    BarrierAll,
		})
		stageOrder = append(stageOrder, template.id)
	}
	slices.SortFunc(stages, func(left, right StageSpec) int {
		return compareStrings(left.ID, right.ID)
	})

	workflow := WorkflowSpec{
		Name:             definition.name,
		Workers:          workers,
		MaxCycles:        DefaultPresetMaxCycles,
		WorkerRoleIDs:    workerRoleIDs,
		SupervisorRoleID: presetSupervisorRoleID,
		ReducerRoleID:    presetReducerRoleID,
		Roles:            roles,
		Stages:           stages,
		StageOrder:       stageOrder,
	}
	if err := ValidateWorkflow(workflow); err != nil {
		return WorkflowSpec{}, fmt.Errorf("compile preset %q: %w", definition.name, err)
	}
	return workflow, nil
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
