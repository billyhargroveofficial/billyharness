package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	jobdomain "github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

func TestJobWizardRequestMatrixAcrossProvidersPresetsAndWorkers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	providers := []struct {
		name       string
		provider   string
		model      string
		reasoning  string
		configured string
		baseURL    string
	}{
		{name: "deepseek", provider: modelinfo.ProviderDeepSeek, model: "deepseek-v4-flash", reasoning: "high", configured: modelinfo.ProviderDeepSeek},
		{name: "qwen", provider: modelinfo.ProviderQwen, model: "qwen3.8-max-preview", reasoning: "high", configured: modelinfo.ProviderQwen},
		{name: "kimi", provider: modelinfo.ProviderKimi, model: "k3", reasoning: "high", configured: modelinfo.ProviderKimi},
		{name: "codex", provider: modelinfo.ProviderOpenAICodex, model: "gpt-5.5", reasoning: "high", configured: modelinfo.ProviderOpenAICodex},
		{
			name: "literal custom with vendor reasoning", provider: "custom", model: "vendor-model-v1",
			reasoning: "ultra", configured: "custom", baseURL: "https://models.example.invalid/v1",
		},
	}

	for _, provider := range providers {
		provider := provider
		t.Run(provider.name, func(t *testing.T) {
			t.Parallel()

			defaults := newJobWizardDefaults(
				provider.provider,
				provider.model,
				provider.reasoning,
				provider.configured,
				provider.model,
				provider.baseURL,
			)
			if got := defaults.providers[defaults.providerIndex].ID; got != provider.provider {
				t.Fatalf("selected provider = %q, want %q", got, provider.provider)
			}

			for presetIndex, preset := range jobPresetChoices {
				presetIndex, preset := presetIndex, preset
				for workers := jobdomain.MinWorkers; workers <= jobdomain.MaxWorkers; workers++ {
					workers := workers
					t.Run(fmt.Sprintf("%s/workers=%d", preset, workers), func(t *testing.T) {
						t.Parallel()

						wizard := newJobWizard(defaults)
						wizard.step = jobStepReview
						wizard.draft.goal = "Run the bounded provider-neutral workflow and verify every cycle."
						wizard.draft.presetIndex = presetIndex
						wizard.draft.workers = workers
						wizard.draft.duration = 6 * time.Hour
						wizard.draft.minRuntime = 5 * time.Hour
						wizard.draft.minCycles = 1
						wizard.draft.maxCycles = 8
						wizard.draft.maxAttempts = 128
						wizard.draft.maxModelCalls = 128
						wizard.draft.maxTokens = 1_000_000
						wizard.draft.readRoots = []string{root}
						wizard.draft.publicWeb = true
						wizard.draft.qwenAcknowledged = provider.provider == modelinfo.ProviderQwen
						if jobPresetHasWriter(preset) {
							wizard.draft.writeRoots = []string{root}
						}

						request, startAfterCheck, err := wizard.request()
						if err != nil {
							t.Fatalf("request(): %v", err)
						}
						if err := request.Validate(); err != nil {
							t.Fatalf("request.Validate(): %v", err)
						}
						if !startAfterCheck || request.AutoStart {
							t.Fatalf("launch mode = returned:%t request.auto_start:%t, want authority-check start with queued create", startAfterCheck, request.AutoStart)
						}
						if request.Preset != preset || request.Workers != workers {
							t.Fatalf("workflow = %s/%d, want %s/%d", request.Preset, request.Workers, preset, workers)
						}
						if request.Route.ProviderID != provider.provider || request.Route.ModelID != provider.model ||
							request.Route.ReasoningEffort != provider.reasoning || request.Route.Thinking != "enabled" {
							t.Fatalf("route = %#v", request.Route)
						}
						if !slices.Equal(request.Authority.Providers, []string{provider.provider}) {
							t.Fatalf("provider authority = %#v", request.Authority.Providers)
						}
						if !slices.Equal(request.Authority.ReadRoots, []string{root}) ||
							!slices.Equal(request.Authority.NetworkHosts, []string{"*"}) {
							t.Fatalf("read/network authority = %#v", request.Authority)
						}
						if !slices.Contains(request.Authority.Tools, "fs_read_file") ||
							!slices.Contains(request.Authority.Tools, "web_search") ||
							!slices.Contains(request.Authority.Tools, "time_now") {
							t.Fatalf("read/web tool bundle = %#v", request.Authority.Tools)
						}
						if jobPresetHasWriter(preset) {
							if !slices.Equal(request.Authority.WriteRoots, []string{root}) ||
								!slices.Contains(request.Authority.Tools, "fs_write_file") {
								t.Fatalf("writer authority = %#v", request.Authority)
							}
						} else if len(request.Authority.WriteRoots) != 0 || slices.Contains(request.Authority.Tools, "fs_write_file") {
							t.Fatalf("read-only preset gained write authority: %#v", request.Authority)
						}

						const wantCadence = uint64(2572)
						if request.DurationSeconds != uint64((6*time.Hour)/time.Second) ||
							request.MinRuntimeSeconds != uint64((5*time.Hour)/time.Second) ||
							request.CadenceSeconds != wantCadence {
							t.Fatalf("schedule = duration:%d minimum:%d cadence:%d", request.DurationSeconds, request.MinRuntimeSeconds, request.CadenceSeconds)
						}
						requiredCycles := uint64(1) + (request.MinRuntimeSeconds+request.CadenceSeconds-1)/request.CadenceSeconds
						workflow, err := jobdomain.CompilePreset(preset, workers)
						if err != nil {
							t.Fatal(err)
						}
						perCycle := uint64(0)
						for _, stage := range workflow.Stages {
							perCycle += uint64(len(stage.RoleIDs))
						}
						wantFloor := perCycle * requiredCycles
						if got := jobInvocationFloor(preset, workers, requiredCycles); got != wantFloor {
							t.Fatalf("invocation floor = %d, want %d", got, wantFloor)
						}
						if request.Budget.MaxAttempts < wantFloor || request.Budget.MaxModelCalls < wantFloor || request.Budget.MaxTokens < wantFloor {
							t.Fatalf("budget cannot cover invocation floor %d: %#v", wantFloor, request.Budget)
						}
					})
				}
			}
		})
	}
}

