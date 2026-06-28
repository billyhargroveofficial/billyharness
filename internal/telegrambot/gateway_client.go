package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Harness interface {
	CreateSession(context.Context, string) (string, error)
	RunSession(context.Context, string, gateway.RunRequest, func(protocol.Event)) error
	CancelSession(context.Context, string) (bool, error)
	MCPStatus(context.Context) (string, error)
	ConfigStatus(context.Context) (string, error)
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
	body, err := json.Marshal(gateway.CreateSessionRequest{Profile: profile})
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
	var raw any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}
	pretty, _ := json.MarshalIndent(raw, "", "  ")
	return string(pretty), nil
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

func (c *GatewayClient) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	c.Client = &http.Client{Timeout: 0}
	return c.Client
}
