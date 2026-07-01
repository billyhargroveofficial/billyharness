package gatewayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	GatewayAuthTokenEnv       = "BILLYHARNESS_GATEWAY_AUTH_TOKEN"
	LegacyGatewayAuthTokenEnv = "FAST_AGENT_GATEWAY_AUTH_TOKEN"
)

var ErrSessionNotFound = errors.New("gateway session not found")

type Client struct {
	BaseURL string
	Client  *http.Client
}

type RunResult struct {
	EventCount    int
	LastSeq       int64
	Completed     bool
	Failed        bool
	Failure       string
	TerminalEvent protocol.Event
	SeqGap        *EventSeqGapError
	StreamGaps    int
	DroppedEvents int64
}

type StatusError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

type RunFailedError struct {
	Message string
	Event   protocol.Event
}

type EventSeqGapError struct {
	AfterSeq int64
	GotSeq   int64
}

type UnavailableError struct {
	BaseURL string
	Err     error
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: NormalizeBaseURL(baseURL),
		Client:  &http.Client{Timeout: 0},
	}
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("gateway %s %s HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("gateway %s %s HTTP %d: %s", e.Method, e.Path, e.StatusCode, body)
}

func (e *StatusError) Is(target error) bool {
	return target == ErrSessionNotFound && e != nil && e.StatusCode == http.StatusNotFound && strings.Contains(e.Path, "/v1/sessions/")
}

func (e *RunFailedError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return "gateway run failed"
	}
	return e.Message
}

func (e *EventSeqGapError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("gateway event sequence gap: got seq %d after %d", e.GotSeq, e.AfterSeq)
}

func (e *UnavailableError) Error() string {
	hint := UnavailableHint(e.BaseURL)
	if e.Err == nil {
		return hint
	}
	return hint + ": " + e.Err.Error()
}

func (e *UnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NormalizeBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return value
		}
		host := normalizeClientHost(parsed.Hostname())
		if port := parsed.Port(); port != "" {
			parsed.Host = net.JoinHostPort(host, port)
		} else {
			parsed.Host = host
		}
		return parsed.String()
	}
	if strings.HasPrefix(value, ":") {
		return "http://127.0.0.1" + value
	}
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		host = normalizeClientHost(host)
		return "http://" + net.JoinHostPort(host, port)
	}
	return "http://" + value
}

func AuthTokenFromEnv() string {
	for _, key := range []string{GatewayAuthTokenEnv, LegacyGatewayAuthTokenEnv} {
		if value, ok := config.LookupEnvOrDotenv(key); ok {
			if token := strings.TrimSpace(value); token != "" {
				return token
			}
		}
	}
	return ""
}

func SetAuthHeader(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	if req == nil || token == "" || req.Header.Get("Authorization") != "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

func SetAuthHeaderFromEnv(req *http.Request) {
	SetAuthHeader(req, AuthTokenFromEnv())
}

func DoWithReadyRetry(ctx context.Context, client *http.Client, baseURL string, makeRequest func() (*http.Request, error)) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := makeRequest()
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err == nil {
		return resp, nil
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, err
	}
	if !WaitForReady(ctx, baseURL, 2*time.Second) {
		return nil, &UnavailableError{BaseURL: baseURL, Err: err}
	}
	req, reqErr := makeRequest()
	if reqErr != nil {
		return nil, reqErr
	}
	return client.Do(req)
}

func WaitForReady(ctx context.Context, baseURL string, timeout time.Duration) bool {
	baseURL = NormalizeBaseURL(baseURL)
	if baseURL == "" {
		return false
	}
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 220 * time.Millisecond}
	for {
		if healthOK(ctx, &client, baseURL) {
			return true
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func UnavailableHint(baseURL string) string {
	baseURL = NormalizeBaseURL(baseURL)
	if baseURL == "" {
		baseURL = "configured gateway"
	}
	parts := []string{
		"gateway " + baseURL + " is not reachable",
		"start it with ./bin/fast-agent-harness gateway",
		"or run systemctl restart billyharness-gateway.service",
		"inspect with systemctl --no-pager --full status billyharness-gateway.service",
	}
	return strings.Join(parts, "; ")
}

func (c *Client) CreateSession(ctx context.Context, profile string) (string, error) {
	return c.CreateSessionFromMessages(ctx, profile, nil)
}

func (c *Client) CreateSessionFromMessages(ctx context.Context, profile string, messages []protocol.Message) (string, error) {
	return c.CreateSessionFromMessagesWithOwner(ctx, profile, messages, gatewayapi.SessionOwner{})
}

func (c *Client) CreateSessionWithOwner(ctx context.Context, profile string, owner gatewayapi.SessionOwner) (string, error) {
	return c.CreateSessionFromMessagesWithOwner(ctx, profile, nil, owner)
}

func (c *Client) CreateSessionFromMessagesWithOwner(ctx context.Context, profile string, messages []protocol.Message, owner gatewayapi.SessionOwner) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	req := gatewayapi.CreateSessionRequest{Profile: profile, Messages: messages, Owner: owner}
	if err := c.JSON(ctx, http.MethodPost, "/v1/sessions", req, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("gateway returned empty session id")
	}
	return out.ID, nil
}

func (c *Client) ListSessions(ctx context.Context) ([]gatewayapi.SessionSummary, error) {
	var out gatewayapi.SessionListResponse
	if err := c.JSON(ctx, http.MethodGet, "/v1/sessions", nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (gatewayapi.SessionResponse, error) {
	var out gatewayapi.SessionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID))
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return gatewayapi.SessionResponse{}, err
	}
	return out, nil
}

