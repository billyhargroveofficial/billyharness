package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
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
	webMaxLinks              = 20
	maxWriteBytes            = 2 * 1024 * 1024
	maxExecOutput            = 512 * 1024
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
	Spec    protocol.ToolSpec
	Handler func(context.Context, json.RawMessage) (Result, error)
}

type Registry struct {
	cfg          config.Config
	tools        map[string]Tool
	mcpTools     map[string]Tool
	manager      *mcpclient.Manager
	instructions []string
}

func NewRegistry(cfg config.Config) *Registry {
	r := &Registry{cfg: cfg, tools: map[string]Tool{}, mcpTools: map[string]Tool{}}
	r.addTime()
	r.addFSRead()
	r.addFSList()
	r.addFSSearch()
	r.addFSWrite()
	r.addFSMkdir()
	r.addShellExec()
	r.addWebFetch()
	r.addWebSearch()
	r.addWebCrawl()
	r.addToolSearch()
	return r
}

func NewRegistryWithMCP(ctx context.Context, cfg config.Config) (*Registry, error) {
	if !cfg.MCPEnabled {
		return NewRegistry(cfg), nil
	}
	if cfg.Provider == "mock" && len(cfg.MCPServers) == 0 && len(cfg.MCPConfigFiles) == 0 {
		return NewRegistry(cfg), nil
	}
	if len(cfg.MCPServers) == 0 {
		if err := cfg.LoadDefaultMCPServers(); err != nil {
			return nil, err
		}
	}
	registry := NewRegistry(cfg)
	if len(cfg.MCPServers) == 0 {
		return registry, nil
	}
	manager, err := mcpclient.NewManager(ctx, cfg)
	if err != nil {
		return nil, err
	}
	registry.manager = manager
	registry.instructions = manager.Instructions()
	for _, external := range manager.Tools() {
		spec := external.Spec
		handler := external.Handler
		registry.mcpTools[spec.Name] = Tool{
			Spec: spec,
			Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
				content, err := handler(ctx, args)
				return Result{Content: content}, err
			},
		}
	}
	if len(registry.mcpTools) > 0 {
		registry.addMCPGateway()
	}
	return registry, nil
}

func (r *Registry) Instructions() []string {
	if r == nil {
		return nil
	}
	instructions := append([]string(nil), r.instructions...)
	if r.hasMCPServer("telegram_parilka") {
		instructions = append(instructions, `telegram-parilka: Russian "парилка" / "Parilka" means the configured Telegram Parilka chat. For requests asking what is happening there, use mcp_list_tools with server "telegram-parilka", then mcp_call on read_history/search_messages/get_thread_context/get_chat_info. Do not inspect filesystem paths for this.`)
	}
	return instructions
}

func (r *Registry) Config() config.Config {
	if r == nil {
		return config.Config{}
	}
	return r.cfg
}

func (r *Registry) MCPStatuses() []mcpclient.ServerStatus {
	if r == nil || r.manager == nil {
		return nil
	}
	return r.manager.Statuses()
}

func (r *Registry) hasMCPServer(server string) bool {
	if r == nil {
		return false
	}
	for name := range r.mcpTools {
		if mcpServerFromToolName(name) == server {
			return true
		}
	}
	return false
}

