package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/billyhargroveofficial/billyharness/internal/commandregistry"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/promptcommands"
)

type slashCommand struct {
	id       string
	title    string
	category string
	name     string
	args     string
	summary  string
	aliases  []string
	source   string
	kind     string
}

type slashArg struct {
	value   string
	summary string
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
	for _, command := range m.slashCommands() {
		if slashCommandMatches(command, token) && len(m.slashArgs(command)) > 0 {
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
	for _, command := range m.slashCommands() {
		name := strings.TrimPrefix(command.name, "/")
		haystack := strings.ToLower(name + " " + command.args + " " + strings.Join(command.aliases, " "))
		switch {
		case token == "":
			prefix = append(prefix, command)
		case name == token:
			exact = append(exact, command)
		case slashCommandMatches(command, "/"+token):
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
	action, ok := actionForSlash(command.name)
	if !ok || !m.actionEnabled(action) || action.args == nil {
		return nil
	}
	return action.args(m)
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
	for _, command := range m.slashCommands() {
		if slashCommandMatches(command, fields[0]) {
			return true
		}
	}
	return false
}

func (m *Model) handleSlashCommand(prompt string) (bool, tea.Cmd) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 {
		return true, nil
	}
	command := strings.ToLower(fields[0])
	rawArg := ""
	if len(fields) > 1 {
		rawArg = strings.TrimSpace(strings.TrimPrefix(prompt, fields[0]))
	}
	action, ok := actionForSlash(command)
	if ok && m.actionEnabled(action) && action.run != nil {
		return action.run(m, strings.ToLower(rawArg))
	}
	if custom, ok := m.promptCommand(command); ok {
		return m.runPromptCommand(custom, rawArg)
	}
	m.status = "unknown command " + fields[0]
	return false, nil
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

func (m *Model) loadPromptCommands() {
	commands, err := promptcommands.Load(promptcommands.LoadOptions{
		HomeDir:        config.BillyHomeDir(),
		WorkspaceRoots: m.toolPolicy.WorkspaceRoots,
		BuiltIns:       commandregistry.BuiltInPromptCommandNameSet(nil),
	})
	m.promptCommands = commands
	m.promptCommandErr = ""
	if err != nil {
		m.promptCommandErr = err.Error()
		if m.status == "" || m.status == "ready" {
			m.status = "commands error: " + err.Error()
		}
	}
}

func (m Model) slashCommands() []slashCommand {
	entries := m.commandRegistry().Entries()
	commands := make([]slashCommand, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != commandregistry.KindAction && entry.Kind != commandregistry.KindPromptCommand {
			continue
		}
		if !strings.HasPrefix(entry.Name, "/") {
			continue
		}
		commands = append(commands, slashCommand{
			id:       entry.ID,
			title:    entry.Name,
			category: entry.Category,
			name:     entry.Name,
			args:     entry.ArgumentHint,
			summary:  entry.Description,
			aliases:  append([]string(nil), entry.Aliases...),
			source:   entry.Source,
			kind:     entry.Kind,
		})
	}
	return commands
}

func (m Model) commandRegistry() commandregistry.Registry {
	profiles, err := commandregistry.ProfilesFromHome(config.BillyHomeDir(), m.currentProfile())
	if err != nil {
		profiles = []commandregistry.Profile{{Name: m.currentProfile(), Current: true, Available: false, Availability: err.Error()}}
	}
	return commandregistry.Build(commandregistry.BuildOptions{
		PromptCommands: m.promptCommands,
		Profiles:       profiles,
	})
}

func (m Model) commandsText(query string) string {
	registry := m.commandRegistry()
	query = strings.TrimSpace(query)
	if query != "" {
		return commandregistry.FormatEntries(registry.Search(query, 50))
	}
	return commandregistry.FormatEntries(registry.Entries())
}

func (m Model) profileSlashArgs() []slashArg {
	profiles, err := commandregistry.ProfilesFromHome(config.BillyHomeDir(), m.currentProfile())
	if err != nil {
		return []slashArg{{m.currentProfile(), err.Error()}}
	}
	args := make([]slashArg, 0, len(profiles))
	for _, profile := range profiles {
		summary := "profile"
		if profile.Current {
			summary = "current profile"
		}
		if !profile.Available {
			summary = profile.Availability
		}
		args = append(args, slashArg{profile.Name, summary})
	}
	return args
}

func (m Model) promptCommand(token string) (promptcommands.Command, bool) {
	name := promptcommands.NormalizeName(token)
	if name == "" {
		return promptcommands.Command{}, false
	}
	for _, command := range m.promptCommands {
		if command.Name == name {
			return command, true
		}
	}
	return promptcommands.Command{}, false
}

func (m *Model) runPromptCommand(command promptcommands.Command, arguments string) (bool, tea.Cmd) {
	expanded, hash, err := promptcommands.Expand(command, arguments, promptcommands.ExpandOptions{})
	if err != nil {
		m.status = err.Error()
		return true, nil
	}
	if strings.TrimSpace(expanded) == "" {
		m.status = "empty prompt command " + command.Name
		return true, nil
	}
	m.textarea.SetValue(expanded)
	m.promptCommandMeta = promptCommandMetadata(command, arguments, expanded, hash)
	model, cmd := m.submitPrompt(expanded)
	if updated, ok := model.(Model); ok {
		*m = updated
	}
	return true, cmd
}

func promptCommandMetadata(command promptcommands.Command, arguments, expanded, hash string) map[string]string {
	original := "/" + command.Name
	if strings.TrimSpace(arguments) != "" {
		original += " " + strings.TrimSpace(arguments)
	}
	metadata := map[string]string{
		"prompt_command":                 command.Name,
		"prompt_command_original":        original,
		"prompt_command_expanded_bytes":  strconv.Itoa(len([]byte(expanded))),
		"prompt_command_expanded_sha256": hash,
	}
	if command.Scope != "" {
		metadata["prompt_command_scope"] = command.Scope
	}
	if command.SourcePath != "" {
		metadata["prompt_command_source"] = command.SourcePath
	}
	return metadata
}

func copyPromptMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
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
		if command.source != "" {
			line = padRight(truncateRunes(label, nameW), nameW) + "  " + truncateRunes(command.summary+" ["+command.source+"]", summaryW)
		}
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
