package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	jobdomain "github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/tui/jobclient"
)

func TestJobsSlashIsGatewayOnlyAndModalKeysDoNotReachComposer(t *testing.T) {
	m := newTestModel(t)
	before := len(m.blocks)
	handled, cmd := m.handleSlashCommand("/jobs")
	if !handled || cmd != nil || m.jobs != nil {
		t.Fatalf("local /jobs = handled:%t cmd:%v screen:%v", handled, cmd, m.jobs)
	}
	if len(m.blocks) != before+1 || !strings.Contains(m.blocks[len(m.blocks)-1].Content, "gateway -job-concurrency") {
		t.Fatalf("local /jobs did not show actionable gateway setup: %#v", m.blocks)
	}

	m.gatewayURL = "http://127.0.0.1:8765"
	m.textarea.SetValue("unsent composer draft")
	m.busy = true
	handled, cmd = m.handleSlashCommand("/job")
	if !handled || cmd == nil || m.jobs == nil {
		t.Fatalf("gateway /job = handled:%t cmd:%v screen:%v", handled, cmd, m.jobs)
	}
	next, _ := m.Update(jobKey('z'))
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "unsent composer draft" {
		t.Fatalf("modal key reached composer: %q", got)
	}
	if !updated.busy {
		t.Fatal("opening jobs changed the background chat busy state")
	}
	updated.closeJobs()
	if !updated.textarea.Focused() || updated.textarea.Value() != "unsent composer draft" {
		t.Fatal("closing jobs did not restore the untouched composer")
	}
}

func TestJobsListSelectionUsesStableIDAndRejectsStaleScreenPoll(t *testing.T) {
	m, screen, _ := newJobsTestModel(t)
	screen.selectedID = "job-b"
	message := jobsListLoadedMsg{
		instance: screen.instance, generation: screen.generation, poll: 1, at: time.Now(),
		result: jobclient.ListResultMsg{Jobs: []gatewayapi.JobSummaryResponse{{ID: "job-a"}, {ID: "job-b"}}},
	}
	if handled, _ := m.updateJobs(message); !handled || screen.selectedID != "job-b" {
		t.Fatalf("stable selection = handled:%t selected:%q", handled, screen.selectedID)
	}
	if screen.selectedIndex() != 1 {
		t.Fatalf("selected index = %d, want 1", screen.selectedIndex())
	}

	stale := jobsListLoadedMsg{
		instance: screen.instance - 1, generation: screen.generation, poll: 99,
		result: jobclient.ListResultMsg{Jobs: []gatewayapi.JobSummaryResponse{{ID: "stale"}}},
	}
	_, _ = m.updateJobs(stale)
	if len(screen.list) != 2 || screen.selectedID != "job-b" {
		t.Fatalf("stale prior-screen response was applied: list=%#v selected=%q", screen.list, screen.selectedID)
	}
}

func TestJobsCancelConfirmationDefaultsNoAndSnapshotsTarget(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	screen.list = []gatewayapi.JobSummaryResponse{
		{ID: "job-a", Status: jobdomain.JobStatusRunning},
		{ID: "job-b", Status: jobdomain.JobStatusRunning},
	}
	screen.selectedID = "job-b"
	_ = m.updateJobsKey(jobKey('x'))
	if screen.confirmCancelJobID != "job-b" {
		t.Fatalf("cancel target = %q", screen.confirmCancelJobID)
	}
	screen.selectedID = "job-a"
	if cmd := m.updateJobsKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || screen.confirmCancelJobID != "" {
		t.Fatalf("Enter must default to no: cmd=%v target=%q", cmd, screen.confirmCancelJobID)
	}
	if fake.callCount("cancel:job-b") != 0 {
		t.Fatal("default-no confirmation contacted the gateway")
	}

	screen.selectedID = "job-b"
	_ = m.updateJobsKey(jobKey('x'))
	screen.selectedID = "job-a"
	cmd := m.updateJobsKey(jobKey('y'))
	if cmd == nil || screen.pendingJobID != "job-b" {
		t.Fatalf("confirmed cancel lost snapshotted target: cmd=%v pending=%q", cmd, screen.pendingJobID)
	}
	_ = cmd()
	if fake.callCount("cancel:job-b") != 1 || fake.callCount("cancel:job-a") != 0 {
		t.Fatalf("cancel calls = %#v", fake.callsSnapshot())
	}
}

