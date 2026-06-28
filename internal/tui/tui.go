package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Options struct {
	GatewayURL string
	Model      string
	Dangerous  bool
	MaxRounds  int
	Plain      bool
	Version    string
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

type block struct {
	id        string
	kind      string
	title     string
	content   string
	live      bool
	eventType protocol.EventType
	started   time.Time
	updated   time.Time
}

type selectionPoint struct {
	row int
	col int
}

type Model struct {
	cfg        config.Config
	gatewayURL string
	sessionID  string
	messages   []protocol.Message
	version    string

	models       []string
	modelIndex   int
	thinking     []thinkingMode
	thinkingIdx  int
	theme        string
	toolView     string
	thinkView    string
	showThinking bool
	dangerous    bool
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

	blocks            []block
	nextBlockSeq      int64
	collapsed         map[int]bool
	selected          int
	busy              bool
	status            string
	err               string
	events            chan tea.Msg
	modelCalls        int
	toolCalls         int
	inputTok          int64
	outputTok         int64
	cacheHitTok       int64
	cacheMissTok      int64
	reasoningTok      int64
	lastInputTok      int64
	lastOutputTok     int64
	lastCacheHitTok   int64
	lastCacheMissTok  int64
	usageAccounting   usageAccumulator
	slashIndex        int
	slashDismissed    string
	authInputProvider string
	selecting         bool
	selectStart       selectionPoint
	selectEnd         selectionPoint
	runStartedAt      time.Time
	lastRunDuration   time.Duration
	spinnerFrame      int
}

type sessionReadyMsg struct {
	id string
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

type authResultMsg struct {
	text string
	err  error
}

type clipboardCopiedMsg struct {
	chars  int
	method string
	err    string
}

type tickMsg time.Time

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultTextareaPlaceholder = "Message billyharness. Type / for commands."
const osc52MaxBytes = 100 * 1024

func Run(opts Options) error {
	cfg := config.Default()
	cfg.StoreReasoningContent = true
	if opts.Dangerous {
		cfg.AutoApproveDangerous = true
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
		cfg:          cfg,
		gatewayURL:   strings.TrimRight(opts.GatewayURL, "/"),
		messages:     agent.InitialMessages(cfg),
		version:      version,
		models:       models,
		modelIndex:   modelIndex,
		thinking:     thinking,
		thinkingIdx:  thinkingIdx,
		theme:        theme,
		toolView:     toolView,
		thinkView:    thinkView,
		showThinking: thinkView != "hidden",
		dangerous:    opts.Dangerous || cfg.AutoApproveDangerous,
		maxRounds:    cfg.MaxToolRounds,
		followOutput: true,
		plain:        plain,
		settings:     settings,
		settingsPath: settingsPath,
		sessionsDir:  sessionsDir,
		localChatID:  localChatID,
		chatTitle:    "new chat",
		chatCreated:  createdAt,
		textarea:     ta,
		viewport:     vp,
		collapsed:    map[int]bool{},
		events:       make(chan tea.Msg, 256),
		status:       status,
	}
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
		if msg.Code == tea.KeyEnter {
			if msg.Mod.Contains(tea.ModAlt) {
				m.textarea.InsertString("\n")
				skipTextareaUpdate = true
				break
			}
			return m.send()
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+s":
			return m.send()
		case "ctrl+n":
			m.cycleModel()
			skipTextareaUpdate = true
		case "ctrl+t":
			m.cycleReasoning()
			skipTextareaUpdate = true
		case "pgup":
			m.viewport.PageUp()
			m.followOutput = false
			skipTextareaUpdate = true
			skipViewportUpdate = true
		case "pgdown":
			m.viewport.PageDown()
			m.followOutput = m.viewport.AtBottom()
			skipTextareaUpdate = true
			skipViewportUpdate = true
		case "alt+home":
			m.viewport.GotoTop()
			m.followOutput = false
			skipTextareaUpdate = true
			skipViewportUpdate = true
		case "alt+end":
			m.viewport.GotoBottom()
			m.followOutput = true
			skipTextareaUpdate = true
			skipViewportUpdate = true
		case "ctrl+g":
			if m.gatewayURL != "" {
				m.status = "connecting gateway"
				return m, m.createSessionCmd()
			}
			skipTextareaUpdate = true
		case "ctrl+r":
			m.toggleThinkingDisplay()
			reflow = true
			gotoBottom = m.followOutput
			skipTextareaUpdate = true
		case "ctrl+e":
			if len(m.blocks) > 0 {
				m.toggleSelectedBlock()
				reflow = true
			}
			skipTextareaUpdate = true
		case "ctrl+p":
			if m.selected > 0 {
				m.selected--
				reflow = true
			}
			skipTextareaUpdate = true
		case "ctrl+l":
			if m.selected < len(m.blocks)-1 {
				m.selected++
				reflow = true
			}
			skipTextareaUpdate = true
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && m.mouseInViewport(msg.X, msg.Y) {
			point := m.selectionPointFromMouse(msg.X, msg.Y)
			m.selecting = true
			m.selectStart = point
			m.selectEnd = point
			m.applySelectionHighlight()
			m.status = "selecting"
			skipTextareaUpdate = true
			skipViewportUpdate = true
		}
	case tea.MouseMotionMsg:
		if m.selecting {
			m.selectEnd = m.selectionPointFromMouseClamped(msg.X, msg.Y)
			m.applySelectionHighlight()
			m.status = "selecting"
			skipTextareaUpdate = true
			skipViewportUpdate = true
		}
	case tea.MouseReleaseMsg:
		if m.selecting && msg.Button == tea.MouseLeft {
			m.selectEnd = m.selectionPointFromMouseClamped(msg.X, msg.Y)
			m.applySelectionHighlight()
			text := m.selectedTranscriptText()
			m.selecting = false
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
		m.status = "gateway session " + msg.id[:min(len(msg.id), 8)]
		m.settings.LastGatewaySessionID = msg.id
		_ = m.saveSettings()
		_ = m.saveCurrentSession()
	case streamEventMsg:
		m.applyEvent(msg.event)
		reflow = true
		gotoBottom = m.followOutput
		if m.busy {
			cmds = append(cmds, m.waitEventCmd())
		}
	case runDoneMsg:
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
	}

	var cmd tea.Cmd
	if !skipTextareaUpdate {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	if _, ok := msg.(tea.KeyPressMsg); ok && m.width > 0 {
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
	popup := m.slashPopupView()
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
	m.viewport.SetWidth(m.width)
	m.viewport.HighlightStyle = styles.selection
	m.viewport.SelectedHighlightStyle = styles.selection
	m.textarea.SetWidth(m.inputContentWidth(styles))
	m.textarea.SetHeight(m.inputHeight(m.inputContentWidth(styles)))
	ta := m.textarea
	ta.SetStyles(styles.textarea)
	inputH := lipgloss.Height(styles.input.Width(m.inputContentWidth(styles)).Render(ta.View()))
	runStatusH := lipgloss.Height(m.runStatusView())
	statusH := lipgloss.Height(styles.status.Width(m.statusContentWidth(styles)).Render(m.inlineStatusView()))
	popupH := m.slashPopupHeight()
	vh := max(4, m.height-inputH-runStatusH-statusH-popupH)
	m.viewport.SetHeight(vh)
	m.reflow(gotoBottom)
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
	return "Enter send  Alt+Enter newline  Tab complete slash command  mouse/Pg scroll  Alt+End follow"
}

type slashCommand struct {
	name    string
	args    string
	summary string
}

type slashArg struct {
	value   string
	summary string
}

type statusSegment struct {
	text  string
	style lipgloss.Style
}

func slashCommands() []slashCommand {
	return []slashCommand{
		{"/help", "", "show commands and key bindings"},
		{"/status", "", "show current session details"},
		{"/auth", "deepseek|codex", "configure DeepSeek key or Codex OAuth"},
		{"/theme", "light|dark", "switch active theme"},
		{"/model", "flash|pro|gpt|id", "switch model"},
		{"/models", "", "list known models"},
		{"/profile", "billy|name", "switch SOUL.md system profile"},
		{"/mcp", "", "show connected MCP servers"},
		{"/reasoning", "high|max|off", "set provider reasoning effort"},
		{"/thinkview", "expanded|collapsed|hidden", "control thinking blocks"},
		{"/thinking", "on|off", "alias for thinking visibility"},
		{"/toolview", "auto|expanded|collapsed|hidden", "control tool blocks"},
		{"/new", "", "start a new chat"},
		{"/resume", "[id-prefix]", "list or resume local chats"},
		{"/fork", "[id-prefix]", "fork current or named chat"},
		{"/exit", "", "save and quit"},
	}
}

func (m Model) slashActive() bool {
	text := m.textarea.Value()
	return strings.HasPrefix(text, "/") && !strings.Contains(text, "\n") && text != m.slashDismissed
}

func (m Model) slashToken() string {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" || !strings.HasPrefix(text, "/") {
		return ""
	}
	token, _, _ := strings.Cut(text, " ")
	return strings.ToLower(token)
}

func (m Model) slashParts() (commandToken, argPrefix string, hasArg bool) {
	text := m.textarea.Value()
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return "", "", false
	}
	trimmedLeft := strings.TrimLeft(text, " \t")
	for i, r := range trimmedLeft {
		if r == ' ' || r == '\t' {
			return strings.ToLower(trimmedLeft[:i]), strings.TrimSpace(trimmedLeft[i+1:]), true
		}
	}
	return strings.ToLower(strings.TrimSpace(trimmedLeft)), "", false
}

func (m Model) slashArgMode() (slashCommand, string, bool) {
	token, argPrefix, _ := m.slashParts()
	if token == "" {
		return slashCommand{}, "", false
	}
	for _, command := range slashCommands() {
		if token == command.name && len(m.slashArgs(command)) > 0 {
			return command, strings.ToLower(argPrefix), true
		}
	}
	return slashCommand{}, "", false
}

func (m Model) filteredSlashCommands() []slashCommand {
	token := m.slashToken()
	if strings.HasPrefix(token, "/") {
		token = strings.TrimPrefix(token, "/")
	}
	var exact []slashCommand
	var prefix []slashCommand
	var contains []slashCommand
	var summary []slashCommand
	for _, command := range slashCommands() {
		name := strings.TrimPrefix(command.name, "/")
		haystack := strings.ToLower(name + " " + command.args)
		switch {
		case token == "":
			prefix = append(prefix, command)
		case name == token:
			exact = append(exact, command)
		case strings.HasPrefix(name, token):
			prefix = append(prefix, command)
		case strings.Contains(haystack, token):
			contains = append(contains, command)
		case strings.Contains(strings.ToLower(command.summary), token):
			summary = append(summary, command)
		}
	}
	out := append(exact, prefix...)
	out = append(out, contains...)
	out = append(out, summary...)
	return out
}

func (m Model) slashArgs(command slashCommand) []slashArg {
	next := func(values []slashArg, current string) []slashArg {
		for i, item := range values {
			if item.value == current {
				return append(append([]slashArg{}, values[i+1:]...), values[:i+1]...)
			}
		}
		return values
	}
	switch command.name {
	case "/auth":
		return []slashArg{
			{"deepseek", "save DeepSeek API key"},
			{"codex", "import Codex OAuth from codex login"},
		}
	case "/theme":
		values := []slashArg{
			{"dark", "black codex-style theme"},
			{"light", "light theme"},
			{"toggle", "switch to the other theme"},
		}
		return next(values, m.theme)
	case "/model":
		values := []slashArg{
			{"flash", "deepseek-v4-flash"},
			{"pro", "deepseek-v4-pro"},
			{"gpt", "gpt-5.5 via Codex subscription"},
			{"gpt-5.5", "Codex/ChatGPT subscription"},
			{"gpt-5.4", "Codex/ChatGPT subscription"},
			{"gpt-5.4-mini", "faster Codex/ChatGPT model"},
			{"gpt-5.3-codex-spark", "ultra-fast Codex coding model"},
			{"deepseek-v4-flash", "full model id"},
			{"deepseek-v4-pro", "full model id"},
			{"toggle", "switch to the other configured model"},
		}
		switch m.currentModel() {
		case "deepseek-v4-flash":
			return next(values, "flash")
		case "deepseek-v4-pro":
			return next(values, "pro")
		case "gpt-5.5":
			return next(values, "gpt")
		}
		return values
	case "/profile":
		return []slashArg{
			{m.currentProfile(), "current SOUL.md profile"},
			{"billy", "teacher-style default profile"},
		}
	case "/reasoning":
		values := []slashArg{
			{"high", "reasoning high"},
			{"xhigh", "reasoning xhigh"},
			{"max", "reasoning max"},
			{"medium", "reasoning medium"},
			{"low", "reasoning low"},
			{"off", "disable reasoning"},
			{"toggle", "cycle reasoning mode"},
		}
		return next(values, m.currentThinking().effortLabel())
	case "/thinkview":
		values := []slashArg{
			{"expanded", "show thinking content"},
			{"collapsed", "show collapsed thinking blocks"},
			{"hidden", "hide thinking blocks"},
			{"toggle", "cycle thinking view"},
		}
		return next(values, m.thinkView)
	case "/thinking":
		if m.showThinking {
			return []slashArg{{"off", "hide thinking blocks"}, {"on", "show thinking blocks"}, {"toggle", "switch thinking visibility"}}
		}
		return []slashArg{{"on", "show thinking blocks"}, {"off", "hide thinking blocks"}, {"toggle", "switch thinking visibility"}}
	case "/toolview":
		values := []slashArg{
			{"auto", "expand tool blocks while running"},
			{"expanded", "show full tool blocks"},
			{"collapsed", "collapse tool blocks"},
			{"hidden", "hide tool blocks"},
			{"toggle", "cycle tool view"},
		}
		return next(values, m.toolView)
	case "/resume":
		return m.sessionArgs(true)
	case "/fork":
		return m.sessionArgs(false)
	default:
		return nil
	}
}

func (m Model) filteredSlashArgs(command slashCommand, prefix string) []slashArg {
	args := m.slashArgs(command)
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return args
	}
	var exact []slashArg
	var starts []slashArg
	var contains []slashArg
	for _, arg := range args {
		value := strings.ToLower(arg.value)
		haystack := value + " " + strings.ToLower(arg.summary)
		switch {
		case value == prefix:
			exact = append(exact, arg)
		case strings.HasPrefix(value, prefix):
			starts = append(starts, arg)
		case strings.Contains(haystack, prefix):
			contains = append(contains, arg)
		}
	}
	out := append(exact, starts...)
	out = append(out, contains...)
	return out
}

func (m Model) exactSlashArg(command slashCommand, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return false
	}
	for _, arg := range m.slashArgs(command) {
		if strings.ToLower(arg.value) == prefix {
			return true
		}
	}
	return false
}

func (m Model) sessionArgs(includeList bool) []slashArg {
	var args []slashArg
	if includeList {
		args = append(args, slashArg{"list", "show saved chats"})
	} else {
		args = append(args, slashArg{"current", "fork current chat"})
	}
	sessions, err := listChatSessions(m.sessionsDir)
	if err != nil {
		return args
	}
	for i, session := range sessions {
		if i >= 12 {
			break
		}
		title := session.Title
		if title == "" {
			title = "untitled"
		}
		when := session.UpdatedAt.Local().Format("01-02 15:04")
		args = append(args, slashArg{
			value:   shortID(session.ID),
			summary: truncateRunes(title, 44) + " · " + when,
		})
	}
	return args
}

func (m Model) exactSlashCommand(prompt string) bool {
	fields := strings.Fields(strings.ToLower(prompt))
	if len(fields) == 0 {
		return false
	}
	for _, command := range slashCommands() {
		if fields[0] == command.name {
			return true
		}
	}
	return false
}

func (m *Model) handleSlashNavigation(msg tea.KeyPressMsg) bool {
	if !m.slashActive() {
		return false
	}
	if command, prefix, ok := m.slashArgMode(); ok {
		args := m.filteredSlashArgs(command, prefix)
		if len(args) == 0 {
			m.slashIndex = 0
			return false
		}
		switch msg.String() {
		case "tab":
			m.clampSlashIndexLen(len(args))
			m.setSlashArg(command, args[m.slashIndex].value)
			return true
		case "down", "ctrl+n":
			m.slashIndex = (m.slashIndex + 1) % len(args)
			return true
		case "up", "ctrl+p":
			m.slashIndex--
			if m.slashIndex < 0 {
				m.slashIndex = len(args) - 1
			}
			return true
		case "esc":
			m.slashIndex = 0
			m.slashDismissed = m.textarea.Value()
			return true
		}
		m.clampSlashIndexLen(len(args))
		return false
	}
	commands := m.filteredSlashCommands()
	if len(commands) == 0 {
		m.slashIndex = 0
		return false
	}
	switch msg.String() {
	case "tab":
		m.clampSlashIndex(commands)
		m.textarea.SetValue(commands[m.slashIndex].name + " ")
		m.slashDismissed = ""
		return true
	case "down", "ctrl+n":
		m.slashIndex = (m.slashIndex + 1) % len(commands)
		return true
	case "up", "ctrl+p":
		m.slashIndex--
		if m.slashIndex < 0 {
			m.slashIndex = len(commands) - 1
		}
		return true
	case "esc":
		m.slashIndex = 0
		m.slashDismissed = m.textarea.Value()
		return true
	}
	m.clampSlashIndex(commands)
	return false
}

func (m *Model) clampSlashIndex(commands []slashCommand) {
	m.clampSlashIndexLen(len(commands))
}

func (m *Model) clampSlashIndexLen(length int) {
	if length == 0 {
		m.slashIndex = 0
		return
	}
	if m.slashIndex < 0 {
		m.slashIndex = 0
	}
	if m.slashIndex >= length {
		m.slashIndex = length - 1
	}
}

func (m *Model) setSlashArg(command slashCommand, value string) {
	m.textarea.SetValue(command.name + " " + value)
	m.slashDismissed = ""
}

func (m Model) slashPopupView() string {
	if !m.slashActive() {
		return ""
	}
	styles := m.styles()
	outerW := min(max(40, m.width-4), 88)
	contentW := max(36, outerW-styles.popup.GetHorizontalFrameSize())
	if command, prefix, ok := m.slashArgMode(); ok {
		return m.slashArgPopupView(styles, command, prefix, contentW)
	}
	commands := m.filteredSlashCommands()
	if len(commands) == 0 {
		noMatch := styles.popupMuted.Width(contentW).Render("No slash command matches " + strconv.Quote(m.slashToken()))
		hint := styles.popupMuted.Width(contentW).Render("Esc close")
		return styles.popup.Width(contentW).Render(noMatch + "\n" + hint)
	}
	index := m.slashIndex
	if index < 0 || index >= len(commands) {
		index = 0
	}
	limit := min(len(commands), 6)
	start, end := slashPopupWindow(len(commands), index, limit)
	var lines []string
	if start > 0 {
		lines = append(lines, styles.popupMuted.Width(contentW).Render(fmt.Sprintf("%d previous matches", start)))
	}
	nameW := min(30, max(18, contentW/2-2))
	summaryW := max(12, contentW-nameW-5)
	for i := start; i < end; i++ {
		command := commands[i]
		label := command.name
		if command.args != "" {
			label += " " + command.args
		}
		line := padRight(truncateRunes(label, nameW), nameW) + "  " + truncateRunes(command.summary, summaryW)
		if i == index {
			lines = append(lines, styles.popupSelected.Width(contentW).Render(line))
		} else {
			lines = append(lines, styles.popupLine.Width(contentW).Render(line))
		}
	}
	if end < len(commands) {
		lines = append(lines, styles.popupMuted.Width(contentW).Render(fmt.Sprintf("%d more matches", len(commands)-end)))
	}
	lines = append(lines, styles.popupMuted.Width(contentW).Render("Up/Down select  Tab complete  Enter run  Esc close"))
	return styles.popup.Width(contentW).Render(strings.Join(lines, "\n"))
}

func (m Model) slashArgPopupView(styles themeStyles, command slashCommand, prefix string, contentW int) string {
	args := m.filteredSlashArgs(command, prefix)
	if len(args) == 0 {
		noMatch := styles.popupMuted.Width(contentW).Render("No argument matches " + strconv.Quote(prefix))
		hint := styles.popupMuted.Width(contentW).Render("Esc close")
		return styles.popup.Width(contentW).Render(noMatch + "\n" + hint)
	}
	index := m.slashIndex
	if index < 0 || index >= len(args) {
		index = 0
	}
	limit := min(len(args), 6)
	start, end := slashPopupWindow(len(args), index, limit)
	var lines []string
	title := styles.popupMuted.Width(contentW).Render(command.name + " argument")
	lines = append(lines, title)
	if start > 0 {
		lines = append(lines, styles.popupMuted.Width(contentW).Render(fmt.Sprintf("%d previous matches", start)))
	}
	valueW := min(28, max(14, contentW/2-2))
	summaryW := max(12, contentW-valueW-5)
	for i := start; i < end; i++ {
		arg := args[i]
		line := padRight(truncateRunes(arg.value, valueW), valueW) + "  " + truncateRunes(arg.summary, summaryW)
		if i == index {
			lines = append(lines, styles.popupSelected.Width(contentW).Render(line))
		} else {
			lines = append(lines, styles.popupLine.Width(contentW).Render(line))
		}
	}
	if end < len(args) {
		lines = append(lines, styles.popupMuted.Width(contentW).Render(fmt.Sprintf("%d more matches", len(args)-end)))
	}
	lines = append(lines, styles.popupMuted.Width(contentW).Render("Up/Down select  Tab complete  Enter run  Esc close"))
	return styles.popup.Width(contentW).Render(strings.Join(lines, "\n"))
}

func slashPopupWindow(length, index, limit int) (int, int) {
	if length <= 0 || limit <= 0 {
		return 0, 0
	}
	if index < 0 {
		index = 0
	}
	if index >= length {
		index = length - 1
	}
	limit = min(limit, length)
	start := index - limit + 1
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > length {
		end = length
		start = max(0, end-limit)
	}
	return start, end
}

func (m Model) slashPopupHeight() int {
	view := m.slashPopupView()
	if view == "" {
		return 0
	}
	return lipgloss.Height(view)
}

func (m Model) inlineStatusView() string {
	styles := m.styles()
	access := "Guarded"
	if m.dangerous || m.cfg.AutoApproveDangerous {
		access = "Full Access"
	}
	top := []statusSegment{
		{m.runStateText(), styles.statusState},
		{m.currentModel(), styles.statusModel},
		{"🧠 " + m.currentThinking().effortLabel(), styles.statusReasoning},
		{access, styles.statusAccess},
		{"Context " + m.contextText() + " used", styles.statusUsage},
		{m.costText(), styles.statusCost},
	}
	bottom := []statusSegment{
		{"cached " + compactNumber(m.cacheHitTok), styles.statusUsage},
		{"miss " + compactNumber(m.cacheMissTok), styles.statusUsage},
		{"reasoning " + compactNumber(m.reasoningTok), styles.statusReasoning},
		{compactNumber(m.usedTokens()) + " used", styles.statusUsage},
		{"tools " + strconv.Itoa(m.toolCalls), styles.statusDim},
		{"v" + m.version, styles.statusDim},
		{"theme " + m.theme, styles.statusDim},
		{"Main [" + shortID(m.localChatID) + "]", styles.statusDim},
	}
	width := max(1, m.statusContentWidth(styles))
	return renderStatusSegments(width, top, styles.statusSeparator) + "\n" +
		renderStatusSegments(width, bottom, styles.statusSeparator)
}

func (m Model) runStatusView() string {
	if !m.busy {
		return ""
	}
	styles := m.styles()
	elapsed := "0s"
	if !m.runStartedAt.IsZero() {
		elapsed = compactDuration(time.Since(m.runStartedAt))
	}
	state := m.status
	if state == "" || state == "running" {
		state = "agent working"
	}
	text := " " + m.spinner() + " " + state + " · " + elapsed
	return styles.runStatus.Width(m.statusContentWidth(styles)).Render(text)
}

func (m Model) runStateText() string {
	if !m.followOutput {
		return "scrolled"
	}
	if m.busy {
		elapsed := "0s"
		if !m.runStartedAt.IsZero() {
			elapsed = compactDuration(time.Since(m.runStartedAt))
		}
		return "running " + elapsed
	}
	if m.lastRunDuration > 0 {
		return m.status + " · last " + compactDuration(m.lastRunDuration)
	}
	return m.status
}

func (m Model) spinner() string {
	if len(spinnerFrames) == 0 {
		return "*"
	}
	return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
}

func (m Model) contextText() string {
	used := m.contextTokens()
	window := m.settings.ContextWindowTokens
	if window <= 0 {
		return compactNumber(used)
	}
	percent := float64(used) / float64(window) * 100
	if percent < 10 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
}

func (m Model) costText() string {
	if modelinfo.Lookup(m.currentModel()).Subscription {
		return "cost subscription"
	}
	hitPrice, missPrice, outputPrice := m.prices()
	if hitPrice <= 0 && missPrice <= 0 && outputPrice <= 0 {
		return "cost n/a"
	}
	hit := m.cacheHitTok
	miss := m.cacheMissTok
	if hit == 0 && miss == 0 {
		miss = m.inputTok
	}
	cost := (float64(hit)/1_000_000)*hitPrice +
		(float64(miss)/1_000_000)*missPrice +
		(float64(m.outputTok)/1_000_000)*outputPrice
	return fmt.Sprintf("cost $%.6f", cost)
}

func (m Model) prices() (hit, miss, output float64) {
	hit = m.settings.CacheHitPricePer1MTokens
	miss = m.settings.CacheMissPricePer1MTokens
	output = m.settings.OutputPricePer1MTokens
	if hit > 0 || miss > 0 || output > 0 {
		if miss == 0 {
			miss = m.settings.InputPricePer1MTokens
		}
		return hit, miss, output
	}
	if pricing := modelinfo.Lookup(m.currentModel()).Pricing; pricing.CacheHitPer1M > 0 || pricing.CacheMissPer1M > 0 || pricing.OutputPer1M > 0 {
		return pricing.CacheHitPer1M, pricing.CacheMissPer1M, pricing.OutputPer1M
	}
	return 0, m.settings.InputPricePer1MTokens, m.settings.OutputPricePer1MTokens
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
	if m.busy {
		m.status = "busy"
		m.reflow(m.followOutput)
		return *m, nil
	}
	if m.gatewayURL != "" && m.sessionID == "" {
		m.status = "gateway session not ready"
		m.reflow(m.followOutput)
		return *m, nil
	}
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
		go m.runGateway(prompt)
	} else {
		go m.runLocal(prompt)
	}
	m.reflow(true)
	return *m, tea.Batch(m.waitEventCmd(), m.tickCmd())
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

func (m Model) createSessionCmd() tea.Cmd {
	return func() tea.Msg {
		body, err := json.Marshal(map[string]any{
			"messages": m.messages,
			"profile":  m.currentProfile(),
		})
		if err != nil {
			return errMsg{err: err}
		}
		req, err := http.NewRequest(http.MethodPost, m.gatewayURL+"/v1/sessions", bytes.NewReader(body))
		if err != nil {
			return errMsg{err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return errMsg{err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return errMsg{err: fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return errMsg{err: err}
		}
		if out.ID == "" {
			return errMsg{err: fmt.Errorf("gateway returned empty session id")}
		}
		return sessionReadyMsg{id: out.ID}
	}
}

func (m Model) runLocal(prompt string) {
	cfg := m.currentConfig()
	prov, err := provider.New(cfg)
	if err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	defer registry.Close()
	a := agent.New(cfg, prov, registry)
	msgs := append([]protocol.Message(nil), m.messages...)
	msgs = append(msgs, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	next, err := a.RunMessages(context.Background(), msgs, func(event protocol.Event) {
		m.events <- streamEventMsg{event: event}
	})
	m.events <- runDoneMsg{messages: next, err: err}
}

func (m Model) runGateway(prompt string) {
	body, _ := json.Marshal(map[string]any{
		"prompt":           prompt,
		"provider":         m.currentProvider(),
		"model":            m.currentModel(),
		"profile":          m.currentProfile(),
		"thinking":         m.currentThinking().kind,
		"reasoning_effort": m.currentThinking().effort,
		"max_tool_rounds":  m.maxRounds,
	})
	path := fmt.Sprintf("%s/v1/sessions/%s/run", m.gatewayURL, m.sessionID)
	req, err := http.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		m.events <- runDoneMsg{err: fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))}
		return
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var runErr error
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			m.events <- runDoneMsg{err: err}
			return
		}
		m.events <- streamEventMsg{event: event}
		if event.Type == protocol.EventRunFailed {
			runErr = fmt.Errorf("%v", event.Data)
		}
	}
	if err := scanner.Err(); err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	if runErr != nil {
		m.events <- runDoneMsg{err: runErr}
		return
	}
	messages, err := m.fetchGatewayMessages()
	if err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	m.events <- runDoneMsg{messages: messages}
}

func (m Model) fetchGatewayMessages() ([]protocol.Message, error) {
	path := fmt.Sprintf("%s/v1/sessions/%s", m.gatewayURL, m.sessionID)
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	var out struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

type mcpStatusResponse struct {
	ConfigFiles []string                 `json:"config_files"`
	Allowed     []string                 `json:"allowed"`
	Enabled     bool                     `json:"enabled"`
	Servers     []mcpclient.ServerStatus `json:"servers"`
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
	cfg := m.currentConfig()
	if err := cfg.LoadDefaultMCPServers(); err != nil {
		return "", err
	}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		return "", err
	}
	defer registry.Close()
	cfg = registry.Config()
	return formatMCPStatus(mcpStatusResponse{
		ConfigFiles: cfg.MCPConfigFiles,
		Allowed:     cfg.MCPAllowedServers,
		Enabled:     cfg.MCPEnabled,
		Servers:     registry.MCPStatuses(),
	}), nil
}

func (m Model) fetchGatewayMCPStatus() (mcpStatusResponse, error) {
	path := fmt.Sprintf("%s/v1/mcp", m.gatewayURL)
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return mcpStatusResponse{}, err
	}
	client := http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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
	configFiles := strings.Join(status.ConfigFiles, ", ")
	if configFiles == "" {
		configFiles = "(none)"
	}
	allowed := strings.Join(status.Allowed, ", ")
	if allowed == "" {
		allowed = "(all)"
	}
	lines := []string{
		"config: " + configFiles,
		"allowed: " + allowed,
		"native: web_search, web_fetch, web_extract, web_crawl",
	}
	if !status.Enabled {
		lines = append(lines, "mcp: disabled")
		return strings.Join(lines, "\n")
	}
	if len(status.Servers) == 0 {
		lines = append(lines, "servers: none connected")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "")
	for _, server := range status.Servers {
		state := "disabled"
		if server.Enabled && server.Connected {
			state = "connected"
		} else if server.Enabled && server.Error != "" {
			state = "error"
		} else if server.Enabled {
			state = "not connected"
		}
		line := fmt.Sprintf("%-10s %-10s %s tools:%d", server.Name, state, server.Transport, server.ToolCount)
		if server.Required {
			line += " required"
		}
		if server.PID > 0 {
			line += fmt.Sprintf(" pid:%d", server.PID)
		}
		if server.StartedAt != nil && !server.StartedAt.IsZero() {
			line += " started:" + server.StartedAt.Local().Format("15:04:05")
		}
		if server.Error != "" {
			line += "\n  " + oneLinePreview(server.Error, 180)
		}
		if server.LastError != "" && server.LastError != server.Error {
			line += "\n  last: " + oneLinePreview(server.LastError, 180)
		}
		if server.LastErrorAt != nil && !server.LastErrorAt.IsZero() {
			line += "\n  last_error_at: " + server.LastErrorAt.Local().Format("2006-01-02 15:04:05")
		}
		if server.StderrTail != "" {
			line += "\n  stderr: " + oneLinePreview(server.StderrTail, 180)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

type authStatusResponse = credentials.Status

func (m *Model) handleAuthCommand(arg string) (bool, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "", "deepseek", "api", "key", "deepseek api key":
		m.authInputProvider = "deepseek"
		m.textarea.Placeholder = "Paste DeepSeek API key. Enter saves, Esc cancels."
		m.textarea.SetValue("")
		m.status = "paste DeepSeek API key"
		return true, nil
	case "codex", "oauth", "chatgpt", "codex oauth":
		m.status = "importing Codex OAuth"
		return true, m.authCodexImportCmd()
	case "status", "list":
		m.status = "loading auth status"
		return true, m.authStatusCmd()
	default:
		m.status = "unknown auth action " + arg
		return false, nil
	}
}

func (m *Model) cancelAuthInput() {
	m.authInputProvider = ""
	m.textarea.Placeholder = defaultTextareaPlaceholder
}

func (m Model) authSaveCmd(providerName, secret string) tea.Cmd {
	return func() tea.Msg {
		var (
			text string
			err  error
		)
		switch providerName {
		case "deepseek":
			text, err = m.saveDeepSeekCredential(secret)
		default:
			err = fmt.Errorf("unknown auth provider %s", providerName)
		}
		return authResultMsg{text: text, err: err}
	}
}

func (m Model) authCodexImportCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := m.importCodexCredential()
		return authResultMsg{text: text, err: err}
	}
}

