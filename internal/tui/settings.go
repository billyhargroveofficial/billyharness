package tui

import (
	"os"
	"path/filepath"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const (
	// legacySettingsContextWindowTokens is a compatibility value for old TUI
	// settings; runtime config re-derives model windows from modelinfo.
	legacySettingsContextWindowTokens = 1000000
)

type appSettings config.BillySettings

func loadAppSettings() (appSettings, string, string, error) {
	dir := billyHomeDir()
	settingsPath := filepath.Join(dir, "settings.json")
	sessionsDir := filepath.Join(dir, "sessions")
	settings := defaultAppSettings()
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return settings, settingsPath, sessionsDir, err
	}
	loaded, err := config.LoadBillySettingsWithDefaults(settingsPath, config.BillySettings(settings))
	if err != nil {
		if os.IsNotExist(err) {
			_ = saveAppSettings(settingsPath, settings)
			return settings, settingsPath, sessionsDir, nil
		}
		return settings, settingsPath, sessionsDir, err
	}
	settings = appSettings(loaded).normalized()
	return settings, settingsPath, sessionsDir, nil
}

func defaultAppSettings() appSettings {
	return appSettings{
		Theme:                     "dark",
		ToolView:                  "collapsed",
		ThinkView:                 "expanded",
		TranscriptMode:            "rich",
		ContextWindowTokens:       legacySettingsContextWindowTokens,
		InputPricePer1MTokens:     0,
		OutputPricePer1MTokens:    0,
		CacheHitPricePer1MTokens:  0,
		CacheMissPricePer1MTokens: 0,
	}
}

func (s appSettings) normalized() appSettings {
	if s.Theme != "dark" && s.Theme != "light" {
		s.Theme = "dark"
	}
	if !validViewMode(s.ToolView, []string{"auto", "expanded", "collapsed", "current", "hidden", "errors"}) {
		s.ToolView = "collapsed"
	}
	if !validViewMode(s.ThinkView, []string{"expanded", "collapsed", "hidden"}) {
		s.ThinkView = "expanded"
	}
	if !validViewMode(s.TranscriptMode, []string{"raw", "rich"}) {
		s.TranscriptMode = "rich"
	}
	if s.ContextWindowTokens <= 0 || s.ContextWindowTokens == 128000 {
		s.ContextWindowTokens = legacySettingsContextWindowTokens
	}
	if s.LastProfile == "" {
		s.LastProfile = "billy"
	}
	if s.LastAccessMode != "" {
		s.LastAccessMode = config.NormalizeAccessMode(s.LastAccessMode)
	}
	return s
}

func saveAppSettings(path string, settings appSettings) error {
	settings = settings.normalized()
	return config.SaveBillySettings(path, config.BillySettings(settings))
}

func validViewMode(value string, allowed []string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func billyHomeDir() string {
	if explicit := os.Getenv("BILLYHARNESS_HOME"); explicit != "" {
		return explicit
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".billyharness"
	}
	return filepath.Join(home, "billyharness")
}