func TestJobsOperationAndHistoryFencesRejectMismatchedMessages(t *testing.T) {
	m, screen, _ := newJobsTestModel(t)
	screen.mode = jobsModeDetail
	screen.selectedID = "job-a"
	screen.detail = jobsTestResponse("job-a", jobdomain.JobStatusRunning, 7)
	screen.pendingAction = jobclient.ActionPause
	screen.pendingJobID = "job-a"

	wrong := jobsOperationMsg{
		instance: screen.instance, generation: screen.generation,
		result: jobclient.JobResultMsg{Action: jobclient.ActionResume, JobID: "job-a", Response: jobsTestResponse("job-a", jobdomain.JobStatusRunning, 8)},
	}
	_, _ = m.updateJobs(wrong)
	if screen.pendingAction != jobclient.ActionPause || screen.pendingJobID != "job-a" {
		t.Fatalf("wrong operation stole pending slot: %q/%q", screen.pendingAction, screen.pendingJobID)
	}

	foreignAttempts := jobsAttemptsLoadedMsg{
		instance: screen.instance, generation: screen.generation, sequence: 99, revision: 7,
		jobID: "job-a", offset: 68, limit: 32,
		result: jobclient.AttemptsResultMsg{
			JobID: "job-other", Offset: 68, Limit: 32,
			Page: gatewayapi.JobAttemptPage{JobID: "job-other", Offset: 68, Limit: 32},
		},
	}
	_, _ = m.updateJobs(foreignAttempts)
	if screen.appliedAttempts != 0 {
		t.Fatalf("foreign attempt page poisoned sequence fence: %d", screen.appliedAttempts)
	}

	foreignArtifacts := jobsArtifactsLoadedMsg{
		instance: screen.instance, generation: screen.generation, sequence: 77, revision: 7,
		jobID: "job-a", offset: 100, limit: 500,
		result: jobclient.ArtifactsResultMsg{
			JobID: "job-a", Offset: 100, Limit: 500,
			Page: gatewayapi.JobArtifactPage{JobID: "job-a", Offset: 0, Limit: 500},
		},
	}
	_, _ = m.updateJobs(foreignArtifacts)
	if screen.appliedArtifacts != 0 {
		t.Fatalf("wrong artifact page poisoned sequence fence: %d", screen.appliedArtifacts)
	}
}

func TestJobsCommandsCarryTUIOwnerAndFetchHistoryTails(t *testing.T) {
	_, screen, fake := newJobsTestModel(t)
	response := jobsTestResponse("job-a", jobdomain.JobStatusRunning, 9)
	response.History.Attempts = 100
	response.History.Artifacts = 600

	attemptMsg := screen.attemptsCmd(response)().(jobsAttemptsLoadedMsg)
	artifactMsg := screen.artifactsCmd(response)().(jobsArtifactsLoadedMsg)
	if attemptMsg.offset != 68 || attemptMsg.limit != 32 || artifactMsg.offset != 100 || artifactMsg.limit != 500 {
		t.Fatalf("tail pages = attempts:%d/%d artifacts:%d/%d", attemptMsg.offset, attemptMsg.limit, artifactMsg.offset, artifactMsg.limit)
	}
	if got := fake.callsSnapshot(); !reflect.DeepEqual(got, []string{"attempts:job-a:68:32", "artifacts:job-a:100:500"}) {
		t.Fatalf("history calls = %#v", got)
	}
	for _, owner := range fake.ownersSnapshot() {
		if owner.ClientType != "tui" || owner.TUIChatID != "chat-test" || owner.Profile != "profile-test" || owner.Model != "model-test" {
			t.Fatalf("owner context = %#v", owner)
		}
	}
}

func TestJobsScheduledWaitIsNotResumedEarlyAndFinalResultCopies(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	screen.mode = jobsModeDetail
	screen.selectedID = "job-a"
	screen.detail = jobsTestResponse("job-a", jobdomain.JobStatusWaiting, 4)
	screen.detail.State.NextWakeAt = time.Now().UTC().Add(time.Hour)
	if cmd := m.updateJobsKey(jobKey('r')); cmd != nil || fake.callCount("resume:job-a") != 0 || !strings.Contains(screen.notice, "scheduled wait") {
		t.Fatalf("scheduled resume = cmd:%v notice:%q calls:%#v", cmd, screen.notice, fake.callsSnapshot())
	}
	screen.detail.State.Status = jobdomain.JobStatusCompleted
	screen.detail.State.TerminalReason = jobdomain.TerminalReasonSuccess
	screen.detail.State.NextWakeAt = time.Time{}
	screen.detail.State.FinalResult = "canonical answer"
	if cmd := m.updateJobsKey(jobKey('c')); cmd == nil {
		t.Fatal("copy final result did not return a command")
	}
}

