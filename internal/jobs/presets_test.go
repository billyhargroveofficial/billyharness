package jobs

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestBuiltInPresetNames(t *testing.T) {
	t.Parallel()

	want := []string{
		PresetGeneral,
		PresetResearch,
		PresetCoding,
		PresetDebug,
		PresetReview,
		PresetPlanning,
		PresetWriting,
		PresetCompare,
	}
	got := BuiltInPresetNames()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuiltInPresetNames() = %v, want %v", got, want)
	}

	got[0] = "mutated"
	if next := BuiltInPresetNames(); !reflect.DeepEqual(next, want) {
		t.Fatalf("BuiltInPresetNames() shares mutable storage: %v", next)
	}
}

func TestCompilePresetConformance(t *testing.T) {
	t.Parallel()

	writerCounts := map[string]int{
		PresetGeneral:  0,
		PresetResearch: 0,
		PresetCoding:   1,
		PresetDebug:    1,
		PresetReview:   0,
		PresetPlanning: 0,
		PresetWriting:  1,
		PresetCompare:  0,
	}
	stageOrders := map[string][]string{
		PresetGeneral:  {"explore", "reduce", "supervise"},
		PresetResearch: {"investigate", "reduce", "supervise"},
		PresetCoding:   {"analyze", "implement", "verify", "reduce", "supervise"},
		PresetDebug:    {"diagnose", "repair", "verify", "reduce", "supervise"},
		PresetReview:   {"review", "reduce", "supervise"},
		PresetPlanning: {"plan", "reduce", "supervise"},
		PresetWriting:  {"develop", "draft", "review", "reduce", "supervise"},
		PresetCompare:  {"evaluate", "reduce", "supervise"},
	}
	parallelStages := map[string][]string{
		PresetGeneral:  {"explore"},
		PresetResearch: {"investigate"},
		PresetCoding:   {"analyze", "verify"},
		PresetDebug:    {"diagnose", "verify"},
		PresetReview:   {"review"},
		PresetPlanning: {"plan"},
		PresetWriting:  {"develop", "review"},
		PresetCompare:  {"evaluate"},
	}

	for _, name := range BuiltInPresetNames() {
		name := name
		for workers := MinWorkflowWorkers; workers <= MaxWorkflowWorkers; workers++ {
			workers := workers
			t.Run(name+"/workers="+strconv.Itoa(workers), func(t *testing.T) {
				t.Parallel()

				first, err := CompilePreset(name, workers)
				if err != nil {
					t.Fatalf("CompilePreset(%q, %d): %v", name, workers, err)
				}
				second, err := CompilePreset(name, workers)
				if err != nil {
					t.Fatalf("second CompilePreset(%q, %d): %v", name, workers, err)
				}
				if !reflect.DeepEqual(first, second) {
					t.Fatalf("CompilePreset(%q, %d) is not deterministic", name, workers)
				}
				if err := ValidateWorkflow(first); err != nil {
					t.Fatalf("ValidateWorkflow(CompilePreset(%q, %d)): %v", name, workers, err)
				}
				if first.Name != name {
					t.Fatalf("workflow name = %q, want %q", first.Name, name)
				}
				if first.Workers != workers {
					t.Fatalf("workflow workers = %d, want %d", first.Workers, workers)
				}
				if first.MaxCycles != DefaultPresetMaxCycles {
					t.Fatalf("workflow max cycles = %d, want %d", first.MaxCycles, DefaultPresetMaxCycles)
				}
				if !reflect.DeepEqual(first.StageOrder, stageOrders[name]) {
					t.Fatalf("workflow stage order = %v, want %v", first.StageOrder, stageOrders[name])
				}

				firstJSON, err := json.Marshal(first)
				if err != nil {
					t.Fatalf("marshal first workflow: %v", err)
				}
				secondJSON, err := json.Marshal(second)
				if err != nil {
					t.Fatalf("marshal second workflow: %v", err)
				}
				if string(firstJSON) != string(secondJSON) {
					t.Fatalf("compiled JSON differs:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
				}
				lowerJSON := strings.ToLower(string(firstJSON))
				for _, providerOrModel := range []string{
					"qwen",
					"openai",
					"anthropic",
					"claude",
					"deepseek",
					"gemini",
					`"provider":`,
					`"model":`,
				} {
					if strings.Contains(lowerJSON, providerOrModel) {
						t.Fatalf("compiled preset contains provider/model pin %q: %s", providerOrModel, firstJSON)
					}
				}

				roles := make(map[string]RoleSpec, len(first.Roles))
				writers := 0
				for _, role := range first.Roles {
					roles[role.ID] = role
					if len(role.Authority.Providers) != 0 {
						t.Fatalf("role %q has provider bindings %v", role.ID, role.Authority.Providers)
					}
					if role.Writer {
						writers++
					}
				}
				if writers != writerCounts[name] {
					t.Fatalf("writer role count = %d, want %d", writers, writerCounts[name])
				}

				if slices.Contains(first.WorkerRoleIDs, first.SupervisorRoleID) {
					t.Fatalf("supervisor %q counted as worker", first.SupervisorRoleID)
				}
				if slices.Contains(first.WorkerRoleIDs, first.ReducerRoleID) {
					t.Fatalf("reducer %q counted as worker", first.ReducerRoleID)
				}
				if first.SupervisorRoleID == first.ReducerRoleID {
					t.Fatal("supervisor and reducer roles are identical")
				}

				for _, stage := range first.Stages {
					if stage.Barrier != BarrierAll {
						t.Fatalf("stage %q barrier = %q, want %q", stage.ID, stage.Barrier, BarrierAll)
					}
					if stage.MaxWorkers < 1 || stage.MaxWorkers > workers {
						t.Fatalf("stage %q max workers = %d, workflow limit %d", stage.ID, stage.MaxWorkers, workers)
					}
					if len(stage.RoleIDs) != stage.MaxWorkers {
						t.Fatalf("stage %q is not a flat batch: roles=%d max_workers=%d", stage.ID, len(stage.RoleIDs), stage.MaxWorkers)
					}
					if !slices.IsSorted(stage.RoleIDs) {
						t.Fatalf("stage %q role order is unstable: %v", stage.ID, stage.RoleIDs)
					}
					stageWriters := 0
					for _, roleID := range stage.RoleIDs {
						if roles[roleID].Writer {
							stageWriters++
						}
					}
					if stageWriters > 1 {
						t.Fatalf("stage %q has %d concurrent writers", stage.ID, stageWriters)
					}
					if stageWriters == 1 && len(stage.RoleIDs) != 1 {
						t.Fatalf("stage %q runs a writer concurrently: %v", stage.ID, stage.RoleIDs)
					}
				}
				for _, stageID := range parallelStages[name] {
					stage := presetStageByID(t, first, stageID)
					if stage.MaxWorkers != workers {
						t.Fatalf("parallel stage %q max workers = %d, want %d", stage.ID, stage.MaxWorkers, workers)
					}
				}
			})
		}
	}
}

func TestCompilePresetRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		preset  string
		workers int
		want    string
	}{
		{name: "unknown preset", preset: "forecast", workers: 2, want: "unknown preset"},
		{name: "zero workers", preset: PresetGeneral, workers: 0, want: "between 1 and 4"},
		{name: "too many workers", preset: PresetGeneral, workers: 5, want: "between 1 and 4"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := CompilePreset(test.preset, test.workers)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CompilePreset(%q, %d) error = %v, want containing %q", test.preset, test.workers, err, test.want)
			}
		})
	}
}

func TestCompilePresetReturnsIndependentValue(t *testing.T) {
	t.Parallel()

	pristine, err := CompilePreset(PresetCoding, MaxWorkflowWorkers)
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := CompilePreset(PresetCoding, MaxWorkflowWorkers)
	if err != nil {
		t.Fatal(err)
	}
	mutated.Roles[0].Purpose = "changed"
	mutated.WorkerRoleIDs[0] = "changed"
	mutated.Stages[0].RoleIDs[0] = "changed"
	mutated.StageOrder[0] = "changed"

	again, err := CompilePreset(PresetCoding, MaxWorkflowWorkers)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, pristine) {
		t.Fatalf("preset registry was mutated:\n got: %#v\nwant: %#v", again, pristine)
	}
}

func presetStageByID(t *testing.T, workflow WorkflowSpec, stageID string) StageSpec {
	t.Helper()
	for _, stage := range workflow.Stages {
		if stage.ID == stageID {
			return stage
		}
	}
	t.Fatalf("workflow %q has no stage %q", workflow.Name, stageID)
	return StageSpec{}
}
