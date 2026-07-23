package jobs

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// MinWorkflowWorkers is the smallest useful user-facing worker limit.
	MinWorkflowWorkers = MinWorkers
	// MaxWorkflowWorkers bounds every flat worker batch.
	MaxWorkflowWorkers = MaxWorkers
)

// WorkflowSpec is the provider-neutral, deterministic shape produced by a
// preset. StageOrder is explicit so execution never depends on map iteration
// or on incidental declaration order.
type WorkflowSpec struct {
	Name             string      `json:"name"`
	Workers          int         `json:"workers"`
	MaxCycles        int         `json:"max_cycles"`
	WorkerRoleIDs    []string    `json:"worker_role_ids"`
	SupervisorRoleID string      `json:"supervisor_role_id"`
	ReducerRoleID    string      `json:"reducer_role_id"`
	Roles            []RoleSpec  `json:"roles"`
	Stages           []StageSpec `json:"stages"`
	StageOrder       []string    `json:"stage_order"`
}

// Validate validates a compiled workflow without consulting a provider,
// runtime, filesystem, clock, or other ambient state.
func (w WorkflowSpec) Validate() error {
	return ValidateWorkflow(w)
}

// ValidateWorkflow enforces the single bounded workflow model shared by every
// built-in preset.
func ValidateWorkflow(w WorkflowSpec) error {
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("workflow name is required")
	}
	if w.Workers < MinWorkflowWorkers || w.Workers > MaxWorkflowWorkers {
		return fmt.Errorf(
			"workflow workers must be between %d and %d, got %d",
			MinWorkflowWorkers,
			MaxWorkflowWorkers,
			w.Workers,
		)
	}
	if w.MaxCycles <= 0 {
		return fmt.Errorf("workflow max cycles must be positive")
	}
	if len(w.Roles) == 0 {
		return fmt.Errorf("workflow roles are required")
	}
	if len(w.Stages) == 0 {
		return fmt.Errorf("workflow stages are required")
	}

	roles := make(map[string]RoleSpec, len(w.Roles))
	writerRoles := make(map[string]struct{})
	for index, role := range w.Roles {
		if len(role.Authority.Providers) != 0 {
			return fmt.Errorf("workflow role %q contains provider bindings", role.ID)
		}
		if err := role.Validate(); err != nil {
			return fmt.Errorf("workflow role %d: %w", index, err)
		}
		if _, exists := roles[role.ID]; exists {
			return fmt.Errorf("workflow role %q is duplicated", role.ID)
		}
		roles[role.ID] = role
		if role.Writer {
			writerRoles[role.ID] = struct{}{}
		}
	}
	if len(writerRoles) > 1 {
		return fmt.Errorf("workflow may declare at most one writer role")
	}

	if strings.TrimSpace(w.SupervisorRoleID) == "" {
		return fmt.Errorf("workflow supervisor role is required")
	}
	if strings.TrimSpace(w.ReducerRoleID) == "" {
		return fmt.Errorf("workflow reducer role is required")
	}
	if w.SupervisorRoleID == w.ReducerRoleID {
		return fmt.Errorf("workflow supervisor and reducer roles must be distinct")
	}
	if _, ok := roles[w.SupervisorRoleID]; !ok {
		return fmt.Errorf("workflow supervisor role %q is unknown", w.SupervisorRoleID)
	}
	if _, ok := roles[w.ReducerRoleID]; !ok {
		return fmt.Errorf("workflow reducer role %q is unknown", w.ReducerRoleID)
	}
	if roles[w.SupervisorRoleID].Writer {
		return fmt.Errorf("workflow supervisor role %q cannot be a writer", w.SupervisorRoleID)
	}
	if roles[w.ReducerRoleID].Writer {
		return fmt.Errorf("workflow reducer role %q cannot be a writer", w.ReducerRoleID)
	}

	workerRoles := make(map[string]struct{}, len(w.WorkerRoleIDs))
	for index, roleID := range w.WorkerRoleIDs {
		if strings.TrimSpace(roleID) == "" {
			return fmt.Errorf("workflow worker role %d is empty", index)
		}
		if roleID == w.SupervisorRoleID || roleID == w.ReducerRoleID {
			return fmt.Errorf("workflow control role %q cannot be a worker role", roleID)
		}
		if _, ok := roles[roleID]; !ok {
			return fmt.Errorf("workflow worker role %q is unknown", roleID)
		}
		if _, exists := workerRoles[roleID]; exists {
			return fmt.Errorf("workflow worker role %q is duplicated", roleID)
		}
		workerRoles[roleID] = struct{}{}
	}
	if len(workerRoles) == 0 {
		return fmt.Errorf("workflow worker roles are required")
	}
	for roleID := range roles {
		if roleID == w.SupervisorRoleID || roleID == w.ReducerRoleID {
			continue
		}
		if _, ok := workerRoles[roleID]; !ok {
			return fmt.Errorf("workflow role %q is neither a worker nor a control role", roleID)
		}
	}

	stages := make(map[string]StageSpec, len(w.Stages))
	referencedRoles := make(map[string]struct{}, len(w.Roles))
	controlReferences := map[string]int{
		w.SupervisorRoleID: 0,
		w.ReducerRoleID:    0,
	}
	for index, stage := range w.Stages {
		if _, exists := stages[stage.ID]; exists {
			return fmt.Errorf("workflow stage %q is duplicated", stage.ID)
		}
		if stage.Barrier != BarrierAll {
			return fmt.Errorf("workflow stage %q must use the all barrier", stage.ID)
		}
		if stage.MaxWorkers > w.Workers {
			return fmt.Errorf(
				"workflow stage %q max workers %d exceeds workflow limit %d",
				stage.ID,
				stage.MaxWorkers,
				w.Workers,
			)
		}
		if len(stage.RoleIDs) != stage.MaxWorkers {
			return fmt.Errorf(
				"workflow stage %q must be one flat batch: %d roles for max workers %d",
				stage.ID,
				len(stage.RoleIDs),
				stage.MaxWorkers,
			)
		}
		if !slices.IsSorted(stage.RoleIDs) {
			return fmt.Errorf("workflow stage %q role IDs must be in stable sorted order", stage.ID)
		}
		if err := stage.Validate(); err != nil {
			return fmt.Errorf("workflow stage %d: %w", index, err)
		}

		seenStageRoles := make(map[string]struct{}, len(stage.RoleIDs))
		stageWriters := 0
		for _, roleID := range stage.RoleIDs {
			role, ok := roles[roleID]
			if !ok {
				return fmt.Errorf("workflow stage %q references unknown role %q", stage.ID, roleID)
			}
			if _, exists := seenStageRoles[roleID]; exists {
				return fmt.Errorf("workflow stage %q repeats role %q", stage.ID, roleID)
			}
			seenStageRoles[roleID] = struct{}{}
			referencedRoles[roleID] = struct{}{}
			if _, control := controlReferences[roleID]; control {
				if len(stage.RoleIDs) != 1 {
					return fmt.Errorf("workflow stage %q must isolate control role %q", stage.ID, roleID)
				}
				controlReferences[roleID]++
			}
			if role.Writer {
				stageWriters++
			}
		}
		if stageWriters > 1 {
			return fmt.Errorf("workflow stage %q has concurrent writers", stage.ID)
		}
		if stageWriters == 1 && len(stage.RoleIDs) != 1 {
			return fmt.Errorf("workflow stage %q must isolate its writer", stage.ID)
		}
		stages[stage.ID] = stage
	}
	for roleID, references := range controlReferences {
		if references != 1 {
			return fmt.Errorf("workflow control role %q must be referenced exactly once, got %d", roleID, references)
		}
	}

	if len(w.StageOrder) != len(w.Stages) {
		return fmt.Errorf(
			"workflow stage order has %d references for %d stages",
			len(w.StageOrder),
			len(w.Stages),
		)
	}
	orderedStages := make(map[string]struct{}, len(w.StageOrder))
	for index, stageID := range w.StageOrder {
		if _, ok := stages[stageID]; !ok {
			return fmt.Errorf("workflow stage order %d references unknown stage %q", index, stageID)
		}
		if _, exists := orderedStages[stageID]; exists {
			return fmt.Errorf("workflow stage order repeats stage %q", stageID)
		}
		orderedStages[stageID] = struct{}{}
	}

	for roleID := range roles {
		if _, ok := referencedRoles[roleID]; !ok {
			return fmt.Errorf("workflow role %q is not referenced by a stage", roleID)
		}
	}
	for roleID := range workerRoles {
		if _, ok := referencedRoles[roleID]; !ok {
			return fmt.Errorf("workflow worker role %q is not referenced by a stage", roleID)
		}
	}

	return nil
}
