package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type BotError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e BotError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram HTTP %d: %s; retry after %s", e.Code, e.Description, e.RetryAfter)
	}
	return fmt.Sprintf("telegram HTTP %d: %s", e.Code, e.Description)
}

type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	minInterval time.Duration

	mu           sync.Mutex
	lastCall     map[int64]time.Time
	backoffUntil map[int64]time.Time
}

type ClientOptions struct {
	BaseURL     string
	Token       string
	HTTPClient  *http.Client
	MinInterval time.Duration
}

func NewClient(opts ClientOptions) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 70 * time.Second}
	}
	minInterval := opts.MinInterval
	if minInterval <= 0 {
		minInterval = 1100 * time.Millisecond
	}
	return &Client{
		baseURL:      baseURL,
		token:        strings.TrimSpace(opts.Token),
		httpClient:   client,
		minInterval:  minInterval,
		lastCall:     map[int64]time.Time{},
		backoffUntil: map[int64]time.Time{},
	}
}

func (c *Client) DeleteWebhook(ctx context.Context) error {
	var out bool
	return c.post(ctx, 0, "deleteWebhook", map[string]any{"drop_pending_updates": false}, &out)
}

func (c *Client) GetUpdates(ctx context.Context, offset, timeoutSec int) ([]Update, error) {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	var updates []Update
	err := c.post(ctx, 0, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message"},
	}, &updates)
	return updates, err
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text, parseMode string, threadID int) (SentMessage, error) {
	var msg SentMessage
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	if threadID > 0 {
		payload["message_thread_id"] = threadID
	}
	err := c.postWithRetry(ctx, chatID, "sendMessage", payload, &msg)
	if err == nil || parseMode == "" || !isTelegramParseError(err) {
		return msg, err
	}
	delete(payload, "parse_mode")
	payload["text"] = fallbackText(text, parseMode)
	err = c.postWithRetry(ctx, chatID, "sendMessage", payload, &msg)
	return msg, err
}

func (c *Client) SendRichMessageMarkdown(ctx context.Context, chatID int64, markdown string, threadID int) (SentMessage, error) {
	var msg SentMessage
	payload := map[string]any{
		"chat_id": chatID,
		"rich_message": InputRichMessage{
			Markdown: markdown,
		},
	}
	if threadID > 0 {
		payload["message_thread_id"] = threadID
	}
	err := c.postWithRetry(ctx, chatID, "sendRichMessage", payload, &msg)
	return msg, err
}

func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text, parseMode string) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	var out json.RawMessage
	err := c.postWithRetry(ctx, chatID, "editMessageText", payload, &out)
	if err == nil || parseMode == "" || !isTelegramParseError(err) {
		return err
	}
	delete(payload, "parse_mode")
	payload["text"] = fallbackText(text, parseMode)
	return c.postWithRetry(ctx, chatID, "editMessageText", payload, &out)
}

func (c *Client) EditMessageRichMarkdown(ctx context.Context, chatID int64, messageID int, markdown string) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"rich_message": InputRichMessage{
			Markdown: markdown,
		},
	}
	var out json.RawMessage
	return c.postWithRetry(ctx, chatID, "editMessageText", payload, &out)
}

func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int) error {
	var out bool
	return c.postWithRetry(ctx, chatID, "deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	}, &out)
}

func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if action == "" {
		action = "typing"
	}
	var out bool
	return c.post(ctx, chatID, "sendChatAction", map[string]any{
		"chat_id": chatID,
		"action":  action,
	}, &out)
}

func (c *Client) postWithRetry(ctx context.Context, chatID int64, method string, payload any, out any) error {
	err := c.post(ctx, chatID, method, payload, out)
	var botErr BotError
	if !asBotError(err, &botErr) || botErr.RetryAfter <= 0 {
		return err
	}
	c.setBackoff(chatID, botErr.RetryAfter+250*time.Millisecond)
	timer := time.NewTimer(botErr.RetryAfter + 250*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return c.post(ctx, chatID, method, payload, out)
	}
}

func (c *Client) post(ctx context.Context, chatID int64, method string, payload any, out any) error {
	if c.token == "" {
		return fmt.Errorf("telegram bot token is empty")
	}
	if chatID != 0 {
		if err := c.waitRate(ctx, chatID); err != nil {
			return err
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return redactTelegramError(err, c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return redactTelegramError(err, c.token)
	}
	defer resp.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope botAPIResponse[json.RawMessage]
	if err := json.Unmarshal(limited, &envelope); err != nil {
		return fmt.Errorf("telegram %s invalid JSON: %w", method, err)
	}
	if !envelope.OK || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BotError{
			Code:        firstNonZero(envelope.ErrorCode, resp.StatusCode),
			Description: envelope.Description,
			RetryAfter:  time.Duration(envelope.Parameters.RetryAfter) * time.Second,
		}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

func (c *Client) waitRate(ctx context.Context, chatID int64) error {
	now := time.Now()
	c.mu.Lock()
	scheduled := now
	if last, ok := c.lastCall[chatID]; ok {
		if next := last.Add(c.minInterval); next.After(scheduled) {
			scheduled = next
		}
	}
	if until, ok := c.backoffUntil[chatID]; ok {
		if until.After(scheduled) {
			scheduled = until
		}
		if !until.After(now) {
			delete(c.backoffUntil, chatID)
		}
	}
	c.lastCall[chatID] = scheduled
	c.mu.Unlock()
	wait := time.Until(scheduled)
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			c.releaseRateReservation(chatID, scheduled)
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (c *Client) releaseRateReservation(chatID int64, scheduled time.Time) {
	if chatID == 0 {
		return
	}
	c.mu.Lock()
	if c.lastCall[chatID].Equal(scheduled) {
		delete(c.lastCall, chatID)
	}
	c.mu.Unlock()
}

func (c *Client) setBackoff(chatID int64, interval time.Duration) {
	if chatID == 0 || interval <= 0 {
		return
	}
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	until := time.Now().Add(interval)
	c.mu.Lock()
	if until.After(c.backoffUntil[chatID]) {
		c.backoffUntil[chatID] = until
	}
	c.mu.Unlock()
}

func isTelegramParseError(err error) bool {
	var botErr BotError
	if !asBotError(err, &botErr) || botErr.Code != http.StatusBadRequest {
		return false
	}
	desc := strings.ToLower(botErr.Description)
	return strings.Contains(desc, "parse") || strings.Contains(desc, "entit")
}

func redactTelegramError(err error, token string) error {
	if err == nil {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return err
	}
	text := strings.ReplaceAll(err.Error(), token, "<redacted>")
	if text == err.Error() {
		return err
	}
	return redactedError{text: text, err: err}
}

type redactedError struct {
	text string
	err  error
}

func (e redactedError) Error() string {
	return e.text
}

func (e redactedError) Unwrap() error {
	return e.err
}

func asBotError(err error, target *BotError) bool {
	if err == nil {
		return false
	}
	if value, ok := err.(BotError); ok {
		*target = value
		return true
	}
	if value, ok := err.(*BotError); ok {
		*target = *value
		return true
	}
	return false
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func fallbackText(text, parseMode string) string {
	if !strings.EqualFold(parseMode, "HTML") {
		return text
	}
	var out strings.Builder
	inTag := false
	for _, r := range text {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return html.UnescapeString(out.String())
}
