package config

import (
	"reflect"
	"testing"
	"time"
)

func TestConfigProjectionsNormalizeModelRoutingAndSparkDisablement(t *testing.T) {
	cfg := Config{
		Provider:     "deepseek",
		Model:        "spark",
		Profile:      "Team/Profile",
		DisableSpark: true,
		MaxTokens:    1234,
	}

	provider := cfg.ProviderSelection()
	model := cfg.ModelSelection()
	profile := cfg.ProfileSelection()
	toolPolicy := cfg.ToolPolicySettings()
	binding := cfg.ProviderBinding()

	if provider.Provider != "openai-codex" || provider.Provider != binding.Provider.Provider {
		t.Fatalf("provider projection = %#v binding=%#v", provider, binding.Provider)
	}
	if model.Model != "gpt-5.4-mini" || model.Model != binding.Model.Model {
		t.Fatalf("model projection = %#v binding=%#v", model, binding.Model)
	}
	if !model.DisableSpark || model.MaxTokens != 1234 {
		t.Fatalf("model settings = %#v", model)
	}
	if profile.Profile != "teamprofile" {
		t.Fatalf("profile = %q", profile.Profile)
	}
	if toolPolicy.WebSummaryProvider != "openai-codex" || toolPolicy.WebSummaryModel != "gpt-5.4-mini" || toolPolicy.WebSummaryMaxOutputTokens != 700 {
		t.Fatalf("web summary policy projection = %#v", toolPolicy)
	}
}

