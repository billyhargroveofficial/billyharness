package docsgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagesReferenceMatchesGoListSet(t *testing.T) {
	data, err := packagesReferenceInput()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	listed, err := listGoPackages(root, "./internal/...", "./cmd/...")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, pkg := range listed {
		short, ok := shortPackageName(pkg.ImportPath)
		if ok {
			want[short] = true
		}
	}
	if len(data.Packages) != len(want) {
		t.Fatalf("generated packages = %d, want %d", len(data.Packages), len(want))
	}
	for _, pkg := range data.Packages {
		if !want[pkg.Package] {
			t.Fatalf("generated package %s not in go list set", pkg.Package)
		}
		if strings.TrimSpace(pkg.Doc) == "" {
			t.Fatalf("package %s has empty doc sentence", pkg.Package)
		}
	}
}

func TestPackagesReverseIndexIsInverse(t *testing.T) {
	data, err := packagesReferenceInput()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]packageDoc{}
	for _, pkg := range data.Packages {
		byName[pkg.Package] = pkg
	}
	for _, pkg := range data.Packages {
		for _, importedBy := range pkg.ImportedBy {
			importer, ok := byName[importedBy]
			if !ok {
				t.Fatalf("%s reverse importer %s is missing", pkg.Package, importedBy)
			}
			if !containsString(importer.InternalImports, pkg.Package) {
				t.Fatalf("%s reverse importer %s does not import it; imports=%v", pkg.Package, importedBy, importer.InternalImports)
			}
		}
		for _, imported := range pkg.InternalImports {
			target, ok := byName[imported]
			if !ok {
				t.Fatalf("%s imports missing package %s", pkg.Package, imported)
			}
			if !containsString(target.ImportedBy, pkg.Package) {
				t.Fatalf("%s imports %s, but reverse index is %v", pkg.Package, imported, target.ImportedBy)
			}
		}
	}
}

func TestPackagesInternalSetMatchesArchitectureMap(t *testing.T) {
	data, err := packagesReferenceInput()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	architectureSet := architecturePackageSet(t, filepath.Join(root, "docs", "architecture.md"))
	for _, pkg := range data.Packages {
		if !strings.HasPrefix(pkg.Package, "internal/") {
			continue
		}
		name := strings.TrimPrefix(pkg.Package, "internal/")
		if !architectureSet[name] {
			t.Fatalf("internal package %s missing from architecture map", name)
		}
		delete(architectureSet, name)
	}
	for name := range architectureSet {
		t.Fatalf("architecture map package %s missing from generated package set", name)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func architecturePackageSet(t *testing.T, path string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	inPackageMap := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "## Package Map":
			inPackageMap = true
			continue
		case inPackageMap && strings.HasPrefix(line, "## "):
			return out
		case !inPackageMap || !strings.HasPrefix(line, "|"):
			continue
		}
		cells := markdownTableCellsForPackages(line)
		if len(cells) == 0 || cells[0] == "Package" || strings.Contains(cells[0], "---") {
			continue
		}
		name := firstBacktickValue(cells[0])
		name = strings.TrimPrefix(name, "internal/")
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func markdownTableCellsForPackages(line string) []string {
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cells = append(cells, part)
		}
	}
	return cells
}

func firstBacktickValue(text string) string {
	start := strings.Index(text, "`")
	if start < 0 {
		return ""
	}
	text = text[start+1:]
	end := strings.Index(text, "`")
	if end < 0 {
		return ""
	}
	return text[:end]
}
