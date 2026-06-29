package telegrambot

func normalizeAllowlistOptions(opts Options) Options {
	if opts.AllowAllChats {
		opts.RequireAllowlist = false
	} else if opts.SendEnabled && !opts.DryRunDefault {
		opts.RequireAllowlist = true
	}
	return opts
}

func (b *Bot) allowed(msg Message) bool {
	if b.opts.AllowAllChats {
		return true
	}
	if len(b.opts.AllowedChatIDs) == 0 && len(b.opts.AllowedUserIDs) == 0 {
		return !b.opts.RequireAllowlist
	}
	if b.opts.AllowedChatIDs[msg.Chat.ID] {
		return true
	}
	if msg.From != nil && b.opts.AllowedUserIDs[msg.From.ID] {
		return true
	}
	return false
}
