// Portions of this file are adapted from OpenAI Codex.
// Original project: https://github.com/openai/codex
// Copyright 2025 OpenAI
// Licensed under the Apache License, Version 2.0.

package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/attachments"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

type Codex struct {
	BaseURL           string
	Model             string
	ReasoningEffort   string
	MaxTokens         int
	RequestTimeout    time.Duration
	StreamIdleTimeout time.Duration
	Originator        string
	UserAgent         string
	SessionID         string
	MaxRetries        int
	CodexRefreshURL   string
	CodexClientID     string
	Auth              *codexAuth
	Client            *http.Client
	AttachmentStore   attachments.Store
	authMu            sync.Mutex
}

type codexAuthSnapshot struct {
	AccessToken string
	AccountID   string
}

func (c *Codex) Stream(ctx context.Context, req Request) (<-chan Event, <-chan error) {
	events := newProviderEventChannel()
	errs := make(chan error, 1)
	go runProviderStream(events, errs, func() error {
		return c.stream(ctx, req, events)
	})
	return events, errs
}

func (c *Codex) stream(ctx context.Context, req Request, events chan<- Event) error {
	body, err := c.body(req)
	if err != nil {
		return err
	}
	if err := c.ensureFreshAuth(ctx); err != nil {
		return err
	}
	resp, respCancel, meta, err := c.doResponsesWithRetry(ctx, body, RequestMetadata{
		RequestID:  req.RequestID,
		ProviderID: "openai-codex",
		ModelID:    req.Model,
	})
	if err != nil {
		return withRequestMetadata(err, meta)
	}
	if respCancel != nil {
		defer respCancel()
	}
	defer resp.Body.Close()
	if err := sendEvent(ctx, events, Event{Kind: EventRequestMetadata, Request: meta}); err != nil {
		return err
	}
	if err := parseResponsesSSE(ctx, resp.Body, c.StreamIdleTimeout, events); err != nil {
		if _, ok := FinishFromError(err); ok {
			return withRequestMetadata(err, meta)
		}
		return withRequestMetadata(providerStreamError("openai-codex", req.Model, err), meta)
	}
	return nil
}

func (c *Codex) doResponsesWithRetry(ctx context.Context, body []byte, meta RequestMetadata) (*http.Response, context.CancelFunc, RequestMetadata, error) {
	retriesUsed := 0
	refreshedUnauthorized := false
	attempts := 0
	for {
		attempts++
		resp, respCancel, err := c.doResponsesRequest(ctx, body)
		if err == nil {
			meta.Attempts = attempts
			meta.Retries = attempts - 1
			if meta.Retries < retriesUsed {
				meta.Retries = retriesUsed
			}
			meta.StatusCode = resp.StatusCode
			meta.ProviderRequestID = firstHeader(resp.Header, "x-request-id", "request-id", "openai-request-id")
			return resp, respCancel, meta, nil
		}
		meta.Attempts = attempts
		meta.Retries = attempts - 1
		if meta.Retries < retriesUsed {
			meta.Retries = retriesUsed
		}
		meta.StatusCode = 0
		meta.ProviderRequestID = ""
		var providerErr *ProviderError
		if errors.As(err, &providerErr) {
			meta.StatusCode = providerErr.Status
			meta.ProviderRequestID = providerErr.RequestID
		}
		refreshed, refreshErr := c.refreshAfterUnauthorized(ctx, err, refreshedUnauthorized)
		if refreshErr != nil {
			return nil, nil, meta, refreshErr
		}
		if refreshed {
			refreshedUnauthorized = true
			continue
		}
		if !retryableProviderError(err) || retriesUsed >= c.MaxRetries {
			return nil, nil, meta, err
		}
		if sleepErr := sleepProviderRetry(ctx, providerRetryDelay(err, retriesUsed)); sleepErr != nil {
			return nil, nil, meta, sleepErr
		}
		retriesUsed++
	}
}

