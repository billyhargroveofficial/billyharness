package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	jobdomain "github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/tui/jobclient"
)

const (
	jobsPollInterval = 2 * time.Second
	jobsReadTimeout  = 10 * time.Second
	jobsWriteTimeout = 30 * time.Second
)

type jobsMode uint8

const (
	jobsModeList jobsMode = iota + 1
	jobsModeDetail
	jobsModeWizard
)

// jobsScreen is a modal control plane for durable gateway jobs. It owns no
// scheduler or provider objects: all state and mutations cross the typed
// gateway client boundary.
type jobsScreen struct {
	client   jobclient.Client
	owner    gatewayapi.SessionOwner
	now      func() time.Time
	tick     func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
	ctx      context.Context
	cancel   context.CancelFunc
	instance uint64

	mode             jobsMode
	generation       uint64
	nextPoll         uint64
	appliedPoll      uint64
	nextAttempts     uint64
	appliedAttempts  uint64
	nextArtifacts    uint64
	appliedArtifacts uint64
	pollTimer        uint64
	polling          bool
	lastPoll         time.Time
	listLoaded       bool

	list         []gatewayapi.JobSummaryResponse
	selectedID   string
	detail       gatewayapi.JobResponse
	attempts     []jobdomain.Attempt
	artifacts    []jobdomain.ArtifactRef
	detailScroll int

	loading            bool
	pendingAction      jobclient.Action
	pendingJobID       string
	reconcilingAction  jobclient.Action
	reconcilingJobID   string
	confirmCancelJobID string
	err                string
	attemptsErr        string
	artifactsErr       string
	notice             string

	wizardDefaults jobWizardDefaults
	wizard         *jobWizard
	createIntent   *jobCreateIntent
}

type jobCreateIntent struct {
	request gatewayapi.CreateJobRequest
	start   bool
	// ambiguous is set after a create acknowledgement fails. While it is set,
	// the wizard is intentionally locked to the same idempotent request/ID
	// until a GET recovers it, the exact POST is retried, or the panel closes.
	ambiguous bool
	createErr string
}

type jobsPollTickMsg struct {
	instance   uint64
	generation uint64
	timer      uint64
}

type jobsListLoadedMsg struct {
	instance   uint64
	generation uint64
	poll       uint64
	result     jobclient.ListResultMsg
	at         time.Time
}

type jobsDetailLoadedMsg struct {
	instance   uint64
	generation uint64
	poll       uint64
	result     jobclient.JobResultMsg
	at         time.Time
}

type jobsAttemptsLoadedMsg struct {
	instance   uint64
	generation uint64
	sequence   uint64
	revision   uint64
	jobID      string
	offset     int
	limit      int
	result     jobclient.AttemptsResultMsg
}

type jobsArtifactsLoadedMsg struct {
	instance   uint64
	generation uint64
	sequence   uint64
	revision   uint64
	jobID      string
	offset     int
	limit      int
	result     jobclient.ArtifactsResultMsg
}

type jobsOperationMsg struct {
	instance   uint64
	generation uint64
	result     jobclient.JobResultMsg
}

func newJobsScreen(client jobclient.Client, owner gatewayapi.SessionOwner, defaults jobWizardDefaults, instance uint64) *jobsScreen {
	now := time.Now
	ctx, cancel := context.WithCancel(context.Background())
	return &jobsScreen{
		client:         client,
		owner:          owner,
		now:            now,
		tick:           tea.Tick,
		ctx:            ctx,
		cancel:         cancel,
		instance:       instance,
		mode:           jobsModeList,
		generation:     1,
		loading:        true,
		wizardDefaults: defaults,
	}
}

func (m *Model) openJobs() tea.Cmd {
	if strings.TrimSpace(m.gatewayURL) == "" {
		m.status = "durable jobs require gateway mode"
		return nil
	}
	owner := gatewayapi.SessionOwner{
		ClientType: "tui",
		TUIChatID:  m.localChatID,
		Profile:    m.currentProfile(),
		Model:      m.currentModel(),
	}
	defaults := newJobWizardDefaults(
		m.currentProvider(),
		m.currentModel(),
		m.currentThinking().effortLabel(),
		m.cfg.Provider,
		m.cfg.Model,
		m.cfg.BaseURL,
	)
	m.jobsScreenSequence++
	m.jobs = newJobsScreen(jobclient.New(m.gatewayURL), owner, defaults, m.jobsScreenSequence)
	m.textarea.Blur()
	m.jobs.polling = true
	return m.jobs.listCmd()
}