func (m Model) authStatusCmd() tea.Cmd {
	return func() tea.Msg {
		status, err := m.loadAuthStatus()
		if err != nil {
			return authResultMsg{err: err}
		}
		return authResultMsg{text: formatAuthStatus(status)}
	}
}

func (m Model) saveDeepSeekCredential(apiKey string) (string, error) {
	if m.gatewayURL == "" {
		status, err := credentials.SaveDeepSeekAPIKey(apiKey)
		if err != nil {
			return "", err
		}
		return "DeepSeek API key saved\n" + formatProviderStatus("deepseek", status), nil
	}
	body, _ := json.Marshal(map[string]string{"api_key": apiKey})
	var out struct {
		DeepSeek credentials.ProviderStatus `json:"deepseek"`
	}
	if err := m.gatewayJSON(http.MethodPost, "/v1/auth/deepseek", body, &out); err != nil {
		return "", err
	}
	return "DeepSeek API key saved\n" + formatProviderStatus("deepseek", out.DeepSeek), nil
}

func (m Model) importCodexCredential() (string, error) {
	if m.gatewayURL == "" {
		status, err := credentials.ImportCodexAuth(m.currentConfig(), "")
		if err != nil {
			return "", err
		}
		return "Codex OAuth imported\n" + formatProviderStatus("codex", status), nil
	}
	var out struct {
		Codex credentials.ProviderStatus `json:"codex"`
	}
	if err := m.gatewayJSON(http.MethodPost, "/v1/auth/codex/import", []byte(`{}`), &out); err != nil {
		return "", err
	}
	return "Codex OAuth imported\n" + formatProviderStatus("codex", out.Codex), nil
}

