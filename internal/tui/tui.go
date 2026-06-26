package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Options struct {
	GatewayURL string
	Model      string
	Dangerous  bool
	MaxRounds  int
	Plain      bool
}

type thinkingMode struct {
	label  string
	kind   string
	effort string
}

type block struct {
	kind    string
	title   string
	content string
}

type Model struct {
	cfg        config.Config
	gatewayURL string
	sessionID  string
	messages   []protocol.Message

	models       []string
	modelIndex   int
	thinking     []thinkingMode
	thinkingIdx  int
	theme        string
	showThinking bool
	dangerous    bool
	maxRounds    int
	followOutput bool
	plain        bool

	textarea textarea.Model
	viewport viewport.Model
	width    int
	height   int

	blocks     []block
	collapsed  map[int]bool
	selected   int
	busy       bool
	status     string
	err        string
	events     chan tea.Msg
	modelCalls int
	toolCalls  int
	inputTok   int64
	outputTok  int64
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
	ta.Placeholder = "Type message or /theme /model /reasoning. Enter sends, Alt+Enter newline."
	ta.Prompt = "  "
	ta.ShowLineNumbers = false
	ta.SetHeight(4)
	ta.SetWidth(80)
	ta.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.KeyMap = viewport.KeyMap{}
	models := []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	modelIndex := 0
	if opts.Model != "" {
		models = appendIfMissing(models, opts.Model)
		for i, model := range models {
			if model == opts.Model {
				modelIndex = i
				break
			}
		}
	} else if cfg.Model == "deepseek-v4-pro" {
		modelIndex = 1
	}
	plain := opts.Plain || strings.EqualFold(os.Getenv("TERM"), "dumb")
	m := Model{
		cfg:          cfg,
		gatewayURL:   strings.TrimRight(opts.GatewayURL, "/"),
		messages:     agent.InitialMessages(),
		models:       models,
		modelIndex:   modelIndex,
		thinking:     []thinkingMode{{"reasoning: high", "enabled", "high"}, {"reasoning: max", "enabled", "max"}, {"reasoning: off", "disabled", ""}},
		theme:        "light",
		showThinking: true,
		dangerous:    opts.Dangerous,
		maxRounds:    cfg.MaxToolRounds,
		followOutput: true,
		plain:        plain,
		textarea:     ta,
		viewport:     vp,
		collapsed:    map[int]bool{},
		events:       make(chan tea.Msg, 256),
		status:       "ready",
	}
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
				m.collapsed[m.selected] = !m.collapsed[m.selected]
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
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp, tea.MouseWheelLeft:
			m.followOutput = false
		}
	case sessionReadyMsg:
		m.sessionID = msg.id
		m.status = "gateway session " + msg.id[:min(len(msg.id), 8)]
	case streamEventMsg:
		m.applyEvent(msg.event)
		reflow = true
		gotoBottom = m.followOutput
		if m.busy {
			cmds = append(cmds, m.waitEventCmd())
		}
	case runDoneMsg:
		m.busy = false
		if len(msg.messages) > 0 {
			m.messages = msg.messages
		}
		if msg.err != nil {
			m.err = msg.err.Error()
			m.addBlock("error", "ERROR", m.err)
		} else {
			m.status = "completed"
		}
		reflow = true
		gotoBottom = m.followOutput
	case errMsg:
		m.busy = false
		m.err = msg.err.Error()
		m.addBlock("error", "ERROR", m.err)
		reflow = true
		gotoBottom = m.followOutput
	}

	var cmd tea.Cmd
	if !skipTextareaUpdate {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
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
	header := m.headerView()
	ta := m.textarea
	ta.SetStyles(styles.textarea)
	input := styles.input.Width(max(20, m.width-2)).Render(ta.View())
	footer := styles.footer.Width(max(20, m.width-2)).Render(m.footerView())
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), input, footer))
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
	v.MouseMode = tea.MouseModeCellMotion
}

