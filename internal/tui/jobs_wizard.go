package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	jobdomain "github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

type jobProviderChoice struct {
	ID     string
	Name   string
	Models []string
}

type jobWizardDefaults struct {
	providers       []jobProviderChoice
	providerIndex   int
	model           string
	reasoningEffort string
}

func newJobWizardDefaults(currentProvider, currentModel, currentReasoning, configuredProvider, configuredModel, configuredBaseURL string) jobWizardDefaults {
	currentProvider = modelinfo.NormalizeProvider(currentProvider)
	currentModel = modelinfo.NormalizeAlias(currentModel)
	choices := make([]jobProviderChoice, 0, 6)
	for _, provider := range modelinfo.Providers() {
		if provider.Custom || provider.ID == "" || provider.ID == "custom" {
			continue
		}
		choices = append(choices, jobProviderChoice{
			ID: provider.ID, Name: provider.Name, Models: append([]string(nil), provider.Models...),
		})
	}
	configuredID := modelinfo.NormalizeProvider(configuredProvider)
	configuredInfo := modelinfo.Provider(configuredID)
	if configuredID == modelinfo.ProviderMock {
		choices = append(choices, jobProviderChoice{ID: configuredID, Name: configuredInfo.Name, Models: []string{"mock"}})
	} else if configuredID != "" && configuredInfo.Custom && strings.TrimSpace(configuredBaseURL) != "" {
		model := modelinfo.NormalizeAlias(configuredModel)
		if model != "" {
			choices = append(choices, jobProviderChoice{ID: configuredID, Name: configuredInfo.Name, Models: []string{model}})
		}
	}
	providerIndex := 0
	for index := range choices {
		if choices[index].ID == currentProvider {
			providerIndex = index
			break
		}
	}
	if len(choices) == 0 {
		choices = []jobProviderChoice{{ID: modelinfo.ProviderDeepSeek, Name: "DeepSeek", Models: []string{"deepseek-v4-flash"}}}
	}
	selected := &choices[providerIndex]
	if currentModel != "" && modelinfo.ProviderForModel(currentModel, selected.ID) == selected.ID && !slices.Contains(selected.Models, currentModel) {
		selected.Models = append(selected.Models, currentModel)
	}
	if currentModel == "" || !slices.Contains(selected.Models, currentModel) {
		currentModel = selected.Models[0]
	}
	return jobWizardDefaults{
		providers:       choices,
		providerIndex:   providerIndex,
		model:           currentModel,
		reasoningEffort: modelinfo.NormalizeReasoningEffort(currentReasoning),
	}
}

type jobWizardStep uint8

const (
	jobStepGoal jobWizardStep = iota
	jobStepPreset
	jobStepWorkers
	jobStepProvider
	jobStepModel
	jobStepReasoning
	jobStepDuration
	jobStepMinRuntime
	jobStepMinCycles
	jobStepMaxCycles
	jobStepMaxAttempts
	jobStepMaxModelCalls
	jobStepMaxTokens
	jobStepReadRoots
	jobStepWriteRoots
	jobStepPublicWeb
	jobStepStartMode
	jobStepReview
	jobStepCount
)

type jobDraft struct {
	goal             string
	presetIndex      int
	workers          int
	providerIndex    int
	modelIndex       int
	reasoningIndex   int
	duration         time.Duration
	minRuntime       time.Duration
	minCycles        uint64
	maxCycles        uint64
	maxAttempts      uint64
	maxModelCalls    uint64
	maxTokens        uint64
	readRoots        []string
	writeRoots       []string
	publicWeb        bool
	startAfterCheck  bool
	qwenAcknowledged bool
}

type jobWizard struct {
	step           jobWizardStep
	draft          jobDraft
	providers      []jobProviderChoice
	reasoningExtra string
	reviewScroll   int
	editor         textarea.Model
	err            string
}

type wizardOutcome uint8

const (
	wizardOutcomeNone wizardOutcome = iota
	wizardOutcomeCancel
	wizardOutcomeCreate
)