func TestJobWizardBuiltInModelReasoningMatrix(t *testing.T) {
	t.Parallel()

	for _, provider := range modelinfo.Providers() {
		provider := provider
		if provider.Custom {
			continue
		}
		for _, model := range provider.Models {
			model := model
			for _, effort := range modelinfo.Lookup(model).ReasoningModes {
				effort := effort
				t.Run(provider.ID+"/"+model+"/"+effort, func(t *testing.T) {
					t.Parallel()

					defaults := newJobWizardDefaults(provider.ID, model, effort, provider.ID, model, provider.BaseURL)
					wizard := newJobWizard(defaults)
					wizard.step = jobStepReview
					wizard.draft.goal = "Validate the selected provider route."
					wizard.draft.qwenAcknowledged = provider.ID == modelinfo.ProviderQwen

					request, _, err := wizard.request()
					if err != nil {
						t.Fatalf("request(): %v", err)
					}
					wantThinking := "enabled"
					if effort == "off" {
						wantThinking = "disabled"
					}
					if request.Route.ProviderID != provider.ID || request.Route.ModelID != model ||
						request.Route.ReasoningEffort != effort || request.Route.Thinking != wantThinking {
						t.Fatalf("route = %#v, want provider=%s model=%s thinking=%s reasoning=%s", request.Route, provider.ID, model, wantThinking, effort)
					}
					if err := request.Validate(); err != nil {
						t.Fatalf("request.Validate(): %v", err)
					}
				})
			}
		}
	}
}

func TestJobWizardCustomDefaultsAndReasoningSurviveSelection(t *testing.T) {
	t.Parallel()

	for _, providerID := range []string{"custom", "my-compatible"} {
		providerID := providerID
		t.Run(providerID, func(t *testing.T) {
			t.Parallel()

			defaults := newJobWizardDefaults(
				providerID, "vendor-model-v1", "ultra",
				providerID, "vendor-model-v1", "https://models.example.invalid/v1",
			)
			if got := defaults.providers[defaults.providerIndex].ID; got != providerID {
				t.Fatalf("selected provider = %q, want %q", got, providerID)
			}
			wizard := newJobWizard(defaults)
			if wizard.model() != "vendor-model-v1" || wizard.reasoning() != "ultra" {
				t.Fatalf("custom defaults = model:%q reasoning:%q", wizard.model(), wizard.reasoning())
			}
			wizard.step = jobStepReview
			wizard.draft.goal = "Use the exact configured custom binding."
			request, _, err := wizard.request()
			if err != nil {
				t.Fatal(err)
			}
			if request.Route.ProviderID != providerID || request.Route.ModelID != "vendor-model-v1" ||
				request.Route.Thinking != "enabled" || request.Route.ReasoningEffort != "ultra" {
				t.Fatalf("custom route = %#v", request.Route)
			}
		})
	}

	withoutEndpoint := newJobWizardDefaults("custom", "vendor-model-v1", "high", "custom", "vendor-model-v1", "")
	if slices.ContainsFunc(withoutEndpoint.providers, func(choice jobProviderChoice) bool { return choice.ID == "custom" }) {
		t.Fatal("custom provider without an explicitly configured endpoint was offered")
	}
}

