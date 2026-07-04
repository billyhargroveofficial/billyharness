package gatewaybase

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/serviceops"
)

const (
	GatewayAuthTokenEnv       = "BILLYHARNESS_GATEWAY_AUTH_TOKEN"
	LegacyGatewayAuthTokenEnv = "FAST_AGENT_GATEWAY_AUTH_TOKEN"
)

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

func UnavailableHint(baseURL string) string {
	baseURL = NormalizeBaseURL(baseURL)
	if baseURL == "" {
		baseURL = "configured gateway"
	}
	parts := []string{
		"gateway " + baseURL + " is not reachable",
		"start it with ./bin/fast-agent-harness gateway",
		"or run systemctl restart " + serviceops.GatewayServiceName,
		"inspect with systemctl --no-pager --full status " + serviceops.GatewayServiceName,
	}
	return strings.Join(parts, "; ")
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
