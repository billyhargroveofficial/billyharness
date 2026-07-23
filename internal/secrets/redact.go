package secrets

import (
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type redactPattern struct {
	re          *regexp.Regexp
	replacement string
}

var patterns = []redactPattern{
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@]+@)`), `${1}redacted:redacted@`},
	{regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|auth_token|session_token|bot_token|telegram_bot_token|token|api[_-]?key|apikey|secret|client_secret|password)=)[^&\\\s"'<>}\]]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?im)(^|\\[rn]|\r?\n|[^\w-])(\s*(?:authorization|proxy-authorization|x-api-key|api-key|cookie|set-cookie)\s*[:=]\s*)[^\\\r\n]+`), `${1}${2}[redacted]`},
	{regexp.MustCompile(`(?i)(^|[\s\["',])((?:--?|/)(?:access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|session[_-]?token|bot[_-]?token|token|api[_-]?key|apikey|secret|client[_-]?secret|password)(?:\s+|=))[^"',}\]\s]+`), `${1}${2}[redacted]`},
	{regexp.MustCompile(`(?i)(^|[\s\["',])((?:--?|/)(?:access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|session[_-]?token|bot[_-]?token|token|api[_-]?key|apikey|secret|client[_-]?secret|password)"?\s*,\s*"?)[^"',}\]\s]+`), `${1}${2}[redacted]`},
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s"',}]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)("?(?:access|refresh|id|auth|session|bot)?_?token"?\s*[:=]\s*"?)[^&\["',}\]\s]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)("?(?:api[_-]?key|apikey|client[_-]?secret|secret|password)"?\s*[:=]\s*"?)[^&\["',}\]\s]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)(bot)\d{6,12}:[A-Za-z0-9_-]{20,}`), `${1}[redacted]`},
	{regexp.MustCompile(`\b\d{6,12}:[A-Za-z0-9_-]{20,}\b`), `[redacted]`},
	{regexp.MustCompile(`\bsk-sp-[A-Za-z0-9._-]{12,}\b`), `[redacted]`},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`), `[redacted]`},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), `[redacted]`},
	{regexp.MustCompile(`\bgh[opsu]_[A-Za-z0-9_]{20,}\b`), `[redacted]`},
	{regexp.MustCompile(`\by0__[A-Za-z0-9_-]{20,}\b`), `[redacted]`},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`), `[redacted]`},
	{regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=_-]+`), `[redacted]`},
}

func Redact(input string, extra ...string) string {
	out := input
	for _, secret := range extra {
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	for _, secret := range environmentSecrets() {
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	for _, pattern := range patterns {
		out = pattern.re.ReplaceAllString(out, pattern.replacement)
	}
	return out
}

func RedactURL(rawURL string) string {
	return Redact(strings.TrimSpace(rawURL))
}

func RedactJSON(value any, extra ...string) ([]byte, error) {
	return redactJSON(value, "", "", extra...)
}

func RedactJSONIndent(value any, prefix, indent string, extra ...string) ([]byte, error) {
	return redactJSON(value, prefix, indent, extra...)
}

func RedactValue(value any, extra ...string) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[Redact(key, extra...)] = RedactValue(item, extra...)
		}
		return out
	case []any:
		stringItems := make([]string, 0, len(v))
		allStrings := true
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			stringItems = append(stringItems, text)
		}
		if allStrings {
			extra = append(append([]string(nil), extra...), ValuesFromArgs(stringItems)...)
		}
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, RedactValue(item, extra...))
		}
		return out
	case string:
		return Redact(v, extra...)
	default:
		return value
	}
}

func ValuesFromURLCredentials(rawURL string) []string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.User == nil {
		return nil
	}
	var values []string
	if username := u.User.Username(); len(username) >= 8 {
		values = append(values, username)
	}
	if password, ok := u.User.Password(); ok && len(password) >= 8 {
		values = append(values, password)
	}
	return values
}

func ValuesFromArgs(args []string) []string {
	var values []string
	for i, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if ok && IsSecretName(key) && len(value) >= 8 {
			values = append(values, value)
			continue
		}
		if IsSecretName(arg) && i+1 < len(args) && len(args[i+1]) >= 8 {
			values = append(values, args[i+1])
		}
	}
	return values
}

func IsSecretName(value string) bool {
	value = strings.ToLower(strings.TrimLeft(strings.TrimSpace(value), "-/"))
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Contains(value, "token") ||
		strings.Contains(value, "secret") ||
		strings.Contains(value, "password") ||
		strings.Contains(value, "api_key") ||
		strings.Contains(value, "apikey")
}

func redactJSON(value any, prefix, indent string, extra ...string) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	redacted := RedactValue(decoded, extra...)
	if indent != "" {
		return json.MarshalIndent(redacted, prefix, indent)
	}
	return json.Marshal(redacted)
}

func environmentSecrets() []string {
	var out []string
	for _, pair := range os.Environ() {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || len(value) < 8 {
			continue
		}
		if IsSecretName(name) {
			out = append(out, value)
		}
	}
	return out
}
