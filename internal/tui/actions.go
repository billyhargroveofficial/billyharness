package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/promptcommands"
)

type actionSpec struct {
	id              string
	title           string
	category        string
	keybinding      string
	keyAliases      []string
	keySummary      string
	slash           string
	slashArgs       string
	slashAliases    []string
	telegramAliases []string
	summary         string
	enabled         func(Model) bool
	args            func(Model) []slashArg
	run             func(*Model, string) (bool, tea.Cmd)
	keyRun          func(*Model, tea.KeyPressMsg) keyActionResult
}

type keyActionResult struct {
	cmd                tea.Cmd
	model              tea.Model
	returnNow          bool
	reflow             bool
	gotoBottom         bool
	skipTextareaUpdate bool
	skipViewportUpdate bool
}

func actionRegistry() []actionSpec {
	return withSharedActionDefinitions([]actionSpec{
		{
			id:         "message.send",
			title:      "Send Message",
			category:   "message",
			keybinding: "enter",
			keyAliases: []string{"ctrl+s"},
			keySummary: "send message",
			summary:    "send message",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				model, cmd := m.send()
				return keyActionResult{model: model, cmd: cmd, returnNow: true}
			},
		},
		{
			id:         "message.newline",
			title:      "Insert Newline",
			category:   "message",
			keybinding: "alt+enter",
			keySummary: "insert newline",
			summary:    "insert newline",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.textarea.InsertString("\n")
				return keyActionResult{skipTextareaUpdate: true}
			},
		},
		{
			id:       "help.show",
			title:    "Show Help",
			category: "session",
			slash:    "/help",
			summary:  "show commands and key bindings",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.addInfoBlock("HELP", m.helpText())
				m.status = "help shown"
				return true, nil
			},
		},
		{
			id:       "status.show",
			title:    "Show Status",
			category: "session",
			slash:    "/status",
			summary:  "show current session details",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.addInfoBlock("STATUS", m.statusText())
				m.status = "status shown"
				return true, nil
			},
		},
		{
			id:       "context.show",
			title:    "Show Context",
			category: "runtime",
			slash:    "/context",
			summary:  "show active context and contributors",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.status = "loading context"
				return true, m.contextStatusCmd()
			},
		},
		{
			id:        "diff.preview",
			title:     "Preview Diff",
			category:  "session",
			slash:     "/diff",
			slashArgs: "[change_id]",
			summary:   "preview latest turn diff before undo",
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				m.status = "loading diff preview"
				return true, m.turnDiffPreviewCmd(arg)
			},
		},
		{
			id:        "undo.apply",
			title:     "Undo Change",
			category:  "session",
			slash:     "/undo",
			slashArgs: "[change_id]",
			summary:   "revert latest or named turn change",
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				m.status = "undoing turn change"
				return true, m.turnUndoCmd(arg)
			},
		},
		{
			id:       "redo.apply",
			title:    "Redo Change",
			category: "session",
			slash:    "/redo",
			summary:  "reapply last undone turn change",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.status = "redoing turn change"
				return true, m.turnRedoCmd()
			},
		},
		{
			id:       "config.show",
			title:    "Show Config",
			category: "setup",
			slash:    "/config",
			summary:  "show resolved config summary",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.status = "loading config"
				return true, m.configStatusCmd()
			},
		},
		{
			id:        "auth.configure",
			title:     "Configure Auth",
			category:  "setup",
			slash:     "/auth",
			slashArgs: "deepseek|codex",
			summary:   "configure DeepSeek key or Codex OAuth",
			args: func(Model) []slashArg {
				return []slashArg{
					{"deepseek", "save DeepSeek API key"},
					{"codex", "import Codex OAuth from codex login"},
				}
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.handleAuthCommand(arg)
			},
		},
		{
			id:        "theme.set",
			title:     "Switch Theme",
			category:  "ui",
			slash:     "/theme",
			slashArgs: "light|dark",
			summary:   "switch active theme",
			args: func(m Model) []slashArg {
				return rotateSlashArgs([]slashArg{
					{"dark", "black codex-style theme"},
					{"light", "light theme"},
					{"toggle", "switch to the other theme"},
				}, m.theme)
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setTheme(arg), nil
			},
		},
		{
			id:        "model.set",
			title:     "Switch Model",
			category:  "runtime",
			slash:     "/model",
			slashArgs: "flash|pro|gpt|id",
			summary:   "switch model",
			args: func(m Model) []slashArg {
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
					return rotateSlashArgs(values, "flash")
				case "deepseek-v4-pro":
					return rotateSlashArgs(values, "pro")
				case "gpt-5.5":
					return rotateSlashArgs(values, "gpt")
				}
				return values
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setModel(arg), nil
			},
			keybinding: "ctrl+n",
			keySummary: "cycle model",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.cycleModel()
				return keyActionResult{skipTextareaUpdate: true}
			},
		},
		{
			id:       "models.list",
			title:    "List Models",
			category: "runtime",
			slash:    "/models",
			summary:  "list known models",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.addInfoBlock("MODELS", m.modelsText())
				m.status = "models shown"
				return true, nil
			},
		},
		{
			id:        "profile.set",
			title:     "Switch Profile",
			category:  "runtime",
			slash:     "/profile",
			slashArgs: "billy|name",
			summary:   "switch SOUL.md system profile",
			args: func(m Model) []slashArg {
				return []slashArg{
					{m.currentProfile(), "current SOUL.md profile"},
					{"billy", "teacher-style default profile"},
				}
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return true, m.setProfile(arg)
			},
		},
		{
			id:       "mcp.show",
			title:    "Show MCP",
			category: "runtime",
			slash:    "/mcp",
			summary:  "show connected MCP servers",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.status = "loading mcp status"
				return true, m.mcpStatusCmd()
			},
		},
		{
			id:        "reasoning.set",
			title:     "Set Reasoning",
			category:  "runtime",
			slash:     "/reasoning",
			slashArgs: "high|max|off",
			summary:   "set provider reasoning effort",
			args: func(m Model) []slashArg {
				return rotateSlashArgs([]slashArg{
					{"high", "reasoning high"},
					{"xhigh", "reasoning xhigh"},
					{"max", "reasoning max"},
					{"medium", "reasoning medium"},
					{"low", "reasoning low"},
					{"off", "disable reasoning"},
					{"toggle", "cycle reasoning mode"},
				}, m.currentThinking().effortLabel())
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setReasoning(arg), nil
			},
			keybinding: "ctrl+t",
			keySummary: "cycle reasoning mode",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.cycleReasoning()
				return keyActionResult{skipTextareaUpdate: true}
			},
		},
		{
			id:           "access.mode",
			title:        "Set Access Mode",
			category:     "runtime",
			slash:        "/mode",
			slashArgs:    "build|guarded|plan",
			slashAliases: []string{"/access"},
			summary:      "set run access mode",
			args: func(m Model) []slashArg {
				return rotateSlashArgs([]slashArg{
					{config.AccessModeBuild, "allow normal build tools"},
					{config.AccessModeGuarded, "deny write and shell tools"},
					{config.AccessModePlan, "read/search planning mode"},
				}, config.NormalizeAccessMode(m.accessMode))
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setAccessMode(arg), nil
			},
		},
		{
			id:        "thinking.view",
			title:     "Set Thinking View",
			category:  "ui",
			slash:     "/thinkview",
			slashArgs: "expanded|collapsed|hidden",
			summary:   "control thinking blocks",
			args: func(m Model) []slashArg {
				return rotateSlashArgs([]slashArg{
					{"expanded", "show thinking content"},
					{"collapsed", "show collapsed thinking blocks"},
					{"hidden", "hide thinking blocks"},
					{"toggle", "cycle thinking view"},
				}, m.thinkView)
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setThinkView(arg), nil
			},
		},
		{
			id:           "thinking.visibility",
			title:        "Toggle Thinking",
			category:     "ui",
			slash:        "/thinking",
			slashArgs:    "on|off",
			slashAliases: []string{"/show-reasoning", "/show_reasoning"},
			summary:      "alias for thinking visibility",
			args: func(m Model) []slashArg {
				if m.showThinking {
					return []slashArg{{"off", "hide thinking blocks"}, {"on", "show thinking blocks"}, {"toggle", "switch thinking visibility"}}
				}
				return []slashArg{{"on", "show thinking blocks"}, {"off", "hide thinking blocks"}, {"toggle", "switch thinking visibility"}}
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setThinkingDisplay(arg), nil
			},
			keybinding: "ctrl+r",
			keySummary: "toggle thinking display",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.toggleThinkingDisplay()
				return keyActionResult{reflow: true, gotoBottom: m.followOutput, skipTextareaUpdate: true}
			},
		},
		{
			id:        "tool.view",
			title:     "Set Tool View",
			category:  "ui",
			slash:     "/toolview",
			slashArgs: "auto|expanded|collapsed|current|hidden|errors",
			summary:   "control tool blocks",
			args: func(m Model) []slashArg {
				return rotateSlashArgs([]slashArg{
					{"auto", "expand tool blocks while running"},
					{"expanded", "show full tool blocks"},
					{"collapsed", "collapse tool blocks"},
					{"current", "show current turn tools only"},
					{"hidden", "hide tool blocks"},
					{"errors", "show failed tool blocks only"},
					{"toggle", "cycle tool view"},
				}, m.toolView)
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.setToolView(arg), nil
			},
		},
		{
			id:        "copy.semantic",
			title:     "Copy",
			category:  "ui",
			slash:     "/copy",
			slashArgs: "selected|last|tool|transcript|code|command",
			summary:   "copy raw transcript text without UI chrome",
			args: func(Model) []slashArg {
				return []slashArg{
					{"selected", "copy selected transcript cell"},
					{"last", "copy last assistant answer"},
					{"tool", "copy selected or last raw tool output"},
					{"transcript", "copy full raw transcript"},
					{"code", "copy selected or last fenced code block"},
					{"command", "copy current command line"},
				}
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return m.handleCopyCommand(arg)
			},
		},
		{
			id:       "chat.new",
			title:    "New Chat",
			category: "chat",
			slash:    "/new",
			summary:  "start a new chat",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				return true, m.newChat()
			},
		},
		{
			id:        "chat.resume",
			title:     "Resume Chat",
			category:  "chat",
			slash:     "/resume",
			slashArgs: "[id-prefix]",
			summary:   "list or resume local chats",
			args: func(m Model) []slashArg {
				return m.sessionArgs(true)
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return true, m.resumeChat(arg)
			},
		},
		{
			id:        "chat.fork",
			title:     "Fork Chat",
			category:  "chat",
			slash:     "/fork",
			slashArgs: "[id-prefix]",
			summary:   "fork current or named chat",
			args: func(m Model) []slashArg {
				return m.sessionArgs(false)
			},
			run: func(m *Model, arg string) (bool, tea.Cmd) {
				return true, m.forkChat(arg)
			},
		},
		{
			id:         "palette.open",
			title:      "Command Palette",
			category:   "ui",
			keybinding: "ctrl+k",
			keySummary: "open command palette",
			summary:    "open command palette",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.textarea.SetValue("/")
				m.slashIndex = 0
				m.slashDismissed = ""
				m.status = "command palette"
				return keyActionResult{reflow: true, gotoBottom: m.followOutput, skipTextareaUpdate: true}
			},
		},
		{
			id:         "gateway.reconnect",
			title:      "Reconnect Gateway",
			category:   "session",
			keybinding: "ctrl+g",
			keySummary: "reconnect gateway",
			summary:    "reconnect gateway",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				if m.gatewayURL != "" {
					if strings.TrimSpace(m.sessionID) != "" {
						m.status = "replaying gateway"
						return keyActionResult{model: *m, cmd: m.replayGatewayEventsCmd(true), returnNow: true}
					}
					m.status = "connecting gateway"
					return keyActionResult{model: *m, cmd: m.createSessionCmd(), returnNow: true}
				}
				return keyActionResult{skipTextareaUpdate: true}
			},
		},
		{
			id:         "viewport.page_up",
			title:      "Page Up",
			category:   "ui",
			keybinding: "pgup",
			keySummary: "scroll transcript up",
			summary:    "scroll transcript up",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.viewport.PageUp()
				m.followOutput = false
				return keyActionResult{skipTextareaUpdate: true, skipViewportUpdate: true}
			},
		},
		{
			id:         "viewport.page_down",
			title:      "Page Down",
			category:   "ui",
			keybinding: "pgdown",
			keySummary: "scroll transcript down",
			summary:    "scroll transcript down",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.viewport.PageDown()
				m.followOutput = m.viewport.AtBottom()
				return keyActionResult{skipTextareaUpdate: true, skipViewportUpdate: true}
			},
		},
		{
			id:         "viewport.top",
			title:      "Top",
			category:   "ui",
			keybinding: "alt+home",
			keySummary: "jump to top",
			summary:    "jump to top",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.viewport.GotoTop()
				m.followOutput = false
				return keyActionResult{skipTextareaUpdate: true, skipViewportUpdate: true}
			},
		},
		{
			id:         "viewport.bottom",
			title:      "Follow Bottom",
			category:   "ui",
			keybinding: "alt+end",
			keySummary: "follow bottom",
			summary:    "follow bottom",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				m.viewport.GotoBottom()
				m.followOutput = true
				return keyActionResult{skipTextareaUpdate: true, skipViewportUpdate: true}
			},
		},
		{
			id:         "block.toggle",
			title:      "Toggle Block",
			category:   "ui",
			keybinding: "ctrl+e",
			keySummary: "collapse or expand selected block",
			summary:    "collapse or expand selected block",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				result := keyActionResult{skipTextareaUpdate: true}
				if len(m.blocks) > 0 {
					m.toggleSelectedBlock()
					result.reflow = true
					result.gotoBottom = m.followOutput
				}
				return result
			},
		},
		{
			id:         "block.previous",
			title:      "Previous Block",
			category:   "ui",
			keybinding: "ctrl+p",
			keySummary: "select previous block",
			summary:    "select previous block",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				result := keyActionResult{skipTextareaUpdate: true}
				if m.selected > 0 {
					m.selected--
					result.reflow = true
					result.gotoBottom = m.followOutput
				}
				return result
			},
		},
		{
			id:         "block.next",
			title:      "Next Block",
			category:   "ui",
			keybinding: "ctrl+l",
			keySummary: "select next block",
			summary:    "select next block",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				result := keyActionResult{skipTextareaUpdate: true}
				if m.selected < len(m.blocks)-1 {
					m.selected++
					result.reflow = true
					result.gotoBottom = m.followOutput
				}
				return result
			},
		},
		{
			id:           "session.exit",
			title:        "Exit",
			category:     "session",
			slash:        "/exit",
			slashAliases: []string{"/quit"},
			summary:      "save and quit",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				_ = m.saveCurrentSession()
				_ = m.saveSettings()
				return true, tea.Quit
			},
			keybinding: "ctrl+c",
			keySummary: "quit",
			keyRun: func(m *Model, _ tea.KeyPressMsg) keyActionResult {
				return keyActionResult{model: *m, cmd: tea.Quit, returnNow: true}
			},
		},
	})
}

