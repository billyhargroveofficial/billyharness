package telegrambot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/commandregistry"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/memory"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
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

type gatewaySessionUndoer interface {
	UndoSession(context.Context, string, string) (gatewayapi.SessionUndoResponse, error)
	RedoSession(context.Context, string) (gatewayapi.SessionUndoResponse, error)
}

type managedProcessReporter interface {
	ProcessStatus(context.Context) (string, error)
}

type telegramCommandHandler func(*Bot, context.Context, Message, ChatScope, string)

type telegramCommandSpec struct {
	actionID      string
	aliases       []string
	usage         string
	summary       string
	class         telegramCommandClass
	bypassRunLock bool
	handler       telegramCommandHandler
}

type authCommandSafetyPolicy struct {
	SecretBearing                bool
	RequireDeletionBeforePersist bool
	RequirePrivateOwnerChat      bool
}

var authCommandSafetyPolicies = map[string]authCommandSafetyPolicy{
	"deepseek": {SecretBearing: true, RequireDeletionBeforePersist: true, RequirePrivateOwnerChat: true},
	"api":      {SecretBearing: true, RequireDeletionBeforePersist: true, RequirePrivateOwnerChat: true},
	"key":      {SecretBearing: true, RequireDeletionBeforePersist: true, RequirePrivateOwnerChat: true},
}

