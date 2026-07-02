package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverParsesFrontmatterAndSourcePrecedence(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "skills", "writer", "SKILL.md"), `---
name: writer
description: "Home writer"
tags: [writing, Draft]
---

# Ignored heading
`)
	writeSkillFile(t, filepath.Join(project, ".billyharness", "skills", "writer", "SKILL.md"), `---
name: writer
description: Project writer
---

# Project body
`)

	opts := Options{HomeDir: home, WorkspaceRoots: []string{project}, DisableDefaultHermes: true}
	found, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %#v", found)
	}
	if found[0].Source != SourceHome || found[0].Description != "Home writer" || strings.Join(found[0].Tags, ",") != "writing,draft" {
		t.Fatalf("home skill = %#v", found[0])
	}
	view, err := View(opts, ViewRequest{Name: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Skill.Source != SourceHome || !strings.Contains(view.Content, "Home writer") {
		t.Fatalf("default view did not prefer home source: %#v", view)
	}
	projectView, err := View(opts, ViewRequest{Name: "writer", Source: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if projectView.Skill.Source != SourceProject || !strings.Contains(projectView.Content, "Project body") {
		t.Fatalf("project view = %#v", projectView)
	}
}

func TestListHermesNestedCompatibilityAndSupportFileCaps(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	hermes := t.TempDir()
	writeSkillFile(t, filepath.Join(project, ".claude", "skills", "legacy", "SKILL.md"), "# Legacy\ncompat body")
	writeSkillFile(t, filepath.Join(hermes, "research", "arxiv", "SKILL.md"), `---
name: arxiv
description: "Search arXiv"
metadata:
  hermes:
    tags: [Research, Papers]
---

# arXiv
`)
	writeSkillFile(t, filepath.Join(hermes, "research", "arxiv", "references", "guide.md"), strings.Repeat("0123456789", 20))

	opts := Options{
		HomeDir:                 home,
		WorkspaceRoots:          []string{project},
		IncludeCompat:           true,
		HermesRuntimeSkillsDir:  hermes,
		DisableDefaultHermes:    true,
		HermesHomeSkillsDir:     filepath.Join(t.TempDir(), "missing-home"),
		HermesOptionalSkillsDir: filepath.Join(t.TempDir(), "missing-optional"),
	}
	list, err := List(opts, ListRequest{Query: "papers", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "arxiv" || list.Items[0].Source != SourceHermesRuntime ||
		list.Items[0].Category != "research" || list.Items[0].SupportFiles[0] != "references/guide.md" {
		t.Fatalf("hermes list = %#v", list)
	}
	view, err := View(opts, ViewRequest{Name: "arxiv", Source: "hermes_runtime", FilePath: "references/guide.md", MaxChars: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Truncated || view.FilePath != "references/guide.md" || !strings.Contains(view.Content, "...[truncated]") {
		t.Fatalf("support view = %#v", view)
	}
	if _, err := View(opts, ViewRequest{Name: "arxiv", FilePath: "../SKILL.md"}); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestImportCopiesSelectedCompatibilitySkillWithMetadata(t *testing.T) {
	home := t.TempDir()
	hermes := t.TempDir()
	skillBody := "# Arxiv\nBody\n"
	writeSkillFile(t, filepath.Join(hermes, "research", "arxiv", "SKILL.md"), skillBody)
	writeSkillFile(t, filepath.Join(hermes, "research", "arxiv", "references", "guide.md"), "guide")
	opts := Options{
		HomeDir:                home,
		IncludeCompat:          true,
		HermesRuntimeSkillsDir: hermes,
		DisableDefaultHermes:   true,
	}

	result, err := Import(opts, ImportRequest{Name: "arxiv", Source: "hermes_runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != SourceHermesRuntime || result.FilesCopied < 2 || result.BytesCopied == 0 {
		t.Fatalf("import result = %#v", result)
	}
	body, err := os.ReadFile(filepath.Join(home, "skills", "arxiv", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != skillBody {
		t.Fatalf("copied body = %q", body)
	}
	var meta map[string]any
	metaBody, err := os.ReadFile(filepath.Join(home, "skills", "arxiv", "billyharness.skill.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metaBody, &meta); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(skillBody))
	if meta["source"] != SourceHermesRuntime || meta["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("metadata = %#v", meta)
	}
	if _, err := Import(opts, ImportRequest{Name: "arxiv", Source: "hermes_runtime"}); err == nil {
		t.Fatal("expected duplicate destination error")
	}
}

func writeSkillFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
