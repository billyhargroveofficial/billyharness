package telegrambot

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
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
	return "<b>Auth</b>\n<pre>" + esc(strings.Join([]string{
		formatProviderStatusText("deepseek", status.DeepSeek),
		formatProviderStatusText("codex", status.Codex),
	}, "\n\n")) + "</pre>"
}

func formatProviderStatusText(name string, status credentials.ProviderStatus) string {
	parts := []string{name}
	if status.Configured {
		parts = append(parts, "configured")
	} else {
		parts = append(parts, "not configured")
	}
	if status.Source != "" {
		parts = append(parts, "source="+status.Source)
	}
	if status.Path != "" {
		parts = append(parts, "path="+status.Path)
	}
	if status.Mode != "" {
		parts = append(parts, "mode="+status.Mode)
	}
	if status.Refresh != "" {
		parts = append(parts, "refresh="+status.Refresh)
	}
	if status.ExpiresAt != "" {
		parts = append(parts, "expires="+status.ExpiresAt)
	}
	if status.AccountID != "" {
		parts = append(parts, "account="+status.AccountID)
	}
	return strings.Join(parts, "\n  ")
}

func StatusHTML(state ChatState, opts Options) string {
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
	return "<b>Status</b>\n" +
		"session: <code>" + esc(short(state.SessionID)) + "</code>\n" +
		"model: <code>" + esc(fallback(state.Model, opts.Model)) + "</code>\n" +
		"profile: <code>" + esc(fallback(state.Profile, opts.Profile)) + "</code>\n" +
		"access mode: <code>" + esc(config.NormalizeAccessMode(fallback(state.AccessMode, opts.AccessMode))) + "</code>\n" +
		"reasoning: <code>" + esc(fallback(state.ReasoningEffort, opts.ReasoningEffort)) + "</code>\n" +
		"agent turns: <code>" + esc(strconv.Itoa(state.AgentTurns)) + "</code>\n" +
		"tools: <code>" + esc(strconv.Itoa(state.ToolCalls)) + "</code>\n" +
		"event cursor: <code>" + esc(strconv.FormatInt(state.LastEventSeq, 10)) + "</code>\n" +
		"pending input: <code>" + esc(statusPendingInput(state)) + "</code>\n" +
		"context window: <code>" + esc(compactInt(opts.ContextWindow)) + "</code>\n" +
		"send: <code>" + esc(fmt.Sprint(opts.SendEnabled && !opts.DryRunDefault)) + "</code>\n" +
		"allowed chats: <code>" + esc(strings.Join(allowedChats, ",")) + "</code>\n" +
		"allowed users: <code>" + esc(strings.Join(allowedUsers, ",")) + "</code>"
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