func (m *Model) closeJobs() {
	if m.jobs != nil && m.jobs.cancel != nil {
		m.jobs.cancel()
	}
	m.jobs = nil
	m.textarea.Focus()
	if m.width > 0 {
		m.resize(m.followOutput)
	}
}

// updateJobs handles modal keys before the composer, while allowing unrelated
// chat stream messages to continue through the normal update loop behind the
// full-screen panel.
func (m *Model) updateJobs(msg tea.Msg) (bool, tea.Cmd) {
	if m.jobs == nil {
		return false, nil
	}
	s := m.jobs
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if s.wizard != nil {
			s.wizard.resizeEditor(msg.Width)
		}
		return true, nil
	case tea.KeyPressMsg:
		return true, m.updateJobsKey(msg)
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return true, nil
	case tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		if s.mode == jobsModeWizard && s.wizard != nil && s.wizard.isTextStep() {
			return true, s.wizard.updateEditor(msg)
		}
		return true, nil
	case clipboardCopiedMsg:
		if msg.err != "" {
			s.notice = "copy failed: " + jobSingleLine(msg.err)
		} else {
			s.notice = fmt.Sprintf("copied %d chars via %s", msg.chars, jobSingleLine(msg.method))
		}
		return true, nil
	case jobsPollTickMsg:
		if msg.instance != s.instance || msg.generation != s.generation || msg.timer != s.pollTimer ||
			s.polling || s.pendingAction != "" || s.mode == jobsModeWizard {
			return true, nil
		}
		s.polling = true
		if s.mode == jobsModeDetail && s.detail.State.Spec.ID != "" {
			return true, s.detailCmd(s.detail.State.Spec.ID)
		}
		return true, s.listCmd()
	case jobsListLoadedMsg:
		if msg.instance != s.instance || msg.generation != s.generation || msg.poll <= s.appliedPoll || s.mode != jobsModeList {
			return true, nil
		}
		s.polling = false
		s.loading = false
		s.appliedPoll = msg.poll
		s.lastPoll = msg.at
		if msg.result.Err != nil {
			s.err = jobSingleLine(msg.result.Err.Error())
			return true, s.pollTickCmd()
		}
		s.err = ""
		s.listLoaded = true
		s.list = append([]gatewayapi.JobSummaryResponse(nil), msg.result.Jobs...)
		s.reconcileSelection()
		s.finishReconciliation()
		return true, s.pollTickCmd()
	case jobsDetailLoadedMsg:
		if msg.instance != s.instance || msg.generation != s.generation || msg.poll <= s.appliedPoll || s.mode != jobsModeDetail {
			return true, nil
		}
		s.polling = false
		s.loading = false
		s.appliedPoll = msg.poll
		s.lastPoll = msg.at
		if msg.result.Err != nil {
			s.err = jobSingleLine(msg.result.Err.Error())
			return true, s.pollTickCmd()
		}
		s.err = ""
		if msg.result.Action != jobclient.ActionShow || msg.result.JobID != s.selectedID || msg.result.Response.State.Spec.ID != msg.result.JobID {
			s.err = "gateway returned detail for a different job"
			return true, s.pollTickCmd()
		}
		if s.detail.State.Spec.ID == msg.result.JobID && msg.result.Response.State.Revision < s.detail.State.Revision {
			return true, s.pollTickCmd()
		}
		s.detail = msg.result.Response
		s.selectedID = msg.result.Response.State.Spec.ID
		s.finishReconciliation()
		return true, tea.Batch(s.attemptsCmd(s.detail), s.artifactsCmd(s.detail), s.pollTickCmd())
	case jobsAttemptsLoadedMsg:
		if msg.instance != s.instance || msg.generation != s.generation || msg.sequence <= s.appliedAttempts ||
			s.mode != jobsModeDetail || msg.jobID != s.detail.State.Spec.ID || msg.revision < s.detail.State.Revision {
			return true, nil
		}
		if msg.result.JobID != msg.jobID || msg.result.Offset != msg.offset || msg.result.Limit != msg.limit {
			return true, nil
		}
		if msg.result.Err != nil {
			s.appliedAttempts = msg.sequence
			s.attemptsErr = jobSingleLine(msg.result.Err.Error())
			return true, nil
		}
		if msg.result.Page.JobID != msg.jobID || msg.result.Page.Offset != msg.offset || msg.result.Page.Limit != msg.limit {
			return true, nil
		}
		s.appliedAttempts = msg.sequence
		s.attemptsErr = ""
		s.attempts = append([]jobdomain.Attempt(nil), msg.result.Page.Attempts...)
		return true, nil
	case jobsArtifactsLoadedMsg:
		if msg.instance != s.instance || msg.generation != s.generation || msg.sequence <= s.appliedArtifacts ||
			s.mode != jobsModeDetail || msg.jobID != s.detail.State.Spec.ID || msg.revision < s.detail.State.Revision {
			return true, nil
		}
		if msg.result.JobID != msg.jobID || msg.result.Offset != msg.offset || msg.result.Limit != msg.limit {
			return true, nil
		}
		if msg.result.Err != nil {
			s.appliedArtifacts = msg.sequence
			s.artifactsErr = jobSingleLine(msg.result.Err.Error())
			return true, nil
		}
		if msg.result.Page.JobID != msg.jobID || msg.result.Page.Offset != msg.offset || msg.result.Page.Limit != msg.limit {
			return true, nil
		}
		s.appliedArtifacts = msg.sequence
		s.artifactsErr = ""
		s.artifacts = append([]jobdomain.ArtifactRef(nil), msg.result.Page.Artifacts...)
		return true, nil
	case jobsOperationMsg:
		if msg.instance != s.instance || msg.generation != s.generation {
			return true, nil
		}
		expectedAction, expectedJobID := s.pendingAction, s.pendingJobID
		if expectedAction == "" || msg.result.Action != expectedAction || msg.result.JobID != expectedJobID {
			return true, nil
		}
		s.pendingAction, s.pendingJobID = "", ""
		s.loading = false
		if msg.result.Err != nil {
			s.err = jobSingleLine(msg.result.Err.Error())
			if msg.result.Action == jobclient.ActionCreate && s.createIntent != nil {
				s.createIntent.ambiguous = true
				s.createIntent.createErr = s.err
				jobID := s.createIntent.request.JobID
				cmd := s.actionCmd(jobclient.ActionShow, jobID)
				// actionCmd clears feedback before dispatch; restore the diagnostic
				// so the automatic exact-ID recovery remains visible.
				s.err = jobSingleLine(msg.result.Err.Error())
				s.notice = "create acknowledgement failed; checking the exact prepared job ID before allowing a retry"
				return true, cmd
			}
			if msg.result.Action == jobclient.ActionShow && s.createIntent != nil && s.createIntent.ambiguous {
				if jobHTTPNotFound(msg.result.Err) {
					originalErr := s.createIntent.createErr
					s.createIntent = nil
					s.err = originalErr
					s.notice = "gateway confirmed the prepared job ID does not exist; the draft is editable and the next y will prepare a fresh ID"
					return true, nil
				}
				s.notice = "create state is still unknown; press y to retry the exact same prepared job ID, or q/Esc to close and reconcile on reopen"
				return true, nil
			}
			s.reconcilingAction = msg.result.Action
			s.reconcilingJobID = msg.result.JobID
			s.notice = fmt.Sprintf("%s acknowledgement is uncertain; canonical state is being reconciled before another control is allowed", msg.result.Action)
			return true, s.reconcileCmd()
		}
		if msg.result.Response.State.Spec.ID == "" || msg.result.Response.State.Spec.ID != msg.result.JobID {
			s.err = "gateway returned an operation result for a different job"
			return true, s.reconcileCmd()
		}
		if s.detail.State.Spec.ID == msg.result.JobID && msg.result.Response.State.Revision < s.detail.State.Revision {
			s.err = "gateway returned a stale operation revision"
			return true, s.reconcileCmd()
		}
		s.err = ""
		if s.detail.State.Spec.ID != "" && s.detail.State.Spec.ID != msg.result.JobID {
			s.attempts = nil
			s.artifacts = nil
			s.attemptsErr = ""
			s.artifactsErr = ""
		}
		s.detail = msg.result.Response
		s.selectedID = msg.result.Response.State.Spec.ID
		s.mode = jobsModeDetail
		s.confirmCancelJobID = ""
		if msg.result.Action == jobclient.ActionCreate ||
			(msg.result.Action == jobclient.ActionShow && s.createIntent != nil) {
			return true, m.finishCreatedJob(msg.result.Response)
		}
		s.notice = actionNotice(msg.result.Action, msg.result.Response.State)
		return true, tea.Batch(s.attemptsCmd(s.detail), s.artifactsCmd(s.detail), s.pollTickCmd())
	}
	return false, nil
}

