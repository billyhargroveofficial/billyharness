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

func TestScheduleTriggerConfigValidation(t *testing.T) {
	cfg := testFileConfig()
	cfg.Triggers[0].ID = "fixture.schedule"
	cfg.Triggers[0].Kind = TriggerKindSchedule
	cfg.Triggers[0].Schedule = nil
	_, _, err := BuildRegistryFromConfig(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "schedule required") {
		t.Fatalf("missing schedule err = %v", err)
	}

	cfg.Triggers[0].Schedule = &ScheduleConfig{
		Kind:       ScheduleKindInterval,
		Every:      "30m",
		StartAtUTC: "2026-07-07T00:00:00Z",
		MaxCatchup: 2,
	}
	registry, _, err := BuildRegistryFromConfig(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := registry.TriggerBinding("fixture.schedule")
	if err != nil {
		t.Fatal(err)
	}
	if trigger.Schedule == nil || trigger.Schedule.Every != "30m0s" || trigger.Schedule.MaxCatchup != 2 {
		t.Fatalf("schedule = %#v", trigger.Schedule)
	}

	cfg = testFileConfig()
	cfg.Triggers[0].Schedule = &ScheduleConfig{Kind: ScheduleKindInterval, Every: "30m", StartAtUTC: "2026-07-07T00:00:00Z"}
	_, _, err = BuildRegistryFromConfig(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "schedule is only valid") {
		t.Fatalf("webhook schedule err = %v", err)
	}
}

func TestRunPolicyConfigValidationAndStatus(t *testing.T) {
	cfg := testFileConfig()
	cfg.Triggers[0].RunPolicy = &RunPolicyConfig{
		Enabled:         true,
		Mode:            RunPolicyModeStartIfIdle,
		MaxRunsPerHour:  4,
		Cooldown:        "10m",
		MaxToolRounds:   2,
		AccessMode:      "guarded",
		InterruptPolicy: "",
	}
	registry, status, err := BuildRegistryFromConfig(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.EnabledAutoRunCount != 1 {
		t.Fatalf("status = %#v", status)
	}
	trigger, err := registry.TriggerBinding("fixture.webhook")
	if err != nil {
		t.Fatal(err)
	}
	if trigger.RunPolicy == nil || trigger.RunPolicy.Cooldown != "10m0s" || trigger.RunPolicy.MaxRunsPerHour != 4 {
		t.Fatalf("run policy = %#v", trigger.RunPolicy)
	}

	cfg.Triggers[0].RunPolicy.AccessMode = "root"
	_, _, err = BuildRegistryFromConfig(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "access_mode") {
		t.Fatalf("invalid access mode err = %v", err)
	}
}

func TestRunPolicyRejectsUnknownOverrideFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentclub.config.json")
	body := []byte(`{
  "schema_version": 1,
  "capabilities": [{"id":"fixture.review","kind":"review","risk":"read_only","dispatch":"admit_only"}],
  "trusted_bindings": [{"id":"fixture.binding","capability":"fixture.review","client_type":"ingress","client_id":"ingress:fixture:prod","enabled":true}],
  "triggers": [{
    "id":"fixture.webhook",
    "kind":"webhook",
    "source":"fixture",
    "capability":"fixture.review",
    "event_type":"review_queue",
    "owner":{"client_type":"ingress","client_id":"ingress:fixture:prod"},
    "target_session_id":"session-1",
    "prompt":"Review fixture.",
    "auth_method":"none",
    "run_policy":{"enabled":true,"provider":"nope"},
    "enabled":true
  }]
}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
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
