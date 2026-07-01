package filesearch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileResolverRanksExactBasenameAndSkipsSensitive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "target.go"), "package src\n")
	writeFile(t, filepath.Join(root, "docs", "target.go.notes.md"), "notes\n")
	writeFile(t, filepath.Join(root, "deep", "my-target.go"), "package deep\n")
	writeFile(t, filepath.Join(root, ".env"), "TARGET=secret\n")

	result, err := NewResolver(0).Find(context.Background(), Options{
		Roots:      []string{root},
		Query:      "target.go",
		Limit:      10,
		DisableGit: true,
		DisableRG:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) < 2 {
		t.Fatalf("matches = %#v", result.Matches)
	}
	if got := result.Matches[0].Path; got != "src/target.go" {
		t.Fatalf("top match = %q, want exact basename first; all=%#v", got, result.Matches)
	}
	for _, match := range result.Matches {
		if strings.Contains(match.Path, ".env") {
			t.Fatalf("sensitive path leaked: %#v", result.Matches)
		}
		if match.Type != "file" || match.Score <= 0 {
			t.Fatalf("match missing type/score: %#v", match)
		}
	}
	if result.Source != sourceWalk || result.FilesSkippedSensitive != 1 {
		t.Fatalf("result source/skips = %#v", result)
	}
}

func TestFileResolverGitRgAndWalkFallbacks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app", "main.go"), "package main\n")

	if _, err := exec.LookPath("git"); err == nil {
		runCmd(t, root, "git", "init")
		result, err := NewResolver(0).Find(context.Background(), Options{Roots: []string{root}, Query: "main.go", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if result.Source != sourceGit || len(result.Matches) == 0 || result.Matches[0].Source != sourceGit {
			t.Fatalf("git result = %#v", result)
		}
	}

	if _, err := exec.LookPath("rg"); err == nil {
		result, err := NewResolver(0).Find(context.Background(), Options{
			Roots:      []string{root},
			Query:      "main.go",
			Limit:      5,
			DisableGit: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Source != sourceRipgrep || len(result.Matches) == 0 || result.Matches[0].Source != sourceRipgrep {
			t.Fatalf("rg result = %#v", result)
		}
	}

	result, err := NewResolver(0).Find(context.Background(), Options{
		Roots:      []string{root},
		Query:      "main.go",
		Limit:      5,
		DisableGit: true,
		DisableRG:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != sourceWalk || len(result.Matches) == 0 || result.Matches[0].Source != sourceWalk {
		t.Fatalf("walk result = %#v", result)
	}
}

func TestFileResolverPaginationAndSubpath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n")
	writeFile(t, filepath.Join(root, "other", "c.go"), "package other\n")

	result, err := NewResolver(0).Find(context.Background(), Options{
		Roots:      []string{root},
		Path:       "pkg",
		Query:      ".go",
		Limit:      1,
		DisableGit: true,
		DisableRG:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || !strings.HasPrefix(result.Matches[0].Path, "pkg/") {
		t.Fatalf("subpath matches = %#v", result.Matches)
	}
	if !result.Truncated || result.NextOffset != 1 || result.Total != 2 {
		t.Fatalf("pagination = %#v", result)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}
