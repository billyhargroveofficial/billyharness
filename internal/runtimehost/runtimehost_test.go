package runtimehost_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
	tuiruntime "github.com/billyhargroveofficial/billyharness/internal/tui/runtimeclient"
)

func TestSettingsFromConfigMatchesGatewayTUIAndRegistryProjections(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.Profile = "research"
	cfg.WorkspaceRoots = []string{"/repo"}
	cfg.ProjectDocFallbacks = []string{"AGENTS.md"}
	cfg.MCPEnabled = true
	cfg.MCPAllowedServers = []string{"github"}
	cfg.DiagnosticsEnabled = true
	cfg.HooksEnabled = true
	cfg.StoreReasoningContent = true
	cfg.AutoApproveDangerous = true
	cfg.ApplyModelProviderDefaults()

	settings := runtimehost.SettingsFromConfig(cfg)
	if settings.ProviderBinding != cfg.ProviderBinding() ||
		settings.Profile != cfg.ProfileSelection() ||
		settings.Runtime != cfg.RuntimeLimits() ||
		!reflect.DeepEqual(settings.ToolPolicy, cfg.ToolPolicySettings()) ||
		!reflect.DeepEqual(settings.Diagnostics, cfg.DiagnosticsSettings()) ||
		!reflect.DeepEqual(settings.MCP, cfg.MCPSettings()) ||
		!reflect.DeepEqual(settings.Hooks, cfg.HookSettings()) ||
		!reflect.DeepEqual(settings.Instructions, cfg.InstructionSettings()) ||
		settings.Auth != cfg.AuthSettings() {
		t.Fatalf("runtimehost settings drifted from config projections:\nsettings=%#v\ncfg=%#v", settings, cfg)
	}

	gatewaySettings := gateway.ServerSettingsFromConfig(cfg)
	if !reflect.DeepEqual(gatewaySettings, gateway.ServerSettingsFromRuntimeHost(settings)) {
		t.Fatalf("gateway settings drifted from runtimehost:\nfrom config=%#v\nfrom host=%#v", gatewaySettings, gateway.ServerSettingsFromRuntimeHost(settings))
	}

	var tuiSettings tuiruntime.Settings = settings
	if !reflect.DeepEqual(tuiSettings.RegistrySettings(), settings.RegistrySettings()) {
		t.Fatalf("TUI runtime settings registry projection drifted:\ntui=%#v\nhost=%#v", tuiSettings.RegistrySettings(), settings.RegistrySettings())
	}
}

func TestHostBuildsMockRuntimeBundle(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = modelinfo.ProviderMock
	cfg.Model = "mock"
	cfg.ApplyModelProviderDefaults()

	host, err := runtimehost.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer host.Close()

	if host.Provider == nil || host.Registry == nil || host.Agent() == nil {
		t.Fatalf("incomplete host bundle: %#v", host)
	}
	if host.Settings.ProviderCaps.Provider != modelinfo.ProviderMock ||
		host.Settings.ProviderCaps.Model != "mock" ||
		!host.Settings.ProviderCaps.Streaming {
		t.Fatalf("provider capabilities not attached: %#v", host.Settings.ProviderCaps)
	}
	specs := host.Registry.Specs()
	if len(specs) == 0 {
		t.Fatal("registry specs are empty")
	}
}

func TestRuntimeDiffSettingsPreserveInstructionPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.Profile = "billy"
	cfg.WorkspaceRoots = []string{"/repo"}
	cfg.ProjectDocMaxBytes = 123
	cfg.ProjectDocFallbacks = []string{"AGENTS.md", "README.md"}
	cfg.ProjectContextMaxBytes = 456
	cfg.MemoryEnabled = true
	cfg.MemorySummaryMaxBytes = 789
	cfg.MemoryIndexMaxBytes = 321
	cfg.MemoryTopicMaxBytes = 654
	diff := config.RuntimeDiffSettingsFromConfig(cfg)

	settings := runtimehost.SettingsFromRuntimeDiffSettings(diff)
	want := cfg.InstructionSettings()
	if !reflect.DeepEqual(settings.Instructions, want) {
		t.Fatalf("instruction settings drifted:\ngot  %#v\nwant %#v", settings.Instructions, want)
	}
	if settings.RuntimeDiffSettings().GatewayAddr != cfg.GatewayAddr {
		t.Fatalf("runtime diff gateway addr = %q, want %q", settings.RuntimeDiffSettings().GatewayAddr, cfg.GatewayAddr)
	}
}
