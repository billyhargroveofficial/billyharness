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

func TestFSGrepRegexContextBoundsAndSkips(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "app.go"), "package main\nalpha\nNeedle 123\nomega\n")
	writeTestFile(t, filepath.Join(root, "note.txt"), "Needle 999 should be skipped by include\n")
	writeTestFile(t, filepath.Join(root, ".env"), "Needle secret\n")
	writeTestFile(t, filepath.Join(root, "bin.go"), "Needle\x00binary\n")
	writeTestFile(t, filepath.Join(root, "large.go"), strings.Repeat("x", maxFSGrepFileBytes+1))
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "sub", "other.go"), "needle 456\n")

	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_grep",
		Arguments: rawArgs(map[string]any{
			"pattern":          `needle\s+\d+`,
			"path":             ".",
			"include":          "*.go",
			"case_insensitive": true,
			"context":          1,
			"limit":            1,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app.go-2- alpha", "app.go:3:1: Needle 123", "app.go-4- omega", "...[truncated; next_offset=1]"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("grep output missing %q:\n%s", want, result.Content)
		}
	}
	for _, notWant := range []string{".env", "note.txt", "sub/other.go:1"} {
		if strings.Contains(result.Content, notWant) {
			t.Fatalf("grep output should not contain %q:\n%s", notWant, result.Content)
		}
	}
	if !result.Truncated ||
		anyInt64(result.Metadata["returned_matches"]) != 1 ||
		anyInt64(result.Metadata["next_offset"]) != 1 ||
		anyInt64(result.Metadata["files_skipped_sensitive"]) != 1 ||
		anyInt64(result.Metadata["files_skipped_large"]) != 1 ||
		anyInt64(result.Metadata["files_skipped_binary"]) != 1 {
		t.Fatalf("grep metadata = %#v", result.Metadata)
	}

	filesOnly, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_grep",
		Arguments: rawArgs(map[string]any{
			"pattern":          `needle\s+\d+`,
			"path":             ".",
			"include":          "*.go",
			"case_insensitive": true,
			"output_mode":      "files_with_matches",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app.go", "sub/other.go"} {
		if !strings.Contains(filesOnly.Content, want) {
			t.Fatalf("files_with_matches missing %q:\n%s", want, filesOnly.Content)
		}
	}
	if strings.Contains(filesOnly.Content, "Needle 123") {
		t.Fatalf("files_with_matches leaked content:\n%s", filesOnly.Content)
	}

	counts, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_grep",
		Arguments: rawArgs(map[string]any{
			"pattern":     `Needle`,
			"path":        ".",
			"include":     "app.go",
			"output_mode": "count",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(counts.Content) != "app.go: 1" {
		t.Fatalf("count output = %q", counts.Content)
	}
}

func TestFSGrepRejectsInvalidRegex(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "app.go"), "hello\n")
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_grep",
		Arguments: rawArgs(map[string]any{"pattern": "(", "path": "."}),
	})
	if err == nil || !strings.Contains(err.Error(), "missing closing") {
		t.Fatalf("err = %v", err)
	}
	if !result.IsError || result.ErrorCode != "fs_grep_invalid_regex" {
		t.Fatalf("result = %#v", result)
	}
}

func TestFSGlobRecursiveSortLimitAndSensitiveSkip(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "sub", "b.go"), "package sub\n")
	writeTestFile(t, filepath.Join(root, "sub", "note.md"), "# note\n")
	writeTestFile(t, filepath.Join(root, ".env"), "SECRET=1\n")
	oldTime := time.Unix(100, 0)
	newTime := time.Unix(200, 0)
	if err := os.Chtimes(filepath.Join(root, "main.go"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "sub", "b.go"), newTime, newTime); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_glob",
		Arguments: rawArgs(map[string]any{
			"pattern": "**/*.go",
			"path":    ".",
			"type":    "file",
			"sort":    "modified",
			"limit":   1,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(result.Content, "\n")
	if len(lines) < 2 || lines[0] != "sub/b.go" || lines[1] != "...[truncated; next_offset=1]" {
		t.Fatalf("glob output = %q", result.Content)
	}
	if !result.Truncated ||
		anyInt64(result.Metadata["matches"]) != 2 ||
		anyInt64(result.Metadata["returned_matches"]) != 1 ||
		anyInt64(result.Metadata["files_skipped_sensitive"]) != 1 {
		t.Fatalf("glob metadata = %#v", result.Metadata)
	}

	dirs, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_glob",
		Arguments: rawArgs(map[string]any{
			"pattern": "sub",
			"path":    ".",
			"type":    "dir",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(dirs.Content) != "sub/" {
		t.Fatalf("dir glob output = %q", dirs.Content)
	}
}

func TestFSSearchToolsParallelMetadata(t *testing.T) {
	registry := NewRegistry(config.Default())
	for _, name := range []string{"fs_grep", "fs_glob"} {
		meta, ok := registry.ParallelMetadata(name)
		if !ok || meta.Policy != ParallelPolicyReadOnly || !meta.Idempotent || meta.RequiresExclusiveWorkspace || !meta.CanRunParallel() {
			t.Fatalf("%s metadata = %#v ok=%v", name, meta, ok)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
