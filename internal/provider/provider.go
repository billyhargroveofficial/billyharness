package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

type Request struct {
	Model       string
	Messages    []protocol.Message
	Tools       []protocol.ToolSpec
	Temperature *float64
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
}

type EventKind int

const (
	EventContent EventKind = iota
	EventReasoning
	EventToolCallDelta
	EventUsage
	EventDone
)

type Event struct {
	Kind      EventKind
	Text      string
	ToolIndex int
	ToolID    string
	ToolName  string
	ArgsDelta string
	Usage     Usage
}

type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, <-chan error)
}

func New(cfg config.Config) (Provider, error) {
	if cfg.Provider == "mock" {
		return Mock{}, nil
	}
	if cfg.APIKey() == "" {
		return nil, fmt.Errorf("missing API key env var %s", cfg.APIKeyEnv)
	}
	return &DeepSeek{
		BaseURL:           strings.TrimRight(cfg.BaseURL, "/"),
		APIKey:            cfg.APIKey(),
		Model:             cfg.Model,
		Thinking:          cfg.Thinking,
		ReasoningEffort:   cfg.ReasoningEffort,
		MaxTokens:         cfg.MaxTokens,
		RequestTimeout:    cfg.RequestTimeout,
		StreamIdleTimeout: cfg.StreamIdleTimeout,
		Client:            &http.Client{Timeout: 0},
	}, nil
}

type Mock struct{}

func (Mock) Stream(ctx context.Context, req Request) (<-chan Event, <-chan error) {
	events := make(chan Event, 2)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		last := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == protocol.RoleUser {
				last = req.Messages[i].Content
				break
			}
		}
		select {
		case events <- Event{Kind: EventContent, Text: "mock: " + last}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		events <- Event{Kind: EventDone}
	}()
	return events, errs
}

type DeepSeek struct {
	BaseURL           string
	APIKey            string
	Model             string
	Thinking          string
	ReasoningEffort   string
	MaxTokens         int
	RequestTimeout    time.Duration
	StreamIdleTimeout time.Duration
	Client            *http.Client
}

func (d *DeepSeek) Stream(ctx context.Context, req Request) (<-chan Event, <-chan error) {
	events := make(chan Event, 64)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := d.stream(ctx, req, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (d *DeepSeek) stream(ctx context.Context, req Request, events chan<- Event) error {
	body, err := d.body(req)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, d.RequestTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, d.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.APIKey)
	resp, err := d.Client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("provider request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, secrets.Redact(string(limited), d.APIKey))
	}
	return parseSSE(reqCtx, resp.Body, d.StreamIdleTimeout, events)
}

func (d *DeepSeek) body(req Request) ([]byte, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		item := map[string]any{"role": string(msg.Role)}
		if msg.Role == protocol.RoleTool {
			item["tool_call_id"] = msg.ToolCallID
			item["content"] = msg.Content
		} else {
			item["content"] = msg.Content
		}
		if msg.Role == protocol.RoleAssistant && len(msg.ToolCalls) > 0 {
			if msg.Content == "" {
				item["content"] = nil
			}
			if msg.ReasoningContent != "" {
				item["reasoning_content"] = msg.ReasoningContent
			}
			toolCalls := make([]map[string]any, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				toolCalls = append(toolCalls, map[string]any{
					"id":   call.ID,
					"type": "function",
					"function": map[string]any{
						"name":      call.Name,
						"arguments": string(call.Arguments),
					},
				})
			}
			item["tool_calls"] = toolCalls
		}
		messages = append(messages, item)
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		var params any
		if err := json.Unmarshal(tool.Parameters, &params); err != nil {
			return nil, fmt.Errorf("invalid tool schema for %s: %w", tool.Name, err)
		}
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  params,
			},
		})
	}
	payload := map[string]any{
		"model":          req.Model,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	if d.MaxTokens > 0 {
		payload["max_tokens"] = d.MaxTokens
	}
	if d.Thinking != "" {
		payload["thinking"] = map[string]any{"type": d.Thinking}
		if d.Thinking == "enabled" && d.ReasoningEffort != "" {
			payload["reasoning_effort"] = d.ReasoningEffort
		}
	} else if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	return json.Marshal(payload)
}

func parseSSE(ctx context.Context, r io.Reader, idle time.Duration, events chan<- Event) error {
	lines := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		errs <- scanner.Err()
	}()
	var timer <-chan time.Time
	if idle > 0 {
		timer = time.After(idle)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer:
			return errors.New("provider stream idle timeout")
		case err := <-errs:
			return err
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			if idle > 0 {
				timer = time.After(idle)
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				events <- Event{Kind: EventDone}
				continue
			}
			parsed, err := parseChunk([]byte(data))
			if err != nil {
				return err
			}
			for _, event := range parsed {
				events <- event
			}
		}
	}
}

func parseChunk(data []byte) ([]Event, error) {
	var raw struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid SSE JSON: %w", err)
	}
	var events []Event
	if raw.Usage != nil {
		events = append(events, Event{Kind: EventUsage, Usage: Usage{InputTokens: raw.Usage.PromptTokens, OutputTokens: raw.Usage.CompletionTokens}})
	}
	if len(raw.Choices) == 0 {
		return events, nil
	}
	delta := raw.Choices[0].Delta
	if delta.Content != "" {
		events = append(events, Event{Kind: EventContent, Text: delta.Content})
	}
	if delta.ReasoningContent != "" {
		events = append(events, Event{Kind: EventReasoning, Text: delta.ReasoningContent})
	}
	for _, call := range delta.ToolCalls {
		events = append(events, Event{
			Kind:      EventToolCallDelta,
			ToolIndex: call.Index,
			ToolID:    call.ID,
			ToolName:  call.Function.Name,
			ArgsDelta: call.Function.Arguments,
		})
	}
	return events, nil
}

type ToolAccumulator struct {
	calls []partialToolCall
}

type partialToolCall struct {
	ID   string
	Name string
	Args strings.Builder
}

func (a *ToolAccumulator) Push(event Event) {
	for len(a.calls) <= event.ToolIndex {
		a.calls = append(a.calls, partialToolCall{})
	}
	call := &a.calls[event.ToolIndex]
	if event.ToolID != "" {
		call.ID = event.ToolID
	}
	if event.ToolName != "" {
		call.Name = event.ToolName
	}
	call.Args.WriteString(event.ArgsDelta)
}

func (a *ToolAccumulator) Finish() ([]protocol.ToolCall, error) {
	var out []protocol.ToolCall
	for i, call := range a.calls {
		if call.Name == "" {
			continue
		}
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		args := strings.TrimSpace(call.Args.String())
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, fmt.Errorf("tool call %s had invalid JSON args", call.Name)
		}
		out = append(out, protocol.ToolCall{ID: id, Name: call.Name, Arguments: json.RawMessage(args)})
	}
	return out, nil
}
