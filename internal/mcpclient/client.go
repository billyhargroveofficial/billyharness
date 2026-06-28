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
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const (
	protocolVersion              = "2025-06-18"
	defaultMCPToolOutputBytes    = 64 * 1024
	mcpReadBufferBytes           = 32 * 1024
	mcpCallResponseOverheadBytes = 64 * 1024
	minMCPCallResponseBytes      = 256 * 1024
	maxMCPControlResponseBytes   = 4 * 1024 * 1024
)

type ExternalTool struct {
	Spec    protocol.ToolSpec
	Handler func(context.Context, json.RawMessage) (string, error)
}

type ServerStatus struct {
	Name        string     `json:"name"`
	Transport   string     `json:"transport"`
	Enabled     bool       `json:"enabled"`
	Required    bool       `json:"required"`
	Connected   bool       `json:"connected"`
	ToolCount   int        `json:"tool_count"`
	PID         int        `json:"pid,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
	StderrTail  string     `json:"stderr_tail,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type Manager struct {
	tools         []ExternalTool
	instructions  []string
	mu            sync.RWMutex
	clients       []*stdioClient
	statusClients []*stdioClient
	statuses      []ServerStatus
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
			manager.addStatus(status, nil)
			continue
		}
		if server.URL != "" {
			err := fmt.Errorf("MCP server %s uses streamable HTTP; billyharness currently supports stdio MCP only", server.Name)
			status.Error = err.Error()
			manager.addStatus(status, nil)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		if strings.TrimSpace(server.Command) == "" {
			err := fmt.Errorf("MCP server %s has no command", server.Name)
			status.Error = err.Error()
			manager.addStatus(status, nil)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		client, specs, serverInstructions, err := startStdio(ctx, cfg, server)
		if err != nil {
			status.Error = err.Error()
			manager.addStatus(status, nil)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		status.Connected = true
		status.ToolCount = len(specs)
		manager.addStatus(status, client)
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
	m.mu.RLock()
	statuses := append([]ServerStatus(nil), m.statuses...)
	clients := append([]*stdioClient(nil), m.statusClients...)
	m.mu.RUnlock()
	for i, client := range clients {
		if client != nil && i < len(statuses) {
			statuses[i].Connected = client.connected.Load()
			statuses[i].PID = client.pid()
			if !client.startedAt.IsZero() {
				startedAt := client.startedAt
				statuses[i].StartedAt = &startedAt
			}
			lastErr, lastErrAt := client.lastError()
			statuses[i].LastError = lastErr
			if !lastErrAt.IsZero() {
				statuses[i].LastErrorAt = &lastErrAt
			}
			if !statuses[i].Connected {
				if statuses[i].Error == "" {
					statuses[i].Error = statuses[i].LastError
				}
				statuses[i].StderrTail = client.stderrTail()
			}
		}
	}
	return statuses
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
	m.mu.Lock()
	clients := append([]*stdioClient(nil), m.clients...)
	m.clients = nil
	for i, client := range m.statusClients {
		if client != nil && i < len(m.statuses) {
			client.connected.Store(false)
			m.statuses[i].Connected = false
		}
	}
	m.mu.Unlock()
	for _, client := range clients {
		client.close()
	}
}

func (m *Manager) addStatus(status ServerStatus, client *stdioClient) {
	m.statuses = append(m.statuses, status)
	m.statusClients = append(m.statusClients, client)
}

func mcpTransport(server config.MCPServer) string {
	if server.URL != "" {
		return "streamable-http"
	}
	return "stdio"
}

type stdioClient struct {
	server      config.MCPServer
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	out         *bufio.Reader
	mu          sync.Mutex
	closeOnce   sync.Once
	nextID      int64
	stderr      *limitedBuffer
	connected   atomic.Bool
	outputLimit int
	startedAt   time.Time
	statusMu    sync.RWMutex
	lastErr     string
	lastErrAt   time.Time
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
		server:      server,
		cmd:         cmd,
		stdin:       stdin,
		out:         bufio.NewReaderSize(stdout, mcpReadBufferBytes),
		stderr:      stderrBuf,
		outputLimit: mcpToolOutputLimit(cfg.MaxToolOutputBytes),
		startedAt:   time.Now().UTC(),
	}
	client.connected.Store(true)
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
		resp, err := c.read(ctx, c.responseLimit(method))
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
		err := c.withStderr(ctx.Err())
		c.markError(err)
		c.close()
		return err
	case err := <-done:
		if err != nil {
			err = fmt.Errorf("MCP %s write: %w", c.server.Name, err)
			c.markError(err)
			c.close()
			return err
		}
		return nil
	}
}

