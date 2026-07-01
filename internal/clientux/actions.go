package clientux

// ActionDefinition is frontend-neutral metadata for user-visible actions.
// Runtime handlers stay in the concrete clients.
type ActionDefinition struct {
	ID              string
	Title           string
	Category        string
	Slash           string
	SlashArgs       string
	SlashAliases    []string
	TelegramAliases []string
	TelegramUsage   string
	Summary         string
	TelegramSummary string
}

func ActionDefinitions() []ActionDefinition {
	out := make([]ActionDefinition, len(sharedActionDefinitions))
	for i, def := range sharedActionDefinitions {
		out[i] = copyActionDefinition(def)
	}
	return out
}

func ActionDefinitionByID(id string) (ActionDefinition, bool) {
	for _, def := range sharedActionDefinitions {
		if def.ID == id {
			return copyActionDefinition(def), true
		}
	}
	return ActionDefinition{}, false
}

func MustActionDefinition(id string) ActionDefinition {
	def, ok := ActionDefinitionByID(id)
	if !ok {
		panic("unknown client UX action definition: " + id)
	}
	return def
}

func (d ActionDefinition) TelegramCommandUsage() string {
	if d.TelegramUsage != "" {
		return d.TelegramUsage
	}
	if d.Slash == "" {
		return ""
	}
	if d.SlashArgs != "" {
		return d.Slash + " " + d.SlashArgs
	}
	return d.Slash
}

func (d ActionDefinition) TelegramCommandSummary() string {
	if d.TelegramSummary != "" {
		return d.TelegramSummary
	}
	return d.Summary
}

func copyActionDefinition(def ActionDefinition) ActionDefinition {
	def.SlashAliases = append([]string{}, def.SlashAliases...)
	def.TelegramAliases = append([]string{}, def.TelegramAliases...)
	return def
}

