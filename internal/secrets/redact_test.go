package secrets

import (
	"strings"
	"testing"
)

func TestRedactExactEnvironmentAndPatterns(t *testing.T) {
	t.Setenv("BILLY_SECRET_TOKEN", "env-secret-value")
	input := strings.Join([]string{
		"exact literal-secret",
		"env env-secret-value",
		"Authorization: Bearer bearer-secret-value",
		`{"refresh_token":"refresh-secret-value"}`,
		"sk-testsecret123456789",
		"github_pat_123456789012345678901234567890",
	}, "\n")
	out := Redact(input, "literal-secret")
	for _, leaked := range []string{
		"literal-secret",
		"env-secret-value",
		"bearer-secret-value",
		"refresh-secret-value",
		"sk-testsecret",
		"github_pat_",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("redaction leaked %q in %q", leaked, out)
		}
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("redacted marker missing: %q", out)
	}
}