func (r *Registry) Specs() []protocol.ToolSpec {
	specs := make([]protocol.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, tool.Spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

func (r *Registry) Call(ctx context.Context, call protocol.ToolCall) (Result, error) {
	tool, ok := r.lookup(call.Name)
	if !ok {
		return errorResult("unknown_tool", fmt.Sprintf("unknown tool %s", call.Name)), fmt.Errorf("unknown tool %s", call.Name)
	}
	call.Arguments = normalizeArgs(call.Arguments)
	if err := validateArgs(tool.Spec.Parameters, call.Arguments); err != nil {
		return errorResult("validation_error", err.Error()), err
	}
	return tool.Handler(ctx, call.Arguments)
}

func (r *Registry) CanRunParallel(name string) bool {
	tool, ok := r.lookup(name)
	if !ok {
		return false
	}
	switch tool.Spec.Risk {
	case protocol.RiskReadOnly, protocol.RiskNetwork:
		return true
	default:
		return false
	}
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
	r.tools[tool.Spec.Name] = tool
}

func (r *Registry) Register(tool Tool) error {
	if tool.Spec.Name == "" {
		return fmt.Errorf("tool name required")
	}
	if _, exists := r.tools[tool.Spec.Name]; exists {
		return fmt.Errorf("tool %s already registered", tool.Spec.Name)
	}
	r.tools[tool.Spec.Name] = tool
	return nil
}

func (r *Registry) Close() {
	if r != nil && r.manager != nil {
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

func (r *Registry) addFSRead() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_read_file",
			Description: "Read a UTF-8 file from the allowed workspace.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
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
			bytes, err := os.ReadFile(path)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: string(bytes)}, nil
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
			if err := r.requireDangerous(); err != nil {
				return Result{}, err
			}
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
			if err := r.requireDangerous(); err != nil {
				return Result{}, err
			}
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
			Description: "Run a command by argv in an allowed workspace directory. Do not use for Telegram/Parilka chat context; use mcp_list_tools and mcp_call instead. Enabled by default; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=false to disable write/execute tools.",
			Parameters:  raw(`{"type":"object","properties":{"argv":{"type":"array","items":{"type":"string"},"minItems":1},"cwd":{"type":"string","default":"."},"timeout_sec":{"type":"integer","default":20},"max_output_bytes":{"type":"integer","default":65536}},"required":["argv"],"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			if err := r.requireDangerous(); err != nil {
				return Result{}, err
			}
			var in struct {
				Argv           []string `json:"argv"`
				CWD            string   `json:"cwd"`
				TimeoutSec     int      `json:"timeout_sec"`
				MaxOutputBytes int      `json:"max_output_bytes"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if len(in.Argv) == 0 || in.Argv[0] == "" {
				return Result{}, fmt.Errorf("argv required")
			}
			if in.CWD == "" {
				in.CWD = "."
			}
			if in.TimeoutSec <= 0 || in.TimeoutSec > 120 {
				in.TimeoutSec = 20
			}
			explicitMaxOutput := in.MaxOutputBytes > 0
			if in.MaxOutputBytes <= 0 || in.MaxOutputBytes > maxExecOutput {
				in.MaxOutputBytes = maxExecOutput
			}
			cwd, err := r.safePath(in.CWD)
			if err != nil {
				return Result{}, err
			}
			cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSec)*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cmdCtx, in.Argv[0], in.Argv[1:]...)
			cmd.Dir = cwd
			output, err := cmd.CombinedOutput()
			text := string(output)
			if explicitMaxOutput {
				text = truncate(text, in.MaxOutputBytes)
			}
			if cmdCtx.Err() != nil {
				return Result{Content: text}, cmdCtx.Err()
			}
			if err != nil {
				return Result{Content: text}, fmt.Errorf("command failed: %w", err)
			}
			return Result{Content: text}, nil
		},
	})
}

