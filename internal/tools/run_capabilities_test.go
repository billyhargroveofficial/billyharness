package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func TestIsolatedRunCapabilitiesRequireBothNonEmptyAllowlists(t *testing.T) {
	registry := NewRegistry(config.Default())
	for _, test := range []struct {
		name     string
		scope    string
		tools    []string
		prefixes []string
	}{
		{name: "missing tools", scope: protocol.CapabilityScopeIsolatedPlanV1, prefixes: []string{"https://example.com/api"}},
		{name: "missing prefixes", scope: protocol.CapabilityScopeIsolatedPlanV1, tools: []string{"web_fetch"}},
		{name: "missing scope", tools: []string{"web_fetch"}, prefixes: []string{"https://example.com/api"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if capabilities, err := registry.NewRunCapabilities(test.scope, test.tools, test.prefixes); err == nil {
				t.Fatalf("invalid capabilities accepted: %#v", capabilities)
			}
		})
	}
}

func TestRunCapabilitiesFilterSpecsAndDenyRegistryCalls(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch", "time_now"},
		[]string{"https://example.com/api"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(context.Background(), cfg.ToolPolicySettings(), capabilities)
	specs := snapshot.Specs()
	if len(specs) != 2 || specs[0].Name != "time_now" || specs[1].Name != "web_fetch" {
		t.Fatalf("bounded specs = %#v", specs)
	}

	result, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: []byte(`{"path":"README.md"}`),
	})
	if err == nil || result.ErrorCode != "permission_denied" ||
		result.Metadata["permission_reason"] != "tool_not_allowlisted_for_run" {
		t.Fatalf("disallowed call result=%#v err=%v", result, err)
	}
	if decision := snapshot.PolicyDecision("fs_read_file"); decision.Decision != "deny" ||
		decision.Source != "run_capabilities" || decision.Reason != "tool_not_allowlisted_for_run" {
		t.Fatalf("disallowed policy decision = %#v", decision)
	}
}

