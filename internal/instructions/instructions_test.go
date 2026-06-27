package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestLoadProjectInstructionsRootToCwdWithOverridePreference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root rules")
	sub := filepath.Join(root, "pkg")
	mustMkdir(t, sub)
	mustWrite(t, filepath.Join(sub, "AGENTS.md"), "ignored sub regular")
	mustWrite(t, filepath.Join(sub, "AGENTS.override.md"), "sub override")
	nested := filepath.Join(sub, "nested")
	mustMkdir(t, nested)

	loaded := Load(config.Config{
		WorkspaceRoots:     []string{nested},
		ProjectDocMaxBytes: 32 * 1024,
	})
	if loaded.Directory != nested {
		t.Fatalf("Directory = %q", loaded.Directory)
	}
	if loaded.Text != "root rules\n\nsub override" {
		t.Fatalf("Text = %q", loaded.Text)
	}
	if len(loaded.Sources) != 2 {
		t.Fatalf("Sources = %#v", loaded.Sources)
	}
	if filepath.Base(loaded.Sources[1].Path) != "AGENTS.override.md" {
		t.Fatalf("override was not preferred: %#v", loaded.Sources)
	}
}

func TestLoadGlobalBeforeProjectWithSeparator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	mustWrite(t, filepath.Join(home, "AGENTS.md"), "ignored global")
	mustWrite(t, filepath.Join(home, "AGENTS.override.md"), "global override")
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "project rules")

	loaded := Load(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	})
	want := "global override" + projectDocSeparator + "project rules"
	if loaded.Text != want {
		t.Fatalf("Text = %q, want %q", loaded.Text, want)
	}
	if len(loaded.Sources) != 2 || loaded.Sources[0].Scope != "global" || loaded.Sources[1].Scope != "project" {
		t.Fatalf("Sources = %#v", loaded.Sources)
	}
}

func TestProjectDocMaxBytesCapsProjectInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "abcdef")

	loaded := Load(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 3,
	})
	if loaded.Text != "abc" {
		t.Fatalf("Text = %q", loaded.Text)
	}
	if len(loaded.Sources) != 1 || !loaded.Sources[0].Capped {
		t.Fatalf("Sources = %#v", loaded.Sources)
	}
}

func TestProjectDocFallbackFilename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "fallback rules")

	loaded := Load(config.Config{
		WorkspaceRoots:      []string{root},
		ProjectDocMaxBytes:  32 * 1024,
		ProjectDocFallbacks: []string{"CLAUDE.md"},
	})
	if loaded.Text != "fallback rules" {
		t.Fatalf("Text = %q", loaded.Text)
	}
	if filepath.Base(loaded.Sources[0].Path) != "CLAUDE.md" {
		t.Fatalf("Sources = %#v", loaded.Sources)
	}
}

func TestMessageRendersCodexStyleUserContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "project rules")

	msg, ok := Message(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	})
	if !ok {
		t.Fatal("Message returned false")
	}
	if msg.Role != protocol.RoleUser {
		t.Fatalf("Role = %q", msg.Role)
	}
	for _, want := range []string{contextStartMarker, " for " + root, contextOpenMarker, "project rules", contextEndMarker} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("message missing %q: %s", want, msg.Content)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
