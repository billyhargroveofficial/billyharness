package telegrambot

import (
	"strings"
	"testing"
)

func TestRedactTelegramTextCoversCredentialURLsTokensAndHeaders(t *testing.T) {
	input := strings.Join([]string{
		"gateway failed: https://user-secret:pass-secret@example.com/run?token=query-secret&ok=1",
		"Authorization: Bearer bearer-secret-value",
		"X-Api-Key: header-secret-value",
		"Cookie: session=super-secret-cookie",
		"provider returned sk-telegramsecret123456789",
	}, "\n")
	got := redactTelegramText(input)
	for _, leaked := range []string{
		"user-secret",
		"pass-secret",
		"query-secret",
		"bearer-secret-value",
		"header-secret-value",
		"super-secret-cookie",
		"sk-telegramsecret",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redaction leaked %q in:\n%s", leaked, got)
		}
	}
	for _, want := range []string{"https://redacted:redacted@example.com/run", "token=[redacted]", "[redacted]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redaction missing %q in:\n%s", want, got)
		}
	}
}
