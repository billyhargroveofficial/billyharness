package telegrambot

import (
	"reflect"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
)

func TestTelegramCommandMetadataDrivesHelpAndBypass(t *testing.T) {
	help := HelpHTML()
	seenAction := map[string]bool{}
	for _, spec := range telegramCommands() {
		if spec.actionID == "" {
			t.Fatalf("telegram command missing action id: %#v", spec)
		}
		def, ok := clientux.ActionDefinitionByID(spec.actionID)
		if !ok {
			t.Fatalf("telegram command action %q missing shared definition", spec.actionID)
		}
		seenAction[spec.actionID] = true
		if !reflect.DeepEqual(spec.aliases, def.TelegramAliases) {
			t.Fatalf("telegram command %q aliases = %#v, want %#v", spec.actionID, spec.aliases, def.TelegramAliases)
		}
		if spec.usage != def.TelegramCommandUsage() {
			t.Fatalf("telegram command %q usage = %q, want %q", spec.actionID, spec.usage, def.TelegramCommandUsage())
		}
		if spec.summary != def.TelegramCommandSummary() {
			t.Fatalf("telegram command %q summary = %q, want %q", spec.actionID, spec.summary, def.TelegramCommandSummary())
		}
		if spec.usage != "" && !strings.Contains(help, "<code>"+spec.usage+"</code>") {
			t.Fatalf("help is missing command usage %q:\n%s", spec.usage, help)
		}
		for _, alias := range spec.aliases {
			if got := bypassActiveRunLock(alias); got != spec.bypassRunLock {
				t.Fatalf("bypassActiveRunLock(%q) = %t, want %t", alias, got, spec.bypassRunLock)
			}
			withBotName := alias + "@billyharness_bot"
			if got := bypassActiveRunLock(withBotName); got != spec.bypassRunLock {
				t.Fatalf("bypassActiveRunLock(%q) = %t, want %t", withBotName, got, spec.bypassRunLock)
			}
		}
	}
	for _, def := range clientux.ActionDefinitions() {
		if len(def.TelegramAliases) > 0 && !seenAction[def.ID] {
			t.Fatalf("telegram aliases for action %q are not consumed by telegram command metadata", def.ID)
		}
	}
}
