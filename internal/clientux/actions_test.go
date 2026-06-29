package clientux

import "testing"

func TestActionDefinitionsAreStableAndSlashPrefixed(t *testing.T) {
	defs := ActionDefinitions()
	seen := map[string]bool{}
	for _, def := range defs {
		if def.ID == "" {
			t.Fatal("action definition missing ID")
		}
		if seen[def.ID] {
			t.Fatalf("duplicate action definition ID %q", def.ID)
		}
		seen[def.ID] = true
		for _, alias := range append(def.SlashAliases, def.TelegramAliases...) {
			if alias == "" || alias[0] != '/' {
				t.Fatalf("action %q has non-slash alias %q", def.ID, alias)
			}
		}
		if len(def.TelegramAliases) > 0 {
			if def.TelegramCommandUsage() == "" {
				t.Fatalf("telegram action %q missing usage", def.ID)
			}
			if def.TelegramCommandSummary() == "" {
				t.Fatalf("telegram action %q missing summary", def.ID)
			}
		}
	}

	defs[0].TelegramAliases[0] = "/mutated"
	def, ok := ActionDefinitionByID("help.show")
	if !ok {
		t.Fatal("help.show definition missing")
	}
	if got := def.TelegramAliases[0]; got != "/start" {
		t.Fatalf("ActionDefinitions leaked mutable aliases, got %q", got)
	}
}
