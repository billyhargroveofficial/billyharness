package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	jobdomain "github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

func (m Model) jobsView() tea.View {
	if m.width == 0 || m.jobs == nil {
		view := tea.NewView("starting jobs...")
		m.applyTerminalMode(&view)
		return view
	}
	content := m.jobs.render(m.styles(), m.width, m.height)
	view := tea.NewView(content)
	view.ForegroundColor = lipgloss.Color(m.styles().foreground)
	m.applyTerminalMode(&view)
	return view
}

func (s *jobsScreen) render(styles themeStyles, width, height int) string {
	width, height = max(1, width), max(3, height)
	header := s.renderHeader(styles, width)
	footer := s.renderFooter(styles, width)
	bodyHeight := max(1, height-2)
	var body []string
	switch s.mode {
	case jobsModeWizard:
		body = s.renderWizard(styles, width, bodyHeight)
	case jobsModeDetail:
		all := s.detailLines(styles, width)
		start := min(max(0, s.detailScroll), max(0, len(all)-bodyHeight))
		body = all[start:min(len(all), start+bodyHeight)]
	default:
		body = s.listLines(styles, width, bodyHeight)
	}
	for len(body) < bodyHeight {
		body = append(body, "")
	}
	if len(body) > bodyHeight {
		body = body[:bodyHeight]
	}
	parts := make([]string, 0, height)
	parts = append(parts, header)
	for _, line := range body {
		parts = append(parts, jobFitRendered(line, width))
	}
	parts = append(parts, footer)
	return strings.Join(parts, "\n")
}

func (s *jobsScreen) renderHeader(styles themeStyles, width int) string {
	mode := "LIST"
	if s.mode == jobsModeDetail {
		mode = "DETAIL"
	} else if s.mode == jobsModeWizard {
		mode = "NEW JOB"
	}
	parts := []string{" DURABLE JOBS", mode}
	if s.pendingAction != "" {
		parts = append(parts, fmt.Sprintf("ACTION %s %s PENDING", strings.ToUpper(string(s.pendingAction)), jobShortID(s.pendingJobID)))
	} else if s.reconcilingAction != "" {
		parts = append(parts, fmt.Sprintf("ACTION %s %s RECONCILING", strings.ToUpper(string(s.reconcilingAction)), jobShortID(s.reconcilingJobID)))
	}
	if s.mode != jobsModeWizard {
		if !s.lastPoll.IsZero() {
			parts = append(parts, "polled "+jobRelative(s.now().Sub(s.lastPoll))+" ago")
		} else if s.loading {
			parts = append(parts, "loading")
		}
	}
	return jobFitRendered(styles.header.Render(strings.Join(parts, "  ·  ")), width)
}

func (s *jobsScreen) renderFooter(styles themeStyles, width int) string {
	text := ""
	if s.pendingAction != "" {
		text = fmt.Sprintf("%s pending; q/Esc close; reopen reconciles durable state", s.pendingAction)
	} else if s.reconcilingAction != "" {
		text = fmt.Sprintf("%s acknowledgement uncertain; reconciling; q/Esc close", s.reconcilingAction)
	} else if s.confirmCancelJobID != "" {
		text = "Cancel " + jobShortID(s.confirmCancelJobID) + "?  y confirm  Enter/Esc/n keep"
	} else if s.mode == jobsModeWizard {
		if s.wizard != nil && s.wizard.step == jobStepReview {
			if s.wizard.provider().ID == modelinfo.ProviderQwen {
				text = "↑/↓/Pg scroll  a acknowledge Qwen warning  y create  Shift+Tab back  Esc discard"
			} else {
				text = "↑/↓/Pg scroll  y create  Shift+Tab back  Esc discard"
			}
		} else {
			text = "Enter next  Shift+Tab back  Alt+Enter newline  Esc discard"
		}
	} else if s.mode == jobsModeDetail {
		if jobStopInProgress(s.detail.State) {
			text = "↑/↓ scroll  stop/drain in progress; controls disabled  c copy  f refresh  Esc list  q close"
		} else {
			text = "↑/↓ scroll  s start  p pause  r resume  x cancel  c copy result  f refresh  Esc list  q close"
		}
	} else {
		text = "↑/↓ select  Enter inspect  n new  s start  p pause  r resume  x cancel  f refresh  Esc close"
	}
	if width < 80 && s.pendingAction == "" && s.reconcilingAction == "" && s.confirmCancelJobID == "" && s.mode != jobsModeWizard {
		if s.mode == jobsModeDetail {
			if jobStopInProgress(s.detail.State) {
				text = "↑↓ scroll  stop/drain pending  c copy  f refresh  Esc list"
			} else {
				text = "↑↓ scroll  p/r control  x cancel  c copy  f refresh  Esc list"
			}
		} else {
			text = "↑↓ select  Enter inspect  n new  x cancel  f refresh  Esc close"
		}
	}
	return jobFitRendered(styles.footer.Render(text), width)
}

