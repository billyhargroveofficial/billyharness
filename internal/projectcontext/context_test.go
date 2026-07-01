package projectcontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestProjectContextSnapshotDetectsBoundedLocalHints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/app\n")
	mustWrite(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest --run","build":"vite build","dev":"vite"}}`)
	mustWrite(t, filepath.Join(root, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "project rules")
	mustWrite(t, filepath.Join(root, ".env.example"), "API_TOKEN=super-secret\nSERVICE_URL=https://example.invalid\nexport PUBLIC_FLAG=true\n")
	cwd := filepath.Join(root, "cmd", "app")
	mustMkdir(t, cwd)

	settings := config.InstructionSettings{
		WorkspaceRoots:         []string{cwd},
		ProjectDocMaxBytes:     32 * 1024,
		ProjectContextMaxBytes: 2048,
	}
	snapshot := SnapshotFromSettings(settings)
	if snapshot.CWD != cwd || snapshot.GitRoot != root {
		t.Fatalf("roots = cwd:%q git:%q", snapshot.CWD, snapshot.GitRoot)
	}
	if !hasPackageManager(snapshot, "go") || !hasPackageManager(snapshot, "pnpm") {
		t.Fatalf("package managers = %#v", snapshot.PackageManagers)
	}
	if !hasCommand(snapshot, "go test ./...") || !hasCommand(snapshot, "pnpm run build") || !hasCommand(snapshot, "pnpm run test") {
		t.Fatalf("commands = %#v", snapshot.Commands)
	}
	if len(snapshot.InstructionSources) != 1 || snapshot.InstructionSources[0].Bytes == 0 || snapshot.InstructionSources[0].SHA256 == "" {
		t.Fatalf("instruction sources = %#v", snapshot.InstructionSources)
	}
	if len(snapshot.EnvFiles) != 1 || strings.Join(snapshot.EnvFiles[0].Vars, ",") != "API_TOKEN,PUBLIC_FLAG,SERVICE_URL" {
		t.Fatalf("env hints = %#v", snapshot.EnvFiles)
	}
	rendered, ok := Render(snapshot, settings.ProjectContextMaxBytes)
	if !ok {
		t.Fatal("Render returned false")
	}
	for _, want := range []string{"<PROJECT_CONTEXT>", "go test ./...", "pnpm run build", "AGENTS.md", "API_TOKEN", "SERVICE_URL"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered context missing %q:\n%s", want, rendered)
		}
	}
	for _, leaked := range []string{"super-secret", "https://example.invalid", "vitest --run", "vite build"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("rendered context leaked %q:\n%s", leaked, rendered)
		}
	}
}

func TestProjectContextRenderCapsOutput(t *testing.T) {
	snapshot := Snapshot{
		CWD: "/repo",
		Commands: []LikelyCommand{
			{Name: "test", Command: strings.Repeat("go test ./... ", 20), Source: "go.mod", Confidence: "high"},
		},
	}
	rendered, ok := Render(snapshot, 120)
	if !ok {
		t.Fatal("Render returned false")
	}
	if len(rendered) > 120 || !strings.Contains(rendered, "truncated") {
		t.Fatalf("rendered len=%d:\n%s", len(rendered), rendered)
	}
}

func TestProjectContextReconcileNoopsWhenUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/app\n")

	settings := config.InstructionSettings{
		WorkspaceRoots:         []string{root},
		ProjectDocMaxBytes:     32 * 1024,
		ProjectContextMaxBytes: 2048,
	}
	msg, ok := Message(settings)
	if !ok {
		t.Fatal("Message returned false")
	}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		msg,
		{Role: protocol.RoleUser, Content: "prompt"},
	}
	next, changed := ReconcileMessages(settings, messages)
	if changed || len(next) != len(messages) || next[1].Content != msg.Content {
		t.Fatalf("reconcile changed unchanged context changed=%v next=%#v", changed, next)
	}
}

func TestProjectContextReconcileUpdatesChangedContextOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-empty"))
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/app\n")

	settings := config.InstructionSettings{
		WorkspaceRoots:         []string{root},
		ProjectDocMaxBytes:     32 * 1024,
		ProjectContextMaxBytes: 2048,
	}
	msg, ok := Message(settings)
	if !ok {
		t.Fatal("Message returned false")
	}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		msg,
		{Role: protocol.RoleUser, Content: "prompt"},
	}
	mustWrite(t, filepath.Join(root, ".env.example"), "NEW_FLAG=true\n")
	updated, changed := ReconcileMessages(settings, messages)
	if !changed {
		t.Fatal("expected changed context to reconcile")
	}
	if len(updated) != len(messages) || !strings.HasPrefix(updated[1].Content, updateMarker) || !strings.Contains(updated[1].Content, "NEW_FLAG") {
		t.Fatalf("updated messages = %#v", updated)
	}
	if ContentHash(updated[1].Content) == ContentHash(msg.Content) {
		t.Fatalf("context hash did not change")
	}
	reused, changed := ReconcileMessages(settings, updated)
	if changed || len(reused) != len(updated) || reused[1].Content != updated[1].Content {
		t.Fatalf("second reconcile should reuse stored epoch changed=%v reused=%#v", changed, reused)
	}
}

func TestProjectContextReconcileObservationFailurePreservesPriorContext(t *testing.T) {
	old := protocol.Message{Role: protocol.RoleUser, Content: "# Project context\n<PROJECT_CONTEXT>\ncwd: /repo\n</PROJECT_CONTEXT>"}
	messages := []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		old,
		{Role: protocol.RoleUser, Content: "prompt"},
	}
	next, changed := ReconcileMessages(config.InstructionSettings{ProjectContextMaxBytes: 0}, messages)
	if changed || next[1].Content != old.Content {
		t.Fatalf("disabled observation should preserve context changed=%v next=%#v", changed, next)
	}
}

func hasPackageManager(snapshot Snapshot, name string) bool {
	for _, pm := range snapshot.PackageManagers {
		if pm.Name == name {
			return true
		}
	}
	return false
}

func hasCommand(snapshot Snapshot, command string) bool {
	for _, candidate := range snapshot.Commands {
		if candidate.Command == command {
			return true
		}
	}
	return false
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
