package agentclub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type ClientOptions struct {
	GatewayURL  string
	BearerToken string
	Owner       Owner
	HTTPClient  *http.Client
}

type Client struct {
	gatewayURL  string
	bearerToken string
	owner       Owner
	httpClient  *http.Client
}

type StatusError struct {
	Method         string
	Path           string
	StatusCode     int
	BodySHA256     string
	ResponseBytes  int64
	RedactedReason string
}

func NewClient(opts ClientOptions) *Client {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		gatewayURL:  strings.TrimSpace(opts.GatewayURL),
		bearerToken: strings.TrimSpace(opts.BearerToken),
		owner:       normalizeOwner(opts.Owner),
		httpClient:  httpClient,
	}
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.BodySHA256 != "" {
		return fmt.Sprintf("agentclub gateway %s %s HTTP %d body_sha256=%s", e.Method, e.Path, e.StatusCode, shortHash(e.BodySHA256))
	}
	return fmt.Sprintf("agentclub gateway %s %s HTTP %d", e.Method, e.Path, e.StatusCode)
}

func (c *Client) PostEvent(ctx context.Context, sessionID string, event EventRequest) (EventAdmissionResponse, error) {
	normalized, err := NormalizeEventRequest(event)
	if err != nil {
		return EventAdmissionResponse{}, err
	}
	var out EventAdmissionResponse
	path := "/v1/sessions/" + url.PathEscape(strings.TrimSpace(sessionID)) + "/agentclub/events"
	if err := c.json(ctx, http.MethodPost, path, normalized, nil, &out); err != nil {
		return EventAdmissionResponse{}, err
	}
	return out, nil
}

func (c *Client) DeliverTrigger(ctx context.Context, triggerID string, delivery TriggerDelivery) (TriggerDeliveryResponse, error) {
	var out TriggerDeliveryResponse
	path := "/v1/agentclub/triggers/" + url.PathEscape(strings.TrimSpace(triggerID)) + "/deliveries"
	if err := c.doJSONBody(ctx, http.MethodPost, path, delivery.Body, delivery.Headers, &out); err != nil {
		return TriggerDeliveryResponse{}, err
	}
	return out, nil
}

func (c *Client) Capabilities(ctx context.Context) (CapabilityListResponse, error) {
	var out CapabilityListResponse
	if err := c.json(ctx, http.MethodGet, "/v1/agentclub/capabilities", nil, nil, &out); err != nil {
		return CapabilityListResponse{}, err
	}
	return out, nil
}

func (c *Client) json(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	return c.doJSONBody(ctx, method, path, raw, headers, out)
}

func (c *Client) doJSONBody(ctx context.Context, method, path string, body []byte, headers map[string]string, out any) error {
	resp, err := c.do(ctx, method, path, body, headers)
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

func (c *Client) do(ctx context.Context, method, path string, body []byte, headers map[string]string) (*http.Response, error) {
	baseURL, err := normalizeGatewayURL(c.gatewayURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("agentclub request build failed")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
	setOwnerHeaders(req, c.owner)
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentclub request failed")
	}
	return resp, nil
}

func normalizeGatewayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("agentclub gateway URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("agentclub gateway URL is invalid")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("agentclub gateway URL must not include credentials")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("agentclub gateway URL must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("agentclub gateway URL host is required")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func statusError(resp *http.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	status := &StatusError{
		Method:         method,
		Path:           path,
		StatusCode:     resp.StatusCode,
		ResponseBytes:  int64(len(limited)),
		RedactedReason: "response body intentionally omitted",
	}
	if len(limited) > 0 {
		sum := sha256.Sum256(limited)
		status.BodySHA256 = hex.EncodeToString(sum[:])
	}
	return status
}

func setOwnerHeaders(req *http.Request, owner Owner) {
	owner = normalizeOwner(owner)
	if owner.ClientID != "" {
		req.Header.Set(HeaderSessionClientID, owner.ClientID)
	}
	if owner.ClientType != "" {
		req.Header.Set(HeaderSessionClientType, owner.ClientType)
	}
	if owner.TelegramChatID != 0 {
		req.Header.Set(HeaderSessionTelegramChatID, fmt.Sprintf("%d", owner.TelegramChatID))
	}
	if owner.TelegramThreadID != 0 {
		req.Header.Set(HeaderSessionTelegramThreadID, fmt.Sprintf("%d", owner.TelegramThreadID))
	}
	if owner.TelegramUserID != 0 {
		req.Header.Set(HeaderSessionTelegramUserID, fmt.Sprintf("%d", owner.TelegramUserID))
	}
	if owner.TUIChatID != "" {
		req.Header.Set(HeaderSessionTUIChatID, owner.TUIChatID)
	}
}

func normalizeOwner(owner Owner) Owner {
	owner.ClientID = strings.TrimSpace(owner.ClientID)
	owner.ClientType = strings.ToLower(strings.TrimSpace(owner.ClientType))
	return owner
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
