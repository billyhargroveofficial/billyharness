package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/filesearch"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools/discovery"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

const (
	defaultWebBytes          = 64 * 1024
	maxWebBytes              = 512 * 1024
	webTextChars             = 6 * 1024
	webFullTextChars         = 24 * 1024
	webHardTextChars         = 24 * 1024
	webDefaultTextTokens     = 900
	webMaxTextTokens         = 8000
	webCrawlChars            = 3 * 1024
	webCrawlDefaultTokens    = 600
	webCrawlDefaultTotalToks = 2500
	webCrawlMaxTotalToks     = 12000
	webDigestChars           = 1400
	webExtractChars          = 1600
	webKeyPointChars         = 220
	webTinyDirectTokens      = 80
	webInlineDefaultTokens   = 0
	webInlineMaxTokens       = 1200
	webMaxLinks              = 20
	maxWriteBytes            = 2 * 1024 * 1024
	maxExecOutput            = 512 * 1024
	defaultSkillReadChars    = 12 * 1024
	maxSkillReadChars        = 60 * 1024
)

type Result struct {
	Content   string
	IsError   bool
	ErrorCode string
	Metadata  map[string]any
	Truncated bool
	OutputRef string
}

type Tool struct {
	Spec     protocol.ToolSpec
	Parallel ParallelMetadata
	Handler  func(context.Context, json.RawMessage) (Result, error)
}

type ParallelMetadata struct {
	Policy                     string `json:"parallel_policy,omitempty"`
	Idempotent                 bool   `json:"idempotent,omitempty"`
	RequiresExclusiveWorkspace bool   `json:"requires_exclusive_workspace,omitempty"`
	RateLimitKey               string `json:"rate_limit_key,omitempty"`
	Cancellable                bool   `json:"cancellable,omitempty"`
	MaxConcurrency             int    `json:"max_concurrency,omitempty"`
}

const (
	ParallelPolicyReadOnly           = "read_only"
	ParallelPolicyNetworkRateLimited = "network_rate_limited"
	ParallelPolicyExclusiveWorkspace = "exclusive_workspace"
	ParallelPolicyUnknownExternal    = "unknown_external"
)

type Registry struct {
	toolPolicy      config.ToolPolicySettings
	diagnostics     config.DiagnosticsSettings
	diagnosticsErr  string
	mcpSettings     config.MCPSettings
	tools           map[string]Tool
	mcpTools        map[string]Tool
	mcpCatalog      mcpCatalogState
	mcpStatuses     []mcpclient.ServerStatus
	mcpUnsubscribe  func()
	mcpMu           sync.RWMutex
	manager         *mcpclient.Manager
	instructions    []string
	fileResolver    *filesearch.Resolver
	shellMu         sync.Mutex
	shellProcesses  map[string]*managedShellProcess
	shellSeq        int64
	webSummarizer   webtools.Summarizer
	webSummarySlots chan struct{}
	webSummarySeq   int64
}

type RegistryOption func(*Registry)

type RegistrySettings struct {
	Provider    config.ProviderBinding
	ToolPolicy  config.ToolPolicySettings
	Diagnostics config.DiagnosticsSettings
	MCP         config.MCPSettings
}

type mcpCatalogState struct {
	Kind         string   `json:"kind"`
	Version      int64    `json:"version"`
	ToolCount    int      `json:"tool_count"`
	Stale        bool     `json:"stale"`
	ModelVisible bool     `json:"model_visible"`
	Collisions   []string `json:"collisions,omitempty"`
}

type modelVisibleToolCatalog struct {
	Kind                    string   `json:"kind"`
	ToolCount               int      `json:"tool_count"`
	IncludesDynamicMCPTools bool     `json:"includes_dynamic_mcp_tools"`
	MCPDiscoveryTools       []string `json:"mcp_discovery_tools"`
}

func RegistrySettingsFromConfig(cfg config.Config) RegistrySettings {
	return RegistrySettings{
		Provider:    cfg.ProviderBinding(),
		ToolPolicy:  cfg.ToolPolicySettings(),
		Diagnostics: cfg.DiagnosticsSettings(),
		MCP:         cfg.MCPSettings(),
	}
}

func WithWebSummarizer(summarizer webtools.Summarizer) RegistryOption {
	return func(r *Registry) {
		r.webSummarizer = summarizer
	}
}

func NewRegistry(cfg config.Config, opts ...RegistryOption) *Registry {
	return NewRegistryFromSettings(RegistrySettingsFromConfig(cfg), opts...)
}

