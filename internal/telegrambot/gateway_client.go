package telegrambot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Harness interface {
	CreateSession(context.Context, string) (string, error)
	ReplaySessionEvents(context.Context, string, int64, func(protocol.Event)) error
	RunSession(context.Context, string, gatewayapi.RunRequest, func(protocol.Event)) error
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
		BaseURL: gatewayclient.NormalizeBaseURL(baseURL),
		Client:  &http.Client{Timeout: 0},
	}
}

func (c *GatewayClient) CreateSession(ctx context.Context, profile string) (string, error) {
	return c.CreateSessionFromMessages(ctx, profile, nil)
}

func (c *GatewayClient) CreateSessionFromMessages(ctx context.Context, profile string, messages []protocol.Message) (string, error) {
	return c.gatewayClient().CreateSessionFromMessages(ctx, profile, messages)
}

func (c *GatewayClient) CreateSessionWithOwner(ctx context.Context, profile string, owner gatewayapi.SessionOwner) (string, error) {
	return c.gatewayClient().CreateSessionWithOwner(ctx, profile, owner)
}

func (c *GatewayClient) CreateSessionFromMessagesWithOwner(ctx context.Context, profile string, messages []protocol.Message, owner gatewayapi.SessionOwner) (string, error) {
	return c.gatewayClient().CreateSessionFromMessagesWithOwner(ctx, profile, messages, owner)
}

func (c *GatewayClient) ListSessions(ctx context.Context) ([]gatewayapi.SessionSummary, error) {
	return c.gatewayClient().ListSessions(ctx)
}

func (c *GatewayClient) GetSession(ctx context.Context, sessionID string) (gatewayapi.SessionResponse, error) {
	return c.gatewayClient().GetSession(ctx, sessionID)
}

func (c *GatewayClient) RunSession(ctx context.Context, sessionID string, run gatewayapi.RunRequest, emit func(protocol.Event)) error {
	return c.gatewayClient().RunSession(ctx, sessionID, run, emit)
}

func (c *GatewayClient) ReplaySessionEvents(ctx context.Context, sessionID string, afterSeq int64, emit func(protocol.Event)) error {
	return c.gatewayClient().ReplaySessionEvents(ctx, sessionID, afterSeq, emit)
}

func (c *GatewayClient) MCPStatus(ctx context.Context) (string, error) {
	resp, err := gatewayclient.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/mcp", nil)
		if err != nil {
			return nil, err
		}
		gatewayclient.SetAuthHeaderFromEnv(req)
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
	resp, err := gatewayclient.DoWithReadyRetry(ctx, c.client(), c.BaseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/config", nil)
		if err != nil {
			return nil, err
		}
		gatewayclient.SetAuthHeaderFromEnv(req)
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
	var out gatewayapi.SessionContextResponse
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/context"
	if err := c.gatewayJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return gatewayclient.FormatSessionContext(out), nil
}

func (c *GatewayClient) AuthStatus(ctx context.Context) (credentials.Status, error) {
	var out credentials.Status
	if err := c.gatewayJSON(ctx, http.MethodGet, "/v1/auth/status", nil, &out); err != nil {
		return credentials.Status{}, err
	}
	return out, nil
}

func (c *GatewayClient) SaveDeepSeekAPIKey(ctx context.Context, apiKey string) (credentials.ProviderStatus, error) {
	body, err := json.Marshal(gatewayapi.DeepSeekAuthRequest{APIKey: apiKey})
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
	return c.gatewayClient().CancelSession(ctx, sessionID)
}

func (c *GatewayClient) gatewayJSON(ctx context.Context, method, path string, body []byte, out any) error {
	var payload any
	if body != nil {
		payload = json.RawMessage(body)
	}
	return c.gatewayClient().JSON(ctx, method, path, payload, out)
}

func (c *GatewayClient) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	c.Client = &http.Client{Timeout: 0}
	return c.Client
}

func (c *GatewayClient) gatewayClient() *gatewayclient.Client {
	return &gatewayclient.Client{BaseURL: c.BaseURL, Client: c.client()}
}