var jobPresetChoices = jobdomain.BuiltInPresetNames()

func newJobWizard(defaults jobWizardDefaults) *jobWizard {
	providerIndex := defaults.providerIndex
	if providerIndex < 0 || providerIndex >= len(defaults.providers) {
		providerIndex = 0
	}
	w := &jobWizard{
		providers:      append([]jobProviderChoice(nil), defaults.providers...),
		reasoningExtra: defaults.reasoningEffort,
		draft: jobDraft{
			workers:         1,
			providerIndex:   providerIndex,
			duration:        6 * time.Hour,
			minCycles:       1,
			maxCycles:       8,
			maxAttempts:     128,
			maxModelCalls:   128,
			maxTokens:       1_000_000,
			startAfterCheck: true,
		},
	}
	models := w.modelChoices()
	for index, model := range models {
		if model == defaults.model {
			w.draft.modelIndex = index
			break
		}
	}
	reasoning := w.reasoningChoices()
	w.draft.reasoningIndex = preferredReasoningIndex(reasoning, defaults.reasoningEffort)
	w.editor = textarea.New()
	w.editor.Prompt = ""
	w.editor.ShowLineNumbers = false
	w.prepareEditor()
	return w
}

func (w *jobWizard) updateKey(msg tea.KeyPressMsg) (tea.Cmd, wizardOutcome) {
	w.err = ""
	switch msg.String() {
	case "esc":
		return nil, wizardOutcomeCancel
	case "shift+tab", "ctrl+p":
		if w.isTextStep() {
			if err := w.commitEditor(); err != nil {
				w.err = jobSingleLine(err.Error())
				return nil, wizardOutcomeNone
			}
		}
		w.previous()
		return nil, wizardOutcomeNone
	}
	if w.step == jobStepReview {
		switch msg.String() {
		case "a":
			if w.provider().ID == modelinfo.ProviderQwen {
				w.draft.qwenAcknowledged = !w.draft.qwenAcknowledged
			}
		case "y":
			if w.provider().ID == modelinfo.ProviderQwen && !w.draft.qwenAcknowledged {
				w.err = "acknowledge the Qwen unattended-use warning with a before creating"
				return nil, wizardOutcomeNone
			}
			return nil, wizardOutcomeCreate
		case "enter":
			w.err = "press y to confirm creation"
		}
		return nil, wizardOutcomeNone
	}
	if w.isChoiceStep() {
		switch msg.String() {
		case "left", "up", "h", "k":
			w.rotateChoice(-1)
		case "right", "down", "l", "j", " ":
			w.rotateChoice(1)
		case "enter", "tab":
			w.next()
		}
		return nil, wizardOutcomeNone
	}
	if msg.String() == "alt+enter" && (w.step == jobStepGoal || w.step == jobStepReadRoots || w.step == jobStepWriteRoots) {
		w.editor.InsertString("\n")
		return nil, wizardOutcomeNone
	}
	if msg.String() == "enter" || msg.String() == "tab" {
		if err := w.commitEditor(); err != nil {
			w.err = jobSingleLine(err.Error())
			return nil, wizardOutcomeNone
		}
		w.next()
		return nil, wizardOutcomeNone
	}
	return w.updateEditor(msg), wizardOutcomeNone
}

func (w *jobWizard) updateReviewScroll(msg tea.KeyPressMsg, width, available int) bool {
	if w.step != jobStepReview {
		return false
	}
	contentWidth := max(12, width-4)
	available = max(1, available)
	maximum := max(0, len(w.reviewDisplayLines(contentWidth))-available)
	switch msg.String() {
	case "up", "k":
		w.reviewScroll = max(0, w.reviewScroll-1)
	case "down", "j":
		w.reviewScroll = min(maximum, w.reviewScroll+1)
	case "pgup":
		w.reviewScroll = max(0, w.reviewScroll-available)
	case "pgdown":
		w.reviewScroll = min(maximum, w.reviewScroll+available)
	case "home", "g":
		w.reviewScroll = 0
	case "end", "G":
		w.reviewScroll = maximum
	default:
		return false
	}
	return true
}

