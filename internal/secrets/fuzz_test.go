package secrets

import (
	"encoding/json"
	"testing"
)

func FuzzRedactTextAndJSON(f *testing.F) {
	seeds := []struct {
		input string
		extra string
	}{
		{"Authorization: Bearer bearer-secret-value", "bearer-secret-value"},
		{`{"api_key":"json-api-secret-value","safe":"ok"}`, "json-api-secret-value"},
		{"https://user-secret-123:pass-secret-456@example.com/run?token=query-secret", "query-secret"},
		{"plain text", "literal-secret"},
	}
	for _, seed := range seeds {
		f.Add(seed.input, seed.extra)
	}
	f.Fuzz(func(t *testing.T, input string, extra string) {
		if len(input) > 4096 || len(extra) > 512 {
			t.Skip()
		}
		_ = Redact(input, extra)
		_ = RedactURL(input)

		var decoded any
		if err := json.Unmarshal([]byte(input), &decoded); err != nil {
			return
		}
		body, err := RedactJSON(decoded, extra)
		if err != nil {
			t.Fatalf("RedactJSON error: %v", err)
		}
		if !json.Valid(body) {
			t.Fatalf("RedactJSON returned invalid JSON: %s", body)
		}
	})
}
