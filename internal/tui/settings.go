package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const (
	defaultContextWindowTokens = 1000000
)

type appSettings struct {
	Theme                     string  `json:"theme"`
	ToolView                  string  `json:"tool_view"`
	ThinkView                 string  `json:"think_view"`
	ContextWindowTokens       int64   `json:"context_window_tokens"`
	InputPricePer1MTokens     float64 `json:"input_price_per_1m_tokens"`
	OutputPricePer1MTokens    float64 `json:"output_price_per_1m_tokens"`
	CacheHitPricePer1MTokens  float64 `json:"cache_hit_price_per_1m_tokens"`
	CacheMissPricePer1MTokens float64 `json:"cache_miss_price_per_1m_tokens"`
	LastLocalChatID           string  `json:"last_local_chat_id,omitempty"`
	LastGatewaySessionID      string  `json:"last_gateway_session_id,omitempty"`
	LastSelectedModel         string  `json:"last_selected_model,omitempty"`
	LastProfile               string  `json:"last_profile,omitempty"`
	LastAccessMode            string  `json:"last_access_mode,omitempty"`
	LastReasoningEffort       string  `json:"last_reasoning_effort,omitempty"`
	LastReasoningKind         string  `json:"last_reasoning_kind,omitempty"`
}

func loadAppSettings() (appSettings, string, string, error) {
	dir := billyHomeDir()
	settingsPath := filepath.Join(dir, "settings.json")
	sessionsDir := filepath.Join(dir, "sessions")
	settings := defaultAppSettings()
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return settings, settingsPath, sessionsDir, err
	}
	bytes, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			_ = saveAppSettings(settingsPath, settings)
			return settings, settingsPath, sessionsDir, nil
		}
		return settings, settingsPath, sessionsDir, err
	}
	if err := json.Unmarshal(bytes, &settings); err != nil {
		return defaultAppSettings(), settingsPath, sessionsDir, err
	}
	settings = settings.normalized()
	return settings, settingsPath, sessionsDir, nil
}

func defaultAppSettings() appSettings {
	return appSettings{
		Theme:                     "dark",
		ToolView:                  "collapsed",
		ThinkView:                 "expanded",
		ContextWindowTokens:       defaultContextWindowTokens,
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
	if s.ContextWindowTokens <= 0 || s.ContextWindowTokens == 128000 {
		s.ContextWindowTokens = defaultContextWindowTokens
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(bytes, '\n'), 0o600)
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