func (m *Model) resize(gotoBottom bool) {
	inputH := 6
	headerH := 3
	footerH := 2
	vh := max(5, m.height-inputH-headerH-footerH)
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(vh)
	m.textarea.SetWidth(max(20, m.width-4))
	m.textarea.SetHeight(4)
	m.reflow(gotoBottom)
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
	line := fmt.Sprintf(" fast-agent-harness TUI  %s  %s  %s  theme:%s  %s ",
		mode, m.currentModel(), m.currentThinking().label, m.theme, danger)
	status := m.status
	if !m.followOutput {
		status = "scrolled: Alt+End follow | " + status
	}
	stats := fmt.Sprintf(" %s | calls m:%d tools:%d tok in:%d out:%d ",
		status, m.modelCalls, m.toolCalls, m.inputTok, m.outputTok)
	return styles.header.Width(m.width).Render(line) + "\n" + styles.status.Width(m.width).Render(stats)
}

func (m Model) footerView() string {
	return "/theme /model /reasoning /thinking /status  Enter send  Alt+Enter newline  mouse/Pg scroll  Alt+End follow"
}

func (m *Model) send() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.textarea.Value())
	if prompt == "" {
		m.status = "empty prompt"
		m.reflow(m.followOutput)
		return *m, nil
	}
	if strings.HasPrefix(prompt, "/") {
		if m.handleSlashCommand(prompt) {
			m.textarea.SetValue("")
		}
		m.reflow(m.followOutput)
		return *m, nil
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
	m.addBlock("user", "USER", prompt)
	m.busy = true
	m.err = ""
	m.status = "running"
	m.followOutput = true
	m.modelCalls = 0
	m.toolCalls = 0
	m.inputTok = 0
	m.outputTok = 0
	if m.gatewayURL != "" {
		go m.runGateway(prompt)
	} else {
		go m.runLocal(prompt)
	}
	m.reflow(true)
	return *m, m.waitEventCmd()
}

func (m Model) waitEventCmd() tea.Cmd {
	return func() tea.Msg {
		return <-m.events
	}
}

func (m Model) createSessionCmd() tea.Cmd {
	return func() tea.Msg {
		req, err := http.NewRequest(http.MethodPost, m.gatewayURL+"/v1/sessions", nil)
		if err != nil {
			return errMsg{err: err}
		}
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
	registry := tools.NewRegistry(cfg)
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
		"model":            m.currentModel(),
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
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			m.events <- runDoneMsg{err: err}
			return
		}
		m.events <- streamEventMsg{event: event}
	}
	m.events <- runDoneMsg{err: scanner.Err()}
}

func (m Model) currentConfig() config.Config {
	cfg := m.cfg
	cfg.Model = m.currentModel()
	cfg.Thinking = m.currentThinking().kind
	cfg.ReasoningEffort = m.currentThinking().effort
	cfg.StoreReasoningContent = true
	cfg.AutoApproveDangerous = m.dangerous
	cfg.MaxToolRounds = m.maxRounds
	return cfg
}

func (m Model) currentModel() string {
	return m.models[m.modelIndex]
}

func (m Model) currentThinking() thinkingMode {
	return m.thinking[m.thinkingIdx]
}

func (m *Model) handleSlashCommand(prompt string) bool {
	fields := strings.Fields(prompt)
	if len(fields) == 0 {
		return true
	}
	command := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.ToLower(strings.Join(fields[1:], " "))
	}
	switch command {
	case "/theme":
		return m.setTheme(arg)
	case "/model":
		return m.setModel(arg)
	case "/models":
		m.addInfoBlock("MODELS", m.modelsText())
		m.status = "models shown"
		return true
	case "/reasoning":
		return m.setReasoning(arg)
	case "/thinking", "/show-reasoning", "/show_reasoning":
		return m.setThinkingDisplay(arg)
	case "/status":
		m.addInfoBlock("STATUS", m.statusText())
		m.status = "status shown"
		return true
	case "/help":
		m.addInfoBlock("HELP", helpText())
		m.status = "help shown"
		return true
	default:
		m.status = "unknown command " + fields[0]
		return false
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
	return true
}

