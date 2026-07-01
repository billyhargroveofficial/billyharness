package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestFileFindFilesRanksRelativePathsAndMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "cmd", "target.go"), "package cmd\n")
	writeTestFile(t, filepath.Join(root, "docs", "target.go.md"), "# target\n")
	writeTestFile(t, filepath.Join(root, ".env"), "TARGET=secret\n")

	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "fs_find_files",
		Arguments: rawArgs(map[string]any{
			"query": "target.go",
			"path":  ".",
			"limit": 1,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(result.Content, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "cmd/target.go\tfile\tscore=") || lines[1] != "...[truncated; next_offset=1]" {
		t.Fatalf("find output = %q", result.Content)
	}
	if strings.Contains(result.Content, root) || strings.Contains(result.Content, ".env") {
		t.Fatalf("find output leaked absolute/sensitive path:\n%s", result.Content)
	}
	if !result.Truncated ||
		anyInt64(result.Metadata["matches"]) != 2 ||
		anyInt64(result.Metadata["returned_matches"]) != 1 ||
		anyInt64(result.Metadata["next_offset"]) != 1 ||
		anyInt64(result.Metadata["files_skipped_sensitive"]) != 1 {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
	if got, _ := result.Metadata["display_summary"].(string); !strings.Contains(got, "1/2 file matches") || !strings.Contains(got, "truncated") {
		t.Fatalf("display summary = %q", got)
	}
}

func TestFileFindFilesRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)
	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_find_files",
		Arguments: rawArgs(map[string]any{"query": "passwd", "path": "/etc"}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("err = %v", err)
	}
}

func TestFileFindFilesParallelMetadata(t *testing.T) {
	registry := NewRegistry(config.Default())
	meta, ok := registry.ParallelMetadata("fs_find_files")
	if !ok || meta.Policy != ParallelPolicyReadOnly || !meta.Idempotent || meta.RequiresExclusiveWorkspace || !meta.CanRunParallel() {
		t.Fatalf("metadata = %#v ok=%v", meta, ok)
	}
}
