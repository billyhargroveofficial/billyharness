package provider

import (
	"context"
	"errors"
	"time"
)

const (
	maxProviderBackoff   = 5 * time.Second
	maxProviderRetryWait = time.Minute
)

func withProviderRetry(ctx context.Context, maxRetries int, fn func(attempt int) error) error {
	if maxRetries < 0 {
		maxRetries = 0
	}
	var last error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := fn(attempt)
		if err == nil {
			return nil
		}
		last = err
		if !retryableProviderError(err) || attempt == maxRetries {
			return err
		}
		if err := sleepProviderRetry(ctx, providerRetryDelay(err, attempt)); err != nil {
			return err
		}
	}
	return last
}

func retryableProviderError(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.Retryable()
}

func providerRetryDelay(err error, attempt int) time.Duration {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.RetryAfter > 0 {
		if providerErr.RetryAfter > maxProviderRetryWait {
			return maxProviderRetryWait
		}
		return providerErr.RetryAfter
	}
	delay := time.Duration(250*(1<<attempt)) * time.Millisecond
	if delay > maxProviderBackoff {
		return maxProviderBackoff
	}
	return delay
}

func sleepProviderRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