func TestJobsViewSanitizesUntrustedFieldsAndFitsSmallTerminal(t *testing.T) {
	_, screen, _ := newJobsTestModel(t)
	screen.mode = jobsModeDetail
	screen.selectedID = "job-a"
	screen.detail = jobsTestResponse("job-a", jobdomain.JobStatusCompleted, 12)
	screen.detail.State.TerminalReason = jobdomain.TerminalReasonSuccess
	screen.detail.State.Spec.Goal = "goal\x1b[31m\a\u202eevil"
	screen.detail.State.FinalResult = "final\x1b]52;bad\a\nsecond line"
	screen.detail.LastError = "error\x00\u202e"
	screen.attempts = []jobdomain.Attempt{{RoleID: "research.primary", Status: jobdomain.AttemptStatusSucceeded, Cycle: 1, AttemptNo: 1, Result: "ok\x1b[2J"}}
	screen.artifacts = []jobdomain.ArtifactRef{{ID: "artifact-1", URI: "file:///tmp/result\x1b[2J", MediaType: "text/markdown"}}

	rendered := screen.render(newThemeStyles(tuiThemes["dark"]), 60, 20)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 20 {
		t.Fatalf("rendered height = %d, want 20\n%s", len(lines), rendered)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got > 60 {
			t.Fatalf("line %d width = %d, want <= 60: %q", index, got, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(rendered)
	if strings.ContainsAny(plain, "\x00\a\x1b") || strings.ContainsRune(plain, '\u202e') {
		t.Fatalf("render leaked terminal controls: %q", plain)
	}
	if got := stripJobTerminalControls("a\u202eb\x1bc"); got != "abc" {
		t.Fatalf("control sanitizer = %q", got)
	}

	screen.mode = jobsModeWizard
	screen.wizard = newJobWizard(jobWizardDefaults{
		providers: []jobProviderChoice{{
			ID: "vendor\x1b]52;provider\a", Name: "Vendor\u202e", Models: []string{"model\x1b[2J\a"},
		}},
	})
	screen.wizard.step = jobStepModel
	choice := ansi.Strip(screen.render(newThemeStyles(tuiThemes["dark"]), 60, 16))
	if strings.ContainsAny(choice, "\a\x1b") || strings.ContainsRune(choice, '\u202e') {
		t.Fatalf("wizard choice leaked terminal controls: %q", choice)
	}
}

func TestJobsViewUsesCanonicalAdmissionTimeForElapsed(t *testing.T) {
	_, screen, _ := newJobsTestModel(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	admittedAt := now.Add(-5*time.Hour - 30*time.Minute)
	screen.now = func() time.Time { return now }
	screen.mode = jobsModeList
	screen.listLoaded = true
	screen.list = []gatewayapi.JobSummaryResponse{{
		ID: "job-timed", Goal: "bounded research", Preset: jobdomain.PresetResearch,
		Status: jobdomain.JobStatusRunning, AdmittedAt: admittedAt, Deadline: now.Add(time.Hour),
	}}
	screen.selectedID = "job-timed"
	list := ansi.Strip(screen.render(newThemeStyles(tuiThemes["dark"]), 120, 12))
	if !strings.Contains(list, "5h30m") {
		t.Fatalf("list omitted canonical elapsed time:\n%s", list)
	}

	screen.mode = jobsModeDetail
	screen.detail = jobsTestResponse("job-timed", jobdomain.JobStatusRunning, 1)
	screen.detail.State.Spec.AdmittedAt = admittedAt
	detail := strings.Join(screen.detailLines(newThemeStyles(tuiThemes["dark"]), 120), "\n")
	if !strings.Contains(detail, "elapsed="+jobElapsed(admittedAt, now)) || !strings.Contains(detail, "admitted: "+jobTime(admittedAt)) {
		t.Fatalf("detail omitted canonical admission time:\n%s", detail)
	}

	screen.detail.State.Spec.AdmittedAt = time.Time{}
	legacy := strings.Join(screen.detailLines(newThemeStyles(tuiThemes["dark"]), 120), "\n")
	if !strings.Contains(legacy, "elapsed=unavailable") {
		t.Fatalf("legacy detail invented elapsed time:\n%s", legacy)
	}
	if got := jobElapsed(now.Add(time.Minute), now); got != "clock-skew" {
		t.Fatalf("future admission elapsed = %q", got)
	}
}

func TestJobsWizardReviewScrollKeepsQwenConfirmationReachable(t *testing.T) {
	_, screen, _ := newJobsTestModel(t)
	screen.mode = jobsModeWizard
	screen.wizard = newJobWizard(newJobWizardDefaults(
		modelinfo.ProviderQwen, "qwen3.8-max-preview", "high",
		modelinfo.ProviderQwen, "qwen3.8-max-preview", modelinfo.Provider(modelinfo.ProviderQwen).BaseURL,
	))
	screen.wizard.step = jobStepReview
	screen.wizard.draft.goal = strings.Repeat("long forecasting goal ", 20)
	screen.wizard.updateReviewScroll(tea.KeyPressMsg{Code: tea.KeyEnd}, 60, screen.wizardReviewAvailable(14))
	rendered := ansi.Strip(screen.render(newThemeStyles(tuiThemes["dark"]), 60, 16))
	if !strings.Contains(rendered, "Press y") || !strings.Contains(rendered, "ACKNOWLEDGED") {
		t.Fatalf("review end did not expose Qwen confirmation at 60x16:\n%s", rendered)
	}
	if strings.Count(rendered, "\n")+1 != 16 {
		t.Fatalf("review height is not bounded:\n%s", rendered)
	}
}

func TestJobWizardEditorStripsBidiFormatControlsOnPaste(t *testing.T) {
	wizard := newJobWizard(newJobWizardDefaults(
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", "high",
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", modelinfo.Provider(modelinfo.ProviderDeepSeek).BaseURL,
	))
	_ = wizard.updateEditor(tea.PasteMsg{Content: "safe\u202eevil\x1b[2J"})
	if got := wizard.editor.Value(); strings.ContainsRune(got, '\u202e') || strings.ContainsRune(got, '\x1b') {
		t.Fatalf("wizard editor retained terminal controls: %q", got)
	}
}

func TestJobsCreateAmbiguousRetryReusesPreparedIDAndBlocksDoubleSubmit(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	prepareJobsReview(screen, false)
	fake.createErr = errors.New("lost create acknowledgement")
	fake.showErr = errors.New("gateway temporarily unavailable")

	createCmd := m.updateJobsKey(jobKey('y'))
	if createCmd == nil || screen.createIntent == nil {
		t.Fatal("review confirmation did not prepare a create")
	}
	preparedID := screen.createIntent.request.JobID
	if second := m.updateJobsKey(jobKey('y')); second != nil {
		t.Fatal("a second y while create was pending submitted another command")
	}
	createMessage := createCmd().(jobsOperationMsg)
	_, showCmd := m.updateJobs(createMessage)
	if showCmd == nil || screen.pendingAction != jobclient.ActionShow || screen.pendingJobID != preparedID {
		t.Fatalf("lost acknowledgement did not trigger exact-ID recovery: pending=%s/%s", screen.pendingAction, screen.pendingJobID)
	}
	showMessage := showCmd().(jobsOperationMsg)
	_, _ = m.updateJobs(showMessage)
	if screen.createIntent == nil || !screen.createIntent.ambiguous {
		t.Fatal("non-404 recovery failure discarded the idempotent create intent")
	}
	if cmd := m.updateJobsKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || screen.createIntent == nil || screen.createIntent.request.JobID != preparedID {
		t.Fatal("a non-y key changed the ambiguous prepared create ID")
	}

	fake.createErr = nil
	fake.showErr = nil
	retryCmd := m.updateJobsKey(jobKey('y'))
	if retryCmd == nil || screen.pendingJobID != preparedID {
		t.Fatalf("retry did not reuse prepared ID: %q", screen.pendingJobID)
	}
	retryMessage := retryCmd().(jobsOperationMsg)
	_, _ = m.updateJobs(retryMessage)
	if screen.createIntent != nil || screen.detail.State.Spec.ID != preparedID {
		t.Fatalf("successful retry did not settle the prepared create: intent=%v detail=%q", screen.createIntent, screen.detail.State.Spec.ID)
	}
	if fake.callCount("create:"+preparedID) != 2 || fake.callCount("show:"+preparedID) != 1 {
		t.Fatalf("exact-ID create recovery calls = %#v", fake.callsSnapshot())
	}
	for _, call := range fake.callsSnapshot() {
		if strings.HasPrefix(call, "create:") && call != "create:"+preparedID {
			t.Fatalf("ambiguous recovery created a different ID: %#v", fake.callsSnapshot())
		}
	}
}

func TestJobsCreateLostAcknowledgementRecoversCommittedJobWithoutSecondPost(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	prepareJobsReview(screen, false)
	fake.createErr = errors.New("response stream reset after durable commit")

	createCmd := m.updateJobsKey(jobKey('y'))
	if createCmd == nil || screen.createIntent == nil {
		t.Fatal("review confirmation did not prepare create")
	}
	prepared := screen.createIntent.request
	committed := jobsTestResponse(prepared.JobID, jobdomain.JobStatusQueued, 1)
	committed.State.Spec.Authority = prepared.Authority
	committed.State.Spec.Goal = prepared.Goal
	fake.mu.Lock()
	fake.responses[prepared.JobID] = committed
	fake.mu.Unlock()

	_, showCmd := m.updateJobs(createCmd().(jobsOperationMsg))
	if showCmd == nil {
		t.Fatal("lost create acknowledgement did not trigger exact-ID GET")
	}
	_, _ = m.updateJobs(showCmd().(jobsOperationMsg))
	if screen.createIntent != nil || screen.detail.State.Spec.ID != prepared.JobID || screen.mode != jobsModeDetail {
		t.Fatalf("committed create was not recovered: intent=%v detail=%q mode=%d", screen.createIntent, screen.detail.State.Spec.ID, screen.mode)
	}
	if fake.callCount("create:"+prepared.JobID) != 1 || fake.callCount("show:"+prepared.JobID) != 1 {
		t.Fatalf("recovery calls = %#v; want one POST and one exact-ID GET", fake.callsSnapshot())
	}
}

func TestJobsCreateRecoveryNotFoundUnlocksDraftAndFreshID(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	prepareJobsReview(screen, false)
	fake.createErr = errors.New("lost create acknowledgement")
	fake.showErr = &gatewayclient.StatusError{Method: "GET", Path: "/v1/jobs/missing", StatusCode: 404}

	firstCmd := m.updateJobsKey(jobKey('y'))
	firstID := screen.createIntent.request.JobID
	_, showCmd := m.updateJobs(firstCmd().(jobsOperationMsg))
	_, _ = m.updateJobs(showCmd().(jobsOperationMsg))
	if screen.createIntent != nil || !strings.Contains(screen.notice, "does not exist") || !strings.Contains(screen.err, "lost create") {
		t.Fatalf("404 did not safely unlock the draft: intent=%v err=%q notice=%q", screen.createIntent, screen.err, screen.notice)
	}

	freshCmd := m.updateJobsKey(jobKey('y'))
	if freshCmd == nil || screen.createIntent == nil || screen.createIntent.request.JobID == firstID {
		t.Fatalf("fresh confirmation did not prepare a fresh ID: old=%q new=%v", firstID, screen.createIntent)
	}
}

func TestJobsAuthorityClampBlocksStartAndExactAuthorityStarts(t *testing.T) {
	t.Run("clamped", func(t *testing.T) {
		m, screen, fake := newJobsTestModel(t)
		prepareJobsReview(screen, true)
		screen.wizard.draft.readRoots = []string{"/notes"}
		fake.clampCreate = true

		cmd := m.updateJobsKey(jobKey('y'))
		_, followup := m.updateJobs(cmd().(jobsOperationMsg))
		if screen.pendingAction != "" || fake.callCount("run:"+screen.detail.State.Spec.ID) != 0 {
			t.Fatalf("clamped create was allowed to start: pending=%q calls=%#v", screen.pendingAction, fake.callsSnapshot())
		}
		if followup == nil || screen.detail.State.Status != jobdomain.JobStatusQueued ||
			!strings.Contains(screen.notice, "read_roots requested=[/notes] persisted=[none]") ||
			!strings.Contains(screen.notice, "create a new job") {
			t.Fatalf("clamp result/notice = status:%s notice:%q", screen.detail.State.Status, screen.notice)
		}
	})

	t.Run("exact", func(t *testing.T) {
		m, screen, fake := newJobsTestModel(t)
		prepareJobsReview(screen, true)
		screen.wizard.draft.readRoots = []string{"/notes"}

		cmd := m.updateJobsKey(jobKey('y'))
		_, runCmd := m.updateJobs(cmd().(jobsOperationMsg))
		jobID := screen.detail.State.Spec.ID
		if runCmd == nil || screen.pendingAction != jobclient.ActionRun || screen.pendingJobID != jobID {
			t.Fatalf("exact authority did not schedule start: pending=%s/%s", screen.pendingAction, screen.pendingJobID)
		}
		_ = runCmd()
		if fake.callCount("run:"+jobID) != 1 {
			t.Fatalf("exact create run calls = %#v", fake.callsSnapshot())
		}
	})
}

func TestJobsHistoryTailLabelsAndArtifactOmissionUseCanonicalTotals(t *testing.T) {
	_, screen, _ := newJobsTestModel(t)
	screen.mode = jobsModeDetail
	screen.detail = jobsTestResponse("job-a", jobdomain.JobStatusRunning, 3)
	screen.detail.History.Attempts = 100
	screen.detail.History.Artifacts = 1000
	screen.attempts = []jobdomain.Attempt{{RoleID: "research.primary", Status: jobdomain.AttemptStatusSucceeded, Cycle: 1, AttemptNo: 1}}
	for index := 0; index < 12; index++ {
		screen.artifacts = append(screen.artifacts, jobdomain.ArtifactRef{ID: "artifact-" + itoa(index), URI: "file:///tmp/a", MediaType: "text/plain"})
	}
	plain := ansi.Strip(strings.Join(screen.detailLines(newThemeStyles(tuiThemes["dark"]), 120), "\n"))
	if !strings.Contains(plain, "not present in recent tail") || !strings.Contains(plain, "showing latest 12 of 1000; 988 older refs omitted") {
		t.Fatalf("bounded-tail disclosure is inaccurate:\n%s", plain)
	}
	screen.artifacts = nil
	pending := ansi.Strip(strings.Join(screen.detailLines(newThemeStyles(tuiThemes["dark"]), 120), "\n"))
	if strings.Contains(pending, "LATEST ARTIFACT REFS\nnone") || !strings.Contains(pending, "canonical history contains 1000 artifact") {
		t.Fatalf("unloaded artifact tail was presented as empty:\n%s", pending)
	}
}

func TestJobsListFailureDoesNotClaimThereAreNoJobs(t *testing.T) {
	_, screen, _ := newJobsTestModel(t)
	screen.loading = false
	screen.err = "gateway offline"
	plain := ansi.Strip(strings.Join(screen.listLines(newThemeStyles(tuiThemes["dark"]), 80, 12), "\n"))
	if strings.Contains(plain, "No durable jobs") || !strings.Contains(plain, "No successful job-list snapshot") {
		t.Fatalf("failed first list was rendered as a successful empty list:\n%s", plain)
	}
}

func TestJobsOperationSwitchClearsPriorJobHistoryAndUncertainMutationLocksControls(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	screen.mode = jobsModeList
	screen.detail = jobsTestResponse("job-a", jobdomain.JobStatusRunning, 4)
	screen.attempts = []jobdomain.Attempt{{RoleID: "research.primary"}}
	screen.artifacts = []jobdomain.ArtifactRef{{ID: "from-a"}}
	screen.attemptsErr, screen.artifactsErr = "old attempts", "old artifacts"
	screen.pendingAction, screen.pendingJobID = jobclient.ActionRun, "job-b"

	response := jobsTestResponse("job-b", jobdomain.JobStatusRunning, 1)
	_, _ = m.updateJobs(jobsOperationMsg{
		instance: screen.instance, generation: screen.generation,
		result: jobclient.JobResultMsg{Action: jobclient.ActionRun, JobID: "job-b", Response: response},
	})
	if len(screen.attempts) != 0 || len(screen.artifacts) != 0 || screen.attemptsErr != "" || screen.artifactsErr != "" {
		t.Fatalf("job B temporarily inherited job A history: attempts=%#v artifacts=%#v errors=%q/%q", screen.attempts, screen.artifacts, screen.attemptsErr, screen.artifactsErr)
	}

	fake.err = errors.New("pause acknowledgement timeout")
	pauseCmd := m.updateJobsKey(jobKey('p'))
	_, reconcileCmd := m.updateJobs(pauseCmd().(jobsOperationMsg))
	if reconcileCmd == nil || screen.reconcilingAction != jobclient.ActionPause {
		t.Fatalf("ambiguous mutation did not enter reconciliation: %q", screen.reconcilingAction)
	}
	plain := ansi.Strip(screen.render(newThemeStyles(tuiThemes["dark"]), 100, 20))
	if !strings.Contains(plain, "RECONCILING") {
		t.Fatalf("persistent reconciliation cue is missing:\n%s", plain)
	}
	if cmd := m.updateJobsKey(jobKey('p')); cmd != nil || fake.callCount("pause:job-b") != 1 {
		t.Fatalf("control repeated during uncertain acknowledgement: cmd=%v calls=%#v", cmd, fake.callsSnapshot())
	}
}

func TestJobsQueuedPauseAndPersistedStopAreGated(t *testing.T) {
	m, screen, fake := newJobsTestModel(t)
	screen.list = []gatewayapi.JobSummaryResponse{{ID: "job-q", Status: jobdomain.JobStatusQueued}}
	screen.selectedID = "job-q"
	if cmd := m.updateJobsKey(jobKey('p')); cmd != nil || fake.callCount("pause:job-q") != 0 || !strings.Contains(screen.notice, "already dormant") {
		t.Fatalf("queued pause gate = cmd:%v notice:%q calls:%#v", cmd, screen.notice, fake.callsSnapshot())
	}

	screen.mode = jobsModeDetail
	screen.detail = jobsTestResponse("job-d", jobdomain.JobStatusRunning, 2)
	screen.detail.State.CancelRequested = true
	screen.detail.State.PendingStop = jobdomain.TerminalReasonOperatorCancellation
	if cmd := m.updateJobsKey(jobKey('x')); cmd != nil || screen.confirmCancelJobID != "" || !strings.Contains(screen.notice, "already draining") {
		t.Fatalf("persisted drain gate = cmd:%v notice:%q", cmd, screen.notice)
	}
	plain := ansi.Strip(strings.Join(screen.detailLines(newThemeStyles(tuiThemes["dark"]), 100), "\n"))
	if !strings.Contains(plain, "cancel_requested=true") || !strings.Contains(plain, "pending_stop=operator_cancellation") {
		t.Fatalf("persisted stop state is not visible:\n%s", plain)
	}
}

func TestJobsTopologyAndTextWizardFitSmallTerminal(t *testing.T) {
	if got := jobWorkflowTopology(jobdomain.PresetCoding, 4); got != "analyze[4] → implement[1] → verify[4] → reduce[1] → supervise[1]" {
		t.Fatalf("coding topology = %q", got)
	}
	_, screen, _ := newJobsTestModel(t)
	screen.mode = jobsModeWizard
	screen.wizard = newJobWizard(screen.wizardDefaults)
	screen.wizard.draft.goal = strings.Repeat("goal ", 40)
	screen.wizard.prepareEditor()
	rendered := screen.render(newThemeStyles(tuiThemes["dark"]), 60, 16)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 16 {
		t.Fatalf("text wizard height = %d, want 16\n%s", len(lines), rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > 60 {
			t.Fatalf("text wizard line %d width=%d: %q", index, width, ansi.Strip(line))
		}
	}
}

func prepareJobsReview(screen *jobsScreen, start bool) {
	screen.mode = jobsModeWizard
	screen.wizard = newJobWizard(screen.wizardDefaults)
	screen.wizard.step = jobStepReview
	screen.wizard.draft.goal = "produce a bounded research result"
	screen.wizard.draft.startAfterCheck = start
}

func newJobsTestModel(t *testing.T) (*Model, *jobsScreen, *jobsTUIGateway) {
	t.Helper()
	m := newTestModel(t)
	fake := &jobsTUIGateway{responses: map[string]gatewayapi.JobResponse{}}
	owner := gatewayapi.SessionOwner{ClientType: "tui", TUIChatID: "chat-test", Profile: "profile-test", Model: "model-test"}
	defaults := newJobWizardDefaults(
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", "high",
		modelinfo.ProviderDeepSeek, "deepseek-v4-flash", modelinfo.Provider(modelinfo.ProviderDeepSeek).BaseURL,
	)
	screen := newJobsScreen(jobclient.WithGateway(fake), owner, defaults, 42)
	screen.tick = func(_ time.Duration, makeMessage func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return makeMessage(time.Now()) }
	}
	m.jobs = screen
	m.width, m.height = 100, 30
	t.Cleanup(screen.cancel)
	return &m, screen, fake
}

func jobsTestResponse(jobID string, status jobdomain.JobStatus, revision uint64) gatewayapi.JobResponse {
	deadline := time.Now().UTC().Add(6 * time.Hour)
	return gatewayapi.JobResponse{
		State: jobdomain.JobState{
			Spec: jobdomain.JobSpec{
				ID: jobID, Goal: "test goal", Preset: jobdomain.PresetResearch, Workers: 1, Deadline: deadline,
				Budget: jobdomain.Budget{MaxCycles: 8, MaxAttempts: 128, MaxModelCalls: 128, MaxTokens: 1_000_000},
				Route:  jobdomain.ExecutionRoute{ProviderID: modelinfo.ProviderDeepSeek, ModelID: "deepseek-v4-flash", Thinking: "enabled", ReasoningEffort: "high"},
				Workflow: jobdomain.WorkflowControl{
					Version: 1, StageOrder: []string{"investigate", "reduce", "supervise"},
					WorkerRoleIDs: []string{"research.primary"}, ReducerRoleID: "control.reducer", SupervisorRoleID: "control.supervisor",
				},
				Authority: jobdomain.Authority{Mode: jobdomain.AuthorityModeAllowList, Providers: []string{modelinfo.ProviderDeepSeek}},
				Roles:     []jobdomain.RoleSpec{{ID: "research.primary"}, {ID: "control.reducer"}, {ID: "control.supervisor"}},
				Stages: []jobdomain.StageSpec{
					{ID: "investigate", RoleIDs: []string{"research.primary"}, MaxWorkers: 1, Barrier: jobdomain.BarrierAll},
					{ID: "reduce", RoleIDs: []string{"control.reducer"}, MaxWorkers: 1, Barrier: jobdomain.BarrierAll},
					{ID: "supervise", RoleIDs: []string{"control.supervisor"}, MaxWorkers: 1, Barrier: jobdomain.BarrierAll},
				},
			},
			Status: status, Revision: revision, Cycle: 1,
		},
	}
}

func jobKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

type jobsTUIGateway struct {
	mu          sync.Mutex
	responses   map[string]gatewayapi.JobResponse
	calls       []string
	owners      []gatewayapi.SessionOwner
	err         error
	createErr   error
	showErr     error
	clampCreate bool
}

func (f *jobsTUIGateway) record(ctx context.Context, call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if owner, ok := gatewayclient.SessionOwnerFromContext(ctx); ok {
		f.owners = append(f.owners, owner)
	}
}

func (f *jobsTUIGateway) response(jobID string) gatewayapi.JobResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	if response, ok := f.responses[jobID]; ok {
		return response
	}
	return jobsTestResponse(jobID, jobdomain.JobStatusRunning, 1)
}

func (f *jobsTUIGateway) CreateJob(ctx context.Context, request gatewayapi.CreateJobRequest) (gatewayapi.JobResponse, error) {
	f.record(ctx, "create:"+request.JobID)
	if f.createErr != nil {
		return gatewayapi.JobResponse{}, f.createErr
	}
	if f.err != nil {
		return gatewayapi.JobResponse{}, f.err
	}
	response := jobsTestResponse(request.JobID, jobdomain.JobStatusQueued, 1)
	response.State.Spec.Authority = request.Authority
	response.State.Spec.Goal = request.Goal
	if f.clampCreate {
		response.State.Spec.Authority.Tools = nil
		response.State.Spec.Authority.ReadRoots = nil
	}
	f.mu.Lock()
	f.responses[request.JobID] = response
	f.mu.Unlock()
	return response, nil
}

func (f *jobsTUIGateway) ListJobs(ctx context.Context) ([]gatewayapi.JobSummaryResponse, error) {
	f.record(ctx, "list")
	return nil, f.err
}

func (f *jobsTUIGateway) GetJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	f.record(ctx, "show:"+jobID)
	if f.showErr != nil {
		return gatewayapi.JobResponse{}, f.showErr
	}
	return f.response(jobID), f.err
}

func (f *jobsTUIGateway) RunJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	f.record(ctx, "run:"+jobID)
	response := f.response(jobID)
	response.State.Status, response.State.Revision = jobdomain.JobStatusRunning, response.State.Revision+1
	return response, f.err
}

func (f *jobsTUIGateway) PauseJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	f.record(ctx, "pause:"+jobID)
	response := f.response(jobID)
	response.State.Status, response.State.Revision = jobdomain.JobStatusPaused, response.State.Revision+1
	return response, f.err
}

