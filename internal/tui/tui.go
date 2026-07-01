package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/filesearch"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/promptcommands"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	tuirender "github.com/billyhargroveofficial/billyharness/internal/tui/render"
	tuiruntime "github.com/billyhargroveofficial/billyharness/internal/tui/runtimeclient"
	tuiselection "github.com/billyhargroveofficial/billyharness/internal/tui/selection"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

type Options struct {
	GatewayURL    string
	GatewayNotice string
	Model         string
	Dangerous     bool
	MaxRounds     int
	AccessMode    string
	Plain         bool
	Version       string
}

type thinkingMode struct {
	label  string
	kind   string
	effort string
}

func (m thinkingMode) effortLabel() string {
	if m.kind == "disabled" || m.effort == "" {
		return "off"
	}
	return m.effort
}

const (
	cellTypeUser            = transcript.CellTypeUser
	cellTypeAssistantStream = transcript.CellTypeAssistantStream
	cellTypeAssistantFinal  = transcript.CellTypeAssistantFinal
	cellTypeThinking        = transcript.CellTypeThinking
	cellTypeToolCall        = transcript.CellTypeToolCall
	cellTypeToolBatch       = transcript.CellTypeToolBatch
	cellTypeToolGroup       = transcript.CellTypeToolGroup
	cellTypeAuditSecurity   = transcript.CellTypeAuditSecurity
	cellTypeCompaction      = transcript.CellTypeCompaction
	cellTypeMCPStatus       = transcript.CellTypeMCPStatus
	cellTypeRunSummary      = transcript.CellTypeRunSummary
	cellTypeError           = transcript.CellTypeError
	cellTypeStatus          = transcript.CellTypeStatus
)

type Model struct {
	cfg                 config.Config
	authSettings        config.AuthSettings
	providerBinding     config.ProviderBinding
	profile             config.ProfileSelection
	runtime             config.RuntimeLimits
	toolPolicy          config.ToolPolicySettings
	diagnosticsSettings config.DiagnosticsSettings
	mcpSettings         config.MCPSettings
	hookSettings        config.HookSettings
	instructions        config.InstructionSettings
	promptCommands      []promptcommands.Command
	promptCommandErr    string
	promptCommandMeta   map[string]string
	gatewayURL          string
	sessionID           string
	lastGatewayEventSeq int64
	messages            []protocol.Message
	version             string

	models       []string
	modelIndex   int
	thinking     []thinkingMode
	thinkingIdx  int
	theme        string
	toolView     string
	thinkView    string
	showThinking bool
	dangerous    bool
	accessMode   string
	maxRounds    int
	followOutput bool
	plain        bool
	settings     appSettings
	settingsPath string
	sessionsDir  string
	localChatID  string
	chatTitle    string
	chatCreated  time.Time

	textarea textarea.Model
	viewport viewport.Model
	// viewportContent is the unhighlighted transcript. Mouse selection applies
	// ANSI styling over this copy so repeated drag events do not stack styles.
	viewportContent string
	width           int
	height          int

	blocks               []transcript.Cell
	richRenderCache      map[string]tuirender.CellCache
	nextBlockSeq         int64
	collapsed            map[int]bool
	selected             int
	busy                 bool
	status               string
	err                  string
	events               chan tea.Msg
	pendingStreamEvents  []protocol.Event
	streamBatchScheduled bool
	pendingUserInput     *protocol.UserInputRequestEvent
	uxProjector          *uxprojector.Projector
	transcriptProjector  *transcript.Projector
	transcriptStale      bool
	reflowCount          int
	modelCalls           int
	toolCalls            int
	runStartModelCalls   int
	runStartToolCalls    int
	runStartInputTok     int64
	runStartOutputTok    int64
	runStartCacheHit     int64
	runStartCacheMiss    int64
	runStartReasoning    int64
	runStartSummaryIn    int64
	runStartSummaryOut   int64
	runStartSummaryAPI   int64
	runStartHelperIn     int64
	runStartHelperOut    int64
	runStartHelperHit    int64
	runStartHelperMiss   int64
	runStartHelperAPI    int64
	inputTok             int64
	outputTok            int64
	cacheHitTok          int64
	cacheMissTok         int64
	reasoningTok         int64
	lastInputTok         int64
	lastOutputTok        int64
	lastCacheHitTok      int64
	lastCacheMissTok     int64
	toolSummaryInTok     int64
	toolSummaryOutTok    int64
	toolSummaryAPITok    int64
	helperModelInTok     int64
	helperModelOutTok    int64
	helperModelCacheHit  int64
	helperModelCacheMiss int64
	helperModelAPITok    int64
	slashIndex           int
	slashDismissed       string
	fileResolver         *filesearch.Resolver
	fileMentionIndex     int
	fileMentionSeq       int64
	fileMentionPending   int64
	fileMentionToken     fileMentionToken
	fileMentionResults   []filesearch.Match
	fileMentionDismissed string
	fileMentionSearching bool
	fileMentionErr       string
	authInputProvider    string
	selection            tuiselection.Controller
	runStartedAt         time.Time
	lastRunDuration      time.Duration
	spinnerFrame         int
}

type streamEventMsg struct {
	event protocol.Event
}

type runDoneMsg struct {
	messages []protocol.Message
	err      error
}

type errMsg struct {
	err error
}