func NewRegistryFromSettings(settings RegistrySettings, opts ...RegistryOption) *Registry {
	settings = cloneRegistrySettings(settings)
	diagnosticSettings, diagnosticsErr := loadRegistryDiagnosticsSettings(settings.Diagnostics)
	r := &Registry{
		toolPolicy:      settings.ToolPolicy,
		diagnostics:     diagnosticSettings,
		mcpSettings:     settings.MCP,
		tools:           map[string]Tool{},
		mcpTools:        map[string]Tool{},
		fileResolver:    filesearch.NewResolver(filesearch.DefaultCacheTTL),
		shellProcesses:  map[string]*managedShellProcess{},
		webSummarySlots: make(chan struct{}, defaultWebSummaryConcurrency),
	}
	if diagnosticsErr != nil {
		r.diagnosticsErr = diagnosticsErr.Error()
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	r.addTime()
	r.addTodoWrite()
	r.addAskUser()
	r.addFSRead()
	r.addFSList()
	r.addFSSearch()
	r.addFSGrep()
	r.addFSGlob()
	r.addFSFindFiles()
	r.addFSWrite()
	r.addFSEdit()
	r.addFSMkdir()
	r.addDiagnostics()
	r.addShellExec()
	r.addWebFetch()
	r.addWebSearch()
	r.addWebCrawl()
	r.addWebExtract()
	r.addWebCache()
	r.addToolSearch()
	r.addSkills()
	return r
}

func NewRegistryWithMCP(ctx context.Context, cfg config.Config, opts ...RegistryOption) (*Registry, error) {
	return NewRegistryWithMCPFromSettings(ctx, RegistrySettingsFromConfig(cfg), opts...)
}

func NewRegistryWithMCPFromSettings(ctx context.Context, settings RegistrySettings, opts ...RegistryOption) (*Registry, error) {
	settings = cloneRegistrySettings(settings)
	mcpSettings := settings.MCP
	if !mcpSettings.Enabled {
		return NewRegistryFromSettings(settings, opts...), nil
	}
	if settings.Provider.Provider.Provider == "mock" && len(mcpSettings.Servers) == 0 && len(mcpSettings.ConfigFiles) == 0 {
		return NewRegistryFromSettings(settings, opts...), nil
	}
	if len(mcpSettings.Servers) == 0 {
		loaded, err := config.LoadDefaultMCPSettings(mcpSettings)
		if err != nil {
			return nil, err
		}
		mcpSettings = loaded
		settings.MCP = mcpSettings
	}
	registry := NewRegistryFromSettings(settings, opts...)
	if len(mcpSettings.Servers) == 0 {
		return registry, nil
	}
	manager, err := mcpclient.NewManagerFromSettings(ctx, mcpclient.ManagerSettingsFromProjections(settings.ToolPolicy, mcpSettings))
	if err != nil {
		return nil, err
	}
	registry.manager = manager
	registry.mcpUnsubscribe = manager.AddCatalogListener(func(change mcpclient.CatalogChange) {
		registry.markMCPCatalogStale(change)
		registry.syncMCPToolsFromManager()
	})
	registry.syncMCPToolsFromManager()
	registry.addMCPGateway()
	return registry, nil
}

func cloneRegistrySettings(settings RegistrySettings) RegistrySettings {
	settings.ToolPolicy = cloneToolPolicySettings(settings.ToolPolicy)
	settings.Diagnostics = cloneDiagnosticsSettings(settings.Diagnostics)
	settings.MCP = cloneMCPSettings(settings.MCP)
	return settings
}

func cloneToolPolicySettings(settings config.ToolPolicySettings) config.ToolPolicySettings {
	settings.WorkspaceRoots = append([]string(nil), settings.WorkspaceRoots...)
	settings.ProjectDocFallbacks = append([]string(nil), settings.ProjectDocFallbacks...)
	return settings
}

func cloneDiagnosticsSettings(settings config.DiagnosticsSettings) config.DiagnosticsSettings {
	return config.Config{
		DiagnosticsEnabled:     settings.Enabled,
		DiagnosticsConfigFiles: settings.ConfigFiles,
		DiagnosticsCommands:    settings.Commands,
	}.DiagnosticsSettings()
}

func cloneMCPSettings(settings config.MCPSettings) config.MCPSettings {
	return config.Config{
		MCPEnabled:        settings.Enabled,
		MCPConfigFiles:    settings.ConfigFiles,
		MCPAllowedServers: settings.AllowedServers,
		MCPServers:        settings.Servers,
	}.MCPSettings()
}

func loadRegistryDiagnosticsSettings(settings config.DiagnosticsSettings) (config.DiagnosticsSettings, error) {
	return config.LoadDefaultDiagnosticsSettings(settings)
}

func (r *Registry) Instructions() []string {
	if r == nil {
		return nil
	}
	r.mcpMu.RLock()
	instructions := append([]string(nil), r.instructions...)
	hasParilka := false
	for name := range r.mcpTools {
		if discovery.MCPServerFromToolName(name) == "telegram_parilka" {
			hasParilka = true
			break
		}
	}
	r.mcpMu.RUnlock()
	if hasParilka {
		instructions = append(instructions, `telegram-parilka: Russian "парилка" / "Parilka" means the configured Telegram Parilka chat. For requests asking what is happening there, use mcp_list_tools with server "telegram-parilka", then mcp_call on read_history/search_messages/get_thread_context/get_chat_info. Do not inspect filesystem paths for this.`)
	}
	return instructions
}

func (r *Registry) MCPSettings() config.MCPSettings {
	if r == nil {
		return config.MCPSettings{}
	}
	settings := r.mcpSettings
	return config.Config{
		MCPEnabled:        settings.Enabled,
		MCPConfigFiles:    settings.ConfigFiles,
		MCPAllowedServers: settings.AllowedServers,
		MCPServers:        settings.Servers,
	}.MCPSettings()
}

func (r *Registry) MCPStatuses() []mcpclient.ServerStatus {
	if r == nil {
		return nil
	}
	if r.manager == nil {
		r.mcpMu.RLock()
		defer r.mcpMu.RUnlock()
		return cloneMCPStatuses(r.mcpStatuses)
	}
	return r.manager.Statuses()
}

func (r *Registry) AddMCPStatusListener(listener func(mcpclient.ServerStatus)) func() {
	if r == nil || r.manager == nil {
		return func() {}
	}
	return r.manager.AddStatusListener(listener)
}

func (r *Registry) AddMCPCatalogListener(listener func(mcpclient.CatalogChange)) func() {
	if r == nil || r.manager == nil {
		return func() {}
	}
	return r.manager.AddCatalogListener(listener)
}

func (r *Registry) refreshMCPTools(ctx context.Context) {
	if r == nil || r.manager == nil {
		return
	}
	r.manager.Refresh(ctx)
	r.syncMCPToolsFromManager()
}

func (r *Registry) syncMCPToolsFromManager() {
	if r == nil || r.manager == nil {
		return
	}
	snapshot := r.manager.CatalogSnapshot()
	next := map[string]Tool{}
	for _, external := range snapshot.Tools {
		spec := external.Spec
		handler := external.Handler
		next[spec.Name] = Tool{
			Spec: spec,
			Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
				content, err := handler(ctx, args)
				return Result{Content: content}, err
			},
		}
	}
	r.mcpMu.Lock()
	r.mcpTools = next
	r.instructions = snapshot.Instructions
	r.mcpCatalog = mcpCatalogState{
		Kind:         "dynamic_mcp_catalog",
		Version:      snapshot.Version,
		ToolCount:    len(next),
		Stale:        false,
		ModelVisible: false,
		Collisions:   append([]string(nil), snapshot.Collisions...),
	}
	r.mcpMu.Unlock()
}

