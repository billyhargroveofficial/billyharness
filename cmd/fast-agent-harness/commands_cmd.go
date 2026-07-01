package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/commandregistry"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/promptcommands"
)

func commandsCmd(args []string) error {
	return commandsCommand(args, nil)
}

func commandsCommand(args []string, stdout io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		return commandsListCommand(args[1:], stdout)
	case "search":
		return commandsSearchCommand(args[1:], stdout)
	case "-h", "--help", "help":
		commandsUsage(stdout)
		return nil
	default:
		commandsUsage(stdout)
		return fmt.Errorf("unknown commands command %q", args[0])
	}
}

func commandsListCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("commands list", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print command registry as JSON")
	limit := fs.Int("limit", 0, "maximum entries to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: commands list [-limit N] [-json]")
	}
	registry, err := loadCommandRegistry()
	if err != nil {
		return err
	}
	entries := registry.Entries()
	if *limit > 0 && len(entries) > *limit {
		entries = entries[:*limit]
	}
	return writeCommandEntries(stdout, entries, *jsonOut)
}

func commandsSearchCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("commands search", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print command registry matches as JSON")
	limit := fs.Int("limit", 50, "maximum entries to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return fmt.Errorf("usage: commands search [-limit N] [-json] QUERY")
	}
	registry, err := loadCommandRegistry()
	if err != nil {
		return err
	}
	return writeCommandEntries(stdout, registry.Search(query, *limit), *jsonOut)
}

func loadCommandRegistry() (commandregistry.Registry, error) {
	resolved, err := config.Resolve()
	if err != nil {
		return commandregistry.Registry{}, err
	}
	promptCommands, err := promptcommands.Load(promptcommands.LoadOptions{
		HomeDir:        config.BillyHomeDir(),
		WorkspaceRoots: resolved.Config.WorkspaceRoots,
		BuiltIns:       commandregistry.BuiltInPromptCommandNameSet(nil),
	})
	if err != nil {
		return commandregistry.Registry{}, err
	}
	profiles, err := commandregistry.ProfilesFromHome(config.BillyHomeDir(), resolved.Config.Profile)
	if err != nil {
		return commandregistry.Registry{}, err
	}
	return commandregistry.Build(commandregistry.BuildOptions{
		PromptCommands: promptCommands,
		Profiles:       profiles,
	}), nil
}

func writeCommandEntries(stdout io.Writer, entries []commandregistry.Entry, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	fmt.Fprintln(stdout, commandregistry.FormatEntries(entries))
	return nil
}

func commandsUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "usage:")
	fmt.Fprintln(stdout, "  fast-agent-harness commands list [-limit N] [-json]")
	fmt.Fprintln(stdout, "  fast-agent-harness commands search [-limit N] [-json] QUERY")
}
