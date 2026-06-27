package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const protocolVersion = "2025-06-18"

type ExternalTool struct {
	Spec    protocol.ToolSpec
	Handler func(context.Context, json.RawMessage) (string, error)
}

type ServerStatus struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Enabled   bool   `json:"enabled"`
	Required  bool   `json:"required"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	Error     string `json:"error,omitempty"`
}

type Manager struct {
	tools        []ExternalTool
	instructions []string
	clients      []*stdioClient
	statuses     []ServerStatus
}

func NewManager(ctx context.Context, cfg config.Config) (*Manager, error) {
	manager := &Manager{}
	var errs []string
	seenTools := map[string]string{}
	for _, server := range cfg.MCPServers {
		status := ServerStatus{
			Name:      server.Name,
			Transport: mcpTransport(server),
			Enabled:   server.Enabled,
			Required:  server.Required,
		}
		if !server.Enabled {
			manager.statuses = append(manager.statuses, status)
			continue
		}
		if server.URL != "" {
			err := fmt.Errorf("MCP server %s uses streamable HTTP; billyharness currently supports stdio MCP only", server.Name)
			status.Error = err.Error()
			manager.statuses = append(manager.statuses, status)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		if strings.TrimSpace(server.Command) == "" {
			err := fmt.Errorf("MCP server %s has no command", server.Name)
			status.Error = err.Error()
			manager.statuses = append(manager.statuses, status)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		client, specs, serverInstructions, err := startStdio(ctx, cfg, server)
		if err != nil {
			status.Error = err.Error()
			manager.statuses = append(manager.statuses, status)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		status.Connected = true
		status.ToolCount = len(specs)
		manager.statuses = append(manager.statuses, status)
		manager.clients = append(manager.clients, client)
		if strings.TrimSpace(serverInstructions) != "" {
			manager.instructions = append(manager.instructions, fmt.Sprintf("%s: %s", server.Name, truncateText(serverInstructions, 512)))
		}
		for _, spec := range specs {
			originalName := spec.Name
			externalName := toolName(server.Name, originalName)
			if externalName == "" {
				continue
			}
			if prev := seenTools[externalName]; prev != "" {
				errs = append(errs, fmt.Sprintf("MCP tool name collision: %s maps both %s and %s/%s", externalName, prev, server.Name, originalName))
				continue
			}
			seenTools[externalName] = server.Name + "/" + originalName
			spec.Name = externalName
			spec.Description = strings.TrimSpace(fmt.Sprintf("MCP %s/%s. %s", server.Name, originalName, spec.Description))
			spec.Risk = protocol.RiskExternal
			toolTimeout := server.ToolTimeout
			if toolTimeout <= 0 {
				toolTimeout = 300 * time.Second
			}
			manager.tools = append(manager.tools, ExternalTool{
				Spec: spec,
				Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
					callCtx, cancel := context.WithTimeout(ctx, toolTimeout)
					defer cancel()
					return client.callTool(callCtx, originalName, args)
				},
			})
		}
	}
	sort.Slice(manager.tools, func(i, j int) bool {
		return manager.tools[i].Spec.Name < manager.tools[j].Spec.Name
	})
	if len(errs) > 0 {
		manager.Close()
		return nil, fmt.Errorf("MCP initialization failed: %s", strings.Join(errs, "; "))
	}
	return manager, nil
}

func (m *Manager) Tools() []ExternalTool {
	if m == nil {
		return nil
	}
	return append([]ExternalTool(nil), m.tools...)
}

func (m *Manager) Statuses() []ServerStatus {
	if m == nil {
		return nil
	}
	return append([]ServerStatus(nil), m.statuses...)
}

func (m *Manager) Instructions() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.instructions...)
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, client := range m.clients {
		client.close()
	}
	m.clients = nil
}

func mcpTransport(server config.MCPServer) string {
	if server.URL != "" {
		return "streamable-http"
	}
	return "stdio"
}

type stdioClient struct {
	server config.MCPServer
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	mu     sync.Mutex
	nextID int64
	stderr *limitedBuffer
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type listToolsResult struct {
	Tools []mcpTool `json:"tools"`
}

type initializeResult struct {
	Instructions string `json:"instructions"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type callToolResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError"`
}