func (r *Registry) markMCPCatalogStale(change mcpclient.CatalogChange) {
	if r == nil {
		return
	}
	r.mcpMu.Lock()
	if change.Version > r.mcpCatalog.Version {
		r.mcpCatalog.Stale = true
	}
	r.mcpMu.Unlock()
}

func (r *Registry) mcpCatalogSnapshot() mcpCatalogState {
	if r == nil {
		return mcpCatalogState{}
	}
	r.mcpMu.RLock()
	defer r.mcpMu.RUnlock()
	state := r.mcpCatalog
	state.Kind = "dynamic_mcp_catalog"
	state.ToolCount = len(r.mcpTools)
	state.ModelVisible = false
	state.Collisions = append([]string(nil), state.Collisions...)
	return state
}

func (r *Registry) modelVisibleToolCatalogSnapshot() modelVisibleToolCatalog {
	count := 0
	if r != nil {
		count = len(r.tools)
	}
	return modelVisibleToolCatalog{
		Kind:                    "static_gateway_tools",
		ToolCount:               count,
		IncludesDynamicMCPTools: false,
		MCPDiscoveryTools:       []string{"tool_search", "mcp_list_tools", "mcp_call"},
	}
}

func addMCPCatalogMetadata(metadata map[string]any, state mcpCatalogState) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["mcp_catalog_kind"] = state.Kind
	metadata["mcp_catalog_version"] = state.Version
	metadata["mcp_catalog_tool_count"] = state.ToolCount
	metadata["mcp_catalog_stale"] = state.Stale
	metadata["mcp_catalog_model_visible"] = state.ModelVisible
	if len(state.Collisions) > 0 {
		metadata["mcp_catalog_collisions"] = append([]string(nil), state.Collisions...)
	}
	return metadata
}

func addModelVisibleToolMetadata(metadata map[string]any, state modelVisibleToolCatalog) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["model_visible_tool_catalog_kind"] = state.Kind
	metadata["model_visible_tool_count"] = state.ToolCount
	metadata["model_visible_includes_dynamic_mcp_tools"] = state.IncludesDynamicMCPTools
	return metadata
}

func (r *Registry) mcpToolsSnapshot() map[string]Tool {
	if r == nil {
		return nil
	}
	r.mcpMu.RLock()
	defer r.mcpMu.RUnlock()
	out := make(map[string]Tool, len(r.mcpTools))
	for name, tool := range r.mcpTools {
		out[name] = tool
	}
	return out
}

func (r *Registry) hasMCPServer(server string) bool {
	if r == nil {
		return false
	}
	for name := range r.mcpToolsSnapshot() {
		if discovery.MCPServerFromToolName(name) == server {
			return true
		}
	}
	return false
}

