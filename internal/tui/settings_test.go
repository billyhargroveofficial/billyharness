package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestLoadAppSettingsUsesCanonicalBillySettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	path := filepath.Join(home, "settings.json")
	if err := config.SaveBillySettings(path, config.BillySettings{
		Theme:                "light",
		ToolView:             "expanded",
		ThinkView:            "hidden",
		TranscriptMode:       "raw",
		ContextWindowTokens:  777000,
		LastSelectedModel:    "gpt-5.5",
		LastProfile:          "teacher.profile",
		LastAccessMode:       "read-only",
		LastReasoningEffort:  "xhigh",
		LastReasoningKind:    "enabled",
		ContextCompactTokens: 333000,
	}); err != nil {
		t.Fatal(err)
	}
	settings, settingsPath, sessionsDir, err := loadAppSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settingsPath != path {
		t.Fatalf("settings path = %q, want %q", settingsPath, path)
	}
	if sessionsDir != filepath.Join(home, "sessions") {
		t.Fatalf("sessions dir = %q", sessionsDir)
	}
	if settings.Theme != "light" ||
		settings.ToolView != "expanded" ||
		settings.ContextWindowTokens != 777000 ||
		settings.LastSelectedModel != "gpt-5.5" ||
		settings.LastAccessMode != config.AccessModePlan ||
		settings.ContextCompactTokens != 333000 {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestLoadAppSettingsCreatesCanonicalDefaultFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	settings, path, _, err := loadAppSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "dark" || settings.ContextWindowTokens != legacySettingsContextWindowTokens {
		t.Fatalf("default settings = %#v", settings)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadBillySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme != "dark" || loaded.ContextWindowTokens != legacySettingsContextWindowTokens {
		t.Fatalf("saved default settings = %#v", loaded)
	}
}
