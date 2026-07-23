package webtools

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const maxAllowedURLPrefixes = 32

// NormalizeAllowedHTTPSURLPrefixes validates exact HTTPS origin/path entries.
// The historical "prefixes" wire name is retained, but matching is exact by
// canonical path; request query parameters are ignored.
func NormalizeAllowedHTTPSURLPrefixes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxAllowedURLPrefixes {
		return nil, fmt.Errorf("allowed_url_prefixes has %d entries; maximum is %d", len(values), maxAllowedURLPrefixes)
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("allowed_url_prefixes[%d] must be a non-empty canonical HTTPS URL without surrounding whitespace", i)
		}
		u, err := url.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("allowed_url_prefixes[%d]: %w", i, err)
		}
		canonical, err := canonicalAllowedHTTPSURL(u, true)
		if err != nil {
			return nil, fmt.Errorf("allowed_url_prefixes[%d]: %w", i, err)
		}
		if _, ok := seen[canonical]; ok {
			return nil, fmt.Errorf("allowed_url_prefixes contains duplicate %q", canonical)
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	sort.Strings(out)
	return out, nil
}

func ValidateURLAgainstAllowedHTTPSPrefixes(rawURL string, prefixes []string) error {
	if len(prefixes) == 0 {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	return validateAllowedHTTPSURL(u, prefixes)
}

func validateAllowedHTTPSURL(target *url.URL, prefixes []string) error {
	if len(prefixes) == 0 {
		return nil
	}
	normalized, err := NormalizeAllowedHTTPSURLPrefixes(prefixes)
	if err != nil {
		return fmt.Errorf("invalid allowed URL prefix policy: %w", err)
	}
	targetOrigin, targetPath, err := canonicalHTTPSOriginPath(target, false)
	if err != nil {
		return fmt.Errorf("URL denied by allowed_url_prefixes: %w", err)
	}
	for _, prefix := range normalized {
		u, parseErr := url.Parse(prefix)
		if parseErr != nil {
			return fmt.Errorf("invalid normalized allowed URL prefix %q: %w", prefix, parseErr)
		}
		prefixOrigin, prefixPath, parseErr := canonicalHTTPSOriginPath(u, true)
		if parseErr != nil {
			return fmt.Errorf("invalid normalized allowed URL prefix %q: %w", prefix, parseErr)
		}
		if targetOrigin == prefixOrigin && targetPath == prefixPath {
			return nil
		}
	}
	return fmt.Errorf("URL %q is outside allowed_url_prefixes", target.String())
}

func canonicalAllowedHTTPSURL(u *url.URL, prefix bool) (string, error) {
	origin, escapedPath, err := canonicalHTTPSOriginPath(u, prefix)
	if err != nil {
		return "", err
	}
	return origin + escapedPath, nil
}

func canonicalHTTPSOriginPath(u *url.URL, prefix bool) (string, string, error) {
	if u == nil {
		return "", "", fmt.Errorf("URL required")
	}
	if u.Scheme != "https" {
		return "", "", fmt.Errorf("only https URL prefixes are allowed")
	}
	if u.Opaque != "" || u.User != nil {
		return "", "", fmt.Errorf("URL userinfo and opaque URLs are not allowed")
	}
	if u.Hostname() == "" {
		return "", "", fmt.Errorf("URL host required")
	}
	if u.Fragment != "" {
		return "", "", fmt.Errorf("URL fragments are not allowed")
	}
	if prefix && (u.RawQuery != "" || u.ForceQuery) {
		return "", "", fmt.Errorf("allowed URL prefixes must not contain a query")
	}
	host, err := canonicalASCIIHost(u.Hostname())
	if err != nil {
		return "", "", err
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", "", fmt.Errorf("invalid HTTPS port %q", port)
		}
		if value == 443 {
			port = ""
		}
	}
	if strings.Contains(u.Host, "@") {
		return "", "", fmt.Errorf("URL userinfo is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := "https://" + host
	if port != "" {
		origin += ":" + port
	}
	escapedPath, err := canonicalSafeURLPath(u)
	if err != nil {
		return "", "", err
	}
	return origin, escapedPath, nil
}

func canonicalSafeURLPath(u *url.URL) (string, error) {
	escaped := u.EscapedPath()
	if escaped == "" {
		escaped = "/"
	}
	if strings.Contains(escaped, "%") {
		return "", fmt.Errorf("URL paths in allowed_url_prefixes policy must use an unescaped canonical path")
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", fmt.Errorf("invalid URL path encoding: %w", err)
	}
	if !strings.HasPrefix(decoded, "/") || strings.Contains(decoded, "\\") || strings.ContainsRune(decoded, '\x00') {
		return "", fmt.Errorf("URL path must be an absolute slash path")
	}
	if strings.Contains(decoded, "//") {
		return "", fmt.Errorf("URL path must not contain empty path segments")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("URL path must not contain dot segments")
		}
	}
	if escaped != "/" {
		escaped = strings.TrimSuffix(escaped, "/")
	}
	return escaped, nil
}

func canonicalASCIIHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("URL host required")
	}
	if strings.Contains(host, "%") {
		return "", fmt.Errorf("IPv6 zone identifiers are not allowed")
	}
	for _, r := range host {
		if r > 127 {
			return "", fmt.Errorf("non-ASCII URL hostnames are not allowed")
		}
	}
	host = normalizeHost(host)
	if host == "" {
		return "", fmt.Errorf("URL host required")
	}
	return host, nil
}
