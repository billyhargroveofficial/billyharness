package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestToolsFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_TOOLS_MCP_HELPER") != "1" {
		return
	}
	mode := os.Getenv("BILLYHARNESS_TOOLS_MCP_MODE")
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
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
				"instructions":    "Use echo for MCP gateway tests.",
			}})
		case "tools/list":
			if mode == "close_then_bad_reconnect" && toolsMCPPhaseExists() {
				_, _ = os.Stdout.Write([]byte("{not json\n"))
				os.Exit(0)
			}
			name := "echo"
			description := "Echo text"
			if mode == "bad_list_once_then_new_tool" && !toolsMCPPhaseExists() {
				writeToolsMCPPhase()
				_, _ = os.Stdout.Write([]byte("{not json\n"))
				os.Exit(0)
			}
			if (mode == "close_once_then_new_tool" || mode == "bad_list_once_then_new_tool") && toolsMCPPhaseExists() {
				name = "new_echo"
				description = "New echo text"
			}
			if mode == "structured_output" {
				name = "rich"
				description = "Structured output"
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        name,
				"description": description,
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false},
			}}}})
		case "tools/call":
			var call struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &call)
			if call.Name == "rich" && mode == "structured_output" {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "visible: " + fmt.Sprint(call.Arguments["text"])},
						{"type": "image", "mimeType": "image/png", "data": "BASE64_IMAGE_DATA"},
						{"type": "resource", "resource": map[string]any{"uri": "file:///tmp/result.json", "mimeType": "application/json", "text": "resource payload"}},
					},
					"structuredContent": map[string]any{"answer": "structured " + fmt.Sprint(call.Arguments["text"]), "count": 2},
					"_meta":             map[string]any{"request_id": "fake-call-1"},
					"isError":           false,
				}})
				continue
			}
			if call.Name == "new_echo" && (mode == "close_once_then_new_tool" || mode == "bad_list_once_then_new_tool") && toolsMCPPhaseExists() {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "new: " + fmt.Sprint(call.Arguments["text"])}},
					"isError": false,
				}})
				continue
			}
			if call.Name != "echo" {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				continue
			}
			if (mode == "close_once_then_new_tool" || mode == "bad_list_once_then_new_tool") && toolsMCPPhaseExists() {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				continue
			}
			if (mode == "close_once_then_echo" || mode == "close_once_then_new_tool") && !toolsMCPPhaseExists() {
				writeToolsMCPPhase()
				os.Exit(0)
			}
			if mode == "close_then_bad_reconnect" && !toolsMCPPhaseExists() {
				writeToolsMCPPhase()
				os.Exit(0)
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprint(call.Arguments["text"])}},
				"isError": false,
			}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}

func toolsMCPPhaseExists() bool {
	path := os.Getenv("BILLYHARNESS_TOOLS_MCP_PHASE_FILE")
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeToolsMCPPhase() {
	path := os.Getenv("BILLYHARNESS_TOOLS_MCP_PHASE_FILE")
	if path != "" {
		_ = os.WriteFile(path, []byte("closed"), 0o600)
	}
}