func (f *jobsTUIGateway) ResumeJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	f.record(ctx, "resume:"+jobID)
	response := f.response(jobID)
	response.State.Status, response.State.Revision = jobdomain.JobStatusRunning, response.State.Revision+1
	return response, f.err
}

func (f *jobsTUIGateway) CancelJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	f.record(ctx, "cancel:"+jobID)
	response := f.response(jobID)
	response.State.Status, response.State.Revision = jobdomain.JobStatusCancelled, response.State.Revision+1
	response.State.TerminalReason = jobdomain.TerminalReasonOperatorCancellation
	return response, f.err
}

func (f *jobsTUIGateway) ListJobAttempts(ctx context.Context, jobID string, offset, limit int) (gatewayapi.JobAttemptPage, error) {
	f.record(ctx, "attempts:"+jobID+":"+itoa(offset)+":"+itoa(limit))
	return gatewayapi.JobAttemptPage{JobID: jobID, Offset: offset, Limit: limit}, f.err
}

func (f *jobsTUIGateway) ListJobArtifacts(ctx context.Context, jobID string, offset, limit int) (gatewayapi.JobArtifactPage, error) {
	f.record(ctx, "artifacts:"+jobID+":"+itoa(offset)+":"+itoa(limit))
	return gatewayapi.JobArtifactPage{JobID: jobID, Offset: offset, Limit: limit}, f.err
}

func (f *jobsTUIGateway) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *jobsTUIGateway) ownersSnapshot() []gatewayapi.SessionOwner {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gatewayapi.SessionOwner(nil), f.owners...)
}

func (f *jobsTUIGateway) callCount(call string) int {
	count := 0
	for _, got := range f.callsSnapshot() {
		if got == call {
			count++
		}
	}
	return count
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}

var _ jobclient.Gateway = (*jobsTUIGateway)(nil)