func (s *jobsScreen) listLines(styles themeStyles, width, height int) []string {
	var lines []string
	lines = append(lines, styles.statusDim.Render(
		"workers are workflow roles; actual gateway job concurrency is not exposed and may serialize them",
	))
	if s.err != "" {
		lines = append(lines, styles.activity.Error.Render("ERROR  "+jobSingleLine(s.err)))
	} else if s.notice != "" {
		lines = append(lines, styles.status.Render(jobSingleLine(s.notice)))
	}
	if s.pendingAction != "" {
		lines = append(lines, styles.status.Render(fmt.Sprintf("%s pending for %s — q/Esc closes the panel; durable state will reconcile on reopen", s.pendingAction, jobShortID(s.pendingJobID))))
	}
	if s.loading && len(s.list) == 0 {
		return append(lines, "Loading durable jobs…")
	}
	if len(s.list) == 0 {
		if s.err != "" && !s.listLoaded {
			return append(lines, "No successful job-list snapshot is available; the gateway may be unreachable.")
		}
		return append(lines, "No durable jobs. Press n to create one.")
	}
	lines = append(lines, styles.statusDim.Render("STATE      ID              PRESET      CYCLE  CALLS   TOKENS   ELAPSED   DEADLINE / GOAL"))
	available := max(1, height-len(lines))
	selected := s.selectedIndex()
	start := selected - available + 1
	if start < 0 {
		start = 0
	}
	if start+available > len(s.list) {
		start = max(0, len(s.list)-available)
	}
	for index := start; index < min(len(s.list), start+available); index++ {
		item := s.list[index]
		state := jobSingleLine(string(item.Status))
		if item.Active {
			state += "*"
		}
		deadline := jobDeadlineRemaining(item.Deadline, s.now())
		goal := jobSingleLine(item.Goal)
		line := fmt.Sprintf("%-10s %-15s %-11s %5d %6d %8s %9s  %s  %s",
			state, jobShortID(item.ID), jobSingleLine(item.Preset), item.Cycle,
			item.Usage.ModelCalls, jobCompactNumber(item.Usage.TotalTokens()),
			jobElapsed(item.AdmittedAt, s.now()), deadline, goal)
		style := styles.popupLine
		prefix := "  "
		if item.ID == s.selectedID {
			style = styles.popupSelected
			prefix = "› "
		}
		lines = append(lines, style.Render(prefix+jobFitPlain(line, max(1, width-2))))
	}
	return lines
}

