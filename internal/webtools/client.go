package webtools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout      = 30 * time.Second
	defaultMaxRedirects = 5
	defaultUserAgent    = "fast-agent-harness-go/0.1 (+https://localhost)"
)

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type ResolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f ResolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type Client struct {
	Resolver     Resolver
	DialContext  DialContextFunc
	Timeout      time.Duration
	MaxRedirects int
	UserAgent    string
}

type Response struct {
	Body        []byte
	URL         string
	ContentType string
	StatusCode  int
}

func DefaultClient() Client {
	return Client{}
}

func (c Client) Get(ctx context.Context, rawURL string, maxBytes int) (Response, error) {
	u, err := c.validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return Response{}, err
	}
	httpClient := &http.Client{
		Timeout: c.timeout(),
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           c.publicDialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= c.maxRedirects() {
				return fmt.Errorf("too many redirects")
			}
			_, err := c.validatePublicHTTPURL(req.Context(), req.URL.String())
			return err
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Accept", "text/html,text/plain,application/json,application/xml;q=0.9,*/*;q=0.1")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Response{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(limited), 1000))
	}
	if maxBytes <= 0 {
		maxBytes = 1
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return Response{}, err
	}
	return Response{
		Body:        body,
		URL:         resp.Request.URL.String(),
		ContentType: resp.Header.Get("Content-Type"),
		StatusCode:  resp.StatusCode,
	}, nil
}

func ValidatePublicHTTPURL(ctx context.Context, rawURL string, resolver Resolver) (*url.URL, error) {
	return Client{Resolver: resolver}.validatePublicHTTPURL(ctx, rawURL)
}

func (c Client) validatePublicHTTPURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL host required")
	}
	if err := c.validatePublicHost(ctx, host); err != nil {
		return nil, err
	}
	return u, nil
}

func (c Client) validatePublicHost(ctx context.Context, host string) error {
	host = normalizeHost(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("refusing localhost URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !IsPublicIP(ip) {
			return fmt.Errorf("refusing non-public IP %s", ip)
		}
		return nil
	}
	addrs, err := c.lookup(ctx, host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("host resolved to no addresses")
	}
	for _, addr := range addrs {
		if !IsPublicIP(addr.IP) {
			return fmt.Errorf("refusing host %s resolved to non-public IP %s", host, addr.IP)
		}
	}
	return nil
}

func (c Client) publicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host = normalizeHost(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, fmt.Errorf("refusing localhost URL")
	}
	dial := c.dialContext()
	if ip := net.ParseIP(host); ip != nil {
		if !IsPublicIP(ip) {
			return nil, fmt.Errorf("refusing non-public IP %s", ip)
		}
		return dial(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	addrs, err := c.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("host resolved to no addresses")
	}
	var lastErr error
	for _, addr := range addrs {
		if !IsPublicIP(addr.IP) {
			return nil, fmt.Errorf("refusing host %s resolved to non-public IP %s", host, addr.IP)
		}
		conn, err := dial(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("host resolved to no dialable addresses")
}

func (c Client) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	resolver := c.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return resolver.LookupIPAddr(lookupCtx, host)
}

func (c Client) dialContext() DialContextFunc {
	if c.DialContext != nil {
		return c.DialContext
	}
	dialer := &net.Dialer{}
	return dialer.DialContext
}

func (c Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return defaultTimeout
}

func (c Client) maxRedirects() int {
	if c.MaxRedirects > 0 {
		return c.MaxRedirects
	}
	return defaultMaxRedirects
}

func (c Client) userAgent() string {
	if strings.TrimSpace(c.UserAgent) != "" {
		return c.UserAgent
	}
	return defaultUserAgent
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func IsPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
