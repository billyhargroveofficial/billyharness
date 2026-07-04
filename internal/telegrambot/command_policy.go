package telegrambot

import "fmt"

type telegramCommandClass int

const (
	telegramCommandPublic telegramCommandClass = iota
	telegramCommandSessionScoped
	telegramCommandOperatorOnly
	telegramCommandOwnerOnly
)

func (b *Bot) authorizeCommand(msg Message, spec telegramCommandSpec) error {
	switch spec.class {
	case telegramCommandPublic, telegramCommandSessionScoped:
		return nil
	case telegramCommandOperatorOnly:
		return b.authorizeOperatorCommand(msg)
	case telegramCommandOwnerOnly:
		if !telegramPrivateChat(msg) {
			return fmt.Errorf("Command requires a private owner chat.")
		}
		return b.authorizeOperatorCommand(msg)
	default:
		return fmt.Errorf("Command is not available: unknown authorization class.")
	}
}

func (b *Bot) authorizeOperatorCommand(msg Message) error {
	if msg.From == nil {
		return fmt.Errorf("Command requires an identified Telegram operator.")
	}
	if msg.From.IsBot {
		return fmt.Errorf("Command requires a human Telegram operator.")
	}
	if !b.operatorUserAllowed(msg.From.ID) {
		return fmt.Errorf("Command requires an allowed Telegram operator.")
	}
	return nil
}

func (b *Bot) operatorUserAllowed(userID int64) bool {
	if len(b.opts.AllowedOperatorUserIDs) > 0 {
		return b.opts.AllowedOperatorUserIDs[userID]
	}
	return b.opts.AllowedUserIDs[userID]
}
