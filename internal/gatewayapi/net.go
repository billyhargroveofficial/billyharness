package gatewayapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayauth"
	"github.com/billyhargroveofficial/billyharness/internal/serviceops"
)

const (
	GatewayAuthTokenEnv       = gatewayauth.PrimaryEnv
	LegacyGatewayAuthTokenEnv = gatewayauth.LegacyEnv
)

type UnavailableError struct {
	BaseURL string
	Err     error
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
	token, _ := ResolveAuthToken()
	return token
}

// ResolveAuthToken resolves the shared gateway transport credential. The old
// AuthTokenFromEnv name remains for compatibility, but resolution also includes
// the dedicated Billyharness home token file and bounded migration fallbacks.
func ResolveAuthToken() (string, error) {
	result, err := gatewayauth.Resolve()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Value), nil
}

func SetAuthHeader(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	if req == nil || token == "" || req.Header.Get("Authorization") != "" {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

func SetAuthHeaderFromEnv(req *http.Request) {
	_ = SetAuthHeaderFromDefault(req)
}

// SetAuthHeaderFromDefault attaches the shared gateway token without forwarding
// a local managed credential to an arbitrary remote host. Loopback requests may
// use the dedicated store and its bounded home-dotenv migration fallbacks;
// non-loopback requests only accept explicit process environment credentials.
func SetAuthHeaderFromDefault(req *http.Request) error {
	if req == nil || req.Header.Get("Authorization") != "" {
		return nil
	}

	var (
		token string
		err   error
	)
	if requestURLIsLoopback(req) {
		token, err = ResolveAuthToken()
	} else {
		token, err = resolveProcessAuthToken()
	}
	if err != nil {
		return err
	}
	SetAuthHeader(req, token)
	return nil
}

func resolveProcessAuthToken() (string, error) {
	for _, name := range []string{GatewayAuthTokenEnv, LegacyGatewayAuthTokenEnv} {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		token, err := gatewayauth.ValidateToken(raw)
		if err != nil {
			return "", fmt.Errorf("invalid gateway bearer token from %s: %w", name, err)
		}
		return token, nil
	}
	return "", nil
}

func requestURLIsLoopback(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	host := strings.TrimSuffix(strings.TrimSpace(req.URL.Hostname()), ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	if !isConnectionRefused(err) {
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

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
