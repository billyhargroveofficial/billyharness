package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeHygieneRunner struct {
	lsFiles string
	calls   []string
}

func (f *fakeHygieneRunner) CombinedOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "git" && strings.Join(args, " ") == "ls-files -- *.go" {
		return f.lsFiles, nil
	}
	return "", fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
}

func TestHygieneUsesGitLsFilesAndReportsArtifactsSeparately(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "tracked.go", "package main\n")
	writeTestFile(t, repo, "huge_untracked.go", strings.Repeat("// untracked\n", hygieneGoFileLineLimit+1))
	writeTestFile(t, repo, "gateway-sessions/events.jsonl", "{}\n")
	writeTestFile(t, repo, "bin/fast-agent-harness", "binary")

	runner := &fakeHygieneRunner{lsFiles: "tracked.go\n"}
	report, err := collectHygieneReport(context.Background(), hygieneOptions{
		RepoDir: repo,
		Timeout: time.Second,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "git ls-files -- *.go" {
		t.Fatalf("git calls = %#v", runner.calls)
	}
	if report.Source.TrackedGoFiles != 1 {
		t.Fatalf("tracked go files = %d", report.Source.TrackedGoFiles)
	}
	if len(report.Source.LargeFiles) != 0 {
		t.Fatalf("untracked large file should not be counted: %#v", report.Source.LargeFiles)
	}
	if artifactBytes(report, "gateway-sessions") == 0 {
		t.Fatalf("gateway-sessions artifact size was not reported: %#v", report.RuntimeArtifacts)
	}
	if artifactBytes(report, "bin") == 0 {
		t.Fatalf("bin artifact size was not reported: %#v", report.RuntimeArtifacts)
	}
}

func TestHygieneStrictFlagsLargeTrackedFiles(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "internal/tui/tui.go", strings.Repeat("// source\n", hygieneGoFileLineLimit+1))
	writeTestFile(t, repo, "internal/tui/tui_test.go", strings.Repeat("// test\n", hygieneGoTestFileLineLimit+1))

	runner := &fakeHygieneRunner{lsFiles: "internal/tui/tui.go\ninternal/tui/tui_test.go\n"}
	var out bytes.Buffer
	err := hygieneCommand([]string{"-repo", repo, "-strict"}, &out, runner)
	if err == nil || !strings.Contains(err.Error(), "large source files") {
		t.Fatalf("strict error = %v", err)
	}
	rendered := out.String()
	for _, want := range []string{
		"internal/tui/tui.go: 1501 LOC > 1500",
		"internal/tui/tui_test.go: 1201 LOC > 1200",
		"runtime artifacts:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("hygiene output missing %q:\n%s", want, rendered)
		}
	}
}

func TestHygieneStrictAllowsDocumentedLargeFiles(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "docs/architecture.md", strings.Join([]string{
		"# Architecture",
		"",
		"## File Size Budget Exceptions",
		"",
		"| File | Current exception owner | Split plan |",
		"| --- | --- | --- |",
		"| `internal/tui/tui_test.go` | P1.4 TUI subpackage tests. | Split tests. |",
		"",
		"## Guarded Rules",
	}, "\n"))
	writeTestFile(t, repo, "internal/tui/tui_test.go", strings.Repeat("// test\n", hygieneGoTestFileLineLimit+1))

	runner := &fakeHygieneRunner{lsFiles: "internal/tui/tui_test.go\n"}
	var out bytes.Buffer
	if err := hygieneCommand([]string{"-repo", repo, "-strict"}, &out, runner); err != nil {
		t.Fatalf("allowlisted large file should not fail strict mode: %v\n%s", err, out.String())
	}
	rendered := out.String()
	if !strings.Contains(rendered, "allowed large source files:") ||
		!strings.Contains(rendered, "internal/tui/tui_test.go: 1201 LOC > 1200") {
		t.Fatalf("hygiene output missing allowlisted large file:\n%s", rendered)
	}
}

func TestHygieneStrictFlagsMissingTrackedFiles(t *testing.T) {
	repo := t.TempDir()
	runner := &fakeHygieneRunner{lsFiles: "internal/tui/missing.go\n"}
	var out bytes.Buffer
	err := hygieneCommand([]string{"-repo", repo, "-strict"}, &out, runner)
	if err == nil || !strings.Contains(err.Error(), "missing tracked Go files") {
		t.Fatalf("strict missing-file error = %v", err)
	}
	if !strings.Contains(out.String(), "missing tracked Go files:") ||
		!strings.Contains(out.String(), "internal/tui/missing.go") {
		t.Fatalf("hygiene output missing tracked-file report:\n%s", out.String())
	}
}

func writeTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func artifactBytes(report hygieneReport, path string) int64 {
	for _, artifact := range report.RuntimeArtifacts {
		if artifact.Path == path {
			return artifact.Bytes
		}
	}
	return 0
}
