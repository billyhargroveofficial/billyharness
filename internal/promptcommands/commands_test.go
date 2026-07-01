package promptcommands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPromptCommandsFromHomeAndWorkspaceSkipsBuiltIns(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeCommand(t, filepath.Join(home, "commands", "review.md"), `---
description: Review the current diff
argument_hint: [path]
---
Review $ARGUMENTS and focus on regressions.
`)
	writeCommand(t, filepath.Join(root, ".billyharness", "commands", "test.md"), `Run tests for $1.`)
	writeCommand(t, filepath.Join(root, ".billyharness", "commands", "help.md"), `Shadow help.`)

	commands, err := Load(LoadOptions{
		HomeDir:        home,
		WorkspaceRoots: []string{root},
		BuiltIns:       BuiltInNameSet([]string{"/help"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	if commands[0].Name != "review" || commands[0].Description != "Review the current diff" || commands[0].ArgumentHint != "[path]" || commands[0].Scope != "home" {
		t.Fatalf("home command = %#v", commands[0])
	}
	if commands[1].Name != "test" || commands[1].Scope != "workspace" {
		t.Fatalf("workspace command = %#v", commands[1])
	}
}

func TestExpandPromptCommandPlaceholdersAndCaps(t *testing.T) {
	command := Command{Name: "review", Template: "Review $ARGUMENTS\nfirst=$1 second=$2 missing=$9"}
	expanded, hash, err := Expand(command, "internal/tui package", ExpandOptions{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expanded, "Review internal/tui package") ||
		!strings.Contains(expanded, "first=internal/tui second=package missing=") ||
		hash == "" {
		t.Fatalf("expanded=%q hash=%q", expanded, hash)
	}
	if _, _, err := Expand(command, strings.Repeat("x", 128), ExpandOptions{MaxBytes: 16}); err == nil {
		t.Fatal("expected max bytes error")
	}
}

func TestNormalizePromptCommandName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"/Review_Code.md", "review-code"},
		{"release-note.md", "release-note"},
		{"bad$name.md", ""},
	} {
		if got := NormalizeName(tc.in); got != tc.want {
			t.Fatalf("NormalizeName(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func writeCommand(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