func startStdio(parent context.Context, cfg config.Config, server config.MCPServer) (*stdioClient, []protocol.ToolSpec, string, error) {
	timeout := server.StartupTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if disallowedShell(server.Command) {
		return nil, nil, "", fmt.Errorf("MCP %s command %q is not allowed; use direct argv commands, not shells", server.Name, server.Command)
	}
	cwd, err := mcpCWD(cfg, server)
	if err != nil {
		return nil, nil, "", fmt.Errorf("MCP %s cwd: %w", server.Name, err)
	}

	cmd := exec.Command(server.Command, server.Args...)
	cmd.Dir = cwd
	cmd.Env = mcpEnv(server)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, "", fmt.Errorf("MCP %s stdin: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, "", fmt.Errorf("MCP %s stdout: %w", server.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, "", fmt.Errorf("MCP %s stderr: %w", server.Name, err)
	}
	stderrBuf := &limitedBuffer{limit: 8192}
	go func() {
		_, _ = io.Copy(stderrBuf, stderr)
	}()
	if err := cmd.Start(); err != nil {
		return nil, nil, "", fmt.Errorf("MCP %s start: %w", server.Name, err)
	}
	client := &stdioClient{
		server: server,
		cmd:    cmd,
		stdin:  stdin,
		out:    bufio.NewReaderSize(stdout, 1024*1024),
		stderr: stderrBuf,
	}
	initResult, err := client.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "billyharness",
			"version": "0.1.0",
		},
	})
	if err != nil {
		client.close()
		return nil, nil, "", err
	}
	var init initializeResult
	_ = json.Unmarshal(initResult, &init)
	if err := client.notify(ctx, "notifications/initialized", nil); err != nil {
		client.close()
		return nil, nil, "", err
	}
	result, err := client.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		client.close()
		return nil, nil, "", err
	}
	var listed listToolsResult
	if err := json.Unmarshal(result, &listed); err != nil {
		client.close()
		return nil, nil, "", fmt.Errorf("MCP %s tools/list decode: %w", server.Name, err)
	}
	specs := make([]protocol.ToolSpec, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		if !toolAllowed(server, tool.Name) {
			continue
		}
		schema := tool.InputSchema
		if len(schema) == 0 || !json.Valid(schema) {
			schema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
		}
		specs = append(specs, protocol.ToolSpec{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
			Risk:        protocol.RiskExternal,
		})
	}
	return client, specs, init.Instructions, nil
}

