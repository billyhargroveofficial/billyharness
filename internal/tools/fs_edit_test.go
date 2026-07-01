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

func TestFSEditFileAppliesExactEditsAtomically(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	original := "alpha beta beta\ngamma\n"
	if err := os.WriteFile(target, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_edit_file",
		Arguments: rawArgs(map[string]any{
			"path":            "note.txt",
			"expected_sha256": sha256Hex([]byte(original)),
			"edits": []map[string]any{
				{"old_string": "alpha", "new_string": "ALPHA"},
				{"old_string": "beta", "new_string": "BETA", "replace_all": true},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	edited, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(edited) != "ALPHA BETA BETA\ngamma\n" {
		t.Fatalf("edited content = %q", edited)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v err=%v, want 0640", info, err)
	}
	if strings.Contains(result.Content, "ALPHA BETA") {
		t.Fatalf("result leaked file content: %q", result.Content)
	}
	if anyInt64(result.Metadata["edit_count"]) != 2 ||
		anyInt64(result.Metadata["replacements"]) != 3 ||
		result.Metadata["before_sha256"] != sha256Hex([]byte(original)) ||
		result.Metadata["after_sha256"] != sha256Hex(edited) {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestFSEditFileFailuresDoNotMutateFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	original := "one two two\n"
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "ambiguous",
			args: map[string]any{"path": "note.txt", "edits": []map[string]any{{"old_string": "two", "new_string": "TWO"}}},
			want: "matched 2 times",
		},
		{
			name: "missing",
			args: map[string]any{"path": "note.txt", "edits": []map[string]any{{"old_string": "absent", "new_string": "present"}}},
			want: "not found",
		},
		{
			name: "unchanged",
			args: map[string]any{"path": "note.txt", "edits": []map[string]any{{"old_string": "one", "new_string": "one"}}},
			want: "must differ",
		},
		{
			name: "hash mismatch",
			args: map[string]any{"path": "note.txt", "expected_sha256": strings.Repeat("0", 64), "edits": []map[string]any{{"old_string": "one", "new_string": "ONE"}}},
			want: "expected_sha256 mismatch",
		},
		{
			name: "second edit fails after first would apply",
			args: map[string]any{"path": "note.txt", "edits": []map[string]any{{"old_string": "one", "new_string": "ONE"}, {"old_string": "missing", "new_string": "x"}}},
			want: "not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := registry.Call(context.Background(), protocol.ToolCall{Name: "fs_edit_file", Arguments: rawArgs(tt.args)})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			bytes, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(bytes) != original {
				t.Fatalf("file mutated after failure: %q", bytes)
			}
		})
	}
}

func TestFSEditFilePolicyAndParallelMetadata(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_edit_file",
		Arguments: rawArgs(map[string]any{"path": "note.txt", "edits": []map[string]any{{"old_string": "hello", "new_string": "hi"}}}),
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected policy denial, got result=%#v err=%v", result, err)
	}
	bytes, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(bytes) != "hello\n" {
		t.Fatalf("policy-denied edit mutated file: %q", bytes)
	}

	cfg.AutoApproveDangerous = true
	registry = NewRegistry(cfg)
	meta, ok := registry.ParallelMetadata("fs_edit_file")
	if !ok || meta.Policy != ParallelPolicyExclusiveWorkspace || !meta.RequiresExclusiveWorkspace || meta.CanRunParallel() {
		t.Fatalf("fs_edit_file parallel metadata = %#v ok=%v", meta, ok)
	}
}
