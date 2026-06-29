package architecture

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const internalPrefix = "github.com/billyhargroveofficial/billyharness/internal/"

type goPackage struct {
	ImportPath string
	Imports    []string
}

type architecturePackageRule struct {
	Name    string
	Allowed map[string]bool
}

func TestInternalPackageBoundaries(t *testing.T) {
	pkgs := internalPackages(t)
	rules := architecturePackageRules(t)

	for name := range pkgs {
		if _, ok := rules[name]; !ok {
			t.Errorf("docs/architecture.md package map missing internal/%s", name)
		}
	}
	for name := range rules {
		if _, ok := pkgs[name]; !ok {
			t.Errorf("docs/architecture.md package map includes internal/%s, but go list ./internal/... did not find that package", name)
		}
	}
	for name, pkg := range pkgs {
		rule, ok := rules[name]
		if !ok {
			continue
		}
		for _, imp := range pkg.Imports {
			if !strings.HasPrefix(imp, internalPrefix) {
				continue
			}
			short := strings.TrimPrefix(imp, internalPrefix)
			if !rule.Allowed[short] {
				t.Errorf("internal/%s imports internal/%s; docs/architecture.md allowed internal imports are %s", name, short, allowedList(rule.Allowed))
			}
		}
	}
}

func internalPackages(t *testing.T) map[string]goPackage {
	t.Helper()
	root := repoRoot(t)
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goBin, "list", "-json", "./internal/...")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	pkgs := map[string]goPackage{}
	for {
		var pkg goPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode go list JSON: %v", err)
		}
		if !strings.HasPrefix(pkg.ImportPath, internalPrefix) {
			continue
		}
		name := strings.TrimPrefix(pkg.ImportPath, internalPrefix)
		pkgs[name] = pkg
	}
	return pkgs
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

func architecturePackageRules(t *testing.T) map[string]architecturePackageRule {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]architecturePackageRule{}
	inPackageMap := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "## Package Map":
			inPackageMap = true
			continue
		case inPackageMap && strings.HasPrefix(line, "## "):
			return rules
		case !inPackageMap || !strings.HasPrefix(line, "|"):
			continue
		}
		cells := markdownTableCells(line)
		if len(cells) < 4 || cells[0] == "Package" || strings.Contains(cells[0], "---") {
			continue
		}
		name, ok := architecturePackageName(cells[0])
		if !ok {
			t.Fatalf("could not parse package cell %q in docs/architecture.md", cells[0])
		}
		rules[name] = architecturePackageRule{
			Name:    name,
			Allowed: architectureAllowedImports(cells[2]),
		}
	}
	return rules
}

func markdownTableCells(line string) []string {
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

func architecturePackageName(cell string) (string, bool) {
	values := backtickValues(cell)
	if len(values) == 0 {
		return "", false
	}
	name, ok := strings.CutPrefix(values[0], "internal/")
	return name, ok && name != ""
}

func architectureAllowedImports(cell string) map[string]bool {
	allowed := map[string]bool{}
	if strings.EqualFold(strings.TrimSpace(cell), "none") {
		return allowed
	}
	for _, value := range backtickValues(cell) {
		value = strings.TrimPrefix(value, "internal/")
		if value != "" {
			allowed[value] = true
		}
	}
	return allowed
}

func backtickValues(text string) []string {
	var values []string
	for {
		start := strings.Index(text, "`")
		if start < 0 {
			return values
		}
		text = text[start+1:]
		end := strings.Index(text, "`")
		if end < 0 {
			return values
		}
		values = append(values, text[:end])
		text = text[end+1:]
	}
}

func allowedList(allowed map[string]bool) string {
	if len(allowed) == 0 {
		return "none"
	}
	items := make([]string, 0, len(allowed))
	for item := range allowed {
		items = append(items, item)
	}
	slices.Sort(items)
	return strings.Join(items, ", ")
}
