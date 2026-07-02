package gateway

import (
	"net"
	"net/http"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewaybase"
)

const (
	GatewayAuthTokenEnv       = gatewaybase.GatewayAuthTokenEnv
	LegacyGatewayAuthTokenEnv = gatewaybase.LegacyGatewayAuthTokenEnv
)

func NormalizeBaseURL(value string) string {
	return gatewaybase.NormalizeBaseURL(value)
}

func AuthTokenFromEnv() string {
	return gatewaybase.AuthTokenFromEnv()
}

func SetAuthHeader(req *http.Request, token string) {
	gatewaybase.SetAuthHeader(req, token)
}

func SetAuthHeaderFromEnv(req *http.Request) {
	gatewaybase.SetAuthHeaderFromEnv(req)
}

func RequiresAuthForAddr(addr string) bool {
	host := addrHost(addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	return !isLoopbackHost(host)
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
