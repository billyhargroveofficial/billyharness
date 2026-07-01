package diagnostics

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseDiagnosticsSortsCapsAndFallsBackToRaw(t *testing.T) {
	output := strings.Join([]string{
		"src/a.go:10:2: warning: unused value",
		"src/b.go:4:1: undefined: Thing",
		"src/a.go:11:3: error: broken",
		"src/a.go:12:3: error: capped",
		"src/c.ts(7,5): warning TS123: maybe wrong",
	}, "\n")
	issues, truncated := Parse(output, ParseOptions{MaxIssues: 10, MaxIssuesPerFile: 2})
	if !truncated {
		t.Fatal("expected per-file truncation")
	}
	if len(issues) != 4 {
		t.Fatalf("issues = %#v", issues)
	}
	if issues[0].Severity != "error" || issues[0].File != "src/a.go" || issues[0].Line != 11 || issues[0].Column != 3 {
		t.Fatalf("first issue = %#v", issues[0])
	}
	if issues[1].Severity != "error" || issues[1].File != "src/b.go" {
		t.Fatalf("second issue = %#v", issues[1])
	}
	if issues[3].Severity != "warning" || issues[3].File != "src/c.ts" {
		t.Fatalf("warning issue = %#v", issues[3])
	}

	fallback, truncated := Parse("compiler exploded without a location", ParseOptions{FallbackOnFailure: true})
	if truncated || len(fallback) != 1 || !fallback[0].Raw || fallback[0].Severity != "error" {
		t.Fatalf("fallback = %#v truncated=%v", fallback, truncated)
	}
}

func TestRunDiagnosticsCommandBoundsOutputAndParsesIssues(t *testing.T) {
	result, err := Run(context.Background(), RunRequest{Command: Command{
		Name:             "lint",
		Command:          "sh",
		Args:             []string{"-c", "printf 'pkg/main.go:3:2: error: bad\\n'; yes raw | head -c 4096; exit 1"},
		Timeout:          5 * time.Second,
		MaxOutputBytes:   128,
		MaxIssues:        10,
		MaxIssuesPerFile: 5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == 0 || result.OriginalOutputBytes <= int64(len(result.RawOutput)) || !result.OutputTruncated {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Issues) != 1 || result.Issues[0].File != "pkg/main.go" || result.ErrorCount != 1 {
		t.Fatalf("issues = %#v errors=%d", result.Issues, result.ErrorCount)
	}
	text := Format(result, "/tmp/out.txt")
	if !strings.Contains(text, "<diagnostics") || !strings.Contains(text, "raw_output_ref: /tmp/out.txt") || strings.Contains(text, "raw raw raw raw") {
		t.Fatalf("formatted diagnostics leaked raw output: %q", text)
	}
}
