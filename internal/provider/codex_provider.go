// Portions of this file are adapted from OpenAI Codex.
// Original project: https://github.com/openai/codex
// Copyright 2025 OpenAI
// Licensed under the Apache License, Version 2.0.

package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

type Codex struct {
	BaseURL           string
	Model             string
	ReasoningEffort   string
	RequestTimeout    time.Duration
	StreamIdleTimeout time.Duration
	Originator        string
	UserAgent         string
	SessionID         string
	CodexRefreshURL   string
	CodexClientID     string
	Auth              *codexAuth
	Client            *http.Client
}

func (c *Codex) Stream(ctx context.Context, req Request) (<-chan Event, <-chan error) {
	events := make(chan Event, 64)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := c.stream(ctx, req, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (c *Codex) stream(ctx context.Context, req Request, events chan<- Event) error {
	body, err := c.body(req)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.RequestTimeout)
	defer cancel()
	if c.Auth != nil && c.Auth.needsRefresh(time.Now()) && c.Auth.RefreshToken != "" {
		if err := c.Auth.refresh(reqCtx, config.Config{
			CodexRefreshURL: c.CodexRefreshURL,
			CodexClientID:   c.CodexClientID,
		}, c.Client); err != nil {
			return err
		}
	}
	if c.Auth != nil && c.Auth.needsRefresh(time.Now()) {
		return fmt.Errorf("Codex access token needs refresh but no refresh token is available")
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, codexResponsesURL(c.BaseURL), bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.Auth.AccessToken)
	if c.Auth.AccountID != "" {
		httpReq.Header.Set("ChatGPT-Account-ID", c.Auth.AccountID)
	}
	if c.Auth.FedRAMP {
		httpReq.Header.Set("X-OpenAI-Fedramp", "true")
	}
	if c.Originator != "" {
		httpReq.Header.Set("originator", c.Originator)
	}
	if c.UserAgent != "" {
		httpReq.Header.Set("User-Agent", c.UserAgent)
	}
	if c.SessionID != "" {
		httpReq.Header.Set("session-id", c.SessionID)
	}
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("Codex provider request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Codex provider HTTP %d: %s", resp.StatusCode, secrets.Redact(string(limited), c.Auth.AccessToken))
	}
	return parseResponsesSSE(reqCtx, resp.Body, c.StreamIdleTimeout, events)
}

func (c *Codex) body(req Request) ([]byte, error) {
	input, instructions := codexInput(req.Messages)
	tools, err := codexTools(req.Tools)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"model":               req.Model,
		"input":               input,
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"store":               false,
		"stream":              true,
		"include":             []string{},
		"prompt_cache_key":    "billyharness",
	}
	if strings.TrimSpace(instructions) != "" {
		payload["instructions"] = instructions
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	} else {
		payload["parallel_tool_calls"] = false
	}
	if effort := codexReasoningEffort(c.ReasoningEffort); effort != "" {
		payload["reasoning"] = map[string]any{
			"effort":  effort,
			"summary": "auto",
		}
		payload["include"] = []string{"reasoning.encrypted_content"}
	}
	return json.Marshal(payload)
}

func codexInput(messages []protocol.Message) ([]map[string]any, string) {
	var input []map[string]any
	var instructions []string
	for _, msg := range messages {
		switch msg.Role {
		case protocol.RoleSystem:
			if strings.TrimSpace(msg.Content) != "" {
				instructions = append(instructions, msg.Content)
			}
		case protocol.RoleUser:
			input = append(input, codexMessage("user", "input_text", msg.Content))
		case protocol.RoleAssistant:
			if msg.Content != "" {
				input = append(input, codexMessage("assistant", "output_text", msg.Content))
			}
			for _, call := range msg.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"name":      call.Name,
					"arguments": string(call.Arguments),
					"call_id":   call.ID,
				})
			}
		case protocol.RoleTool:
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		}
	}
	return input, strings.Join(instructions, "\n\n")
}

func codexMessage(role, contentType, text string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": contentType,
			"text": text,
		}},
	}
}

func codexTools(specs []protocol.ToolSpec) ([]map[string]any, error) {
	tools := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		var params any
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return nil, fmt.Errorf("invalid tool schema for %s: %w", spec.Name, err)
		}
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        spec.Name,
			"description": spec.Description,
			"strict":      false,
			"parameters":  params,
		})
	}
	return tools, nil
}

func codexReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "disabled", "none", "false":
		return ""
	case "low", "medium", "high", "xhigh", "max", "minimal":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func codexResponsesURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/responses") {
		return base
	}
	return base + "/responses"
}

func parseResponsesSSE(ctx context.Context, r io.Reader, idle time.Duration, events chan<- Event) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, errs := scanLines(ctx, r)
	var timer <-chan time.Time
	if idle > 0 {
		timer = time.After(idle)
	}
	parser := newResponsesParser()
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		chunk := strings.TrimSpace(strings.Join(data, "\n"))
		data = data[:0]
		if chunk == "" || chunk == "[DONE]" {
			return nil
		}
		if err := parser.Handle(ctx, []byte(chunk), events); err != nil {
			return err
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer:
			return errors.New("Codex provider stream idle timeout")
		case err := <-errs:
			if err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
			if !parser.completed {
				return errors.New("Codex stream closed before response.completed")
			}
			return nil
		case line, ok := <-lines:
			if !ok {
				if err := flush(); err != nil {
					return err
				}
				if !parser.completed {
					return errors.New("Codex stream closed before response.completed")
				}
				return nil
			}
			if idle > 0 {
				timer = time.After(idle)
			}
			line = strings.TrimRight(line, "\r")
			if line == "" {
				if err := flush(); err != nil {
					return err
				}
				if parser.completed {
					return nil
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
	}
}

type responsesParser struct {
	indexByCallID  map[string]int
	callIDByItemID map[string]string
	nameByCallID   map[string]string
	sawArgsDelta   map[string]bool
	nextIndex      int
	sawTextDelta   bool
	completed      bool
}

func newResponsesParser() *responsesParser {
	return &responsesParser{
		indexByCallID:  map[string]int{},
		callIDByItemID: map[string]string{},
		nameByCallID:   map[string]string{},
		sawArgsDelta:   map[string]bool{},
	}
}

func (p *responsesParser) Handle(ctx context.Context, data []byte, events chan<- Event) error {
	var raw struct {
		Type         string          `json:"type"`
		Delta        string          `json:"delta"`
		ItemID       string          `json:"item_id"`
		CallID       string          `json:"call_id"`
		Item         json.RawMessage `json:"item"`
		Response     json.RawMessage `json:"response"`
		SummaryIndex *int64          `json:"summary_index"`
		ContentIndex *int64          `json:"content_index"`
		Error        *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid Codex SSE JSON: %w", err)
	}
	switch raw.Type {
	case "response.output_text.delta":
		if raw.Delta != "" {
			p.sawTextDelta = true
			if err := sendEvent(ctx, events, Event{Kind: EventContent, Text: raw.Delta}); err != nil {
				return err
			}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if raw.Delta != "" {
			if err := sendEvent(ctx, events, Event{Kind: EventReasoning, Text: raw.Delta}); err != nil {
				return err
			}
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		callID := p.resolveCallID(raw.CallID, raw.ItemID)
		index := p.toolIndex(callID)
		p.sawArgsDelta[callID] = true
		if err := sendEvent(ctx, events, Event{
			Kind:      EventToolCallDelta,
			ToolIndex: index,
			ToolID:    callID,
			ToolName:  p.nameByCallID[callID],
			ArgsDelta: raw.Delta,
		}); err != nil {
			return err
		}
	case "response.output_item.added", "response.output_item.done":
		if len(raw.Item) == 0 {
			return nil
		}
		return p.handleOutputItem(ctx, raw.Type, raw.Item, events)
	case "response.completed":
		usage := codexUsage(raw.Response)
		if usage != (Usage{}) {
			if err := sendEvent(ctx, events, Event{Kind: EventUsage, Usage: usage}); err != nil {
				return err
			}
		}
		if err := sendEvent(ctx, events, Event{Kind: EventDone}); err != nil {
			return err
		}
		p.completed = true
	case "response.failed", "response.incomplete":
		return codexResponseError(raw.Response, raw.Type)
	case "error":
		if raw.Error != nil {
			return fmt.Errorf("Codex error %s: %s", raw.Error.Code, raw.Error.Message)
		}
	}
	return nil
}

func (p *responsesParser) handleOutputItem(ctx context.Context, eventType string, data []byte, events chan<- Event) error {
	var item struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		CallID    string `json:"call_id"`
		Role      string `json:"role"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return fmt.Errorf("invalid Codex output item JSON: %w", err)
	}
	switch item.Type {
	case "function_call", "custom_tool_call":
		callID := firstNonEmpty(item.CallID, item.ID)
		if item.ID != "" && callID != "" {
			p.callIDByItemID[item.ID] = callID
			if item.ID != callID {
				p.aliasCallID(item.ID, callID)
			}
		}
		index := p.toolIndex(callID)
		if item.Name != "" {
			p.nameByCallID[callID] = item.Name
		}
		args := ""
		if eventType == "response.output_item.done" && !p.sawArgsDelta[callID] {
			args = item.Arguments
		}
		return sendEvent(ctx, events, Event{
			Kind:      EventToolCallDelta,
			ToolIndex: index,
			ToolID:    callID,
			ToolName:  item.Name,
			ArgsDelta: args,
		})
	case "message":
		if eventType == "response.output_item.done" && !p.sawTextDelta && item.Role == "assistant" {
			for _, content := range item.Content {
				if content.Type == "output_text" && content.Text != "" {
					if err := sendEvent(ctx, events, Event{Kind: EventContent, Text: content.Text}); err != nil {
						return err
					}
				}
			}
		}
	case "reasoning":
		for _, summary := range item.Summary {
			if summary.Text != "" {
				if err := sendEvent(ctx, events, Event{Kind: EventReasoning, Text: summary.Text}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *responsesParser) toolIndex(callID string) int {
	if callID == "" {
		callID = fmt.Sprintf("call_%d", p.nextIndex)
	}
	if index, ok := p.indexByCallID[callID]; ok {
		return index
	}
	index := p.nextIndex
	p.nextIndex++
	p.indexByCallID[callID] = index
	return index
}

func (p *responsesParser) resolveCallID(callID, itemID string) string {
	if callID != "" {
		return callID
	}
	if itemID != "" {
		if mapped := p.callIDByItemID[itemID]; mapped != "" {
			return mapped
		}
		return itemID
	}
	return ""
}

func (p *responsesParser) aliasCallID(from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	if index, ok := p.indexByCallID[from]; ok {
		if _, exists := p.indexByCallID[to]; !exists {
			p.indexByCallID[to] = index
		}
		delete(p.indexByCallID, from)
	}
	if name := p.nameByCallID[from]; name != "" {
		if p.nameByCallID[to] == "" {
			p.nameByCallID[to] = name
		}
		delete(p.nameByCallID, from)
	}
	if p.sawArgsDelta[from] {
		p.sawArgsDelta[to] = true
		delete(p.sawArgsDelta, from)
	}
}

func codexUsage(data json.RawMessage) Usage {
	var raw struct {
		Usage *struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			TotalTokens        int64 `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails *struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if len(data) == 0 || json.Unmarshal(data, &raw) != nil || raw.Usage == nil {
		return Usage{}
	}
	cacheHit := int64(0)
	if raw.Usage.InputTokensDetails != nil {
		cacheHit = raw.Usage.InputTokensDetails.CachedTokens
	}
	reasoning := int64(0)
	if raw.Usage.OutputTokensDetails != nil {
		reasoning = raw.Usage.OutputTokensDetails.ReasoningTokens
	}
	cacheMiss := raw.Usage.InputTokens - cacheHit
	if cacheMiss < 0 {
		cacheMiss = 0
	}
	return Usage{
		InputTokens:     raw.Usage.InputTokens,
		OutputTokens:    raw.Usage.OutputTokens,
		CacheHitTokens:  cacheHit,
		CacheMissTokens: cacheMiss,
		ReasoningTokens: reasoning,
	}
}

func codexResponseError(data json.RawMessage, fallback string) error {
	var raw struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if len(data) > 0 && json.Unmarshal(data, &raw) == nil {
		if raw.Error != nil {
			if raw.Error.Code != "" {
				return fmt.Errorf("Codex %s: %s", raw.Error.Code, raw.Error.Message)
			}
			if raw.Error.Message != "" {
				return fmt.Errorf("Codex error: %s", raw.Error.Message)
			}
		}
		if raw.IncompleteDetails != nil && raw.IncompleteDetails.Reason != "" {
			return fmt.Errorf("Codex incomplete response: %s", raw.IncompleteDetails.Reason)
		}
	}
	return fmt.Errorf("Codex stream event %s", fallback)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func newCodexSessionID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
