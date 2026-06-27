package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		" 127.0.0.1:8765/ ":        "http://127.0.0.1:8765",
		":8765/":                   "http://127.0.0.1:8765",
		"0.0.0.0:8765":             "http://127.0.0.1:8765",
		"[::]:8765":                "http://127.0.0.1:8765",
		"http://0.0.0.0:8765/":     "http://127.0.0.1:8765",
		"http://localhost:80/":     "http://localhost:80",
		"https://example.com/api/": "https://example.com/api",
	}
	for input, want := range tests {
		if got := NormalizeBaseURL(input); got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRequiresAuthForAddr(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:8765":  false,
		"localhost:8765":  false,
		"[::1]:8765":      false,
		":8765":           true,
		"0.0.0.0:8765":    true,
		"[::]:8765":       true,
		"192.0.2.10:8765": true,
	}
	for input, want := range tests {
		if got := RequiresAuthForAddr(input); got != want {
			t.Fatalf("RequiresAuthForAddr(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestSetAuthHeaderFromEnv(t *testing.T) {
	t.Setenv(GatewayAuthTokenEnv, "test-token")

	req := httptest.NewRequest("GET", "/v1/mcp", nil)
	SetAuthHeaderFromEnv(req)

	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q", got)
	}
}
