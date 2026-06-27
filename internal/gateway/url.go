package gateway

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
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

func RequiresAuthForAddr(addr string) bool {
	host := addrHost(addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	return !isLoopbackHost(host)
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

func addrHost(addr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err == nil {
		return strings.Trim(strings.TrimSpace(host), "[]")
	}
	host = strings.Trim(strings.TrimSpace(addr), "[]")
	if strings.HasPrefix(host, ":") {
		return ""
	}
	return host
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	return isLoopbackHost(addrHost(remoteAddr))
}