func (r *Registry) Specs() []protocol.ToolSpec {
	specs := make([]protocol.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		if !toolVisibleForPolicy(tool.Spec, r.toolPolicy) {
			continue
		}
		specs = append(specs, tool.Spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

func toolVisibleForPolicy(spec protocol.ToolSpec, policy config.ToolPolicySettings) bool {
	if config.NormalizeAccessMode(policy.AccessMode) != config.AccessModePlan {
		return true
	}
	switch spec.Risk {
	case protocol.RiskReadOnly, protocol.RiskNetwork:
		return true
	default:
		return false
	}
}

func (r *Registry) Call(ctx context.Context, call protocol.ToolCall) (Result, error) {
	tool, ok := r.lookup(call.Name)
	if !ok {
		return errorResult("unknown_tool", fmt.Sprintf("unknown tool %s", call.Name)), fmt.Errorf("unknown tool %s", call.Name)
	}
	call.Arguments = normalizeArgs(call.Arguments)
	if decision, err := r.checkPolicy(tool); err != nil {
		result := errorResult("permission_denied", err.Error())
		result.Metadata = decision.Metadata()
		return result, err
	}
	if err := validateArgs(tool.Spec.Parameters, call.Arguments); err != nil {
		return errorResult("validation_error", err.Error()), err
	}
	return tool.Handler(ctx, call.Arguments)
}

func (r *Registry) CanRunParallel(name string) bool {
	meta, ok := r.ParallelMetadata(name)
	if !ok {
		return false
	}
	return meta.CanRunParallel()
}

func (r *Registry) ParallelMetadata(name string) (ParallelMetadata, bool) {
	tool, ok := r.lookup(name)
	if !ok {
		return ParallelMetadata{}, false
	}
	return normalizeParallelMetadata(tool.Spec.Name, tool.Spec.Risk, tool.Parallel), true
}

func (m ParallelMetadata) CanRunParallel() bool {
	return m.Idempotent && !m.RequiresExclusiveWorkspace &&
		(m.Policy == ParallelPolicyReadOnly || m.Policy == ParallelPolicyNetworkRateLimited)
}

func (r *Registry) Risk(name string) (protocol.Risk, bool) {
	tool, ok := r.lookup(name)
	if !ok {
		return "", false
	}
	return tool.Spec.Risk, true
}

func (r *Registry) lookup(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func normalizeArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || strings.TrimSpace(string(args)) == "" || strings.TrimSpace(string(args)) == "null" {
		return json.RawMessage(`{}`)
	}
	return args
}

func errorResult(code, content string) Result {
	return Result{Content: content, IsError: true, ErrorCode: code}
}

func (r *Registry) add(tool Tool) {
	tool.Parallel = normalizeParallelMetadata(tool.Spec.Name, tool.Spec.Risk, tool.Parallel)
	r.tools[tool.Spec.Name] = tool
}

func (r *Registry) Register(tool Tool) error {
	if tool.Spec.Name == "" {
		return fmt.Errorf("tool name required")
	}
	if _, exists := r.tools[tool.Spec.Name]; exists {
		return fmt.Errorf("tool %s already registered", tool.Spec.Name)
	}
	tool.Parallel = normalizeParallelMetadata(tool.Spec.Name, tool.Spec.Risk, tool.Parallel)
	r.tools[tool.Spec.Name] = tool
	return nil
}

func normalizeParallelMetadata(name string, risk protocol.Risk, meta ParallelMetadata) ParallelMetadata {
	defaults := defaultParallelMetadata(name, risk)
	if meta.Policy == "" {
		meta.Policy = defaults.Policy
	}
	if !meta.Idempotent {
		meta.Idempotent = defaults.Idempotent
	}
	if !meta.RequiresExclusiveWorkspace {
		meta.RequiresExclusiveWorkspace = defaults.RequiresExclusiveWorkspace
	}
	if meta.RateLimitKey == "" {
		meta.RateLimitKey = defaults.RateLimitKey
	}
	if !meta.Cancellable {
		meta.Cancellable = defaults.Cancellable
	}
	if meta.MaxConcurrency <= 0 {
		meta.MaxConcurrency = defaults.MaxConcurrency
	}
	return meta
}

func defaultParallelMetadata(name string, risk protocol.Risk) ParallelMetadata {
	switch name {
	case "time_now", "fs_read_file", "fs_list", "fs_search", "fs_grep", "fs_glob", "fs_find_files", "tool_search", "skill_list", "skill_read", "web_cache_status":
		return ParallelMetadata{Policy: ParallelPolicyReadOnly, Idempotent: true, Cancellable: true}
	case "web_search", "web_fetch", "web_extract", "web_crawl":
		return ParallelMetadata{Policy: ParallelPolicyNetworkRateLimited, Idempotent: true, RateLimitKey: "web", Cancellable: true, MaxConcurrency: 3}
	case AskUserToolName, "fs_write_file", "fs_edit_file", "fs_make_dir", "diagnostics_run", "shell_exec", "shell_output", "shell_kill", "web_cache_clear":
		return ParallelMetadata{Policy: ParallelPolicyExclusiveWorkspace, RequiresExclusiveWorkspace: true, Cancellable: true, MaxConcurrency: 1}
	case "mcp_list_tools", "mcp_call":
		return ParallelMetadata{Policy: ParallelPolicyUnknownExternal, RequiresExclusiveWorkspace: true, Cancellable: true, RateLimitKey: "mcp", MaxConcurrency: 1}
	}
	switch risk {
	case protocol.RiskReadOnly:
		return ParallelMetadata{Policy: ParallelPolicyReadOnly, Idempotent: true, Cancellable: true}
	case protocol.RiskNetwork:
		return ParallelMetadata{Policy: ParallelPolicyNetworkRateLimited, Idempotent: true, RateLimitKey: "network", Cancellable: true, MaxConcurrency: 2}
	default:
		return ParallelMetadata{Policy: ParallelPolicyExclusiveWorkspace, RequiresExclusiveWorkspace: true, Cancellable: true, MaxConcurrency: 1}
	}
}

func (r *Registry) Close() {
	if r != nil {
		r.closeManagedShellProcesses()
	}
	if r != nil && r.manager != nil {
		if r.mcpUnsubscribe != nil {
			r.mcpUnsubscribe()
			r.mcpUnsubscribe = nil
		}
		r.manager.Close()
		r.manager = nil
	}
}

func (r *Registry) addTime() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "time_now",
			Description: "Return the current UTC time.",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: time.Now().UTC().Format(time.RFC3339Nano)}, nil
		},
	})
}

