package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestBillySettingsLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := BillySettings{
		Theme:                     "light",
		ToolView:                  "expanded",
		ThinkView:                 "hidden",
		TranscriptMode:            "raw",
		ContextWindowTokens:       777000,
		ContextCompactTokens:      333000,
		InputPricePer1MTokens:     1.25,
		OutputPricePer1MTokens:    2.5,
		CacheHitPricePer1MTokens:  0.1,
		CacheMissPricePer1MTokens: 0.2,
		LastLocalChatID:           "local-1",
		LastGatewaySessionID:      "gateway-1",
		LastSelectedModel:         "gpt-5.5",
		LastProfile:               "teacher.profile",
		LastAccessMode:            AccessModePlan,
		LastReasoningEffort:       "xhigh",
		LastReasoningKind:         "enabled",
	}
	if err := SaveBillySettings(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("settings mode = %v, want 0600", got)
	}
	got, err := LoadBillySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings round trip = %#v, want %#v", got, want)
	}
}

func TestBillySettingsFieldSpecsReflectStruct(t *testing.T) {
	specs := BillySettingsFieldSpecs()
	if got, want := len(specs), reflect.TypeOf(BillySettings{}).NumField(); got != want {
		t.Fatalf("settings field specs = %d, want %d", got, want)
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.Field == "" || spec.JSON == "" || spec.Type == "" || spec.Writer == "" {
			t.Fatalf("incomplete settings field spec: %#v", spec)
		}
		if seen[spec.JSON] {
			t.Fatalf("duplicate settings JSON key %q", spec.JSON)
		}
		seen[spec.JSON] = true
	}
	for _, key := range []string{
		"last_selected_model",
		"last_reasoning_kind",
		"last_reasoning_effort",
		"last_profile",
		"last_access_mode",
		"context_window_tokens",
		"context_compact_tokens",
	} {
		if !seen[key] {
			t.Fatalf("settings field specs missing %q", key)
		}
	}
}