func (r *Registry) addWebFetch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_fetch",
			Description: "Fetch a public HTTP(S) URL and return an extractive summary, capped text, and links. Use max_tokens/max_chars to control context cost. Set full_text only when exact page text is required; output is still capped.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"max_bytes":{"type":"integer","default":65536},"max_tokens":{"type":"integer","default":900,"description":"Approximate output token budget for extracted text."},"max_chars":{"type":"integer","description":"Maximum extracted text characters returned; combined with max_tokens by taking the smaller budget."},"full_text":{"type":"boolean","default":false},"max_links":{"type":"integer","default":20}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL       string `json:"url"`
				MaxBytes  int    `json:"max_bytes"`
				MaxTokens int    `json:"max_tokens"`
				MaxChars  int    `json:"max_chars"`
				FullText  bool   `json:"full_text"`
				MaxLinks  int    `json:"max_links"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			page, err := fetchPage(ctx, in.URL, boundedBytes(in.MaxBytes))
			if err != nil {
				return Result{}, err
			}
			compact := compactFetchedPage(page, webFetchOptions{
				MaxChars:  in.MaxChars,
				MaxTokens: in.MaxTokens,
				FullText:  in.FullText,
				MaxLinks:  in.MaxLinks,
			})
			out, _ := json.MarshalIndent(compact, "", "  ")
			return Result{Content: string(out), Metadata: webPageMetadata(compact), Truncated: compact.OutputTextTruncated}, nil
		},
	})
}

func (r *Registry) addWebSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_search",
			Description: "Search the web via DuckDuckGo Lite and return public result URLs. No API key required.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","default":5}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if strings.TrimSpace(in.Query) == "" {
				return Result{}, fmt.Errorf("query required")
			}
			if in.Limit <= 0 || in.Limit > 10 {
				in.Limit = 5
			}
			results, err := searchDuckDuckGoLite(ctx, in.Query, in.Limit)
			if err != nil {
				return Result{}, err
			}
			out, _ := json.MarshalIndent(results, "", "  ")
			return Result{Content: string(out)}, nil
		},
	})
}

func (r *Registry) addWebCrawl() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_crawl",
			Description: "Crawl public HTTP(S) pages breadth-first and return compact summaries plus capped text. max_total_tokens bounds total returned crawl text.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"max_pages":{"type":"integer","default":3},"max_depth":{"type":"integer","default":1},"same_host":{"type":"boolean","default":true},"max_bytes_per_page":{"type":"integer","default":65536},"max_tokens_per_page":{"type":"integer","default":600},"max_total_tokens":{"type":"integer","default":2500},"max_chars_per_page":{"type":"integer","description":"Maximum extracted text characters per page; combined with token budgets by taking the smaller budget."},"full_text":{"type":"boolean","default":false}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL              string `json:"url"`
				MaxPages         int    `json:"max_pages"`
				MaxDepth         int    `json:"max_depth"`
				SameHost         *bool  `json:"same_host"`
				MaxBytesPerPage  int    `json:"max_bytes_per_page"`
				MaxTokensPerPage int    `json:"max_tokens_per_page"`
				MaxTotalTokens   int    `json:"max_total_tokens"`
				MaxCharsPerPage  int    `json:"max_chars_per_page"`
				FullText         bool   `json:"full_text"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			sameHost := true
			if in.SameHost != nil {
				sameHost = *in.SameHost
			}
			pages, err := crawl(ctx, in.URL, in.MaxPages, in.MaxDepth, sameHost, boundedBytes(in.MaxBytesPerPage))
			if err != nil {
				return Result{}, err
			}
			out, _ := json.MarshalIndent(compactCrawlPages(pages, webFetchOptions{
				MaxChars:       in.MaxCharsPerPage,
				MaxTokens:      in.MaxTokensPerPage,
				MaxTotalTokens: in.MaxTotalTokens,
				FullText:       in.FullText,
				MaxLinks:       0,
			}), "", "  ")
			return Result{Content: string(out)}, nil
		},
	})
}

func (r *Registry) addToolSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "tool_search",
			Description: "Search native and connected MCP tools by name or description without listing every external tool in the model prompt. Returns compact call hints.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string","description":"Tool capability, name, or description text to search for. Empty returns the first matching tools."},"server":{"type":"string","description":"Optional MCP server filter: telegram, telegram-parilka, github, or context7."},"limit":{"type":"integer","default":20},"include_schema":{"type":"boolean","default":false,"description":"Include input schema for matching tools when exact arguments are needed."}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query         string `json:"query"`
				Server        string `json:"server"`
				Limit         int    `json:"limit"`
				IncludeSchema bool   `json:"include_schema"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 80 {
				in.Limit = 20
			}
			results := r.searchTools(in.Query, in.Server, in.Limit, in.IncludeSchema)
			out, _ := json.MarshalIndent(map[string]any{
				"tools":     results.Items,
				"truncated": results.Truncated,
			}, "", "  ")
			return Result{Content: string(out), Metadata: map[string]any{"matches": len(results.Items), "truncated": results.Truncated}}, nil
		},
	})
}

type toolSearchResults struct {
	Items     []toolSearchItem
	Truncated bool
}

type toolSearchItem struct {
	Name        string          `json:"name"`
	Source      string          `json:"source"`
	Server      string          `json:"server,omitempty"`
	CallTool    string          `json:"call_tool"`
	CallName    string          `json:"call_name,omitempty"`
	Risk        protocol.Risk   `json:"risk,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

