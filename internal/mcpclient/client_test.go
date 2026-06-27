package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_MCP_HELPER") != "1" {
		return
	}
	runFakeMCPServer()
	os.Exit(0)
}

func runFakeMCPServer() {
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
			_ = enc.Encode(response(req.ID, map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "Echo text", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}},
				{"name": "env", "description": "Show selected env", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
				{"name": "fail", "description": "Fail with secret", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
			}}))
		case "tools/call":
			call := struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}{}
			_ = json.Unmarshal(req.Params, &call)
			switch call.Name {
			case "echo":
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
			default:
				_ = enc.Encode(rpcErrorResponse(req.ID, -32602, "unknown tool"))
			}
		default:
			_ = enc.Encode(rpcErrorResponse(req.ID, -32601, "method not found"))
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