type mcpStatusMsg struct {
	text string
	err  error
}

type configStatusMsg struct {
	text string
	err  error
}

type contextStatusMsg struct {
	text string
	err  error
}

type turnDiffPreviewMsg struct {
	text string
	err  error
}

type turnUndoMsg struct {
	text string
	err  error
}

type turnRedoMsg struct {
	text string
	err  error
}

type userInputAnswerMsg struct {
	requestID string
	status    string
	err       error
}

type clipboardCopiedMsg struct {
	chars  int
	method string
	err    string
}

type tickMsg time.Time
type eventBatchTickMsg time.Time

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultTextareaPlaceholder = "Message billyharness. Type / for commands."
const streamEventBatchInterval = 25 * time.Millisecond

func Run(opts Options) error {
	cfg := config.Default()
	cfg.StoreReasoningContent = true
	if opts.Dangerous {
		cfg.AutoApproveDangerous = true
	}
	if opts.AccessMode != "" {
		cfg.AccessMode = config.NormalizeAccessMode(opts.AccessMode)
	}
	if opts.MaxRounds > 0 {
		cfg.MaxToolRounds = opts.MaxRounds
	}
	m := NewModel(cfg, opts)
	_, err := tea.NewProgram(m).Run()
	return err
}

func NewModel(cfg config.Config, opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = defaultTextareaPlaceholder
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.SetWidth(80)
	ta.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.KeyMap = viewport.KeyMap{}
	settings, settingsPath, sessionsDir, settingsErr := loadAppSettings()
	if opts.Model == "" && settings.LastSelectedModel != "" {
		cfg.Model = settings.LastSelectedModel
	}
	if settings.LastProfile != "" {
		cfg.Profile = config.NormalizeProfileName(settings.LastProfile)
	}
	if settings.LastReasoningKind != "" {
		cfg.Thinking = settings.LastReasoningKind
		cfg.ReasoningEffort = settings.LastReasoningEffort
	}
	if opts.AccessMode == "" && settings.LastAccessMode != "" {
		cfg.AccessMode = config.NormalizeAccessMode(settings.LastAccessMode)
	}
	models := []string{
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
	}
	modelIndex := 0
	if opts.Model != "" {
		models = appendIfMissing(models, opts.Model)
		for i, model := range models {
			if model == opts.Model {
				modelIndex = i
				break
			}
		}
	} else if cfg.Model != "" {
		models = appendIfMissing(models, cfg.Model)
		for i, model := range models {
			if model == cfg.Model {
				modelIndex = i
				break
			}
		}
	}
	thinking := []thinkingMode{
		{"reasoning: high", "enabled", "high"},
		{"reasoning: xhigh", "enabled", "xhigh"},
		{"reasoning: max", "enabled", "max"},
		{"reasoning: medium", "enabled", "medium"},
		{"reasoning: low", "enabled", "low"},
		{"reasoning: off", "disabled", ""},
	}
	thinkingIdx := 0
	for i, mode := range thinking {
		if mode.kind == cfg.Thinking && mode.effort == cfg.ReasoningEffort {
			thinkingIdx = i
			break
		}
		if cfg.Thinking == "disabled" && mode.kind == "disabled" {
			thinkingIdx = i
		}
	}
	plain := opts.Plain || strings.EqualFold(os.Getenv("TERM"), "dumb")
	theme := settings.Theme
	if theme == "" {
		theme = "dark"
	}
	toolView := settings.ToolView
	if toolView == "" {
		toolView = "collapsed"
	}
	thinkView := settings.ThinkView
	if thinkView == "" {
		thinkView = "expanded"
	}
	status := "ready"
	if notice := strings.TrimSpace(opts.GatewayNotice); notice != "" {
		status = notice
	}
	if settingsErr != nil {
		status = "settings error: " + settingsErr.Error()
	}
	localChatID := newChatID()
	createdAt := time.Now().UTC()
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	m := Model{
		cfg:                 cfg,
		gatewayURL:          strings.TrimRight(opts.GatewayURL, "/"),
		version:             version,
		models:              models,
		modelIndex:          modelIndex,
		thinking:            thinking,
		thinkingIdx:         thinkingIdx,
		theme:               theme,
		toolView:            toolView,
		thinkView:           thinkView,
		showThinking:        thinkView != "hidden",
		dangerous:           opts.Dangerous || cfg.AutoApproveDangerous,
		accessMode:          config.NormalizeAccessMode(cfg.AccessMode),
		maxRounds:           cfg.MaxToolRounds,
		followOutput:        true,
		plain:               plain,
		settings:            settings,
		settingsPath:        settingsPath,
		sessionsDir:         sessionsDir,
		localChatID:         localChatID,
		chatTitle:           "new chat",
		chatCreated:         createdAt,
		textarea:            ta,
		viewport:            vp,
		collapsed:           map[int]bool{},
		richRenderCache:     map[string]tuirender.CellCache{},
		events:              make(chan tea.Msg, 256),
		uxProjector:         uxprojector.New(),
		transcriptProjector: transcript.NewProjector(),
		fileResolver:        filesearch.NewResolver(filesearch.DefaultCacheTTL),
		status:              status,
	}
	m.refreshConfigProjections()
	m.loadPromptCommands()
	m.messages = tuiruntime.InitialMessages(m.instructions)
	_ = m.saveCurrentSession()
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{}
	if m.gatewayURL != "" {
		cmds = append(cmds, m.createSessionCmd())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	reflow := false
	gotoBottom := false
	skipTextareaUpdate := false
	skipViewportUpdate := false
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize(m.followOutput)
	case tea.KeyPressMsg:
		if m.authInputProvider != "" && msg.String() == "esc" {
			m.cancelAuthInput()
			skipTextareaUpdate = true
			break
		}
		if m.handleSlashNavigation(msg) {
			skipTextareaUpdate = true
			break
		}
		if m.handleFileMentionNavigation(msg) {
			skipTextareaUpdate = true
			break
		}
		if result, ok := m.handleKeyAction(msg); ok {
			if result.returnNow {
				if result.model == nil {
					result.model = m
				}
				return result.model, result.cmd
			}
			if result.cmd != nil {
				cmds = append(cmds, result.cmd)
			}
			reflow = reflow || result.reflow
			gotoBottom = gotoBottom || result.gotoBottom
			skipTextareaUpdate = result.skipTextareaUpdate
			skipViewportUpdate = result.skipViewportUpdate
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && m.selection.Begin(m.selectionViewport(), msg.X, msg.Y) {
			m.applySelectionHighlight()
			m.status = "selecting"
			skipTextareaUpdate = true
			skipViewportUpdate = true
		}
	case tea.MouseMotionMsg:
		if m.selection.Drag(m.selectionViewport(), msg.X, msg.Y) {
			m.applySelectionHighlight()
			m.status = "selecting"
			skipTextareaUpdate = true
			skipViewportUpdate = true
		}
	case tea.MouseReleaseMsg:
		if msg.Button == tea.MouseLeft && m.selection.Release(m.selectionViewport(), msg.X, msg.Y) {
			m.applySelectionHighlight()
			text := m.selectedTranscriptText()
			skipTextareaUpdate = true
			skipViewportUpdate = true
			if strings.TrimSpace(text) != "" {
				m.status = fmt.Sprintf("copying %d chars", len([]rune(text)))
				cmds = append(cmds, copySelectionCmd(text))
			} else {
				m.status = "selection empty"
			}
		}
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp, tea.MouseWheelLeft:
			m.followOutput = false
		}
	case clipboardCopiedMsg:
		if msg.err != "" {
			m.status = "copy failed: " + msg.err
		} else {
			m.status = fmt.Sprintf("copied %d chars via %s", msg.chars, msg.method)
		}
	case sessionReadyMsg:
		m.sessionID = msg.id
		m.lastGatewayEventSeq = 0
		m.status = "gateway session " + msg.id[:min(len(msg.id), 8)]
		m.settings.LastGatewaySessionID = msg.id
		_ = m.saveSettings()
		_ = m.saveCurrentSession()
	case replayEventsMsg:
		if msg.err != nil {
			if msg.fallbackCreate {
				m.status = "gateway replay failed; creating session"
				cmds = append(cmds, m.createSessionCmd())
			} else {
				m.err = msg.err.Error()
				m.addBlock("error", "GATEWAY", m.err)
				m.status = "gateway replay failed"
				reflow = true
				gotoBottom = m.followOutput
			}
			break
		}
		for _, event := range msg.events {
			m.applyEvent(event)
		}
		if len(msg.messages) > 0 {
			m.messages = msg.messages
		}
		m.status = fmt.Sprintf("gateway replayed %d events", len(msg.events))
		_ = m.saveCurrentSession()
		reflow = true
		gotoBottom = m.followOutput
	case streamEventMsg:
		m.queueStreamEvent(msg.event)
		if shouldFlushStreamEvent(msg.event) {
			reflow = m.flushStreamEvents()
			gotoBottom = m.followOutput
		} else if !m.streamBatchScheduled {
			m.streamBatchScheduled = true
			cmds = append(cmds, m.eventBatchCmd())
		}
		if m.busy {
			cmds = append(cmds, m.waitEventCmd())
		}
	case runDoneMsg:
		if m.flushStreamEvents() {
			reflow = true
			gotoBottom = m.followOutput
		}
		m.streamBatchScheduled = false
		m.busy = false
		m.finishLiveBlocks()
		if !m.runStartedAt.IsZero() {
			m.lastRunDuration = time.Since(m.runStartedAt)
		}
		m.runStartedAt = time.Time{}
		if len(msg.messages) > 0 {
			m.messages = msg.messages
		}
		if msg.err != nil {
			m.err = msg.err.Error()
			m.addBlock("error", "ERROR", m.err)
		} else {
			m.status = "completed"
		}
		_ = m.saveCurrentSession()
		reflow = true
		gotoBottom = m.followOutput
	case errMsg:
		if m.flushStreamEvents() {
			reflow = true
			gotoBottom = m.followOutput
		}
		m.streamBatchScheduled = false
		m.busy = false
		m.finishLiveBlocks()
		if !m.runStartedAt.IsZero() {
			m.lastRunDuration = time.Since(m.runStartedAt)
		}
		m.runStartedAt = time.Time{}
		m.err = msg.err.Error()
		m.addBlock("error", "ERROR", m.err)
		_ = m.saveCurrentSession()
		reflow = true
		gotoBottom = m.followOutput
	case mcpStatusMsg:
		if msg.err != nil {
			m.addBlock("error", "MCP", msg.err.Error())
			m.status = "mcp status failed"
		} else {
			m.addInfoBlock("MCP", msg.text)
			m.status = "mcp status shown"
		}
		reflow = true
		gotoBottom = m.followOutput
	case configStatusMsg:
		if msg.err != nil {
			m.addBlock("error", "CONFIG", msg.err.Error())
			m.status = "config failed"
		} else {
			m.addInfoBlock("CONFIG", msg.text)
			m.status = "config shown"
		}
		reflow = true
		gotoBottom = m.followOutput
	case contextStatusMsg:
		if msg.err != nil {
			m.addBlock("error", "CONTEXT", msg.err.Error())
			m.status = "context failed"
		} else {
			m.addInfoBlock("CONTEXT", msg.text)
			m.status = "context shown"
		}
		reflow = true
		gotoBottom = m.followOutput
	case turnDiffPreviewMsg:
		if msg.err != nil {
			m.addBlock("error", "DIFF", msg.err.Error())
			m.status = "diff preview failed"
		} else {
			m.addInfoBlock("DIFF", msg.text)
			m.status = "diff preview shown"
		}
		reflow = true
		gotoBottom = m.followOutput
	case turnUndoMsg:
		if msg.err != nil {
			m.addBlock("error", "UNDO", msg.err.Error())
			m.status = "undo failed"
		} else {
			m.addInfoBlock("UNDO", msg.text)
			m.status = "undo applied"
		}
		reflow = true
		gotoBottom = m.followOutput
	case turnRedoMsg:
		if msg.err != nil {
			m.addBlock("error", "REDO", msg.err.Error())
			m.status = "redo failed"
		} else {
			m.addInfoBlock("REDO", msg.text)
			m.status = "redo applied"
		}
		reflow = true
		gotoBottom = m.followOutput
	case userInputAnswerMsg:
		if msg.err != nil {
			m.addBlock("error", "ANSWER", msg.err.Error())
			m.status = "answer failed"
		} else {
			if m.pendingUserInput != nil && m.pendingUserInput.RequestID == msg.requestID {
				m.pendingUserInput = nil
			}
			if msg.status != "" {
				m.status = "answer " + msg.status
			} else {
				m.status = "answer sent"
			}
		}
		reflow = true
		gotoBottom = m.followOutput
	case fileMentionResultsMsg:
		m.applyFileMentionResults(msg)
		if m.width > 0 {
			m.resize(m.followOutput)
		}
	case authResultMsg:
		m.cancelAuthInput()
		if msg.err != nil {
			m.addBlock("error", "AUTH", msg.err.Error())
			m.status = "auth failed"
		} else {
			m.addInfoBlock("AUTH", msg.text)
			m.status = "auth updated"
		}
		reflow = true
		gotoBottom = m.followOutput
	case tickMsg:
		if m.busy {
			m.spinnerFrame++
			cmds = append(cmds, m.tickCmd())
		}
	case eventBatchTickMsg:
		m.streamBatchScheduled = false
		if m.flushStreamEvents() {
			reflow = true
			gotoBottom = m.followOutput
		}
	}

	var cmd tea.Cmd
	if !skipTextareaUpdate {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	if _, ok := msg.(tea.KeyPressMsg); ok && m.width > 0 {
		if cmd := m.updateFileMentionSearch(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.resize(m.followOutput)
	}
	if !skipViewportUpdate {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
		if wheel, ok := msg.(tea.MouseWheelMsg); ok {
			switch wheel.Button {
			case tea.MouseWheelDown, tea.MouseWheelRight:
				m.followOutput = m.viewport.AtBottom()
			}
		}
	}
	if reflow {
		m.reflow(gotoBottom)
	}
	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("starting...")
		m.applyTerminalMode(&v)
		return v
	}
	styles := m.styles()
	ta := m.textarea
	ta.SetStyles(styles.textarea)
	input := styles.input.Width(m.inputContentWidth(styles)).Render(ta.View())
	popup := m.inputPopupView()
	runStatus := m.runStatusView()
	status := styles.status.Width(m.statusContentWidth(styles)).Render(m.inlineStatusView())
	parts := []string{m.viewport.View()}
	if popup != "" {
		parts = append(parts, popup)
	}
	if runStatus != "" {
		parts = append(parts, runStatus)
	}
	parts = append(parts, input, status)
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
	v.BackgroundColor = lipgloss.Color(styles.background)
	v.ForegroundColor = lipgloss.Color(styles.foreground)
	m.applyTerminalMode(&v)
	return v
}

func (m Model) applyTerminalMode(v *tea.View) {
	if m.plain {
		v.DisableBracketedPasteMode = true
		return
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
}

func (m *Model) resize(gotoBottom bool) {
	styles := m.styles()
	oldViewportWidth := m.viewport.Width()
	needsTranscriptReflow := oldViewportWidth != m.width || (m.viewportContent == "" && len(m.blocks) > 0)
	m.viewport.SetWidth(m.width)
	m.viewport.HighlightStyle = styles.selection
	m.viewport.SelectedHighlightStyle = styles.selection
	inputContentW := m.inputContentWidth(styles)
	m.textarea.SetWidth(inputContentW)
	m.textarea.SetHeight(m.inputHeight(inputContentW))
	ta := m.textarea
	ta.SetStyles(styles.textarea)
	inputH := lipgloss.Height(styles.input.Width(inputContentW).Render(ta.View()))
	runStatusH := lipgloss.Height(m.runStatusView())
	statusH := lipgloss.Height(styles.status.Width(m.statusContentWidth(styles)).Render(m.inlineStatusView()))
	popupH := m.inputPopupHeight()
	vh := max(4, m.height-inputH-runStatusH-statusH-popupH)
	m.viewport.SetHeight(vh)
	if needsTranscriptReflow {
		m.reflow(gotoBottom)
		return
	}
	if gotoBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) inputHeight(contentWidth int) int {
	text := m.textarea.Value()
	contentWidth = max(1, contentWidth)
	height := 0
	for _, line := range strings.Split(text, "\n") {
		lineWidth := max(1, lipgloss.Width(line))
		height += max(1, (lineWidth+contentWidth-1)/contentWidth)
	}
	if height < 1 {
		height = 1
	}
	return min(height, 6)
}

func (m Model) inputContentWidth(styles themeStyles) int {
	outer := max(24, m.width-2)
	return max(20, outer-styles.input.GetHorizontalFrameSize())
}

func (m Model) statusContentWidth(styles themeStyles) int {
	return max(20, m.width-styles.status.GetHorizontalFrameSize())
}

func (m Model) headerView() string {
	styles := m.styles()
	mode := "local"
	if m.gatewayURL != "" {
		mode = "gateway"
	}
	danger := "safe"
	if m.dangerous {
		danger = "dangerous tools"
	}
	state := strings.ToUpper(m.status)
	if m.busy {
		state = "RUNNING"
	}
	if !m.followOutput {
		state = "SCROLLED"
	}
	title := m.chatTitle
	if title == "" {
		title = "new chat"
	}
	line := fitSegments(max(1, m.width-2), "  ",
		"billyharness",
		state,
		mode,
		shortModel(m.currentModel()),
		m.currentThinking().effortLabel(),
		danger,
		"chat:"+shortID(m.localChatID),
		title,
	)
	return styles.header.Width(m.width).Render(" " + line)
}

func (m Model) footerView() string {
	return "Enter send  Alt+Enter newline  Ctrl+K commands  Tab complete  mouse/Pg scroll  Alt+End follow"
}

type statusSegment struct {
	text  string
	style lipgloss.Style
}

func (m Model) contextTokens() int64 {
	return m.lastInputTok + m.lastOutputTok
}

func (m Model) usedTokens() int64 {
	uncachedInput := m.inputTok - m.cacheHitTok
	if uncachedInput < 0 {
		uncachedInput = m.cacheMissTok
	}
	return uncachedInput + m.outputTok
}

func (m *Model) send() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.textarea.Value())
	if m.authInputProvider != "" {
		if prompt == "" {
			m.status = "empty credential"
			m.reflow(m.followOutput)
			return *m, nil
		}
		provider := m.authInputProvider
		m.textarea.SetValue("")
		m.textarea.SetHeight(1)
		m.status = "saving " + provider + " credential"
		m.reflow(m.followOutput)
		return *m, m.authSaveCmd(provider, prompt)
	}
	if prompt == "" {
		m.status = "empty prompt"
		m.reflow(m.followOutput)
		return *m, nil
	}
	if m.pendingUserInput != nil {
		return m.submitUserInputAnswer(prompt)
	}
	if strings.HasPrefix(prompt, "/") {
		if command, prefix, ok := m.slashArgMode(); ok {
			args := m.filteredSlashArgs(command, prefix)
			if len(args) > 0 && !m.exactSlashArg(command, prefix) {
				m.clampSlashIndexLen(len(args))
				prompt = command.name + " " + args[m.slashIndex].value
			}
		}
		if !m.exactSlashCommand(prompt) {
			commands := m.filteredSlashCommands()
			if len(commands) > 0 {
				m.clampSlashIndex(commands)
				command := commands[m.slashIndex]
				prompt = command.name
				if args := m.slashArgs(command); len(args) > 0 {
					prompt += " " + args[0].value
				}
			}
		}
		handled, cmd := m.handleSlashCommand(prompt)
		if handled {
			m.textarea.SetValue("")
			m.textarea.SetHeight(1)
		}
		m.reflow(m.followOutput)
		return *m, cmd
	}
	return m.submitPrompt(prompt)
}