func (r *Registry) searchTools(query, server string, limit int, includeSchema bool) toolSearchResults {
	if limit <= 0 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))
	serverFilter := normalizeMCPServerFilter(server)
	if serverFilter == "" && isParilkaAlias(query) {
		serverFilter = "telegram_parilka"
		query = ""
	}
	var items []toolSearchItem
	truncated := false
	add := func(item toolSearchItem, spec protocol.ToolSpec) bool {
		haystack := strings.ToLower(spec.Name + " " + spec.Description + " " + item.Server + " " + item.Source)
		if !toolSearchMatches(haystack, query) {
			return false
		}
		if len(items) >= limit {
			truncated = true
			return true
		}
		item.Description = truncate(oneLine(spec.Description), 240)
		item.Risk = spec.Risk
		if includeSchema {
			item.InputSchema = spec.Parameters
		}
		items = append(items, item)
		return false
	}

	nativeNames := make([]string, 0, len(r.tools))
	for name := range r.tools {
		nativeNames = append(nativeNames, name)
	}
	sort.Strings(nativeNames)
	for _, name := range nativeNames {
		if serverFilter != "" {
			continue
		}
		tool := r.tools[name]
		if stop := add(toolSearchItem{
			Name:     tool.Spec.Name,
			Source:   "native",
			CallTool: tool.Spec.Name,
		}, tool.Spec); stop {
			return toolSearchResults{Items: items, Truncated: truncated}
		}
	}

	mcpNames := make([]string, 0, len(r.mcpTools))
	for name := range r.mcpTools {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		tool := r.mcpTools[name]
		serverName := mcpServerFromToolName(name)
		if serverFilter != "" && serverName != serverFilter {
			continue
		}
		if stop := add(toolSearchItem{
			Name:     tool.Spec.Name,
			Source:   "mcp",
			Server:   displayMCPServerName(serverName),
			CallTool: "mcp_call",
			CallName: tool.Spec.Name,
		}, tool.Spec); stop {
			return toolSearchResults{Items: items, Truncated: truncated}
		}
	}
	return toolSearchResults{Items: items, Truncated: truncated}
}