func (c *Codex) doResponsesRequest(ctx context.Context, body []byte) (*http.Response, context.CancelFunc, error) {
	auth, err := c.authSnapshot()
	if err != nil {
		return nil, nil, err
	}
	reqCtx, finishSetup, cancelReq := newRequestSetupContext(ctx, c.RequestTimeout)
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, codexResponsesURL(c.BaseURL), bytes.NewReader(body))
	if err != nil {
		_ = finishSetup()
		cancelReq()
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	if auth.AccountID != "" {
		httpReq.Header.Set("ChatGPT-Account-ID", auth.AccountID)
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
	if finishSetup() {
		if resp != nil {
			_ = resp.Body.Close()
		}
		cancelReq()
		return nil, nil, context.DeadlineExceeded
	}
	if err != nil {
		cancelReq()
		return nil, nil, providerTransportError("codex", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancelReq()
		return nil, nil, providerHTTPError("codex", resp.StatusCode, resp.Header, secrets.Redact(string(limited), auth.AccessToken))
	}
	return resp, cancelReq, nil
}

func (c *Codex) ensureFreshAuth(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("Codex provider is nil")
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.Auth == nil || strings.TrimSpace(c.Auth.AccessToken) == "" {
		return fmt.Errorf("Codex auth is missing an access token")
	}
	if !c.Auth.needsRefresh(time.Now()) {
		return nil
	}
	if c.Auth.RefreshToken == "" {
		return fmt.Errorf("Codex access token needs refresh but no refresh token is available")
	}
	return c.refreshAuthLocked(ctx)
}

func (c *Codex) authSnapshot() (codexAuthSnapshot, error) {
	if c == nil {
		return codexAuthSnapshot{}, fmt.Errorf("Codex provider is nil")
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.Auth == nil || strings.TrimSpace(c.Auth.AccessToken) == "" {
		return codexAuthSnapshot{}, fmt.Errorf("Codex auth is missing an access token")
	}
	return codexAuthSnapshot{
		AccessToken: c.Auth.AccessToken,
		AccountID:   c.Auth.AccountID,
	}, nil
}

func (c *Codex) refreshAfterUnauthorized(ctx context.Context, err error, alreadyRefreshed bool) (bool, error) {
	if alreadyRefreshed {
		return false, nil
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Status != http.StatusUnauthorized {
		return false, nil
	}
	if c == nil {
		return false, nil
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.Auth == nil || c.Auth.PAT || c.Auth.RefreshToken == "" {
		return false, nil
	}
	if err := c.refreshAuthLocked(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Codex) refreshAuthLocked(ctx context.Context) error {
	refreshCtx, finishSetup, cancelRefresh := newRequestSetupContext(ctx, c.RequestTimeout)
	err := c.Auth.refresh(refreshCtx, config.AuthSettings{
		CodexRefreshURL: c.CodexRefreshURL,
		CodexClientID:   c.CodexClientID,
	}, c.Client)
	setupTimedOut := finishSetup()
	cancelRefresh()
	if setupTimedOut {
		return context.DeadlineExceeded
	}
	return err
}

func (c *Codex) body(req Request) ([]byte, error) {
	input, instructions, err := c.codexInput(req.Messages)
	if err != nil {
		return nil, err
	}
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
	if c.MaxTokens > 0 {
		payload["max_output_tokens"] = c.MaxTokens
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

func (c *Codex) codexInput(messages []protocol.Message) ([]map[string]any, string, error) {
	var input []map[string]any
	var instructions []string
	imageNumber := 0
	for _, msg := range messages {
		switch msg.Role {
		case protocol.RoleSystem:
			if strings.TrimSpace(msg.Content) != "" {
				instructions = append(instructions, msg.Content)
			}
		case protocol.RoleUser:
			content, err := c.codexUserContent(msg, &imageNumber)
			if err != nil {
				return nil, "", err
			}
			input = append(input, codexMessageContent("user", content))
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
	return input, strings.Join(instructions, "\n\n"), nil
}

func (c *Codex) codexUserContent(msg protocol.Message, imageNumber *int) ([]map[string]any, error) {
	parts := protocol.MessagePartsOrText(msg)
	if len(msg.Parts) > 0 && strings.TrimSpace(msg.Content) != "" && !messagePartsHaveText(msg.Parts) {
		parts = append([]protocol.MessagePart{protocol.TextPart(msg.Content)}, parts...)
	}
	if len(parts) == 0 {
		return []map[string]any{{"type": "input_text", "text": ""}}, nil
	}
	content := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case protocol.MessagePartText:
			if part.Text != "" {
				content = append(content, map[string]any{"type": "input_text", "text": part.Text})
			}
		case protocol.MessagePartAttachment:
			if part.Attachment == nil {
				return nil, errors.New("attachment part is missing metadata")
			}
			if part.Attachment.Kind != "" && part.Attachment.Kind != protocol.AttachmentKindImage {
				return nil, fmt.Errorf("unsupported attachment kind %q for Codex input", part.Attachment.Kind)
			}
			item, err := c.codexImageContent(*part.Attachment, imageNumber)
			if err != nil {
				return nil, err
			}
			content = append(content, item...)
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "input_text", "text": ""})
	}
	return content, nil
}

func (c *Codex) codexImageContent(ref protocol.AttachmentRef, imageNumber *int) ([]map[string]any, error) {
	store := c.attachmentStore()
	data, resolved, err := store.Read(ref)
	if err != nil {
		return nil, fmt.Errorf("resolve image attachment %s: %w", firstNonEmpty(ref.ID, ref.FileName, "unknown"), err)
	}
	number := 1
	if imageNumber != nil {
		(*imageNumber)++
		number = *imageNumber
	}
	mimeType := strings.TrimSpace(resolved.MIMEType)
	if mimeType == "" {
		prefix := data
		if len(prefix) > 512 {
			prefix = prefix[:512]
		}
		mimeType = http.DetectContentType(prefix)
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("attachment %s has unsupported MIME type %q", resolved.ID, mimeType)
	}
	image := map[string]any{
		"type":      "input_image",
		"image_url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
	}
	if detail := codexImageDetail(resolved.Detail); detail != "" {
		image["detail"] = detail
	}
	return []map[string]any{
		{"type": "input_text", "text": fmt.Sprintf("[Image #%d]", number)},
		image,
	}, nil
}

func messagePartsHaveText(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartText && part.Text != "" {
			return true
		}
	}
	return false
}

func (c *Codex) attachmentStore() attachments.Store {
	if c != nil && strings.TrimSpace(c.AttachmentStore.Root) != "" {
		return c.AttachmentStore
	}
	return attachments.DefaultStore()
}

func codexMessage(role, contentType, text string) map[string]any {
	return codexMessageContent(role, []map[string]any{{
		"type": contentType,
		"text": text,
	}})
}

func codexMessageContent(role string, content []map[string]any) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}
}

func codexImageDetail(detail protocol.AttachmentDetail) string {
	switch detail {
	case protocol.AttachmentDetailAuto, protocol.AttachmentDetailLow, protocol.AttachmentDetailHigh:
		return string(detail)
	default:
		return ""
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
	idleTimer, idleC := newStreamIdleTimer(idle)
	defer stopStreamIdleTimer(idleTimer)
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
		case <-idleC:
			return errors.New("Codex provider stream idle timeout")
		case line, ok := <-lines:
			if !ok {
				if err := <-errs; err != nil {
					return err
				}
				if err := flush(); err != nil {
					return err
				}
				if !parser.completed {
					return errors.New("Codex stream closed before response.completed")
				}
				return nil
			}
			resetStreamIdleTimer(idleTimer, idle)
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
	sawToolCall    bool
	sawRefusal     bool
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
		p.sawToolCall = true
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
	case "response.refusal.delta", "response.refusal.done":
		p.sawRefusal = true
	case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
		usage := codexUsage(raw.Response)
		if usage != (Usage{}) {
			if err := sendEvent(ctx, events, Event{Kind: EventUsage, Usage: usage}); err != nil {
				return err
			}
		}
		finish, terminalErr := codexResponseFinish(raw.Response, raw.Type, p.sawToolCall, p.sawRefusal)
		if err := sendEvent(ctx, events, Event{Kind: EventDone, Finish: finish}); err != nil {
			return err
		}
		p.completed = true
		return terminalErr
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
		p.sawToolCall = true
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
		for _, content := range item.Content {
			switch content.Type {
			case "refusal":
				p.sawRefusal = true
			case "output_text":
				if eventType == "response.output_item.done" && !p.sawTextDelta && item.Role == "assistant" && content.Text != "" {
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

type codexTerminalResponse struct {
	Status string `json:"status"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	} `json:"output"`
}

// codexResponseFinish normalizes terminal Responses API state. The event type
// and response.status are redundant in valid streams; disagreement is treated
// as an explicit unknown finish so a corrupt or incompatible stream can never
// masquerade as successful completion.
func codexResponseFinish(data json.RawMessage, eventType string, sawToolCall, sawRefusal bool) (Finish, error) {
	var response codexTerminalResponse
	if len(data) > 0 {
		if err := json.Unmarshal(data, &response); err != nil {
			finish := Finish{Kind: FinishUnknown, RawReason: sanitizeFinishReason(eventType)}
			return finish, codexFinishTerminalError(finish, fmt.Errorf("invalid Codex terminal response JSON: %w", err))
		}
	}
	for _, output := range response.Output {
		switch output.Type {
		case "function_call", "custom_tool_call":
			sawToolCall = true
		case "message":
			for _, content := range output.Content {
				if content.Type == "refusal" {
					sawRefusal = true
				}
			}
		}
	}

	expectedStatus := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(eventType)), "response.")
	status := codexFinishToken(response.Status)
	if status != "" && status != expectedStatus {
		rawReason := firstFinishReason(response.Status, eventType)
		finish := Finish{Kind: FinishUnknown, RawReason: rawReason}
		return finish, codexFinishTerminalError(finish, fmt.Errorf("Codex terminal event %s has contradictory response status %q", eventType, response.Status))
	}
	if status == "" {
		status = expectedStatus
	}
	if sawToolCall && sawRefusal {
		finish := Finish{Kind: FinishUnknown, RawReason: "tool_calls_and_refusal"}
		return finish, codexFinishTerminalError(finish, errors.New("Codex response contains both tool calls and a refusal"))
	}

	switch status {
	case "completed":
		if response.Error != nil || response.IncompleteDetails != nil {
			finish := Finish{Kind: FinishUnknown, RawReason: "completed_with_failure_details"}
			return finish, codexFinishTerminalError(finish, errors.New("Codex completed response contains failure details"))
		}
		rawReason := firstFinishReason(response.Status, status)
		switch {
		case sawRefusal:
			return Finish{Kind: FinishRefusal, RawReason: "refusal"}, nil
		case sawToolCall:
			return Finish{Kind: FinishToolCalls, RawReason: rawReason}, nil
		default:
			return Finish{Kind: FinishNatural, RawReason: rawReason}, nil
		}
	case "incomplete":
		rawReason := ""
		if response.IncompleteDetails != nil {
			rawReason = response.IncompleteDetails.Reason
		}
		finish := normalizeChatFinishReason(rawReason)
		if finish.RawReason == "" {
			finish.RawReason = "incomplete"
		}
		return finish, codexFinishTerminalError(finish, codexResponseError(data, eventType))
	case "failed":
		finish := codexFailedFinish(response)
		return finish, codexFinishTerminalError(finish, codexResponseError(data, eventType))
	case "cancelled":
		finish := Finish{Kind: FinishPause, RawReason: firstFinishReason(response.Status, status)}
		return finish, codexFinishTerminalError(finish, fmt.Errorf("Codex response ended with status %s", status))
	default:
		finish := Finish{Kind: FinishUnknown, RawReason: firstFinishReason(response.Status, status, eventType)}
		return finish, codexFinishTerminalError(finish, fmt.Errorf("Codex response ended with unsupported status %q", status))
	}
}

func codexFinishTerminalError(finish Finish, detail error) error {
	finishErr := FinishErrorFor(finish)
	if finishErr == nil {
		return detail
	}
	if detail == nil {
		return finishErr
	}
	return errors.Join(finishErr, detail)
}

func codexFailedFinish(response codexTerminalResponse) Finish {
	rawReason := "failed"
	if response.Error != nil {
		rawReason = firstFinishReason(response.Error.Code, rawReason)
	}
	switch codexFinishToken(rawReason) {
	case "context_length_exceeded", "context_window_exceeded", "model_context_window_exceeded", "input_too_long":
		return Finish{Kind: FinishContextLimit, RawReason: rawReason}
	case "max_output_tokens", "max_tokens", "output_limit":
		return Finish{Kind: FinishOutputLimit, RawReason: rawReason}
	case "refusal", "refused":
		return Finish{Kind: FinishRefusal, RawReason: rawReason}
	case "content_filter", "content_policy_violation", "image_content_policy_violation", "bio_policy", "cyber_policy", "prohibited_content", "safety":
		return Finish{Kind: FinishContentFilter, RawReason: rawReason}
	case "server_error", "rate_limit", "rate_limit_exceeded", "insufficient_quota", "server_overloaded", "overloaded", "resource_exhausted", "vector_store_timeout":
		return Finish{Kind: FinishResourceLimit, RawReason: rawReason}
	default:
		return Finish{Kind: FinishUnknown, RawReason: rawReason}
	}
}

func codexFinishToken(value string) string {
	value = strings.ToLower(sanitizeFinishReason(value))
	return strings.NewReplacer("-", "_", " ", "_").Replace(value)
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