func (m *Model) submitPrompt(prompt string) (tea.Model, tea.Cmd) {
	if m.busy {
		m.status = "busy"
		m.reflow(m.followOutput)
		m.promptCommandMeta = nil
		return *m, nil
	}
	if m.gatewayURL != "" && m.sessionID == "" {
		m.status = "gateway session not ready"
		m.reflow(m.followOutput)
		m.promptCommandMeta = nil
		return *m, nil
	}
	promptMetadata := copyPromptMetadata(m.promptCommandMeta)
	m.promptCommandMeta = nil
	m.textarea.SetValue("")
	m.textarea.SetHeight(1)
	m.addBlock("user", "USER", prompt)
	m.busy = true
	m.err = ""
	m.status = "running"
	m.runStartedAt = time.Now()
	m.followOutput = true
	if m.chatTitle == "new chat" {
		m.chatTitle = sessionTitle(prompt)
	}
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		go m.runGateway(prompt, promptMetadata)
	} else {
		go m.runLocal(prompt, promptMetadata)
	}
	m.reflow(true)
	return *m, tea.Batch(m.waitEventCmd(), m.tickCmd())
}

func (m *Model) submitUserInputAnswer(answer string) (tea.Model, tea.Cmd) {
	if m.gatewayURL == "" || m.sessionID == "" || m.pendingUserInput == nil {
		m.status = "no pending question"
		m.reflow(m.followOutput)
		return *m, nil
	}
	requestID := m.pendingUserInput.RequestID
	m.textarea.SetValue("")
	m.textarea.SetHeight(1)
	m.addBlock("user", "ANSWER", answer)
	m.status = "sending answer"
	m.reflow(true)
	return *m, m.answerUserInputCmd(requestID, answer)
}