func toolSearchMatches(haystack, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	if strings.Contains(haystack, query) {
		return true
	}
	for _, term := range strings.Fields(query) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func (r *Registry) addMCPGateway() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp_list_tools",
			Description: "List connected MCP tools compactly. Use before mcp_call when Telegram, Telegram Parilka, GitHub, or Context7 tools are needed. For Russian парилка/Parilka chat requests, use server telegram-parilka.",
			Parameters:  raw(`{"type":"object","properties":{"server":{"type":"string","description":"Optional MCP server filter: telegram, telegram-parilka, github, or context7. telegram_parilka and парилка are also accepted."},"query":{"type":"string","description":"Optional case-insensitive name/description filter."},"limit":{"type":"integer","default":40},"include_schema":{"type":"boolean","default":false,"description":"Include a matching tool input schema only when needed to call it."}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Server        string `json:"server"`
				Query         string `json:"query"`
				Limit         int    `json:"limit"`
				IncludeSchema bool   `json:"include_schema"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 80 {
				in.Limit = 40
			}
			type item struct {
				Name        string          `json:"name"`
				Server      string          `json:"server,omitempty"`
				Description string          `json:"description,omitempty"`
				InputSchema json.RawMessage `json:"input_schema,omitempty"`
			}
			type serverItem struct {
				Name      string `json:"name"`
				Transport string `json:"transport,omitempty"`
				Enabled   bool   `json:"enabled"`
				Required  bool   `json:"required"`
				Connected bool   `json:"connected"`
				ToolCount int    `json:"tool_count,omitempty"`
				Error     string `json:"error,omitempty"`
			}
			serverFilter := normalizeMCPServerFilter(in.Server)
			query := strings.ToLower(strings.TrimSpace(in.Query))
			if serverFilter == "" && isParilkaAlias(query) {
				serverFilter = "telegram_parilka"
				query = ""
			}
			names := make([]string, 0, len(r.mcpTools))
			for name := range r.mcpTools {
				names = append(names, name)
			}
			sort.Strings(names)
			var tools []item
			truncated := false
			for _, name := range names {
				tool := r.mcpTools[name]
				server := mcpServerFromToolName(name)
				if serverFilter != "" && server != serverFilter {
					continue
				}
				haystack := strings.ToLower(name + " " + tool.Spec.Description)
				if query != "" && !strings.Contains(haystack, query) {
					continue
				}
				if len(tools) >= in.Limit {
					truncated = true
					break
				}
				out := item{
					Name:        name,
					Server:      displayMCPServerName(server),
					Description: truncate(oneLine(tool.Spec.Description), 240),
				}
				if in.IncludeSchema {
					out.InputSchema = tool.Spec.Parameters
				}
				tools = append(tools, out)
			}
			statuses := r.MCPStatuses()
			servers := make([]serverItem, 0, len(statuses))
			for _, status := range statuses {
				normalized := normalizeMCPServerFilter(status.Name)
				if serverFilter != "" && normalized != serverFilter {
					continue
				}
				servers = append(servers, serverItem{
					Name:      displayMCPServerName(normalized),
					Transport: status.Transport,
					Enabled:   status.Enabled,
					Required:  status.Required,
					Connected: status.Connected,
					ToolCount: status.ToolCount,
					Error:     status.Error,
				})
			}
			out, _ := json.MarshalIndent(map[string]any{
				"tools":     tools,
				"servers":   servers,
				"truncated": truncated,
			}, "", "  ")
			return Result{Content: string(out)}, nil
		},
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp_call",
			Description: "Call a connected MCP tool by full name after inspecting it with mcp_list_tools. For Parilka chat requests, call a tool named like mcp__telegram_parilka__read_history or mcp__telegram_parilka__search_messages.",
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
			tool, ok := r.mcpTools[name]
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

func (r *Registry) requireDangerous() error {
	if r.cfg.AutoApproveDangerous {
		return nil
	}
	return fmt.Errorf("tool disabled; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=true or unset FAST_AGENT_AUTO_APPROVE_DANGEROUS to enable write/execute tools")
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
	for _, root := range r.cfg.WorkspaceRoots {
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
	if len(r.cfg.WorkspaceRoots) > 0 && r.cfg.WorkspaceRoots[0] != "" {
		return r.cfg.WorkspaceRoots[0]
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

type fetchedPage struct {
	URL         string   `json:"url"`
	Status      int      `json:"status"`
	ContentType string   `json:"content_type"`
	Title       string   `json:"title,omitempty"`
	Text        string   `json:"text"`
	Links       []string `json:"links,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
}

type searchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type crawlItem struct {
	URL   string `json:"url"`
	Depth int    `json:"depth"`
}

type crawlPage struct {
	URL   string `json:"url"`
	Depth int    `json:"depth"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

type webFetchOptions struct {
	MaxChars       int
	MaxTokens      int
	MaxTotalTokens int
	FullText       bool
	MaxLinks       int
}

type compactPage struct {
	URL                 string   `json:"url"`
	Status              int      `json:"status,omitempty"`
	ContentType         string   `json:"content_type,omitempty"`
	Title               string   `json:"title,omitempty"`
	Summary             string   `json:"summary,omitempty"`
	Text                string   `json:"text,omitempty"`
	Links               []string `json:"links,omitempty"`
	Truncated           bool     `json:"truncated,omitempty"`
	OriginalTextChars   int      `json:"original_text_chars,omitempty"`
	ReturnedTextChars   int      `json:"returned_text_chars,omitempty"`
	EstimatedTextTokens int      `json:"estimated_text_tokens,omitempty"`
	BudgetTextChars     int      `json:"budget_text_chars,omitempty"`
	BudgetTextTokens    int      `json:"budget_text_tokens,omitempty"`
	OutputTextTruncated bool     `json:"output_text_truncated,omitempty"`
	CompactNote         string   `json:"compact_note,omitempty"`
}

type compactCrawlPage struct {
	URL                 string `json:"url"`
	Depth               int    `json:"depth"`
	Title               string `json:"title,omitempty"`
	Summary             string `json:"summary,omitempty"`
	Text                string `json:"text,omitempty"`
	Error               string `json:"error,omitempty"`
	OriginalTextChars   int    `json:"original_text_chars,omitempty"`
	ReturnedTextChars   int    `json:"returned_text_chars,omitempty"`
	EstimatedTextTokens int    `json:"estimated_text_tokens,omitempty"`
	BudgetTextChars     int    `json:"budget_text_chars,omitempty"`
	BudgetTextTokens    int    `json:"budget_text_tokens,omitempty"`
	OutputTextTruncated bool   `json:"output_text_truncated,omitempty"`
	CompactNote         string `json:"compact_note,omitempty"`
}

func compactFetchedPage(page fetchedPage, opts webFetchOptions) compactPage {
	maxChars, budgetTokens := webTextBudget(opts, webTextChars, webDefaultTextTokens, webMaxTextTokens)
	text, outputTruncated := compactWebText(page.Text, maxChars, opts.FullText)
	maxLinks := opts.MaxLinks
	if maxLinks <= 0 || maxLinks > 50 {
		maxLinks = webMaxLinks
	}
	links := page.Links
	if len(links) > maxLinks {
		links = links[:maxLinks]
	}
	return compactPage{
		URL:                 page.URL,
		Status:              page.Status,
		ContentType:         page.ContentType,
		Title:               page.Title,
		Summary:             summarizeText(page.Title, page.Text, 900),
		Text:                text,
		Links:               links,
		Truncated:           page.Truncated,
		OriginalTextChars:   len([]rune(page.Text)),
		ReturnedTextChars:   len([]rune(text)),
		EstimatedTextTokens: estimateTokens(text),
		BudgetTextChars:     maxChars,
		BudgetTextTokens:    budgetTokens,
		OutputTextTruncated: outputTruncated || len(page.Links) > len(links),
		CompactNote:         webCompactNote(outputTruncated || len(page.Links) > len(links), opts.FullText),
	}
}

func compactCrawlPages(pages []crawlPage, opts webFetchOptions) []compactCrawlPage {
	maxChars, budgetTokens := webTextBudget(opts, webCrawlChars, webCrawlDefaultTokens, webMaxTextTokens)
	totalTextChars, totalTokens := webTotalTextBudget(opts.MaxTotalTokens, len(pages))
	if totalTextChars > 0 && len(pages) > 0 {
		perPageTotalCap := max(800, totalTextChars/len(pages))
		if maxChars > perPageTotalCap {
			maxChars = perPageTotalCap
			budgetTokens = estimateTokensByChars(maxChars)
		}
	}
	out := make([]compactCrawlPage, 0, len(pages))
	for _, page := range pages {
		text, outputTruncated := compactWebText(page.Text, maxChars, opts.FullText)
		out = append(out, compactCrawlPage{
			URL:                 page.URL,
			Depth:               page.Depth,
			Title:               page.Title,
			Summary:             summarizeText(page.Title, page.Text, 700),
			Text:                text,
			Error:               page.Error,
			OriginalTextChars:   len([]rune(page.Text)),
			ReturnedTextChars:   len([]rune(text)),
			EstimatedTextTokens: estimateTokens(text),
			BudgetTextChars:     maxChars,
			BudgetTextTokens:    min(budgetTokens, totalTokens),
			OutputTextTruncated: outputTruncated,
			CompactNote:         webCompactNote(outputTruncated, opts.FullText),
		})
	}
	return out
}

func webTextBudget(opts webFetchOptions, fallbackChars, fallbackTokens, maxTokens int) (int, int) {
	fallback := fallbackChars
	if opts.FullText && opts.MaxChars <= 0 && opts.MaxTokens <= 0 {
		fallback = webFullTextChars
	}
	charBudget := normalizedWebChars(opts.MaxChars, fallback)
	tokenBudget := normalizedWebTokens(opts.MaxTokens, fallbackTokens, maxTokens)
	tokenChars := tokenBudget * 4
	if tokenChars > 0 && tokenChars < charBudget {
		charBudget = tokenChars
	}
	if charBudget > webHardTextChars {
		charBudget = webHardTextChars
	}
	return charBudget, estimateTokensByChars(charBudget)
}

func webTotalTextBudget(maxTotalTokens, pageCount int) (int, int) {
	if pageCount <= 0 {
		return 0, 0
	}
	tokens := normalizedWebTokens(maxTotalTokens, webCrawlDefaultTotalToks, webCrawlMaxTotalToks)
	return tokens * 4, tokens
}

func normalizedWebChars(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value < 800 {
		value = 800
	}
	if value > webHardTextChars {
		value = webHardTextChars
	}
	return value
}

func normalizedWebTokens(value, fallback, maxTokens int) int {
	if value <= 0 {
		value = fallback
	}
	if value < 200 {
		value = 200
	}
	if value > maxTokens {
		value = maxTokens
	}
	return value
}

func compactWebText(text string, maxChars int, fullText bool) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return truncateRunesWithMarker(text, maxChars)
}

func webCompactNote(truncated, fullText bool) string {
	if !truncated {
		return ""
	}
	if fullText {
		return "full_text was requested, but output is still capped by max_tokens/max_chars to protect context cost"
	}
	return "text is capped; increase max_tokens/max_chars only if exact source text is required"
}

func webPageMetadata(page compactPage) map[string]any {
	return map[string]any{
		"original_text_chars":     page.OriginalTextChars,
		"returned_text_chars":     page.ReturnedTextChars,
		"estimated_text_tokens":   page.EstimatedTextTokens,
		"budget_text_chars":       page.BudgetTextChars,
		"budget_text_tokens":      page.BudgetTextTokens,
		"output_text_truncated":   page.OutputTextTruncated,
		"response_body_truncated": page.Truncated,
	}
}

func estimateTokens(text string) int {
	return estimateTokensByChars(len([]rune(text)))
}

func estimateTokensByChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func summarizeText(title, text string, maxChars int) string {
	text = oneLine(text)
	if text == "" {
		return strings.TrimSpace(title)
	}
	var parts []string
	if title = strings.TrimSpace(title); title != "" {
		parts = append(parts, title)
	}
	sentences := splitSentences(text)
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		candidate := strings.Join(append(append([]string{}, parts...), sentence), " — ")
		if len([]rune(candidate)) > maxChars {
			break
		}
		parts = append(parts, sentence)
		if len(parts) >= 4 {
			break
		}
	}
	if len(parts) == 0 {
		return truncate(text, maxChars)
	}
	return truncate(strings.Join(parts, " — "), maxChars)
}

func splitSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' && r != '。' && r != '…' {
			continue
		}
		if i+1 <= start {
			continue
		}
		out = append(out, strings.TrimSpace(text[start:i+len(string(r))]))
		start = i + len(string(r))
		if len(out) >= 8 {
			break
		}
	}
	if len(out) == 0 {
		return []string{truncate(text, 320)}
	}
	return out
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func truncateRunesWithMarker(text string, maxRunes int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text, false
	}
	if maxRunes < 32 {
		maxRunes = 32
	}
	return string(runes[:maxRunes]) + "\n...[truncated; call web_fetch with full_text=true only if exact full page text is required]", true
}