func (w *jobWizard) updateEditor(msg tea.Msg) tea.Cmd {
	switch typed := msg.(type) {
	case tea.PasteMsg:
		typed.Content = stripJobTerminalControls(typed.Content)
		msg = typed
	case tea.KeyPressMsg:
		typed.Text = stripJobTerminalControls(typed.Text)
		msg = typed
	}
	var cmd tea.Cmd
	w.editor, cmd = w.editor.Update(msg)
	return cmd
}

func (w *jobWizard) isTextStep() bool {
	switch w.step {
	case jobStepGoal, jobStepDuration, jobStepMinRuntime, jobStepMinCycles, jobStepMaxCycles,
		jobStepMaxAttempts, jobStepMaxModelCalls, jobStepMaxTokens,
		jobStepReadRoots, jobStepWriteRoots:
		return true
	default:
		return false
	}
}

func (w *jobWizard) isChoiceStep() bool {
	return !w.isTextStep() && w.step != jobStepReview
}

func (w *jobWizard) next() {
	if w.step+1 < jobStepCount {
		w.step++
		if w.step == jobStepWriteRoots && !jobPresetHasWriter(w.preset()) {
			w.step = jobStepPublicWeb
		}
		w.prepareEditor()
	}
}

func (w *jobWizard) previous() {
	if w.step > jobStepGoal {
		w.step--
		if w.step == jobStepWriteRoots && !jobPresetHasWriter(w.preset()) {
			w.step = jobStepReadRoots
		}
		w.prepareEditor()
	}
}

func (w *jobWizard) prepareEditor() {
	if !w.isTextStep() {
		w.editor.Blur()
		return
	}
	value := ""
	switch w.step {
	case jobStepGoal:
		value = w.draft.goal
	case jobStepDuration:
		value = w.draft.duration.String()
	case jobStepMinRuntime:
		if w.draft.minRuntime == 0 {
			value = "off"
		} else {
			value = w.draft.minRuntime.String()
		}
	case jobStepMinCycles:
		value = strconv.FormatUint(w.draft.minCycles, 10)
	case jobStepMaxCycles:
		value = strconv.FormatUint(w.draft.maxCycles, 10)
	case jobStepMaxAttempts:
		value = strconv.FormatUint(w.draft.maxAttempts, 10)
	case jobStepMaxModelCalls:
		value = strconv.FormatUint(w.draft.maxModelCalls, 10)
	case jobStepMaxTokens:
		value = strconv.FormatUint(w.draft.maxTokens, 10)
	case jobStepReadRoots:
		value = strings.Join(w.draft.readRoots, "\n")
	case jobStepWriteRoots:
		value = strings.Join(w.draft.writeRoots, "\n")
	}
	w.editor.SetValue(value)
	w.editor.SetHeight(w.editorHeight())
	w.editor.Focus()
}

func (w *jobWizard) resizeEditor(width int) {
	w.editor.SetWidth(max(20, width-6))
	w.editor.SetHeight(w.editorHeight())
}

func (w *jobWizard) editorHeight() int {
	switch w.step {
	case jobStepGoal:
		return 6
	case jobStepReadRoots, jobStepWriteRoots:
		return 4
	default:
		return 1
	}
}

