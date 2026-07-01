package telegrambot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
)

type gatewaySessionManager interface {
	ListSessions(context.Context) ([]gatewayapi.SessionSummary, error)
	GetSession(context.Context, string) (gatewayapi.SessionResponse, error)
	CreateSessionFromMessages(context.Context, string, []protocol.Message) (string, error)
}

type gatewaySessionPreviewer interface {
	PreviewSessionUndo(context.Context, string, string) (gatewayapi.SessionUndoResponse, error)
}

type telegramCommandHandler func(*Bot, context.Context, Message, ChatScope, string)

type telegramCommandSpec struct {
	actionID      string
	aliases       []string
	usage         string
	summary       string
	bypassRunLock bool
	handler       telegramCommandHandler
}

func telegramCommands() []telegramCommandSpec {
	return []telegramCommandSpec{
		telegramActionCommand("help.show", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleHelpCommand,
		}),
		telegramActionCommand("chat.new", telegramCommandSpec{
			handler: (*Bot).handleNewCommand,
		}),
		telegramActionCommand("chat.resume", telegramCommandSpec{
			handler: (*Bot).handleResumeCommand,
		}),
		telegramActionCommand("chat.fork", telegramCommandSpec{
			handler: (*Bot).handleForkCommand,
		}),
		telegramActionCommand("status.show", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleStatusCommand,
		}),
		telegramActionCommand("model.set", telegramCommandSpec{
			handler: (*Bot).handleModelCommand,
		}),
		telegramActionCommand("profile.set", telegramCommandSpec{
			handler: (*Bot).handleProfileCommand,
		}),
		telegramActionCommand("reasoning.set", telegramCommandSpec{
			handler: (*Bot).handleReasoningCommand,
		}),
		telegramActionCommand("access.mode", telegramCommandSpec{
			handler: (*Bot).handleModeCommand,
		}),
		telegramActionCommand("mcp.show", telegramCommandSpec{
			handler: (*Bot).handleMCPCommand,
		}),
		telegramActionCommand("tool.view", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleToolViewCommand,
		}),
		telegramActionCommand("config.show", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleConfigCommand,
		}),
		telegramActionCommand("context.show", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleContextCommand,
		}),
		telegramActionCommand("diff.preview", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleDiffCommand,
		}),
		telegramActionCommand("auth.configure", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleAuthCommand,
		}),
		telegramActionCommand("run.cancel", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleCancelCommand,
		}),
	}
}

func telegramActionCommand(id string, spec telegramCommandSpec) telegramCommandSpec {
	def := clientux.MustActionDefinition(id)
	spec.actionID = id
	spec.aliases = append([]string{}, def.TelegramAliases...)
	spec.usage = def.TelegramCommandUsage()
	spec.summary = def.TelegramCommandSummary()
	return spec
}

func (b *Bot) handleCommand(ctx context.Context, msg Message, text string) {
	scope := messageChatScope(msg)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	cmd := normalizeTelegramCommand(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	spec, ok := telegramCommandFor(cmd)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Unknown command. Use /help.")
		return
	}
	spec.handler(b, ctx, msg, scope, arg)
}

func telegramCommandFor(cmd string) (telegramCommandSpec, bool) {
	cmd = normalizeTelegramCommand(cmd)
	for _, spec := range telegramCommands() {
		for _, alias := range spec.aliases {
			if normalizeTelegramCommand(alias) == cmd {
				return spec, true
			}
		}
	}
	return telegramCommandSpec{}, false
}

func telegramCommandBypassesRunLock(cmd string) bool {
	spec, ok := telegramCommandFor(cmd)
	return ok && spec.bypassRunLock
}

func normalizeTelegramCommand(cmd string) string {
	return strings.ToLower(strings.SplitN(strings.TrimSpace(cmd), "@", 2)[0])
}

func telegramCommandHelpHTML() string {
	lines := []string{
		"<b>Billyharness Telegram</b>",
		"Send a message to run the agent.",
		"",
		"Commands:",
	}
	for _, spec := range telegramCommands() {
		if spec.usage == "" {
			continue
		}
		line := "<code>" + esc(spec.usage) + "</code>"
		if spec.summary != "" {
			line += " " + esc(spec.summary)
		}
		lines = append(lines, line)
	}
	lines = append(lines,
		"<code>/auth</code> auth status",
		"<code>/auth codex</code> import Codex OAuth",
	)
	return strings.Join(lines, "\n")
}