func (c *stdioClient) read(ctx context.Context, limit int) (rpcResponse, error) {
	type readResult struct {
		line []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		line, err := c.readLine(limit)
		done <- readResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		err := c.withStderr(ctx.Err())
		c.markError(err)
		c.close()
		return rpcResponse{}, err
	case result := <-done:
		if result.err != nil {
			err := c.withStderr(result.err)
			c.markError(err)
			c.close()
			return rpcResponse{}, err
		}
		var resp rpcResponse
		if err := json.Unmarshal(bytes.TrimSpace(result.line), &resp); err != nil {
			err = fmt.Errorf("MCP %s sent invalid JSON-RPC: %w", c.server.Name, err)
			c.markError(err)
			c.close()
			return rpcResponse{}, err
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
	text := renderContent(out, c.outputLimit)
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
	c.connected.Store(false)
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
			_ = c.cmd.Process.Kill()
			_, _ = c.cmd.Process.Wait()
		}
	})
}

func (c *stdioClient) markError(err error) {
	if c == nil || err == nil {
		return
	}
	c.statusMu.Lock()
	c.lastErr = err.Error()
	c.lastErrAt = time.Now().UTC()
	c.statusMu.Unlock()
}

func (c *stdioClient) lastError() (string, time.Time) {
	if c == nil {
		return "", time.Time{}
	}
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.lastErr, c.lastErrAt
}

func (c *stdioClient) pid() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *stdioClient) stderrTail() string {
	if c == nil || c.stderr == nil {
		return ""
	}
	return secrets.Redact(strings.TrimSpace(c.stderr.String()), serverSecrets(c.server)...)
}

func (c *stdioClient) responseLimit(method string) int {
	if method != "tools/call" {
		return maxMCPControlResponseBytes
	}
	limit := c.outputLimit + mcpCallResponseOverheadBytes
	if limit < minMCPCallResponseBytes {
		return minMCPCallResponseBytes
	}
	return limit
}

func (c *stdioClient) readLine(limit int) ([]byte, error) {
	if limit <= 0 {
		limit = maxMCPControlResponseBytes
	}
	var line []byte
	for {
		chunk, err := c.out.ReadSlice('\n')
		if len(chunk) > 0 {
			if len(line)+len(chunk) > limit {
				keep := limit - len(line)
				if keep > 0 {
					line = append(line, chunk[:keep]...)
				}
				return line, responseTooLargeError{limit: limit}
			}
			line = append(line, chunk...)
		}
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

type responseTooLargeError struct {
	limit int
}

func (e responseTooLargeError) Error() string {
	return fmt.Sprintf("response exceeded %d bytes", e.limit)
}

func (c *stdioClient) withStderr(err error) error {
	text := strings.TrimSpace(c.stderr.String())
	if text == "" {
		return fmt.Errorf("MCP %s transport: %w", c.server.Name, err)
	}
	return fmt.Errorf("MCP %s transport: %w: %s", c.server.Name, err, secrets.Redact(text, serverSecrets(c.server)...))
}

func renderContent(out callToolResult, limit int) string {
	limit = mcpToolOutputLimit(limit)
	var b strings.Builder
	omitted := 0
	appendPart := func(text string) {
		if text == "" {
			return
		}
		if b.Len() > 0 {
			if omitted > 0 {
				omitted++
			} else {
				omitted += appendLimitedUTF8(&b, "\n", limit)
			}
		}
		if omitted > 0 {
			omitted += len(text)
			return
		}
		omitted += appendLimitedUTF8(&b, text, limit)
	}
	for _, item := range out.Content {
		if item["type"] == "text" {
			if text, ok := item["text"].(string); ok {
				appendPart(text)
				continue
			}
		}
		bytes, _ := json.Marshal(item)
		if len(bytes) > 0 {
			appendPart(string(bytes))
		}
	}
	if b.Len() == 0 && omitted == 0 && out.StructuredContent != nil {
		bytes, _ := json.MarshalIndent(out.StructuredContent, "", "  ")
		return truncateMCPOutput(string(bytes), limit)
	}
	return withMCPTruncationNote(b.String(), omitted)
}

func mcpToolOutputLimit(limit int) int {
	if limit <= 0 {
		return defaultMCPToolOutputBytes
	}
	return limit
}

func truncateMCPOutput(text string, limit int) string {
	limit = mcpToolOutputLimit(limit)
	if len(text) <= limit {
		return text
	}
	trimmed := trimUTF8Bytes(text, limit)
	return withMCPTruncationNote(trimmed, len(text)-len(trimmed))
}

func withMCPTruncationNote(text string, omitted int) string {
	if omitted <= 0 {
		return text
	}
	return text + fmt.Sprintf("\n...[truncated %d bytes from MCP tool output]", omitted)
}

func appendLimitedUTF8(b *strings.Builder, text string, limit int) int {
	if limit <= 0 {
		limit = defaultMCPToolOutputBytes
	}
	remaining := limit - b.Len()
	if remaining <= 0 {
		return len(text)
	}
	if len(text) <= remaining {
		b.WriteString(text)
		return 0
	}
	trimmed := trimUTF8Bytes(text, remaining)
	b.WriteString(trimmed)
	return len(text) - len(trimmed)
}

func trimUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	text = text[:maxBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
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