func (m Model) waitEventCmd() tea.Cmd {
	return func() tea.Msg {
		return <-m.events
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) eventBatchCmd() tea.Cmd {
	return tea.Tick(streamEventBatchInterval, func(t time.Time) tea.Msg {
		return eventBatchTickMsg(t)
	})
}

func (m Model) runLocal(prompt string, metadata map[string]string) {
	next, err := tuiruntime.RunLocal(context.Background(), m.runtimeClientSettings(), m.messages, prompt, metadata, func(event protocol.Event) {
		m.events <- streamEventMsg{event: event}
	})
	m.events <- runDoneMsg{messages: next, err: err}
}

type mcpStatusResponse = mcpstatus.Response

type configStatusResponse struct {
	Config   map[string]any         `json:"config"`
	Values   []config.ResolvedValue `json:"values"`
	Warnings []string               `json:"warnings,omitempty"`
}

func (m Model) configStatusCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := m.loadConfigSummary()
		return configStatusMsg{text: text, err: err}
	}
}

func (m Model) contextStatusCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := m.loadContextStatus()
		return contextStatusMsg{text: text, err: err}
	}
}

func (m Model) loadContextStatus() (string, error) {
	if m.gatewayURL != "" {
		if m.sessionID == "" {
			return "", fmt.Errorf("gateway session is not ready")
		}
		var out gatewayapi.SessionContextResponse
		path := "/v1/sessions/" + url.PathEscape(m.sessionID) + "/context"
		if err := m.gatewayJSON(http.MethodGet, path, nil, &out); err != nil {
			return "", err
		}
		return gatewayclient.FormatSessionContext(out), nil
	}
	resp := clientux.BuildContextResponseWithOptions(m.runtime, m.localChatID, m.messages, clientux.ContextReportOptions{
		Runtime: gatewayapi.ContextRuntime{
			Model:         m.currentModel(),
			Profile:       m.currentProfile(),
			ReasoningMode: m.currentThinking().effortLabel(),
			AccessMode:    m.currentAccessMode(),
		},
		Usage: gatewayapi.ContextUsage{
			ModelCalls:              m.modelCalls,
			ToolCalls:               m.toolCalls,
			InputTokens:             m.inputTok,
			OutputTokens:            m.outputTok,
			CacheHitTokens:          m.cacheHitTok,
			CacheMissTokens:         m.cacheMissTok,
			ReasoningTokens:         m.reasoningTok,
			LastInputTokens:         m.lastInputTok,
			LastOutputTokens:        m.lastOutputTok,
			LastCacheHitTokens:      m.lastCacheHitTok,
			LastCacheMissTokens:     m.lastCacheMissTok,
			WebSummaryInputTokens:   m.toolSummaryInTok,
			WebSummaryOutputTokens:  m.toolSummaryOutTok,
			HelperModelInputTokens:  m.helperModelInTok,
			HelperModelOutputTokens: m.helperModelOutTok,
			HelperModelCacheHit:     m.helperModelCacheHit,
			HelperModelCacheMiss:    m.helperModelCacheMiss,
			HelperModelAPITokens:    m.helperModelAPITok,
		},
	})
	return gatewayclient.FormatSessionContext(resp), nil
}

