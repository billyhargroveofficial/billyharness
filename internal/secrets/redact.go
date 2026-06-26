package secrets

import "strings"

func Redact(input string, secrets ...string) string {
	out := input
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	return out
}