func TestDurableJobRunCapabilitiesEnforceSeparateReadAndWriteRoots(t *testing.T) {
	readRoot := t.TempDir()
	writeRoot := t.TempDir()
	readPath := filepath.Join(readRoot, "notes.md")
	if err := os.WriteFile(readPath, []byte("bounded evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePath := filepath.Join(writeRoot, "result.md")

	cfg := config.Default()
	cfg.AccessMode = config.AccessModeBuild
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewDurableJobRunCapabilities(
		[]string{"fs_read_file", "fs_write_file", "time_now"},
		[]string{readRoot},
		[]string{writeRoot},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	broaderPolicy := cfg.ToolPolicySettings()
	broaderPolicy.WorkspaceRoots = []string{readRoot, writeRoot}
	callCtx := contextWithToolPolicy(t.Context(), broaderPolicy)
	if specs := snapshot.Specs(); len(specs) != 3 || specs[0].Name != "fs_read_file" || specs[1].Name != "fs_write_file" || specs[2].Name != "time_now" {
		t.Fatalf("durable specs = %#v", specs)
	}
	read, err := snapshot.Call(callCtx, protocol.ToolCall{Name: "fs_read_file", Arguments: rawArgs(map[string]any{"path": readPath})})
	if err != nil || !strings.Contains(read.Content, "bounded evidence") {
		t.Fatalf("authorized read result=%#v err=%v", read, err)
	}
	if result, err := snapshot.Call(callCtx, protocol.ToolCall{Name: "fs_read_file", Arguments: rawArgs(map[string]any{"path": writePath})}); err == nil {
		t.Fatalf("read crossed into write-only root: %#v", result)
	}
	write, err := snapshot.Call(callCtx, protocol.ToolCall{Name: "fs_write_file", Arguments: rawArgs(map[string]any{"path": writePath, "content": "result"})})
	if err != nil || write.IsError {
		t.Fatalf("authorized write result=%#v err=%v", write, err)
	}
	if got, err := os.ReadFile(writePath); err != nil || string(got) != "result" {
		t.Fatalf("written file=%q err=%v", got, err)
	}
	if result, err := snapshot.Call(callCtx, protocol.ToolCall{Name: "fs_write_file", Arguments: rawArgs(map[string]any{"path": filepath.Join(readRoot, "forbidden.md"), "content": "no"})}); err == nil {
		t.Fatalf("write crossed into read-only root: %#v", result)
	}
}

func TestDurableJobRunCapabilitiesFailClosedForUnsafeToolsAndMissingDimensions(t *testing.T) {
	registry := NewRegistry(config.Default())
	for _, test := range []struct {
		name     string
		tools    []string
		reads    []string
		writes   []string
		prefixes []string
		want     string
	}{
		{name: "shell", tools: []string{"shell_exec"}, want: "not enforceable"},
		{name: "memory", tools: []string{"memory_read"}, want: "not enforceable"},
		{name: "read root", tools: []string{"fs_read_file"}, want: "read_roots"},
		{name: "write root", tools: []string{"fs_write_file"}, want: "write_roots"},
		{name: "network", tools: []string{"web_fetch"}, want: "allowed_url_prefixes"},
		{name: "restricted search", tools: []string{"web_search"}, prefixes: []string{"https://docs.example/"}, want: "requires allowed_url_prefixes"},
		{name: "wildcard root", tools: []string{"fs_read_file"}, reads: []string{"*"}, want: "concrete absolute"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := registry.NewDurableJobRunCapabilities(test.tools, test.reads, test.writes, test.prefixes)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("capability error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDurableJobRunCapabilitiesCloneRootsAndWildcardAttestation(t *testing.T) {
	readRoot := t.TempDir()
	writeRoot := t.TempDir()
	resolvedReadRoot, err := resolvedPathForPolicy(readRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedWriteRoot, err := resolvedPathForPolicy(writeRoot)
	if err != nil {
		t.Fatal(err)
	}
	reads := []string{readRoot}
	writes := []string{writeRoot}
	registry := NewRegistry(config.Default())
	capabilities, err := registry.NewDurableJobRunCapabilities(
		[]string{"fs_read_file", "fs_write_file", "web_search"}, reads, writes, []string{"*"},
	)
	if err != nil {
		t.Fatal(err)
	}
	reads[0] = "/mutated-input"
	writes[0] = "/mutated-input"
	clone := capabilities.Clone()
	cloneReads := clone.ReadRoots()
	cloneWrites := clone.WriteRoots()
	cloneReads[0] = "/mutated-clone"
	cloneWrites[0] = "/mutated-clone"
	if got := capabilities.ReadRoots(); len(got) != 1 || got[0] != resolvedReadRoot {
		t.Fatalf("read roots mutated through alias: %#v", got)
	}
	if got := capabilities.WriteRoots(); len(got) != 1 || got[0] != resolvedWriteRoot {
		t.Fatalf("write roots mutated through alias: %#v", got)
	}
	attestation := capabilities.Attestation()
	wildcardHash := sha256.Sum256([]byte(`["*"]`))
	if attestation.Scope != protocol.CapabilityScopeDurableJobV1 ||
		attestation.AllowedToolsCount != 3 ||
		attestation.AllowedURLPrefixesCount != 1 ||
		attestation.AllowedURLPrefixesSHA256 != hex.EncodeToString(wildcardHash[:]) ||
		attestation.ReadRootsCount != 1 || attestation.WriteRootsCount != 1 {
		t.Fatalf("durable attestation = %#v", attestation)
	}
}

func TestDurableJobCapabilityPinsResolvedRootAgainstSymlinkRetarget(t *testing.T) {
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "note.txt"), []byte("FIRST_ROOT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "note.txt"), []byte("SECOND_ROOT_MUST_NOT_LEAK"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "authorized")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.Default())
	capabilities, err := registry.NewDurableJobRunCapabilities([]string{"fs_read_file"}, []string{link}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	result, err := snapshot.Call(t.Context(), protocol.ToolCall{
		Name: "fs_read_file", Arguments: rawArgs(map[string]any{"path": filepath.Join(link, "note.txt")}),
	})
	if err == nil || strings.Contains(result.Content, "SECOND_ROOT_MUST_NOT_LEAK") {
		t.Fatalf("retargeted root escaped pinned capability: result=%#v err=%v", result, err)
	}
}

func TestDurableJobNetworkPrefixesDenyOtherHostsBeforeHandler(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewDurableJobRunCapabilities(
		[]string{"web_fetch"}, nil, nil, []string{"https://docs.example/"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	result, err := snapshot.Call(t.Context(), protocol.ToolCall{
		Name: "web_fetch", Arguments: []byte(`{"url":"https://evil.example/private"}`),
	})
	if err == nil || result.ErrorCode != "permission_denied" || result.Metadata["permission_reason"] != "url_not_allowlisted_for_run" {
		t.Fatalf("host denial result=%#v err=%v", result, err)
	}
	if _, err := registry.NewDurableJobRunCapabilities([]string{"web_search"}, nil, nil, []string{"*"}); err != nil {
		t.Fatalf("explicit unrestricted HTTPS search rejected: %v", err)
	}
}

func TestDurableJobWildcardSearchCannotRecoverAmbientProviderBackend(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	cfg.WebSearchBackend = "tavily"
	cfg.WebTavilyAPIKeyEnv = "AMBIENT_TAVILY_KEY"
	t.Setenv("AMBIENT_TAVILY_KEY", "must-not-be-used")
	registry := NewRegistry(cfg, WithNativeWebClient(webtools.Client{
		Resolver: webtools.ResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("bounded native transport reached")
		}),
	}))
	capabilities, err := registry.NewDurableJobRunCapabilities([]string{"web_search"}, nil, nil, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	ambient := cfg.ToolPolicySettings()
	ambient.WebSearchBackend = "tavily"
	ctx := contextWithToolPolicy(t.Context(), ambient)
	result, err := snapshot.Call(ctx, protocol.ToolCall{Name: "web_search", Arguments: []byte(`{"query":"bounded capabilities"}`)})
	if err == nil || !strings.Contains(err.Error(), "bounded native transport reached") {
		t.Fatalf("isolated search recovered ambient backend: result=%#v err=%v", result, err)
	}
}

func TestRunCapabilitiesFilterToolSearchResults(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"tool_search", "web_fetch"},
		[]string{"https://example.com/"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(context.Background(), cfg.ToolPolicySettings(), capabilities)
	result, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "tool_search",
		Arguments: []byte(`{"query":"","limit":80}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, `"name": "web_fetch"`) ||
		!strings.Contains(result.Content, `"name": "tool_search"`) {
		t.Fatalf("allowlisted discovery results missing: %s", result.Content)
	}
	for _, disallowed := range []string{"fs_read_file", "memory_read", "skill_read", "shell_exec", "mcp_call"} {
		if strings.Contains(result.Content, `"`+disallowed+`"`) {
			t.Fatalf("tool_search leaked disallowed tool %q: %s", disallowed, result.Content)
		}
	}
}

func TestRunCapabilitiesRejectWebURLBeforeHandlerOrCache(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.com/api"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(context.Background(), cfg.ToolPolicySettings(), capabilities)
	result, err := snapshot.Call(context.Background(), protocol.ToolCall{
		Name:      "web_fetch",
		Arguments: []byte(`{"url":"https://example.com/private"}`),
	})
	if err == nil || result.ErrorCode != "permission_denied" ||
		result.Metadata["permission_reason"] != "url_not_allowlisted_for_run" ||
		!strings.Contains(err.Error(), "outside allowed_url_prefixes") {
		t.Fatalf("URL denial result=%#v err=%v", result, err)
	}
}

func TestRestrictedSnapshotOverridesBroaderCapabilitiesPreseededInContext(t *testing.T) {
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	broader, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.com/api", "https://example.com/private"},
	)
	if err != nil {
		t.Fatal(err)
	}
	narrower, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.com/api"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), narrower)
	ctx := contextWithRunCapabilities(t.Context(), broader)
	result, err := snapshot.Call(ctx, protocol.ToolCall{
		Name:      "web_fetch",
		Arguments: []byte(`{"url":"https://example.com/private"}`),
	})
	if err == nil || result.ErrorCode != "permission_denied" ||
		result.Metadata["permission_reason"] != "url_not_allowlisted_for_run" {
		t.Fatalf("broader context survived snapshot boundary: result=%#v err=%v", result, err)
	}
}

func TestRestrictedWebRunDoesNotReuseWarmedUnrestrictedRedirectCache(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	cfg.WebCacheEnabled = true
	cfg.WebCacheTTL = time.Hour
	cfg.WebCacheMaxBytes = 1 << 20
	restrictedTransport := false
	client := webtools.Client{
		Resolver: webtools.ResolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
			if restrictedTransport {
				return nil, errors.New("restricted transport reached")
			}
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}),
	}
	registry := NewRegistry(cfg, WithNativeWebClient(client))
	startURL := "https://example.test/api/start"
	opts := webFetchOptions{}
	unrestrictedKey, ok := registry.webCacheKey(t.Context(), "web_fetch", startURL, opts, nil)
	if !ok {
		t.Fatal("unrestricted cache key missing")
	}
	ref, err := storeWebOutput("web_fetch", "https://example.test/outside", "OUTSIDE_REDIRECT_CACHE_CONTENT")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.saveWebPageCache(unrestrictedKey, compactPage{
		URL:         "https://example.test/outside",
		Summary:     "OUTSIDE_REDIRECT_CACHE_CONTENT",
		OutputClass: "extractive_summary",
		OutputRef:   ref,
	}); err != nil {
		t.Fatal(err)
	}
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.test/api/start"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(t.Context(), cfg.ToolPolicySettings(), capabilities)
	if key, enabled := snapshot.registry.webCacheKey(t.Context(), "web_fetch", startURL, opts, nil); enabled || key != "" {
		t.Fatalf("restricted page cache unexpectedly enabled: key=%q enabled=%v", key, enabled)
	}
	if key, enabled := snapshot.registry.webCacheKey(t.Context(), "web_crawl", startURL, opts, map[string]any{"max_pages": 1}); enabled || key != "" {
		t.Fatalf("restricted crawl cache unexpectedly enabled: key=%q enabled=%v", key, enabled)
	}
	restrictedTransport = true
	result, err := snapshot.Call(t.Context(), protocol.ToolCall{
		Name:      "web_fetch",
		Arguments: []byte(`{"url":"https://example.test/api/start"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "restricted transport reached") {
		t.Fatalf("restricted call did not reach bounded transport: result=%#v err=%v", result, err)
	}
	if strings.Contains(result.Content, "OUTSIDE_REDIRECT_CACHE_CONTENT") {
		t.Fatalf("restricted call replayed unrestricted redirect cache: %#v", result)
	}
}

func TestRunCapabilitiesAttestationUsesCanonicalJSONArrayHashes(t *testing.T) {
	registry := NewRegistry(config.Default())
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch", "time_now"},
		[]string{"https://b.example:443/api/", "https://a.example/"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := capabilities.Attestation()
	toolsHash := sha256.Sum256([]byte(`["time_now","web_fetch"]`))
	prefixHash := sha256.Sum256([]byte(`["https://a.example/","https://b.example/api"]`))
	if got.Scope != protocol.CapabilityScopeIsolatedPlanV1 ||
		got.AllowedToolsCount != 2 ||
		got.AllowedToolsSHA256 != hex.EncodeToString(toolsHash[:]) ||
		got.AllowedURLPrefixesCount != 2 ||
		got.AllowedURLPrefixesSHA256 != hex.EncodeToString(prefixHash[:]) {
		t.Fatalf("attestation = %#v", got)
	}
}

func TestIsolatedRunSnapshotOmitsAmbientMCPCatalogStatusAndInstructions(t *testing.T) {
	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summary"
	cfg.WebCacheEnabled = true
	cfg.WebSearchBackend = "tavily"
	cfg.WebExtractBackend = "tavily"
	registry := NewRegistry(cfg, WithWebSummarizer(fatalSummarizer{t: t}))
	registry.mcpTools["mcp__fake__echo"] = fakeMCPTool("mcp__fake__echo", "echo")
	registry.mcpStatuses = []mcpclient.ServerStatus{{
		Name:      "fake",
		Enabled:   true,
		Connected: true,
		ToolCount: 1,
	}}
	registry.instructions = []string{"PROMOTED_MCP_CONTEXT_MUST_NOT_LEAK"}
	registry.mcpServerInstructions = []string{"UNTRUSTED_MCP_CONTEXT_MUST_NOT_LEAK"}
	capabilities, err := registry.NewRunCapabilities(
		protocol.CapabilityScopeIsolatedPlanV1,
		[]string{"web_fetch"},
		[]string{"https://example.com/api"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.SnapshotWithToolPolicyAndCapabilities(
		context.Background(),
		cfg.ToolPolicySettings(),
		capabilities,
	)
	if snapshot.registry == nil ||
		len(snapshot.registry.mcpTools) != 0 ||
		len(snapshot.registry.mcpStatuses) != 0 ||
		len(snapshot.registry.instructions) != 0 ||
		len(snapshot.registry.mcpServerInstructions) != 0 ||
		snapshot.registry.mcpSettings.Enabled ||
		snapshot.registry.toolPolicy.WebSummaryMode != "extractive" ||
		snapshot.registry.toolPolicy.WebCacheEnabled ||
		snapshot.registry.toolPolicy.WebSearchBackend != "native" ||
		snapshot.registry.toolPolicy.WebExtractBackend != "native" ||
		snapshot.registry.webSummarizer != nil {
		t.Fatalf("isolated MCP snapshot leaked ambient state: %#v", snapshot.registry)
	}
	ctx := contextWithRunCapabilities(t.Context(), capabilities)
	page := fetchedPage{URL: "https://example.com/api", Text: strings.Repeat("evidence ", 200)}
	compact := compactFetchedPage(page, webFetchOptions{})
	registry.applyModelSummaryToPage(ctx, &compact, page, webFetchOptions{})
	if compact.SummaryMode == "model" {
		t.Fatalf("isolated web fetch used ambient model summarizer: %#v", compact)
	}
}