func (m Model) loadAuthStatus() (authStatusResponse, error) {
	if m.gatewayURL == "" {
		return credentials.CurrentStatus(m.currentConfig()), nil
	}
	var out authStatusResponse
	if err := m.gatewayJSON(http.MethodGet, "/v1/auth/status", nil, &out); err != nil {
		return authStatusResponse{}, err
	}
	return out, nil
}

func (m Model) gatewayJSON(method, path string, body []byte, out any) error {
	req, err := http.NewRequest(method, m.gatewayURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func formatAuthStatus(status credentials.Status) string {
	return strings.Join([]string{
		formatProviderStatus("deepseek", status.DeepSeek),
		formatProviderStatus("codex", status.Codex),
	}, "\n")
}

func formatProviderStatus(name string, status credentials.ProviderStatus) string {
	state := "missing"
	if status.Configured {
		state = "configured"
	}
	parts := []string{name + ": " + state}
	if status.Mode != "" {
		parts = append(parts, "mode "+status.Mode)
	}
	if status.AccountID != "" {
		parts = append(parts, "account "+status.AccountID)
	}
	if status.ExpiresAt != "" {
		parts = append(parts, "expires "+status.ExpiresAt)
	}
	if status.Path != "" {
		parts = append(parts, "path "+status.Path)
	}
	if status.Source != "" && status.Source != status.Path {
		parts = append(parts, "source "+status.Source)
	}
	return strings.Join(parts, "\n  ")
}

func (m Model) currentConfig() config.Config {
	cfg := m.cfg
	cfg.Model = m.currentModel()
	cfg.Provider = m.currentProvider()
	cfg.Profile = m.currentProfile()
	cfg.Thinking = m.currentThinking().kind
	cfg.ReasoningEffort = m.currentThinking().effort
	cfg.StoreReasoningContent = true
	cfg.AutoApproveDangerous = cfg.AutoApproveDangerous || m.dangerous
	cfg.MaxToolRounds = m.maxRounds
	cfg.ApplyModelProviderDefaults()
	return cfg
}

func (m Model) currentModel() string {
	return m.models[m.modelIndex]
}

func (m Model) currentProvider() string {
	return modelinfo.ProviderForModel(m.currentModel(), m.cfg.Provider)
}

func (m Model) currentProfile() string {
	return config.NormalizeProfileName(m.cfg.Profile)
}

func (m Model) currentThinking() thinkingMode {
	return m.thinking[m.thinkingIdx]
}

func (m *Model) handleSlashCommand(prompt string) (bool, tea.Cmd) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 {
		return true, nil
	}
	command := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.ToLower(strings.Join(fields[1:], " "))
	}
	switch command {
	case "/auth":
		return m.handleAuthCommand(arg)
	case "/theme":
		return m.setTheme(arg), nil
	case "/model":
		return m.setModel(arg), nil
	case "/models":
		m.addInfoBlock("MODELS", m.modelsText())
		m.status = "models shown"
		return true, nil
	case "/profile":
		return true, m.setProfile(arg)
	case "/mcp":
		m.status = "loading mcp status"
		return true, m.mcpStatusCmd()
	case "/reasoning":
		return m.setReasoning(arg), nil
	case "/thinking", "/show-reasoning", "/show_reasoning":
		return m.setThinkingDisplay(arg), nil
	case "/toolview":
		return m.setToolView(arg), nil
	case "/thinkview":
		return m.setThinkView(arg), nil
	case "/new":
		return true, m.newChat()
	case "/resume":
		return true, m.resumeChat(arg)
	case "/fork":
		return true, m.forkChat(arg)
	case "/exit", "/quit":
		_ = m.saveCurrentSession()
		_ = m.saveSettings()
		return true, tea.Quit
	case "/status":
		m.addInfoBlock("STATUS", m.statusText())
		m.status = "status shown"
		return true, nil
	case "/help":
		m.addInfoBlock("HELP", helpText())
		m.status = "help shown"
		return true, nil
	default:
		m.status = "unknown command " + fields[0]
		return false, nil
	}
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
	m.localChatID = newChatID()
	m.chatTitle = "new chat"
	m.chatCreated = time.Now().UTC()
	m.messages = agent.InitialMessages(m.currentConfig())
	m.blocks = nil
	m.collapsed = map[int]bool{}
	m.selected = 0
	m.modelCalls = 0
	m.toolCalls = 0
	m.inputTok = 0
	m.outputTok = 0
	m.cacheHitTok = 0
	m.cacheMissTok = 0
	m.reasoningTok = 0
	m.lastInputTok = 0
	m.lastOutputTok = 0
	m.lastCacheHitTok = 0
	m.lastCacheMissTok = 0
	m.sessionID = ""
	m.followOutput = true
	m.status = "profile " + m.currentProfile() + "; new chat"
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) cycleReasoning() {
	m.thinkingIdx = (m.thinkingIdx + 1) % len(m.thinking)
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
			value = "expanded"
		case "expanded":
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
	}
	if !validViewMode(value, []string{"auto", "expanded", "collapsed", "hidden"}) {
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

func (m *Model) newChat() tea.Cmd {
	if m.busy {
		m.status = "busy"
		return nil
	}
	_ = m.saveCurrentSession()
	m.localChatID = newChatID()
	m.chatTitle = "new chat"
	m.chatCreated = time.Now().UTC()
	m.messages = agent.InitialMessages(m.currentConfig())
	m.blocks = nil
	m.collapsed = map[int]bool{}
	m.selected = 0
	m.modelCalls = 0
	m.toolCalls = 0
	m.inputTok = 0
	m.outputTok = 0
	m.cacheHitTok = 0
	m.cacheMissTok = 0
	m.reasoningTok = 0
	m.lastInputTok = 0
	m.lastOutputTok = 0
	m.lastCacheHitTok = 0
	m.lastCacheMissTok = 0
	m.usageAccounting.Reset()
	m.status = "new chat " + shortID(m.localChatID)
	m.followOutput = true
	m.sessionID = ""
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) resumeChat(prefix string) tea.Cmd {
	if m.busy {
		m.status = "busy"
		return nil
	}
	prefix = strings.TrimSpace(strings.ToLower(prefix))
	if prefix == "list" || prefix == "all" {
		prefix = ""
	}
	if strings.TrimSpace(prefix) == "" {
		m.addInfoBlock("CHATS", m.sessionsText())
		m.status = "chats listed"
		return nil
	}
	session, err := loadChatSession(m.sessionsDir, prefix)
	if err != nil {
		m.status = err.Error()
		m.addBlock("error", "ERROR", err.Error())
		return nil
	}
	m.applyChatSession(session)
	m.status = "resumed " + shortID(m.localChatID)
	if m.gatewayURL != "" {
		m.sessionID = ""
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) forkChat(prefix string) tea.Cmd {
	if m.busy {
		m.status = "busy"
		return nil
	}
	prefix = strings.TrimSpace(strings.ToLower(prefix))
	if prefix == "current" {
		prefix = ""
	}
	if strings.TrimSpace(prefix) != "" {
		session, err := loadChatSession(m.sessionsDir, prefix)
		if err != nil {
			m.status = err.Error()
			m.addBlock("error", "ERROR", err.Error())
			return nil
		}
		m.applyChatSession(session)
	}
	old := m.localChatID
	m.localChatID = newChatID()
	m.chatTitle = "fork of " + shortID(old)
	m.chatCreated = time.Now().UTC()
	m.sessionID = ""
	m.status = "forked " + shortID(old) + " -> " + shortID(m.localChatID)
	m.addInfoBlock("FORK", m.status)
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) applyChatSession(session chatSession) {
	m.localChatID = session.ID
	m.chatTitle = session.Title
	if m.chatTitle == "" {
		m.chatTitle = "untitled"
	}
	m.chatCreated = session.CreatedAt
	if m.chatCreated.IsZero() {
		m.chatCreated = time.Now().UTC()
	}
	if session.Profile != "" {
		m.cfg.Profile = config.NormalizeProfileName(session.Profile)
	}
	if len(session.Messages) > 0 {
		m.messages = append([]protocol.Message(nil), session.Messages...)
	} else {
		m.messages = agent.InitialMessages(m.currentConfig())
	}
	m.blocks = decodeBlocks(session.Blocks)
	m.ensureBlockMetadata()
	m.collapsed = map[int]bool{}
	m.selected = max(0, len(m.blocks)-1)
	m.inputTok = session.InputTokens
	m.outputTok = session.OutputTokens
	m.cacheHitTok = session.CacheHitTokens
	m.cacheMissTok = session.CacheMissTokens
	m.reasoningTok = session.ReasoningTokens
	m.lastInputTok = 0
	m.lastOutputTok = 0
	m.lastCacheHitTok = 0
	m.lastCacheMissTok = 0
	m.usageAccounting.Reset()
	m.toolCalls = session.ToolCalls
	m.modelCalls = session.ModelCalls
	if session.Model != "" {
		m.models = appendIfMissing(m.models, session.Model)
		for i, model := range m.models {
			if model == session.Model {
				m.modelIndex = i
				break
			}
		}
	}
	for i, mode := range m.thinking {
		if mode.kind == session.ReasoningKind && mode.effort == session.ReasoningEffort {
			m.thinkingIdx = i
			break
		}
	}
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	m.reflow(true)
}

func (m Model) sessionsText() string {
	sessions, err := listChatSessions(m.sessionsDir)
	if err != nil {
		return err.Error()
	}
	if len(sessions) == 0 {
		return "no saved chats"
	}
	var lines []string
	for i, session := range sessions {
		if i >= 20 {
			lines = append(lines, fmt.Sprintf("... %d more", len(sessions)-i))
			break
		}
		title := session.Title
		if title == "" {
			title = "untitled"
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  tok:%d/%d tools:%d",
			shortID(session.ID),
			session.UpdatedAt.Local().Format("2006-01-02 15:04"),
			title,
			session.InputTokens,
			session.OutputTokens,
			session.ToolCalls,
		))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) saveSettings() error {
	if m.settingsPath == "" {
		return nil
	}
	m.settings.Theme = m.theme
	m.settings.ToolView = m.toolView
	m.settings.ThinkView = m.thinkView
	m.settings.LastLocalChatID = m.localChatID
	m.settings.LastGatewaySessionID = m.sessionID
	m.settings.LastSelectedModel = m.currentModel()
	m.settings.LastProfile = m.currentProfile()
	m.settings.LastReasoningKind = m.currentThinking().kind
	m.settings.LastReasoningEffort = m.currentThinking().effort
	return saveAppSettings(m.settingsPath, m.settings)
}

func (m *Model) saveCurrentSession() error {
	if m.sessionsDir == "" || m.localChatID == "" {
		return nil
	}
	created := m.chatCreated
	if created.IsZero() {
		created = time.Now().UTC()
	}
	title := m.chatTitle
	if title == "" {
		title = "untitled"
	}
	return saveChatSession(m.sessionsDir, chatSession{
		ID:               m.localChatID,
		Title:            title,
		CreatedAt:        created,
		UpdatedAt:        time.Now().UTC(),
		Model:            m.currentModel(),
		Profile:          m.currentProfile(),
		ReasoningKind:    m.currentThinking().kind,
		ReasoningEffort:  m.currentThinking().effort,
		GatewaySessionID: m.sessionID,
		Messages:         append([]protocol.Message(nil), m.messages...),
		Blocks:           encodeBlocks(m.blocks),
		InputTokens:      m.inputTok,
		OutputTokens:     m.outputTok,
		CacheHitTokens:   m.cacheHitTok,
		CacheMissTokens:  m.cacheMissTok,
		ReasoningTokens:  m.reasoningTok,
		ToolCalls:        m.toolCalls,
		ModelCalls:       m.modelCalls,
	})
}

func encodeBlocks(blocks []block) []savedBlock {
	out := make([]savedBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, savedBlock{Kind: b.kind, Title: b.title, Content: b.content})
	}
	return out
}

func decodeBlocks(blocks []savedBlock) []block {
	out := make([]block, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, block{kind: b.Kind, title: b.Title, content: b.Content})
	}
	return out
}

