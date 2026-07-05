package docsgen

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/commandregistry"
	"github.com/billyhargroveofficial/billyharness/internal/telegrambot"
	"github.com/billyhargroveofficial/billyharness/internal/tui"
)

type commandsReferenceData struct {
	Actions         []tui.ActionDoc
	Telegram        []telegrambot.CommandDoc
	Shared          []clientux.ActionDefinition
	RegistryActions []commandregistry.Entry
}

func GenerateCommands() ([]byte, error) {
	data := commandsReferenceInput()
	var b bytes.Buffer
	b.Write(header("internal/clientux internal/tui internal/telegrambot internal/commandregistry"))
	b.WriteString("# Commands Reference\n\n")
	b.WriteString("This reference joins the shared action metadata, TUI action registry, Telegram command registry, and built-in commandregistry action entries. Runtime prompt commands, profiles, and MCP prompts are intentionally live-only and appear through `/commands`.\n\n")
	b.WriteString("## Cross-Surface Actions\n\n")
	b.WriteString(markdownTable([]string{"ID", "TUI slash", "TUI keys", "Telegram aliases", "Class", "Summary"}, commandSurfaceRows(data)))
	b.WriteString("\n## Keybinding-Only TUI Actions\n\n")
	b.WriteString(markdownTable([]string{"ID", "Keys", "Summary"}, keybindingOnlyRows(data.Actions)))
	b.WriteString("\n## Telegram Command Classes\n\n")
	b.WriteString(markdownTable([]string{"Class", "Policy meaning"}, commandClassRows(data.Telegram)))
	b.WriteString("\n## Runtime Command Registry\n\n")
	b.WriteString("`commandregistry.Build()` merges built-in client actions with prompt commands, profiles, and MCP prompts. Docsgen records the built-in action entries; runtime-only sources remain visible through the live `/commands` surface.\n\n")
	b.WriteString(markdownTable([]string{"Source", "How it enters the registry", "Docsgen behavior"}, commandRegistryRows(data)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func commandsReferenceInput() commandsReferenceData {
	shared := clientux.ActionDefinitions()
	registry := commandregistry.Build(commandregistry.BuildOptions{Actions: shared})
	return commandsReferenceData{
		Actions:         tui.ActionDocs(),
		Telegram:        telegrambot.CommandDocs(),
		Shared:          shared,
		RegistryActions: registry.Entries(),
	}
}

func commandSurfaceRows(data commandsReferenceData) [][]string {
	actions := map[string]tui.ActionDoc{}
	for _, action := range data.Actions {
		actions[action.ID] = action
	}
	telegram := map[string]telegrambot.CommandDoc{}
	for _, command := range data.Telegram {
		telegram[command.ActionID] = command
	}
	shared := map[string]clientux.ActionDefinition{}
	for _, def := range data.Shared {
		shared[def.ID] = def
	}
	ids := map[string]bool{}
	for id := range actions {
		ids[id] = true
	}
	for id := range telegram {
		ids[id] = true
	}
	for id := range shared {
		ids[id] = true
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)

	rows := make([][]string, 0, len(ordered))
	for _, id := range ordered {
		action, hasAction := actions[id]
		command, hasCommand := telegram[id]
		def, hasShared := shared[id]
		summary := ""
		if hasShared {
			summary = def.Summary
		}
		if summary == "" && hasAction {
			summary = action.Summary
		}
		if summary == "" && hasCommand {
			summary = command.Summary
		}
		tuiSlash := ""
		tuiKeys := ""
		if hasAction {
			tuiSlash = slashUsage(action.Slash, action.SlashArgs, action.SlashAliases)
			tuiKeys = actionKeys(action)
		}
		telegramAliases := ""
		class := ""
		if hasCommand {
			telegramAliases = strings.Join(command.Aliases, ", ")
			class = command.Class
		}
		rows = append(rows, []string{id, tuiSlash, tuiKeys, telegramAliases, class, summary})
	}
	return rows
}

func keybindingOnlyRows(actions []tui.ActionDoc) [][]string {
	type row struct {
		id      string
		keys    string
		summary string
	}
	var rows []row
	for _, action := range actions {
		keys := actionKeys(action)
		if keys == "" || action.Slash != "" {
			continue
		}
		rows = append(rows, row{id: action.ID, keys: keys, summary: action.Summary})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, []string{row.id, row.keys, row.summary})
	}
	return out
}

func commandClassRows(commands []telegrambot.CommandDoc) [][]string {
	seen := map[string]bool{}
	for _, command := range commands {
		if command.Class != "" {
			seen[command.Class] = true
		}
	}
	order := []string{"public", "session-scoped", "operator-only", "owner-only"}
	rows := make([][]string, 0, len(seen))
	for _, class := range order {
		if seen[class] {
			rows = append(rows, []string{class, commandClassDescription(class)})
			delete(seen, class)
		}
	}
	var rest []string
	for class := range seen {
		rest = append(rest, class)
	}
	sort.Strings(rest)
	for _, class := range rest {
		rows = append(rows, []string{class, commandClassDescription(class)})
	}
	return rows
}

func commandClassDescription(class string) string {
	switch class {
	case "public":
		return "No Telegram operator gate; handler and gateway checks still apply where relevant"
	case "session-scoped":
		return "Available in allowed chats, with session ownership and access enforced by handlers and gateway calls"
	case "operator-only":
		return "Requires an allowed Telegram operator"
	case "owner-only":
		return "Requires an allowed Telegram operator in a private owner chat"
	default:
		return "Unknown authorization class"
	}
}

func commandRegistryRows(data commandsReferenceData) [][]string {
	return [][]string{
		{"built-in actions", "clientux.ActionDefinitions() through commandregistry.Build()", strconv.Itoa(len(data.RegistryActions)) + " generated entries"},
		{"prompt commands", "BuildOptions.PromptCommands from local Markdown prompt commands", "runtime-only"},
		{"profiles", "BuildOptions.Profiles from configured profile directories", "runtime-only"},
		{"MCP prompts", "BuildOptions.MCPPrompts from connected MCP servers", "runtime-only"},
	}
}

func slashUsage(name, args string, aliases []string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	usage := name
	if strings.TrimSpace(args) != "" {
		usage += " " + strings.TrimSpace(args)
	}
	if len(aliases) > 0 {
		usage += " (aliases: " + strings.Join(aliases, ", ") + ")"
	}
	return usage
}

func actionKeys(action tui.ActionDoc) string {
	var keys []string
	if strings.TrimSpace(action.Keybinding) != "" {
		keys = append(keys, strings.TrimSpace(action.Keybinding))
	}
	for _, alias := range action.KeyAliases {
		if strings.TrimSpace(alias) != "" {
			keys = append(keys, strings.TrimSpace(alias))
		}
	}
	return strings.Join(keys, ", ")
}