func (b *Bot) handleHelpCommand(ctx context.Context, msg Message, _ ChatScope, _ string) {
	_ = b.sendHTML(ctx, msg, HelpHTML())
}

func (b *Bot) handleNewCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if state.Profile == "" {
		state.Profile = b.opts.Profile
	}
	if state.AccessMode == "" {
		state.AccessMode = b.opts.AccessMode
	}
	state.AccessMode = config.NormalizeAccessMode(state.AccessMode)
	id, err := b.createOwnedSession(ctx, msg, state)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Gateway session failed: "+err.Error())
		return
	}
	state.SessionID = id
	state.AgentTurns = 0
	state.ToolCalls = 0
	state.LastEventSeq = 0
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "New Billyharness session: "+short(id))
}

func (b *Bot) handleStatusCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	_ = b.sendHTML(ctx, msg, StatusHTML(state, b.opts))
}

func (b *Bot) handleModelCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if arg == "" {
		_ = b.sendPlain(ctx, msg, "Current model: "+fallback(state.Model, b.opts.Model))
		return
	}
	state.Model = modelAlias(arg)
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Model: "+state.Model)
}

func (b *Bot) handleProfileCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if arg == "" {
		_ = b.sendPlain(ctx, msg, "Current profile: "+fallback(state.Profile, b.opts.Profile))
		return
	}
	profile := config.NormalizeProfileName(arg)
	cfg := config.Config{Profile: profile}
	if err := cfg.ApplyProfileMetadata(); err != nil {
		_ = b.sendPlain(ctx, msg, "Profile error: "+err.Error())
		return
	}
	state.Profile = profile
	if cfg.Model != "" {
		state.Model = modelAlias(cfg.Model)
	}
	if strings.TrimSpace(cfg.ReasoningEffort) != "" {
		state.ReasoningEffort = strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort))
	}
	state.SessionID = ""
	state.AgentTurns = 0
	state.ToolCalls = 0
	state.LastEventSeq = 0
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Profile: "+state.Profile+"; next message starts a new session")
}

func (b *Bot) handleReasoningCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if arg == "" {
		_ = b.sendPlain(ctx, msg, "Current reasoning: "+fallback(state.ReasoningEffort, b.opts.ReasoningEffort))
		return
	}
	state.ReasoningEffort = strings.ToLower(strings.TrimSpace(arg))
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Reasoning: "+state.ReasoningEffort)
}

func (b *Bot) handleModeCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if state.AccessMode == "" {
		state.AccessMode = b.opts.AccessMode
	}
	if strings.TrimSpace(arg) == "" {
		_ = b.sendPlain(ctx, msg, "Current access mode: "+config.NormalizeAccessMode(state.AccessMode))
		return
	}
	mode := strings.ToLower(strings.TrimSpace(arg))
	switch mode {
	case config.AccessModeBuild, config.AccessModeGuarded, config.AccessModePlan, "safe", "readonly", "read-only", "read_only", "analysis":
	default:
		_ = b.sendPlain(ctx, msg, "Unknown access mode. Use build, guarded, or plan.")
		return
	}
	state.AccessMode = config.NormalizeAccessMode(mode)
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Access mode: "+state.AccessMode)
}

func (b *Bot) handleMCPCommand(ctx context.Context, msg Message, _ ChatScope, _ string) {
	status, err := b.harness.MCPStatus(ctx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "MCP status failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, "<b>MCP</b>\n<pre>"+esc(status)+"</pre>")
}

func (b *Bot) handleToolViewCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if state.SessionID == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	body, err := b.toolViewHTML(ctx, state.SessionID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Toolview failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, body)
}

func (b *Bot) handleConfigCommand(ctx context.Context, msg Message, _ ChatScope, _ string) {
	status, err := b.harness.ConfigStatus(ctx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Config status failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, "<b>Config</b>\n<pre>"+esc(status)+"</pre>")
}

func (b *Bot) handleContextCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if state.SessionID == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	status, err := b.harness.ContextStatus(ctx, state.SessionID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Context status failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, "<b>Context</b>\n<pre>"+esc(status)+"</pre>")
}

func (b *Bot) handleDiffCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	previewer, ok := b.harness.(gatewaySessionPreviewer)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Diff preview is not available in this harness.")
		return
	}
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if state.SessionID == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	out, err := previewer.PreviewSessionUndo(ctx, state.SessionID, strings.TrimSpace(arg))
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Diff preview failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, formatTurnDiffPreviewHTML(out))
}