func (m *Model) cycleModel() {
	m.modelIndex = (m.modelIndex + 1) % len(m.models)
	m.status = "model " + m.currentModel()
}

func (m *Model) setModel(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.cycleModel()
		return true
	case "flash", "v4 flash", "v4-flash", "deepseek flash", "deepseek-v4-flash":
		value = "deepseek-v4-flash"
	case "pro", "v4 pro", "v4-pro", "deepseek pro", "deepseek-v4-pro":
		value = "deepseek-v4-pro"
	default:
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
	return true
}

func (m *Model) cycleReasoning() {
	m.thinkingIdx = (m.thinkingIdx + 1) % len(m.thinking)
	m.status = m.currentThinking().label
}

func (m *Model) setReasoning(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.cycleReasoning()
		return true
	case "high", "on", "enabled":
		value = "high"
	case "max", "maximum":
		value = "max"
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
			return true
		}
	}
	m.status = "unknown reasoning " + value
	return false
}

func (m *Model) toggleThinkingDisplay() {
	m.showThinking = !m.showThinking
	if m.showThinking {
		m.status = "thinking blocks visible"
	} else {
		m.status = "thinking blocks hidden"
	}
}

func (m *Model) setThinkingDisplay(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.toggleThinkingDisplay()
	case "on", "show", "shown", "visible", "yes", "true":
		m.showThinking = true
		m.status = "thinking blocks visible"
	case "off", "hide", "hidden", "no", "false":
		m.showThinking = false
		m.status = "thinking blocks hidden"
	default:
		m.status = "unknown thinking display " + value
		return false
	}
	return true
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
	if m.showThinking {
		thinkingDisplay = "visible"
	}
	follow := "off"
	if m.followOutput {
		follow = "on"
	}
	return fmt.Sprintf(
		"mode: %s\nmodel: %s\nreasoning: %s / %s\nthinking blocks: %s\ntheme: %s\ngateway: %s\nsession: %s\ntools: %s, max rounds %d\ncalls: model %d, tools %d\ntokens: input %d, output %d\nfollow output: %s",
		mode,
		m.currentModel(),
		m.currentThinking().kind,
		m.currentThinking().effort,
		thinkingDisplay,
		m.theme,
		gateway,
		session,
		toolsMode,
		m.maxRounds,
		m.modelCalls,
		m.toolCalls,
		m.inputTok,
		m.outputTok,
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
		lines = append(lines, fmt.Sprintf("%s %s", marker, model))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) applyEvent(event protocol.Event) {
	switch event.Type {
	case protocol.EventRunStarted:
		m.status = "run started"
	case protocol.EventModelCallStarted:
		m.modelCalls++
		m.status = fmt.Sprintf("model call %d", m.modelCalls)
	case protocol.EventAssistantReasoning:
		m.appendToOpenBlock("reasoning", "THINKING", fmt.Sprint(event.Data))
	case protocol.EventAssistantDelta:
		m.appendToOpenBlock("assistant", "ASSISTANT", fmt.Sprint(event.Data))
	case protocol.EventToolCallRequested:
		m.toolCalls++
		m.status = "running tool " + toolName(event.Data)
		m.addBlock("tool", toolTitle(event.Data), toolBody(event.Data))
	case protocol.EventToolCallFinished:
		m.appendToOpenBlock("tool", "TOOL", "\n\nresult:\n"+fmt.Sprint(event.Data))
		m.collapseLastToolBlockIfLarge()
	case protocol.EventProviderUsageUpdate:
		in, out := usage(event.Data)
		m.inputTok += in
		m.outputTok += out
	case protocol.EventRunCompleted:
		m.status = "completed"
	case protocol.EventRunFailed:
		m.addBlock("error", "ERROR", fmt.Sprint(event.Data))
		m.status = "failed"
	}
}

