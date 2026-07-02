package config

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestNoAmbiguousContextWindowRuntimeFallbacks(t *testing.T) {
	root := repoRoot(t)
	forbidden := map[string]string{
		"defaultContextWindowTokens":       "use a name that says whether this is an unknown-model fallback, a legacy settings value, or a real model limit",
		"legacyDefaultContextWindowTokens": "use legacySettingsContextWindowTokens so the compatibility scope is clear",
		"context_limit":                    "use context_window_tokens",
	}
	paths := []string{
		"internal/config/defaults.go",
		"internal/config/profile.go",
		"internal/tui/settings.go",
		"internal/telegrambot/render.go",
		"internal/telegrambot/context_window.go",
		"docs/profiles.md",
	}
	for _, rel := range paths {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for needle, msg := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("%s contains ambiguous %q: %s", rel, needle, msg)
			}
		}
		assertNoAmbiguousMillionTokenContextWindow(t, rel, text)
	}
}

func TestNoStaleContextLimitTerminologyInRuntimeAndLiveDocs(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range runtimeAndLiveDocFiles(t, root) {
		text := readRepoFile(t, root, rel)
		for i, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "context_limit") && !strings.Contains(line, "hygiene: allow context_limit") {
				t.Fatalf("%s:%d uses stale context_limit terminology; use context_window_tokens", rel, i+1)
			}
		}
	}
}

func TestModelContextWindowLiteralsStayInModelInfo(t *testing.T) {
	root := repoRoot(t)
	numericContextWindow := regexp.MustCompile(`\bContextWindowTokens\s*:\s*[0-9][0-9_]*\b`)
	for _, rel := range runtimeGoFiles(t, root) {
		text := readRepoFile(t, root, rel)
		for i, line := range strings.Split(text, "\n") {
			if !numericContextWindow.MatchString(line) {
				continue
			}
			if rel == "internal/modelinfo/modelinfo.go" || strings.Contains(line, "hygiene: allow context-window literal") {
				continue
			}
			t.Fatalf("%s:%d hardcodes a context window outside modelinfo: %s", rel, i+1, strings.TrimSpace(line))
		}
	}
}

func TestStatusFormatterLabelsStayCanonical(t *testing.T) {
	root := repoRoot(t)
	forbidden := map[string]string{
		"provider_api_calls": "render helper API calls instead",
		"provider_cost":      "render helper API cost or model cost instead",
		"helper_api":         "render helper API with spaces in user-facing text",
		"cost subscription":  "render subscription or model cost explicitly",
	}
	paths := []string{
		"internal/gatewayclient/client.go",
		"internal/telegrambot/render.go",
		"internal/tui/status.go",
		"cmd/fast-agent-harness/doctor.go",
		"docs/context.md",
	}
	for _, rel := range paths {
		text := readRepoFile(t, root, rel)
		lower := strings.ToLower(text)
		for needle, msg := range forbidden {
			if strings.Contains(lower, needle) {
				t.Fatalf("%s contains stale status label %q: %s", rel, needle, msg)
			}
		}
	}
}

func assertNoAmbiguousMillionTokenContextWindow(t *testing.T, rel, text string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "1_000_000") && !strings.Contains(line, "1000000") {
			continue
		}
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "contextwindow") &&
			!strings.Contains(lower, "context_window") &&
			!strings.Contains(lower, "context window") {
			continue
		}
		if strings.Contains(line, "legacySettingsContextWindowTokens") ||
			strings.Contains(line, "unknownModelContextWindowFallbackTokens") {
			continue
		}
		t.Fatalf("%s:%d contains ambiguous hardcoded context window: %s", rel, i+1, strings.TrimSpace(line))
	}
}

func runtimeAndLiveDocFiles(t *testing.T, root string) []string {
	t.Helper()
	files := runtimeGoFiles(t, root)
	files = append(files,
		"docs/context.md",
		"docs/profiles.md",
		"docs/setup.md",
		"docs/telegram.md",
		"docs/web.md",
	)
	return files
}

func runtimeGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