func (m *Model) updateJobsKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.jobs
	if s == nil {
		return nil
	}
	if msg.String() == "ctrl+c" {
		_ = m.saveCurrentSession()
		_ = m.saveSettings()
		if s.cancel != nil {
			s.cancel()
		}
		return tea.Quit
	}
	if s.pendingAction != "" || s.reconcilingAction != "" {
		if msg.String() == "q" || msg.String() == "esc" {
			m.closeJobs()
			return nil
		}
		if s.pendingAction != "" {
			s.notice = fmt.Sprintf("%s is still pending; wait for reconciliation", s.pendingAction)
		} else {
			s.notice = fmt.Sprintf("%s acknowledgement is uncertain; canonical state is still reconciling", s.reconcilingAction)
		}
		return nil
	}
	if s.confirmCancelJobID != "" {
		switch msg.String() {
		case "y", "Y":
			jobID := s.confirmCancelJobID
			s.confirmCancelJobID = ""
			return s.actionCmd(jobclient.ActionCancel, jobID)
		case "n", "N", "enter", "esc", "backspace":
			s.confirmCancelJobID = ""
			s.notice = "cancellation dismissed"
		}
		return nil
	}
	if s.mode == jobsModeWizard {
		if s.createIntent != nil {
			switch msg.String() {
			case "y":
				s.loading = true
				s.pendingAction = jobclient.ActionCreate
				s.pendingJobID = s.createIntent.request.JobID
				s.err = ""
				s.notice = "retrying the exact prepared create request and job ID"
				return s.createCmd(s.createIntent.request)
			case "q", "esc":
				m.closeJobs()
				return nil
			default:
				s.notice = "create state is unknown; only y retries the exact prepared ID, while q/Esc closes and reconciles on reopen"
				return nil
			}
		}
		if s.wizard.updateReviewScroll(msg, m.width, s.wizardReviewAvailable(max(1, m.height-2))) {
			return nil
		}
		cmd, outcome := s.wizard.updateKey(msg)
		switch outcome {
		case wizardOutcomeCancel:
			s.toList()
			return s.listCmd()
		case wizardOutcomeCreate:
			request, start, err := s.wizard.request()
			if err != nil {
				s.wizard.err = jobSingleLine(err.Error())
				return nil
			}
			// Start-after-check deliberately creates QUEUED first. The persisted
			// authority is compared with the requested envelope before Run.
			request.AutoStart = false
			request = jobclient.PrepareCreateRequest(request)
			s.createIntent = &jobCreateIntent{request: request, start: start}
			s.loading = true
			s.pendingAction = jobclient.ActionCreate
			s.pendingJobID = request.JobID
			s.err = ""
			return s.createCmd(request)
		}
		return cmd
	}

	switch msg.String() {
	case "q":
		m.closeJobs()
		return nil
	case "esc", "backspace":
		if s.mode == jobsModeDetail {
			s.toList()
			return s.listCmd()
		}
		m.closeJobs()
		return nil
	case "n":
		s.mode = jobsModeWizard
		s.bumpGeneration()
		s.wizard = newJobWizard(s.wizardDefaults)
		s.wizard.resizeEditor(m.width)
		s.err, s.notice = "", ""
		return nil
	case "f":
		if !s.polling {
			s.pollTimer++
			s.polling = true
			if s.mode == jobsModeDetail {
				return s.detailCmd(s.selectedJobID())
			}
			return s.listCmd()
		}
		return nil
	}

	if s.mode == jobsModeList {
		return s.updateListKey(msg)
	}
	return s.updateDetailKey(msg, m.width, m.height)
}