func TestJobWizardPreservesReasoningAcrossProviderAndModelChanges(t *testing.T) {
	t.Parallel()

	defaults := newJobWizardDefaults(
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", "high",
		"my-compatible", "vendor-model-v1", "https://models.example.invalid/v1",
	)
	wizard := newJobWizard(defaults)
	wizard.step = jobStepProvider
	for range len(wizard.providers) - 1 {
		wizard.rotateChoice(1)
		if got := wizard.reasoning(); got != "high" {
			t.Fatalf("provider %q reasoning = %q, want high", wizard.provider().ID, got)
		}
	}

	for index, choice := range wizard.providers {
		if choice.ID == modelinfo.ProviderKimi {
			wizard.draft.providerIndex = index
			break
		}
	}
	wizard.draft.modelIndex = 0
	wizard.draft.reasoningIndex = preferredReasoningIndex(wizard.reasoningChoices(), "high")
	wizard.step = jobStepModel
	wizard.rotateChoice(1)
	if wizard.model() != "kimi-for-coding" || wizard.reasoning() != "high" {
		t.Fatalf("Kimi model switch = model:%q reasoning:%q", wizard.model(), wizard.reasoning())
	}
}

func TestJobWizardWriterRootPolicyAndStepSkipping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	defaults := newJobWizardDefaults(
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", "high",
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", modelinfo.Provider(modelinfo.ProviderDeepSeek).BaseURL,
	)

	readOnly := newJobWizard(defaults)
	readOnly.step = jobStepReview
	readOnly.draft.goal = "Do not grant a writer to a read-only workflow."
	readOnly.draft.writeRoots = []string{root}
	if _, _, err := readOnly.request(); err == nil || !strings.Contains(err.Error(), "no isolated writer") {
		t.Fatalf("read-only write-root error = %v", err)
	}

	changed := newJobWizard(defaults)
	changed.draft.presetIndex = slices.Index(jobPresetChoices, jobdomain.PresetCoding)
	changed.draft.writeRoots = []string{root}
	changed.step = jobStepPreset
	changed.rotateChoice(-changed.draft.presetIndex)
	if changed.preset() != jobdomain.PresetGeneral || len(changed.draft.writeRoots) != 0 {
		t.Fatalf("read-only preset retained write roots: preset=%s roots=%#v", changed.preset(), changed.draft.writeRoots)
	}

	readOnly = newJobWizard(defaults)
	readOnly.step = jobStepReadRoots
	readOnly.next()
	if readOnly.step != jobStepPublicWeb {
		t.Fatalf("read-only wizard next step = %v, want public web", readOnly.step)
	}
	readOnly.previous()
	if readOnly.step != jobStepReadRoots {
		t.Fatalf("read-only wizard previous step = %v, want read roots", readOnly.step)
	}

	writer := newJobWizard(defaults)
	writer.draft.presetIndex = slices.Index(jobPresetChoices, jobdomain.PresetCoding)
	writer.step = jobStepReadRoots
	writer.next()
	if writer.step != jobStepWriteRoots {
		t.Fatalf("writer wizard next step = %v, want write roots", writer.step)
	}
}

func TestJobWizardQwenRequiresExplicitAcknowledgement(t *testing.T) {
	t.Parallel()

	wizard := newJobWizard(newJobWizardDefaults(
		modelinfo.ProviderQwen, "qwen3.8-max-preview", "high",
		modelinfo.ProviderQwen, "qwen3.8-max-preview", modelinfo.Provider(modelinfo.ProviderQwen).BaseURL,
	))
	wizard.step = jobStepReview
	wizard.draft.goal = "Run only after explicit provider-terms acknowledgement."
	if _, _, err := wizard.request(); err == nil || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("unacknowledged Qwen error = %v", err)
	}
	wizard.draft.qwenAcknowledged = true
	if _, _, err := wizard.request(); err != nil {
		t.Fatalf("acknowledged Qwen request: %v", err)
	}
}