func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}

func mcpServerFromToolName(name string) string {
	name = strings.TrimPrefix(name, "mcp__")
	server, _, ok := strings.Cut(name, "__")
	if !ok {
		return ""
	}
	return server
}

func normalizeMCPServerFilter(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if isParilkaAlias(value) {
		return "telegram_parilka"
	}
	return sanitizeMCPServerName(value)
}

func displayMCPServerName(value string) string {
	if value == "telegram_parilka" {
		return "telegram-parilka"
	}
	return value
}

func isParilkaAlias(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "parilka") ||
		strings.Contains(value, "парил")
}

func sanitizeMCPServerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = mcpServerUnsafeRE.ReplaceAllString(value, "_")
	return strings.Trim(value, "_")
}

func searchDuckDuckGoLite(ctx context.Context, query string, limit int) ([]searchResult, error) {
	values := url.Values{"q": []string{query}}
	searchURL := "https://lite.duckduckgo.com/lite/?" + values.Encode()
	body, _, _, err := httpGet(ctx, searchURL, maxWebBytes)
	if err != nil {
		return nil, err
	}
	return parseSearchResults(searchURL, string(body), limit), nil
}

func fetchPage(ctx context.Context, rawURL string, maxBytes int) (fetchedPage, error) {
	body, finalURL, contentType, err := httpGet(ctx, rawURL, maxBytes+1)
	if err != nil {
		return fetchedPage{}, err
	}
	truncated := false
	if len(body) > maxBytes {
		truncated = true
		body = body[:maxBytes]
	}
	textBody := string(body)
	page := fetchedPage{
		URL:         finalURL,
		Status:      http.StatusOK,
		ContentType: contentType,
		Truncated:   truncated,
	}
	if isHTML(contentType, textBody) {
		page.Title = extractTitle(textBody)
		page.Text = truncate(cleanHTMLText(textBody), maxBytes)
		page.Links = extractLinks(finalURL, textBody, 50)
		return page, nil
	}
	if !isTextual(contentType) {
		return fetchedPage{}, fmt.Errorf("refusing non-text response content-type %q", contentType)
	}
	page.Text = truncate(textBody, maxBytes)
	return page, nil
}