func (s *jobsScreen) updateListKey(msg tea.KeyPressMsg) tea.Cmd {
	if len(s.list) == 0 {
		return nil
	}
	index := s.selectedIndex()
	switch msg.String() {
	case "up", "k":
		index = max(0, index-1)
	case "down", "j":
		index = min(len(s.list)-1, index+1)
	case "home", "g":
		index = 0
	case "end", "G":
		index = len(s.list) - 1
	case "enter":
		return s.openDetail(s.list[index].ID)
	case "s":
		if s.list[index].Status != jobdomain.JobStatusQueued {
			s.notice = "start is available only for queued jobs"
			return nil
		}
		return s.actionCmd(jobclient.ActionRun, s.list[index].ID)
	case "p":
		if !jobCanPause(s.list[index].Status) {
			if s.list[index].Status == jobdomain.JobStatusQueued {
				s.notice = "queued jobs are already dormant; use s to start"
				return nil
			}
			s.notice = "pause is unavailable for this state"
			return nil
		}
		return s.actionCmd(jobclient.ActionPause, s.list[index].ID)
	case "r":
		if s.list[index].Status == jobdomain.JobStatusWaiting {
			s.notice = "loading wait details before resume; scheduled waits cannot be resumed early"
			return s.openDetail(s.list[index].ID)
		}
		if s.list[index].Status != jobdomain.JobStatusPaused {
			s.notice = "resume is available only for paused/waiting jobs"
			return nil
		}
		return s.actionCmd(jobclient.ActionResume, s.list[index].ID)
	case "x":
		if s.list[index].Status.IsTerminal() {
			s.notice = "terminal jobs cannot be cancelled"
			return nil
		}
		s.selectedID = s.list[index].ID
		s.confirmCancelJobID = s.list[index].ID
		return nil
	default:
		return nil
	}
	s.selectedID = s.list[index].ID
	return nil
}

