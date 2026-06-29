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
			fmt.Fprintf(&b, "  %s: %s (%.1f%%, %d msg)\n", source.Source, compactContextNumber(source.EstimatedTokens), source.Percent, source.MessageCount)
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
			fmt.Fprintf(&b, "  #%d %s %s: %s - %s\n", contributor.Index, contributor.Role, name, compactContextNumber(contributor.EstimatedTokens), preview)
		}
	}
	return strings.TrimRight(b.String(), "\n")
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
		if event.Seq > 0 {
			cursor = event.Seq
			result.LastSeq = event.Seq
		}
		result.EventCount++
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