func (w *jobWizard) commitEditor() error {
	value := strings.TrimSpace(w.editor.Value())
	switch w.step {
	case jobStepGoal:
		if value == "" {
			return fmt.Errorf("goal is required")
		}
		if len(value) > gatewayapi.MaxJobGoalBytes {
			return fmt.Errorf("goal exceeds %d bytes", gatewayapi.MaxJobGoalBytes)
		}
		w.draft.goal = value
	case jobStepDuration:
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration%time.Second != 0 {
			return fmt.Errorf("duration must be a positive whole-second value such as 6h")
		}
		if uint64(duration/time.Second) > gatewayapi.MaxJobDurationSeconds {
			return fmt.Errorf("duration cannot exceed 7d")
		}
		w.draft.duration = duration
	case jobStepMinRuntime:
		if value == "" || strings.EqualFold(value, "off") || value == "0" {
			w.draft.minRuntime = 0
			break
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration%time.Second != 0 {
			return fmt.Errorf("minimum runtime must be off or a positive whole-second value such as 5h")
		}
		if uint64(duration/time.Second) > gatewayapi.MaxJobDurationSeconds {
			return fmt.Errorf("minimum runtime cannot exceed 7d")
		}
		w.draft.minRuntime = duration
	case jobStepMinCycles:
		parsed, err := parsePositiveJobUint("min cycles", value, gatewayapi.MaxJobBudgetCycles)
		if err != nil {
			return err
		}
		w.draft.minCycles = parsed
	case jobStepMaxCycles:
		parsed, err := parsePositiveJobUint("max cycles", value, gatewayapi.MaxJobBudgetCycles)
		if err != nil {
			return err
		}
		w.draft.maxCycles = parsed
	case jobStepMaxAttempts:
		parsed, err := parsePositiveJobUint("max attempts", value, gatewayapi.MaxJobBudgetAttempts)
		if err != nil {
			return err
		}
		w.draft.maxAttempts = parsed
	case jobStepMaxModelCalls:
		parsed, err := parsePositiveJobUint("max model calls", value, gatewayapi.MaxJobBudgetModelCalls)
		if err != nil {
			return err
		}
		w.draft.maxModelCalls = parsed
	case jobStepMaxTokens:
		parsed, err := parsePositiveJobUint("max tokens", value, gatewayapi.MaxJobBudgetTokens)
		if err != nil {
			return err
		}
		w.draft.maxTokens = parsed
	case jobStepReadRoots:
		roots, err := parseJobRoots(value)
		if err != nil {
			return fmt.Errorf("read roots: %w", err)
		}
		w.draft.readRoots = roots
	case jobStepWriteRoots:
		roots, err := parseJobRoots(value)
		if err != nil {
			return fmt.Errorf("write roots: %w", err)
		}
		w.draft.writeRoots = roots
	}
	return nil
}

func parsePositiveJobUint(label, value string, maximum uint64) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", label)
	}
	if parsed > maximum {
		return 0, fmt.Errorf("%s cannot exceed %d", label, maximum)
	}
	return parsed, nil
}

func parseJobRoots(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var roots []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		root := strings.TrimSpace(line)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return nil, fmt.Errorf("%q must be an absolute clean path", root)
		}
		if _, duplicate := seen[root]; duplicate {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	slices.Sort(roots)
	return roots, nil
}

func (w *jobWizard) rotateChoice(delta int) {
	rotate := func(index, count int) int {
		if count <= 0 {
			return 0
		}
		return (index + delta + count) % count
	}
	switch w.step {
	case jobStepPreset:
		w.draft.presetIndex = rotate(w.draft.presetIndex, len(jobPresetChoices))
		if !jobPresetHasWriter(w.preset()) {
			w.draft.writeRoots = nil
		}
	case jobStepWorkers:
		w.draft.workers = rotate(w.draft.workers-1, jobdomain.MaxWorkers) + 1
	case jobStepProvider:
		priorReasoning := w.reasoning()
		w.draft.providerIndex = rotate(w.draft.providerIndex, len(w.providers))
		w.draft.modelIndex = 0
		w.draft.reasoningIndex = preferredReasoningIndex(w.reasoningChoices(), priorReasoning)
		w.draft.qwenAcknowledged = false
	case jobStepModel:
		priorReasoning := w.reasoning()
		w.draft.modelIndex = rotate(w.draft.modelIndex, len(w.modelChoices()))
		w.draft.reasoningIndex = preferredReasoningIndex(w.reasoningChoices(), priorReasoning)
	case jobStepReasoning:
		w.draft.reasoningIndex = rotate(w.draft.reasoningIndex, len(w.reasoningChoices()))
	case jobStepPublicWeb:
		w.draft.publicWeb = !w.draft.publicWeb
	case jobStepStartMode:
		w.draft.startAfterCheck = !w.draft.startAfterCheck
	}
}

