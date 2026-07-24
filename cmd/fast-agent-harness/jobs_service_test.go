package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/jobruntime"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func TestDurableJobBindingResolverPinsEachBuiltInRouteWithoutSecretsOrFailover(t *testing.T) {
	const qwenSecret = "qwen-test-secret-must-not-enter-binding"
	t.Setenv("QWEN_TOKEN_PLAN_API_KEY", qwenSecret)
	base := config.BuiltIn()
	base.ProviderMaxRetries = 9
	resolver := durableJobBindingResolver(base)
	tests := []struct {
		name       string
		route      jobs.ExecutionRoute
		wantAPIEnv string
	}{
		{
			name:       "deepseek",
			route:      jobs.ExecutionRoute{ProviderID: "deepseek", ModelID: "deepseek-v4-pro", Thinking: "enabled", ReasoningEffort: "high"},
			wantAPIEnv: "DEEPSEEK_API_KEY",
		},
		{
			name:       "qwen subscription",
			route:      jobs.ExecutionRoute{ProviderID: "qwen", ModelID: "qwen3.8-max-preview", Thinking: "enabled", ReasoningEffort: "xhigh"},
			wantAPIEnv: "QWEN_TOKEN_PLAN_API_KEY",
		},
		{
			name:  "codex subscription",
			route: jobs.ExecutionRoute{ProviderID: "openai-codex", ModelID: "gpt-5.5", Thinking: "enabled", ReasoningEffort: "xhigh"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding, err := resolver.ResolveBinding(context.Background(), test.route)
			if err != nil {
				t.Fatal(err)
			}
			if binding.Provider.Provider != test.route.ProviderID ||
				binding.Model.Model != test.route.ModelID ||
				binding.Model.Thinking != test.route.Thinking ||
				binding.Model.ReasoningEffort != test.route.ReasoningEffort {
				t.Fatalf("binding route = %#v, want %#v", binding, test.route)
			}
			if binding.Limits.ProviderMaxRetries != 0 {
				t.Fatalf("binding retained provider retries: %#v", binding.Limits)
			}
			if test.wantAPIEnv != "" && binding.Auth.APIKeyEnv != test.wantAPIEnv {
				t.Fatalf("API key env = %q, want %q", binding.Auth.APIKeyEnv, test.wantAPIEnv)
			}
			encoded, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), qwenSecret) {
				t.Fatalf("binding resolved credential value: %s", encoded)
			}
		})
	}
}

func TestDurableJobBindingResolverDoesNotLeakPrimaryBindingAcrossProviders(t *testing.T) {
	base := config.BuiltIn()
	base.Provider = "deepseek"
	base.Model = "deepseek-v4-pro"
	base.BaseURL = "https://private-deepseek.invalid/v1"
	base.APIKeyEnv = "PRIVATE_DEEPSEEK_KEY"
	resolver := durableJobBindingResolver(base)

	qwenRoute := jobs.ExecutionRoute{
		ProviderID: "qwen", ModelID: "qwen3.8-max-preview", Thinking: "enabled", ReasoningEffort: "high",
	}
	binding, err := resolver.ResolveBinding(context.Background(), qwenRoute)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Provider.BaseURL == base.BaseURL || binding.Auth.APIKeyEnv == base.APIKeyEnv {
		t.Fatalf("primary provider binding leaked across routes: %#v", binding)
	}
	if binding.Provider.BaseURL != "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1" ||
		binding.Auth.APIKeyEnv != "QWEN_TOKEN_PLAN_API_KEY" {
		t.Fatalf("Qwen provider binding = %#v", binding)
	}
}

func TestDurableJobBindingResolverAllowsOnlyTheConfiguredCustomProvider(t *testing.T) {
	custom := config.BuiltIn()
	custom.Provider = "my-compatible"
	custom.Model = "private-model-v7"
	custom.BaseURL = "https://models.example.invalid/v1"
	custom.APIKeyEnv = "MY_COMPATIBLE_KEY"
	resolver := durableJobBindingResolver(custom)
	route := jobs.ExecutionRoute{ProviderID: "my-compatible", ModelID: "private-model-v7", Thinking: "disabled", ReasoningEffort: "off"}
	binding, err := resolver.ResolveBinding(context.Background(), route)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Provider.BaseURL != custom.BaseURL || binding.Auth.APIKeyEnv != custom.APIKeyEnv {
		t.Fatalf("configured custom binding = %#v", binding)
	}

	wrong := route
	wrong.ProviderID = "other-compatible"
	if _, err := resolver.ResolveBinding(context.Background(), wrong); err == nil || !strings.Contains(err.Error(), "not the gateway's explicitly configured custom binding") {
		t.Fatalf("unconfigured custom provider error = %v", err)
	}
}