func formatTurnDiffPreviewHTML(out gatewayapi.SessionUndoResponse) string {
	body := formatTurnDiffPreviewText(out)
	return trimTelegram("<b>Turn diff</b>\n<pre>" + esc(body) + "</pre>")
}

func formatTurnDiffPreviewText(out gatewayapi.SessionUndoResponse) string {
	var lines []string
	if strings.TrimSpace(out.Change.ChangeID) != "" {
		lines = append(lines, toolrender.TurnChangeDetails(out.Change))
	} else if strings.TrimSpace(out.ChangeID) != "" {
		lines = append(lines, "change: "+strings.TrimSpace(out.ChangeID))
	}
	patch := strings.TrimRight(out.Patch, "\n")
	if patch != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "preview:", patch)
	}
	if out.PatchTruncated {
		lines = append(lines, "[preview truncated]")
	}
	if len(out.Conflicts) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "conflicts:")
		for _, conflict := range out.Conflicts {
			lines = append(lines, "- "+conflict)
		}
	}
	if len(lines) == 0 {
		return "No turn diff preview is available."
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) handleCancelCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	localCancelled := b.cancelChat(key)
	if state.SessionID != "" {
		b.cancelGatewaySession(state.SessionID)
	}
	if localCancelled {
		_ = b.sendPlain(ctx, msg, "Cancelled current run.")
	} else if state.SessionID != "" {
		_ = b.sendPlain(ctx, msg, "Cancel requested.")
	} else {
		_ = b.sendPlain(ctx, msg, "No active run.")
	}
}

func (b *Bot) handleResumeCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	manager, ok := b.harness.(gatewaySessionManager)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Gateway session listing is not available in this harness.")
		return
	}
	sessions, err := manager.ListSessions(ctx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Resume failed: "+err.Error())
		return
	}
	sessions = filterTelegramSessionsForMessage(sessions, msg)
	arg = strings.TrimSpace(arg)
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if arg == "" {
		_ = b.sendHTML(ctx, msg, formatTelegramSessionListHTML(sessions, state.SessionID))
		return
	}
	session, err := resolveTelegramSession(sessions, arg)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Resume failed: "+err.Error())
		return
	}
	state.SessionID = session.ID
	state.LastEventSeq = 0
	if session.Profile != "" {
		state.Profile = session.Profile
	}
	if session.Model != "" {
		state.Model = session.Model
	}
	if session.ReasoningEffort != "" {
		state.ReasoningEffort = session.ReasoningEffort
	}
	if session.AccessMode != "" {
		state.AccessMode = config.NormalizeAccessMode(session.AccessMode)
	}
	if session.RunSeq > 0 {
		state.AgentTurns = int(session.RunSeq)
	}
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Resumed Billyharness session: "+short(session.ID))
}

func (b *Bot) handleForkCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	manager, ok := b.harness.(gatewaySessionManager)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Gateway session forking is not available in this harness.")
		return
	}
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	sourceID := strings.TrimSpace(arg)
	if sourceID == "" || strings.EqualFold(sourceID, "current") {
		sourceID = state.SessionID
	}
	if sourceID == "" {
		_ = b.sendPlain(ctx, msg, "No current session to fork. Send a message first or pass a session id.")
		return
	}
	sessions, err := manager.ListSessions(ctx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Fork failed: "+err.Error())
		return
	}
	sessions = filterTelegramSessionsForMessage(sessions, msg)
	source, err := resolveTelegramSession(sessions, sourceID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Fork failed: "+err.Error())
		return
	}
	full, err := manager.GetSession(ctx, source.ID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Fork failed: "+err.Error())
		return
	}
	if len(full.Messages) == 0 {
		_ = b.sendPlain(ctx, msg, "Fork failed: source session has no replayable messages.")
		return
	}
	profile := state.Profile
	if profile == "" {
		profile = b.opts.Profile
	}
	id, err := b.forkOwnedSession(ctx, msg, profile, full.Messages, state)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Fork failed: "+err.Error())
		return
	}
	state.SessionID = id
	state.LastEventSeq = 0
	state.AgentTurns = 0
	state.ToolCalls = 0
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Forked "+short(source.ID)+" into "+short(id))
}

func resolveTelegramSession(sessions []gatewayapi.SessionSummary, prefix string) (gatewayapi.SessionSummary, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return gatewayapi.SessionSummary{}, fmt.Errorf("session id required")
	}
	var matches []gatewayapi.SessionSummary
	for _, session := range sessions {
		if session.ID == prefix {
			return session, nil
		}
		if strings.HasPrefix(session.ID, prefix) {
			matches = append(matches, session)
		}
	}
	switch len(matches) {
	case 0:
		return gatewayapi.SessionSummary{}, fmt.Errorf("session %q not found", prefix)
	case 1:
		return matches[0], nil
	default:
		return gatewayapi.SessionSummary{}, fmt.Errorf("session prefix %q is ambiguous (%d matches)", prefix, len(matches))
	}
}

