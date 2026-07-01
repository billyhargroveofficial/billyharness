package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultDiagnosticTimeout        = 120 * time.Second
	defaultDiagnosticMaxOutputBytes = 256 * 1024
	defaultDiagnosticMaxIssues      = 100
	defaultDiagnosticMaxIssuesFile  = 20
)

type DiagnosticCommand struct {
	Name             string
	Command          string
	Args             []string
	CWD              string
	Timeout          time.Duration
	MaxOutputBytes   int
	MaxIssues        int
	MaxIssuesPerFile int
	Enabled          bool
}

type diagnosticsConfigFile struct {
	Diagnostics diagnosticsTOML `toml:"diagnostics"`
}

type diagnosticsTOML struct {
	Commands map[string]diagnosticCommandTOML `toml:"commands"`
}

type diagnosticCommandTOML struct {
	Command          string   `toml:"command"`
	Args             []string `toml:"args"`
	CWD              string   `toml:"cwd"`
	TimeoutSec       float64  `toml:"timeout_sec"`
	MaxOutputBytes   int      `toml:"max_output_bytes"`
	MaxIssues        int      `toml:"max_issues"`
	MaxIssuesPerFile int      `toml:"max_issues_per_file"`
	Enabled          *bool    `toml:"enabled"`
}

func (c *Config) LoadDefaultDiagnostics() error {
	if !c.DiagnosticsEnabled {
		c.DiagnosticsCommands = nil
		return nil
	}
	settings, err := LoadDefaultDiagnosticsSettings(c.DiagnosticsSettings())
	if err != nil {
		return err
	}
	c.DiagnosticsConfigFiles = settings.ConfigFiles
	c.DiagnosticsCommands = settings.Commands
	return nil
}

func LoadDefaultDiagnosticsSettings(settings DiagnosticsSettings) (DiagnosticsSettings, error) {
	settings = cloneDiagnosticsSettings(settings)
	if !settings.Enabled {
		settings.Commands = nil
		return settings, nil
	}
	if len(settings.Commands) > 0 {
		settings.Commands = normalizeDiagnosticCommands(settings.Commands)
		return settings, nil
	}
	files := settings.ConfigFiles
	if len(files) == 0 {
		files = DefaultDiagnosticsConfigFiles()
	}
	if len(files) > 0 {
		commands, err := LoadDiagnostics(files)
		if err != nil {
			return DiagnosticsSettings{}, err
		}
		settings.ConfigFiles = append([]string(nil), files...)
		settings.Commands = commands
	}
	if len(settings.Commands) == 0 {
		settings.Commands = DefaultDiagnosticCommands()
	}
	return settings, nil
}

func DefaultDiagnosticsConfigFiles() []string {
	path := DefaultDiagnosticsConfigFile()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{path}
}

func DefaultDiagnosticsConfigFile() string {
	return filepath.Join(BillyHomeDir(), "diagnostics.config.toml")
}

func LoadDiagnostics(files []string) ([]DiagnosticCommand, error) {
	merged := map[string]DiagnosticCommand{}
	var order []string
	for _, file := range files {
		if strings.TrimSpace(file) == "" {
			continue
		}
		var root diagnosticsConfigFile
		if _, err := toml.DecodeFile(file, &root); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(root.Diagnostics.Commands))
		for name := range root.Diagnostics.Commands {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			raw := root.Diagnostics.Commands[name]
			command, ok, err := raw.toCommand(name)
			if err != nil {
				return nil, err
			}
			if !ok {
				delete(merged, normalizeDiagnosticCommandName(name))
				order = removeString(order, normalizeDiagnosticCommandName(name))
				continue
			}
			if _, exists := merged[command.Name]; !exists {
				order = append(order, command.Name)
			}
			merged[command.Name] = command
		}
	}
	out := make([]DiagnosticCommand, 0, len(merged))
	for _, name := range order {
		if command, ok := merged[name]; ok {
			out = append(out, command)
		}
	}
	return out, nil
}

func DefaultDiagnosticCommands() []DiagnosticCommand {
	return []DiagnosticCommand{normalizeDiagnosticCommand(DiagnosticCommand{
		Name:             "go-test",
		Command:          "go",
		Args:             []string{"test", "./..."},
		Timeout:          defaultDiagnosticTimeout,
		MaxOutputBytes:   defaultDiagnosticMaxOutputBytes,
		MaxIssues:        defaultDiagnosticMaxIssues,
		MaxIssuesPerFile: defaultDiagnosticMaxIssuesFile,
		Enabled:          true,
	})}
}

func (c diagnosticCommandTOML) toCommand(name string) (DiagnosticCommand, bool, error) {
	enabled := true
	if c.Enabled != nil {
		enabled = *c.Enabled
	}
	if !enabled {
		return DiagnosticCommand{}, false, nil
	}
	command := DiagnosticCommand{
		Name:             name,
		Command:          c.Command,
		Args:             append([]string(nil), c.Args...),
		CWD:              c.CWD,
		Timeout:          time.Duration(c.TimeoutSec * float64(time.Second)),
		MaxOutputBytes:   c.MaxOutputBytes,
		MaxIssues:        c.MaxIssues,
		MaxIssuesPerFile: c.MaxIssuesPerFile,
		Enabled:          true,
	}
	command = normalizeDiagnosticCommand(command)
	if command.Name == "" {
		return DiagnosticCommand{}, false, fmt.Errorf("diagnostics.commands.%s: command name required", name)
	}
	if command.Command == "" {
		return DiagnosticCommand{}, false, fmt.Errorf("diagnostics.commands.%s: command required", name)
	}
	return command, true, nil
}

func normalizeDiagnosticCommands(commands []DiagnosticCommand) []DiagnosticCommand {
	out := make([]DiagnosticCommand, 0, len(commands))
	seen := map[string]bool{}
	for _, command := range commands {
		command = normalizeDiagnosticCommand(command)
		if command.Name == "" || command.Command == "" || seen[command.Name] || !command.Enabled {
			continue
		}
		seen[command.Name] = true
		out = append(out, command)
	}
	return out
}

func normalizeDiagnosticCommand(command DiagnosticCommand) DiagnosticCommand {
	command.Name = normalizeDiagnosticCommandName(command.Name)
	command.Command = strings.TrimSpace(command.Command)
	command.CWD = strings.TrimSpace(command.CWD)
	command.Args = cleanStringList(command.Args)
	if command.Timeout <= 0 {
		command.Timeout = defaultDiagnosticTimeout
	}
	if command.Timeout > 10*time.Minute {
		command.Timeout = 10 * time.Minute
	}
	if command.MaxOutputBytes <= 0 {
		command.MaxOutputBytes = defaultDiagnosticMaxOutputBytes
	}
	if command.MaxOutputBytes > 2*1024*1024 {
		command.MaxOutputBytes = 2 * 1024 * 1024
	}
	if command.MaxIssues <= 0 {
		command.MaxIssues = defaultDiagnosticMaxIssues
	}
	if command.MaxIssues > 500 {
		command.MaxIssues = 500
	}
	if command.MaxIssuesPerFile <= 0 {
		command.MaxIssuesPerFile = defaultDiagnosticMaxIssuesFile
	}
	if command.MaxIssuesPerFile > 100 {
		command.MaxIssuesPerFile = 100
	}
	if !command.Enabled {
		command.Enabled = true
	}
	return command
}

func normalizeDiagnosticCommandName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return strings.Trim(value, "-")
}
