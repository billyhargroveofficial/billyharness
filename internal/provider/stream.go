package provider

import (
	"bufio"
	"context"
	"io"
	"sync/atomic"
	"time"
)

const (
	providerEventBuffer = 1024
	providerLineBuffer  = 256
)

func newProviderEventChannel() chan Event {
	return make(chan Event, providerEventBuffer)
}

func runProviderStream(events chan Event, errs chan error, run func() error) {
	var streamErr error
	defer func() {
		if streamErr != nil {
			errs <- streamErr
		}
		close(errs)
	}()
	defer close(events)
	streamErr = run()
}

func newRequestSetupContext(ctx context.Context, timeout time.Duration) (context.Context, func() bool, context.CancelFunc) {
	reqCtx, cancel := context.WithCancel(ctx)
	var timer *time.Timer
	var timedOut atomic.Bool
	if timeout > 0 {
		timer = time.AfterFunc(timeout, func() {
			timedOut.Store(true)
			cancel()
		})
	}
	finishSetup := func() bool {
		if timer != nil {
			if !timer.Stop() {
				timedOut.Store(true)
			}
		}
		return timedOut.Load()
	}
	return reqCtx, finishSetup, cancel
}

func scanLines(ctx context.Context, r io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string, providerLineBuffer)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		errs <- scanner.Err()
	}()
	return lines, errs
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) error {
	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
