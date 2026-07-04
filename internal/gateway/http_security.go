package gateway

import (
	"context"
	"crypto/subtle"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

type httpSecurityOptions struct {
	requireMutationAuth                      bool
	devAllowUnauthenticatedLoopbackMutations bool
}

type mutationAuthInfo struct {
	bearer    bool
	devBypass bool
}

type mutationAuthContextKey struct{}

type httpSecurityError struct {
	status int
	reason string
}

func (e httpSecurityError) Error() string {
	return e.reason
}

func (s *Server) httpSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		v1Route := isGatewayV1Route(r)
		if v1Route {
			if err := s.validateBrowserReachableV1Request(r); err != nil {
				s.writeSecurityDenial(w, r, err.status, err.reason)
				return
			}
		}
		mutation := isGatewayMutation(r)
		if mutation {
			if s.httpSecurity.requireMutationAuth {
				if err := validateJSONMutationContentType(r); err != nil {
					s.writeSecurityDenial(w, r, err.status, err.reason)
					return
				}
				if bearerTokenMatches(r.Header.Get("Authorization"), s.authToken) {
					ctx := context.WithValue(r.Context(), mutationAuthContextKey{}, mutationAuthInfo{bearer: true})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if s.httpSecurity.devAllowUnauthenticatedLoopbackMutations && isLoopbackRemoteAddr(r.RemoteAddr) {
					ctx := context.WithValue(r.Context(), mutationAuthContextKey{}, mutationAuthInfo{devBypass: true})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if strings.TrimSpace(s.authToken) == "" {
					s.writeSecurityDenial(w, r, http.StatusServiceUnavailable, "gateway bearer token is not configured for mutating requests")
					return
				}
				s.writeUnauthorized(w, r)
				return
			}
		}
		if v1Route && s.authToken != "" && !bearerTokenMatches(r.Header.Get("Authorization"), s.authToken) {
			s.writeUnauthorized(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isGatewayV1Route(r *http.Request) bool {
	return r != nil && strings.HasPrefix(r.URL.Path, "/v1/")
}

func isGatewayMutation(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return strings.HasPrefix(r.URL.Path, "/v1/")
	}
}

func (s *Server) validateBrowserReachableV1Request(r *http.Request) *httpSecurityError {
	if isLoopbackRemoteAddr(r.RemoteAddr) && !s.isAllowedLoopbackHost(r.Host) {
		return &httpSecurityError{
			status: http.StatusForbidden,
			reason: "gateway host is not allowed for loopback request",
		}
	}
	if err := validateSameOriginHeader(r, "Origin"); err != nil {
		return err
	}
	if r.Header.Get("Origin") == "" {
		if err := validateSameOriginHeader(r, "Referer"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) isAllowedLoopbackHost(host string) bool {
	host = addrHost(host)
	if host == "" {
		return false
	}
	if isLoopbackHost(host) {
		return true
	}
	gatewayHost := addrHost(s.gatewayAddr)
	return gatewayHost != "" && strings.EqualFold(host, gatewayHost) && isLoopbackHost(gatewayHost)
}

func validateSameOriginHeader(r *http.Request, header string) *httpSecurityError {
	raw := strings.TrimSpace(r.Header.Get(header))
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return &httpSecurityError{status: http.StatusForbidden, reason: strings.ToLower(header) + " header is invalid"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return &httpSecurityError{status: http.StatusForbidden, reason: strings.ToLower(header) + " scheme is not allowed"}
	}
	if !sameHostPort(parsed.Host, r.Host) {
		return &httpSecurityError{status: http.StatusForbidden, reason: strings.ToLower(header) + " does not match gateway host"}
	}
	return nil
}

func validateJSONMutationContentType(r *http.Request) *httpSecurityError {
	if r == nil || r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return &httpSecurityError{status: http.StatusUnsupportedMediaType, reason: "content-type application/json required for mutating JSON requests"}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return &httpSecurityError{status: http.StatusUnsupportedMediaType, reason: "content-type application/json required for mutating JSON requests"}
	}
	if !strings.EqualFold(mediaType, "application/json") {
		return &httpSecurityError{status: http.StatusUnsupportedMediaType, reason: "content-type application/json required for mutating JSON requests"}
	}
	return nil
}

func sameHostPort(left, right string) bool {
	leftHost, leftPort := splitHostPortForCompare(left)
	rightHost, rightPort := splitHostPortForCompare(right)
	return strings.EqualFold(leftHost, rightHost) && leftPort == rightPort
}

func splitHostPortForCompare(value string) (string, string) {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		return strings.Trim(strings.TrimSpace(host), "[]"), strings.TrimSpace(port)
	}
	return strings.Trim(strings.TrimSpace(value), "[]"), ""
}

func (s *Server) writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="billyharness-gateway"`)
	s.writeSecurityDenial(w, r, http.StatusUnauthorized, "gateway bearer token required")
}

func (s *Server) writeSecurityDenial(w http.ResponseWriter, r *http.Request, status int, reason string) {
	log.Printf("gateway security denial method=%s path=%s remote=%s status=%d reason=%s", r.Method, r.URL.Path, r.RemoteAddr, status, reason)
	writeError(w, status, reason)
}

func bearerTokenMatches(header, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(header))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(fields[1]), []byte(token)) == 1
}

func (s *Server) requestMayOverrideProviderModel(ctx context.Context) bool {
	if s == nil || !s.httpSecurity.requireMutationAuth {
		return true
	}
	info, _ := ctx.Value(mutationAuthContextKey{}).(mutationAuthInfo)
	return info.bearer
}

func clampMaxToolRoundsOverride(configured, requested int) int {
	if requested <= 0 {
		return 0
	}
	if configured > 0 && requested > configured {
		return configured
	}
	return requested
}

func clampAccessModeOverride(configured, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	requestedMode, ok := config.ParseAccessMode(requested)
	if !ok {
		return requested
	}
	configuredMode := config.NormalizeAccessMode(configured)
	if accessModePrivilege(requestedMode) > accessModePrivilege(configuredMode) {
		return configuredMode
	}
	return requestedMode
}

func accessModePrivilege(mode string) int {
	switch config.NormalizeAccessMode(mode) {
	case config.AccessModePlan:
		return 0
	case config.AccessModeGuarded:
		return 1
	default:
		return 2
	}
}
