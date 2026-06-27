package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

const protocolVersion = "2025-11-25"

type Server struct {
	registry *tools.Registry
}

type request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(registry *tools.Registry) *Server {
	return &Server{registry: registry}
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if writeErr := enc.Encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}); writeErr != nil {
				return writeErr
			}
			continue
		}
		resp, ok := s.handle(ctx, req)
		if !ok {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, req request) (response, bool) {
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, -32600, "invalid JSON-RPC version"), true
	}
	switch req.Method {
	case "initialize":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":        "fast-agent-harness-go",
				"title":       "Fast Agent Harness",
				"version":     "0.1.0",
				"description": "Native tools exposed by fast-agent-harness-go",
			},
			"instructions": "Use tools for filesystem, web, and controlled shell operations. Write and execute tools are enabled by default. Set FAST_AGENT_AUTO_APPROVE_DANGEROUS=false to disable them.",
		}}, true
	case "notifications/initialized":
		return response{}, false
	case "ping":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}, true
	case "tools/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.mcpTools()}}, true
	case "tools/call":
		return s.callTool(ctx, req), true
	default:
		return errorResponse(req.ID, -32601, "method not found"), true
	}
}

func (s *Server) mcpTools() []map[string]any {
	specs := s.registry.Specs()
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		var schema any
		if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"name":        spec.Name,
			"title":       spec.Name,
			"description": spec.Description,
			"inputSchema": schema,
			"annotations": annotations(spec.Risk),
			"_meta":       map[string]any{"risk": spec.Risk},
		})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, req request) response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) == 0 {
		return errorResponse(req.ID, -32602, "params required")
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, "invalid params")
	}
	if params.Name == "" {
		return errorResponse(req.ID, -32602, "tool name required")
	}
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage(`{}`)
	}
	result, err := s.registry.Call(ctx, protocol.ToolCall{Name: params.Name, Arguments: params.Arguments})
	isError := false
	text := result.Content
	if err != nil {
		isError = true
		if text == "" {
			text = err.Error()
		} else {
			text = fmt.Sprintf("%s\n%s", text, err.Error())
		}
	}
	return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}}
}

func annotations(risk protocol.Risk) map[string]any {
	return map[string]any{
		"readOnlyHint":    risk == protocol.RiskReadOnly || risk == protocol.RiskNetwork,
		"destructiveHint": risk == protocol.RiskWrite || risk == protocol.RiskExecute,
		"openWorldHint":   risk == protocol.RiskNetwork || risk == protocol.RiskExternal,
	}
}

func errorResponse(id *json.RawMessage, code int, message string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}
