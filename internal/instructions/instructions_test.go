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

	loaded := Load(instructionSettings(config.Config{
		WorkspaceRoots:     []string{nested},
		ProjectDocMaxBytes: 32 * 1024,
	}))
	if loaded.Directory != nested {
		t.Fatalf("Directory = %q", loaded.Directory)
	}
	if loaded.Text != "root rules\n\nsub override" {
		t.Fatalf("Text = %q", loaded.Text)
	}
	if len(loaded.Sources) != 2 {
		t.Fatalf("Sources = %#v", loaded.Sources)
	}
	if loaded.Sources[0].Bytes != len("root rules") || len(loaded.Sources[0].SHA256) != 64 {
		t.Fatalf("source metadata = %#v", loaded.Sources[0])
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

	loaded := Load(instructionSettings(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	}))
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

	loaded := Load(instructionSettings(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 3,
	}))
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

	loaded := Load(instructionSettings(config.Config{
		WorkspaceRoots:      []string{root},
		ProjectDocMaxBytes:  32 * 1024,
		ProjectDocFallbacks: []string{"CLAUDE.md"},
	}))
	if loaded.Text != "fallback rules" {
		t.Fatalf("Text = %q", loaded.Text)
	}
	if filepath.Base(loaded.Sources[0].Path) != "CLAUDE.md" {
		t.Fatalf("Sources = %#v", loaded.Sources)
	}
}

func TestInstructionSourceMetadataIncludesBytesAndHash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "project rules")

	loaded := Load(instructionSettings(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	}))
	if len(loaded.Sources) != 1 || loaded.Sources[0].Bytes != len("project rules") || len(loaded.Sources[0].SHA256) != 64 {
		t.Fatalf("sources = %#v", loaded.Sources)
	}
}

func TestMessageRendersCodexStyleUserContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "project rules")

	msg, ok := Message(instructionSettings(config.Config{
		WorkspaceRoots:     []string{root},
		ProjectDocMaxBytes: 32 * 1024,
	}))
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

func TestLoadProfileInstructionFragmentsInMetadataOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	profileDir := filepath.Join(home, "profiles", "teacher")
	mustWrite(t, filepath.Join(profileDir, "profile.toml"), `
name = "teacher"
instruction_fragments = ["00-base.md", "10-extra.md"]
`)
	mustWrite(t, filepath.Join(profileDir, "00-base.md"), "base rules")
	mustWrite(t, filepath.Join(profileDir, "10-extra.md"), "extra rules")

	profile, ok := LoadProfile(instructionSettings(config.Config{Profile: "teacher"}))
	if !ok {
		t.Fatal("LoadProfile returned false")
	}
	if profile.Text != "base rules\n\nextra rules" {
		t.Fatalf("Text = %q", profile.Text)
	}
	if len(profile.Sources) != 2 ||
		filepath.Base(profile.Sources[0].Path) != "00-base.md" ||
		filepath.Base(profile.Sources[1].Path) != "10-extra.md" {
		t.Fatalf("Sources = %#v", profile.Sources)
	}
	rendered := profile.ContextualText()
	for _, want := range []string{"# Billyharness profile: teacher", "Sources:", "00-base.md", "10-extra.md", "<SOUL>", "base rules\n\nextra rules", "</SOUL>"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered profile missing %q: %s", want, rendered)
		}
	}
}

func TestLoadProfileInstructionFragmentsSkipMissingAndTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	profileDir := filepath.Join(home, "profiles", "teacher")
	mustWrite(t, filepath.Join(home, "outside.md"), "outside secret")
	mustWrite(t, filepath.Join(profileDir, "profile.toml"), `
name = "teacher"
instruction_fragments = ["missing.md", "../outside.md", "safe.md"]
`)
	mustWrite(t, filepath.Join(profileDir, "safe.md"), "safe rules")

	msg, ok := ProfileMessage(instructionSettings(config.Config{Profile: "teacher"}))
	if !ok {
		t.Fatal("ProfileMessage returned false")
	}
	if !strings.Contains(msg.Content, "safe rules") {
		t.Fatalf("profile message missing safe fragment: %s", msg.Content)
	}
	for _, blocked := range []string{"outside secret", "missing.md", "../outside.md"} {
		if strings.Contains(msg.Content, blocked) {
			t.Fatalf("profile message included blocked fragment %q: %s", blocked, msg.Content)
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

func instructionSettings(cfg config.Config) config.InstructionSettings {
	return cfg.InstructionSettings()
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
