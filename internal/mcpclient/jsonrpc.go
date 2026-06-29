package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

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

type callToolResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError"`
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
		if resp.Method != "" && len(resp.ID) == 0 {
			c.handleNotification(resp.Method, resp.Params)
			continue
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

func (c *stdioClient) handleNotification(method string, params json.RawMessage) {
	if c == nil || c.onNotification == nil {
		return
	}
	c.onNotification(method, params)
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