func (m Model) loadConfigSummary() (string, error) {
	if m.gatewayURL != "" {
		var out configStatusResponse
		if err := m.gatewayJSON(http.MethodGet, "/v1/config", nil, &out); err != nil {
			return "", err
		}
		return config.FormatSummary(out.Values, out.Warnings), nil
	}
	base, err := config.Resolve()
	if err != nil {
		return "", err
	}
	resolved, err := config.Resolve(config.RuntimeDiffOverridesFromSettings(base.Config, m.runtimeDiffSettings(), config.SourceGateway)...)
	if err != nil {
		return "", err
	}
	return config.FormatSummary(resolved.SanitizedValues(), resolved.Warnings), nil
}

func (m Model) mcpStatusCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := m.loadMCPStatus()
		return mcpStatusMsg{text: text, err: err}
	}
}

func (m Model) loadMCPStatus() (string, error) {
	if m.gatewayURL != "" {
		resp, err := m.fetchGatewayMCPStatus()
		if err != nil {
			return "", err
		}
		return formatMCPStatus(resp), nil
	}
	status, err := tuiruntime.MCPStatus(context.Background(), m.runtimeClientSettings())
	if err != nil {
		return "", err
	}
	return formatMCPStatus(status), nil
}

func (m Model) fetchGatewayMCPStatus() (mcpStatusResponse, error) {
	client := http.Client{Timeout: 15 * time.Second}
	resp, err := m.gatewayRequest(context.Background(), &client, http.MethodGet, "/v1/mcp", nil)
	if err != nil {
		return mcpStatusResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return mcpStatusResponse{}, fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	var out mcpStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return mcpStatusResponse{}, err
	}
	return out, nil
}

