package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnavailableHintIncludesRecoveryCommands(t *testing.T) {
	hint := UnavailableHint(":8765")
	for _, want := range []string{
		"gateway http://127.0.0.1:8765 is not reachable",
		"./bin/fast-agent-harness gateway",
		"systemctl restart billyharness-gateway.service",
		"systemctl --no-pager --full status billyharness-gateway.service",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint %q missing %q", hint, want)
		}
	}
}

func TestDoWithReadyRetryWrapsConnectionRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "dial", URL: req.URL.String(), Err: syscall.ECONNREFUSED}
	})}

	_, err := DoWithReadyRetry(ctx, client, "127.0.0.1:1", func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/v1/mcp", nil)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %T %v, want UnavailableError", err, err)
	}
	if !strings.Contains(err.Error(), "./bin/fast-agent-harness gateway") ||
		!strings.Contains(err.Error(), "systemctl restart billyharness-gateway.service") {
		t.Fatalf("error does not include recovery commands: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
