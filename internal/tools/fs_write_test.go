package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestWriteToolEnabledByDefault(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	target := filepath.Join(root, "default-on.txt")
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": target, "content": "enabled"}),
	}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "enabled" {
		t.Fatalf("content = %q", bytes)
	}
}

func TestWriteToolEnabledCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	target := filepath.Join(root, "nested", "out.txt")
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": target, "content": "hello"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello" {
		t.Fatalf("content = %q", bytes)
	}
	if result.Metadata["display_group"] != "filesystem" ||
		result.Metadata["display_path"] != target ||
		result.Metadata["display_target"] != "out.txt" ||
		result.Metadata["display_collapse_default"] != true ||
		result.Metadata["retry_semantics"] != "overwrite_replay_safe" ||
		anyInt64(result.Metadata["bytes_written"]) != 5 {
		t.Fatalf("write metadata = %#v", result.Metadata)
	}
}

func TestFSMakeDirEnabledAndRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_make_dir",
		Arguments: rawArgs(map[string]any{"path": "nested/dir"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "nested", "dir")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%v err=%v", info, err)
	}
	if result.Metadata["display_group"] != "filesystem" ||
		result.Metadata["display_target"] != "dir" ||
		result.Metadata["retry_semantics"] != "idempotent_mkdir_all" ||
		result.Metadata["existed"] != false {
		t.Fatalf("mkdir metadata = %#v", result.Metadata)
	}
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_make_dir",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(t.TempDir(), "outside")}),
	}); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func TestFSWriteRejectsSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	if err := os.WriteFile(target, []byte("safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	for _, args := range []map[string]any{
		{"path": "link.txt", "content": "overwrite\n"},
		{"path": "link.txt", "content": "append\n", "append": true},
	} {
		_, err := registry.Call(context.Background(), protocol.ToolCall{
			Name:      "fs_write_file",
			Arguments: rawArgs(args),
		})
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink write rejection for args %#v, got %v", args, err)
		}
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "safe\n" {
		t.Fatalf("symlink target mutated: %q", got)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink should remain in place: %v", err)
	}
}
