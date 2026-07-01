package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDiagnosticsParsesCommandsAndDisableOverrides(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")
	if err := os.WriteFile(first, []byte(`
[diagnostics.commands.lint]
command = "sh"
args = ["-c", "echo one"]
timeout_sec = 3
max_output_bytes = 1234
max_issues = 7
max_issues_per_file = 2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`
[diagnostics.commands.lint]
enabled = false

[diagnostics.commands.test]
command = "go"
args = ["test", "./internal/..."]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, err := LoadDiagnostics([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0].Name != "test" || commands[0].Command != "go" {
		t.Fatalf("commands = %#v", commands)
	}
	if commands[0].Timeout != defaultDiagnosticTimeout || commands[0].MaxIssues != defaultDiagnosticMaxIssues {
		t.Fatalf("defaults not applied: %#v", commands[0])
	}
}

func TestLoadDefaultDiagnosticsSettingsUsesDefaultsOrConfiguredFile(t *testing.T) {
	settings, err := LoadDefaultDiagnosticsSettings(DiagnosticsSettings{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Commands) != 1 || settings.Commands[0].Name != "go-test" || settings.Commands[0].Timeout != defaultDiagnosticTimeout {
		t.Fatalf("default settings = %#v", settings)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "diagnostics.toml")
	if err := os.WriteFile(file, []byte(`
[diagnostics.commands.fast]
command = "sh"
args = ["-c", "true"]
timeout_sec = 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err = LoadDefaultDiagnosticsSettings(DiagnosticsSettings{Enabled: true, ConfigFiles: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Commands) != 1 || settings.Commands[0].Name != "fast" || settings.Commands[0].Timeout != time.Second {
		t.Fatalf("configured settings = %#v", settings)
	}
}
