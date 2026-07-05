package docsgen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDocsCurrent(t *testing.T) {
	root := repoRoot(t)
	generatedDir := filepath.Join(root, "docs", "generated")
	expected := map[string]bool{}
	for _, target := range Targets() {
		expected[target.Filename] = true
		body, err := target.Generate()
		if err != nil {
			t.Fatalf("generate %s: %v", target.Name, err)
		}
		path := filepath.Join(generatedDir, target.Filename)
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, body) {
			t.Fatalf("docs/generated/%s is stale; run: go run ./cmd/fast-agent-harness docsgen", target.Filename)
		}
	}
	entries, err := os.ReadDir(generatedDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".md" || name == "README.md" {
			continue
		}
		if !expected[name] {
			t.Fatalf("docs/generated/%s has no docsgen target; run: go run ./cmd/fast-agent-harness docsgen", name)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}