func (s *jobsScreen) updateDetailKey(msg tea.KeyPressMsg, width, height int) tea.Cmd {
	state := s.detail.State
	switch msg.String() {
	case "up", "k":
		s.detailScroll = max(0, s.detailScroll-1)
	case "down", "j":
		s.detailScroll = min(s.maxDetailScroll(width, height), s.detailScroll+1)
	case "pgup":
		s.detailScroll = max(0, s.detailScroll-max(1, height-6))
	case "pgdown":
		s.detailScroll = min(s.maxDetailScroll(width, height), s.detailScroll+max(1, height-6))
	case "home", "g":
		s.detailScroll = 0
	case "end", "G":
		s.detailScroll = s.maxDetailScroll(width, height)
	case "s":
		if jobStopInProgress(state) {
			s.notice = jobStopNotice(state)
			return nil
		}
		if state.Status != jobdomain.JobStatusQueued {
			s.notice = "start is available only for queued jobs"
			return nil
		}
		return s.actionCmd(jobclient.ActionRun, state.Spec.ID)
	case "p":
		if jobStopInProgress(state) {
			s.notice = jobStopNotice(state)
			return nil
		}
		if !jobCanPause(state.Status) {
			if state.Status == jobdomain.JobStatusQueued {
				s.notice = "queued jobs are already dormant; use s to start"
				return nil
			}
			s.notice = "pause is unavailable for this state"
			return nil
		}
		return s.actionCmd(jobclient.ActionPause, state.Spec.ID)
	case "r":
		if jobStopInProgress(state) {
			s.notice = jobStopNotice(state)
			return nil
		}
		if !jobCanResumeState(state) {
			if state.Status == jobdomain.JobStatusWaiting && !state.NextWakeAt.IsZero() {
				s.notice = "scheduled wait continues until " + jobTime(state.NextWakeAt)
				return nil
			}
			s.notice = "resume is available only for paused/waiting jobs"
			return nil
		}
		return s.actionCmd(jobclient.ActionResume, state.Spec.ID)
	case "x":
		if jobStopInProgress(state) {
			s.notice = jobStopNotice(state)
			return nil
		}
		if state.Status.IsTerminal() {
			s.notice = "terminal jobs cannot be cancelled"
			return nil
		}
		s.confirmCancelJobID = state.Spec.ID
	case "c":
		if strings.TrimSpace(state.FinalResult) == "" {
			s.notice = "no final result is available to copy"
			return nil
		}
		s.notice = fmt.Sprintf("copying final result (%d chars)", len([]rune(state.FinalResult)))
		return copySelectionCmd(state.FinalResult)
	}
	return nil
}

func (s *jobsScreen) openDetail(jobID string) tea.Cmd {
	s.mode = jobsModeDetail
	s.bumpGeneration()
	s.selectedID = strings.TrimSpace(jobID)
	s.detail = gatewayapi.JobResponse{}
	s.attempts = nil
	s.artifacts = nil
	s.detailScroll = 0
	s.loading = true
	s.err, s.notice = "", ""
	s.polling = true
	return s.detailCmd(jobID)
}

func (s *jobsScreen) toList() {
	s.mode = jobsModeList
	s.bumpGeneration()
	s.wizard = nil
	s.detailScroll = 0
	s.loading = true
	s.err, s.notice = "", ""
	s.polling = true
}

func (s *jobsScreen) bumpGeneration() {
	s.generation++
	s.nextPoll = 0
	s.appliedPoll = 0
	s.nextAttempts = 0
	s.appliedAttempts = 0
	s.nextArtifacts = 0
	s.appliedArtifacts = 0
	s.pollTimer++
	s.polling = false
}

func (s *jobsScreen) nextPollID() uint64 {
	s.nextPoll++
	return s.nextPoll
}

func (s *jobsScreen) context(timeout time.Duration) (context.Context, context.CancelFunc) {
	base := gatewayclient.WithSessionOwner(s.ctx, s.owner)
	return context.WithTimeout(base, timeout)
}