func withSharedActionDefinitions(actions []actionSpec) []actionSpec {
	for i := range actions {
		def, ok := clientux.ActionDefinitionByID(actions[i].id)
		if !ok {
			continue
		}
		actions[i].title = def.Title
		actions[i].category = def.Category
		actions[i].slash = def.Slash
		actions[i].slashArgs = def.SlashArgs
		actions[i].slashAliases = append([]string{}, def.SlashAliases...)
		actions[i].telegramAliases = append([]string{}, def.TelegramAliases...)
		actions[i].summary = def.Summary
	}
	return actions
}

func rotateSlashArgs(values []slashArg, current string) []slashArg {
	for i, item := range values {
		if item.value == current {
			return append(append([]slashArg{}, values[i+1:]...), values[:i+1]...)
		}
	}
	return values
}

func slashCommands() []slashCommand {
	actions := actionRegistry()
	commands := make([]slashCommand, 0, len(actions))
	for _, action := range actions {
		if action.slash == "" {
			continue
		}
		commands = append(commands, slashCommand{
			id:       action.id,
			title:    action.title,
			category: action.category,
			name:     action.slash,
			args:     action.slashArgs,
			summary:  action.summary,
			aliases:  append([]string{}, action.slashAliases...),
		})
	}
	return commands
}

