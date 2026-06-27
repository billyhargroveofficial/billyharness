package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"syscall"
	"time"
)

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
	if !isConnectionRefused(err) || !WaitForReady(ctx, baseURL, 2*time.Second) {
		return nil, err
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
