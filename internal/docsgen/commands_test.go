package docsgen

import (
	"bytes"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/commandregistry"
	"github.com/billyhargroveofficial/billyharness/internal/telegrambot"
	"github.com/billyhargroveofficial/billyharness/internal/tui"
)

func TestCommandsReferenceCoversCommandSurfaces(t *testing.T) {
	output, err := GenerateCommands()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range tui.ActionDocs() {
		if !bytes.Contains(output, []byte(action.ID)) {
			t.Fatalf("commands reference missing TUI action %s", action.ID)
		}
		if action.Slash != "" && !bytes.Contains(output, []byte(action.Slash)) {
			t.Fatalf("commands reference missing TUI slash %s", action.Slash)
		}
	}
	for _, command := range telegrambot.CommandDocs() {
		for _, alias := range command.Aliases {
			if !bytes.Contains(output, []byte(alias)) {
				t.Fatalf("commands reference missing Telegram alias %s", alias)
			}
		}
	}
	if !bytes.Contains(output, []byte("/models")) {
		t.Fatal("commands reference missing /models command")
	}
}

func TestCommandsReferenceRecordsBuiltInCommandRegistryActions(t *testing.T) {
	data := commandsReferenceInput()
	registry := commandregistry.Build(commandregistry.BuildOptions{Actions: data.Shared})
	if got, want := len(data.RegistryActions), len(registry.Entries()); got != want {
		t.Fatalf("registry action entries = %d, want %d", got, want)
	}
	if got := len(commandRegistryRows(data)); got != 4 {
		t.Fatalf("command registry rows = %d, want 4", got)
	}
}
