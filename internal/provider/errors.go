package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ErrorKind string

const (
	ErrorTransport       ErrorKind = "transport"
	ErrorRateLimit       ErrorKind = "rate_limit"
	ErrorAuth            ErrorKind = "auth"
	ErrorBadRequest      ErrorKind = "bad_request"
	ErrorContextOverflow ErrorKind = "context_overflow"
	ErrorServer          ErrorKind = "server"
	ErrorStreamClosed    ErrorKind = "stream_closed"
	ErrorUnknown         ErrorKind = "unknown"
)

type ProviderError struct {
	Provider   string
	ModelID    string
	Kind       ErrorKind
	Status     int
	Message    string
	RequestID  string
	Attempts   int
	Retries    int
	RetryAfter time.Duration
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{}
	if e.Provider != "" {
		parts = append(parts, e.Provider)
	}
	if e.Kind != "" {
		parts = append(parts, string(e.Kind))
	}
	if e.Status != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.Status))
	}
	prefix := strings.Join(parts, " ")
	if prefix == "" {
		prefix = "provider error"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		return prefix
	}
	return prefix + ": " + msg
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ProviderError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ErrorTransport, ErrorRateLimit, ErrorServer, ErrorStreamClosed:
		return true
	default:
		return false
	}
}

func providerHTTPError(provider string, status int, header http.Header, body string) *ProviderError {
	kind := classifyProviderStatus(status, body)
	return &ProviderError{
		Provider:   provider,
		Kind:       kind,
		Status:     status,
		Message:    strings.TrimSpace(body),
		RequestID:  firstHeader(header, "x-request-id", "request-id", "openai-request-id"),
		RetryAfter: parseRetryAfter(header.Get("Retry-After"), time.Now()),
	}
}

func providerTransportError(provider string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &ProviderError{Provider: provider, Kind: ErrorTransport, Err: err}
}

func classifyProviderStatus(status int, body string) ErrorKind {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context") {
		return ErrorContextOverflow
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorAuth
	case http.StatusTooManyRequests:
		return ErrorRateLimit
	case http.StatusBadRequest:
		return ErrorBadRequest
	}
	if status >= 500 || status == http.StatusRequestTimeout || status == 425 {
		return ErrorServer
	}
	return ErrorUnknown
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds <= 0 || seconds != seconds {
			return 0
		}
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds > float64(maxDuration)/float64(time.Second) {
			return maxDuration
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if parsed, err := http.ParseTime(value); err == nil {
		if parsed.Before(now) {
			return 0
		}
		return parsed.Sub(now)
	}
	return 0
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}
