package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestShellExecBackgroundOutputCursorAndKill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	defer registry.Close()

	start, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "shell_exec",
		Arguments: rawArgs(map[string]any{
			"argv":       []string{"sh", "-c", "printf start; sleep 10"},
			"cwd":        ".",
			"background": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processID, _ := start.Metadata["process_id"].(string)
	if processID == "" || !strings.Contains(start.Content, processID) {
		t.Fatalf("start result = %#v", start)
	}

	output := waitForShellOutput(t, registry, processID, 0, 64, 0, "start")
	if output.OutputRef == "" || !strings.HasPrefix(output.OutputRef, filepath.Join(home, "tool-output")) {
		t.Fatalf("output ref = %q metadata=%#v", output.OutputRef, output.Metadata)
	}
	if _, err := os.Stat(output.OutputRef); err != nil {
		t.Fatal(err)
	}
	next := anyInt64(output.Metadata["next_cursor"])
	if next <= 0 {
		t.Fatalf("next cursor = %d metadata=%#v", next, output.Metadata)
	}

	kill, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_kill",
		Arguments: rawArgs(map[string]any{"process_id": processID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kill.Content, processID) {
		t.Fatalf("kill result = %#v", kill)
	}
	waitForManagedShellExit(t, registry, processID)
}

func TestShellOutputCursorTailAndExitedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	defer registry.Close()

	start, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "shell_exec",
		Arguments: rawArgs(map[string]any{
			"argv":       []string{"sh", "-c", "printf abcdef"},
			"background": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processID := start.Metadata["process_id"].(string)
	waitForManagedShellExit(t, registry, processID)

	first, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_output",
		Arguments: rawArgs(map[string]any{"process_id": processID, "cursor": 0, "max_output_bytes": 3}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "abc" || !first.Truncated || first.OutputRef == "" || anyInt64(first.Metadata["next_cursor"]) != 3 {
		t.Fatalf("first output = %#v", first)
	}
	second, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_output",
		Arguments: rawArgs(map[string]any{"process_id": processID, "cursor": 3, "max_output_bytes": 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "def" || second.Truncated || second.Metadata["exited"] != true || anyInt64(second.Metadata["exit_code"]) != 0 {
		t.Fatalf("second output = %#v", second)
	}
	tail, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_output",
		Arguments: rawArgs(map[string]any{"process_id": processID, "tail_bytes": 2}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tail.Content != "ef" {
		t.Fatalf("tail output = %#v", tail)
	}
}

func TestRegistryCloseTerminatesManagedShellProcesses(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	start, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "sleep 10"}, "background": true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processID := start.Metadata["process_id"].(string)
	registry.Close()
	waitForManagedShellExit(t, registry, processID)
}

func waitForShellOutput(t *testing.T, registry *Registry, processID string, cursor int64, maxBytes int, tailBytes int, want string) Result {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := registry.Call(context.Background(), protocol.ToolCall{
			Name: "shell_output",
			Arguments: rawArgs(map[string]any{
				"process_id":       processID,
				"cursor":           cursor,
				"max_output_bytes": maxBytes,
				"tail_bytes":       tailBytes,
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Content, want) {
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for shell output %q", want)
	return Result{}
}

func waitForManagedShellExit(t *testing.T, registry *Registry, processID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		proc, err := registry.managedShell(processID)
		if err != nil {
			t.Fatal(err)
		}
		if proc.isExited() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to exit", processID)
}
