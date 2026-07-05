package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// BillySettings is the canonical settings.json shape owned by Billyharness.
// Unknown fields are not preserved on save; settings.json is written only by
// this program and read by config resolution plus the TUI.
type BillySettings struct {
	Theme                     string  `json:"theme" settings_writer:"tui"`
	ToolView                  string  `json:"tool_view" settings_writer:"tui"`
	ThinkView                 string  `json:"think_view" settings_writer:"tui"`
	TranscriptMode            string  `json:"transcript_mode" settings_writer:"tui"`
	ContextWindowTokens       int64   `json:"context_window_tokens" settings_writer:"tui"`
	ContextCompactTokens      int     `json:"context_compact_tokens,omitempty" settings_writer:"manual/legacy"`
	InputPricePer1MTokens     float64 `json:"input_price_per_1m_tokens" settings_writer:"tui"`
	OutputPricePer1MTokens    float64 `json:"output_price_per_1m_tokens" settings_writer:"tui"`
	CacheHitPricePer1MTokens  float64 `json:"cache_hit_price_per_1m_tokens" settings_writer:"tui"`
	CacheMissPricePer1MTokens float64 `json:"cache_miss_price_per_1m_tokens" settings_writer:"tui"`
	LastLocalChatID           string  `json:"last_local_chat_id,omitempty" settings_writer:"tui"`
	LastGatewaySessionID      string  `json:"last_gateway_session_id,omitempty" settings_writer:"tui"`
	LastSelectedModel         string  `json:"last_selected_model,omitempty" settings_writer:"tui"`
	LastProfile               string  `json:"last_profile,omitempty" settings_writer:"tui"`
	LastAccessMode            string  `json:"last_access_mode,omitempty" settings_writer:"tui"`
	LastReasoningEffort       string  `json:"last_reasoning_effort,omitempty" settings_writer:"tui"`
	LastReasoningKind         string  `json:"last_reasoning_kind,omitempty" settings_writer:"tui"`
}

type BillySettingsFieldSpec struct {
	Field    string
	JSON     string
	Type     string
	Writer   string
	Optional bool
}

func BillySettingsPath() string {
	return filepath.Join(BillyHomeDir(), "settings.json")
}

func LoadBillySettings(path string) (BillySettings, error) {
	return loadBillySettings(path, BillySettings{})
}

func LoadBillySettingsWithDefaults(path string, defaults BillySettings) (BillySettings, error) {
	return loadBillySettings(path, defaults)
}

func SaveBillySettings(path string, settings BillySettings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func BillySettingsFieldSpecs() []BillySettingsFieldSpec {
	typ := reflect.TypeOf(BillySettings{})
	out := make([]BillySettingsFieldSpec, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonName, optional := settingsJSONName(field)
		if jsonName == "" || jsonName == "-" {
			continue
		}
		out = append(out, BillySettingsFieldSpec{
			Field:    field.Name,
			JSON:     jsonName,
			Type:     field.Type.String(),
			Writer:   field.Tag.Get("settings_writer"),
			Optional: optional,
		})
	}
	return out
}

func loadBillySettings(path string, defaults BillySettings) (BillySettings, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return defaults, err
	}
	settings := defaults
	if err := json.Unmarshal(body, &settings); err != nil {
		return defaults, err
	}
	return settings, nil
}

func settingsJSONName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name, false
	}
	name, options, _ := strings.Cut(tag, ",")
	return name, strings.Contains(","+options+",", ",omitempty,")
}