func (c *Client) RunSession(ctx context.Context, sessionID string, run gatewayapi.RunRequest, emit func(protocol.Event)) error {
	_, err := c.RunSessionResult(ctx, sessionID, run, emit)
	return err
}

func (c *Client) RunSessionResult(ctx context.Context, sessionID string, run gatewayapi.RunRequest, emit func(protocol.Event)) (RunResult, error) {
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/run"
	body, err := json.Marshal(run)
	if err != nil {
		return RunResult{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return RunResult{}, err
	}
	defer resp.Body.Close()
	if err := statusError(resp, http.MethodPost, path); err != nil {
		return RunResult{}, err
	}
	result, err := decodeEvents(resp.Body, 0, emit)
	if err != nil {
		return result, err
	}
	if result.Failed {
		return result, &RunFailedError{Message: result.Failure, Event: result.TerminalEvent}
	}
	return result, nil
}

func (c *Client) ReplaySessionEvents(ctx context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	return c.sessionEvents(ctx, sessionID, afterSeq, false, emit)
}

func (c *Client) FollowSessionEvents(ctx context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	return c.sessionEvents(ctx, sessionID, afterSeq, true, emit)
}

func (c *Client) AdmitSessionInput(ctx context.Context, sessionID string, input gatewayapi.SessionInputRequest) (gatewayapi.SessionInputResponse, error) {
	var out gatewayapi.SessionInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/inputs"
	if err := c.JSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	return out, nil
}

func (c *Client) sessionEvents(ctx context.Context, sessionID string, afterSeq int64, follow bool, emit func(protocol.Event)) error {
	path := fmt.Sprintf("/v1/sessions/%s/events?after_seq=%d&follow=%t", url.PathEscape(strings.TrimSpace(sessionID)), afterSeq, follow)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusError(resp, http.MethodGet, path); err != nil {
		return err
	}
	_, err = decodeEvents(resp.Body, afterSeq, emit)
	return err
}

func (c *Client) CancelSession(ctx context.Context, sessionID string) (bool, error) {
	var out gatewayapi.CancelSessionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/cancel"
	if err := c.JSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return false, err
	}
	return out.Cancelled, nil
}

func (c *Client) AnswerUserInput(ctx context.Context, sessionID, requestID string, answer gatewayapi.UserInputAnswerRequest) (gatewayapi.UserInputResponse, error) {
	var out gatewayapi.UserInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/user_input/" + url.PathEscape(strings.TrimSpace(requestID)) + "/answer"
	if err := c.JSON(ctx, http.MethodPost, path, answer, &out); err != nil {
		return gatewayapi.UserInputResponse{}, err
	}
	return out, nil
}

func (c *Client) RejectUserInput(ctx context.Context, sessionID, requestID string, reject gatewayapi.UserInputRejectRequest) (gatewayapi.UserInputResponse, error) {
	var out gatewayapi.UserInputResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/user_input/" + url.PathEscape(strings.TrimSpace(requestID)) + "/reject"
	if err := c.JSON(ctx, http.MethodPost, path, reject, &out); err != nil {
		return gatewayapi.UserInputResponse{}, err
	}
	return out, nil
}

func (c *Client) PreviewSessionUndo(ctx context.Context, sessionID, changeID string) (gatewayapi.SessionUndoResponse, error) {
	return c.UndoSession(ctx, sessionID, gatewayapi.SessionUndoRequest{ChangeID: changeID, Preview: true})
}

