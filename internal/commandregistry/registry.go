package commandregistry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/promptcommands"
)

const (
	KindAction        = "action"
	KindPromptCommand = "prompt_command"
	KindMCPPrompt     = "mcp_prompt"
	KindProfile       = "profile"
)

type Entry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Source       string   `json:"source"`
	Category     string   `json:"category,omitempty"`
	Description  string   `json:"description,omitempty"`
	ArgumentHint string   `json:"argument_hint,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	Available    bool     `json:"available"`
	Availability string   `json:"availability,omitempty"`
	SourcePath   string   `json:"source_path,omitempty"`
	MCPServer    string   `json:"mcp_server,omitempty"`
	MCPName      string   `json:"mcp_name,omitempty"`
	Profile      string   `json:"profile,omitempty"`
	Current      bool     `json:"current,omitempty"`
}

type Profile struct {
	Name         string
	SourcePath   string
	Current      bool
	Available    bool
	Availability string
}

type BuildOptions struct {
	Actions        []clientux.ActionDefinition
	PromptCommands []promptcommands.Command
	Profiles       []Profile
	MCPPrompts     []mcpclient.Prompt
}

type Registry struct {
	entries []Entry
}

func Build(opts BuildOptions) Registry {
	actions := opts.Actions
	if actions == nil {
		actions = clientux.ActionDefinitions()
	}
	var entries []Entry
	for _, action := range actions {
		entry, ok := entryFromAction(action)
		if ok {
			entries = append(entries, entry)
		}
	}
	for _, command := range opts.PromptCommands {
		if entry, ok := entryFromPromptCommand(command); ok {
			entries = append(entries, entry)
		}
	}
	for _, profile := range opts.Profiles {
		if entry, ok := entryFromProfile(profile); ok {
			entries = append(entries, entry)
		}
	}
	for _, prompt := range opts.MCPPrompts {
		if entry, ok := entryFromMCPPrompt(prompt); ok {
			entries = append(entries, entry)
		}
	}
	return Registry{entries: entries}
}

func (r Registry) Entries() []Entry {
	out := make([]Entry, len(r.entries))
	for i, entry := range r.entries {
		out[i] = cloneEntry(entry)
	}
	return out
}

func (r Registry) Search(query string, limit int) []Entry {
	query = strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		entry Entry
		score int
		index int
	}
	var matches []scored
	for i, entry := range r.entries {
		score := scoreEntry(entry, query)
		if score <= 0 {
			continue
		}
		matches = append(matches, scored{entry: entry, score: score, index: i})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return entryLess(matches[i].entry, matches[j].entry)
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]Entry, len(matches))
	for i, match := range matches {
		out[i] = cloneEntry(match.entry)
	}
	return out
}

func FormatEntries(entries []Entry) string {
	if len(entries) == 0 {
		return "No commands found."
	}
	var b strings.Builder
	for _, entry := range entries {
		name := entry.Name
		if entry.ArgumentHint != "" {
			name += " " + entry.ArgumentHint
		}
		fmt.Fprintf(&b, "%s [%s/%s]", name, entry.Source, entry.Kind)
		if entry.Description != "" {
			fmt.Fprintf(&b, " - %s", entry.Description)
		}
		if !entry.Available {
			reason := strings.TrimSpace(entry.Availability)
			if reason == "" {
				reason = "unavailable"
			}
			fmt.Fprintf(&b, " (%s)", reason)
		}
		if entry.Current {
			b.WriteString(" (current)")
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func BuiltInPromptCommandNameSet(actions []clientux.ActionDefinition) map[string]bool {
	if actions == nil {
		actions = clientux.ActionDefinitions()
	}
	var names []string
	for _, action := range actions {
		names = append(names, action.Slash)
		names = append(names, action.SlashAliases...)
		names = append(names, action.TelegramAliases...)
	}
	return promptcommands.BuiltInNameSet(names)
}

func ProfilesFromHome(homeDir, current string) ([]Profile, error) {
	homeDir = strings.TrimSpace(homeDir)
	current = config.NormalizeProfileName(current)
	byName := map[string]Profile{}
	add := func(profile Profile) {
		name := config.NormalizeProfileName(profile.Name)
		if name == "" {
			return
		}
		profile.Name = name
		profile.Current = name == current
		if existing, ok := byName[name]; ok {
			if existing.Available && !profile.Available {
				profile.Available = true
				profile.SourcePath = existing.SourcePath
			}
			if profile.SourcePath == "" {
				profile.SourcePath = existing.SourcePath
			}
		}
		if profile.Available {
			profile.Availability = ""
		}
		byName[name] = profile
	}

	defaultProfile := Profile{Name: config.DefaultProfileName, Available: true}
	if homeDir != "" {
		defaultProfile.SourcePath = profileSourcePath(filepath.Join(homeDir, "profiles", config.DefaultProfileName))
	}
	add(defaultProfile)
	add(Profile{Name: current, Available: current == config.DefaultProfileName, Availability: "profile files not found"})

	if homeDir != "" {
		root := filepath.Join(homeDir, "profiles")
		entries, err := os.ReadDir(root)
		if err != nil {
			if !os.IsNotExist(err) {
				return sortedProfiles(byName), err
			}
			return sortedProfiles(byName), nil
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := config.NormalizeProfileName(entry.Name())
			if name == "" {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			sourcePath := profileSourcePath(dir)
			if sourcePath == "" {
				continue
			}
			add(Profile{Name: name, SourcePath: sourcePath, Available: true})
		}
	}
	return sortedProfiles(byName), nil
}

func entryFromAction(action clientux.ActionDefinition) (Entry, bool) {
	name := strings.TrimSpace(action.Slash)
	if name == "" && len(action.TelegramAliases) > 0 {
		name = strings.TrimSpace(action.TelegramAliases[0])
	}
	if name == "" {
		return Entry{}, false
	}
	aliases := append([]string(nil), action.SlashAliases...)
	aliases = append(aliases, action.TelegramAliases...)
	aliases = dedupeStrings(aliases, name)
	return Entry{
		ID:           "action:" + action.ID,
		Name:         name,
		Kind:         KindAction,
		Source:       "builtin",
		Category:     action.Category,
		Description:  strings.TrimSpace(action.Summary),
		ArgumentHint: strings.TrimSpace(action.SlashArgs),
		Aliases:      aliases,
		Available:    true,
	}, true
}

func entryFromPromptCommand(command promptcommands.Command) (Entry, bool) {
	name := promptcommands.NormalizeName(command.Name)
	if name == "" {
		return Entry{}, false
	}
	source := strings.TrimSpace(command.Scope)
	if source == "" {
		source = "local"
	}
	return Entry{
		ID:           "prompt:" + source + ":" + name,
		Name:         "/" + name,
		Kind:         KindPromptCommand,
		Source:       source,
		Category:     "prompt",
		Description:  strings.TrimSpace(command.Description),
		ArgumentHint: strings.TrimSpace(command.ArgumentHint),
		Available:    true,
		SourcePath:   strings.TrimSpace(command.SourcePath),
	}, true
}

func entryFromProfile(profile Profile) (Entry, bool) {
	name := config.NormalizeProfileName(profile.Name)
	if name == "" {
		return Entry{}, false
	}
	available := profile.Available
	availability := strings.TrimSpace(profile.Availability)
	if !available && availability == "" {
		availability = "profile files not found"
	}
	return Entry{
		ID:           "profile:" + name,
		Name:         "/profile " + name,
		Kind:         KindProfile,
		Source:       "profile",
		Category:     "runtime",
		Description:  "switch profile",
		Available:    available,
		Availability: availability,
		SourcePath:   strings.TrimSpace(profile.SourcePath),
		Profile:      name,
		Current:      profile.Current,
	}, true
}

func entryFromMCPPrompt(prompt mcpclient.Prompt) (Entry, bool) {
	server := strings.TrimSpace(prompt.Server)
	name := strings.TrimSpace(prompt.Name)
	if server == "" || name == "" {
		return Entry{}, false
	}
	return Entry{
		ID:           "mcp_prompt:" + server + ":" + name,
		Name:         "mcp:" + server + "/" + name,
		Kind:         KindMCPPrompt,
		Source:       "mcp",
		Category:     "prompt",
		Description:  strings.TrimSpace(prompt.Description),
		ArgumentHint: promptArgumentHint(prompt.Arguments),
		Available:    false,
		Availability: "metadata only; prompt invocation is not implemented",
		MCPServer:    server,
		MCPName:      name,
	}, true
}

func promptArgumentHint(args []mcpclient.PromptArgument) string {
	var parts []string
	for _, arg := range args {
		name := strings.TrimSpace(arg.Name)
		if name == "" {
			continue
		}
		if arg.Required {
			parts = append(parts, "<"+name+">")
		} else {
			parts = append(parts, "["+name+"]")
		}
	}
	return strings.Join(parts, " ")
}

func profileSourcePath(dir string) string {
	for _, name := range []string{"profile.toml", "SOUL.md"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func sortedProfiles(byName map[string]Profile) []Profile {
	out := make([]Profile, 0, len(byName))
	for _, profile := range byName {
		out = append(out, profile)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entryLess(entries[i], entries[j])
	})
}

func entryLess(left, right Entry) bool {
	if kindRank(left.Kind) != kindRank(right.Kind) {
		return kindRank(left.Kind) < kindRank(right.Kind)
	}
	if left.Source != right.Source {
		return left.Source < right.Source
	}
	return left.Name < right.Name
}

func kindRank(kind string) int {
	switch kind {
	case KindAction:
		return 0
	case KindPromptCommand:
		return 1
	case KindProfile:
		return 2
	case KindMCPPrompt:
		return 3
	default:
		return 9
	}
}

func scoreEntry(entry Entry, query string) int {
	if query == "" {
		return 1
	}
	fields := []string{entry.Name, entry.Description, entry.Source, entry.Kind, entry.Category, entry.ArgumentHint, entry.Profile, entry.MCPServer, entry.MCPName}
	fields = append(fields, entry.Aliases...)
	best := 0
	for _, field := range fields {
		score := scoreField(strings.ToLower(field), query)
		if score > best {
			best = score
		}
	}
	return best
}

func scoreField(field, query string) int {
	field = strings.TrimSpace(field)
	if field == "" {
		return 0
	}
	switch {
	case field == query:
		return 100
	case strings.TrimPrefix(field, "/") == strings.TrimPrefix(query, "/"):
		return 95
	case strings.HasPrefix(field, query):
		return 80
	case strings.HasPrefix(strings.TrimPrefix(field, "/"), strings.TrimPrefix(query, "/")):
		return 75
	case strings.Contains(field, query):
		return 50
	default:
		return 0
	}
}

func dedupeStrings(values []string, exclude ...string) []string {
	excluded := map[string]bool{}
	for _, value := range exclude {
		excluded[strings.TrimSpace(value)] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || excluded[value] || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneEntry(entry Entry) Entry {
	entry.Aliases = append([]string(nil), entry.Aliases...)
	return entry
}
