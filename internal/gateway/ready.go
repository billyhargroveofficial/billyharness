package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"time"
)

type UnavailableError struct {
	BaseURL string
	Err     error
}

func (e *UnavailableError) Error() string {
	hint := UnavailableHint(e.BaseURL)
	if e.Err == nil {
		return hint
	}
	return hint + ": " + e.Err.Error()
}

func (e *UnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func UnavailableHint(baseURL string) string {
	baseURL = NormalizeBaseURL(baseURL)
	if baseURL == "" {
		baseURL = "configured gateway"
	}
	parts := []string{
		"gateway " + baseURL + " is not reachable",
		"start it with ./bin/fast-agent-harness gateway",
		"or run systemctl restart billyharness-gateway.service",
		"inspect with systemctl --no-pager --full status billyharness-gateway.service",
	}
	return strings.Join(parts, "; ")
}

func WaitForReady(ctx context.Context, baseURL string, timeout time.Duration) bool {
	baseURL = NormalizeBaseURL(baseURL)
	if baseURL == "" {
		return false
	}
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 220 * time.Millisecond}
	for {
		if healthOK(ctx, &client, baseURL) {
			return true
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func DoWithReadyRetry(ctx context.Context, client *http.Client, baseURL string, makeRequest func() (*http.Request, error)) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := makeRequest()
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err == nil {
		return resp, nil
	}
	if !isConnectionRefused(err) {
		return nil, err
	}
	if !WaitForReady(ctx, baseURL, 2*time.Second) {
		return nil, &UnavailableError{BaseURL: baseURL, Err: err}
	}
	req, reqErr := makeRequest()
	if reqErr != nil {
		return nil, reqErr
	}
	return client.Do(req)
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

func healthOK(ctx context.Context, client *http.Client, baseURL string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 260*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