func telegramCommands() []telegramCommandSpec {
	return []telegramCommandSpec{
		telegramActionCommand("help.show", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleHelpCommand,
		}),
		telegramActionCommand("commands.search", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleCommandsCommand,
		}),
		telegramActionCommand("chat.new", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleNewCommand,
		}),
		telegramActionCommand("chat.resume", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleResumeCommand,
		}),
		telegramActionCommand("chat.fork", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleForkCommand,
		}),
		telegramActionCommand("status.show", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleStatusCommand,
		}),
		telegramActionCommand("model.set", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleModelCommand,
		}),
		telegramActionCommand("models.list", telegramCommandSpec{
			bypassRunLock: true,
			handler:       (*Bot).handleModelsCommand,
		}),
		telegramActionCommand("profile.set", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleProfileCommand,
		}),
		telegramActionCommand("reasoning.set", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleReasoningCommand,
		}),
		telegramActionCommand("access.mode", telegramCommandSpec{
			class:   telegramCommandSessionScoped,
			handler: (*Bot).handleModeCommand,
		}),
		telegramActionCommand("mcp.show", telegramCommandSpec{
			class:   telegramCommandOperatorOnly,
			handler: (*Bot).handleMCPCommand,
		}),
		telegramActionCommand("processes.show", telegramCommandSpec{
			class:         telegramCommandOperatorOnly,
			bypassRunLock: true,
			handler:       (*Bot).handleProcessesCommand,
		}),
		telegramActionCommand("tool.view", telegramCommandSpec{
			class:         telegramCommandSessionScoped,
			bypassRunLock: true,
			handler:       (*Bot).handleToolViewCommand,
		}),
		telegramActionCommand("config.show", telegramCommandSpec{
			class:         telegramCommandOperatorOnly,
			bypassRunLock: true,
			handler:       (*Bot).handleConfigCommand,
		}),
		telegramActionCommand("context.show", telegramCommandSpec{
			class:         telegramCommandSessionScoped,
			bypassRunLock: true,
			handler:       (*Bot).handleContextCommand,
		}),
		telegramActionCommand("memory.manage", telegramCommandSpec{
			class:         telegramCommandOperatorOnly,
			bypassRunLock: true,
			handler:       (*Bot).handleMemoryCommand,
		}),
		telegramActionCommand("diff.preview", telegramCommandSpec{
			class:         telegramCommandSessionScoped,
			bypassRunLock: true,
			handler:       (*Bot).handleDiffCommand,
		}),
		telegramActionCommand("undo.apply", telegramCommandSpec{
			class:         telegramCommandOperatorOnly,
			bypassRunLock: true,
			handler:       (*Bot).handleUndoCommand,
		}),
		telegramActionCommand("redo.apply", telegramCommandSpec{
			class:         telegramCommandOperatorOnly,
			bypassRunLock: true,
			handler:       (*Bot).handleRedoCommand,
		}),
		telegramActionCommand("auth.configure", telegramCommandSpec{
			class:         telegramCommandOwnerOnly,
			bypassRunLock: true,
			handler:       (*Bot).handleAuthCommand,
		}),
		telegramActionCommand("run.cancel", telegramCommandSpec{
			class:         telegramCommandSessionScoped,
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
	if err := b.authorizeCommand(msg, spec); err != nil {
		_ = b.sendPlain(ctx, msg, err.Error())
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
	for _, command := range CommandDocs() {
		if command.Usage == "" {
			continue
		}
		line := "<code>" + esc(command.Usage) + "</code>"
		if command.Summary != "" {
			line += " " + esc(command.Summary)
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

func (b *Bot) handleCommandsCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	current := fallback(state.Profile, b.opts.Profile)
	profiles, err := commandregistry.ProfilesFromHome(config.BillyHomeDir(), current)
	if err != nil {
		profiles = []commandregistry.Profile{{Name: current, Current: true, Available: false, Availability: err.Error()}}
	}
	registry := commandregistry.Build(commandregistry.BuildOptions{Profiles: profiles})
	query := strings.TrimSpace(arg)
	var entries []commandregistry.Entry
	if query != "" {
		entries = registry.Search(query, 50)
	} else {
		entries = registry.Entries()
	}
	_ = b.sendPlain(ctx, msg, commandregistry.FormatEntries(entries))
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

func (b *Bot) handleStatusCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	runtime := b.activeRuntimeStatus(b.gatewayScopedContext(ctx, msg, state), state)
	switch view := strings.TrimSpace(arg); view {
	case "":
		_ = b.sendHTML(ctx, msg, StatusHTMLWithRuntime(state, b.opts, runtime))
	case "debug":
		_ = b.sendHTML(ctx, msg, StatusDebugHTMLWithRuntime(state, b.opts, runtime))
	default:
		_ = b.sendPlain(ctx, msg, "Unknown status view "+view)
	}
}

func (b *Bot) activeRuntimeStatus(ctx context.Context, state ChatState) gatewayapi.SessionStatus {
	if strings.TrimSpace(state.SessionID) == "" {
		return gatewayapi.SessionStatus{}
	}
	reporter, ok := b.harness.(sessionStatusReporter)
	if !ok {
		return gatewayapi.SessionStatus{}
	}
	status, err := reporter.SessionStatus(ctx, state.SessionID)
	if err != nil {
		return gatewayapi.SessionStatus{}
	}
	return status
}

func (b *Bot) handleModelCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if arg == "" {
		_ = b.sendPlain(ctx, msg, "Current model: "+modelWithCapability(fallback(state.Model, b.opts.Model)))
		return
	}
	state.Model = modelAlias(arg)
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	_ = b.sendPlain(ctx, msg, "Model: "+modelWithCapability(state.Model))
}

func (b *Bot) handleModelsCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	// Keep arg for future provider/name filtering while preserving the shared handler signature.
	_ = arg
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	current := modelAlias(fallback(state.Model, b.opts.Model))
	lines := []string{"Known models:"}
	for _, provider := range modelinfo.Providers() {
		for _, model := range provider.Models {
			marker := " "
			if modelAlias(model) == current {
				marker = "*"
			}
			lines = append(lines, fmt.Sprintf("%s %s (%s, %s)", marker, model, provider.ID, modelinfo.InputCapabilityLabel(model)))
		}
	}
	_ = b.sendPlain(ctx, msg, strings.Join(lines, "\n"))
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
	normalized, ok := config.ParseAccessMode(arg)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Unknown access mode. Use build, guarded, or plan.")
		return
	}
	state.AccessMode = normalized
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

func (b *Bot) handleProcessesCommand(ctx context.Context, msg Message, _ ChatScope, _ string) {
	reporter, ok := b.harness.(managedProcessReporter)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Process dashboard is not available in this harness.")
		return
	}
	status, err := reporter.ProcessStatus(ctx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Process status failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, "<b>Processes</b>\n<pre>"+esc(status)+"</pre>")
}

func (b *Bot) handleToolViewCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if state.SessionID == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	body, err := b.toolViewHTML(b.gatewayScopedContext(ctx, msg, state), state.SessionID)
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
	status, err := b.harness.ContextStatus(b.gatewayScopedContext(ctx, msg, state), state.SessionID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Context status failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, "<b>Context</b>\n<pre>"+esc(status)+"</pre>")
}

func (b *Bot) handleMemoryCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	out, err := memory.RunCommand(b.memorySettings(scope), arg)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Memory failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, trimTelegram("<b>Memory</b>\n<pre>"+esc(out)+"</pre>"))
}

func (b *Bot) memorySettings(scope ChatScope) config.InstructionSettings {
	cfg := config.Default()
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	profile := fallback(state.Profile, b.opts.Profile)
	if strings.TrimSpace(profile) != "" {
		cfg.Profile = config.NormalizeProfileName(profile)
	}
	return cfg.InstructionSettings()
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
	out, err := previewer.PreviewSessionUndo(b.gatewayScopedContext(ctx, msg, state), state.SessionID, strings.TrimSpace(arg))
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Diff preview failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, formatTurnDiffPreviewHTML(out))
}

