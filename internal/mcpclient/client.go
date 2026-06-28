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
	maxMCPReconnectBackoff       = 5 * time.Second
)

const (
	mcpStateDisabled     = "disabled"
	mcpStateConnected    = "connected"
	mcpStateFailed       = "failed"
	mcpStateCrashed      = "crashed"
	mcpStateRestarting   = "restarting"
	mcpStateReconnected  = "reconnected"
	mcpStateDisconnected = "disconnected"
	mcpStateUnsupported  = "unsupported"
)

type ExternalTool struct {
	Spec    protocol.ToolSpec
	Handler func(context.Context, json.RawMessage) (string, error)
}

type ServerStatus struct {
	Name            string     `json:"name"`
	Transport       string     `json:"transport"`
	Command         string     `json:"command,omitempty"`
	URL             string     `json:"url,omitempty"`
	Enabled         bool       `json:"enabled"`
	Required        bool       `json:"required"`
	Connected       bool       `json:"connected"`
	State           string     `json:"state"`
	ToolCount       int        `json:"tool_count"`
	PID             int        `json:"pid,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	LastEventAt     *time.Time `json:"last_event_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	LastErrorAt     *time.Time `json:"last_error_at,omitempty"`
	StderrTail      string     `json:"stderr_tail,omitempty"`
	Error           string     `json:"error,omitempty"`
	RetryCount      int        `json:"retry_count"`
	RestartCount    int        `json:"restart_count"`
	RetryBackoffMS  int64      `json:"retry_backoff_ms,omitempty"`
	NextRetryAt     *time.Time `json:"next_retry_at,omitempty"`
}

type Manager struct {
	tools        []ExternalTool
	instructions []string
	mu           sync.RWMutex
	servers      []*managedServer
}