func (m *Model) newBlock(kind, title, content string) block {
	now := time.Now().UTC()
	m.nextBlockSeq++
	return block{
		id:      fmt.Sprintf("%s-%d", normalizeBlockKind(kind), m.nextBlockSeq),
		kind:    normalizeBlockKind(kind),
		title:   title,
		content: content,
		started: now,
		updated: now,
	}
}

func (m *Model) ensureBlockMetadata() {
	now := time.Now().UTC()
	for i := range m.blocks {
		m.blocks[i].kind = normalizeBlockKind(m.blocks[i].kind)
		if m.blocks[i].id == "" {
			m.nextBlockSeq++
			m.blocks[i].id = fmt.Sprintf("%s-%d", m.blocks[i].kind, m.nextBlockSeq)
		}
		if m.blocks[i].started.IsZero() {
			m.blocks[i].started = now
		}
		if m.blocks[i].updated.IsZero() {
			m.blocks[i].updated = m.blocks[i].started
		}
	}
}

func normalizeBlockKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "user", "assistant", "reasoning", "tool", "error", "status", "audit":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return "status"
	}
}

func blockKindForEvent(eventType protocol.EventType) string {
	switch eventType {
	case protocol.EventAssistantDelta:
		return "assistant"
	case protocol.EventAssistantReasoning:
		return "reasoning"
	case protocol.EventToolCallRequested, protocol.EventToolCallStarted, protocol.EventToolCallFinished:
		return "tool"
	case protocol.EventToolAudit:
		return "audit"
	case protocol.EventRunFailed:
		return "error"
	default:
		return "status"
	}
}

