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

func TestShellProcessesListIncludesDashboardMetadata(t *testing.T) {
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
			"argv":       []string{"sh", "-c", "printf 'ready http://127.0.0.1:4321\\n'; sleep 10"},
			"background": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processID := start.Metadata["process_id"].(string)
	output := waitForShellOutput(t, registry, processID, 0, 512, 0, "4321")
	if output.OutputRef == "" {
		t.Fatalf("expected output ref from shell_output: %#v", output)
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_processes",
		Arguments: rawArgs(map[string]any{"include_exited": true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := list.Metadata["managed_processes"].(protocol.ManagedProcessList)
	if !ok {
		t.Fatalf("managed process metadata = %#v", list.Metadata["managed_processes"])
	}
	if status.Running != 1 || len(status.Processes) != 1 {
		t.Fatalf("process list = %#v content=%s", status, list.Content)
	}
	proc := status.Processes[0]
	if proc.ID != processID || !proc.Running || proc.ElapsedMS < 0 || proc.NextCursor <= 0 {
		t.Fatalf("process status = %#v", proc)
	}
	if proc.OutputRef != output.OutputRef || !strings.Contains(proc.OutputTailPreview, "4321") {
		t.Fatalf("process output metadata = %#v output=%#v", proc, output)
	}
	if !containsInt(proc.DetectedPorts, 4321) || !containsString(proc.DetectedURLs, "http://127.0.0.1:4321") {
		t.Fatalf("detected endpoints = ports %#v urls %#v content=%s", proc.DetectedPorts, proc.DetectedURLs, list.Content)
	}
	for _, want := range []string{processID, "running", "ports=4321", "output_ref=", "cursor="} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("process list missing %q:\n%s", want, list.Content)
		}
	}
	if list.Metadata["display_group"] != "shell_processes" || list.Metadata["collapse_default"] != true {
		t.Fatalf("display metadata = %#v", list.Metadata)
	}

	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_kill",
		Arguments: rawArgs(map[string]any{"process_id": processID}),
	}); err != nil {
		t.Fatal(err)
	}
	waitForManagedShellExit(t, registry, processID)
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

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
