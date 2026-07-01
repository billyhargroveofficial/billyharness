package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestInitialMessagesInjectProjectContextBeforeInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("project rules"), 0o600); err != nil {
		t.Fatal(err)
	}

	messages := InitialMessagesFromSettings(config.InstructionSettings{
		WorkspaceRoots:         []string{root},
		ProjectDocMaxBytes:     32 * 1024,
		ProjectContextMaxBytes: 2048,
	})
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if !strings.Contains(messages[1].Content, "<PROJECT_CONTEXT>") {
		t.Fatalf("project context message = %#v", messages[1])
	}
	if !strings.Contains(messages[2].Content, "# AGENTS.md instructions") {
		t.Fatalf("instructions message = %#v", messages[2])
	}
}