func builtInSlashNameSet() map[string]bool {
	var names []string
	for _, command := range slashCommands() {
		names = append(names, command.name)
		names = append(names, command.aliases...)
	}
	return promptcommands.BuiltInNameSet(names)
}

func actionForSlash(token string) (actionSpec, bool) {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, action := range actionRegistry() {
		if action.slash == token {
			return action, true
		}
		for _, alias := range action.slashAliases {
			if strings.ToLower(alias) == token {
				return action, true
			}
		}
	}
	return actionSpec{}, false
}

func slashCommandMatches(command slashCommand, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == command.name {
		return true
	}
	for _, alias := range command.aliases {
		if strings.ToLower(alias) == token {
			return true
		}
	}
	return false
}

func (m Model) actionEnabled(action actionSpec) bool {
	if action.enabled == nil {
		return true
	}
	return action.enabled(m)
}

func actionForKey(msg tea.KeyPressMsg) (actionSpec, bool) {
	names := keyPressNames(msg)
	for _, action := range actionRegistry() {
		for _, key := range actionKeybindings(action) {
			for _, name := range names {
				if strings.EqualFold(key, name) {
					return action, true
				}
			}
		}
	}
	return actionSpec{}, false
}

func actionKeybindings(action actionSpec) []string {
	var out []string
	if strings.TrimSpace(action.keybinding) != "" {
		out = append(out, strings.TrimSpace(action.keybinding))
	}
	for _, alias := range action.keyAliases {
		if strings.TrimSpace(alias) != "" {
			out = append(out, strings.TrimSpace(alias))
		}
	}
	return out
}