func (w *jobWizard) provider() jobProviderChoice {
	if len(w.providers) == 0 {
		return jobProviderChoice{}
	}
	index := min(max(0, w.draft.providerIndex), len(w.providers)-1)
	return w.providers[index]
}

func (w *jobWizard) modelChoices() []string {
	models := w.provider().Models
	if len(models) == 0 {
		return []string{"model-required"}
	}
	return models
}

func (w *jobWizard) model() string {
	models := w.modelChoices()
	return models[min(max(0, w.draft.modelIndex), len(models)-1)]
}

func (w *jobWizard) reasoningChoices() []string {
	modes := append([]string(nil), modelinfo.Lookup(w.model()).ReasoningModes...)
	if len(modes) == 0 {
		if w.provider().ID != "" && modelinfo.Provider(w.provider().ID).Custom {
			modes = []string{"off", "low", "medium", "high", "xhigh", "max"}
			if extra := modelinfo.NormalizeReasoningEffort(w.reasoningExtra); extra != "" && !slices.Contains(modes, extra) {
				modes = append(modes, extra)
			}
			return modes
		}
		return []string{"off"}
	}
	return modes
}

func preferredReasoningIndex(modes []string, preferred string) int {
	preferred = modelinfo.NormalizeReasoningEffort(preferred)
	for index, mode := range modes {
		if mode == preferred {
			return index
		}
	}
	for index, mode := range modes {
		if mode == "high" {
			return index
		}
	}
	return 0
}

func (w *jobWizard) reasoning() string {
	modes := w.reasoningChoices()
	return modes[min(max(0, w.draft.reasoningIndex), len(modes)-1)]
}

func (w *jobWizard) preset() string {
	return jobPresetChoices[min(max(0, w.draft.presetIndex), len(jobPresetChoices)-1)]
}