func formatMCPStatus(status mcpStatusResponse) string {
	return mcpstatus.Format(status)
}

func (m Model) selectedConfig() config.Config {
	cfg := m.cfg
	cfg.Model = m.currentModel()
	cfg.Provider = modelinfo.ProviderForModel(cfg.Model, cfg.Provider)
	cfg.Profile = config.NormalizeProfileName(cfg.Profile)
	cfg.Thinking = m.currentThinking().kind
	cfg.ReasoningEffort = m.currentThinking().effort
	cfg.StoreReasoningContent = true
	cfg.AutoApproveDangerous = cfg.AutoApproveDangerous || m.dangerous
	cfg.AccessMode = config.NormalizeAccessMode(m.accessMode)
	cfg.MaxToolRounds = m.maxRounds
	cfg.ApplyModelProviderDefaults()
	return cfg
}

func (m *Model) refreshConfigProjections() {
	cfg := m.selectedConfig()
	m.authSettings = cfg.AuthSettings()
	m.providerBinding = cfg.ProviderBinding()
	m.profile = cfg.ProfileSelection()
	m.runtime = cfg.RuntimeLimits()
	m.toolPolicy = cfg.ToolPolicySettings()
	m.diagnosticsSettings = cfg.DiagnosticsSettings()
	m.mcpSettings = cfg.MCPSettings()
	m.hookSettings = cfg.HookSettings()
	m.instructions = cfg.InstructionSettings()
}