func (s *jobsScreen) detailLines(styles themeStyles, width int) []string {
	if s.detail.State.Spec.ID == "" {
		lines := []string{"Loading job detail…"}
		if s.err != "" {
			lines = append(lines, styles.error.Render("ERROR  "+jobSingleLine(s.err)))
		}
		return lines
	}
	state := s.detail.State
	contentWidth := max(12, width-2)
	var lines []string
	appendWrapped := func(text string) {
		wrapped := ansi.Hardwrap(jobMultiline(text), contentWidth, false)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	status := strings.ToUpper(string(state.Status))
	if state.TerminalReason != "" {
		status += " / " + string(state.TerminalReason)
	}
	appendWrapped(fmt.Sprintf("JOB %s  ·  %s  ·  active=%t  ·  revision=%d",
		jobSingleLine(state.Spec.ID), status, s.detail.Active, state.Revision))
	appendWrapped(fmt.Sprintf("workflow: preset=%s workers=%d cycle=%d stage=%s",
		state.Spec.Preset, state.Spec.Workers, state.Cycle, currentJobStage(state)))
	appendWrapped("topology: " + jobStoredWorkflowTopology(state.Spec))
	appendWrapped("barriers: each stage waits for all listed roles; a supervisor continuation may enter durable cadence before the next cycle")
	appendWrapped(fmt.Sprintf("route: %s / %s  thinking=%s reasoning=%s",
		state.Spec.Route.ProviderID, state.Spec.Route.ModelID, state.Spec.Route.Thinking, state.Spec.Route.ReasoningEffort))
	appendWrapped(fmt.Sprintf("deadline: %s  remaining=%s", jobTime(state.Spec.Deadline), jobDeadlineRemaining(state.Spec.Deadline, s.now())))
	if !state.Spec.NotBeforeComplete.IsZero() {
		appendWrapped(fmt.Sprintf("earliest success: %s  cadence=%s  next wake=%s",
			jobTime(state.Spec.NotBeforeComplete), time.Duration(state.Spec.CycleCadenceSeconds)*time.Second, jobOptionalTime(state.NextWakeAt)))
	} else {
		appendWrapped("earliest success: no wall-clock floor  ·  next wake=" + jobOptionalTime(state.NextWakeAt))
	}
	if state.Spec.AdmittedAt.IsZero() {
		appendWrapped("admitted: unavailable (legacy spec)  elapsed=unavailable")
	} else {
		appendWrapped(fmt.Sprintf("admitted: %s  elapsed=%s", jobTime(state.Spec.AdmittedAt), jobElapsed(state.Spec.AdmittedAt, s.now())))
	}
	appendWrapped(fmt.Sprintf("budget: cycles=%d (min=%d max=%d)  attempts=%d/%d  calls=%d/%d  tokens=%d/%d",
		state.Usage.Cycles, state.Spec.EffectiveMinCycles(), state.Spec.Budget.MaxCycles,
		state.Usage.Attempts, state.Spec.Budget.MaxAttempts, state.Usage.ModelCalls, state.Spec.Budget.MaxModelCalls,
		state.Usage.TotalTokens(), state.Spec.Budget.MaxTokens))
	appendWrapped(fmt.Sprintf("history: attempts=%d completed batches=%d artifacts=%d",
		s.detail.History.Attempts, s.detail.History.CompletedBatches, s.detail.History.Artifacts))
	appendWrapped("concurrency: requested role workers are shown below; live gateway invocation limit is not exposed and may serialize them")
	if jobStopInProgress(state) {
		appendWrapped(fmt.Sprintf("stop/drain: cancel_requested=%t pending_stop=%s; active invocations may still be settling",
			state.CancelRequested, jobOptionalStop(state.PendingStop)))
	}
	if s.detail.LastError != "" {
		appendWrapped("last error: " + s.detail.LastError)
	}
	if state.WaitingReason != "" {
		appendWrapped("waiting: " + state.WaitingReason)
	}
	if state.LastDecision != nil {
		appendWrapped(fmt.Sprintf("last supervisor decision: %s — %s", state.LastDecision.Kind, state.LastDecision.Reason))
		if state.LastDecision.NextBatch != nil {
			for _, item := range state.LastDecision.NextBatch.Items {
				appendWrapped("next objective [" + item.RoleID + "]: " + item.Objective)
			}
		}
	}
	if s.err != "" {
		appendWrapped("panel error: " + s.err)
	} else if s.notice != "" {
		appendWrapped("panel: " + s.notice)
	}
	if s.attemptsErr != "" {
		appendWrapped("attempt history error: " + s.attemptsErr)
	}
	if s.artifactsErr != "" {
		appendWrapped("artifact history error: " + s.artifactsErr)
	}

	lines = append(lines, "", styles.runStatus.Render("ROLE STATUS (latest bounded attempt tail)"))
	lines = append(lines, s.roleLines(styles, width)...)
	lines = append(lines, "", styles.runStatus.Render("AUTHORITY"))
	appendWrapped("tools: " + jobListValue(state.Spec.Authority.Tools))
	appendWrapped("read roots: " + jobListValue(state.Spec.Authority.ReadRoots))
	appendWrapped("write roots: " + jobListValue(state.Spec.Authority.WriteRoots))
	appendWrapped("network: " + jobListValue(state.Spec.Authority.NetworkHosts))
	appendWrapped("provider: " + jobListValue(state.Spec.Authority.Providers))
	lines = append(lines, "", styles.runStatus.Render("LATEST ARTIFACT REFS"))
	if len(s.artifacts) == 0 {
		if s.detail.History.Artifacts > 0 {
			appendWrapped(fmt.Sprintf("latest refs are loading or unavailable; canonical history contains %d artifact(s)", s.detail.History.Artifacts))
		} else {
			lines = append(lines, "none")
		}
	} else {
		start := max(0, len(s.artifacts)-12)
		shown := len(s.artifacts) - start
		for _, artifact := range s.artifacts[start:] {
			appendWrapped(fmt.Sprintf("%s  %s  %s", artifact.ID, artifact.MediaType, artifact.URI))
		}
		if s.detail.History.Artifacts > uint64(shown) {
			appendWrapped(fmt.Sprintf("… showing latest %d of %d; %d older refs omitted from the panel",
				shown, s.detail.History.Artifacts, s.detail.History.Artifacts-uint64(shown)))
		}
	}
	lines = append(lines, "", styles.runStatus.Render("GOAL"))
	appendWrapped(state.Spec.Goal)
	if strings.TrimSpace(state.FinalResult) != "" {
		lines = append(lines, "", styles.runStatus.Render("FINAL RESULT"))
		appendWrapped(state.FinalResult)
	}
	return lines
}

func (s *jobsScreen) roleLines(styles themeStyles, width int) []string {
	state := s.detail.State
	latest := make(map[string]jobdomain.Attempt)
	writers := make(map[string]bool)
	for _, role := range state.Spec.Roles {
		writers[role.ID] = role.Writer
	}
	for _, attempt := range s.attempts {
		prior, exists := latest[attempt.RoleID]
		if !exists || attempt.Cycle > prior.Cycle ||
			(attempt.Cycle == prior.Cycle && attempt.AttemptNo >= prior.AttemptNo) {
			latest[attempt.RoleID] = attempt
		}
	}
	current := make(map[string]bool)
	if state.CurrentBatch != nil {
		for _, item := range state.CurrentBatch.Items {
			current[item.RoleID] = true
		}
	}
	roleIDs := append([]string(nil), state.Spec.Workflow.WorkerRoleIDs...)
	for _, roleID := range []string{state.Spec.Workflow.ReducerRoleID, state.Spec.Workflow.SupervisorRoleID} {
		if roleID != "" && !slicesContains(roleIDs, roleID) {
			roleIDs = append(roleIDs, roleID)
		}
	}
	if len(roleIDs) == 0 {
		for _, role := range state.Spec.Roles {
			roleIDs = append(roleIDs, role.ID)
		}
		sort.Strings(roleIDs)
	}
	var lines []string
	for _, roleID := range roleIDs {
		kind := "worker"
		if writers[roleID] {
			kind = "writer"
		} else if roleID == state.Spec.Workflow.ReducerRoleID {
			kind = "reducer"
		} else if roleID == state.Spec.Workflow.SupervisorRoleID {
			kind = "supervisor"
		}
		status, meta := "not started", ""
		if s.detail.History.Attempts > uint64(len(s.attempts)) {
			status = "not present in recent tail"
		}
		attempt, exists := latest[roleID]
		if current[roleID] {
			status = "queued in current batch"
		}
		if exists {
			status = string(attempt.Status)
			meta = fmt.Sprintf("cycle=%d stage=%s try=%d", attempt.Cycle, attempt.StageID, attempt.AttemptNo)
			if current[roleID] && state.CurrentBatch != nil &&
				(attempt.Cycle != state.CurrentBatch.Cycle || attempt.StageID != state.CurrentBatch.StageID) {
				status = "queued in current batch; latest=" + string(attempt.Status)
			}
		}
		line := fmt.Sprintf("%-11s %-30s %-28s %s", kind, jobSingleLine(roleID), status, meta)
		lines = append(lines, styles.popupLine.Render(jobFitPlain(line, max(1, width-2))))
		if exists && width >= 90 {
			snippet := attempt.Error
			if snippet == "" {
				snippet = attempt.Result
			}
			if snippet != "" {
				lines = append(lines, styles.statusDim.Render("  ↳ "+jobFitPlain(jobSingleLine(snippet), max(1, width-6))))
			}
		}
	}
	if len(lines) == 0 {
		return []string{"No role metadata available."}
	}
	return lines
}

func (s *jobsScreen) maxDetailScroll(width, height int) int {
	return max(0, len(s.detailLines(themeStyles{}, max(1, width)))-max(1, height-2))
}

func (s *jobsScreen) renderWizard(styles themeStyles, width, bodyHeight int) []string {
	w := s.wizard
	if w == nil {
		return []string{"Wizard unavailable."}
	}
	contentWidth := max(12, width-4)
	lines := []string{
		styles.runStatus.Render(fmt.Sprintf("NEW DURABLE JOB  ·  step %d/%d  ·  %s", int(w.step)+1, int(jobStepCount), jobWizardStepTitle(w.step))),
	}
	if s.err != "" {
		lines = append(lines, styles.activity.Error.Render("ERROR  "+jobSingleLine(s.err)))
	} else if s.notice != "" {
		lines = append(lines, styles.status.Render(jobSingleLine(s.notice)))
	}
	if w.err != "" {
		lines = append(lines, styles.activity.Error.Render("ERROR  "+jobSingleLine(w.err)))
	}
	if s.pendingAction != "" {
		lines = append(lines, styles.status.Render(fmt.Sprintf("%s pending for %s; closing is safe and reopen reconciles", s.pendingAction, jobShortID(s.pendingJobID))))
	}
	if w.step == jobStepReview {
		review := w.reviewDisplayLines(contentWidth)
		available := s.wizardReviewAvailable(bodyHeight)
		start := min(max(0, w.reviewScroll), max(0, len(review)-available))
		lines = append(lines, review[start:min(len(review), start+available)]...)
		return lines
	}
	lines = append(lines, styles.statusDim.Render(w.stepHelp()))
	if w.isChoiceStep() {
		lines = append(lines, "", styles.popupSelected.Render("‹  "+jobSingleLine(w.choiceValue())+"  ›"))
		return lines
	}
	editor := w.editor
	editor.SetWidth(contentWidth)
	editor.SetHeight(min(w.editorHeight(), max(1, bodyHeight-len(lines)-3)))
	editor.SetStyles(styles.textarea)
	lines = append(lines, "")
	lines = append(lines, strings.Split(renderInputFrame(editor.View(), contentWidth, styles.inputBorder), "\n")...)
	return lines
}

func (s *jobsScreen) wizardReviewAvailable(bodyHeight int) int {
	prefix := 1 // NEW DURABLE JOB title
	if s.err != "" || s.notice != "" {
		prefix++
	}
	if s.wizard != nil && s.wizard.err != "" {
		prefix++
	}
	if s.pendingAction != "" {
		prefix++
	}
	return max(1, bodyHeight-prefix)
}

func (w *jobWizard) stepHelp() string {
	switch w.step {
	case jobStepGoal:
		return "Describe the bounded outcome and completion criteria. Alt+Enter inserts a newline."
	case jobStepPreset:
		return "The selected preset expands to the exact stage topology shown below; [n] is the number of roles behind that stage barrier."
	case jobStepWorkers:
		return "1–4 role workers are requested; the gateway-wide limit may serialize them."
	case jobStepProvider:
		return "Built-ins plus this TUI's configured custom binding are offered; the gateway validates its own exact route."
	case jobStepModel:
		return "Models come from the selected provider registry."
	case jobStepReasoning:
		return "Reasoning is validated against the provider/model capability policy."
	case jobStepDuration:
		return "Hard stop, from 1s through 7d. This is a cap, not promised active compute."
	case jobStepMinRuntime:
		return "Optional (off or e.g. 5h). Queueing, pauses, and gateway downtime count; this is not guaranteed useful compute. Cadence is derived across max cycles."
	case jobStepMinCycles:
		return "Supervisor cannot accept success before this many complete cycles."
	case jobStepMaxCycles:
		return "Hard review-cycle budget; must be at least the minimum."
	case jobStepMaxAttempts:
		return "Hard persisted-attempt budget across every role and retry."
	case jobStepMaxModelCalls:
		return "Hard provider model-call budget."
	case jobStepMaxTokens:
		return "Hard sum of provider-reported input and output tokens."
	case jobStepReadRoots:
		return "Optional; one absolute clean path per line. Roots must be covered by gateway workspace authority (normally its launch CWD); safe start can verify this only after queued creation."
	case jobStepWriteRoots:
		return "Optional; coding/debug/writing only. Roots must be covered by gateway workspace authority (normally its launch CWD); safe start verifies the persisted queued job."
	case jobStepPublicWeb:
		return "Explicitly grants web_search/fetch/extract/crawl to unrestricted public HTTPS destinations."
	case jobStepStartMode:
		return "Safe start creates QUEUED, verifies persisted authority was not clamped, then starts."
	default:
		return ""
	}
}

func (w *jobWizard) choiceValue() string {
	switch w.step {
	case jobStepPreset:
		return w.preset() + " · " + jobWorkflowTopology(w.preset(), w.draft.workers)
	case jobStepWorkers:
		return fmt.Sprintf("%d role worker(s)", w.draft.workers)
	case jobStepProvider:
		return w.provider().Name + " [" + w.provider().ID + "]"
	case jobStepModel:
		return w.model()
	case jobStepReasoning:
		return w.reasoning()
	case jobStepPublicWeb:
		if w.draft.publicWeb {
			return "ON — unrestricted public web read"
		}
		return "OFF — no network authority"
	case jobStepStartMode:
		if w.draft.startAfterCheck {
			return "START AFTER AUTHORITY CHECK"
		}
		return "CREATE QUEUED"
	default:
		return ""
	}
}

func (w *jobWizard) reviewLines() []string {
	request, _, requestErr := w.requestWithoutAcknowledgement()
	lines := []string{
		"Goal: " + jobSingleLine(w.draft.goal),
		fmt.Sprintf("Workflow: %s · workers=%d", w.preset(), w.draft.workers),
		"Topology: " + jobWorkflowTopology(w.preset(), w.draft.workers),
		fmt.Sprintf("Route: %s / %s · reasoning=%s", w.provider().ID, w.model(), w.reasoning()),
		fmt.Sprintf("Hard duration: %s · earliest success=%s · cycles=%d..%d", w.draft.duration, jobOptionalDuration(w.draft.minRuntime), w.draft.minCycles, w.draft.maxCycles),
		fmt.Sprintf("Budgets: attempts=%d · calls=%d · tokens=%d", w.draft.maxAttempts, w.draft.maxModelCalls, w.draft.maxTokens),
		"Read roots: " + jobListValue(w.draft.readRoots),
		"Write roots: " + jobListValue(w.draft.writeRoots),
		"Gateway authority boundary: requested roots must be covered by the gateway workspace authority (normally its launch CWD); coverage is known only from the persisted queued-create response.",
		fmt.Sprintf("Public web: %t · launch: %s", w.draft.publicWeb, map[bool]string{true: "start after authority check", false: "create queued"}[w.draft.startAfterCheck]),
	}
	if requestErr == nil {
		lines = append(lines, "Tools: "+jobListValue(request.Authority.Tools))
		requiredCycles := request.MinCycles
		if requiredCycles == 0 {
			requiredCycles = 1
		}
		if request.MinRuntimeSeconds > 0 && request.CadenceSeconds > 0 {
			scheduled := uint64(1) + (request.MinRuntimeSeconds+request.CadenceSeconds-1)/request.CadenceSeconds
			if scheduled > requiredCycles {
				requiredCycles = scheduled
			}
			lines = append(lines, fmt.Sprintf("Wall-clock pacing: cadence=%s; paused/offline time still counts", time.Duration(request.CadenceSeconds)*time.Second))
		}
		lines = append(lines, fmt.Sprintf("Arithmetic floor: at least %d stage-role invocations across %d required cycle(s)", jobInvocationFloor(w.preset(), w.draft.workers, requiredCycles), requiredCycles))
	} else {
		lines = append(lines, "Validation: "+requestErr.Error())
	}
	if jobPresetHasWriter(w.preset()) {
		lines = append(lines, "Coding boundary: structured filesystem inspect/edit only; durable jobs cannot execute shell, builds/tests, diagnostics, MCP, skills, or secrets.")
	}
	if w.provider().ID == modelinfo.ProviderQwen {
		ack := "NOT ACKNOWLEDGED"
		if w.draft.qwenAcknowledged {
			ack = "ACKNOWLEDGED"
		}
		lines = append(lines,
			"QWEN WARNING: the built-in route uses Token Plan Individual by default. Current terms permit interactive programming/agent-tool use but prohibit automated scripts, application backends, and non-interactive batch processing.",
			"Do not leave this job unattended unless your configured endpoint/plan explicitly permits automation. Press a to toggle acknowledgement: "+ack,
		)
	}
	lines = append(lines, "Press y to create. Enter is deliberately not confirmation.")
	return lines
}

func (w *jobWizard) reviewDisplayLines(width int) []string {
	var lines []string
	for _, line := range w.reviewLines() {
		wrapped := ansi.Hardwrap(jobMultiline(line), max(1, width), false)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines
}

func (w *jobWizard) requestWithoutAcknowledgement() (gatewayapi.CreateJobRequest, bool, error) {
	ack := w.draft.qwenAcknowledged
	w.draft.qwenAcknowledged = true
	request, start, err := w.request()
	w.draft.qwenAcknowledged = ack
	return request, start, err
}

func jobInvocationFloor(preset string, workers int, cycles uint64) uint64 {
	workflow, err := jobdomain.CompilePreset(preset, workers)
	if err != nil {
		return 0
	}
	perCycle := uint64(0)
	for _, stage := range workflow.Stages {
		perCycle += uint64(len(stage.RoleIDs))
	}
	if cycles == 0 {
		cycles = 1
	}
	return perCycle * cycles
}

func jobWorkflowTopology(preset string, workers int) string {
	workflow, err := jobdomain.CompilePreset(preset, workers)
	if err != nil {
		return "invalid workflow: " + jobSingleLine(err.Error())
	}
	return jobStageTopology(workflow.StageOrder, workflow.Stages)
}

func jobStoredWorkflowTopology(spec jobdomain.JobSpec) string {
	return jobStageTopology(spec.Workflow.StageOrder, spec.Stages)
}

func jobStageTopology(order []string, stages []jobdomain.StageSpec) string {
	byID := make(map[string]jobdomain.StageSpec, len(stages))
	for _, stage := range stages {
		byID[stage.ID] = stage
	}
	parts := make([]string, 0, len(order))
	for _, stageID := range order {
		stage, ok := byID[stageID]
		if !ok {
			parts = append(parts, jobSingleLine(stageID)+"[?]")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s[%d]", jobSingleLine(stageID), len(stage.RoleIDs)))
	}
	if len(parts) == 0 {
		return "not persisted"
	}
	return strings.Join(parts, " → ")
}

func jobOptionalStop(reason jobdomain.TerminalReason) string {
	if reason == "" {
		return "none"
	}
	return jobSingleLine(string(reason))
}

func jobOptionalDuration(value time.Duration) string {
	if value <= 0 {
		return "off"
	}
	return value.String()
}

func currentJobStage(state jobdomain.JobState) string {
	if state.Status.IsTerminal() {
		return "terminal"
	}
	if state.CurrentBatch != nil && strings.TrimSpace(state.CurrentBatch.StageID) != "" {
		return state.CurrentBatch.StageID
	}
	if state.NextStageIndex >= 0 && state.NextStageIndex < len(state.Spec.Workflow.StageOrder) {
		return state.Spec.Workflow.StageOrder[state.NextStageIndex]
	}
	return "-"
}

func jobSingleLine(value string) string {
	return strings.Join(strings.Fields(jobMultiline(value)), " ")
}

func jobMultiline(value string) string {
	return strings.TrimSpace(stripJobTerminalControls(value))
}

func stripJobTerminalControls(value string) string {
	var out strings.Builder
	for _, r := range strings.ReplaceAll(value, "\r\n", "\n") {
		switch {
		case r == '\n':
			out.WriteRune(r)
		case r == '\t':
			out.WriteRune(' ')
		case r == '\r':
			out.WriteRune('\n')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func jobFitPlain(value string, width int) string {
	value = jobSingleLine(value)
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

func jobFitRendered(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "")
}

func jobShortID(value string) string {
	value = jobSingleLine(value)
	if len([]rune(value)) <= 15 {
		return value
	}
	return truncateRunes(value, 12) + "…"
}

func jobCompactNumber(value uint64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(value)/1_000_000_000)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func jobDeadlineRemaining(deadline, now time.Time) string {
	if deadline.IsZero() {
		return "no deadline"
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return "expired"
	}
	return jobRelative(remaining) + " left"
}

func jobElapsed(admittedAt, now time.Time) string {
	if admittedAt.IsZero() {
		return "unavailable"
	}
	if now.Before(admittedAt) {
		return "clock-skew"
	}
	return jobRelative(now.Sub(admittedAt))
}

func jobRelative(duration time.Duration) string {
	if duration < 0 {
		duration = -duration
	}
	if duration < time.Second {
		return "<1s"
	}
	if duration < time.Minute {
		return duration.Round(time.Second).String()
	}
	if duration < time.Hour {
		return duration.Round(time.Minute).String()
	}
	return duration.Round(time.Minute).String()
}

func jobTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func jobOptionalTime(value time.Time) string {
	if value.IsZero() {
		return "manual/none"
	}
	return jobTime(value)
}

func jobListValue(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	clean := make([]string, 0, len(values))
	for _, value := range values {
		clean = append(clean, jobSingleLine(value))
	}
	return strings.Join(clean, ", ")
}

func slicesContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