func (m *Model) appendToOpenBlock(kind, title, text string) {
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != kind {
		m.addBlock(kind, title, "")
	}
	m.blocks[len(m.blocks)-1].content += text
	m.selected = len(m.blocks) - 1
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
	m.blocks = append(m.blocks, block{kind: kind, title: title, content: content})
	m.selected = len(m.blocks) - 1
}

func (m *Model) reflow(gotoBottom bool) {
	var parts []string
	for i, b := range m.blocks {
		if b.kind == "reasoning" && !m.showThinking {
			continue
		}
		parts = append(parts, m.renderBlock(i, b))
	}
	m.viewport.SetContent(strings.Join(parts, "\n"))
	if gotoBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) renderBlock(i int, b block) string {
	selected := i == m.selected
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
	}
	marker := " "
	if selected {
		marker = ">"
	}
	body := strings.TrimRight(b.content, "\n")
	if m.collapsed[i] {
		body = collapsedPreview(b.content, 8, 1000)
	}
	title := fmt.Sprintf("%s %s", marker, b.title)
	if body == "" {
		return style.Width(max(20, m.width-2)).Render(title)
	}
	return style.Width(max(20, m.width-2)).Render(title + "\n" + body)
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
	bytes, _ := json.Marshal(value)
	var call struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(bytes, &call)
	if call.Name != "" {
		return call.Name
	}
	var m map[string]any
	if err := json.Unmarshal(bytes, &m); err == nil {
		if name, _ := m["name"].(string); name != "" {
			return name
		}
	}
	return "call"
}

func toolTitle(value any) string {
	name := toolName(value)
	args := toolArgs(value)
	for _, key := range []string{"path", "command", "cmd", "query", "url", "pattern", "glob", "file"} {
		if text, ok := args[key].(string); ok && text != "" {
			return fmt.Sprintf("TOOL %s %s", name, oneLinePreview(text, 80))
		}
	}
	return "TOOL " + name
}

func toolBody(value any) string {
	args := toolArgs(value)
	if len(args) > 0 {
		return pretty(args)
	}
	return pretty(value)
}