func (m Model) runtimeClientSettings() tuiruntime.Settings {
	return tuiruntime.Settings{
		ProviderBinding: m.providerBinding,
		Profile:         m.profile,
		Runtime:         m.runtime,
		ToolPolicy:      m.toolPolicy,
		Diagnostics:     m.diagnosticsSettings,
		MCP:             m.mcpSettings,
		Hooks:           m.hookSettings,
		Instructions:    m.instructions,
	}
}

func (m Model) runtimeDiffSettings() config.RuntimeDiffSettings {
	return config.RuntimeDiffSettings{
		Provider:    m.providerBinding,
		Profile:     m.profile,
		Runtime:     m.runtime,
		ToolPolicy:  m.toolPolicy,
		Diagnostics: m.diagnosticsSettings,
		MCP:         m.mcpSettings,
		Hooks:       m.hookSettings,
		GatewayAddr: m.cfg.GatewayAddr,
	}
}

func (m Model) currentModel() string {
	return m.models[m.modelIndex]
}

func (m Model) currentProvider() string {
	if m.providerBinding.Provider.Provider != "" {
		return m.providerBinding.Provider.Provider
	}
	return modelinfo.ProviderForModel(m.currentModel(), m.cfg.Provider)
}

func (m Model) currentProfile() string {
	if m.profile.Profile != "" {
		return m.profile.Profile
	}
	return config.NormalizeProfileName(m.cfg.Profile)
}

func (m Model) currentThinking() thinkingMode {
	return m.thinking[m.thinkingIdx]
}

func (m Model) currentAccessMode() string {
	return config.NormalizeAccessMode(m.accessMode)
}

func (m *Model) setTheme(value string) bool {
	switch value {
	case "", "toggle", "next":
		if m.theme == "dark" {
			m.theme = "light"
		} else {
			m.theme = "dark"
		}
	case "light", "white":
		m.theme = "light"
	case "dark", "black":
		m.theme = "dark"
	default:
		m.status = "unknown theme " + value
		return false
	}
	m.status = "theme " + m.theme
	_ = m.saveSettings()
	return true
}

func (m *Model) cycleModel() {
	m.modelIndex = (m.modelIndex + 1) % len(m.models)
	m.refreshConfigProjections()
	m.status = "model " + m.currentModel()
	_ = m.saveSettings()
}

func (m *Model) setModel(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.cycleModel()
		return true
	default:
		value = modelinfo.NormalizeAlias(value)
		if !strings.Contains(value, "/") && !strings.Contains(value, " ") {
			// Keep the TUI open to custom compatible model ids without another release.
			m.models = appendIfMissing(m.models, value)
		} else {
			m.status = "unknown model " + value
			return false
		}
	}
	m.models = appendIfMissing(m.models, value)
	for i, model := range m.models {
		if model == value {
			m.modelIndex = i
			break
		}
	}
	m.refreshConfigProjections()
	m.status = "model " + m.currentModel()
	_ = m.saveSettings()
	return true
}

