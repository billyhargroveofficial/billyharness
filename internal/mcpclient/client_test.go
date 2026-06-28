package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestStdioLifecycleCallEnvAndRedaction(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MCP_ALLOWED_FROM_PARENT", "allowed-value")
	t.Setenv("DEEPSEEK_API_KEY", "sk-parent-secret-should-not-pass")
	cfg := config.Config{
		WorkspaceRoots:       []string{root},
		MaxToolOutputBytes:   64 * 1024,
		AutoApproveDangerous: true,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("normal", map[string]string{"MCP_LITERAL_SECRET": "literal-secret-value"}),
			EnvVars:        []string{"MCP_ALLOWED_FROM_PARENT"},
			StartupTimeout: 2 * time.Second,
			ToolTimeout:    2 * time.Second,
			Enabled:        true,
			EnabledTools:   []string{"echo", "env", "fail"},
		}},
	}
	manager, err := NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if got := strings.Join(manager.Instructions(), "\n"); !strings.Contains(got, "Use echo for smoke tests") {
		t.Fatalf("instructions = %#v", manager.Instructions())
	}
	statuses := manager.Statuses()
	if len(statuses) != 1 || statuses[0].Name != "fake" || !statuses[0].Connected || statuses[0].ToolCount != 3 {
		t.Fatalf("statuses = %#v", statuses)
	}

	echo := findTool(t, manager, "mcp__fake__echo")
	text, err := echo.Handler(context.Background(), json.RawMessage(`{"text":"hello mcp"}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello mcp" {
		t.Fatalf("echo = %q", text)
	}

	envTool := findTool(t, manager, "mcp__fake__env")
	envText, err := envTool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(envText, "allowed-value") || !strings.Contains(envText, "[redacted]") {
		t.Fatalf("expected allowlisted and redacted env values, got %q", envText)
	}
	if strings.Contains(envText, "sk-parent-secret") || strings.Contains(envText, "literal-secret-value") {
		t.Fatalf("env leaked secret: %q", envText)
	}

	fail := findTool(t, manager, "mcp__fake__fail")
	failText, err := fail.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected MCP tool error")
	}
	if strings.Contains(failText, "sk-test-secret") || strings.Contains(err.Error(), "sk-test-secret") {
		t.Fatalf("error leaked secret: text=%q err=%v", failText, err)
	}
}

func TestOptionalRequiredShellCWDAndCollisionRules(t *testing.T) {
	root := t.TempDir()
	t.Run("optional failure skipped", func(t *testing.T) {
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers:     []config.MCPServer{{Name: "missing", Command: filepath.Join(root, "does-not-exist"), Enabled: true}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		if len(manager.Tools()) != 0 {
			t.Fatalf("tools = %#v", manager.Tools())
		}
		statuses := manager.Statuses()
		if len(statuses) != 1 || statuses[0].Name != "missing" || statuses[0].Connected || statuses[0].Error == "" {
			t.Fatalf("statuses = %#v", statuses)
		}
		if statuses[0].Command == "" || statuses[0].Transport != "stdio" || statuses[0].State != mcpStateFailed {
			t.Fatalf("status command/transport/state = %#v", statuses[0])
		}
	})

	t.Run("unsupported url transport is visible", func(t *testing.T) {
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers:     []config.MCPServer{{Name: "remote", URL: "https://example.com/mcp", Enabled: true}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		statuses := manager.Statuses()
		if len(statuses) != 1 || statuses[0].Name != "remote" || statuses[0].State != mcpStateUnsupported ||
			statuses[0].Transport != "streamable-http" || statuses[0].URL != "https://example.com/mcp" ||
			!strings.Contains(statuses[0].UnsupportedReason, "streamable HTTP MCP is not implemented") ||
			!strings.Contains(statuses[0].Error, "unsupported") {
			t.Fatalf("unsupported status = %#v", statuses)
		}
	})

	t.Run("optional failure does not poison working servers", func(t *testing.T) {
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers: []config.MCPServer{
				{Name: "missing", Command: filepath.Join(root, "does-not-exist"), Enabled: true},
				{Name: "fake", Command: os.Args[0], Args: []string{"-test.run=TestFakeStdioMCPServer"}, Env: helperEnv("normal", nil), Enabled: true},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		echo := findTool(t, manager, "mcp__fake__echo")
		text, err := echo.Handler(context.Background(), json.RawMessage(`{"text":"still works"}`))
		if err != nil || text != "still works" {
			t.Fatalf("working MCP tool after optional failure = %q err=%v", text, err)
		}
		statuses := manager.Statuses()
		if len(statuses) != 2 || statuses[0].State != mcpStateFailed || statuses[1].State != mcpStateConnected {
			t.Fatalf("mixed statuses = %#v", statuses)
		}
	})

	t.Run("required failure errors", func(t *testing.T) {
		_, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers:     []config.MCPServer{{Name: "missing", Command: filepath.Join(root, "does-not-exist"), Enabled: true, Required: true}},
		})
		if err == nil || !strings.Contains(err.Error(), "MCP initialization failed") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("shell command denied", func(t *testing.T) {
		_, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers:     []config.MCPServer{{Name: "shell", Command: "sh", Enabled: true, Required: true}},
		})
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("cwd outside workspace denied", func(t *testing.T) {
		_, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers: []config.MCPServer{{
				Name:     "outside",
				Command:  os.Args[0],
				Args:     []string{"-test.run=TestFakeStdioMCPServer"},
				Env:      helperEnv("normal", nil),
				CWD:      t.TempDir(),
				Enabled:  true,
				Required: true,
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "outside workspace") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("sanitized tool collision denied", func(t *testing.T) {
		_, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots: []string{root},
			MCPServers: []config.MCPServer{
				{Name: "fake-one", Command: os.Args[0], Args: []string{"-test.run=TestFakeStdioMCPServer"}, Env: helperEnv("normal", nil), Enabled: true, EnabledTools: []string{"echo"}},
				{Name: "fake_one", Command: os.Args[0], Args: []string{"-test.run=TestFakeStdioMCPServer"}, Env: helperEnv("normal", nil), Enabled: true, EnabledTools: []string{"echo"}},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "collision") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestStdioLifecycleCleanupOnCollisionFailureAndClose(t *testing.T) {
	root := t.TempDir()

	t.Run("collision closes started clients", func(t *testing.T) {
		pidOne := filepath.Join(root, "collision-one.pid")
		pidTwo := filepath.Join(root, "collision-two.pid")
		_, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots:     []string{root},
			MaxToolOutputBytes: 64 * 1024,
			MCPServers: []config.MCPServer{
				{Name: "fake-one", Command: os.Args[0], Args: []string{"-test.run=TestFakeStdioMCPServer"}, Env: helperEnv("normal", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidOne}), CWD: root, Enabled: true, EnabledTools: []string{"echo"}},
				{Name: "fake_one", Command: os.Args[0], Args: []string{"-test.run=TestFakeStdioMCPServer"}, Env: helperEnv("normal", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidTwo}), CWD: root, Enabled: true, EnabledTools: []string{"echo"}},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "collision") {
			t.Fatalf("err = %v", err)
		}
		waitProcessGone(t, readPID(t, pidOne))
		waitProcessGone(t, readPID(t, pidTwo))
	})

	t.Run("startup failure closes client", func(t *testing.T) {
		pidFile := filepath.Join(root, "bad-list.pid")
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots:     []string{root},
			MaxToolOutputBytes: 64 * 1024,
			MCPServers: []config.MCPServer{{
				Name:           "bad-list",
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestFakeStdioMCPServer"},
				Env:            helperEnv("bad_list", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile}),
				CWD:            root,
				Enabled:        true,
				StartupTimeout: 2 * time.Second,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		statuses := manager.Statuses()
		if len(statuses) != 1 || statuses[0].Connected || statuses[0].Error == "" {
			t.Fatalf("statuses = %#v", statuses)
		}
		waitProcessGone(t, readPID(t, pidFile))
	})

	t.Run("manager close disconnects status and process", func(t *testing.T) {
		pidFile := filepath.Join(root, "close.pid")
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots:     []string{root},
			MaxToolOutputBytes: 64 * 1024,
			MCPServers: []config.MCPServer{{
				Name:           "fake",
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestFakeStdioMCPServer"},
				Env:            helperEnv("normal", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile}),
				CWD:            root,
				Enabled:        true,
				StartupTimeout: 2 * time.Second,
				EnabledTools:   []string{"echo"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pid := readPID(t, pidFile)
		if statuses := manager.Statuses(); len(statuses) != 1 || !statuses[0].Connected {
			t.Fatalf("statuses before close = %#v", statuses)
		}
		manager.Close()
		if statuses := manager.Statuses(); len(statuses) != 1 || statuses[0].Connected {
			t.Fatalf("statuses after close = %#v", statuses)
		}
		waitProcessGone(t, pid)
	})
}

func TestManagerStatusListenersObserveLifecycleChanges(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(context.Background(), config.Config{
		WorkspaceRoots:     []string{root},
		MaxToolOutputBytes: 64 * 1024,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("normal", nil),
			CWD:            root,
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
			EnabledTools:   []string{"echo"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan ServerStatus, 4)
	cancel := manager.AddStatusListener(func(status ServerStatus) {
		ch <- status
	})
	manager.Close()
	cancel()

	select {
	case status := <-ch:
		if status.Name != "fake" || status.Connected || status.State != mcpStateDisconnected {
			t.Fatalf("listener status = %#v", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for status listener")
	}
}

func TestStdioCallTimeoutAndTransportCloseDisconnectStatus(t *testing.T) {
	root := t.TempDir()

	t.Run("timeout", func(t *testing.T) {
		pidFile := filepath.Join(root, "timeout.pid")
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots:     []string{root},
			MaxToolOutputBytes: 64 * 1024,
			MCPServers: []config.MCPServer{{
				Name:           "fake",
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestFakeStdioMCPServer"},
				Env:            helperEnv("hang", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile}),
				CWD:            root,
				Enabled:        true,
				StartupTimeout: 2 * time.Second,
				ToolTimeout:    50 * time.Millisecond,
				EnabledTools:   []string{"hang"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		pid := readPID(t, pidFile)
		hang := findTool(t, manager, "mcp__fake__hang")
		_, err = hang.Handler(context.Background(), json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
			t.Fatalf("err = %v", err)
		}
		if statuses := manager.Statuses(); len(statuses) != 1 || statuses[0].Connected || statuses[0].LastError == "" || statuses[0].Error == "" || statuses[0].LastErrorAt == nil || statuses[0].LastErrorAt.IsZero() {
			t.Fatalf("statuses = %#v", statuses)
		} else if !strings.Contains(statuses[0].LastError, "deadline exceeded") {
			t.Fatalf("last error = %q", statuses[0].LastError)
		}
		waitProcessGone(t, pid)
	})

	t.Run("transport close", func(t *testing.T) {
		pidFile := filepath.Join(root, "transport-close.pid")
		manager, err := NewManager(context.Background(), config.Config{
			WorkspaceRoots:     []string{root},
			MaxToolOutputBytes: 64 * 1024,
			MCPServers: []config.MCPServer{{
				Name:           "fake",
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestFakeStdioMCPServer"},
				Env:            helperEnv("close_on_call", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile}),
				CWD:            root,
				Enabled:        true,
				StartupTimeout: 2 * time.Second,
				ToolTimeout:    2 * time.Second,
				EnabledTools:   []string{"close"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		pid := readPID(t, pidFile)
		closeTool := findTool(t, manager, "mcp__fake__close")
		_, err = closeTool.Handler(context.Background(), json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "transport") {
			t.Fatalf("err = %v", err)
		}
		if statuses := manager.Statuses(); len(statuses) != 1 || statuses[0].Connected || statuses[0].LastError == "" || statuses[0].Error == "" || statuses[0].LastErrorAt == nil || statuses[0].LastErrorAt.IsZero() {
			t.Fatalf("statuses = %#v", statuses)
		} else if !strings.Contains(statuses[0].LastError, "transport") {
			t.Fatalf("last error = %q", statuses[0].LastError)
		}
		waitProcessGone(t, pid)
	})
}

func TestStdioCrashReconnectStatusLifecycle(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "reconnect.pid")
	phaseFile := filepath.Join(root, "reconnect.phase")
	manager, err := NewManager(context.Background(), config.Config{
		WorkspaceRoots:     []string{root},
		MaxToolOutputBytes: 64 * 1024,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("close_once_then_echo", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile, "BILLYHARNESS_MCP_PHASE_FILE": phaseFile}),
			CWD:            root,
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
			ToolTimeout:    2 * time.Second,
			EnabledTools:   []string{"echo"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	pidOne := readPID(t, pidFile)
	echo := findTool(t, manager, "mcp__fake__echo")

	_, err = echo.Handler(context.Background(), json.RawMessage(`{"text":"first"}`))
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("first call err = %v", err)
	}
	statuses := manager.Statuses()
	if len(statuses) != 1 || statuses[0].Connected || statuses[0].State != mcpStateCrashed || statuses[0].LastError == "" {
		t.Fatalf("crashed status = %#v", statuses)
	}
	waitProcessGone(t, pidOne)

	text, err := echo.Handler(context.Background(), json.RawMessage(`{"text":"second"}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "second" {
		t.Fatalf("reconnected echo = %q", text)
	}
	pidTwo := readPIDChanged(t, pidFile, pidOne)
	statuses = manager.Statuses()
	if len(statuses) != 1 || !statuses[0].Connected || statuses[0].State != mcpStateReconnected || statuses[0].RestartCount != 1 || statuses[0].RetryCount != 1 || statuses[0].ToolCount != 1 || statuses[0].Error != "" {
		t.Fatalf("reconnected status = %#v", statuses)
	}
	if statuses[0].LastError == "" || strings.Contains(statuses[0].LastError, "sk-test-secret") {
		t.Fatalf("last error = %q", statuses[0].LastError)
	}
	manager.Close()
	waitProcessGone(t, pidTwo)
}

func TestStdioReconnectFailureBackoffIsDeterministic(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "reconnect-fail.pid")
	phaseFile := filepath.Join(root, "reconnect-fail.phase")
	manager, err := NewManager(context.Background(), config.Config{
		WorkspaceRoots:     []string{root},
		MaxToolOutputBytes: 64 * 1024,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("close_then_bad_reconnect", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile, "BILLYHARNESS_MCP_PHASE_FILE": phaseFile}),
			CWD:            root,
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
			ToolTimeout:    2 * time.Second,
			EnabledTools:   []string{"echo"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	pidOne := readPID(t, pidFile)
	echo := findTool(t, manager, "mcp__fake__echo")

	_, err = echo.Handler(context.Background(), json.RawMessage(`{"text":"first"}`))
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("first call err = %v", err)
	}
	waitProcessGone(t, pidOne)

	_, err = echo.Handler(context.Background(), json.RawMessage(`{"text":"second"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid JSON-RPC") {
		t.Fatalf("reconnect err = %v", err)
	}
	pidTwo := readPIDChanged(t, pidFile, pidOne)
	waitProcessGone(t, pidTwo)
	statuses := manager.Statuses()
	if len(statuses) != 1 || statuses[0].Connected || statuses[0].State != mcpStateFailed || statuses[0].RetryCount != 1 || statuses[0].RetryBackoffMS <= 0 || statuses[0].NextRetryAt == nil || statuses[0].Error == "" {
		t.Fatalf("failed reconnect status = %#v", statuses)
	}

	_, err = echo.Handler(context.Background(), json.RawMessage(`{"text":"third"}`))
	if err == nil || !strings.Contains(err.Error(), "reconnect backoff active") {
		t.Fatalf("backoff err = %v", err)
	}
	if got := readPID(t, pidFile); got != pidTwo {
		t.Fatalf("backoff call started a new process: got pid %d, want %d", got, pidTwo)
	}
}

func TestStdioReconnectReportsRestartingWhileStartupInFlight(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "restarting.pid")
	phaseFile := filepath.Join(root, "restarting.phase")
	manager, err := NewManager(context.Background(), config.Config{
		WorkspaceRoots:     []string{root},
		MaxToolOutputBytes: 64 * 1024,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("close_then_slow_reconnect", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile, "BILLYHARNESS_MCP_PHASE_FILE": phaseFile}),
			CWD:            root,
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
			ToolTimeout:    2 * time.Second,
			EnabledTools:   []string{"echo"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	pidOne := readPID(t, pidFile)
	echo := findTool(t, manager, "mcp__fake__echo")

	_, err = echo.Handler(context.Background(), json.RawMessage(`{"text":"first"}`))
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("first call err = %v", err)
	}
	waitProcessGone(t, pidOne)

	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := echo.Handler(context.Background(), json.RawMessage(`{"text":"second"}`))
		done <- struct {
			text string
			err  error
		}{text: text, err: err}
	}()

	status := waitMCPStatus(t, manager, func(status ServerStatus) bool {
		return status.State == mcpStateRestarting && !status.Connected && status.RetryCount == 1
	})
	if status.LastEventAt == nil || status.LastEventAt.IsZero() {
		t.Fatalf("restarting status missing event time: %#v", status)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.text != "second" {
			t.Fatalf("reconnected echo = %q", result.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect call")
	}
}

func TestStdioToolOutputIsCappedBeforeReturning(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(context.Background(), config.Config{
		WorkspaceRoots:     []string{root},
		MaxToolOutputBytes: 64,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("large", nil),
			CWD:            root,
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
			ToolTimeout:    2 * time.Second,
			EnabledTools:   []string{"large"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	large := findTool(t, manager, "mcp__fake__large")
	text, err := large.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(text) > 160 || !strings.Contains(text, "truncated") {
		t.Fatalf("expected capped output with truncation note, len=%d text=%q", len(text), text)
	}
	if strings.Count(text, "x") > 64 {
		t.Fatalf("too much raw MCP output returned: %q", text)
	}
	if statuses := manager.Statuses(); len(statuses) != 1 || !statuses[0].Connected {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestOversizedStdioToolResponseClosesTransport(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "huge-raw.pid")
	manager, err := NewManager(context.Background(), config.Config{
		WorkspaceRoots:     []string{root},
		MaxToolOutputBytes: 64,
		MCPServers: []config.MCPServer{{
			Name:           "fake",
			Command:        os.Args[0],
			Args:           []string{"-test.run=TestFakeStdioMCPServer"},
			Env:            helperEnv("huge_raw", map[string]string{"BILLYHARNESS_MCP_PID_FILE": pidFile}),
			CWD:            root,
			Enabled:        true,
			StartupTimeout: 2 * time.Second,
			ToolTimeout:    2 * time.Second,
			EnabledTools:   []string{"huge_raw"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	pid := readPID(t, pidFile)
	huge := findTool(t, manager, "mcp__fake__huge_raw")
	_, err = huge.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "response exceeded") {
		t.Fatalf("err = %v", err)
	}
	if statuses := manager.Statuses(); len(statuses) != 1 || statuses[0].Connected {
		t.Fatalf("statuses = %#v", statuses)
	}
	waitProcessGone(t, pid)
}

func TestMCPEnvVarsReadBillyharnessDotenv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("MCP_FROM_DOTENV=dotenv-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := mcpEnv(config.MCPServer{EnvVars: []string{"MCP_FROM_DOTENV"}})
	if !contains(env, "MCP_FROM_DOTENV=dotenv-secret-value") {
		t.Fatalf("env = %#v", env)
	}
}

func findTool(t *testing.T, manager *Manager, name string) ExternalTool {
	t.Helper()
	for _, tool := range manager.Tools() {
		if tool.Spec.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s not found in %#v", name, manager.Tools())
	return ExternalTool{}
}

func helperEnv(mode string, extra map[string]string) map[string]string {
	env := map[string]string{
		"BILLYHARNESS_MCP_HELPER": "1",
		"BILLYHARNESS_MCP_MODE":   mode,
	}
	for key, value := range extra {
		env[key] = value
	}
	return env
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s was not written", path)
	return 0
}

func readPIDChanged(t *testing.T, path string, old int) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err == nil && pid > 0 && pid != old {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s did not change from %d", path, old)
	return 0
}

func waitMCPStatus(t *testing.T, manager *Manager, match func(ServerStatus) bool) ServerStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []ServerStatus
	for time.Now().Before(deadline) {
		last = manager.Statuses()
		if len(last) == 1 && match(last[0]) {
			return last[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status, last=%#v", last)
	return ServerStatus{}
}

func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still running", pid)
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func TestFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_MCP_HELPER") != "1" {
		return
	}
	runFakeMCPServer()
	os.Exit(0)
}

func runFakeMCPServer() {
	mode := os.Getenv("BILLYHARNESS_MCP_MODE")
	if pidFile := os.Getenv("BILLYHARNESS_MCP_PID_FILE"); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(fmt.Sprint(os.Getpid())), 0o600)
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(response(req.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
				"instructions":    "Use echo for smoke tests.",
			}))
		case "tools/list":
			if mode == "bad_list" || (mode == "close_then_bad_reconnect" && phaseExists()) {
				_, _ = os.Stdout.Write([]byte("{not json\n"))
				sleepForever()
			}
			if mode == "close_then_slow_reconnect" && phaseExists() {
				time.Sleep(250 * time.Millisecond)
			}
			_ = enc.Encode(response(req.ID, map[string]any{"tools": fakeToolsForMode(mode)}))
		case "tools/call":
			call := struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}{}
			_ = json.Unmarshal(req.Params, &call)
			switch call.Name {
			case "echo":
				if (mode == "close_once_then_echo" || mode == "close_then_bad_reconnect" || mode == "close_then_slow_reconnect") && !phaseExists() {
					writePhase()
					os.Exit(0)
				}
				_ = enc.Encode(response(req.ID, toolResult(fmt.Sprint(call.Arguments["text"]), false)))
			case "env":
				body, _ := json.Marshal(map[string]string{
					"allowed": os.Getenv("MCP_ALLOWED_FROM_PARENT"),
					"literal": os.Getenv("MCP_LITERAL_SECRET"),
					"parent":  os.Getenv("DEEPSEEK_API_KEY"),
				})
				_ = enc.Encode(response(req.ID, toolResult(string(body), false)))
			case "fail":
				_ = enc.Encode(response(req.ID, toolResult("failed with sk-test-secret-1234567890", true)))
			case "hang":
				sleepForever()
			case "close":
				os.Exit(0)
			case "large":
				_ = enc.Encode(response(req.ID, toolResult(strings.Repeat("x", 512), false)))
			case "huge_raw":
				_ = enc.Encode(response(req.ID, toolResult(strings.Repeat("x", 300*1024), false)))
			default:
				_ = enc.Encode(rpcErrorResponse(req.ID, -32602, "unknown tool"))
			}
		default:
			_ = enc.Encode(rpcErrorResponse(req.ID, -32601, "method not found"))
		}
	}
}

func sleepForever() {
	for {
		time.Sleep(time.Hour)
	}
}

func phaseExists() bool {
	path := os.Getenv("BILLYHARNESS_MCP_PHASE_FILE")
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writePhase() {
	path := os.Getenv("BILLYHARNESS_MCP_PHASE_FILE")
	if path != "" {
		_ = os.WriteFile(path, []byte("closed"), 0o600)
	}
}

func fakeToolsForMode(mode string) []map[string]any {
	emptyObject := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	switch mode {
	case "hang":
		return []map[string]any{{"name": "hang", "description": "Never responds", "inputSchema": emptyObject}}
	case "close_on_call":
		return []map[string]any{{"name": "close", "description": "Close transport", "inputSchema": emptyObject}}
	case "large":
		return []map[string]any{{"name": "large", "description": "Large text", "inputSchema": emptyObject}}
	case "huge_raw":
		return []map[string]any{{"name": "huge_raw", "description": "Oversized raw response", "inputSchema": emptyObject}}
	default:
		return []map[string]any{
			{"name": "echo", "description": "Echo text", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}},
			{"name": "env", "description": "Show selected env", "inputSchema": emptyObject},
			{"name": "fail", "description": "Fail with secret", "inputSchema": emptyObject},
		}
	}
}

func response(id any, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcErrorResponse(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}