func TestAccessModeNormalizesAndProjects(t *testing.T) {
	if NormalizeAccessMode("") != AccessModeBuild ||
		NormalizeAccessMode("SAFE") != AccessModeGuarded ||
		NormalizeAccessMode("read-only") != AccessModePlan {
		t.Fatalf("unexpected access mode normalization")
	}
	if _, ok := ParseAccessMode("plna"); ok {
		t.Fatalf("invalid access mode parsed successfully")
	}

	cfg := Config{AccessMode: "READONLY", AutoApproveDangerous: true}
	toolPolicy := cfg.ToolPolicySettings()
	if toolPolicy.AccessMode != AccessModePlan {
		t.Fatalf("tool policy access mode = %q", toolPolicy.AccessMode)
	}
	if snapshot := cfg.RuntimeToolSnapshot(); snapshot.AccessMode != AccessModePlan {
		t.Fatalf("runtime tool snapshot access mode = %q", snapshot.AccessMode)
	}

	settings, err := RuntimeDiffSettingsWithRunOverrides(RuntimeDiffSettingsFromConfig(builtInConfig()), RunOverrideSettings{
		AccessMode: "guarded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.ToolPolicy.AccessMode != AccessModeGuarded {
		t.Fatalf("override access mode = %q", settings.ToolPolicy.AccessMode)
	}
	if _, err := RuntimeDiffSettingsWithRunOverrides(RuntimeDiffSettingsFromConfig(builtInConfig()), RunOverrideSettings{AccessMode: "plna"}); err == nil {
		t.Fatalf("invalid runtime access mode override did not fail")
	}
}

func TestHookSettingsProjectionClonesHookData(t *testing.T) {
	cfg := Config{
		HooksEnabled:    true,
		HookConfigFiles: []string{"/tmp/hooks.toml"},
		Hooks: []Hook{{
			Name:    "audit",
			Event:   "before_tool",
			Command: "sh",
			Args:    []string{"-c", "true"},
			Env:     map[string]string{"TOKEN": "secret"},
			EnvVars: []string{"PATH"},
			Enabled: true,
		}},
	}

	settings := cfg.HookSettings()
	cfg.HookConfigFiles[0] = "/tmp/other.toml"
	cfg.Hooks[0].Args[0] = "mutated"
	cfg.Hooks[0].Env["TOKEN"] = "mutated"
	cfg.Hooks[0].EnvVars[0] = "MUTATED"

	if !settings.Enabled ||
		settings.ConfigFiles[0] != "/tmp/hooks.toml" ||
		settings.Hooks[0].Args[0] != "-c" ||
		settings.Hooks[0].Env["TOKEN"] != "secret" ||
		settings.Hooks[0].EnvVars[0] != "PATH" {
		t.Fatalf("hook settings projection = %#v", settings)
	}
}

func TestDiagnosticSnapshotUsesProviderBindingAndAuthProjection(t *testing.T) {
	cfg := Config{
		Provider:             "deepseek",
		Model:                "spark",
		Profile:              "Audit/Profile",
		BaseURL:              "https://deepseek.example",
		APIKeyEnv:            "CUSTOM_DEEPSEEK_KEY",
		CredentialFile:       "/tmp/credentials.json",
		CodexBaseURL:         "https://codex.example/backend",
		CodexAuthFile:        "/tmp/codex.json",
		CodexRefreshURL:      "https://auth.example/token",
		CodexAuthAPIBaseURL:  "https://auth.example/api",
		CodexClientID:        "client-id",
		CodexOriginator:      "originator",
		Thinking:             "enabled",
		ReasoningEffort:      "high",
		DisableSpark:         true,
		ContextWindowTokens:  900000,
		ContextCompactTokens: 600000,
		WebSummaryMode:       "model",
		WebSummaryProvider:   "deepseek",
		WebSummaryModel:      "deepseek-v4-flash",
		WebCacheEnabled:      true,
		WebCacheTTL:          5 * time.Minute,
		WebCacheMaxBytes:     123456,
		MaxToolRounds:        42,
		MaxParallelTools:     7,
		GatewayAddr:          "127.0.0.1:9999",
		MCPEnabled:           true,
		MCPAllowedServers:    []string{"github", "context7"},
		MaxToolOutputBytes:   8192,
	}

	diagnostics := cfg.DiagnosticSnapshot()
	snapshot := diagnostics.ProviderAuth

	if snapshot.Provider != "openai-codex" || snapshot.Model != "gpt-5.4-mini" || snapshot.Profile != "auditprofile" {
		t.Fatalf("snapshot routing = %#v", snapshot)
	}
	if !snapshot.DisableSpark || snapshot.Thinking != "enabled" || snapshot.ReasoningEffort != "high" {
		t.Fatalf("snapshot model settings = %#v", snapshot)
	}
	if snapshot.APIKeyEnv != "CUSTOM_DEEPSEEK_KEY" ||
		snapshot.CredentialFile != "/tmp/credentials.json" ||
		snapshot.CodexAuthFile != "/tmp/codex.json" ||
		snapshot.CodexClientID != "client-id" ||
		snapshot.CodexOriginator != "originator" {
		t.Fatalf("snapshot auth settings = %#v", snapshot)
	}
	caps := diagnostics.ProviderCapability
	if caps.Provider != "openai-codex" ||
		caps.Model != "gpt-5.4-mini" ||
		caps.ContextWindowTokens != 400_000 ||
		caps.MaxOutputTokens != 8192 ||
		!caps.ToolCalls ||
		!caps.ParallelToolCalls ||
		!caps.Streaming ||
		!caps.Reasoning ||
		caps.WebSummaryModel != "gpt-5.4-mini" ||
		caps.MemoryHelperModel != "gpt-5.4-mini" ||
		caps.CostMode != "subscription" ||
		!caps.Subscription ||
		caps.ValidationError != "" {
		t.Fatalf("capability snapshot = %#v", caps)
	}
	runtimeTool := diagnostics.RuntimeTool
	if runtimeTool.ContextWindowTokens != 900000 ||
		runtimeTool.ContextCompactTokens != 600000 ||
		runtimeTool.WebSummaryMode != "model" ||
		runtimeTool.WebCacheTTLMS != int64((5*time.Minute).Milliseconds()) ||
		runtimeTool.WebCacheMaxBytes != 123456 ||
		runtimeTool.MaxToolRounds != 42 ||
		runtimeTool.MaxParallelTools != 7 ||
		runtimeTool.GatewayAddr != "127.0.0.1:9999" ||
		!runtimeTool.MCPEnabled ||
		runtimeTool.MCPAllowedServers != "github,context7" ||
		runtimeTool.MaxToolOutputBytes != 8192 {
		t.Fatalf("snapshot runtime/tool settings = %#v", runtimeTool)
	}
}

func TestProviderBindingCarriesAuthAndRuntimeSettings(t *testing.T) {
	cfg := Config{
		Provider:            "deepseek",
		Model:               "deepseek-v4-flash",
		BaseURL:             "https://deepseek.example",
		CodexBaseURL:        "https://codex.example/backend",
		APIKeyEnv:           "CUSTOM_DEEPSEEK_KEY",
		CredentialFile:      "/tmp/credentials.json",
		CodexAuthFile:       "/tmp/codex.json",
		CodexRefreshURL:     "https://auth.example/token",
		CodexAuthAPIBaseURL: "https://auth.example/api",
		CodexClientID:       "client-id",
		CodexOriginator:     "originator",
		Thinking:            "enabled",
		ReasoningEffort:     "high",
		MaxTokens:           777,
		RequestTimeout:      3 * time.Second,
		StreamIdleTimeout:   4 * time.Second,
		ProviderMaxRetries:  5,
	}

	binding := cfg.ProviderBinding()

	if binding.Provider.Provider != "deepseek" || binding.Provider.BaseURL != cfg.BaseURL || binding.Provider.CodexBaseURL != cfg.CodexBaseURL {
		t.Fatalf("provider binding = %#v", binding.Provider)
	}
	if binding.Auth.APIKeyEnv != cfg.APIKeyEnv ||
		binding.Auth.CredentialFile != cfg.CredentialFile ||
		binding.Auth.CodexAuthFile != cfg.CodexAuthFile ||
		binding.Auth.CodexRefreshURL != cfg.CodexRefreshURL ||
		binding.Auth.CodexAuthAPIBaseURL != cfg.CodexAuthAPIBaseURL ||
		binding.Auth.CodexClientID != cfg.CodexClientID ||
		binding.Auth.CodexOriginator != cfg.CodexOriginator {
		t.Fatalf("auth binding = %#v", binding.Auth)
	}
	if binding.Model.Model != cfg.Model || binding.Model.Thinking != cfg.Thinking || binding.Model.ReasoningEffort != cfg.ReasoningEffort || binding.Model.MaxTokens != cfg.MaxTokens {
		t.Fatalf("model binding = %#v", binding.Model)
	}
	if binding.Limits.RequestTimeout != cfg.RequestTimeout ||
		binding.Limits.StreamIdleTimeout != cfg.StreamIdleTimeout ||
		binding.Limits.ProviderMaxRetries != cfg.ProviderMaxRetries {
		t.Fatalf("runtime binding = %#v", binding.Limits)
	}
}

func TestConfigProjectionsReturnDefensiveCopies(t *testing.T) {
	cfg := Config{
		WorkspaceRoots:      []string{"/repo"},
		ProjectDocFallbacks: []string{"README.md"},
		MCPEnabled:          true,
		MCPConfigFiles:      []string{"mcp.toml"},
		MCPAllowedServers:   []string{"github"},
		MCPServers: []MCPServer{{
			Name:           "github",
			Args:           []string{"serve"},
			Env:            map[string]string{"TOKEN": "secret"},
			EnvVars:        []string{"TOKEN"},
			HTTPHeaders:    map[string]string{"Authorization": "Bearer token"},
			EnvHTTPHeaders: map[string]string{"X-Token": "TOKEN"},
			EnabledTools:   []string{"search"},
			DisabledTools:  []string{"delete"},
		}},
	}

	toolPolicy := cfg.ToolPolicySettings()
	mcp := cfg.MCPSettings()
	instructions := cfg.InstructionSettings()

	toolPolicy.WorkspaceRoots[0] = "/other"
	toolPolicy.ProjectDocFallbacks[0] = "OTHER.md"
	instructions.WorkspaceRoots[0] = "/instructions-other"
	instructions.ProjectDocFallbacks[0] = "INSTRUCTIONS.md"
	mcp.ConfigFiles[0] = "other.toml"
	mcp.AllowedServers[0] = "other"
	mcp.Servers[0].Args[0] = "other"
	mcp.Servers[0].Env["TOKEN"] = "changed"
	mcp.Servers[0].EnvVars[0] = "OTHER_TOKEN"
	mcp.Servers[0].HTTPHeaders["Authorization"] = "changed"
	mcp.Servers[0].EnvHTTPHeaders["X-Token"] = "OTHER_TOKEN"
	mcp.Servers[0].EnabledTools[0] = "other"
	mcp.Servers[0].DisabledTools[0] = "other"

	if cfg.WorkspaceRoots[0] != "/repo" || cfg.ProjectDocFallbacks[0] != "README.md" {
		t.Fatalf("tool projection mutated config: %#v", cfg)
	}
	server := cfg.MCPServers[0]
	if cfg.MCPConfigFiles[0] != "mcp.toml" ||
		cfg.MCPAllowedServers[0] != "github" ||
		server.Args[0] != "serve" ||
		server.Env["TOKEN"] != "secret" ||
		server.EnvVars[0] != "TOKEN" ||
		server.HTTPHeaders["Authorization"] != "Bearer token" ||
		server.EnvHTTPHeaders["X-Token"] != "TOKEN" ||
		server.EnabledTools[0] != "search" ||
		server.DisabledTools[0] != "delete" {
		t.Fatalf("MCP projection mutated config: %#v", cfg)
	}
}

func TestRuntimeDiffOverridesFromSettingsMatchesConfigDiff(t *testing.T) {
	base := builtInConfig()
	current := base
	current.Provider = "openai-codex"
	current.Model = "gpt-5.5"
	current.Profile = "billy"
	current.ReasoningEffort = "xhigh"
	current.MaxToolRounds = 17
	current.MaxParallelTools = 2
	current.WebSummaryMode = "model"
	current.WebSummaryProvider = "openai-codex"
	current.WebSummaryModel = "gpt-5.4-mini"
	current.MCPEnabled = true
	current.MCPConfigFiles = []string{"/tmp/mcp.toml"}
	current.MCPAllowedServers = []string{"search"}
	current.HooksEnabled = true
	current.HookConfigFiles = []string{"/tmp/hooks.toml"}
	current.GatewayAddr = "127.0.0.1:9999"
	current.ApplyModelProviderDefaults()
	current.ApplyWebSummaryDefaults()

	want := RuntimeDiffOverrides(base, current, SourceGateway)
	got := RuntimeDiffOverridesFromSettings(base, RuntimeDiffSettingsFromConfig(current), SourceGateway)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RuntimeDiffOverridesFromSettings mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRuntimeDiffSettingsWithRunOverrides(t *testing.T) {
	base := builtInConfig()
	base.Provider = "deepseek"
	base.Model = "deepseek-v4-flash"
	base.Profile = "billy"
	base.MaxToolRounds = 100
	base.ApplyModelProviderDefaults()

	settings, err := RuntimeDiffSettingsWithRunOverrides(RuntimeDiffSettingsFromConfig(base), RunOverrideSettings{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		Profile:         "billy",
		Thinking:        "enabled",
		ReasoningEffort: "xhigh",
		MaxToolRounds:   9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Provider.Provider.Provider != "openai-codex" ||
		settings.Provider.Model.Model != "gpt-5.5" ||
		settings.Profile.Profile != "billy" ||
		settings.Provider.Model.Thinking != "enabled" ||
		settings.Provider.Model.ReasoningEffort != "xhigh" ||
		settings.Runtime.MaxToolRounds != 9 {
		t.Fatalf("settings = %#v", settings)
	}
}
