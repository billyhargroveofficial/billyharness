package gateway

import (
	"context"
	"errors"
	"net/http"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewaybase"
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
	return gatewaybase.UnavailableHint(baseURL)
}

func WaitForReady(ctx context.Context, baseURL string, timeout time.Duration) bool {
	return gatewaybase.WaitForReady(ctx, baseURL, timeout)
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