func toolArgs(value any) map[string]any {
	bytes, _ := json.Marshal(value)
	var call struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(bytes, &call); err == nil && len(call.Arguments) > 0 && string(call.Arguments) != "null" {
		var args map[string]any
		if err := json.Unmarshal(call.Arguments, &args); err == nil {
			return args
		}
		var raw string
		if err := json.Unmarshal(call.Arguments, &raw); err == nil && raw != "" {
			if err := json.Unmarshal([]byte(raw), &args); err == nil {
				return args
			}
		}
	}
	var generic map[string]any
	if err := json.Unmarshal(bytes, &generic); err != nil {
		return nil
	}
	switch args := generic["arguments"].(type) {
	case map[string]any:
		return args
	case string:
		var parsed map[string]any
		if err := json.Unmarshal([]byte(args), &parsed); err == nil {
			return parsed
		}
	}
	return nil
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

func helpText() string {
	return strings.Join([]string{
		"/theme [light|dark]       switch UI theme",
		"/model [flash|pro]        switch DeepSeek V4 model",
		"/models                   show known models",
		"/reasoning [high|max|off] set provider reasoning effort",
		"/thinking [on|off]        show or hide reasoning blocks",
		"/status                   show session details",
		"Enter                     send",
		"Alt+Enter                 insert newline",
		"Ctrl+S                    send fallback; may freeze SSH if IXON is enabled",
		"mouse wheel / PgUp/PgDn    scroll transcript",
		"Alt+Home / Alt+End         top / follow bottom",
		"Ctrl+E                    collapse or expand selected block",
		"Ctrl+P / Ctrl+L           select previous / next block",
		"Ctrl+G                    reconnect gateway",
	}, "\n")
}

func usage(value any) (int64, int64) {
	bytes, _ := json.Marshal(value)
	var u struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	_ = json.Unmarshal(bytes, &u)
	return u.InputTokens, u.OutputTokens
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
			background:      "#f8fafc",
			foreground:      "#0f172a",
			headerFg:        "#0f172a",
			headerBg:        "#bae6fd",
			statusFg:        "#334155",
			statusBg:        "#f1f5f9",
			footerFg:        "#475569",
			footerBg:        "#e2e8f0",
			inputFg:         "#0f172a",
			inputBg:         "#ffffff",
			mutedFg:         "#64748b",
			inputBorder:     "#94a3b8",
			blockBorder:     "#cbd5e1",
			userBorder:      "#2563eb",
			assistantBorder: "#059669",
			reasoningFg:     "#6d28d9",
			reasoningBorder: "#8b5cf6",
			toolFg:          "#92400e",
			toolBorder:      "#f59e0b",
			errorFg:         "#991b1b",
			errorBorder:     "#ef4444",
		},
		"dark": {
			background:      "#0b1117",
			foreground:      "#d5dde8",
			headerFg:        "#0b1117",
			headerBg:        "#7dd3fc",
			statusFg:        "#d5dde8",
			statusBg:        "#1f2937",
			footerFg:        "#a7b0be",
			footerBg:        "#111827",
			inputFg:         "#e5edf6",
			inputBg:         "#111827",
			mutedFg:         "#94a3b8",
			inputBorder:     "#334155",
			blockBorder:     "#334155",
			userBorder:      "#60a5fa",
			assistantBorder: "#34d399",
			reasoningFg:     "#c4b5fd",
			reasoningBorder: "#8b5cf6",
			toolFg:          "#fde68a",
			toolBorder:      "#f59e0b",
			errorFg:         "#fecaca",
			errorBorder:     "#ef4444",
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
	userBorder      string
	assistantBorder string
	reasoningFg     string
	reasoningBorder string
	toolFg          string
	toolBorder      string
	errorFg         string
	errorBorder     string
}

type themeStyles struct {
	background  string
	foreground  string
	header      lipgloss.Style
	status      lipgloss.Style
	footer      lipgloss.Style
	input       lipgloss.Style
	textarea    textarea.Styles
	block       lipgloss.Style
	user        lipgloss.Style
	assistant   lipgloss.Style
	reasoning   lipgloss.Style
	tool        lipgloss.Style
	error       lipgloss.Style
	statusBlock lipgloss.Style
}

func newThemeStyles(theme tuiTheme) themeStyles {
	text := lipgloss.Color(theme.foreground)
	inputText := lipgloss.Color(theme.inputFg)
	inputBg := lipgloss.Color(theme.inputBg)
	muted := lipgloss.Color(theme.mutedFg)
	block := lipgloss.NewStyle().
		Foreground(text).
		Background(lipgloss.Color(theme.background)).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(theme.blockBorder)).
		Padding(0, 1).
		MarginBottom(1)
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
			Background(lipgloss.Color(theme.statusBg)),
		footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.footerFg)).
			Background(lipgloss.Color(theme.footerBg)).
			Padding(0, 1),
		input: lipgloss.NewStyle().
			Foreground(inputText).
			Background(inputBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(theme.inputBorder)).
			Padding(0, 1),
		textarea:  textareaStyles,
		block:     block,
		user:      block.Copy().BorderForeground(lipgloss.Color(theme.userBorder)),
		assistant: block.Copy().BorderForeground(lipgloss.Color(theme.assistantBorder)),
		reasoning: block.Copy().
			Foreground(lipgloss.Color(theme.reasoningFg)).
			BorderForeground(lipgloss.Color(theme.reasoningBorder)),
		tool: block.Copy().
			Foreground(lipgloss.Color(theme.toolFg)).
			BorderForeground(lipgloss.Color(theme.toolBorder)),
		error: block.Copy().
			Foreground(lipgloss.Color(theme.errorFg)).
			BorderForeground(lipgloss.Color(theme.errorBorder)),
		statusBlock: block.Copy().
			Foreground(lipgloss.Color(theme.statusFg)).
			BorderForeground(lipgloss.Color(theme.statusFg)),
	}
}
