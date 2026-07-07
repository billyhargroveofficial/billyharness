package agentclub

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestLoadConfigFilesAllowsDisabledDefaultWithoutSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultConfigFilename)
	if err := WriteConfigFile(path, DefaultFileConfig()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfigFiles(LoadConfigOptions{Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Registry == nil {
		t.Fatal("registry missing")
	}
	if loaded.Status.CapabilityCount != 1 || loaded.Status.BindingCount != 1 || loaded.Status.TriggerCount != 1 {
		t.Fatalf("status = %#v", loaded.Status)
	}
	if loaded.Status.EnabledBindingCount != 0 || loaded.Status.EnabledTriggerCount != 0 || loaded.Status.MissingSecretEnvCount != 1 {
		t.Fatalf("disabled status = %#v", loaded.Status)
	}
	if _, err := loaded.Registry.TriggerBinding("hh.review_queue.webhook"); !errors.Is(err, ErrTriggerDisabled) {
		t.Fatalf("disabled trigger err = %v", err)
	}
}

func TestLoadConfigFilesResolvesEnabledHMACSecretsAndFailsClosedWhenMissing(t *testing.T) {
	cfg := testFileConfig()
	cfg.Triggers[0].AuthMethod = TriggerAuthHMACSHA256
	cfg.Triggers[0].HMACSecretEnv = "AGENTCLUB_TEST_SECRET"
	path := filepath.Join(t.TempDir(), "agentclub.config.json")
	if err := WriteConfigFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfigFiles(LoadConfigOptions{Files: []string{path}, SecretLookup: func(string) (string, bool) {
		return "", false
	}})
	if err == nil || !strings.Contains(err.Error(), "AGENTCLUB_TEST_SECRET") {
		t.Fatalf("missing secret err = %v", err)
	}
	loaded, err := LoadConfigFiles(LoadConfigOptions{Files: []string{path}, SecretLookup: func(name string) (string, bool) {
		if name == "AGENTCLUB_TEST_SECRET" {
			return "secret-value", true
		}
		return "", false
	}})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := loaded.Registry.TriggerBinding("fixture.webhook")
	if err != nil {
		t.Fatal(err)
	}
	if string(trigger.HMACSecret) != "secret-value" || loaded.Status.MissingSecretEnvCount != 0 || loaded.Status.HMACSecretEnvCount != 1 {
		t.Fatalf("trigger/status = %#v %#v", trigger, loaded.Status)
	}
	body, err := json.Marshal(trigger)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-value") {
		t.Fatalf("secret leaked in JSON: %s", body)
	}
}

func TestBuildRegistryFromConfigRequiresStableTrustedBindingIDs(t *testing.T) {
	cfg := testFileConfig()
	cfg.TrustedBindings[0].ID = ""
	_, _, err := BuildRegistryFromConfig(cfg, nil)
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "trusted binding id required") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigEnableDisableHelpers(t *testing.T) {
	cfg := testFileConfig()
	if err := SetTrustedBindingEnabled(&cfg, "fixture.binding", false); err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedBindings[0].Enabled {
		t.Fatal("binding should be disabled")
	}
	if err := SetTriggerEnabled(&cfg, "fixture.webhook", false); err != nil {
		t.Fatal(err)
	}
	if cfg.Triggers[0].Enabled {
		t.Fatal("trigger should be disabled")
	}
	if err := SetTriggerEnabled(&cfg, "missing", true); !errors.Is(err, ErrConfigItemMissing) {
		t.Fatalf("missing trigger err = %v", err)
	}
}

func TestReadConfigFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"raw_command":"nope"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v", err)
	}
}

func testFileConfig() FileConfig {
	return FileConfig{
		SchemaVersion: SchemaVersion,
		Capabilities: []CapabilityDescriptor{{
			ID:          "fixture.review",
			Title:       "Fixture review",
			Kind:        CapabilityKindReview,
			Risk:        RiskReadOnly,
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Dispatch:    DispatchAdmitOnly,
			Approval:    ApprovalNone,
			Version:     "v0",
		}},
		TrustedBindings: []TrustedBindingConfig{{
			ID:         "fixture.binding",
			Capability: "fixture.review",
			ClientType: "ingress",
			ClientID:   "ingress:fixture:prod",
			Sources:    []string{"fixture"},
			EventTypes: []string{"review_queue"},
			Enabled:    true,
		}},
		Triggers: []TriggerBindingConfig{{
			ID:              "fixture.webhook",
			Kind:            TriggerKindWebhook,
			Source:          "fixture",
			Capability:      "fixture.review",
			EventType:       "review_queue",
			Owner:           gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
			TargetSessionID: "session-1",
			Prompt:          "Review fixture.",
			AuthMethod:      TriggerAuthNone,
			Enabled:         true,
		}},
	}
}