func keyPressNames(msg tea.KeyPressMsg) []string {
	names := []string{strings.ToLower(strings.TrimSpace(msg.String()))}
	if msg.Code == tea.KeyEnter {
		if msg.Mod.Contains(tea.ModAlt) {
			names = append(names, "alt+enter")
		} else {
			names = append(names, "enter")
		}
	}
	return names
}

func (m *Model) handleKeyAction(msg tea.KeyPressMsg) (keyActionResult, bool) {
	action, ok := actionForKey(msg)
	if !ok || !m.actionEnabled(action) || action.keyRun == nil {
		return keyActionResult{}, false
	}
	return action.keyRun(m, msg), true
}

func helpText() string {
	var lines []string
	for _, action := range actionRegistry() {
		if action.slash == "" {
			continue
		}
		usage := action.slash
		if action.slashArgs != "" {
			usage += " " + action.slashArgs
		}
		lines = append(lines, fmt.Sprintf("%-42s %s", usage, action.summary))
	}
	lines = append(lines, keybindingHelpLines()...)
	return strings.Join(lines, "\n")
}

func (m Model) helpText() string {
	lines := []string{helpText()}
	for _, command := range m.promptCommands {
		usage := "/" + command.Name
		if command.ArgumentHint != "" {
			usage += " " + command.ArgumentHint
		}
		lines = append(lines, fmt.Sprintf("%-42s %s", usage, command.Description))
	}
	return strings.Join(lines, "\n")
}

func keybindingHelpLines() []string {
	var lines []string
	lines = append(lines, "Tab / Up / Down                           complete slash commands")
	for _, action := range actionRegistry() {
		keys := actionKeybindings(action)
		if len(keys) == 0 {
			continue
		}
		summary := action.keySummary
		if summary == "" {
			summary = action.summary
		}
		if summary == "" {
			summary = strings.ToLower(action.title)
		}
		lines = append(lines, fmt.Sprintf("%-42s %s", strings.Join(keys, " / "), summary))
	}
	lines = append(lines, "mouse wheel                                scroll transcript")
	return lines
}