func crawl(ctx context.Context, rawURL string, maxPages, maxDepth int, sameHost bool, maxBytesPerPage int) ([]crawlPage, error) {
	if maxPages <= 0 || maxPages > 10 {
		maxPages = 3
	}
	if maxDepth < 0 || maxDepth > 2 {
		maxDepth = 1
	}
	start, err := validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	startHost := strings.ToLower(start.Hostname())
	queue := []crawlItem{{URL: start.String(), Depth: 0}}
	seen := map[string]bool{}
	var pages []crawlPage
	for len(queue) > 0 && len(pages) < maxPages {
		item := queue[0]
		queue = queue[1:]
		if seen[item.URL] {
			continue
		}
		seen[item.URL] = true
		page, err := fetchPage(ctx, item.URL, maxBytesPerPage)
		out := crawlPage{URL: item.URL, Depth: item.Depth}
		if err != nil {
			out.Error = err.Error()
			pages = append(pages, out)
			continue
		}
		out.Title = page.Title
		out.Text = page.Text
		pages = append(pages, out)
		if item.Depth >= maxDepth {
			continue
		}
		for _, link := range page.Links {
			u, err := url.Parse(link)
			if err != nil {
				continue
			}
			if sameHost && strings.ToLower(u.Hostname()) != startHost {
				continue
			}
			if !seen[u.String()] {
				queue = append(queue, crawlItem{URL: u.String(), Depth: item.Depth + 1})
			}
		}
	}
	return pages, nil
}

