package telegrambot

import (
	"regexp"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

var (
	telegramURLCredentialPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@]+@)`)
	telegramURLSecretQuery       = regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|api[_-]?key|apikey|secret|password)=)[^&\s"'<>]+`)
	telegramHeaderSecretPattern  = regexp.MustCompile(`(?im)\b((?:authorization|proxy-authorization|x-api-key|api-key|cookie|set-cookie)\s*[:=]\s*)[^\r\n]+`)
)

func redactTelegramText(text string) string {
	if text == "" {
		return ""
	}
	out := telegramURLCredentialPattern.ReplaceAllString(text, "${1}redacted:redacted@")
	out = telegramURLSecretQuery.ReplaceAllString(out, "${1}[redacted]")
	out = secrets.Redact(out)
	out = telegramHeaderSecretPattern.ReplaceAllString(out, "${1}[redacted]")
	return out
}

func telegramErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(redactTelegramText(err.Error()))
	if text == "" {
		return "failed"
	}
	return text
}
