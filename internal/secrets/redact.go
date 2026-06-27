package secrets

import (
	"os"
	"regexp"
	"strings"
)

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s"',}]+`),
	regexp.MustCompile(`(?i)("?(?:access|refresh|id)?_?token"?\s*[:=]\s*"?)[^"',}\s]+`),
	regexp.MustCompile(`(?i)("?(?:api[_-]?key|secret|password)"?\s*[:=]\s*"?)[^"',}\s]+`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bgh[opsu]_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\by0__[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
}

func Redact(input string, secrets ...string) string {
	out := input
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	for _, secret := range environmentSecrets() {
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	for _, pattern := range patterns {
		out = pattern.ReplaceAllString(out, "${1}[redacted]")
	}
	return out
}

func environmentSecrets() []string {
	var out []string
	for _, pair := range os.Environ() {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || len(value) < 8 {
			continue
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "apikey") {
			out = append(out, value)
		}
	}
	return out
}