func (s *jobsScreen) listCmd() tea.Cmd {
	generation, poll, client := s.generation, s.nextPollID(), s.client
	return func() tea.Msg {
		ctx, cancel := s.context(jobsReadTimeout)
		defer cancel()
		message, ok := client.ListCmd(ctx)().(jobclient.ListResultMsg)
		if !ok {
			message.Err = fmt.Errorf("job client returned an invalid list response")
		}
		return jobsListLoadedMsg{instance: s.instance, generation: generation, poll: poll, result: message, at: s.now()}
	}
}

func (s *jobsScreen) detailCmd(jobID string) tea.Cmd {
	generation, poll, client := s.generation, s.nextPollID(), s.client
	jobID = strings.TrimSpace(jobID)
	return func() tea.Msg {
		ctx, cancel := s.context(jobsReadTimeout)
		defer cancel()
		message, ok := client.ShowCmd(ctx, jobID)().(jobclient.JobResultMsg)
		if !ok {
			message = jobclient.JobResultMsg{Action: jobclient.ActionShow, JobID: jobID, Err: fmt.Errorf("job client returned an invalid detail response")}
		}
		return jobsDetailLoadedMsg{instance: s.instance, generation: generation, poll: poll, result: message, at: s.now()}
	}
}

func (s *jobsScreen) attemptsCmd(response gatewayapi.JobResponse) tea.Cmd {
	total := response.History.Attempts
	offset := 0
	if total > jobclient.MaxAttemptPageLimit {
		offset = int(total - jobclient.MaxAttemptPageLimit)
	}
	s.nextAttempts++
	generation, sequence, revision, jobID, client := s.generation, s.nextAttempts, response.State.Revision, response.State.Spec.ID, s.client
	return func() tea.Msg {
		ctx, cancel := s.context(jobsReadTimeout)
		defer cancel()
		message, ok := client.ListAttemptsCmd(ctx, jobID, offset, jobclient.MaxAttemptPageLimit)().(jobclient.AttemptsResultMsg)
		if !ok {
			message = jobclient.AttemptsResultMsg{JobID: jobID, Offset: offset, Limit: jobclient.MaxAttemptPageLimit, Err: fmt.Errorf("job client returned an invalid attempts response")}
		}
		return jobsAttemptsLoadedMsg{instance: s.instance, generation: generation, sequence: sequence, revision: revision, jobID: jobID, offset: offset, limit: jobclient.MaxAttemptPageLimit, result: message}
	}
}

func (s *jobsScreen) artifactsCmd(response gatewayapi.JobResponse) tea.Cmd {
	total := response.History.Artifacts
	offset := 0
	if total > jobclient.MaxArtifactPageLimit {
		offset = int(total - jobclient.MaxArtifactPageLimit)
	}
	s.nextArtifacts++
	generation, sequence, revision, jobID, client := s.generation, s.nextArtifacts, response.State.Revision, response.State.Spec.ID, s.client
	return func() tea.Msg {
		ctx, cancel := s.context(jobsReadTimeout)
		defer cancel()
		message, ok := client.ListArtifactsCmd(ctx, jobID, offset, jobclient.MaxArtifactPageLimit)().(jobclient.ArtifactsResultMsg)
		if !ok {
			message = jobclient.ArtifactsResultMsg{JobID: jobID, Offset: offset, Limit: jobclient.MaxArtifactPageLimit, Err: fmt.Errorf("job client returned an invalid artifacts response")}
		}
		return jobsArtifactsLoadedMsg{instance: s.instance, generation: generation, sequence: sequence, revision: revision, jobID: jobID, offset: offset, limit: jobclient.MaxArtifactPageLimit, result: message}
	}
}

func (s *jobsScreen) createCmd(request gatewayapi.CreateJobRequest) tea.Cmd {
	s.bumpGeneration()
	s.polling = false
	generation, client := s.generation, s.client
	return func() tea.Msg {
		ctx, cancel := s.context(jobsWriteTimeout)
		defer cancel()
		message, ok := client.CreateCmd(ctx, request)().(jobclient.JobResultMsg)
		if !ok {
			message = jobclient.JobResultMsg{Action: jobclient.ActionCreate, JobID: request.JobID, Err: fmt.Errorf("job client returned an invalid create response")}
		}
		return jobsOperationMsg{instance: s.instance, generation: generation, result: message}
	}
}

