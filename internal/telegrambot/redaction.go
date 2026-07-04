package telegrambot

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

func redactTelegramText(text string) string {
	if text == "" {
		return ""
	}
	return secrets.Redact(text)
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