func (m *Model) setProfile(value string) tea.Cmd {
	value = strings.TrimSpace(value)
	if value == "" {
		m.addInfoBlock("PROFILE", "current profile: "+m.currentProfile()+"\nfile: "+config.DefaultProfileFile(m.currentProfile()))
		m.status = "profile " + m.currentProfile()
		return nil
	}
	profile := config.NormalizeProfileName(value)
	if _, err := config.EnsureDefaultProfileFile(profile); err != nil {
		m.status = "profile error: " + err.Error()
		m.addBlock("error", "ERROR", err.Error())
		return nil
	}
	_ = m.saveCurrentSession()
	m.cfg.Profile = profile
	if err := m.cfg.ApplyProfileMetadata(); err != nil {
		m.status = "profile metadata error: " + err.Error()
		m.addBlock("error", "ERROR", err.Error())
		return nil
	}
	m.applyConfigSelection(m.cfg)
	m.resetFreshChatState("new chat")
	m.status = "profile " + m.currentProfile() + "; new chat"
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) applyConfigSelection(cfg config.Config) {
	defer m.refreshConfigProjections()
	model := modelinfo.NormalizeAlias(cfg.Model)
	if model != "" {
		m.models = appendIfMissing(m.models, model)
		for i, candidate := range m.models {
			if candidate == model {
				m.modelIndex = i
				break
			}
		}
	}
	for i, mode := range m.thinking {
		if mode.kind == cfg.Thinking && mode.effort == cfg.ReasoningEffort {
			m.thinkingIdx = i
			return
		}
	}
	for i, mode := range m.thinking {
		if mode.effort == cfg.ReasoningEffort || (cfg.Thinking == "disabled" && mode.kind == "disabled") {
			m.thinkingIdx = i
			return
		}
	}
}

func (m *Model) setAccessMode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "next", "toggle":
		switch m.currentAccessMode() {
		case config.AccessModeBuild:
			value = config.AccessModeGuarded
		case config.AccessModeGuarded:
			value = config.AccessModePlan
		default:
			value = config.AccessModeBuild
		}
	case config.AccessModeBuild, config.AccessModeGuarded, config.AccessModePlan, "safe", "readonly", "read-only", "read_only", "analysis":
	default:
		m.status = "unknown access mode " + value
		return false
	}
	m.accessMode = config.NormalizeAccessMode(value)
	m.refreshConfigProjections()
	m.status = "mode " + m.accessMode
	_ = m.saveSettings()
	return true
}

func (m *Model) cycleReasoning() {
	m.thinkingIdx = (m.thinkingIdx + 1) % len(m.thinking)
	m.refreshConfigProjections()
	m.status = m.currentThinking().label
	_ = m.saveSettings()
}

func (m *Model) setReasoning(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.cycleReasoning()
		return true
	case "high", "on", "enabled":
		value = "high"
	case "xhigh", "x-high", "extra", "extra-high":
		value = "xhigh"
	case "max", "maximum":
		value = "max"
	case "medium", "med":
		value = "medium"
	case "low", "minimal", "min":
		value = "low"
	case "off", "none", "disabled":
		value = "off"
	default:
		m.status = "unknown reasoning " + value
		return false
	}
	for i, mode := range m.thinking {
		if mode.effort == value || (value == "off" && mode.kind == "disabled") {
			m.thinkingIdx = i
			m.refreshConfigProjections()
			m.status = m.currentThinking().label
			_ = m.saveSettings()
			return true
		}
	}
	m.status = "unknown reasoning " + value
	return false
}

func (m *Model) toggleThinkingDisplay() {
	if m.thinkView == "hidden" {
		m.thinkView = "expanded"
	} else {
		m.thinkView = "hidden"
	}
	m.showThinking = m.thinkView != "hidden"
	m.status = "thinking blocks " + m.thinkView
	_ = m.saveSettings()
}

func (m *Model) setThinkingDisplay(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.toggleThinkingDisplay()
	case "on", "show", "shown", "visible", "yes", "true":
		m.showThinking = true
		m.thinkView = "expanded"
		m.status = "thinking blocks expanded"
	case "off", "hide", "hidden", "no", "false":
		m.showThinking = false
		m.thinkView = "hidden"
		m.status = "thinking blocks hidden"
	default:
		m.status = "unknown thinking display " + value
		return false
	}
	_ = m.saveSettings()
	return true
}

func (m *Model) setToolView(value string) bool {
	switch value {
	case "", "toggle", "next":
		switch m.toolView {
		case "auto":
			value = "collapsed"
		case "collapsed":
			value = "current"
		case "current":
			value = "expanded"
		case "expanded":
			value = "errors"
		case "errors":
			value = "hidden"
		default:
			value = "auto"
		}
	case "show", "visible", "on":
		value = "auto"
	case "open":
		value = "expanded"
	case "close":
		value = "collapsed"
	case "turn", "current-turn":
		value = "current"
	case "failed", "error":
		value = "errors"
	}
	if !validViewMode(value, []string{"auto", "expanded", "collapsed", "current", "hidden", "errors"}) {
		m.status = "unknown toolview " + value
		return false
	}
	m.toolView = value
	m.status = "tool blocks " + value
	_ = m.saveSettings()
	return true
}

func (m *Model) setThinkView(value string) bool {
	switch value {
	case "", "toggle", "next":
		switch m.thinkView {
		case "expanded":
			value = "collapsed"
		case "collapsed":
			value = "hidden"
		default:
			value = "expanded"
		}
	case "show", "visible", "on", "open":
		value = "expanded"
	case "close":
		value = "collapsed"
	case "hide", "off":
		value = "hidden"
	}
	if !validViewMode(value, []string{"expanded", "collapsed", "hidden"}) {
		m.status = "unknown thinkview " + value
		return false
	}
	m.thinkView = value
	m.showThinking = value != "hidden"
	m.status = "thinking blocks " + value
	_ = m.saveSettings()
	return true
}
