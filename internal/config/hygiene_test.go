package config

import (
	"os"
	"path/filepath"
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

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