func (m *Model) addInfoBlock(title, content string) {
	m.followOutput = true
	m.addBlock("status", title, content)
}

func (m Model) statusText() string {
	mode := "local"
	gateway := "none"
	session := "none"
	if m.gatewayURL != "" {
		mode = "gateway"
		gateway = m.gatewayURL
		if m.sessionID != "" {
			session = m.sessionID
		}
	}
	toolsMode := "safe"
	if m.dangerous {
		toolsMode = "dangerous"
	}
	thinkingDisplay := "hidden"
	if m.thinkView != "hidden" {
		thinkingDisplay = m.thinkView
	}
	follow := "off"
	if m.followOutput {
		follow = "on"
	}
	return fmt.Sprintf(
		"mode: %s\nchat: %s\nprovider: %s\nmodel: %s\nprofile: %s\nreasoning: %s / %s\nthinking blocks: %s\ntool blocks: %s\ntheme: %s\ngateway: %s\ngateway session: %s\nlocal settings: %s\ntools: %s, max rounds %d\ncalls: model %d, tools %d\ntokens: input %d, output %d\ncontext: %s\ncost: %s\nfollow output: %s",
		mode,
		m.localChatID,
		m.currentProvider(),
		m.currentModel(),
		m.currentProfile(),
		m.currentThinking().kind,
		m.currentThinking().effort,
		thinkingDisplay,
		m.toolView,
		m.theme,
		gateway,
		session,
		m.settingsPath,
		toolsMode,
		m.maxRounds,
		m.modelCalls,
		m.toolCalls,
		m.inputTok,
		m.outputTok,
		m.contextText(),
		m.costText(),
		follow,
	)
}

func (m Model) modelsText() string {
	var lines []string
	for i, model := range m.models {
		marker := " "
		if i == m.modelIndex {
			marker = "*"
		}
		provider := "deepseek"
		if isCodexModel(model) {
			provider = "openai-codex"
		}
		lines = append(lines, fmt.Sprintf("%s %-24s %s", marker, model, provider))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) applyEvent(event protocol.Event) {
	switch event.Type {
	case protocol.EventRunStarted:
		m.finishLiveBlocks()
		m.status = "run started"
		m.usageAccounting.Reset()
	case protocol.EventModelCallStarted:
		m.modelCalls++
		m.status = fmt.Sprintf("model call %d", m.modelCalls)
		m.usageAccounting.Reset()
	case protocol.EventAssistantReasoning:
		m.appendToOpenBlock("reasoning", "THINKING", fmt.Sprint(event.Data), event.Type)
	case protocol.EventAssistantDelta:
		m.appendToOpenBlock("assistant", "ASSISTANT", fmt.Sprint(event.Data), event.Type)
	case protocol.EventToolAudit:
		m.status = "tool audit " + auditToolName(event.Data)
		m.appendToolAudit(auditEventText(event.Data))
	case protocol.EventToolCallRequested:
		m.toolCalls++
		m.status = "running tool " + toolName(event.Data)
		m.addEventBlock(event.Type, toolTitle(event.Data), toolBody(event.Data))
	case protocol.EventToolCallFinished:
		m.appendToolResult(toolResultText(event.Data))
		m.collapseLastToolBlockIfLarge()
	case protocol.EventContextCompacted:
		m.status = "context compacted"
		m.addInfoBlock("COMPACT", compactEventText(event.Data))
	case protocol.EventProviderUsageUpdate:
		update := usageFromAny(event.Data)
		delta := m.usageAccounting.Apply(update)
		current := m.usageAccounting.Current()
		m.inputTok += delta.InputTokens
		m.outputTok += delta.OutputTokens
		m.cacheHitTok += delta.CacheHitTokens
		m.cacheMissTok += delta.CacheMissTokens
		m.reasoningTok += delta.ReasoningTokens
		m.lastInputTok = current.InputTokens
		m.lastOutputTok = current.OutputTokens
		m.lastCacheHitTok = current.CacheHitTokens
		m.lastCacheMissTok = current.CacheMissTokens
	case protocol.EventRunCompleted:
		m.finishLiveBlocks()
		m.status = "completed"
	case protocol.EventRunFailed:
		m.finishLiveBlocks()
		m.addEventBlock(event.Type, "ERROR", fmt.Sprint(event.Data))
		m.status = "failed"
	}
}

func toolResultText(value any) string {
	bytes, _ := json.Marshal(value)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err == nil && (result.Content != "" || result.Name != "" || result.CallID != "") {
		if result.Content != "" {
			return result.Content
		}
		return fmt.Sprint(value)
	}
	return fmt.Sprint(value)
}

func (m *Model) appendToOpenBlock(kind, title, text string, eventType protocol.EventType) {
	kind = normalizeBlockKind(kind)
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != kind {
		m.addEventBlock(eventType, title, "")
	}
	i := len(m.blocks) - 1
	m.blocks[i].content += text
	m.blocks[i].live = kind == "assistant" || kind == "reasoning"
	m.blocks[i].eventType = eventType
	m.blocks[i].updated = time.Now().UTC()
	m.selected = i
}

func (m *Model) appendToolResult(text string) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		text = "[no output]"
	}
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != "tool" {
		m.addBlock("tool", "Called tool", text)
		return
	}
	i := len(m.blocks) - 1
	content := strings.TrimRight(m.blocks[i].content, "\n")
	if strings.TrimSpace(content) == "" {
		m.blocks[i].content = text
	} else {
		m.blocks[i].content = content + "\n" + text
	}
	m.blocks[i].updated = time.Now().UTC()
	m.selected = i
}

