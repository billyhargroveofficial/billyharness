package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Harness interface {
	CreateSession(context.Context, string) (string, error)
	ReplaySessionEvents(context.Context, string, int64, func(protocol.Event)) error
	RunSession(context.Context, string, gateway.RunRequest, func(protocol.Event)) error
	CancelSession(context.Context, string) (bool, error)
	MCPStatus(context.Context) (string, error)
	ConfigStatus(context.Context) (string, error)
	ContextStatus(context.Context, string) (string, error)
	AuthStatus(context.Context) (credentials.Status, error)
	SaveDeepSeekAPIKey(context.Context, string) (credentials.ProviderStatus, error)
	ImportCodexAuth(context.Context) (credentials.ProviderStatus, error)
}

type GatewayClient struct {
	BaseURL string
	Client  *http.Client
}

func NewGatewayClient(baseURL string) *GatewayClient {
	return &GatewayClient{
		BaseURL: gateway.NormalizeBaseURL(baseURL),
		Client:  &http.Client{Timeout: 0},
	}
}

func (c *GatewayClient) CreateSession(ctx context.Context, profile string) (string, error) {
	return c.CreateSessionFromMessages(ctx, profile, nil)
}

func (c *GatewayClient) CreateSessionFromMessages(ctx context.Context, profile string, messages []protocol.Message) (string, error) {
	body, err := json.Marshal(gateway.CreateSessionRequest{Profile: profile, Messages: messages})
	if err != nil {
		return "", err
	}
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/sessions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("gateway create session HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("gateway returned empty session id")
	}
	return out.ID, nil
}

func (c *GatewayClient) ListSessions(ctx context.Context) ([]gateway.SessionSummary, error) {
	var out gateway.SessionListResponse
	if err := c.gatewayJSON(ctx, http.MethodGet, "/v1/sessions", nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (c *GatewayClient) GetSession(ctx context.Context, sessionID string) (gateway.SessionResponse, error) {
	var out gateway.SessionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID))
	if err := c.gatewayJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return gateway.SessionResponse{}, err
	}
	return out, nil
}

func (c *GatewayClient) RunSession(ctx context.Context, sessionID string, run gateway.RunRequest, emit func(protocol.Event)) error {
	body, err := json.Marshal(run)
	if err != nil {
		return err
	}
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/sessions/"+sessionID+"/run", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway run HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var event protocol.Event
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		emit(event)
	}
}

func (c *GatewayClient) ReplaySessionEvents(ctx context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	path := fmt.Sprintf("/v1/sessions/%s/events?after_seq=%d&follow=false", url.PathEscape(sessionID), afterSeq)
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway events HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var event protocol.Event
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		emit(event)
	}
}

func (c *GatewayClient) MCPStatus(ctx context.Context) (string, error) {
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/mcp", nil)
		if err != nil {
			return nil, err
		}
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("gateway mcp HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	var out mcpstatus.Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return mcpstatus.Format(out), nil
}

func (c *GatewayClient) ConfigStatus(ctx context.Context) (string, error) {
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/config", nil)
		if err != nil {
			return nil, err
		}
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("gateway config HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	var out struct {
		Values   []config.ResolvedValue `json:"values"`
		Warnings []string               `json:"warnings,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return config.FormatSummary(out.Values, out.Warnings), nil
}

func (c *GatewayClient) ContextStatus(ctx context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("empty session id")
	}
	var out gateway.SessionContextResponse
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/context"
	if err := c.gatewayJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return gateway.FormatSessionContext(out), nil
}

func (c *GatewayClient) AuthStatus(ctx context.Context) (credentials.Status, error) {
	var out credentials.Status
	if err := c.gatewayJSON(ctx, http.MethodGet, "/v1/auth/status", nil, &out); err != nil {
		return credentials.Status{}, err
	}
	return out, nil
}

func (c *GatewayClient) SaveDeepSeekAPIKey(ctx context.Context, apiKey string) (credentials.ProviderStatus, error) {
	body, err := json.Marshal(gateway.DeepSeekAuthRequest{APIKey: apiKey})
	if err != nil {
		return credentials.ProviderStatus{}, err
	}
	var out struct {
		DeepSeek credentials.ProviderStatus `json:"deepseek"`
	}
	if err := c.gatewayJSON(ctx, http.MethodPost, "/v1/auth/deepseek", body, &out); err != nil {
		return credentials.ProviderStatus{}, err
	}
	return out.DeepSeek, nil
}

func (c *GatewayClient) ImportCodexAuth(ctx context.Context) (credentials.ProviderStatus, error) {
	var out struct {
		Codex credentials.ProviderStatus `json:"codex"`
	}
	if err := c.gatewayJSON(ctx, http.MethodPost, "/v1/auth/codex/import", []byte(`{}`), &out); err != nil {
		return credentials.ProviderStatus{}, err
	}
	return out.Codex, nil
}

func (c *GatewayClient) CancelSession(ctx context.Context, sessionID string) (bool, error) {
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/sessions/"+sessionID+"/cancel", nil)
		if err != nil {
			return nil, err
		}
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("gateway cancel HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	var out struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Cancelled, nil
}

func (c *GatewayClient) gatewayJSON(ctx context.Context, method, path string, body []byte, out any) error {
	resp, err := gateway.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway %s %s HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *GatewayClient) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	c.Client = &http.Client{Timeout: 0}
	return c.Client
}