func (r *Registry) addFSList() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_list",
			Description: "List files under an allowed workspace directory.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer","default":100}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Path  string `json:"path"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 500 {
				in.Limit = 100
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return Result{}, err
			}
			var lines []string
			for i, entry := range entries {
				if i >= in.Limit {
					lines = append(lines, fmt.Sprintf("...[truncated at %d]", in.Limit))
					break
				}
				lines = append(lines, entry.Name())
			}
			return Result{Content: strings.Join(lines, "\n")}, nil
		},
	})
}

func (r *Registry) addFSSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_search",
			Description: "Search allowed workspace files for a literal query.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"},"path":{"type":"string","default":"."},"limit":{"type":"integer","default":100}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query string `json:"query"`
				Path  string `json:"path"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Path == "" {
				in.Path = "."
			}
			if in.Limit <= 0 || in.Limit > 500 {
				in.Limit = 100
			}
			base, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			var hits []string
			_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || len(hits) >= in.Limit {
					return nil
				}
				if sensitive(path) {
					return nil
				}
				bytes, err := os.ReadFile(path)
				if err != nil || len(bytes) > 2*1024*1024 {
					return nil
				}
				for n, line := range strings.Split(string(bytes), "\n") {
					if strings.Contains(strings.ToLower(line), strings.ToLower(in.Query)) {
						hits = append(hits, fmt.Sprintf("%s:%d: %s", path, n+1, truncate(strings.TrimSpace(line), 240)))
						if len(hits) >= in.Limit {
							return nil
						}
					}
				}
				return nil
			})
			return Result{Content: strings.Join(hits, "\n")}, nil
		},
	})
}

func (r *Registry) addFSWrite() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_write_file",
			Description: "Create, overwrite, or append to a UTF-8 file under the allowed workspace. Enabled by default; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=false to disable write/execute tools.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"append":{"type":"boolean","default":false},"create_dirs":{"type":"boolean","default":true}},"required":["path","content"],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				Append     bool   `json:"append"`
				CreateDirs *bool  `json:"create_dirs"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if len(in.Content) > maxWriteBytes {
				return Result{}, fmt.Errorf("content too large: %d bytes", len(in.Content))
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			createDirs := true
			if in.CreateDirs != nil {
				createDirs = *in.CreateDirs
			}
			if createDirs {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return Result{}, err
				}
			}
			flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			if in.Append {
				flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
			}
			file, err := os.OpenFile(path, flag, 0o644)
			if err != nil {
				return Result{}, err
			}
			defer file.Close()
			n, err := file.WriteString(in.Content)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: fmt.Sprintf("wrote %d bytes to %s", n, path)}, nil
		},
	})
}

func (r *Registry) addFSMkdir() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_make_dir",
			Description: "Create a directory under the allowed workspace. Enabled by default; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=false to disable write/execute tools.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return Result{}, err
			}
			return Result{Content: "created " + path}, nil
		},
	})
}

func (r *Registry) addShellExec() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "shell_exec",
			Description: "Run a command by argv in an allowed workspace directory. Set background=true for Billy-owned long-running processes and poll with shell_output. Do not use for Telegram/Parilka chat context; use mcp_list_tools and mcp_call instead. Enabled by default; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=false to disable write/execute tools.",
			Parameters:  raw(`{"type":"object","properties":{"argv":{"type":"array","items":{"type":"string"},"minItems":1},"cwd":{"type":"string","default":"."},"timeout_sec":{"type":"integer","default":20},"max_output_bytes":{"type":"integer","default":65536},"background":{"type":"boolean","default":false,"description":"Start a Billy-owned background process and return process_id without waiting."}},"required":["argv"],"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: r.handleShellExec,
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "shell_output",
			Description: "Read bounded output from a Billy-owned background shell process by process_id and cursor. Returns inline output plus output_ref metadata for the returned slice.",
			Parameters:  raw(`{"type":"object","properties":{"process_id":{"type":"string"},"cursor":{"type":"integer","default":0},"max_output_bytes":{"type":"integer","default":65536},"tail_bytes":{"type":"integer","default":0,"description":"Optional tail window from the latest retained output; overrides cursor when positive."}},"required":["process_id"],"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: r.handleShellOutput,
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "shell_kill",
			Description: "Terminate one Billy-owned background shell process by opaque process_id. Uses process-group termination where available.",
			Parameters:  raw(`{"type":"object","properties":{"process_id":{"type":"string"}},"required":["process_id"],"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: r.handleShellKill,
	})
}

