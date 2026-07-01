package commandregistry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/promptcommands"
)

func TestBuildRegistryIncludesActionsPromptCommandsProfilesAndMCPPrompts(t *testing.T) {
	home := t.TempDir()
	profileDir := filepath.Join(home, "profiles", "teacher")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "profile.toml"), []byte(`name = "teacher"`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := ProfilesFromHome(home, "teacher")
	if err != nil {
		t.Fatal(err)
	}
	registry := Build(BuildOptions{
		Actions: []clientux.ActionDefinition{{
			ID:           "context.show",
			Category:     "runtime",
			Slash:        "/context",
			SlashAliases: []string{"/ctx"},
			Summary:      "show active context",
		}},
		PromptCommands: []promptcommands.Command{{
			Name:         "review",
			Description:  "review a target",
			ArgumentHint: "<path>",
			SourcePath:   filepath.Join(home, "commands", "review.md"),
			Scope:        "home",
		}},
		Profiles: profiles,
		MCPPrompts: []mcpclient.Prompt{{
			Server:      "notes",
			Name:        "summarize",
			Description: "summarize notes",
			Arguments: []mcpclient.PromptArgument{
				{Name: "topic", Required: true},
				{Name: "style"},
			},
		}},
	})

	entries := registry.Entries()
	assertEntry(t, entries, "/context", KindAction, "builtin", true, "")
	prompt := assertEntry(t, entries, "/review", KindPromptCommand, "home", true, "<path>")
	if prompt.SourcePath == "" {
		t.Fatalf("prompt command missing source path: %#v", prompt)
	}
	profile := assertEntry(t, entries, "/profile teacher", KindProfile, "profile", true, "")
	if !profile.Current {
		t.Fatalf("current profile not marked current: %#v", profile)
	}
	mcp := assertEntry(t, entries, "mcp:notes/summarize", KindMCPPrompt, "mcp", false, "<topic> [style]")
	if !strings.Contains(mcp.Availability, "metadata only") || mcp.MCPServer != "notes" || mcp.MCPName != "summarize" {
		t.Fatalf("MCP prompt metadata = %#v", mcp)
	}
}

func TestRegistrySearchAndFormattingExposeSourceLabels(t *testing.T) {
	registry := Build(BuildOptions{
		Actions: []clientux.ActionDefinition{{ID: "model.set", Category: "runtime", Slash: "/model", SlashArgs: "flash|pro", Summary: "switch model"}},
		PromptCommands: []promptcommands.Command{{
			Name:        "debug",
			Description: "debug a failure",
			Scope:       "workspace",
		}},
		Profiles: []Profile{{Name: "missing", Available: false, Availability: "profile files not found"}},
	})
	matches := registry.Search("debug", 5)
	if len(matches) != 1 || matches[0].Name != "/debug" || matches[0].Source != "workspace" {
		t.Fatalf("debug search = %#v", matches)
	}
	matches = registry.Search("model", 1)
	if len(matches) != 1 || matches[0].Name != "/model" {
		t.Fatalf("model search limit = %#v", matches)
	}
	formatted := FormatEntries(registry.Search("missing", 5))
	if !strings.Contains(formatted, "/profile missing [profile/profile]") || !strings.Contains(formatted, "profile files not found") {
		t.Fatalf("formatted missing profile = %q", formatted)
	}
	if FormatEntries(nil) != "No commands found." {
		t.Fatalf("empty format = %q", FormatEntries(nil))
	}
}

func TestProfilesFromHomeIncludesDefaultCurrentAndFilesystemProfiles(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "profiles", "teacher"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "profiles", "teacher", "SOUL.md"), []byte("teach"), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := ProfilesFromHome(home, "missing")
	if err != nil {
		t.Fatal(err)
	}
	defaultProfile := findProfile(t, profiles, "billy")
	if !defaultProfile.Available {
		t.Fatalf("default profile unavailable: %#v", defaultProfile)
	}
	teacher := findProfile(t, profiles, "teacher")
	if !teacher.Available || teacher.SourcePath == "" {
		t.Fatalf("teacher profile = %#v", teacher)
	}
	missing := findProfile(t, profiles, "missing")
	if missing.Available || !missing.Current || !strings.Contains(missing.Availability, "not found") {
		t.Fatalf("missing profile = %#v", missing)
	}
}

func assertEntry(t *testing.T, entries []Entry, name, kind, source string, available bool, argumentHint string) Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name && entry.Kind == kind && entry.Source == source {
			if entry.Available != available {
				t.Fatalf("%s availability = %v, want %v: %#v", name, entry.Available, available, entry)
			}
			if entry.ArgumentHint != argumentHint {
				t.Fatalf("%s argument hint = %q, want %q: %#v", name, entry.ArgumentHint, argumentHint, entry)
			}
			return entry
		}
	}
	t.Fatalf("missing entry %s kind=%s source=%s in %#v", name, kind, source, entries)
	return Entry{}
}

func findProfile(t *testing.T, profiles []Profile, name string) Profile {
	t.Helper()
	for _, profile := range profiles {
		if profile.Name == name {
			return profile
		}
	}
	t.Fatalf("missing profile %q in %#v", name, profiles)
	return Profile{}
}