func (b *Bot) handleUndoCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	undoer, ok := b.harness.(gatewaySessionUndoer)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Undo is not available in this harness.")
		return
	}
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if state.SessionID == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	out, err := undoer.UndoSession(b.gatewayScopedContext(ctx, msg, state), state.SessionID, strings.TrimSpace(arg))
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Undo failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, formatTurnChangeApplyHTML("Undo", out))
}

func (b *Bot) handleRedoCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	undoer, ok := b.harness.(gatewaySessionUndoer)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Redo is not available in this harness.")
		return
	}
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if state.SessionID == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	out, err := undoer.RedoSession(b.gatewayScopedContext(ctx, msg, state), state.SessionID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Redo failed: "+err.Error())
		return
	}
	_ = b.sendHTML(ctx, msg, formatTurnChangeApplyHTML("Redo", out))
}

func formatTurnDiffPreviewHTML(out gatewayapi.SessionUndoResponse) string {
	body := formatTurnDiffPreviewText(out)
	return trimTelegram("<b>Turn diff</b>\n<pre>" + esc(body) + "</pre>")
}

func formatTurnChangeApplyHTML(label string, out gatewayapi.SessionUndoResponse) string {
	body := formatTurnChangeApplyText(label, out)
	return trimTelegram("<b>" + esc(label) + "</b>\n<pre>" + esc(body) + "</pre>")
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

func formatTurnChangeApplyText(label string, out gatewayapi.SessionUndoResponse) string {
	var lines []string
	if strings.TrimSpace(out.Change.ChangeID) != "" {
		lines = append(lines, toolrender.TurnChangeDetails(out.Change))
	} else if strings.TrimSpace(out.ChangeID) != "" {
		lines = append(lines, "change: "+strings.TrimSpace(out.ChangeID))
	}
	if len(out.RestoredFiles) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.ToLower(label)+" files:")
		for _, file := range out.RestoredFiles {
			lines = append(lines, "- "+file)
		}
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
		return label + " completed."
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) handleCancelCommand(ctx context.Context, msg Message, scope ChatScope, _ string) {
	key := scope.Key()
	state := b.chatState(key)
	localCancelled := b.cancelChat(key)
	if state.SessionID != "" {
		b.cancelGatewaySession(b.gatewayScopedContext(ctx, msg, state), state.SessionID)
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
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	scopedCtx := b.gatewayScopedContext(ctx, msg, state)
	sessions, err := manager.ListSessions(scopedCtx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Resume failed: "+err.Error())
		return
	}
	sessions = filterTelegramSessionsForMessage(sessions, msg)
	arg = strings.TrimSpace(arg)
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
	scopedCtx := b.gatewayScopedContext(ctx, msg, state)
	sessions, err := manager.ListSessions(scopedCtx)
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
	full, err := manager.GetSession(scopedCtx, source.ID)
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
		if !b.prepareSecretAuthCommand(ctx, msg, authCommandSafetyPolicies[strings.ToLower(fields[0])]) {
			return
		}
		status, err := b.harness.SaveDeepSeekAPIKey(ctx, apiKey)
		if err != nil {
			_ = b.sendPlain(ctx, msg, "DeepSeek auth failed: "+redactAuthErrorText(err.Error(), apiKey))
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

func (b *Bot) prepareSecretAuthCommand(ctx context.Context, msg Message, policy authCommandSafetyPolicy) bool {
	if !policy.SecretBearing {
		return true
	}
	if policy.RequirePrivateOwnerChat && !telegramPrivateChat(msg) {
		_ = b.sendPlain(ctx, msg, "Secret-bearing auth commands are only accepted in private owner chat.")
		return false
	}
	if msg.MessageID == 0 {
		_ = b.sendPlain(ctx, msg, "Secret-bearing auth command was not saved because Telegram did not provide a deletable message id. Send it in a private chat and try again.")
		return false
	}
	if policy.RequireDeletionBeforePersist {
		if err := b.delete(ctx, msg.Chat.ID, msg.MessageID); err != nil {
			_ = b.sendPlain(ctx, msg, "Secret-bearing auth command was not saved because Telegram could not delete the original message. Send it in a private chat and try again.")
			return false
		}
	}
	return true
}

func telegramChatIsPrivate(chat Chat) bool {
	chatType := strings.ToLower(strings.TrimSpace(chat.Type))
	return chatType == "" || chatType == "private"
}

func redactAuthErrorText(text string, sensitive ...string) string {
	out := text
	for _, value := range sensitive {
		value = strings.TrimSpace(value)
		if len(value) < 4 {
			continue
		}
		out = strings.ReplaceAll(out, value, "[redacted]")
	}
	return out
}
