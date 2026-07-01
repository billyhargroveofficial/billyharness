package promptcommands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const DefaultMaxExpandedBytes = 32 * 1024

type Command struct {
	Name         string
	Description  string
	ArgumentHint string
	Template     string
	SourcePath   string
	Scope        string
}

type LoadOptions struct {
	HomeDir        string
	WorkspaceRoots []string
	BuiltIns       map[string]bool
}

type ExpandOptions struct {
	MaxBytes int
}

var placeholderRE = regexp.MustCompile(`\$(ARGUMENTS|[1-9])`)

func Load(opts LoadOptions) ([]Command, error) {
	commands := map[string]Command{}
	var loadErrs []string
	for _, dir := range commandDirs(opts) {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			loadErrs = append(loadErrs, fmt.Sprintf("%s: %v", dir.path, err))
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				continue
			}
			path := filepath.Join(dir.path, entry.Name())
			command, err := readCommand(path, dir.scope)
			if err != nil {
				loadErrs = append(loadErrs, err.Error())
				continue
			}
			if command.Name == "" || opts.BuiltIns[command.Name] {
				continue
			}
			commands[command.Name] = command
		}
	}
	out := make([]Command, 0, len(commands))
	for _, command := range commands {
		out = append(out, command)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(loadErrs) > 0 {
		return out, errors.New(strings.Join(loadErrs, "; "))
	}
	return out, nil
}

func Expand(command Command, arguments string, opts ExpandOptions) (string, string, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxExpandedBytes
	}
	args := strings.Fields(arguments)
	expanded := placeholderRE.ReplaceAllStringFunc(command.Template, func(match string) string {
		key := strings.TrimPrefix(match, "$")
		if key == "ARGUMENTS" {
			return arguments
		}
		index := int(key[0] - '1')
		if index >= 0 && index < len(args) {
			return args[index]
		}
		return ""
	})
	expanded = strings.TrimSpace(expanded)
	if len([]byte(expanded)) > maxBytes {
		return "", "", fmt.Errorf("expanded prompt command %q is %d bytes; limit is %d", command.Name, len([]byte(expanded)), maxBytes)
	}
	sum := sha256.Sum256([]byte(expanded))
	return expanded, hex.EncodeToString(sum[:]), nil
}

func BuiltInNameSet(names []string) map[string]bool {
	out := map[string]bool{}
	for _, name := range names {
		if normalized := NormalizeName(name); normalized != "" {
			out[normalized] = true
		}
	}
	return out
}

func NormalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	value = strings.TrimSuffix(value, filepath.Ext(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return ""
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return ""
		}
	}
	return value
}

type commandDir struct {
	path  string
	scope string
}

func commandDirs(opts LoadOptions) []commandDir {
	var dirs []commandDir
	if strings.TrimSpace(opts.HomeDir) != "" {
		dirs = append(dirs, commandDir{path: filepath.Join(opts.HomeDir, "commands"), scope: "home"})
	}
	seen := map[string]bool{}
	for _, root := range opts.WorkspaceRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		dir := filepath.Join(root, ".billyharness", "commands")
		if seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, commandDir{path: dir, scope: "workspace"})
	}
	return dirs
}

func readCommand(path, scope string) (Command, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Command{}, err
	}
	name := NormalizeName(filepath.Base(path))
	if name == "" {
		return Command{}, fmt.Errorf("%s: invalid command filename", path)
	}
	description, hint, template := splitFrontmatter(string(body))
	template = strings.TrimSpace(template)
	if template == "" {
		return Command{}, fmt.Errorf("%s: command template is empty", path)
	}
	if description == "" {
		description = firstTemplateLine(template)
	}
	return Command{
		Name:         name,
		Description:  description,
		ArgumentHint: hint,
		Template:     template,
		SourcePath:   path,
		Scope:        scope,
	}, nil
}

func splitFrontmatter(body string) (description string, argumentHint string, template string) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, "---\n") {
		return "", "", body
	}
	rest := strings.TrimPrefix(body, "---\n")
	head, tail, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return "", "", body
	}
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "description":
			description = value
		case "argument_hint", "argument-hint":
			argumentHint = value
		}
	}
	return description, argumentHint, tail
}

func firstTemplateLine(template string) string {
	for _, line := range strings.Split(template, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "#"))
		if line != "" {
			if len(line) > 80 {
				line = line[:80]
			}
			return line
		}
	}
	return "custom prompt command"
}
