package docsgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	moduleImportPath = "github.com/billyhargroveofficial/billyharness"
	internalPrefix   = moduleImportPath + "/internal/"
	cmdPrefix        = moduleImportPath + "/cmd/"
)

type packagesReferenceData struct {
	Packages []packageDoc
	Totals   packageTotals
}

type packageDoc struct {
	Package         string
	Doc             string
	InternalImports []string
	ImportedBy      []string
}

type packageTotals struct {
	PackageCount int
	EdgeCount    int
}

type listedGoPackage struct {
	Dir        string
	ImportPath string
	Name       string
	Imports    []string
}

func GeneratePackages() ([]byte, error) {
	data, err := packagesReferenceInput()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(header("go list ./internal/... ./cmd/..."))
	b.WriteString("# Package Index\n\n")
	b.WriteString(fmt.Sprintf("Packages: %d. Direct internal edges: %d.\n\n", data.Totals.PackageCount, data.Totals.EdgeCount))
	b.WriteString("This reference is generated from Go package reality. The hand-written package map in `docs/architecture.md` remains the intent and allowlist.\n\n")
	b.WriteString("## Packages\n\n")
	b.WriteString(markdownTable([]string{"Package", "Doc sentence", "Direct internal imports"}, packageRows(data.Packages)))
	b.WriteString("\n## Reverse Import Index\n\n")
	b.WriteString(markdownTable([]string{"Package", "Imported by"}, reverseImportRows(data.Packages)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func packagesReferenceInput() (packagesReferenceData, error) {
	root, err := findRepoRoot()
	if err != nil {
		return packagesReferenceData{}, err
	}
	listed, err := listGoPackages(root, "./internal/...", "./cmd/...")
	if err != nil {
		return packagesReferenceData{}, err
	}
	packages := make([]packageDoc, 0, len(listed))
	known := map[string]bool{}
	for _, pkg := range listed {
		short, ok := shortPackageName(pkg.ImportPath)
		if !ok {
			continue
		}
		known[short] = true
	}
	for _, pkg := range listed {
		short, ok := shortPackageName(pkg.ImportPath)
		if !ok {
			continue
		}
		docSentence, err := packageDocSentence(pkg.Dir, pkg.Name)
		if err != nil {
			return packagesReferenceData{}, err
		}
		packages = append(packages, packageDoc{
			Package:         short,
			Doc:             docSentence,
			InternalImports: directInternalImports(pkg.Imports, known),
		})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Package < packages[j].Package })
	importedBy := reverseImports(packages)
	edgeCount := 0
	for i := range packages {
		packages[i].ImportedBy = importedBy[packages[i].Package]
		edgeCount += len(packages[i].InternalImports)
	}
	return packagesReferenceData{
		Packages: packages,
		Totals: packageTotals{
			PackageCount: len(packages),
			EdgeCount:    edgeCount,
		},
	}, nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}

func listGoPackages(root string, patterns ...string) ([]listedGoPackage, error) {
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command(goBin, args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go list failed: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("go list failed: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []listedGoPackage
	for {
		var pkg listedGoPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			return packages, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list JSON: %w", err)
		}
		packages = append(packages, pkg)
	}
}

func packageDocSentence(dir, name string) (string, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse package docs for %s: %w", dir, err)
	}
	pkg := pkgs[name]
	if pkg == nil {
		for _, candidate := range pkgs {
			pkg = candidate
			break
		}
	}
	if pkg == nil {
		return "", fmt.Errorf("no parseable package in %s", dir)
	}
	text := strings.TrimSpace(doc.New(pkg, "", 0).Doc)
	if text == "" {
		return "", nil
	}
	return doc.Synopsis(text), nil
}

func shortPackageName(importPath string) (string, bool) {
	if short, ok := strings.CutPrefix(importPath, internalPrefix); ok {
		return "internal/" + short, true
	}
	if short, ok := strings.CutPrefix(importPath, cmdPrefix); ok {
		return "cmd/" + short, true
	}
	return "", false
}

func directInternalImports(imports []string, known map[string]bool) []string {
	var out []string
	for _, imp := range imports {
		if short, ok := shortPackageName(imp); ok && known[short] {
			out = append(out, short)
		}
	}
	sort.Strings(out)
	return out
}

func reverseImports(packages []packageDoc) map[string][]string {
	out := map[string][]string{}
	for _, pkg := range packages {
		out[pkg.Package] = nil
	}
	for _, pkg := range packages {
		for _, imp := range pkg.InternalImports {
			out[imp] = append(out[imp], pkg.Package)
		}
	}
	for name := range out {
		sort.Strings(out[name])
	}
	return out
}

func packageRows(packages []packageDoc) [][]string {
	rows := make([][]string, 0, len(packages))
	for _, pkg := range packages {
		rows = append(rows, []string{pkg.Package, pkg.Doc, strings.Join(pkg.InternalImports, ", ")})
	}
	return rows
}

func reverseImportRows(packages []packageDoc) [][]string {
	rows := make([][]string, 0, len(packages))
	for _, pkg := range packages {
		rows = append(rows, []string{pkg.Package, strings.Join(pkg.ImportedBy, ", ")})
	}
	return rows
}