func (m *Model) appendToolAudit(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != "tool" {
		m.addEventBlock(protocol.EventToolAudit, "AUDIT", text)
		return
	}
	i := len(m.blocks) - 1
	if strings.TrimSpace(m.blocks[i].content) == "" {
		m.blocks[i].content = "audit: " + text
	} else {
		m.blocks[i].content = strings.TrimRight(m.blocks[i].content, "\n") + "\naudit: " + text
	}
	m.blocks[i].updated = time.Now().UTC()
	m.selected = i
}

func (m *Model) collapseLastToolBlockIfLarge() {
	if len(m.blocks) == 0 {
		return
	}
	i := len(m.blocks) - 1
	if m.blocks[i].kind != "tool" {
		return
	}
	if len(m.blocks[i].content) > 8000 || strings.Count(m.blocks[i].content, "\n") > 40 {
		m.collapsed[i] = true
	}
}

func (m *Model) addBlock(kind, title, content string) {
	m.blocks = append(m.blocks, m.newBlock(kind, title, content))
	m.selected = len(m.blocks) - 1
}

func (m *Model) addEventBlock(eventType protocol.EventType, title, content string) {
	b := m.newBlock(blockKindForEvent(eventType), title, content)
	b.eventType = eventType
	b.live = b.kind == "assistant" || b.kind == "reasoning"
	m.blocks = append(m.blocks, b)
	m.selected = len(m.blocks) - 1
}

func (m *Model) finishLiveBlocks() {
	now := time.Now().UTC()
	for i := range m.blocks {
		if m.blocks[i].live {
			m.blocks[i].live = false
			m.blocks[i].updated = now
		}
	}
}

func (m *Model) reflow(gotoBottom bool) {
	var parts []string
	for i, b := range m.blocks {
		if b.kind == "reasoning" && m.thinkView == "hidden" {
			continue
		}
		if b.kind == "tool" && m.toolView == "hidden" {
			continue
		}
		parts = append(parts, m.renderBlock(i, b))
	}
	m.viewportContent = strings.Join(parts, "\n")
	m.viewport.SetContent(m.viewportContent)
	if m.hasSelection() {
		m.applySelectionHighlight()
	}
	if gotoBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) renderBlock(i int, b block) string {
	styles := m.styles()
	style := styles.block
	switch b.kind {
	case "user":
		style = styles.user
	case "assistant":
		style = styles.assistant
	case "reasoning":
		style = styles.reasoning
	case "tool":
		style = styles.tool
	case "error":
		style = styles.error
	case "status":
		style = styles.statusBlock
	case "audit":
		style = styles.statusBlock
	}
	body := strings.TrimRight(b.content, "\n")
	switch {
	case b.kind == "tool" && m.toolCollapsed(i):
		body = ""
	case b.kind == "tool" && m.toolView == "auto" && m.collapsed[i]:
		body = collapsedPreview(b.content, 8, 1000)
	case b.kind == "reasoning" && m.thinkView == "collapsed":
		body = collapsedSummary(b.content)
	case m.collapsed[i]:
		body = collapsedPreview(b.content, 8, 1000)
	}
	width := max(20, m.width-style.GetHorizontalFrameSize())
	if b.kind == "assistant" {
		body = renderAssistantBody(body, width, styles, b.live)
	}
	if b.kind == "user" || b.kind == "assistant" {
		return style.Width(width).Render(body)
	}
	return renderActivityBlock(b, body, width, styles)
}

func (m Model) toolCollapsed(i int) bool {
	if i < 0 || i >= len(m.blocks) || m.blocks[i].kind != "tool" {
		return false
	}
	switch m.toolView {
	case "collapsed":
		collapsed, ok := m.collapsed[i]
		if !ok {
			return true
		}
		return collapsed
	case "hidden":
		return true
	default:
		return false
	}
}

func (m *Model) toggleSelectedBlock() {
	if m.selected < 0 || m.selected >= len(m.blocks) {
		return
	}
	if m.blocks[m.selected].kind == "tool" && m.toolView == "collapsed" {
		m.collapsed[m.selected] = !m.toolCollapsed(m.selected)
		return
	}
	m.collapsed[m.selected] = !m.collapsed[m.selected]
}

func blockTitle(b block) string {
	label := strings.ToLower(b.title)
	switch b.kind {
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	case "reasoning":
		return "thinking"
	case "tool":
		return strings.ToLower(oneLinePreview(b.title, 72))
	case "error":
		return "error"
	case "status":
		return strings.ToLower(oneLinePreview(b.title, 72))
	case "audit":
		return strings.ToLower(oneLinePreview(b.title, 72))
	default:
		return label
	}
}

func renderAssistantBody(body string, width int, styles themeStyles, live bool) string {
	if !live {
		return renderTerminalMarkdown(body, width, styles)
	}
	stable, tail := splitLiveMarkdown(body)
	switch {
	case stable == "":
		return body
	case tail == "":
		return renderTerminalMarkdown(stable, width, styles)
	default:
		rendered := strings.TrimRight(renderTerminalMarkdown(stable, width, styles), "\n")
		if rendered == "" {
			return tail
		}
		return rendered + "\n" + tail
	}
}

func splitLiveMarkdown(body string) (stable, tail string) {
	if body == "" || markdownFenceOpen(body) {
		return "", body
	}
	idx := strings.LastIndex(body, "\n")
	if idx < 0 {
		return "", body
	}
	return body[:idx+1], body[idx+1:]
}

func markdownFenceOpen(body string) bool {
	open := false
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			open = !open
		}
	}
	return open
}

func renderActivityBlock(b block, body string, width int, styles themeStyles) string {
	titleStyle := styles.activityStatus
	switch b.kind {
	case "tool":
		titleStyle = styles.activityTool
	case "reasoning":
		titleStyle = styles.activityReasoning
	case "error":
		titleStyle = styles.activityError
	case "status":
		titleStyle = styles.activityStatus
	case "audit":
		titleStyle = styles.activityStatus
	}
	header := titleStyle.Render("• " + activityTitle(b))
	body = strings.Trim(body, "\n")
	if body == "" {
		return header
	}
	return header + "\n" + renderActivityBody(body, max(12, width-2), styles)
}

func activityTitle(b block) string {
	title := strings.TrimSpace(b.title)
	switch b.kind {
	case "reasoning":
		return "Thinking"
	case "tool":
		if title == "" || strings.EqualFold(title, "TOOL") {
			return "Called tool"
		}
		return title
	case "error":
		if title == "" || strings.EqualFold(title, "ERROR") {
			return "Error"
		}
		return title
	case "status":
		if title == "" {
			return "Status"
		}
		return titleCase(title)
	case "audit":
		if title == "" || strings.EqualFold(title, "AUDIT") {
			return "Tool audit"
		}
		return titleCase(title)
	default:
		return blockTitle(b)
	}
}

func renderActivityBody(body string, width int, styles themeStyles) string {
	lines := trimEmptyLines(strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
	if len(lines) == 0 {
		return ""
	}
	var out []string
	first := true
	contentW := max(8, width-4)
	for _, line := range lines {
		wrapped := wrapActivityLine(line, contentW)
		for _, part := range wrapped {
			prefix := "  │ "
			if first {
				prefix = "  └ "
				first = false
			}
			out = append(out, styles.activityGuide.Render(prefix)+styles.activityText.Render(part))
		}
	}
	return strings.Join(out, "\n")
}

func trimEmptyLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func wrapActivityLine(line string, width int) []string {
	width = max(1, width)
	if line == "" {
		return []string{""}
	}
	var out []string
	rest := line
	for xansi.StringWidth(rest) > width {
		part := xansi.Cut(rest, 0, width)
		if part == "" {
			break
		}
		out = append(out, part)
		rest = xansi.Cut(rest, width, xansi.StringWidth(rest))
	}
	out = append(out, rest)
	return out
}

func titleCase(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func (m Model) styles() themeStyles {
	theme, ok := tuiThemes[m.theme]
	if !ok {
		theme = tuiThemes["light"]
	}
	return newThemeStyles(theme)
}

func appendIfMissing(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

func pretty(value any) string {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(bytes)
}

func toolName(value any) string {
	return toolrender.CallName(value)
}

func toolTitle(value any) string {
	_, line := toolrender.CallKeyAndLine(value, toolrender.StyleTUI)
	if strings.TrimSpace(line) != "" {
		return line
	}
	return "Called " + toolName(value)
}

func toolBody(value any) string {
	args := toolrender.CallArgs(value)
	if len(args) == 0 {
		return ""
	}
	switch toolName(value) {
	case "shell_exec":
		return toolMetaLines(args, "cwd", "timeout_sec")
	case "fs_read_file", "fs_list", "fs_search", "fs_make_dir", "web_fetch", "web_extract", "web_search", "web_crawl", "time_now":
		return ""
	case "fs_write_file":
		return toolMetaLines(args, "append", "create_dirs")
	default:
		return pretty(args)
	}
}

func auditToolName(value any) string {
	fields := mapFromAny(value)
	if name := stringField(fields, "name"); name != "" {
		return name
	}
	return "tool"
}

func auditEventText(value any) string {
	fields := mapFromAny(value)
	name := stringField(fields, "name")
	if name == "" {
		name = "tool"
	}
	risk := stringField(fields, "risk")
	if risk == "" {
		risk = "unknown risk"
	}
	decision := "approval required"
	if boolField(fields, "auto_approved") {
		decision = "auto-approved"
	}
	return fmt.Sprintf("%s %s %s", risk, name, decision)
}

func mapFromAny(value any) map[string]any {
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(bytes, &fields); err != nil {
		return nil
	}
	return fields
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	switch value := fields[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func boolField(fields map[string]any, key string) bool {
	if fields == nil {
		return false
	}
	switch value := fields[key].(type) {
	case bool:
		return value
	case string:
		return value == "true" || value == "1" || value == "yes"
	default:
		return false
	}
}

func toolMetaLines(args map[string]any, keys ...string) string {
	var lines []string
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed == "" || (key == "cwd" && typed == ".") {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %s", key, typed))
		case bool:
			lines = append(lines, fmt.Sprintf("%s: %t", key, typed))
		case float64:
			if typed == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %.0f", key, typed))
		default:
			lines = append(lines, fmt.Sprintf("%s: %v", key, typed))
		}
	}
	return strings.Join(lines, "\n")
}

func collapsedPreview(text string, maxLines, maxChars int) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return "[collapsed: empty]"
	}
	lines := strings.Split(trimmed, "\n")
	limited := lines
	if len(limited) > maxLines {
		limited = limited[:maxLines]
	}
	preview := strings.Join(limited, "\n")
	preview = truncateRunes(preview, maxChars)
	more := len(lines) > len(limited) || len(trimmed) > len(preview)
	if more {
		preview += "\n..."
	}
	return fmt.Sprintf("[collapsed: %d chars, Ctrl+E expand]\n%s", len(text), preview)
}

func collapsedSummary(text string) string {
	return fmt.Sprintf("[collapsed: %d chars, Ctrl+E expand]", len(text))
}

func oneLinePreview(text string, maxChars int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}

func truncateRunes(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars])
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func shortModel(model string) string {
	model = strings.TrimPrefix(model, "deepseek-")
	model = strings.TrimPrefix(model, "deepseek/")
	model = strings.TrimPrefix(model, "gpt-")
	if strings.HasPrefix(model, "v4-") {
		return model
	}
	return truncateRunes(model, 18)
}

func isCodexModel(model string) bool {
	return modelinfo.IsCodexModel(model)
}

func padRight(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-lipgloss.Width(text))
}