func (s *jobsScreen) actionCmd(action jobclient.Action, jobID string) tea.Cmd {
	if s.pendingAction != "" || s.reconcilingAction != "" {
		s.notice = "another job action is still pending"
		return nil
	}
	s.pendingAction = action
	s.pendingJobID = strings.TrimSpace(jobID)
	s.loading = true
	s.err, s.notice = "", ""
	s.bumpGeneration()
	generation, client := s.generation, s.client
	return func() tea.Msg {
		timeout := jobsWriteTimeout
		if action == jobclient.ActionShow {
			timeout = jobsReadTimeout
		}
		ctx, cancel := s.context(timeout)
		defer cancel()
		var raw tea.Msg
		switch action {
		case jobclient.ActionShow:
			raw = client.ShowCmd(ctx, jobID)()
		case jobclient.ActionRun:
			raw = client.RunCmd(ctx, jobID)()
		case jobclient.ActionPause:
			raw = client.PauseCmd(ctx, jobID)()
		case jobclient.ActionResume:
			raw = client.ResumeCmd(ctx, jobID)()
		case jobclient.ActionCancel:
			raw = client.CancelCmd(ctx, jobID)()
		default:
			return jobsOperationMsg{instance: s.instance, generation: generation, result: jobclient.JobResultMsg{Action: action, JobID: jobID, Err: fmt.Errorf("unsupported job action %q", action)}}
		}
		message, ok := raw.(jobclient.JobResultMsg)
		if !ok {
			message = jobclient.JobResultMsg{Action: action, JobID: jobID, Err: fmt.Errorf("job client returned an invalid action response")}
		}
		return jobsOperationMsg{instance: s.instance, generation: generation, result: message}
	}
}

func (s *jobsScreen) pollTickCmd() tea.Cmd {
	s.pollTimer++
	instance, generation, timer := s.instance, s.generation, s.pollTimer
	return s.tick(jobsPollInterval, func(time.Time) tea.Msg {
		return jobsPollTickMsg{instance: instance, generation: generation, timer: timer}
	})
}

func (s *jobsScreen) reconcileCmd() tea.Cmd {
	if s.mode == jobsModeWizard {
		return nil
	}
	s.polling = true
	if s.mode == jobsModeDetail && s.detail.State.Spec.ID != "" {
		return s.detailCmd(s.detail.State.Spec.ID)
	}
	return s.listCmd()
}

func (m *Model) finishCreatedJob(response gatewayapi.JobResponse) tea.Cmd {
	s := m.jobs
	intent := s.createIntent
	s.createIntent = nil
	if intent == nil {
		s.err = "created job is missing its local authority intent"
		return tea.Batch(s.attemptsCmd(response), s.artifactsCmd(response), s.pollTickCmd())
	}
	if !equalJobAuthority(intent.request.Authority, response.State.Spec.Authority) {
		diff := jobAuthorityDiff(intent.request.Authority, response.State.Spec.Authority)
		status := strings.ToUpper(string(response.State.Status))
		if status == "" {
			status = "UNKNOWN"
		}
		if intent.start {
			s.notice = "persisted job is " + status + "; safe start was blocked because gateway narrowed " + diff + ". This job's authority is immutable: cancel it if unwanted, reconfigure/restart the gateway authority, then create a new job"
		} else {
			s.notice = "persisted job is " + status + " in CREATE QUEUED mode, and gateway narrowed " + diff + ". This job's authority is immutable: cancel it if unwanted, reconfigure/restart the gateway authority, then create a new job"
		}
		return tea.Batch(s.attemptsCmd(response), s.artifactsCmd(response), s.pollTickCmd())
	}
	if !intent.start {
		s.notice = "job created queued after authority verification"
		return tea.Batch(s.attemptsCmd(response), s.artifactsCmd(response), s.pollTickCmd())
	}
	s.notice = "authority verified; starting job"
	return s.actionCmd(jobclient.ActionRun, response.State.Spec.ID)
}

func equalJobAuthority(left, right jobdomain.Authority) bool {
	if left.Mode != right.Mode {
		return false
	}
	equalSet := func(a, b []string) bool {
		a, b = append([]string(nil), a...), append([]string(nil), b...)
		slices.Sort(a)
		slices.Sort(b)
		return slices.Equal(a, b)
	}
	return equalSet(left.Tools, right.Tools) && equalRootCoverage(left.ReadRoots, right.ReadRoots) &&
		equalRootCoverage(left.WriteRoots, right.WriteRoots) && equalSet(left.NetworkHosts, right.NetworkHosts) &&
		equalSet(left.Providers, right.Providers)
}