func formatTelegramSessionListHTML(sessions []gatewayapi.SessionSummary, currentID string) string {
	if len(sessions) == 0 {
		return "<b>Sessions</b>\nNo gateway sessions."
	}
	const maxSessions = 10
	lines := []string{"<b>Sessions</b>"}
	for i, session := range sessions {
		if i >= maxSessions {
			lines = append(lines, esc(fmt.Sprintf("… %d more", len(sessions)-maxSessions)))
			break
		}
		marker := " "
		if session.ID == currentID {
			marker = "*"
		}
		meta := []string{strconv.Itoa(session.MessageCount) + " msgs"}
		if session.Profile != "" {
			meta = append(meta, session.Profile)
		}
		if session.Model != "" {
			meta = append(meta, session.Model)
		}
		if session.Running {
			meta = append(meta, "running")
		}
		lines = append(lines, esc(marker+" "+short(session.ID)+" · "+strings.Join(meta, " · ")))
	}
	lines = append(lines, "", "Use <code>/resume SESSION_ID</code> or <code>/fork SESSION_ID</code>.")
	return trimTelegram(strings.Join(lines, "\n"))
}

func (b *Bot) toolViewHTML(ctx context.Context, sessionID string) (string, error) {
	renderer := NewRenderer()
	tools := NewToolProgress()
	seenRun := false
	if err := b.harness.ReplaySessionEvents(ctx, sessionID, 0, func(event protocol.Event) {
		if event.Type == protocol.EventRunStarted {
			renderer = NewRenderer()
			tools = NewToolProgress()
			seenRun = true
		}
		for _, rendered := range renderer.Apply(event) {
			if rendered.Kind == "tool" {
				tools.Add(rendered)
			}
		}
	}); err != nil {
		return "", err
	}
	if tools == nil || len(tools.lines) == 0 {
		if seenRun {
			return "<b>Toolview</b>\nNo tool calls in the last run.", nil
		}
		return "<b>Toolview</b>\nNo replayed tool calls for this session.", nil
	}
	var lines []string
	for _, line := range tools.lines {
		lines = append(lines, line.text)
	}
	if len(lines) >= 24 {
		lines = append([]string{"showing latest compact tool lines"}, lines...)
	}
	body := strings.Join(lines, "\n")
	return trimTelegram("<b>Toolview</b>\n<pre>" + esc(body) + "</pre>"), nil
}

func (b *Bot) handleAuthCommand(ctx context.Context, msg Message, _ ChatScope, arg string) {
	fields := strings.Fields(arg)
	if len(fields) == 0 || strings.EqualFold(fields[0], "status") {
		status, err := b.harness.AuthStatus(ctx)
		if err != nil {
			_ = b.sendPlain(ctx, msg, "Auth status failed: "+err.Error())
			return
		}
		_ = b.sendHTML(ctx, msg, formatAuthStatusHTML(status))
		return
	}

	switch strings.ToLower(fields[0]) {
	case "deepseek", "api", "key":
		if len(fields) < 2 {
			_ = b.sendHTML(ctx, msg, authUsageHTML())
			return
		}
		apiKey := strings.TrimSpace(strings.Join(fields[1:], ""))
		b.delete(ctx, msg.Chat.ID, msg.MessageID)
		status, err := b.harness.SaveDeepSeekAPIKey(ctx, apiKey)
		if err != nil {
			_ = b.sendPlain(ctx, msg, "DeepSeek auth failed: "+err.Error())
			return
		}
		_ = b.sendHTML(ctx, msg, "<b>Auth updated</b>\n<pre>"+esc(formatProviderStatusText("deepseek", status))+"</pre>")
	case "codex", "oauth", "chatgpt":
		status, err := b.harness.ImportCodexAuth(ctx)
		if err != nil {
			_ = b.sendPlain(ctx, msg, "Codex OAuth import failed: "+err.Error())
			return
		}
		_ = b.sendHTML(ctx, msg, "<b>Auth updated</b>\n<pre>"+esc(formatProviderStatusText("codex", status))+"</pre>")
	default:
		_ = b.sendHTML(ctx, msg, authUsageHTML())
	}
}