func httpGet(ctx context.Context, rawURL string, maxBytes int) ([]byte, string, string, error) {
	u, err := validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return nil, "", "", err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			_, err := validatePublicHTTPURL(req.Context(), req.URL.String())
			return err
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", "fast-agent-harness-go/0.1 (+https://localhost)")
	req.Header.Set("Accept", "text/html,text/plain,application/json,application/xml;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(limited), 1000))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return nil, "", "", err
	}
	return body, resp.Request.URL.String(), resp.Header.Get("Content-Type"), nil
}

func validatePublicHTTPURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL host required")
	}
	if err := validatePublicHost(ctx, host); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePublicHost(ctx context.Context, host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("refusing localhost URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("refusing non-public IP %s", ip)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("host resolved to no addresses")
	}
	for _, addr := range addrs {
		if !isPublicIP(addr.IP) {
			return fmt.Errorf("refusing host %s resolved to non-public IP %s", host, addr.IP)
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

var (
	mcpServerUnsafeRE = regexp.MustCompile(`[^a-z0-9_]+`)
	anchorRE          = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	brRE              = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</h[1-6]>`)
	scriptRE          = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRE           = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	noscriptRE        = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	tagRE             = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRE           = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankRE           = regexp.MustCompile(`\n{3,}`)
	titleRE           = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func parseSearchResults(baseURL, body string, limit int) []searchResult {
	matches := anchorRE.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var out []searchResult
	for _, match := range matches {
		if len(out) >= limit {
			break
		}
		title := cleanInlineText(match[2])
		link := normalizeLink(baseURL, html.UnescapeString(match[1]))
		link = unwrapDuckDuckGoURL(link)
		if title == "" || link == "" || seen[link] {
			continue
		}
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if strings.Contains(strings.ToLower(u.Hostname()), "duckduckgo.com") {
			continue
		}
		seen[link] = true
		out = append(out, searchResult{Title: title, URL: link})
	}
	return out
}

func normalizeLink(baseURL, rawLink string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(strings.TrimSpace(rawLink))
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func unwrapDuckDuckGoURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.ToLower(u.Hostname()), "duckduckgo.com") {
		if uddg := u.Query().Get("uddg"); uddg != "" {
			return uddg
		}
	}
	return rawURL
}

func extractLinks(baseURL, body string, limit int) []string {
	matches := anchorRE.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var out []string
	for _, match := range matches {
		if len(out) >= limit {
			break
		}
		link := normalizeLink(baseURL, html.UnescapeString(match[1]))
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		u.Fragment = ""
		link = u.String()
		if !seen[link] {
			seen[link] = true
			out = append(out, link)
		}
	}
	return out
}

func extractTitle(body string) string {
	match := titleRE.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return cleanInlineText(match[1])
}

func cleanHTMLText(body string) string {
	body = scriptRE.ReplaceAllString(body, " ")
	body = styleRE.ReplaceAllString(body, " ")
	body = noscriptRE.ReplaceAllString(body, " ")
	body = brRE.ReplaceAllString(body, "\n")
	body = tagRE.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)
	body = spaceRE.ReplaceAllString(body, " ")
	lines := strings.Split(body, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.TrimSpace(blankRE.ReplaceAllString(strings.Join(cleaned, "\n"), "\n\n"))
}

func cleanInlineText(body string) string {
	body = tagRE.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)
	body = spaceRE.ReplaceAllString(body, " ")
	return strings.TrimSpace(body)
}

func isHTML(contentType, body string) bool {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		return true
	}
	return strings.Contains(strings.ToLower(body[:min(len(body), 512)]), "<html")
}

func isTextual(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "csv")
}

func boundedBytes(n int) int {
	if n <= 0 {
		return defaultWebBytes
	}
	if n > maxWebBytes {
		return maxWebBytes
	}
	return n
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
