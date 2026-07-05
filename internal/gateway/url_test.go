package gateway

import (
	"testing"
)

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