func fitSegments(width int, sep string, segments ...string) string {
	var clean []string
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			clean = append(clean, segment)
		}
	}
	if len(clean) == 0 || width <= 0 {
		return ""
	}
	for keep := len(clean); keep > 0; keep-- {
		candidate := strings.Join(clean[:keep], sep)
		if keep < len(clean) {
			candidate += sep + "..."
		}
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return truncateRunes(clean[0], width)
}

func renderStatusSegments(width int, segments []statusSegment, separator lipgloss.Style) string {
	var clean []statusSegment
	for _, segment := range segments {
		segment.text = strings.TrimSpace(segment.text)
		if segment.text != "" {
			clean = append(clean, segment)
		}
	}
	if width <= 0 || len(clean) == 0 {
		return ""
	}
	sep := separator.Render(" · ")
	for keep := len(clean); keep > 0; keep-- {
		rendered := renderStatusParts(clean[:keep], sep)
		if keep < len(clean) {
			rendered += sep + separator.Render("...")
		}
		if lipgloss.Width(rendered) <= width {
			return rendered
		}
	}
	return clean[0].style.Render(truncateRunes(clean[0].text, width))
}

func renderStatusParts(segments []statusSegment, sep string) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		parts = append(parts, segment.style.Render(segment.text))
	}
	return strings.Join(parts, sep)
}

func compactNumber(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%.0fk", float64(value)/1_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func compactDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func helpText() string {
	return strings.Join([]string{
		"/theme [light|dark]                    switch UI theme",
		"/auth [deepseek|codex]                 configure credentials",
		"/model [flash|pro|gpt|id]              switch model",
		"/models                                show known models and providers",
		"/profile [billy|name]                  switch SOUL.md system profile",
		"/mcp                                   show MCP servers and status",
		"/reasoning [low|medium|high|xhigh|off] set provider reasoning effort",
		"/thinkview [expanded|collapsed|hidden] show/collapse/hide thinking blocks",
		"/toolview [auto|expanded|collapsed|hidden] control tool blocks",
		"/new                                   start a new chat",
		"/resume [id-prefix]                    list or resume saved chats",
		"/fork [id-prefix]                      fork current or saved chat",
		"/status                                show session details",
		"/exit                                  save and quit",
		"Tab / Up / Down                         complete slash commands",
		"Enter                                  send",
		"Alt+Enter                              insert newline",
		"Ctrl+S                                 send fallback; may freeze SSH if IXON is enabled",
		"mouse wheel / PgUp/PgDn                 scroll transcript",
		"Alt+Home / Alt+End                      top / follow bottom",
		"Ctrl+E                                 collapse or expand selected block",
		"Ctrl+P / Ctrl+L                        select previous / next block",
		"Ctrl+G                                 reconnect gateway",
	}, "\n")
}

func compactEventText(value any) string {
	bytes, _ := json.Marshal(value)
	var data struct {
		ActiveMessages    int    `json:"active_messages"`
		SummaryChars      int    `json:"summary_chars"`
		Detail            string `json:"detail"`
		CompactionID      string `json:"compaction_id"`
		TriggerPromptToks int64  `json:"trigger_prompt_tokens"`
		ThresholdTokens   int64  `json:"threshold_tokens"`
		CompactedMessages int    `json:"compacted_messages"`
	}
	_ = json.Unmarshal(bytes, &data)
	var lines []string
	if data.CompactionID != "" {
		lines = append(lines, "id: "+data.CompactionID)
	}
	if data.TriggerPromptToks > 0 || data.ThresholdTokens > 0 {
		lines = append(lines, fmt.Sprintf("trigger: %d / threshold %d tokens", data.TriggerPromptToks, data.ThresholdTokens))
	} else if data.Detail != "" {
		lines = append(lines, data.Detail)
	}
	if data.CompactedMessages > 0 {
		lines = append(lines, fmt.Sprintf("compacted messages: %d", data.CompactedMessages))
	}
	if data.ActiveMessages > 0 {
		lines = append(lines, fmt.Sprintf("active messages: %d", data.ActiveMessages))
	}
	if data.SummaryChars > 0 {
		lines = append(lines, fmt.Sprintf("summary chars: %d", data.SummaryChars))
	}
	if len(lines) == 0 {
		return "context compacted"
	}
	return strings.Join(lines, "\n")
}

func (m Model) mouseInViewport(x, y int) bool {
	return x >= 0 && y >= 0 && y < m.viewport.Height() && x < max(1, m.viewport.Width())
}

func (m Model) selectionPointFromMouse(x, y int) selectionPoint {
	return selectionPoint{row: m.viewport.YOffset() + y, col: max(0, m.viewport.XOffset()+x)}
}

func (m Model) selectionPointFromMouseClamped(x, y int) selectionPoint {
	if y < 0 {
		y = 0
	}
	if y >= m.viewport.Height() {
		y = max(0, m.viewport.Height()-1)
	}
	if x < 0 {
		x = 0
	}
	if x >= m.viewport.Width() {
		x = max(0, m.viewport.Width())
	}
	return m.selectionPointFromMouse(x, y)
}

func (m Model) selectedTranscriptText() string {
	start, end := orderedSelection(m.selectStart, m.selectEnd)
	if start.row == end.row && start.col == end.col {
		return ""
	}
	lines := strings.Split(xansi.Strip(m.baseViewportContent()), "\n")
	if len(lines) == 0 {
		return ""
	}
	start.row = max(0, min(start.row, len(lines)-1))
	end.row = max(0, min(end.row, len(lines)-1))
	var selected []string
	for row := start.row; row <= end.row; row++ {
		line := lines[row]
		left := 0
		right := xansi.StringWidth(line)
		if row == start.row {
			left = min(start.col, right)
		}
		if row == end.row {
			right = min(max(end.col, left), right)
		}
		selected = append(selected, strings.TrimRight(xansi.Cut(line, left, right), " "))
	}
	return strings.Trim(strings.Join(selected, "\n"), "\n")
}

func (m Model) hasSelection() bool {
	start, end := orderedSelection(m.selectStart, m.selectEnd)
	return start.row != end.row || start.col != end.col
}

func (m *Model) applySelectionHighlight() {
	m.viewport.SetContent(m.selectionHighlightedContent())
}

func (m Model) baseViewportContent() string {
	if m.viewportContent != "" {
		return m.viewportContent
	}
	return m.viewport.GetContent()
}

func (m Model) selectionHighlightedContent() string {
	content := m.baseViewportContent()
	rawLines := strings.Split(content, "\n")
	ranges := m.selectionLineRanges(rawLines)
	if len(ranges) == 0 {
		return content
	}
	styles := m.styles()
	for _, rng := range ranges {
		rawLines[rng.row] = lipgloss.StyleRanges(
			rawLines[rng.row],
			lipgloss.NewRange(rng.left, rng.right, styles.selection),
		)
	}
	return strings.Join(rawLines, "\n")
}

type selectionLineRange struct {
	row   int
	left  int
	right int
}

func (m Model) selectionLineRanges(rawLines []string) []selectionLineRange {
	start, end := orderedSelection(m.selectStart, m.selectEnd)
	if (start.row == end.row && start.col == end.col) || len(rawLines) == 0 {
		return nil
	}
	start.row = max(0, min(start.row, len(rawLines)-1))
	end.row = max(0, min(end.row, len(rawLines)-1))
	ranges := make([]selectionLineRange, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		lineWidth := xansi.StringWidth(xansi.Strip(rawLines[row]))
		left := 0
		right := lineWidth
		if row == start.row {
			left = min(max(start.col, 0), lineWidth)
		}
		if row == end.row {
			right = min(max(end.col, left), lineWidth)
		}
		if right > left {
			ranges = append(ranges, selectionLineRange{row: row, left: left, right: right})
		}
	}
	return ranges
}

func (m Model) selectionByteRange() (int, int) {
	start, end := orderedSelection(m.selectStart, m.selectEnd)
	if start.row == end.row && start.col == end.col {
		return -1, -1
	}
	content := xansi.Strip(m.baseViewportContent())
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return -1, -1
	}
	start.row = max(0, min(start.row, len(lines)-1))
	end.row = max(0, min(end.row, len(lines)-1))
	startByte := byteOffsetForSelection(lines, start)
	endByte := byteOffsetForSelection(lines, end)
	if endByte < startByte {
		return endByte, startByte
	}
	return startByte, endByte
}

