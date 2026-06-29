package telegrambot

import (
	"context"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type ownerSessionCreator interface {
	CreateSessionWithOwner(context.Context, string, gatewayapi.SessionOwner) (string, error)
}

type ownerSessionForker interface {
	CreateSessionFromMessagesWithOwner(context.Context, string, []protocol.Message, gatewayapi.SessionOwner) (string, error)
}

func (b *Bot) createOwnedSession(ctx context.Context, msg Message, state ChatState) (string, error) {
	profile := fallback(state.Profile, b.opts.Profile)
	owner := b.telegramSessionOwner(msg, state)
	if creator, ok := b.harness.(ownerSessionCreator); ok {
		return creator.CreateSessionWithOwner(ctx, profile, owner)
	}
	return b.harness.CreateSession(ctx, profile)
}

func (b *Bot) forkOwnedSession(ctx context.Context, msg Message, profile string, messages []protocol.Message, state ChatState) (string, error) {
	owner := b.telegramSessionOwner(msg, state)
	if forker, ok := b.harness.(ownerSessionForker); ok {
		return forker.CreateSessionFromMessagesWithOwner(ctx, profile, messages, owner)
	}
	manager, ok := b.harness.(gatewaySessionManager)
	if !ok {
		return "", fmt.Errorf("gateway session forking is not available in this harness")
	}
	return manager.CreateSessionFromMessages(ctx, profile, messages)
}

func (b *Bot) telegramSessionOwner(msg Message, state ChatState) gatewayapi.SessionOwner {
	scope := messageChatScope(msg)
	return gatewayapi.SessionOwner{
		ClientType:       "telegram",
		TelegramChatID:   scope.ChatID,
		TelegramThreadID: scope.ThreadID,
		TelegramUserID:   scope.UserID,
		Profile:          fallback(state.Profile, b.opts.Profile),
		Model:            fallback(state.Model, b.opts.Model),
	}
}

func telegramSessionVisibleToMessage(session gatewayapi.SessionSummary, msg Message) bool {
	scope := messageChatScope(msg)
	owner := session.Owner
	if owner == (gatewayapi.SessionOwner{}) || strings.TrimSpace(owner.ClientType) == "" {
		return true
	}
	if !strings.EqualFold(owner.ClientType, "telegram") {
		return true
	}
	if owner.TelegramChatID != 0 && owner.TelegramChatID != scope.ChatID {
		return false
	}
	if owner.TelegramThreadID != 0 && owner.TelegramThreadID != scope.ThreadID {
		return false
	}
	if owner.TelegramUserID != 0 && owner.TelegramUserID != scope.UserID {
		return false
	}
	return true
}

func filterTelegramSessionsForMessage(sessions []gatewayapi.SessionSummary, msg Message) []gatewayapi.SessionSummary {
	out := sessions[:0]
	for _, session := range sessions {
		if telegramSessionVisibleToMessage(session, msg) {
			out = append(out, session)
		}
	}
	return out
}