func (r *Registry) addToolSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "tool_search",
			Description: "Search static model-visible gateway tools and the dynamic MCP catalog by name or description. MCP results are call hints for mcp_call, not direct model-visible tool specs.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string","description":"Tool capability, name, or description text to search for. Empty returns the first matching tools."},"server":{"type":"string","description":"Optional MCP server filter: telegram, telegram-parilka, github, or context7."},"namespace":{"type":"string","description":"Optional namespace filter such as fs, web, shell, mcp, mcp.github, or telegram-parilka."},"risk":{"type":"string","description":"Optional risk filter: read_only, network, write, execute, or external."},"limit":{"type":"integer","default":20},"include_schema":{"type":"boolean","default":false,"description":"Include input schemas for matching tools when exact arguments are needed, capped by max_schema_tokens."},"max_schema_tokens":{"type":"integer","default":1200,"description":"Maximum estimated schema tokens to include across all returned tools."}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query           string `json:"query"`
				Server          string `json:"server"`
				Namespace       string `json:"namespace"`
				Risk            string `json:"risk"`
				Limit           int    `json:"limit"`
				IncludeSchema   bool   `json:"include_schema"`
				MaxSchemaTokens int    `json:"max_schema_tokens"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 80 {
				in.Limit = 20
			}
			r.refreshMCPTools(ctx)
			results := r.searchTools(in.Query, in.Server, in.Namespace, in.Risk, in.Limit, in.IncludeSchema, in.MaxSchemaTokens)
			catalog := r.mcpCatalogSnapshot()
			modelVisible := r.modelVisibleToolCatalogSnapshot()
			out, _ := json.MarshalIndent(map[string]any{
				"tools":               results.Items,
				"truncated":           results.Truncated,
				"metrics":             results.Metrics,
				"model_visible_tools": modelVisible,
				"mcp_catalog":         catalog,
			}, "", "  ")
			metadata := addMCPCatalogMetadata(results.Metrics.Metadata(), catalog)
			metadata = addModelVisibleToolMetadata(metadata, modelVisible)
			return Result{Content: string(out), Metadata: metadata}, nil
		},
	})
}

func (r *Registry) searchTools(query, server, namespace, risk string, limit int, includeSchema bool, maxSchemaTokens int) discovery.Results {
	if limit <= 0 {
		limit = 20
	}
	return discovery.Search(r.discoveryCandidates(true, true), discovery.Query{
		Query:           query,
		Server:          server,
		Namespace:       namespace,
		Risk:            risk,
		Limit:           limit,
		IncludeSchema:   includeSchema,
		MaxSchemaTokens: maxSchemaTokens,
	})
}

func (r *Registry) discoveryCandidates(includeNative, includeMCP bool) []discovery.Candidate {
	var candidates []discovery.Candidate
	nativeNames := make([]string, 0, len(r.tools))
	if includeNative {
		for name := range r.tools {
			nativeNames = append(nativeNames, name)
		}
		sort.Strings(nativeNames)
		for _, name := range nativeNames {
			tool := r.tools[name]
			candidates = append(candidates, discovery.Candidate{
				Spec:      tool.Spec,
				Source:    discovery.SourceNative,
				Namespace: discovery.NativeNamespace(tool.Spec.Name),
				CallTool:  tool.Spec.Name,
			})
		}
	}

	mcpTools := r.mcpToolsSnapshot()
	mcpNames := make([]string, 0, len(mcpTools))
	if includeMCP {
		for name := range mcpTools {
			mcpNames = append(mcpNames, name)
		}
		sort.Strings(mcpNames)
		for _, name := range mcpNames {
			tool := mcpTools[name]
			serverName := discovery.MCPServerFromToolName(name)
			candidates = append(candidates, discovery.Candidate{
				Spec:      tool.Spec,
				Source:    discovery.SourceMCP,
				Namespace: discovery.MCPNamespace(serverName),
				Server:    serverName,
				CallTool:  "mcp_call",
				CallName:  tool.Spec.Name,
			})
		}
	}
	return candidates
}

func (r *Registry) addSkills() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "skill_list",
			Description: "List available billyharness skills without injecting their contents into the prompt. Reads $BILLYHARNESS_HOME/skills and project .billyharness/skills; .claude/skills requires include_compat=true.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string","description":"Optional case-insensitive name/summary/source filter."},"source":{"type":"string","description":"Optional source filter: home, project, or claude_compat."},"include_compat":{"type":"boolean","default":false,"description":"Also list project .claude/skills as compatibility input."},"limit":{"type":"integer","default":40}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query         string `json:"query"`
				Source        string `json:"source"`
				IncludeCompat bool   `json:"include_compat"`
				Limit         int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 200 {
				in.Limit = 40
			}
			skills := r.discoverSkills(in.IncludeCompat)
			query := strings.ToLower(strings.TrimSpace(in.Query))
			source := normalizeSkillSource(in.Source)
			var items []skillListItem
			truncated := false
			for _, skill := range skills {
				if source != "" && skill.Source != source {
					continue
				}
				haystack := strings.ToLower(skill.Name + " " + skill.Source + " " + skill.Summary)
				if query != "" && !discovery.Matches(haystack, query) {
					continue
				}
				if len(items) >= in.Limit {
					truncated = true
					break
				}
				items = append(items, skillListItem{
					Name:    skill.Name,
					Source:  skill.Source,
					Path:    skill.Path,
					Summary: skill.Summary,
				})
			}
			out, _ := json.MarshalIndent(map[string]any{
				"skills":    items,
				"truncated": truncated,
				"metrics": map[string]any{
					"discovered":       len(skills),
					"returned":         len(items),
					"include_compat":   in.IncludeCompat,
					"content_injected": false,
				},
			}, "", "  ")
			return Result{Content: string(out), Metadata: map[string]any{
				"skills_discovered": len(skills),
				"skills_returned":   len(items),
				"truncated":         truncated,
				"include_compat":    in.IncludeCompat,
			}}, nil
		},
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "skill_read",
			Description: "Read one skill's SKILL.md on demand with a hard output cap. Use skill_list first when the exact name is unknown.",
			Parameters:  raw(`{"type":"object","properties":{"name":{"type":"string","description":"Skill directory name, for example imagegen or code-review."},"source":{"type":"string","description":"Optional source filter: home, project, or claude_compat."},"include_compat":{"type":"boolean","default":false,"description":"Allow reading project .claude/skills compatibility input."},"max_chars":{"type":"integer","default":12288}},"required":["name"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Name          string `json:"name"`
				Source        string `json:"source"`
				IncludeCompat bool   `json:"include_compat"`
				MaxChars      int    `json:"max_chars"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			name := normalizeSkillName(in.Name)
			if name == "" {
				return Result{}, fmt.Errorf("name required")
			}
			maxChars := normalizedSkillReadChars(in.MaxChars)
			source := normalizeSkillSource(in.Source)
			for _, skill := range r.discoverSkills(in.IncludeCompat) {
				if skill.Name != name {
					continue
				}
				if source != "" && skill.Source != source {
					continue
				}
				body, err := os.ReadFile(skill.Path)
				if err != nil {
					return Result{}, err
				}
				content, truncated := truncateRunesWithSimpleMarker(string(body), maxChars)
				out, _ := json.MarshalIndent(map[string]any{
					"name":           skill.Name,
					"source":         skill.Source,
					"path":           skill.Path,
					"content":        content,
					"truncated":      truncated,
					"content_bytes":  len(body),
					"returned_chars": len([]rune(content)),
				}, "", "  ")
				return Result{Content: string(out), Metadata: map[string]any{
					"skill_name":     skill.Name,
					"skill_source":   skill.Source,
					"skill_path":     skill.Path,
					"content_bytes":  len(body),
					"returned_chars": len([]rune(content)),
					"truncated":      truncated,
				}, Truncated: truncated}, nil
			}
			return Result{}, fmt.Errorf("skill %q not found", in.Name)
		},
	})
}

type skillRecord struct {
	Name    string
	Source  string
	Path    string
	Summary string
}

type skillListItem struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
}

func (r *Registry) discoverSkills(includeCompat bool) []skillRecord {
	var dirs []skillSearchDir
	dirs = append(dirs, skillSearchDir{Source: "home", Path: filepath.Join(config.BillyHomeDir(), "skills")})
	for _, root := range r.toolPolicy.WorkspaceRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		dirs = append(dirs, skillSearchDir{Source: "project", Path: filepath.Join(root, ".billyharness", "skills")})
		if includeCompat {
			dirs = append(dirs, skillSearchDir{Source: "claude_compat", Path: filepath.Join(root, ".claude", "skills")})
		}
	}
	var out []skillRecord
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir.Path)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := normalizeSkillName(entry.Name())
			if name == "" {
				continue
			}
			path := filepath.Join(dir.Path, entry.Name(), "SKILL.md")
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			key := dir.Source + "/" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, skillRecord{
				Name:    name,
				Source:  dir.Source,
				Path:    path,
				Summary: skillSummary(path),
			})
		}
	}
	return out
}

type skillSearchDir struct {
	Source string
	Path   string
}

func skillSummary(path string) string {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" && !strings.HasPrefix(line, "---") {
			return truncate(oneLine(line), 200)
		}
	}
	return ""
}

func normalizeSkillName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return strings.Trim(value, ".-")
}

func normalizeSkillSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "compat", "claude", "claude_skills":
		return "claude_compat"
	default:
		return value
	}
}

func normalizedSkillReadChars(value int) int {
	if value <= 0 {
		return defaultSkillReadChars
	}
	if value > maxSkillReadChars {
		return maxSkillReadChars
	}
	return value
}

func truncateRunesWithSimpleMarker(text string, maxRunes int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text, false
	}
	if maxRunes < 32 {
		maxRunes = 32
	}
	return string(runes[:maxRunes]) + "\n...[truncated]", true
}

func (r *Registry) addMCPGateway() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp_list_tools",
			Description: "List the dynamic MCP catalog compactly. Returned MCP tools are not direct model-visible specs; use mcp_call with a listed full tool name. For Russian парилка/Parilka chat requests, use server telegram-parilka.",
			Parameters:  raw(`{"type":"object","properties":{"server":{"type":"string","description":"Optional MCP server filter: telegram, telegram-parilka, github, or context7. telegram_parilka and парилка are also accepted."},"query":{"type":"string","description":"Optional case-insensitive name/description filter."},"namespace":{"type":"string","description":"Optional namespace filter such as mcp, mcp.github, github, or telegram-parilka."},"risk":{"type":"string","description":"Optional risk filter: read_only, network, write, execute, or external."},"limit":{"type":"integer","default":40},"include_schema":{"type":"boolean","default":false,"description":"Include matching tool input schemas only when needed to call them, capped by max_schema_tokens."},"max_schema_tokens":{"type":"integer","default":1200,"description":"Maximum estimated schema tokens to include across returned tools."}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Server          string `json:"server"`
				Query           string `json:"query"`
				Namespace       string `json:"namespace"`
				Risk            string `json:"risk"`
				Limit           int    `json:"limit"`
				IncludeSchema   bool   `json:"include_schema"`
				MaxSchemaTokens int    `json:"max_schema_tokens"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 80 {
				in.Limit = 40
			}
			type item struct {
				Name                string          `json:"name"`
				Server              string          `json:"server,omitempty"`
				Namespace           string          `json:"namespace,omitempty"`
				Risk                protocol.Risk   `json:"risk,omitempty"`
				Description         string          `json:"description,omitempty"`
				InputSchema         json.RawMessage `json:"input_schema,omitempty"`
				SchemaOmittedReason string          `json:"schema_omitted,omitempty"`
			}
			type serverItem struct {
				Name              string     `json:"name"`
				Transport         string     `json:"transport,omitempty"`
				Enabled           bool       `json:"enabled"`
				Required          bool       `json:"required"`
				Connected         bool       `json:"connected"`
				State             string     `json:"state,omitempty"`
				ToolCount         int        `json:"tool_count,omitempty"`
				UnsupportedReason string     `json:"unsupported_reason,omitempty"`
				LastError         string     `json:"last_error,omitempty"`
				RetryCount        int        `json:"retry_count,omitempty"`
				RestartCount      int        `json:"restart_count,omitempty"`
				RetryBackoffMS    int64      `json:"retry_backoff_ms,omitempty"`
				NextRetryAt       *time.Time `json:"next_retry_at,omitempty"`
				Error             string     `json:"error,omitempty"`
			}
			r.refreshMCPTools(ctx)
			results := discovery.Search(r.discoveryCandidates(false, true), discovery.Query{
				Query:           in.Query,
				Server:          in.Server,
				Namespace:       in.Namespace,
				Risk:            in.Risk,
				Limit:           in.Limit,
				IncludeSchema:   in.IncludeSchema,
				MaxSchemaTokens: in.MaxSchemaTokens,
			})
			tools := make([]item, 0, len(results.Items))
			for _, found := range results.Items {
				tools = append(tools, item{
					Name:                found.Name,
					Server:              found.Server,
					Namespace:           found.Namespace,
					Risk:                found.Risk,
					Description:         found.Description,
					InputSchema:         found.InputSchema,
					SchemaOmittedReason: found.SchemaOmittedReason,
				})
			}

			serverFilter := discovery.NormalizeMCPServerFilter(in.Server)
			query := strings.ToLower(strings.TrimSpace(in.Query))
			if serverFilter == "" && discovery.IsParilkaAlias(query) {
				serverFilter = "telegram_parilka"
			}
			statuses := r.MCPStatuses()
			servers := make([]serverItem, 0, len(statuses))
			for _, status := range statuses {
				normalized := discovery.NormalizeMCPServerFilter(status.Name)
				if serverFilter != "" && normalized != serverFilter {
					continue
				}
				servers = append(servers, serverItem{
					Name:              discovery.DisplayMCPServerName(normalized),
					Transport:         status.Transport,
					Enabled:           status.Enabled,
					Required:          status.Required,
					Connected:         status.Connected,
					State:             status.State,
					ToolCount:         status.ToolCount,
					UnsupportedReason: status.UnsupportedReason,
					LastError:         status.LastError,
					RetryCount:        status.RetryCount,
					RestartCount:      status.RestartCount,
					RetryBackoffMS:    status.RetryBackoffMS,
					NextRetryAt:       status.NextRetryAt,
					Error:             status.Error,
				})
			}
			catalog := r.mcpCatalogSnapshot()
			modelVisible := r.modelVisibleToolCatalogSnapshot()
			out, _ := json.MarshalIndent(map[string]any{
				"tools":               tools,
				"servers":             servers,
				"truncated":           results.Truncated,
				"metrics":             results.Metrics,
				"model_visible_tools": modelVisible,
				"mcp_catalog":         catalog,
			}, "", "  ")
			metadata := addMCPCatalogMetadata(results.Metrics.Metadata(), catalog)
			metadata = addModelVisibleToolMetadata(metadata, modelVisible)
			return Result{Content: string(out), Metadata: metadata}, nil
		},
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp_call",
			Description: "Call a connected dynamic MCP catalog tool by full name after inspecting it with mcp_list_tools or tool_search. For Parilka chat requests, call a tool named like mcp__telegram_parilka__read_history or mcp__telegram_parilka__search_messages.",
			Parameters:  raw(`{"type":"object","properties":{"name":{"type":"string","description":"Full MCP tool name, for example mcp__github__search_repositories."},"arguments":{"type":["object","null"],"description":"Arguments for the MCP tool.","additionalProperties":true}},"required":["name"],"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			name := strings.TrimSpace(in.Name)
			if name == "" {
				return Result{}, fmt.Errorf("name required")
			}
			if len(in.Arguments) == 0 || string(in.Arguments) == "null" {
				in.Arguments = json.RawMessage(`{}`)
			}
			r.refreshMCPTools(ctx)
			tool, ok := r.mcpToolsSnapshot()[name]
			if !ok {
				return Result{}, fmt.Errorf("unknown MCP tool %s; call mcp_list_tools first", name)
			}
			if err := validateArgs(tool.Spec.Parameters, in.Arguments); err != nil {
				return errorResult("validation_error", err.Error()), err
			}
			return tool.Handler(ctx, in.Arguments)
		},
	})
}

func (r *Registry) safePath(input string) (string, error) {
	if input == "" {
		input = "."
	}
	if !filepath.IsAbs(input) {
		input = filepath.Join(r.relativeBase(), input)
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	if sensitive(path) {
		return "", fmt.Errorf("refusing sensitive path %s", path)
	}
	policyPath, err := resolvedPathForPolicy(path)
	if err != nil {
		return "", err
	}
	if sensitive(policyPath) {
		return "", fmt.Errorf("refusing sensitive path %s", policyPath)
	}
	for _, root := range r.toolPolicy.WorkspaceRoots {
		absRoot, _ := filepath.Abs(root)
		policyRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			policyRoot = absRoot
		}
		policyRoot, _ = filepath.Abs(policyRoot)
		if policyPath == policyRoot || strings.HasPrefix(policyPath, policyRoot+string(os.PathSeparator)) {
			return path, nil
		}
	}
	return "", fmt.Errorf("path outside workspace roots: %s", path)
}

func resolvedPathForPolicy(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Abs(resolved)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	cursor := filepath.Clean(path)
	var missing []string
	for {
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return filepath.Abs(path)
		}
		missing = append([]string{filepath.Base(cursor)}, missing...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			for _, part := range missing {
				resolved = filepath.Join(resolved, part)
			}
			return filepath.Abs(resolved)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		cursor = parent
	}
}

func (r *Registry) relativeBase() string {
	if len(r.toolPolicy.WorkspaceRoots) > 0 && r.toolPolicy.WorkspaceRoots[0] != "" {
		return r.toolPolicy.WorkspaceRoots[0]
	}
	cwd, _ := os.Getwd()
	return cwd
}

func sensitive(path string) bool {
	lower := strings.ToLower(path)
	for _, needle := range []string{".env", ".ssh", "id_rsa", "id_ed25519", "auth.json", "token", "secret", ".aws", ".kube", ".docker"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("...[truncated %d bytes]", len(s)-n)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