func (w *jobWizard) request() (gatewayapi.CreateJobRequest, bool, error) {
	if w.step != jobStepReview {
		return gatewayapi.CreateJobRequest{}, false, fmt.Errorf("wizard is not at review")
	}
	if w.provider().ID == modelinfo.ProviderQwen && !w.draft.qwenAcknowledged {
		return gatewayapi.CreateJobRequest{}, false, fmt.Errorf("Qwen unattended-use warning is not acknowledged")
	}
	writerPreset := jobPresetHasWriter(w.preset())
	if len(w.draft.writeRoots) != 0 && !writerPreset {
		return gatewayapi.CreateJobRequest{}, false, fmt.Errorf("preset %q has no isolated writer; clear write roots or choose coding, debug, or writing", w.preset())
	}
	tools := make([]string, 0, 14)
	if len(w.draft.readRoots) != 0 {
		tools = append(tools, "fs_find_files", "fs_glob", "fs_grep", "fs_list", "fs_read_file", "fs_search")
	}
	if len(w.draft.writeRoots) != 0 {
		tools = append(tools, "fs_edit_file", "fs_make_dir", "fs_write_file")
	}
	networkHosts := []string(nil)
	if w.draft.publicWeb {
		tools = append(tools, "web_crawl", "web_extract", "web_fetch", "web_search")
		networkHosts = []string{"*"}
	}
	if len(tools) != 0 {
		tools = append(tools, "time_now")
		slices.Sort(tools)
	}
	effort := w.reasoning()
	thinking := "enabled"
	if effort == "off" {
		thinking = "disabled"
	}
	route := jobdomain.ExecutionRoute{
		ProviderID:      modelinfo.NormalizeProvider(w.provider().ID),
		ModelID:         modelinfo.NormalizeAlias(w.model()),
		Thinking:        thinking,
		ReasoningEffort: effort,
	}
	if resolved := modelinfo.ProviderForModel(route.ModelID, route.ProviderID); resolved != route.ProviderID {
		return gatewayapi.CreateJobRequest{}, false, fmt.Errorf("model %q belongs to provider %q", route.ModelID, resolved)
	}
	provider := modelinfo.Provider(route.ProviderID)
	if err := modelinfo.ValidateCapabilityPolicy(modelinfo.CapabilityPolicyRequest{
		Provider: route.ProviderID, Model: route.ModelID, Thinking: route.Thinking,
		ReasoningEffort: route.ReasoningEffort, RequireToolCalls: route.ProviderID != modelinfo.ProviderMock,
		RequireStreaming: true, AllowUnknownModels: provider.Custom,
	}); err != nil {
		return gatewayapi.CreateJobRequest{}, false, err
	}
	request := gatewayapi.CreateJobRequest{
		Goal:            w.draft.goal,
		Preset:          w.preset(),
		Workers:         w.draft.workers,
		MinCycles:       w.draft.minCycles,
		Route:           route,
		DurationSeconds: uint64(w.draft.duration / time.Second),
		Budget: jobdomain.Budget{
			MaxCycles: w.draft.maxCycles, MaxAttempts: w.draft.maxAttempts,
			MaxModelCalls: w.draft.maxModelCalls, MaxTokens: w.draft.maxTokens,
		},
		Authority: jobdomain.Authority{
			Mode: jobdomain.AuthorityModeAllowList, Tools: tools,
			ReadRoots: append([]string(nil), w.draft.readRoots...), WriteRoots: append([]string(nil), w.draft.writeRoots...),
			NetworkHosts: networkHosts, Providers: []string{route.ProviderID},
		},
		AutoStart: false,
	}
	if w.draft.minRuntime > 0 {
		if w.draft.maxCycles < 2 {
			return gatewayapi.CreateJobRequest{}, false, fmt.Errorf("minimum runtime requires at least 2 maximum cycles")
		}
		if w.draft.minRuntime+time.Second > w.draft.duration {
			return gatewayapi.CreateJobRequest{}, false, fmt.Errorf("minimum runtime must leave at least one second before the hard duration")
		}
		minimumSeconds := uint64(w.draft.minRuntime / time.Second)
		intervals := w.draft.maxCycles - 1
		cadence := minimumSeconds / intervals
		if minimumSeconds%intervals != 0 {
			cadence++
		}
		request.MinRuntimeSeconds = minimumSeconds
		request.CadenceSeconds = cadence
	}
	if _, _, err := request.ResolveSchedule(time.Now().UTC()); err != nil {
		return gatewayapi.CreateJobRequest{}, false, err
	}
	return request, w.draft.startAfterCheck, nil
}

func jobPresetHasWriter(preset string) bool {
	return preset == jobdomain.PresetCoding || preset == jobdomain.PresetDebug || preset == jobdomain.PresetWriting
}

func jobWizardStepTitle(step jobWizardStep) string {
	switch step {
	case jobStepGoal:
		return "Goal"
	case jobStepPreset:
		return "Workflow preset"
	case jobStepWorkers:
		return "Parallel worker roles"
	case jobStepProvider:
		return "Provider"
	case jobStepModel:
		return "Model"
	case jobStepReasoning:
		return "Reasoning"
	case jobStepDuration:
		return "Hard wall-clock duration"
	case jobStepMinRuntime:
		return "Earliest successful completion"
	case jobStepMinCycles:
		return "Minimum review cycles"
	case jobStepMaxCycles:
		return "Maximum cycles"
	case jobStepMaxAttempts:
		return "Maximum attempts"
	case jobStepMaxModelCalls:
		return "Maximum model calls"
	case jobStepMaxTokens:
		return "Maximum provider-reported tokens"
	case jobStepReadRoots:
		return "Filesystem read roots"
	case jobStepWriteRoots:
		return "Filesystem write roots"
	case jobStepPublicWeb:
		return "Unrestricted public web research"
	case jobStepStartMode:
		return "Launch mode"
	case jobStepReview:
		return "Review and confirm"
	default:
		return "New job"
	}
}