func TestDurableJobServerAuthorityCuratesStructuredToolsAndWorkspaceRoots(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	protected := t.TempDir()
	cfg := config.BuiltIn()
	cfg.WorkspaceRoots = []string{nested, workspace, workspace}
	registry := newToolRegistryNoMCP(cfg)
	t.Cleanup(registry.Close)

	authority, err := durableJobServerAuthority(cfg, registry, []string{protected})
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, err := canonicalJobAuthorityRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if authority.Mode != jobs.AuthorityModeAllowList ||
		!equalJobStrings(authority.ReadRoots, []string{canonicalWorkspace}) ||
		!equalJobStrings(authority.WriteRoots, []string{canonicalWorkspace}) ||
		!equalJobStrings(authority.NetworkHosts, []string{"*"}) ||
		!containsJobString(authority.Providers, "deepseek") ||
		!containsJobString(authority.Providers, "qwen") ||
		!containsJobString(authority.Providers, "openai-codex") ||
		containsJobString(authority.Providers, "*") {
		t.Fatalf("server authority = %#v", authority)
	}
	wantTools := append([]string(nil), durableJobStructuredToolCandidates...)
	if !equalJobStrings(authority.Tools, wantTools) {
		t.Fatalf("structured tools = %#v, want %#v", authority.Tools, wantTools)
	}
	for _, forbidden := range []string{
		"ask_user", "diagnostics_run", "memory_read", "shell_exec", "shell_output",
		"skill_read", "todo_write", "tool_search", "web_cache_clear",
	} {
		if containsJobString(authority.Tools, forbidden) {
			t.Fatalf("server authority exposed forbidden tool %q: %#v", forbidden, authority.Tools)
		}
	}
}

func TestDurableJobWorkspaceAuthorityDropsRootsOverlappingStore(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := filepath.Join(workspace, "jobs")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	roots, err := durableJobWorkspaceRoots([]string{workspace, storeRoot}, []string{storeRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Fatalf("overlapping workspace roots survived: %#v", roots)
	}
}

func TestDurableJobStackOwnsRecoversAndReleasesFileStore(t *testing.T) {
	cfg := config.BuiltIn()
	cfg.WorkspaceRoots = []string{t.TempDir()}
	registry := newToolRegistryNoMCP(cfg)
	t.Cleanup(registry.Close)
	root := filepath.Join(t.TempDir(), "jobs")

	stack, err := newDurableJobStack(context.Background(), cfg, root, registry)
	if err != nil {
		t.Fatal(err)
	}
	if stack.manager == nil || stack.store == nil || stack.authority.Mode != jobs.AuthorityModeAllowList ||
		stack.invoker == nil || stack.invoker.Limit() != defaultDurableJobMaxConcurrency ||
		stack.maxConcurrentInvocations != defaultDurableJobMaxConcurrency {
		t.Fatalf("incomplete durable job stack: %#v", stack)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	stopped, err := stack.shutdown(shutdownCtx)
	cancel()
	if err != nil || !stopped {
		t.Fatalf("shutdown stopped=%t err=%v", stopped, err)
	}

	reopened, err := jobstore.NewFileStore(root, jobstore.Options{})
	if err != nil {
		t.Fatalf("store lock was not released after manager shutdown: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDurableJobStackConfiguresSharedInvocationConcurrency(t *testing.T) {
	cfg := config.BuiltIn()
	cfg.WorkspaceRoots = []string{t.TempDir()}
	registry := newToolRegistryNoMCP(cfg)
	t.Cleanup(registry.Close)
	root := filepath.Join(t.TempDir(), "jobs")

	stack, err := newDurableJobStack(
		context.Background(), cfg, root, registry, withDurableJobMaxConcurrency(3),
	)
	if err != nil {
		t.Fatal(err)
	}
	if stack.invoker == nil || stack.invoker.Limit() != 3 || stack.maxConcurrentInvocations != 3 {
		t.Fatalf("configured limiter = %#v", stack)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if stopped, err := stack.shutdown(shutdownCtx); err != nil || !stopped {
		t.Fatalf("shutdown stopped=%t err=%v", stopped, err)
	}
}

func TestDurableJobStackRejectsInvalidConcurrencyBeforeOpeningStore(t *testing.T) {
	cfg := config.BuiltIn()
	registry := newToolRegistryNoMCP(cfg)
	t.Cleanup(registry.Close)
	root := filepath.Join(t.TempDir(), "jobs")
	for _, value := range []int{0, jobruntime.MaxLimitedInvokerConcurrency + 1} {
		stack, err := newDurableJobStack(
			context.Background(), cfg, root, registry, withDurableJobMaxConcurrency(value),
		)
		if err == nil || stack != nil || !strings.Contains(err.Error(), "durable job concurrency") {
			t.Fatalf("concurrency %d: stack=%#v err=%v", value, stack, err)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid option opened job store: err=%v", err)
	}
}

func TestDefaultDurableJobStoreDirUsesBillyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	if got, want := defaultDurableJobStoreDir(), filepath.Join(home, "jobs"); got != want {
		t.Fatalf("default job store = %q, want %q", got, want)
	}
}
