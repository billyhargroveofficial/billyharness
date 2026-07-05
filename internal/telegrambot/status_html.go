package telegrambot

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func HelpHTML() string {
	return telegramCommandHelpHTML()
}

func authUsageHTML() string {
	return `<b>Auth</b>
<code>/auth</code> status
<code>/auth deepseek sk-...</code> save DeepSeek API key
<code>/auth codex</code> import Codex OAuth from local codex login`
}

func formatAuthStatusHTML(status credentials.Status) string {
	return "<b>Auth</b>\n<pre>" + esc(credentials.FormatStatusText(status)) + "</pre>"
}

func formatProviderStatusText(name string, status credentials.ProviderStatus) string {
	return credentials.FormatProviderStatusText(name, status)
}

func StatusHTML(state ChatState, opts Options) string {
	return StatusHTMLWithRuntime(state, opts, gatewayapi.SessionStatus{})
}

func StatusHTMLWithRuntime(state ChatState, opts Options, runtime gatewayapi.SessionStatus) string {
	fields := statusHTMLFields(state, opts, runtime)
	return "<b>Status</b>\n" + strings.Join(fields.short, "\n")
}

func StatusDebugHTMLWithRuntime(state ChatState, opts Options, runtime gatewayapi.SessionStatus) string {
	fields := statusHTMLFields(state, opts, runtime)
	return "<b>Status Debug</b>\n" + strings.Join(fields.debug, "\n")
}

func statusHTMLFull(state ChatState, opts Options, runtime gatewayapi.SessionStatus) string {
	fields := statusHTMLFields(state, opts, runtime)
	lines := []string{
		fields.short[0],
		fields.short[1],
		fields.short[2],
		fields.short[3],
		fields.short[4],
		fields.short[5],
		fields.debug[0],
		fields.debug[1],
		fields.debug[2],
		fields.debug[3],
		fields.short[6],
		fields.short[7],
		fields.short[8],
		fields.debug[4],
		fields.debug[5],
		fields.debug[6],
	}
	return "<b>Status</b>\n" + strings.Join(lines, "\n")
}

type statusFields struct {
	short []string
	debug []string
}

func statusHTMLFields(state ChatState, opts Options, runtime gatewayapi.SessionStatus) statusFields {
	model := fallback(state.Model, opts.Model)
	contextWindow := resolveContextWindowForModel(model, opts.ContextWindow, opts.ContextWindowSource)
	compactThreshold := resolveCompactThreshold(contextWindow.Tokens, opts.ContextCompact, opts.ContextCompactSource)
	var allowedChats []string
	for chat := range opts.AllowedChatIDs {
		allowedChats = append(allowedChats, strconv.FormatInt(chat, 10))
	}
	sort.Strings(allowedChats)
	var allowedUsers []string
	for user := range opts.AllowedUserIDs {
		allowedUsers = append(allowedUsers, strconv.FormatInt(user, 10))
	}
	sort.Strings(allowedUsers)
	if opts.AllowAllChats {
		allowedChats = []string{"all chats"}
	} else if len(allowedChats) == 0 {
		allowedChats = []string{"not configured"}
	}
	if len(allowedUsers) == 0 {
		allowedUsers = []string{"not configured"}
	}
	return statusFields{
		short: []string{
			"session: <code>" + esc(short(state.SessionID)) + "</code>",
			"selected model: <code>" + esc(modelWithCapability(model)) + "</code>",
			"active runtime model: <code>" + esc(runtimeModelLabel(runtime)) + "</code>",
			"profile: <code>" + esc(fallback(state.Profile, opts.Profile)) + "</code>",
			"access mode: <code>" + esc(config.NormalizeAccessMode(fallback(state.AccessMode, opts.AccessMode))) + "</code>",
			"reasoning: <code>" + esc(fallback(state.ReasoningEffort, opts.ReasoningEffort)) + "</code>",
			"selected context window: <code>" + esc(compactInt(contextWindow.Tokens)) + "</code>" + esc(contextWindowStatusSuffix(contextWindow.Source)),
			"selected compact threshold: <code>" + esc(compactInt(compactThreshold.Tokens)) + "</code> (" + esc(formatThresholdPercent(compactThreshold.Percent)) + ")" + esc(contextCompactStatusSuffix(compactThreshold.Source)),
			"send: <code>" + esc(fmt.Sprint(opts.SendEnabled && !opts.DryRunDefault)) + "</code>",
		},
		debug: []string{
			"agent turns: <code>" + esc(strconv.Itoa(state.AgentTurns)) + "</code>",
			"tools: <code>" + esc(strconv.Itoa(state.ToolCalls)) + "</code>",
			"event cursor: <code>" + esc(strconv.FormatInt(state.LastEventSeq, 10)) + "</code>",
			"pending input: <code>" + esc(statusPendingInput(state)) + "</code>",
			"allowed chats: <code>" + esc(strings.Join(allowedChats, ",")) + "</code>",
			"allowed users: <code>" + esc(strings.Join(allowedUsers, ",")) + "</code>",
			"allowed user scope: <code>" + esc(allowedUserScope(opts)) + "</code>",
		},
	}
}

func allowedUserScope(opts Options) string {
	if opts.AllowAllChats {
		return "all chats"
	}
	if opts.AllowUserInGroups {
		return "private and group chats"
	}
	return "private chats"
}

type compactThresholdResolution struct {
	Tokens  int64
	Percent float64
	Source  string
}

func resolveCompactThreshold(contextWindow int64, configured int, source string) compactThresholdResolution {
	source = strings.TrimSpace(source)
	if source == "override" && configured > 0 {
		out := compactThresholdResolution{Tokens: int64(configured), Source: "override"}
		if contextWindow > 0 {
			out.Percent = float64(out.Tokens) / float64(contextWindow) * 100
		}
		return out
	}
	if contextWindow <= 0 {
		if configured > 0 {
			return compactThresholdResolution{Tokens: int64(configured), Source: "fallback"}
		}
		return compactThresholdResolution{}
	}
	tokens := contextWindow * 60 / 100
	return compactThresholdResolution{
		Tokens:  tokens,
		Percent: float64(tokens) / float64(contextWindow) * 100,
		Source:  "derived",
	}
}

func formatThresholdPercent(percent float64) string {
	if percent <= 0 {
		return "unknown"
	}
	if percent < 10 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
}

func contextCompactStatusSuffix(source string) string {
	switch strings.TrimSpace(source) {
	case "override":
		return " override"
	case "fallback":
		return " fallback"
	default:
		return ""
	}
}

func runtimeModelLabel(runtime gatewayapi.SessionStatus) string {
	model := strings.TrimSpace(runtime.Model)
	if model == "" {
		return "unknown"
	}
	return modelWithCapability(model)
}

func contextWindowStatusSuffix(source string) string {
	switch strings.TrimSpace(source) {
	case "override":
		return " (override)"
	case "fallback":
		return " (fallback)"
	default:
		return ""
	}
}

func statusPendingInput(state ChatState) string {
	if strings.TrimSpace(state.PendingInputID) == "" {
		return "none"
	}
	if state.PendingUpdateID > 0 {
		return short(state.PendingInputID) + " update=" + strconv.Itoa(state.PendingUpdateID)
	}
	return short(state.PendingInputID)
}
