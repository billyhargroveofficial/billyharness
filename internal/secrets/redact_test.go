package secrets

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactExactEnvironmentAndPatterns(t *testing.T) {
	t.Setenv("BILLY_SECRET_TOKEN", "env-secret-value")
	input := strings.Join([]string{
		"exact literal-secret",
		"env env-secret-value",
		"Authorization: Bearer bearer-secret-value",
		"Proxy-Authorization: Bearer proxy-secret-value",
		"X-Api-Key: header-secret-value",
		"Cookie: session=super-secret-cookie",
		`{"refresh_token":"refresh-secret-value"}`,
		`{"api_key":"json-api-secret-value"}`,
		"mcp args --token argv-token-secret --api-key=argv-inline-secret",
		"https://user-secret-123:pass-secret-456@example.com/mcp?token=query-secret&ok=1",
		"telegram 123456789:AABBCCDDEEFF00112233445566778899",
		"sk-testsecret123456789",
		"github_pat_123456789012345678901234567890",
		"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
	}, "\n")
	out := Redact(input, "literal-secret")
	for _, leaked := range []string{
		"literal-secret",
		"env-secret-value",
		"bearer-secret-value",
		"proxy-secret-value",
		"header-secret-value",
		"super-secret-cookie",
		"refresh-secret-value",
		"json-api-secret-value",
		"argv-token-secret",
		"argv-inline-secret",
		"user-secret-123",
		"pass-secret-456",
		"query-secret",
		"123456789:AABB",
		"sk-testsecret",
		"github_pat_",
		"data:image",
		"iVBORw0KGgo",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("redaction leaked %q in %q", leaked, out)
		}
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("redacted marker missing: %q", out)
	}
	for _, want := range []string{"https://redacted:redacted@example.com/mcp", "token=[redacted]", "--api-key=[redacted]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("redaction missing %q in %q", want, out)
		}
	}
}

func TestRedactSharedBoundaryTable(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		extra   []string
		leaks   []string
		wantAny []string
	}{
		{
			name:  "provider keys",
			input: "deepseek sk-providersecret123456789 github ghp_123456789012345678901234567890123456",
			leaks: []string{"sk-providersecret", "ghp_1234567890"},
		},
		{
			name:  "bearer and authorization headers",
			input: "Authorization: Bearer bearer-secret-value\nProxy-Authorization: Bearer proxy-secret-value",
			leaks: []string{"bearer-secret-value", "proxy-secret-value"},
		},
		{
			name:  "telegram bot token",
			input: "https://api.telegram.org/bot123456789:AABBCCDDEEFF00112233445566778899/sendMessage",
			leaks: []string{"123456789:AABB"},
		},
		{
			name:    "credential URL and query tokens",
			input:   "https://user-secret-123:pass-secret-456@example.com/run?api_key=query-secret&ok=1",
			leaks:   []string{"user-secret-123", "pass-secret-456", "query-secret"},
			wantAny: []string{"https://redacted:redacted@example.com/run", "api_key=[redacted]"},
		},
		{
			name:  "cookie and api key headers",
			input: "X-Api-Key: header-secret-value\nSet-Cookie: session=super-secret-cookie",
			leaks: []string{"header-secret-value", "super-secret-cookie"},
		},
		{
			name:  "MCP args from raw text",
			input: `mcp --token argv-token-secret --api-key=argv-inline-secret ["--secret","argv-json-secret"]`,
			leaks: []string{"argv-token-secret", "argv-inline-secret", "argv-json-secret"},
		},
		{
			name:  "MCP args from explicit extraction",
			input: "server failed argv-token-secret argv-inline-secret",
			extra: ValuesFromArgs([]string{"--token", "argv-token-secret", "--api-key=argv-inline-secret"}),
			leaks: []string{"argv-token-secret", "argv-inline-secret"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input, tt.extra...)
			for _, leaked := range tt.leaks {
				if strings.Contains(got, leaked) {
					t.Fatalf("redaction leaked %q in %q", leaked, got)
				}
			}
			for _, want := range tt.wantAny {
				if !strings.Contains(got, want) {
					t.Fatalf("redaction missing %q in %q", want, got)
				}
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("redacted marker missing: %q", got)
			}
		})
	}
}

func TestRedactJSONRedactsStringsAndKeys(t *testing.T) {
	body, err := RedactJSON(map[string]any{
		"safe": "ok",
		"sk-keysecret123456789": map[string]any{
			"url":  "https://user-secret-123:pass-secret-456@example.com/run?token=query-secret",
			"args": []any{"--token", "argv-json-secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(body) {
		t.Fatalf("redacted JSON is invalid: %s", body)
	}
	text := string(body)
	for _, leaked := range []string{"sk-keysecret", "user-secret-123", "pass-secret-456", "query-secret", "argv-json-secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("redacted JSON leaked %q: %s", leaked, text)
		}
	}
}
