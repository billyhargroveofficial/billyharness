package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestTUIAtFilePopupInsertsExactRelativePath(t *testing.T) {
	root := t.TempDir()
	writeTUITestFile(t, filepath.Join(root, "src", "alpha.go"), "package src\nconst secretContent = true\n")
	writeTUITestFile(t, filepath.Join(root, "docs", "alpha-notes.md"), "# notes\n")
	m := newFileMentionTestModel(t, root)

	m.textarea.SetValue("open @alpha")
	cmd := m.updateFileMentionSearch()
	if cmd == nil {
		t.Fatal("expected file mention search command")
	}
	msg, ok := cmd().(fileMentionResultsMsg)
	if !ok {
		t.Fatalf("search msg = %#v", msg)
	}
	m.applyFileMentionResults(msg)
	if len(m.fileMentionResults) == 0 {
		t.Fatalf("expected file mention results")
	}
	popup := stripANSITest(m.fileMentionPopupView())
	if !strings.Contains(popup, "src/alpha.go") {
		t.Fatalf("popup missing file path:\n%s", popup)
	}
	if !m.handleFileMentionNavigation(tea.KeyPressMsg{Code: tea.KeyTab}) {
		t.Fatal("tab should insert file mention")
	}
	if got := m.textarea.Value(); got != "open src/alpha.go" {
		t.Fatalf("textarea = %q", got)
	}
	if strings.Contains(m.textarea.Value(), "secretContent") {
		t.Fatalf("file mention inserted file content: %q", m.textarea.Value())
	}
}

func TestTUIFileMentionIgnoresStaleAsyncResults(t *testing.T) {
	root := t.TempDir()
	writeTUITestFile(t, filepath.Join(root, "alpha.go"), "package main\n")
	writeTUITestFile(t, filepath.Join(root, "beta.go"), "package main\n")
	m := newFileMentionTestModel(t, root)

	m.textarea.SetValue("open @alpha")
	cmd1 := m.updateFileMentionSearch()
	if cmd1 == nil {
		t.Fatal("expected first search command")
	}
	msg1 := cmd1().(fileMentionResultsMsg)

	m.textarea.SetValue("open @beta")
	cmd2 := m.updateFileMentionSearch()
	if cmd2 == nil {
		t.Fatal("expected second search command")
	}
	msg2 := cmd2().(fileMentionResultsMsg)

	m.applyFileMentionResults(msg1)
	if len(m.fileMentionResults) != 0 || !m.fileMentionSearching {
		t.Fatalf("stale result should be ignored, results=%#v searching=%v", m.fileMentionResults, m.fileMentionSearching)
	}
	m.applyFileMentionResults(msg2)
	if len(m.fileMentionResults) == 0 || m.fileMentionResults[0].Path != "beta.go" {
		t.Fatalf("fresh results = %#v", m.fileMentionResults)
	}
}

func TestTUIFileMentionDismissAndSlashMode(t *testing.T) {
	root := t.TempDir()
	writeTUITestFile(t, filepath.Join(root, "README.md"), "# readme\n")
	m := newFileMentionTestModel(t, root)

	m.textarea.SetValue("/help @read")
	if cmd := m.updateFileMentionSearch(); cmd != nil || m.fileMentionOpen() {
		t.Fatal("slash mode should not open file mention popup")
	}

	m.textarea.SetValue("see @read")
	cmd := m.updateFileMentionSearch()
	if cmd == nil {
		t.Fatal("expected file mention search command")
	}
	m.applyFileMentionResults(cmd().(fileMentionResultsMsg))
	if !m.fileMentionOpen() {
		t.Fatal("file mention should be open before esc")
	}
	if !m.handleFileMentionNavigation(tea.KeyPressMsg{Code: tea.KeyEsc}) {
		t.Fatal("esc should dismiss file mention")
	}
	if m.fileMentionOpen() || m.fileMentionPopupView() != "" {
		t.Fatalf("file mention should be dismissed, popup=%q", m.fileMentionPopupView())
	}
}

func newFileMentionTestModel(t testing.TB, root string) Model {
	t.Helper()
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	m := NewModel(cfg, Options{})
	m.width = 100
	m.height = 30
	m.resize(false)
	return m
}

func writeTUITestFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
