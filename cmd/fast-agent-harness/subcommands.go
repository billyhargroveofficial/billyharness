package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
)

type subcommand struct {
	clientux.CLICommandDoc
	Run func([]string) error
}

var subcommandRuns = map[string]func([]string) error{
	"run":      runOnce,
	"chat":     chat,
	"tui":      tuiCmd,
	"telegram": telegramCmd,
	"serve":    serve,
	"help":     helpCmd,
	"mcp":      mcp,
	"config": func(args []string) error {
		return configCommand(args, os.Stdout)
	},
	"bench": benchCmd,
	"jobs":  jobsCmd,
	"sessions": func(args []string) error {
		return sessionsCmd(args)
	},
	"attachments": attachmentsCmd,
	"inspect-session": func(args []string) error {
		return sessionsInspectCommand(args, os.Stdout)
	},
	"memory":   memoryCmd,
	"commands": commandsCmd,
	"tools": func([]string) error {
		return printTools()
	},
	"doctor":  doctorCmd,
	"hygiene": hygieneCmd,
	"docsgen": docsgenCmd,
}

func helpCmd([]string) error {
	usage()
	return nil
}

var subcommands = mustBuildSubcommands()

func mustBuildSubcommands() []subcommand {
	commands, err := buildSubcommands(clientux.CLICommandDocs(), subcommandRuns)
	if err != nil {
		panic(err)
	}
	return commands
}

func buildSubcommands(docs []clientux.CLICommandDoc, runs map[string]func([]string) error) ([]subcommand, error) {
	seenDocs := map[string]bool{}
	seenTokens := map[string]string{}
	commands := make([]subcommand, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc.Name) == "" {
			return nil, fmt.Errorf("CLI command doc missing name")
		}
		if seenDocs[doc.Name] {
			return nil, fmt.Errorf("duplicate CLI command doc %q", doc.Name)
		}
		seenDocs[doc.Name] = true
		run, ok := runs[doc.Name]
		if !ok || run == nil {
			return nil, fmt.Errorf("CLI command %q missing run func", doc.Name)
		}
		for _, token := range append([]string{doc.Name}, doc.Aliases...) {
			if token == "" {
				return nil, fmt.Errorf("CLI command %q has empty alias", doc.Name)
			}
			if existing := seenTokens[token]; existing != "" {
				return nil, fmt.Errorf("CLI command token %q used by both %q and %q", token, existing, doc.Name)
			}
			seenTokens[token] = doc.Name
		}
		commands = append(commands, subcommand{CLICommandDoc: doc, Run: run})
	}
	for name := range runs {
		if !seenDocs[name] {
			return nil, fmt.Errorf("CLI command run func %q missing doc", name)
		}
	}
	return commands, nil
}

func lookupSubcommand(name string) (subcommand, bool) {
	for _, command := range subcommands {
		if command.Name == name {
			return command, true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return subcommand{}, false
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "fast-agent-harness-go")
	fmt.Fprintln(w, "default:")
	fmt.Fprintln(w, "  fast-agent-harness                 start gateway using billyharness config")
	fmt.Fprintln(w, "commands:")
	for _, command := range clientux.CLICommandDocs() {
		fmt.Fprintf(w, "  %-28s %s\n", cliCommandDisplayName(command), command.Summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run `fast-agent-harness <command> -h` for command-specific flags.")
}

func (c subcommand) displayName() string {
	return cliCommandDisplayName(c.CLICommandDoc)
}

func cliCommandDisplayName(command clientux.CLICommandDoc) string {
	if len(command.Aliases) == 0 {
		return command.Name
	}
	return command.Name + "|" + strings.Join(command.Aliases, "|")
}
