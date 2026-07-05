package docsgen

import (
	"bytes"
	"strings"
	"testing"
)

func TestTargetsAreWellFormedAndDeterministic(t *testing.T) {
	names := map[string]bool{}
	files := map[string]bool{}
	for _, target := range Targets() {
		if target.Name == "" {
			t.Fatalf("target has empty name")
		}
		if names[target.Name] {
			t.Fatalf("duplicate target name %q", target.Name)
		}
		names[target.Name] = true
		if !strings.HasSuffix(target.Filename, ".md") {
			t.Fatalf("target %s filename %q does not end in .md", target.Name, target.Filename)
		}
		if files[target.Filename] {
			t.Fatalf("duplicate target filename %q", target.Filename)
		}
		files[target.Filename] = true
		first, err := target.Generate()
		if err != nil {
			t.Fatalf("generate %s: %v", target.Name, err)
		}
		second, err := target.Generate()
		if err != nil {
			t.Fatalf("generate %s again: %v", target.Name, err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("target %s is not deterministic", target.Name)
		}
	}
}
