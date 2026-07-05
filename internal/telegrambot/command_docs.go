package telegrambot

// CommandDoc is the docs-safe projection of telegramCommandSpec. Handler
// functions remain private; docsgen and /help share this metadata view.
type CommandDoc struct {
	ActionID      string
	Aliases       []string
	Usage         string
	Summary       string
	Class         string
	BypassRunLock bool
}

func CommandDocs() []CommandDoc {
	commands := telegramCommands()
	out := make([]CommandDoc, len(commands))
	for i, command := range commands {
		out[i] = CommandDoc{
			ActionID:      command.actionID,
			Aliases:       copyCommandDocStrings(command.aliases),
			Usage:         command.usage,
			Summary:       command.summary,
			Class:         command.class.String(),
			BypassRunLock: command.bypassRunLock,
		}
	}
	return out
}

func copyCommandDocStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
