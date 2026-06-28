package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type actionSpec struct {
	id              string
	title           string
	category        string
	keybinding      string
	slash           string
	slashArgs       string
	slashAliases    []string
	telegramAliases []string
	summary         string
	enabled         func(Model) bool
	args            func(Model) []slashArg
	run             func(*Model, string) (bool, tea.Cmd)
}

func actionRegistry() []actionSpec {
	return []actionSpec{
		{
			id:       "help.show",
			title:    "Show Help",
			category: "session",
			slash:    "/help",
			telegramAliases: []string{
				"/start",
				"/help",
			},
			summary: "show commands and key bindings",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.addInfoBlock("HELP", helpText())
				m.status = "help shown"
				return true, nil
			},
		},
		{
			id:       "status.show",
			title:    "Show Status",
			category: "session",
			slash:    "/status",
			telegramAliases: []string{
				"/status",
			},
			summary: "show current session details",
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
			telegramAliases: []string{
				"/context",
			},
			summary: "show active context and contributors",
			run: func(m *Model, _ string) (bool, tea.Cmd) {
				m.status = "loading context"
				return true, m.contextStatusCmd()
			},
		},
		{
			id:       "config.show",
			title:    "Show Config",
			category: "setup",
			slash:    "/config",
			telegramAliases: []string{
				"/config",
			},
			summary: "show resolved config summary",
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
			telegramAliases: []string{
				"/auth",
			},
			summary: "configure DeepSeek key or Codex OAuth",
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
			telegramAliases: []string{
				"/model",
			},
			summary: "switch model",
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
			telegramAliases: []string{
				"/profile",
			},
			summary: "switch SOUL.md system profile",
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
			telegramAliases: []string{
				"/mcp",
			},
			summary: "show connected MCP servers",
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
			telegramAliases: []string{
				"/reasoning",
			},
			summary: "set provider reasoning effort",
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
			telegramAliases: []string{
				"/new",
				"/reset",
			},
			summary: "start a new chat",
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
		},
	}
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
	lines = append(lines,
		"Tab / Up / Down                           complete slash commands",
		"Ctrl+K                                    open command palette",
		"Enter                                     send",
		"Alt+Enter                                 insert newline",
		"Ctrl+S                                    send fallback; may freeze SSH if IXON is enabled",
		"mouse wheel / PgUp/PgDn                    scroll transcript",
		"Alt+Home / Alt+End                         top / follow bottom",
		"Ctrl+E                                    collapse or expand selected block",
		"Ctrl+P / Ctrl+L                           select previous / next block",
		"Ctrl+G                                    reconnect gateway",
	)
	return strings.Join(lines, "\n")
}