func byteOffsetForSelection(lines []string, point selectionPoint) int {
	offset := 0
	for row := 0; row < point.row && row < len(lines); row++ {
		offset += len(lines[row])
		if row < len(lines)-1 {
			offset++
		}
	}
	if point.row >= len(lines) {
		return offset
	}
	return offset + byteOffsetForDisplayCol(lines[point.row], point.col)
}

func byteOffsetForDisplayCol(line string, col int) int {
	if col <= 0 {
		return 0
	}
	line = xansi.Strip(line)
	width := 0
	offset := 0
	for offset < len(line) {
		cluster, w := xansi.FirstGraphemeCluster(line[offset:], xansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		clusterWidth := max(1, w)
		if width+clusterWidth > col {
			return offset
		}
		width += clusterWidth
		offset += len(cluster)
	}
	return len(line)
}

func orderedSelection(a, b selectionPoint) (selectionPoint, selectionPoint) {
	if a.row > b.row || (a.row == b.row && a.col > b.col) {
		return b, a
	}
	return a, b
}

func copySelectionCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err == nil {
			return clipboardCopiedMsg{chars: len([]rune(text)), method: "clipboard"}
		} else if oscText := trimUTF8Bytes(text, osc52MaxBytes); oscText == "" {
			return clipboardCopiedMsg{chars: 0, err: err.Error() + "; osc52: selection too large or empty"}
		} else if oscErr := writeOSC52(oscText); oscErr != nil {
			return clipboardCopiedMsg{chars: len([]rune(text)), err: err.Error() + "; osc52: " + oscErr.Error()}
		} else if len(oscText) < len(text) {
			return clipboardCopiedMsg{chars: len([]rune(oscText)), method: "osc52 truncated"}
		}
		return clipboardCopiedMsg{chars: len([]rune(text)), method: "osc52"}
	}
}

func trimUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	text = text[:maxBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func writeOSC52(text string) error {
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := fmt.Fprint(os.Stdout, "\x1b]52;c;"+payload+"\x07")
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var (
	tuiThemes = map[string]tuiTheme{
		"light": {
			background:      "#F7F3EA",
			foreground:      "#1D1B16",
			headerFg:        "#1D1B16",
			headerBg:        "#E9DFC9",
			statusFg:        "#2D3524",
			statusBg:        "#DDE8D7",
			footerFg:        "#6F675C",
			footerBg:        "#E9DFC9",
			inputFg:         "#1D1B16",
			inputBg:         "#FFFDF8",
			mutedFg:         "#6F675C",
			inputBorder:     "#CFC3AF",
			blockBorder:     "#CFC3AF",
			blockBg:         "#FFFDF8",
			userBg:          "#ECECEC",
			userBorder:      "#B9B9B9",
			userFg:          "#222222",
			assistantBg:     "#FFFDF8",
			assistantBorder: "#D8D1C3",
			assistantFg:     "#1D1B16",
			reasoningBg:     "#F4E6C4",
			reasoningFg:     "#4A3512",
			reasoningBorder: "#D2A747",
			toolBg:          "#EDF0E6",
			toolFg:          "#2D3524",
			toolBorder:      "#A4B27C",
			errorBg:         "#F8DAD3",
			errorFg:         "#5A1D15",
			errorBorder:     "#C86552",
		},
		"dark": {
			background:      "#050505",
			foreground:      "#E7E7E7",
			headerFg:        "#E7E7E7",
			headerBg:        "#111111",
			statusFg:        "#E7E7E7",
			statusBg:        "#050505",
			footerFg:        "#8A8A8A",
			footerBg:        "#050505",
			inputFg:         "#F2F2F2",
			inputBg:         "#0B0B0B",
			mutedFg:         "#8A8A8A",
			inputBorder:     "#303030",
			blockBorder:     "#2B2B2B",
			blockBg:         "#080808",
			userBg:          "#161616",
			userBorder:      "#4A4A4A",
			userFg:          "#D8D8D8",
			assistantBg:     "#080808",
			assistantBorder: "#2B2B2B",
			assistantFg:     "#F0F0F0",
			reasoningBg:     "#171104",
			reasoningFg:     "#FFDFA3",
			reasoningBorder: "#F59E0B",
			toolBg:          "#0D1208",
			toolFg:          "#E7F6D4",
			toolBorder:      "#84CC16",
			errorBg:         "#210909",
			errorFg:         "#FFD1D1",
			errorBorder:     "#F87171",
		},
	}
)

type tuiTheme struct {
	background      string
	foreground      string
	headerFg        string
	headerBg        string
	statusFg        string
	statusBg        string
	footerFg        string
	footerBg        string
	inputFg         string
	inputBg         string
	mutedFg         string
	inputBorder     string
	blockBorder     string
	blockBg         string
	userBg          string
	userBorder      string
	userFg          string
	assistantBg     string
	assistantBorder string
	assistantFg     string
	reasoningBg     string
	reasoningFg     string
	reasoningBorder string
	toolBg          string
	toolFg          string
	toolBorder      string
	errorBg         string
	errorFg         string
	errorBorder     string
}

type themeStyles struct {
	background        string
	foreground        string
	header            lipgloss.Style
	status            lipgloss.Style
	footer            lipgloss.Style
	input             lipgloss.Style
	runStatus         lipgloss.Style
	popup             lipgloss.Style
	popupLine         lipgloss.Style
	popupMuted        lipgloss.Style
	popupSelected     lipgloss.Style
	statusState       lipgloss.Style
	statusModel       lipgloss.Style
	statusReasoning   lipgloss.Style
	statusAccess      lipgloss.Style
	statusUsage       lipgloss.Style
	statusCost        lipgloss.Style
	statusDim         lipgloss.Style
	statusSeparator   lipgloss.Style
	selection         lipgloss.Style
	activityText      lipgloss.Style
	activityGuide     lipgloss.Style
	activityTool      lipgloss.Style
	activityReasoning lipgloss.Style
	activityError     lipgloss.Style
	activityStatus    lipgloss.Style
	markdown          terminalMarkdownStyles
	textarea          textarea.Styles
	block             lipgloss.Style
	user              lipgloss.Style
	assistant         lipgloss.Style
	reasoning         lipgloss.Style
	tool              lipgloss.Style
	error             lipgloss.Style
	statusBlock       lipgloss.Style
}

func newThemeStyles(theme tuiTheme) themeStyles {
	text := lipgloss.Color(theme.foreground)
	inputText := lipgloss.Color(theme.inputFg)
	inputBg := lipgloss.Color(theme.inputBg)
	muted := lipgloss.Color(theme.mutedFg)
	statusBg := lipgloss.Color(theme.statusBg)
	block := func(fg, bg, border string) lipgloss.Style {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(fg)).
			Background(lipgloss.Color(bg)).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color(border)).
			Padding(0, 0).
			MarginBottom(1)
	}
	textareaStyles := textarea.DefaultLightStyles()
	baseInput := lipgloss.NewStyle().
		Foreground(inputText).
		Background(inputBg)
	textareaStyles.Focused.Base = baseInput
	textareaStyles.Focused.Text = baseInput
	textareaStyles.Focused.CursorLine = baseInput
	textareaStyles.Focused.Placeholder = lipgloss.NewStyle().
		Foreground(muted).
		Background(inputBg)
	textareaStyles.Focused.Prompt = lipgloss.NewStyle().
		Foreground(muted).
		Background(inputBg)
	textareaStyles.Focused.EndOfBuffer = lipgloss.NewStyle().
		Foreground(inputBg).
		Background(inputBg)
	textareaStyles.Focused.LineNumber = baseInput
	textareaStyles.Focused.CursorLineNumber = baseInput
	textareaStyles.Blurred = textareaStyles.Focused
	textareaStyles.Cursor.Color = inputText
	return themeStyles{
		background: theme.background,
		foreground: theme.foreground,
		header: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.headerFg)).
			Background(lipgloss.Color(theme.headerBg)).
			Bold(true),
		status: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.statusFg)).
			Background(statusBg).
			Padding(0, 1),
		footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.footerFg)).
			Background(lipgloss.Color(theme.footerBg)).
			Padding(0, 1),
		input: lipgloss.NewStyle().
			Foreground(inputText).
			Background(inputBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(theme.inputBorder)).
			Padding(0, 0),
		runStatus: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.statusFg)).
			Background(statusBg).
			Bold(true),
		popup: lipgloss.NewStyle().
			Foreground(text).
			Background(lipgloss.Color(theme.inputBg)).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(theme.inputBorder)).
			Padding(0, 0),
		popupLine: lipgloss.NewStyle().
			Foreground(text).
			Background(lipgloss.Color(theme.inputBg)),
		popupMuted: lipgloss.NewStyle().
			Foreground(muted).
			Background(lipgloss.Color(theme.inputBg)),
		popupSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.headerFg)).
			Background(lipgloss.Color(theme.headerBg)).
			Bold(false),
		statusState: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(statusBg).
			Bold(true),
		statusModel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#BFE7FF")).
			Background(statusBg),
		statusReasoning: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.reasoningBorder)).
			Background(statusBg),
		statusAccess: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.userBorder)).
			Background(statusBg),
		statusUsage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E7D7A9")).
			Background(statusBg),
		statusCost: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#B7E4C7")).
			Background(statusBg),
		statusDim: lipgloss.NewStyle().
			Foreground(muted).
			Background(statusBg),
		statusSeparator: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666")).
			Background(statusBg),
		selection: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#050505")).
			Background(lipgloss.Color("#FFD166")).
			Bold(true),
		activityText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.foreground)),
		activityGuide: lipgloss.NewStyle().
			Foreground(muted),
		activityTool: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.toolBorder)).
			Bold(true),
		activityReasoning: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.reasoningBorder)).
			Bold(true),
		activityError: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.errorBorder)).
			Bold(true),
		activityStatus: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.statusFg)).
			Bold(true),
		markdown:    terminalMarkdownStyleSet(theme),
		textarea:    textareaStyles,
		block:       block(theme.foreground, theme.blockBg, theme.blockBorder),
		user:        block(theme.userFg, theme.userBg, theme.userBorder),
		assistant:   block(theme.assistantFg, theme.assistantBg, theme.assistantBorder),
		reasoning:   block(theme.reasoningFg, theme.reasoningBg, theme.reasoningBorder),
		tool:        block(theme.toolFg, theme.toolBg, theme.toolBorder),
		error:       block(theme.errorFg, theme.errorBg, theme.errorBorder),
		statusBlock: block(theme.statusFg, theme.statusBg, theme.statusFg),
	}
}
