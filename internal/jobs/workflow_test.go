package jobs

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateWorkflowRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*WorkflowSpec)
		want   string
	}{
		{
			name: "unbounded cycles",
			mutate: func(workflow *WorkflowSpec) {
				workflow.MaxCycles = 0
			},
			want: "max cycles",
		},
		{
			name: "unknown worker role",
			mutate: func(workflow *WorkflowSpec) {
				workflow.WorkerRoleIDs[0] = "unknown"
			},
			want: "worker role",
		},
		{
			name: "dangling stage role",
			mutate: func(workflow *WorkflowSpec) {
				stage := firstStageWithRoles(workflow)
				stage.RoleIDs[0] = "unknown"
				slices.Sort(stage.RoleIDs)
			},
			want: "unknown role",
		},
		{
			name: "dangling stage order reference",
			mutate: func(workflow *WorkflowSpec) {
				workflow.StageOrder[0] = "unknown"
			},
			want: "unknown stage",
		},
		{
			name: "duplicate stage order reference",
			mutate: func(workflow *WorkflowSpec) {
				workflow.StageOrder[1] = workflow.StageOrder[0]
			},
			want: "repeats stage",
		},
		{
			name: "non all barrier",
			mutate: func(workflow *WorkflowSpec) {
				workflow.Stages[0].Barrier = BarrierPolicy("any")
			},
			want: "barrier",
		},
		{
			name: "non flat batch",
			mutate: func(workflow *WorkflowSpec) {
				workflow.Stages[0].MaxWorkers = 1
			},
			want: "flat batch",
		},
		{
			name: "unstable result order",
			mutate: func(workflow *WorkflowSpec) {
				stage := firstStageWithAtLeastRoles(workflow, 2)
				stage.RoleIDs[0], stage.RoleIDs[1] = stage.RoleIDs[1], stage.RoleIDs[0]
			},
			want: "stable sorted order",
		},
		{
			name: "provider pin",
			mutate: func(workflow *WorkflowSpec) {
				workflow.Roles[0].Authority.Providers = []string{"qwen"}
			},
			want: "provider bindings",
		},
		{
			name: "multiple writer roles",
			mutate: func(workflow *WorkflowSpec) {
				first := firstStageWithAtLeastRoles(workflow, 2)
				setRoleWriter(workflow, first.RoleIDs[0])
				setRoleWriter(workflow, first.RoleIDs[1])
			},
			want: "writer",
		},
		{
			name: "writer is not isolated",
			mutate: func(workflow *WorkflowSpec) {
				stage := firstStageWithAtLeastRoles(workflow, 2)
				setRoleWriter(workflow, stage.RoleIDs[0])
			},
			want: "isolate its writer",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			workflow, err := CompilePreset(PresetGeneral, 2)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&workflow)
			err = ValidateWorkflow(workflow)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateWorkflow() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func firstStageWithRoles(workflow *WorkflowSpec) *StageSpec {
	for index := range workflow.Stages {
		if len(workflow.Stages[index].RoleIDs) != 0 {
			return &workflow.Stages[index]
		}
	}
	panic("workflow has no stage with roles")
}

func firstStageWithAtLeastRoles(workflow *WorkflowSpec, count int) *StageSpec {
	for index := range workflow.Stages {
		if len(workflow.Stages[index].RoleIDs) >= count {
			return &workflow.Stages[index]
		}
	}
	panic("workflow has no sufficiently large stage")
}

func setRoleWriter(workflow *WorkflowSpec, roleID string) {
	for index := range workflow.Roles {
		if workflow.Roles[index].ID == roleID {
			workflow.Roles[index].Writer = true
			return
		}
	}
	panic("role not found: " + roleID)
}