func (c *Client) UndoSession(ctx context.Context, sessionID string, undo gatewayapi.SessionUndoRequest) (gatewayapi.SessionUndoResponse, error) {
	var out gatewayapi.SessionUndoResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/undo"
	if err := c.JSON(ctx, http.MethodPost, path, undo, &out); err != nil {
		return gatewayapi.SessionUndoResponse{}, err
	}
	return out, nil
}

func (c *Client) RedoSession(ctx context.Context, sessionID string) (gatewayapi.SessionUndoResponse, error) {
	var out gatewayapi.SessionUndoResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/redo"
	if err := c.JSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return gatewayapi.SessionUndoResponse{}, err
	}
	return out, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	return c.do(ctx, method, path, body)
}

func (c *Client) JSON(ctx context.Context, method, path string, body any, out any) error {
	var bytesBody []byte
	var err error
	if body != nil {
		bytesBody, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	resp, err := c.do(ctx, method, path, bytesBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusError(resp, method, path); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	baseURL := NormalizeBaseURL(c.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("gateway URL is empty")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return DoWithReadyRetry(ctx, c.client(), baseURL, func() (*http.Request, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		SetAuthHeaderFromEnv(req)
		return req, nil
	})
}

func (c *Client) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	c.Client = &http.Client{Timeout: 0}
	return c.Client
}

func FormatSessionContext(resp gatewayapi.SessionContextResponse) string {
	var b strings.Builder
	if resp.ID != "" {
		fmt.Fprintf(&b, "session: %s\n", resp.ID)
	}
	fmt.Fprintf(&b, "messages: %d\n", resp.MessageCount)
	if resp.ContextWindowTokens > 0 {
		fmt.Fprintf(&b, "active context: %s / %s (%.1f%%)\n", compactContextNumber(resp.EstimatedTokens), compactContextNumber(resp.ContextWindowTokens), resp.PercentUsed)
	} else {
		fmt.Fprintf(&b, "active context: %s\n", compactContextNumber(resp.EstimatedTokens))
	}
	if resp.ContextCompactTokens > 0 {
		state := "below"
		if resp.OverCompactThreshold {
			state = "over"
		}
		fmt.Fprintf(&b, "compact threshold: %s (%.1f%%, %s)\n", compactContextNumber(resp.ContextCompactTokens), resp.CompactThresholdPercent, state)
	}
	if runtimeText := formatContextRuntime(resp.Runtime); runtimeText != "" {
		fmt.Fprintf(&b, "runtime: %s\n", runtimeText)
	}
	if resp.LastCompaction != nil {
		compaction := resp.LastCompaction
		label := firstNonEmpty(compaction.CompactionID, "unknown")
		if compaction.Seq > 0 {
			label += fmt.Sprintf(" seq=%d", compaction.Seq)
		}
		fmt.Fprintf(&b, "last compaction: %s", label)
		if compaction.Strategy != "" {
			fmt.Fprintf(&b, " strategy=%s", compaction.Strategy)
		}
		if compaction.BeforeTokens > 0 || compaction.AfterTokens > 0 {
			fmt.Fprintf(&b, " tokens %s→%s", compactContextNumber(compaction.BeforeTokens), compactContextNumber(compaction.AfterTokens))
		}
		if compaction.Reason != "" {
			fmt.Fprintf(&b, " reason=%s", compaction.Reason)
		}
		b.WriteByte('\n')
	}
	if usageText := formatContextUsage(resp.Usage); usageText != "" {
		b.WriteString(usageText)
	}
	if promptText := formatContextPrompt(resp.Prompt); promptText != "" {
		b.WriteString(promptText)
	}
	if resp.OutputRefs.Count > 0 || resp.OutputRefs.LargeInlineCount > 0 {
		fmt.Fprintf(&b, "output refs: %d", resp.OutputRefs.Count)
		if resp.OutputRefs.SourceBucketCount > 0 {
			fmt.Fprintf(&b, " buckets=%d", resp.OutputRefs.SourceBucketCount)
		}
		if resp.OutputRefs.LargeInlineCount > 0 {
			fmt.Fprintf(&b, " large_inline=%d", resp.OutputRefs.LargeInlineCount)
		}
		b.WriteByte('\n')
	}
	if len(resp.Thresholds) > 0 {
		var parts []string
		for _, threshold := range resp.Thresholds {
			marker := "○"
			if threshold.Crossed {
				marker = "●"
			}
			parts = append(parts, fmt.Sprintf("%s%d%%", marker, threshold.Percent))
		}
		fmt.Fprintf(&b, "thresholds: %s\n", strings.Join(parts, " "))
	}
	if len(resp.Sources) > 0 {
		b.WriteString("\nsources:\n")
		for _, source := range resp.Sources {
			var flags []string
			if source.LargeInlineCount > 0 {
				flags = append(flags, fmt.Sprintf("large inline %d", source.LargeInlineCount))
			}
			if source.OutputRefCount > 0 {
				flags = append(flags, fmt.Sprintf("output_ref %d", source.OutputRefCount))
			}
			flagText := ""
			if len(flags) > 0 {
				flagText = ", " + strings.Join(flags, ", ")
			}
			fmt.Fprintf(&b, "  %s: %s (%.1f%%, %d msg%s)\n", source.Source, compactContextNumber(source.EstimatedTokens), source.Percent, source.MessageCount, flagText)
		}
	}
	if len(resp.TopContributors) > 0 {
		b.WriteString("\ntop contributors:\n")
		for _, contributor := range resp.TopContributors {
			name := contributor.Source
			if contributor.Name != "" {
				name += "/" + contributor.Name
			}
			preview := contributor.Preview
			if preview == "" {
				preview = "(no text)"
			}
			var flags []string
			if contributor.LargeInline {
				if contributor.InlineBudgetBytes > 0 {
					flags = append(flags, "large inline>"+compactByteNumber(int64(contributor.InlineBudgetBytes)))
				} else {
					flags = append(flags, "large inline")
				}
			}
			if contributor.HasOutputRef {
				flags = append(flags, "output_ref")
			}
			flagText := ""
			if len(flags) > 0 {
				flagText = " [" + strings.Join(flags, ", ") + "]"
			}
			fmt.Fprintf(&b, "  #%d %s %s: %s%s - %s\n", contributor.Index, contributor.Role, name, compactContextNumber(contributor.EstimatedTokens), flagText, preview)
		}
	}
	if len(resp.Warnings) > 0 {
		b.WriteString("\nwarnings:\n")
		for _, warning := range resp.Warnings {
			if strings.TrimSpace(warning) != "" {
				fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(warning))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatContextRuntime(runtime gatewayapi.ContextRuntime) string {
	var parts []string
	if runtime.Model != "" {
		parts = append(parts, "model="+runtime.Model)
	}
	if runtime.Provider != "" {
		parts = append(parts, "provider="+runtime.Provider)
	}
	if runtime.ReasoningMode != "" {
		parts = append(parts, "reasoning="+runtime.ReasoningMode)
	}
	if runtime.AccessMode != "" {
		parts = append(parts, "access="+runtime.AccessMode)
	}
	if runtime.Profile != "" {
		parts = append(parts, "profile="+runtime.Profile)
	}
	return strings.Join(parts, " ")
}

func formatContextUsage(usage gatewayapi.ContextUsage) string {
	var b strings.Builder
	if usage.ModelCalls > 0 || usage.ToolCalls > 0 {
		fmt.Fprintf(&b, "activity: model_calls=%d tools=%d\n", usage.ModelCalls, usage.ToolCalls)
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningTokens > 0 {
		fmt.Fprintf(&b, "usage: input=%s output=%s reasoning=%s\n", compactContextNumber(usage.InputTokens), compactContextNumber(usage.OutputTokens), compactContextNumber(usage.ReasoningTokens))
	}
	if usage.CacheHitTokens > 0 || usage.CacheMissTokens > 0 || usage.LastCacheHitTokens > 0 || usage.LastCacheMissTokens > 0 {
		fmt.Fprintf(&b, "cache: hit=%s miss=%s last_hit=%s last_miss=%s\n",
			compactContextNumber(usage.CacheHitTokens),
			compactContextNumber(usage.CacheMissTokens),
			compactContextNumber(usage.LastCacheHitTokens),
			compactContextNumber(usage.LastCacheMissTokens),
		)
	}
	if usage.WebSummaryInputTokens > 0 || usage.WebSummaryOutputTokens > 0 || usage.HelperModelAPITokens > 0 || usage.HelperModelInputTokens > 0 || usage.HelperModelOutputTokens > 0 || usage.HelperModelCacheHit > 0 || usage.HelperModelCacheMiss > 0 {
		fmt.Fprintf(&b, "helper usage: websum=%s→%s helper=%s→%s helper_api=%s",
			compactContextNumber(usage.WebSummaryInputTokens),
			compactContextNumber(usage.WebSummaryOutputTokens),
			compactContextNumber(usage.HelperModelInputTokens),
			compactContextNumber(usage.HelperModelOutputTokens),
			compactContextNumber(usage.HelperModelAPITokens),
		)
		if usage.HelperModelCacheHit > 0 || usage.HelperModelCacheMiss > 0 {
			fmt.Fprintf(&b, " helper_cache_hit=%s helper_cache_miss=%s",
				compactContextNumber(usage.HelperModelCacheHit),
				compactContextNumber(usage.HelperModelCacheMiss),
			)
		}
		if usage.HelperModelCalls > 0 {
			fmt.Fprintf(&b, " helper_calls=%d", usage.HelperModelCalls)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatContextPrompt(prompt gatewayapi.ContextPrompt) string {
	if contextPromptEmpty(prompt) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "prompt sections: %d sections %s tokens %s bytes", prompt.SectionCount, compactContextNumber(int64(prompt.ApproxTokens)), compactByteNumber(int64(prompt.TotalBytes)))
	if prompt.ToolSchemas > 0 {
		fmt.Fprintf(&b, " tool_schemas=%d", prompt.ToolSchemas)
	}
	if prompt.InventoryHash != "" {
		fmt.Fprintf(&b, " hash=%s", shortContextHash(prompt.InventoryHash))
	}
	b.WriteByte('\n')
	if prompt.CacheStatus != "" || prompt.CacheReason != "" {
		fmt.Fprintf(&b, "prompt cache: status=%s reason=%s\n", firstNonEmpty(prompt.CacheStatus, "unknown"), firstNonEmpty(prompt.CacheReason, "unknown"))
	}
	if len(prompt.Sections) > 0 {
		b.WriteString("prompt section budget:\n")
		limit := len(prompt.Sections)
		if limit > 6 {
			limit = 6
		}
		for _, section := range prompt.Sections[:limit] {
			fmt.Fprintf(&b, "  %s: %s tokens %s bytes hash=%s\n",
				section.Name,
				compactContextNumber(int64(section.ApproxTokens)),
				compactByteNumber(int64(section.ByteCount)),
				shortContextHash(section.SHA256),
			)
		}
		if len(prompt.Sections) > limit {
			fmt.Fprintf(&b, "  ... +%d sections\n", len(prompt.Sections)-limit)
		}
	}
	return b.String()
}

func contextPromptEmpty(prompt gatewayapi.ContextPrompt) bool {
	return prompt.InventoryHash == "" &&
		prompt.SectionCount == 0 &&
		prompt.TotalBytes == 0 &&
		prompt.ApproxTokens == 0 &&
		prompt.ToolSchemas == 0 &&
		len(prompt.Sections) == 0 &&
		prompt.CacheStatus == "" &&
		prompt.CacheReason == ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func shortContextHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func statusError(resp *http.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &StatusError{
		Method:     method,
		Path:       path,
		StatusCode: resp.StatusCode,
		Body:       string(limited),
	}
}

func decodeEvents(reader io.Reader, cursor int64, emit func(protocol.Event)) (RunResult, error) {
	var result RunResult
	dec := json.NewDecoder(reader)
	for {
		var event protocol.Event
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				return result, nil
			}
			return result, err
		}
		if event.Seq > 0 && event.Seq <= cursor {
			continue
		}
		if event.Seq > 0 && cursor > 0 && event.Seq > cursor+1 {
			result.SeqGap = &EventSeqGapError{AfterSeq: cursor, GotSeq: event.Seq}
			return result, result.SeqGap
		}
		if event.Seq > 0 {
			cursor = event.Seq
			result.LastSeq = event.Seq
		}
		result.EventCount++
		if event.Type == protocol.EventGatewayStreamGap {
			result.StreamGaps++
			result.DroppedEvents += streamGapDroppedEvents(event.Data)
		}
		switch event.Type {
		case protocol.EventRunCompleted:
			result.Completed = true
			result.Failed = false
			result.Failure = ""
			result.TerminalEvent = event
		case protocol.EventRunFailed:
			result.Completed = false
			result.Failed = true
			result.Failure = fmt.Sprint(event.Data)
			result.TerminalEvent = event
		}
		if emit != nil {
			emit(event)
		}
	}
}

func streamGapDroppedEvents(data any) int64 {
	switch value := data.(type) {
	case protocol.GatewayStreamGapEvent:
		return value.DroppedEvents
	case map[string]any:
		switch raw := value["dropped_events"].(type) {
		case float64:
			return int64(raw)
		case int64:
			return raw
		case json.Number:
			n, _ := raw.Int64()
			return n
		}
	}
	return 0
}

func healthOK(ctx context.Context, client *http.Client, baseURL string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 260*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func normalizeClientHost(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
}

func compactContextNumber(value int64) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(value)/1_000_000)
	case abs >= 10_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return strconv.FormatInt(value, 10)
	}
}

func compactByteNumber(value int64) string {
	return compactContextNumber(value) + "B"
}