func NewManager(ctx context.Context, cfg config.Config) (*Manager, error) {
	manager := &Manager{}
	var errs []string
	seenTools := map[string]string{}
	for _, server := range cfg.MCPServers {
		runtime := newManagedServer(cfg, server)
		if !server.Enabled {
			manager.addServer(runtime)
			continue
		}
		if server.URL != "" {
			err := fmt.Errorf("MCP server %s uses streamable HTTP; billyharness currently supports stdio MCP only", server.Name)
			runtime.recordStaticErrorState(mcpStateUnsupported, err)
			manager.addServer(runtime)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		if strings.TrimSpace(server.Command) == "" {
			err := fmt.Errorf("MCP server %s has no command", server.Name)
			runtime.recordStaticError(err)
			manager.addServer(runtime)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		specs, serverInstructions, err := runtime.start(ctx, false)
		manager.addServer(runtime)
		if err != nil {
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
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
					return runtime.callTool(callCtx, originalName, args)
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
	servers := append([]*managedServer(nil), m.servers...)
	m.mu.RUnlock()
	statuses := make([]ServerStatus, 0, len(servers))
	for _, server := range servers {
		statuses = append(statuses, server.snapshot())
	}
	return statuses
}

func (m *Manager) Refresh(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.RLock()
	servers := append([]*managedServer(nil), m.servers...)
	m.mu.RUnlock()
	for _, server := range servers {
		if server != nil {
			_, _ = server.ensureConnected(ctx)
		}
	}
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
	servers := append([]*managedServer(nil), m.servers...)
	m.mu.Unlock()
	for _, server := range servers {
		if server != nil {
			server.close()
		}
	}
}

func (m *Manager) addServer(server *managedServer) {
	m.servers = append(m.servers, server)
}

func mcpTransport(server config.MCPServer) string {
	if server.URL != "" {
		return "streamable-http"
	}
	return "stdio"
}

type managedServer struct {
	cfg           config.Config
	server        config.MCPServer
	reconnectable bool
	mu            sync.Mutex
	client        *stdioClient
	status        ServerStatus
	closed        bool
	starting      bool
}

func newManagedServer(cfg config.Config, server config.MCPServer) *managedServer {
	state := mcpStateDisconnected
	if !server.Enabled {
		state = mcpStateDisabled
	}
	return &managedServer{
		cfg:           cfg,
		server:        server,
		reconnectable: server.Enabled && server.URL == "" && strings.TrimSpace(server.Command) != "",
		status: ServerStatus{
			Name:      server.Name,
			Transport: mcpTransport(server),
			Command:   strings.TrimSpace(server.Command),
			URL:       strings.TrimSpace(server.URL),
			Enabled:   server.Enabled,
			Required:  server.Required,
			State:     state,
		},
	}
}

func (s *managedServer) start(ctx context.Context, reconnect bool) ([]protocol.ToolSpec, string, error) {
	s.mu.Lock()
	if !s.reconnectable {
		err := fmt.Errorf("MCP %s cannot reconnect with transport %s", s.server.Name, mcpTransport(s.server))
		s.recordFailureLocked(mcpStateFailed, err, false)
		s.mu.Unlock()
		return nil, "", err
	}
	if s.closed {
		err := fmt.Errorf("MCP %s manager is closed", s.server.Name)
		s.recordFailureLocked(mcpStateDisconnected, err, false)
		s.mu.Unlock()
		return nil, "", err
	}
	return s.startLocked(ctx, reconnect)
}

func (s *managedServer) ensureConnected(ctx context.Context) (*stdioClient, error) {
	s.mu.Lock()
	s.absorbClientLocked()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s manager is closed", s.server.Name)
	}
	if !s.status.Enabled {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s is disabled", s.server.Name)
	}
	if !s.reconnectable {
		if s.status.Error != "" {
			s.mu.Unlock()
			return nil, fmt.Errorf("MCP %s unavailable: %s", s.server.Name, s.status.Error)
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s unavailable", s.server.Name)
	}
	if s.client != nil && s.client.connected.Load() {
		client := s.client
		s.mu.Unlock()
		return client, nil
	}
	if s.starting {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s reconnect already in progress", s.server.Name)
	}
	now := time.Now().UTC()
	if s.status.NextRetryAt != nil && now.Before(*s.status.NextRetryAt) {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s reconnect backoff active until %s after last error: %s", s.server.Name, s.status.NextRetryAt.Format(time.RFC3339Nano), s.status.LastError)
	}
	_, _, err := s.startLocked(ctx, true)
	if err != nil {
		return nil, err
	}
	return s.client, nil
}

func (s *managedServer) startLocked(ctx context.Context, reconnect bool) ([]protocol.ToolSpec, string, error) {
	if s.starting {
		s.mu.Unlock()
		return nil, "", fmt.Errorf("MCP %s reconnect already in progress", s.server.Name)
	}
	oldClient := s.client
	s.client = nil
	s.starting = true
	now := time.Now().UTC()
	s.status.Connected = false
	s.status.State = mcpStateRestarting
	s.status.LastEventAt = timePtr(now)
	s.status.Error = ""
	s.status.StderrTail = ""
	s.status.PID = 0
	s.status.NextRetryAt = nil
	s.status.RetryBackoffMS = 0
	if reconnect {
		s.status.RetryCount++
	}
	s.mu.Unlock()

	if oldClient != nil {
		oldClient.close()
	}
	client, specs, instructions, err := startStdio(ctx, s.cfg, s.server)
	s.mu.Lock()
	s.starting = false
	if s.closed {
		closedErr := fmt.Errorf("MCP %s manager is closed", s.server.Name)
		s.recordFailureLocked(mcpStateDisconnected, closedErr, false)
		s.mu.Unlock()
		if client != nil {
			client.close()
		}
		return nil, "", closedErr
	}
	if err != nil {
		s.recordFailureLocked(mcpStateFailed, err, reconnect)
		s.mu.Unlock()
		return nil, "", err
	}
	s.client = client
	now = time.Now().UTC()
	state := mcpStateConnected
	if reconnect {
		state = mcpStateReconnected
		s.status.RestartCount++
	}
	s.status.Connected = true
	s.status.State = state
	s.status.ToolCount = len(specs)
	s.status.PID = client.pid()
	if !client.startedAt.IsZero() {
		startedAt := client.startedAt
		s.status.StartedAt = &startedAt
	}
	s.status.LastConnectedAt = timePtr(now)
	s.status.LastEventAt = timePtr(now)
	s.status.Error = ""
	s.status.StderrTail = ""
	s.status.NextRetryAt = nil
	s.status.RetryBackoffMS = 0
	s.mu.Unlock()
	return specs, instructions, nil
}

func (s *managedServer) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	client, err := s.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	text, err := client.callTool(ctx, name, args)
	if err != nil && !client.connected.Load() {
		s.mu.Lock()
		s.absorbClientLocked()
		s.mu.Unlock()
	}
	return text, err
}

func (s *managedServer) snapshot() ServerStatus {
	if s == nil {
		return ServerStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.absorbClientLocked()
	return s.status
}

func (s *managedServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	client := s.client
	s.client = nil
	now := time.Now().UTC()
	s.status.Connected = false
	s.status.State = mcpStateDisconnected
	s.status.LastEventAt = timePtr(now)
	s.status.Error = ""
	s.status.NextRetryAt = nil
	s.status.RetryBackoffMS = 0
	s.mu.Unlock()
	if client != nil {
		client.close()
	}
}

func (s *managedServer) recordStaticError(err error) {
	s.recordStaticErrorState(mcpStateFailed, err)
}

func (s *managedServer) recordStaticErrorState(state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordFailureLocked(state, err, false)
}

func (s *managedServer) recordFailureLocked(state string, err error, retryable bool) {
	now := time.Now().UTC()
	message := redactServerError(s.server, err)
	s.status.Connected = false
	s.status.State = state
	s.status.Error = message
	s.status.LastError = message
	s.status.LastErrorAt = timePtr(now)
	s.status.LastEventAt = timePtr(now)
	s.status.StderrTail = ""
	if s.client != nil {
		s.status.StderrTail = s.client.stderrTail()
	}
	if retryable {
		backoff := mcpReconnectBackoff(s.status.RetryCount)
		next := now.Add(backoff)
		s.status.RetryBackoffMS = backoff.Milliseconds()
		s.status.NextRetryAt = &next
	}
}

func (s *managedServer) absorbClientLocked() {
	if s.client == nil {
		return
	}
	client := s.client
	if client.connected.Load() {
		s.status.Connected = true
		s.status.PID = client.pid()
		if !client.startedAt.IsZero() {
			startedAt := client.startedAt
			s.status.StartedAt = &startedAt
		}
		return
	}
	state, lastErr, lastErrAt := client.lifecycleState()
	if state == "" {
		state = mcpStateCrashed
	}
	s.status.Connected = false
	s.status.State = state
	if lastErr != "" {
		s.status.LastError = lastErr
		s.status.Error = lastErr
	}
	if !lastErrAt.IsZero() {
		s.status.LastErrorAt = &lastErrAt
		s.status.LastEventAt = &lastErrAt
	}
	s.status.PID = client.pid()
	s.status.StderrTail = client.stderrTail()
}

func mcpReconnectBackoff(retryCount int) time.Duration {
	if retryCount <= 1 {
		return time.Second
	}
	backoff := time.Second
	for i := 1; i < retryCount; i++ {
		backoff *= 2
		if backoff >= maxMCPReconnectBackoff {
			return maxMCPReconnectBackoff
		}
	}
	return backoff
}

func redactServerError(server config.MCPServer, err error) string {
	if err == nil {
		return ""
	}
	return secrets.Redact(err.Error(), serverSecrets(server)...)
}

func timePtr(t time.Time) *time.Time {
	return &t
}

type stdioClient struct {
	server      config.MCPServer
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	out         *bufio.Reader
	mu          sync.Mutex
	closeOnce   sync.Once
	closing     atomic.Bool
	waitCh      chan error
	nextID      int64
	stderr      *limitedBuffer
	connected   atomic.Bool
	outputLimit int
	startedAt   time.Time
	statusMu    sync.RWMutex
	state       string
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
		waitCh:      make(chan error, 1),
		outputLimit: mcpToolOutputLimit(cfg.MaxToolOutputBytes),
		startedAt:   time.Now().UTC(),
	}
	client.connected.Store(true)
	client.markLifecycle(mcpStateConnected, nil)
	go client.watchProcess()
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
		c.markLifecycle(mcpStateFailed, err)
		c.close()
		return err
	case err := <-done:
		if err != nil {
			err = fmt.Errorf("MCP %s write: %w", c.server.Name, err)
			c.markLifecycle(mcpStateCrashed, err)
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
		c.markLifecycle(mcpStateFailed, err)
		c.close()
		return rpcResponse{}, err
	case result := <-done:
		if result.err != nil {
			err := c.withStderr(result.err)
			state := mcpStateCrashed
			var tooLarge responseTooLargeError
			if errors.As(result.err, &tooLarge) {
				state = mcpStateFailed
			}
			c.markLifecycle(state, err)
			c.close()
			return rpcResponse{}, err
		}
		var resp rpcResponse
		if err := json.Unmarshal(bytes.TrimSpace(result.line), &resp); err != nil {
			err = fmt.Errorf("MCP %s sent invalid JSON-RPC: %w", c.server.Name, err)
			c.markLifecycle(mcpStateFailed, err)
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
	c.closing.Store(true)
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
			_ = c.cmd.Process.Kill()
		}
		if c.waitCh != nil {
			select {
			case <-c.waitCh:
			case <-time.After(2 * time.Second):
			}
		}
	})
}

func (c *stdioClient) watchProcess() {
	err := c.cmd.Wait()
	c.connected.Store(false)
	if !c.closing.Load() {
		if err != nil {
			c.markLifecycle(mcpStateCrashed, fmt.Errorf("MCP %s process exited: %w", c.server.Name, err))
		} else {
			c.markLifecycle(mcpStateCrashed, fmt.Errorf("MCP %s process exited", c.server.Name))
		}
	}
	c.waitCh <- err
}

func (c *stdioClient) markLifecycle(state string, err error) {
	if c == nil {
		return
	}
	c.statusMu.Lock()
	if state != "" {
		c.state = state
	}
	if err != nil {
		message := redactServerError(c.server, err)
		if c.lastErr == "" || preferLifecycleError(c.lastErr, message) {
			c.lastErr = message
			c.lastErrAt = time.Now().UTC()
		}
	}
	c.statusMu.Unlock()
}

func preferLifecycleError(existing, next string) bool {
	if next == "" {
		return false
	}
	if strings.Contains(next, "transport") && strings.Contains(existing, "process exited") {
		return true
	}
	if strings.Contains(next, "deadline exceeded") && !strings.Contains(existing, "deadline exceeded") {
		return true
	}
	if strings.Contains(next, "response exceeded") && !strings.Contains(existing, "response exceeded") {
		return true
	}
	return false
}

func (c *stdioClient) lifecycleState() (string, string, time.Time) {
	if c == nil {
		return "", "", time.Time{}
	}
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.state, c.lastErr, c.lastErrAt
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