func mcpCWD(cfg config.Config, server config.MCPServer) (string, error) {
	cwd := server.CWD
	if cwd == "" {
		if len(cfg.WorkspaceRoots) > 0 && cfg.WorkspaceRoots[0] != "" {
			cwd = cfg.WorkspaceRoots[0]
		} else {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
	} else if !filepath.IsAbs(cwd) {
		base := "."
		if len(cfg.WorkspaceRoots) > 0 && cfg.WorkspaceRoots[0] != "" {
			base = cfg.WorkspaceRoots[0]
		}
		cwd = filepath.Join(base, cwd)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	policy, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	policy, _ = filepath.Abs(policy)
	for _, root := range cfg.WorkspaceRoots {
		if root == "" {
			continue
		}
		absRoot, _ := filepath.Abs(root)
		policyRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			policyRoot = absRoot
		}
		policyRoot, _ = filepath.Abs(policyRoot)
		if policy == policyRoot || strings.HasPrefix(policy, policyRoot+string(os.PathSeparator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("outside workspace roots: %s", abs)
}

func (c *stdioClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.write(ctx, req); err != nil {
		return nil, err
	}
	for {
		resp, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if resp.Method != "" && len(resp.ID) > 0 && string(resp.ID) != fmt.Sprintf("%d", id) {
			_ = c.write(ctx, map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(resp.ID),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
			continue
		}
		if len(resp.ID) == 0 || string(resp.ID) != fmt.Sprintf("%d", id) {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP %s %s failed: %s", c.server.Name, method, secrets.Redact(resp.Error.Message, serverSecrets(c.server)...))
		}
		return resp.Result, nil
	}
}

func (c *stdioClient) notify(ctx context.Context, method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(ctx, req)
}

func (c *stdioClient) write(ctx context.Context, value any) error {
	done := make(chan error, 1)
	go func() {
		bytes, err := json.Marshal(value)
		if err == nil {
			bytes = append(bytes, '\n')
			_, err = c.stdin.Write(bytes)
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("MCP %s write: %w", c.server.Name, err)
		}
		return nil
	}
}

func (c *stdioClient) read(ctx context.Context) (rpcResponse, error) {
	type readResult struct {
		line []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		line, err := c.out.ReadBytes('\n')
		done <- readResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		go c.close()
		return rpcResponse{}, c.withStderr(ctx.Err())
	case result := <-done:
		if result.err != nil {
			return rpcResponse{}, c.withStderr(result.err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(bytes.TrimSpace(result.line), &resp); err != nil {
			return rpcResponse{}, fmt.Errorf("MCP %s sent invalid JSON-RPC: %w", c.server.Name, err)
		}
		return resp, nil
	}
}

func (c *stdioClient) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	result, err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": json.RawMessage(args),
	})
	if err != nil {
		return "", err
	}
	var out callToolResult
	if err := json.Unmarshal(result, &out); err != nil {
		return "", fmt.Errorf("MCP %s tools/call decode: %w", c.server.Name, err)
	}
	text := renderContent(out)
	if out.IsError {
		if text == "" {
			text = "MCP tool returned isError=true"
		}
		text = secrets.Redact(text, serverSecrets(c.server)...)
		return text, errors.New(text)
	}
	return secrets.Redact(text, serverSecrets(c.server)...), nil
}

func (c *stdioClient) close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
}

func (c *stdioClient) withStderr(err error) error {
	text := strings.TrimSpace(c.stderr.String())
	if text == "" {
		return fmt.Errorf("MCP %s transport: %w", c.server.Name, err)
	}
	return fmt.Errorf("MCP %s transport: %w: %s", c.server.Name, err, secrets.Redact(text, serverSecrets(c.server)...))
}

func renderContent(out callToolResult) string {
	var parts []string
	for _, item := range out.Content {
		if item["type"] == "text" {
			if text, ok := item["text"].(string); ok {
				parts = append(parts, text)
				continue
			}
		}
		bytes, _ := json.Marshal(item)
		if len(bytes) > 0 {
			parts = append(parts, string(bytes))
		}
	}
	if len(parts) == 0 && out.StructuredContent != nil {
		bytes, _ := json.MarshalIndent(out.StructuredContent, "", "  ")
		return string(bytes)
	}
	return strings.Join(parts, "\n")
}

func mcpEnv(server config.MCPServer) []string {
	env := make([]string, 0, len(defaultEnvVars)+len(server.EnvVars)+len(server.Env))
	for _, name := range defaultEnvVars {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	for _, name := range server.EnvVars {
		if value, ok := config.LookupEnvOrDotenv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	keys := make([]string, 0, len(server.Env))
	for key := range server.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+server.Env[key])
	}
	return env
}

func serverSecrets(server config.MCPServer) []string {
	var values []string
	for key, value := range server.Env {
		if value == "" || len(value) < 8 {
			continue
		}
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "apikey") {
			values = append(values, value)
		}
	}
	for _, name := range server.EnvVars {
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "token") &&
			!strings.Contains(lower, "secret") &&
			!strings.Contains(lower, "password") &&
			!strings.Contains(lower, "api_key") &&
			!strings.Contains(lower, "apikey") {
			continue
		}
		if value, ok := config.LookupEnvOrDotenv(name); ok && len(value) >= 8 {
			values = append(values, value)
		}
	}
	return values
}

var defaultEnvVars = []string{
	"HOME",
	"LOGNAME",
	"PATH",
	"SHELL",
	"TMPDIR",
	"TEMP",
	"TMP",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"USER",
	"USERNAME",
	"APPDATA",
	"LOCALAPPDATA",
	"PROGRAMDATA",
	"SystemRoot",
	"COMSPEC",
}

func toolAllowed(server config.MCPServer, name string) bool {
	if len(server.EnabledTools) > 0 && !contains(server.EnabledTools, name) {
		return false
	}
	if contains(server.DisabledTools, name) {
		return false
	}
	return true
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

var unsafeToolChars = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func toolName(server, tool string) string {
	name := "mcp__" + sanitize(server) + "__" + sanitize(tool)
	name = strings.Trim(name, "_")
	if name == "mcp" || name == "" {
		return ""
	}
	return name
}

func sanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, ".", "_")
	value = unsafeToolChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "server"
	}
	return value
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + fmt.Sprintf("...[truncated %d bytes]", len(text)-limit)
}

func disallowedShell(command string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	switch base {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	default:
		return false
	}
}

type limitedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = b.buf[len(b.buf)-b.limit:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