var sharedActionDefinitions = []ActionDefinition{
	{
		ID:              "help.show",
		Title:           "Show Help",
		Category:        "session",
		Slash:           "/help",
		TelegramAliases: []string{"/start", "/help"},
		Summary:         "show commands and key bindings",
		TelegramSummary: "show commands",
	},
	{
		ID:              "commands.search",
		Title:           "Search Commands",
		Category:        "session",
		Slash:           "/commands",
		SlashArgs:       "[query]",
		TelegramAliases: []string{"/commands"},
		TelegramUsage:   "/commands [query]",
		Summary:         "search command registry",
		TelegramSummary: "search command registry",
	},
	{
		ID:              "status.show",
		Title:           "Show Status",
		Category:        "session",
		Slash:           "/status",
		TelegramAliases: []string{"/status"},
		Summary:         "show current session details",
		TelegramSummary: "current chat settings",
	},
	{
		ID:              "context.show",
		Title:           "Show Context",
		Category:        "runtime",
		Slash:           "/context",
		TelegramAliases: []string{"/context"},
		Summary:         "show active context and contributors",
		TelegramSummary: "active context and contributors",
	},
	{
		ID:              "memory.manage",
		Title:           "Manage Memory",
		Category:        "runtime",
		Slash:           "/memory",
		SlashArgs:       "list|search|read|add|replace|remove",
		TelegramAliases: []string{"/memory"},
		TelegramUsage:   "/memory list|search|read|add|replace|remove",
		Summary:         "manage local memory entries",
		TelegramSummary: "manage local memory",
	},
	{
		ID:              "diff.preview",
		Title:           "Preview Diff",
		Category:        "session",
		Slash:           "/diff",
		SlashArgs:       "[change_id]",
		TelegramAliases: []string{"/diff"},
		TelegramUsage:   "/diff [change_id]",
		Summary:         "preview latest turn diff before undo",
		TelegramSummary: "preview latest turn diff",
	},
	{
		ID:              "undo.apply",
		Title:           "Undo Change",
		Category:        "session",
		Slash:           "/undo",
		SlashArgs:       "[change_id]",
		TelegramAliases: []string{"/undo"},
		TelegramUsage:   "/undo [change_id]",
		Summary:         "revert latest or named turn change",
		TelegramSummary: "undo latest or named turn change",
	},
	{
		ID:              "redo.apply",
		Title:           "Redo Change",
		Category:        "session",
		Slash:           "/redo",
		TelegramAliases: []string{"/redo"},
		TelegramUsage:   "/redo",
		Summary:         "reapply last undone turn change",
		TelegramSummary: "redo last undone turn change",
	},
	{
		ID:              "config.show",
		Title:           "Show Config",
		Category:        "setup",
		Slash:           "/config",
		TelegramAliases: []string{"/config"},
		Summary:         "show resolved config summary",
		TelegramSummary: "resolved config summary",
	},
	{
		ID:              "auth.configure",
		Title:           "Configure Auth",
		Category:        "setup",
		Slash:           "/auth",
		SlashArgs:       "deepseek|codex",
		TelegramAliases: []string{"/auth"},
		TelegramUsage:   "/auth deepseek sk-...",
		Summary:         "save DeepSeek key or Codex OAuth",
	},
	{
		ID:        "theme.set",
		Title:     "Switch Theme",
		Category:  "ui",
		Slash:     "/theme",
		SlashArgs: "light|dark",
		Summary:   "switch active theme",
	},
	{
		ID:              "model.set",
		Title:           "Switch Model",
		Category:        "runtime",
		Slash:           "/model",
		SlashArgs:       "flash|pro|gpt|id",
		TelegramAliases: []string{"/model"},
		TelegramUsage:   "/model flash|pro|gpt|gpt-5.5",
		Summary:         "switch model",
	},
	{
		ID:       "models.list",
		Title:    "List Models",
		Category: "runtime",
		Slash:    "/models",
		Summary:  "list known models",
	},
	{
		ID:              "profile.set",
		Title:           "Switch Profile",
		Category:        "runtime",
		Slash:           "/profile",
		SlashArgs:       "billy|name",
		TelegramAliases: []string{"/profile"},
		TelegramUsage:   "/profile billy",
		Summary:         "switch SOUL.md system profile",
		TelegramSummary: "switch profile",
	},
	{
		ID:              "mcp.show",
		Title:           "Show MCP",
		Category:        "runtime",
		Slash:           "/mcp",
		TelegramAliases: []string{"/mcp"},
		Summary:         "show connected MCP servers",
		TelegramSummary: "MCP status",
	},
	{
		ID:              "processes.show",
		Title:           "Show Processes",
		Category:        "runtime",
		Slash:           "/processes",
		SlashAliases:    []string{"/ps"},
		TelegramAliases: []string{"/processes", "/ps"},
		TelegramUsage:   "/processes",
		Summary:         "show managed shell processes",
		TelegramSummary: "managed shell processes",
	},
	{
		ID:              "reasoning.set",
		Title:           "Set Reasoning",
		Category:        "runtime",
		Slash:           "/reasoning",
		SlashArgs:       "high|max|off",
		TelegramAliases: []string{"/reasoning"},
		TelegramUsage:   "/reasoning low|medium|high|xhigh|off",
		Summary:         "set provider reasoning effort",
	},
	{
		ID:              "access.mode",
		Title:           "Set Access Mode",
		Category:        "runtime",
		Slash:           "/mode",
		SlashArgs:       "build|guarded|plan",
		SlashAliases:    []string{"/access"},
		TelegramAliases: []string{"/mode", "/access"},
		TelegramUsage:   "/mode build|guarded|plan",
		Summary:         "set run access mode",
		TelegramSummary: "set run access mode",
	},
	{
		ID:        "thinking.view",
		Title:     "Set Thinking View",
		Category:  "ui",
		Slash:     "/thinkview",
		SlashArgs: "expanded|collapsed|hidden",
		Summary:   "control thinking blocks",
	},
	{
		ID:        "transcript.view",
		Title:     "Set Transcript View",
		Category:  "ui",
		Slash:     "/transcript",
		SlashArgs: "raw|rich",
		Summary:   "toggle raw or rich transcript rendering",
	},
	{
		ID:           "thinking.visibility",
		Title:        "Toggle Thinking",
		Category:     "ui",
		Slash:        "/thinking",
		SlashArgs:    "on|off",
		SlashAliases: []string{"/show-reasoning", "/show_reasoning"},
		Summary:      "alias for thinking visibility",
	},
	{
		ID:              "tool.view",
		Title:           "Set Tool View",
		Category:        "ui",
		Slash:           "/toolview",
		SlashArgs:       "auto|expanded|collapsed|current|hidden|errors",
		TelegramAliases: []string{"/toolview", "/tools"},
		TelegramUsage:   "/toolview",
		Summary:         "control tool blocks",
		TelegramSummary: "compact tool details for current session",
	},
	{
		ID:        "copy.semantic",
		Title:     "Copy",
		Category:  "ui",
		Slash:     "/copy",
		SlashArgs: "selected|last|tool|transcript|transcript-rich|code|command",
		Summary:   "copy raw transcript text without UI chrome",
	},
	{
		ID:              "chat.new",
		Title:           "New Chat",
		Category:        "chat",
		Slash:           "/new",
		TelegramAliases: []string{"/new", "/reset"},
		Summary:         "start a new chat",
		TelegramSummary: "new session",
	},
	{
		ID:              "chat.resume",
		Title:           "Resume Chat",
		Category:        "chat",
		Slash:           "/resume",
		SlashArgs:       "[id-prefix]",
		TelegramAliases: []string{"/resume"},
		TelegramUsage:   "/resume SESSION_ID",
		Summary:         "list or resume local chats",
		TelegramSummary: "resume session",
	},
	{
		ID:              "chat.fork",
		Title:           "Fork Chat",
		Category:        "chat",
		Slash:           "/fork",
		SlashArgs:       "[id-prefix]",
		TelegramAliases: []string{"/fork"},
		TelegramUsage:   "/fork current|SESSION_ID",
		Summary:         "fork current or named chat",
		TelegramSummary: "fork session",
	},
	{
		ID:              "run.cancel",
		Title:           "Cancel Run",
		Category:        "runtime",
		TelegramAliases: []string{"/cancel"},
		TelegramUsage:   "/cancel",
		Summary:         "cancel current run",
	},
	{
		ID:           "session.exit",
		Title:        "Exit",
		Category:     "session",
		Slash:        "/exit",
		SlashAliases: []string{"/quit"},
		Summary:      "save and quit",
	},
}