func jobAuthorityDiff(requested, persisted jobdomain.Authority) string {
	equalSet := func(left, right []string) bool {
		left, right = append([]string(nil), left...), append([]string(nil), right...)
		slices.Sort(left)
		slices.Sort(right)
		return slices.Equal(left, right)
	}
	formatSet := func(values []string) string {
		values = append([]string(nil), values...)
		slices.Sort(values)
		return "[" + jobListValue(values) + "]"
	}
	var changed []string
	if requested.Mode != persisted.Mode {
		changed = append(changed, fmt.Sprintf("mode requested=%s persisted=%s", requested.Mode, persisted.Mode))
	}
	for _, dimension := range []struct {
		name       string
		requested  []string
		persisted  []string
		equivalent func([]string, []string) bool
	}{
		{name: "tools", requested: requested.Tools, persisted: persisted.Tools, equivalent: equalSet},
		{name: "read_roots", requested: requested.ReadRoots, persisted: persisted.ReadRoots, equivalent: equalRootCoverage},
		{name: "write_roots", requested: requested.WriteRoots, persisted: persisted.WriteRoots, equivalent: equalRootCoverage},
		{name: "network_hosts", requested: requested.NetworkHosts, persisted: persisted.NetworkHosts, equivalent: equalSet},
		{name: "providers", requested: requested.Providers, persisted: persisted.Providers, equivalent: equalSet},
	} {
		if !dimension.equivalent(dimension.requested, dimension.persisted) {
			changed = append(changed, fmt.Sprintf("%s requested=%s persisted=%s", dimension.name, formatSet(dimension.requested), formatSet(dimension.persisted)))
		}
	}
	if len(changed) == 0 {
		return "authority (an unreported dimension changed)"
	}
	return strings.Join(changed, "; ")
}

func equalRootCoverage(left, right []string) bool {
	covers := func(parents, children []string) bool {
		for _, child := range children {
			covered := false
			for _, parent := range parents {
				relative, err := filepath.Rel(parent, child)
				if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					covered = true
					break
				}
			}
			if !covered {
				return false
			}
		}
		return true
	}
	return covers(left, right) && covers(right, left)
}

func (s *jobsScreen) reconcileSelection() {
	if len(s.list) == 0 {
		s.selectedID = ""
		return
	}
	for _, item := range s.list {
		if item.ID == s.selectedID {
			return
		}
	}
	s.selectedID = s.list[0].ID
}

func (s *jobsScreen) selectedIndex() int {
	for index, item := range s.list {
		if item.ID == s.selectedID {
			return index
		}
	}
	return 0
}

func (s *jobsScreen) selectedJobID() string {
	if s.mode == jobsModeDetail && s.detail.State.Spec.ID != "" {
		return s.detail.State.Spec.ID
	}
	if s.selectedID != "" {
		return s.selectedID
	}
	if len(s.list) != 0 {
		return s.list[0].ID
	}
	return ""
}

func jobCanPause(status jobdomain.JobStatus) bool {
	return status == jobdomain.JobStatusRunning || status == jobdomain.JobStatusWaiting
}

func jobCanResumeState(state jobdomain.JobState) bool {
	return !jobStopInProgress(state) && (state.Status == jobdomain.JobStatusPaused ||
		(state.Status == jobdomain.JobStatusWaiting && state.NextWakeAt.IsZero()))
}

func jobStopInProgress(state jobdomain.JobState) bool {
	return state.CancelRequested || state.PendingStop != ""
}

func jobStopNotice(state jobdomain.JobState) string {
	reason := string(state.PendingStop)
	if reason == "" && state.CancelRequested {
		reason = string(jobdomain.TerminalReasonOperatorCancellation)
	}
	if reason == "" {
		reason = "requested stop"
	}
	return "job is already draining toward " + reason + "; conflicting controls are disabled"
}

func jobHTTPNotFound(err error) bool {
	var status *gatewayclient.StatusError
	return errors.As(err, &status) && status.StatusCode == http.StatusNotFound
}

func (s *jobsScreen) finishReconciliation() {
	if s.reconcilingAction == "" {
		return
	}
	action := s.reconcilingAction
	s.reconcilingAction, s.reconcilingJobID = "", ""
	s.notice = fmt.Sprintf("canonical state refreshed after uncertain %s acknowledgement; inspect it before issuing another control", action)
}

func actionNotice(action jobclient.Action, state jobdomain.JobState) string {
	switch action {
	case jobclient.ActionRun:
		return "job started"
	case jobclient.ActionPause:
		return "job paused at a safe boundary"
	case jobclient.ActionResume:
		if state.Status == jobdomain.JobStatusWaiting && !state.NextWakeAt.IsZero() {
			return "job resumed into its scheduled wait until " + jobTime(state.NextWakeAt)
		}
		return "job resumed"
	case jobclient.ActionCancel:
		return "job cancellation requested"
	default:
		return "job updated"
	}
}
