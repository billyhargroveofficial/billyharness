package tui

// ActionDoc is the docs-safe projection of actionSpec. Runtime closures stay
// private inside actionSpec; docsgen only needs stable action metadata.
type ActionDoc struct {
	ID              string
	Title           string
	Category        string
	Keybinding      string
	KeyAliases      []string
	Slash           string
	SlashArgs       string
	SlashAliases    []string
	TelegramAliases []string
	Summary         string
}

func ActionDocs() []ActionDoc {
	actions := actionRegistry()
	out := make([]ActionDoc, len(actions))
	for i, action := range actions {
		out[i] = ActionDoc{
			ID:              action.id,
			Title:           action.title,
			Category:        action.category,
			Keybinding:      action.keybinding,
			KeyAliases:      copyActionDocStrings(action.keyAliases),
			Slash:           action.slash,
			SlashArgs:       action.slashArgs,
			SlashAliases:    copyActionDocStrings(action.slashAliases),
			TelegramAliases: copyActionDocStrings(action.telegramAliases),
			Summary:         action.summary,
		}
	}
	return out
}

func copyActionDocStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
