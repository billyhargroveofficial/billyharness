package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
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

func TestInitialMessagesInjectMemorySummaryBeforeProjectContextAndInstructions(t *testing.T) {
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
	memoryRoot := filepath.Join(home, "memory")
	if err := os.MkdirAll(filepath.Join(memoryRoot, "topics"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "MEMORY.md"), []byte(`- type=user topic=style summary="Prefers small evidence summaries" path=topics/style.md`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "topics", "style.md"), []byte("SECRET TOPIC BODY SHOULD NOT INLINE"), 0o600); err != nil {
		t.Fatal(err)
	}

	messages := InitialMessagesFromSettings(config.InstructionSettings{
		WorkspaceRoots:         []string{root},
		ProjectDocMaxBytes:     32 * 1024,
		ProjectContextMaxBytes: 2048,
		MemoryEnabled:          true,
		MemorySummaryMaxBytes:  2048,
		MemoryIndexMaxBytes:    4096,
		MemoryTopicMaxBytes:    4096,
	})
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if !strings.Contains(messages[1].Content, "<MEMORY_CONTEXT>") || !strings.Contains(messages[1].Content, "Prefers small evidence summaries") {
		t.Fatalf("memory message = %#v", messages[1])
	}
	if strings.Contains(messages[1].Content, "SECRET TOPIC BODY") {
		t.Fatalf("memory message inlined topic body: %s", messages[1].Content)
	}
	if !strings.Contains(messages[2].Content, "<PROJECT_CONTEXT>") {
		t.Fatalf("project context message = %#v", messages[2])
	}
	if !strings.Contains(messages[3].Content, "# AGENTS.md instructions") {
		t.Fatalf("instructions message = %#v", messages[3])
	}
}

func TestRunMessagesReconcilesChangedProjectContextBeforeProvider(t *testing.T) {
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
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.WorkspaceRoots = []string{root}
	cfg.ProjectContextMaxBytes = 2048
	initial := InitialMessagesFromSettings(cfg.InstructionSettings())
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("NEW_FLAG=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := &captureProvider{}
	a := New(cfg, capture, tools.NewRegistry(cfg))
	next, err := a.RunMessages(context.Background(), append(initial, protocol.Message{Role: protocol.RoleUser, Content: "prompt"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.messages) == 0 {
		t.Fatal("provider saw no messages")
	}
	var contextMessages int
	var sawUpdate bool
	for _, msg := range capture.messages {
		if strings.Contains(msg.Content, "<PROJECT_CONTEXT>") {
			contextMessages++
			if strings.HasPrefix(msg.Content, "# Project context updated") && strings.Contains(msg.Content, "NEW_FLAG") {
				sawUpdate = true
			}
		}
	}
	if contextMessages != 1 || !sawUpdate {
		t.Fatalf("provider context messages=%d sawUpdate=%v messages=%#v", contextMessages, sawUpdate, capture.messages)
	}
	if capture.messages[len(capture.messages)-1].Content != "prompt" {
		t.Fatalf("prompt should remain last provider message: %#v", capture.messages)
	}
	if projectContextMessageCount(next) != 1 {
		t.Fatalf("returned transcript should keep one active project context: %#v", next)
	}
}

func projectContextMessageCount(messages []protocol.Message) int {
	count := 0
	for _, msg := range messages {
		if strings.Contains(msg.Content, "<PROJECT_CONTEXT>") {
			count++
		}
	}
	return count
}
