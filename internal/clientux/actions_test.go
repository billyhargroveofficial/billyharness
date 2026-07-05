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

func TestCLICommandDocsAreStable(t *testing.T) {
	docs := CLICommandDocs()
	seen := map[string]bool{}
	for _, doc := range docs {
		if doc.Name == "" {
			t.Fatal("CLI command doc missing name")
		}
		if doc.Summary == "" {
			t.Fatalf("CLI command %q missing summary", doc.Name)
		}
		if seen[doc.Name] {
			t.Fatalf("duplicate CLI command %q", doc.Name)
		}
		seen[doc.Name] = true
		for _, alias := range doc.Aliases {
			if alias == "" {
				t.Fatalf("CLI command %q has empty alias", doc.Name)
			}
		}
	}
	if !seen["docsgen"] {
		t.Fatal("CLI command docs missing docsgen")
	}

	for i, doc := range docs {
		if len(doc.Aliases) == 0 {
			continue
		}
		want := doc.Aliases[0]
		docs[i].Aliases[0] = "mutated"
		if got := CLICommandDocs()[i].Aliases[0]; got != want {
			t.Fatalf("CLICommandDocs leaked alias slice: got %q, want %q", got, want)
		}
		return
	}
	t.Fatal("test fixture expected at least one CLI alias")
}

func TestDoctorCheckDocsAreStable(t *testing.T) {
	docs := DoctorCheckDocs(DoctorCheckDocInput{
		DocsTargets: []string{"config", "tools"},
		ManagedServices: []DoctorManagedServiceDoc{
			{Service: "billyharness-gateway.service", Subcommand: "gateway", PIDFile: "gateway.pid"},
		},
	})
	seen := map[string]bool{}
	for _, doc := range docs {
		if doc.Name == "" {
			t.Fatal("doctor check doc missing name")
		}
		if doc.Description == "" {
			t.Fatalf("doctor check %q missing description", doc.Name)
		}
		if len(doc.Modes) == 0 {
			t.Fatalf("doctor check %q missing modes", doc.Name)
		}
		if seen[doc.Name] {
			t.Fatalf("duplicate doctor check %q", doc.Name)
		}
		seen[doc.Name] = true
	}
	for _, name := range []string{
		"config provider/model",
		"docs:config",
		"docs:tools",
		"service billyharness-gateway.service",
		"process gateway duplicates",
		"pid file gateway.pid",
		"service unit billyharness-gateway.service",
		"service journal billyharness-gateway.service",
		"gateway /ready",
	} {
		if !seen[name] {
			t.Fatalf("doctor check docs missing %q", name)
		}
	}

	docs[0].Modes[0] = "mutated"
	if got := DoctorCheckDocs(DoctorCheckDocInput{DocsTargets: []string{"config"}})[0].Modes[0]; got == "mutated" {
		t.Fatal("DoctorCheckDocs leaked modes slice")
	}
}
