package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

type stdioClient struct {
	server         config.MCPServer
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	out            *bufio.Reader
	mu             sync.Mutex
	closeOnce      sync.Once
	closing        atomic.Bool
	waitCh         chan error
	nextID         int64
	stderr         *limitedBuffer
	connected      atomic.Bool
	outputLimit    int
	startedAt      time.Time
	statusMu       sync.RWMutex
	state          string
	lastErr        string
	lastErrAt      time.Time
	onNotification func(string, json.RawMessage)
}

type listToolsResult struct {
	Tools []mcpTool `json:"tools"`
}

type listPromptsResult struct {
	Prompts []mcpPrompt `json:"prompts"`
}

type initializeResult struct {
	Instructions string `json:"instructions"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type mcpPrompt struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Arguments   []mcpPromptArgument `json:"arguments"`
}

type mcpPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

func startStdio(parent context.Context, settings ManagerSettings, server config.MCPServer, onNotification func(string, json.RawMessage)) (*stdioClient, []protocol.ToolSpec, []Prompt, string, error) {
	timeout := server.StartupTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if disallowedShell(server.Command) {
		return nil, nil, nil, "", fmt.Errorf("MCP %s command %q is not allowed; use direct argv commands, not shells", server.Name, server.Command)
	}
	cwd, err := mcpCWD(settings, server)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("MCP %s cwd: %w", server.Name, err)
	}

	cmd := exec.Command(server.Command, server.Args...)
	cmd.Dir = cwd
	cmd.Env = mcpEnv(server)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("MCP %s stdin: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("MCP %s stdout: %w", server.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("MCP %s stderr: %w", server.Name, err)
	}
	stderrBuf := &limitedBuffer{limit: 8192}
	go func() {
		_, _ = io.Copy(stderrBuf, stderr)
	}()
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, "", fmt.Errorf("MCP %s start: %w", server.Name, err)
	}
	client := &stdioClient{
		server:         server,
		cmd:            cmd,
		stdin:          stdin,
		out:            bufio.NewReaderSize(stdout, mcpReadBufferBytes),
		stderr:         stderrBuf,
		waitCh:         make(chan error, 1),
		outputLimit:    mcpToolOutputLimit(settings.MaxToolOutputBytes),
		startedAt:      time.Now().UTC(),
		onNotification: onNotification,
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
		return nil, nil, nil, "", err
	}
	var init initializeResult
	_ = json.Unmarshal(initResult, &init)
	if err := client.notify(ctx, "notifications/initialized", nil); err != nil {
		client.close()
		return nil, nil, nil, "", err
	}
	specs, err := client.listTools(ctx)
	if err != nil {
		client.close()
		return nil, nil, nil, "", err
	}
	prompts, err := client.listPrompts(ctx)
	if err != nil && !client.connected.Load() {
		client.close()
		return nil, nil, nil, "", err
	}
	return client, specs, prompts, init.Instructions, nil
}

func (c *stdioClient) listTools(ctx context.Context) ([]protocol.ToolSpec, error) {
	result, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var listed listToolsResult
	if err := json.Unmarshal(result, &listed); err != nil {
		return nil, fmt.Errorf("MCP %s tools/list decode: %w", c.server.Name, err)
	}
	specs := make([]protocol.ToolSpec, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		if !toolAllowed(c.server, tool.Name) {
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
	return specs, nil
}

func (c *stdioClient) listPrompts(ctx context.Context) ([]Prompt, error) {
	result, err := c.request(ctx, "prompts/list", map[string]any{})
	if err != nil {
		if !c.connected.Load() {
			return nil, err
		}
		return nil, nil
	}
	var listed listPromptsResult
	if err := json.Unmarshal(result, &listed); err != nil {
		return nil, fmt.Errorf("MCP %s prompts/list decode: %w", c.server.Name, err)
	}
	prompts := make([]Prompt, 0, len(listed.Prompts))
	for _, prompt := range listed.Prompts {
		name := strings.TrimSpace(prompt.Name)
		if name == "" {
			continue
		}
		out := Prompt{
			Server:      c.server.Name,
			Name:        name,
			Description: strings.TrimSpace(prompt.Description),
			Arguments:   make([]PromptArgument, 0, len(prompt.Arguments)),
		}
		for _, arg := range prompt.Arguments {
			argName := strings.TrimSpace(arg.Name)
			if argName == "" {
				continue
			}
			out.Arguments = append(out.Arguments, PromptArgument{
				Name:        argName,
				Description: strings.TrimSpace(arg.Description),
				Required:    arg.Required,
			})
		}
		prompts = append(prompts, out)
	}
	return prompts, nil
}

func mcpCWD(settings ManagerSettings, server config.MCPServer) (string, error) {
	cwd := server.CWD
	if cwd == "" {
		if len(settings.WorkspaceRoots) > 0 && settings.WorkspaceRoots[0] != "" {
			cwd = settings.WorkspaceRoots[0]
		} else {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
	} else if !filepath.IsAbs(cwd) {
		base := "."
		if len(settings.WorkspaceRoots) > 0 && settings.WorkspaceRoots[0] != "" {
			base = settings.WorkspaceRoots[0]
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
	for _, root := range settings.WorkspaceRoots {
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
