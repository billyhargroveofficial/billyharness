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

type StreamDrainOptions struct {
	OnEvent func(Event) error
	FlushC  func() <-chan time.Time
	OnFlush func() error
}

// DrainStream consumes a provider stream without letting a ready terminal
// error overtake events that the provider published first.
func DrainStream(ctx context.Context, events <-chan Event, errs <-chan error, opts StreamDrainOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var terminalErr error
	for events != nil || errs != nil {
		var flushC <-chan time.Time
		if opts.FlushC != nil {
			flushC = opts.FlushC()
		}

		// Keep cancellation and flushes responsive even when a provider has a
		// continuously ready event channel.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		select {
		case <-flushC:
			if opts.OnFlush != nil {
				if err := opts.OnFlush(); err != nil {
					return err
				}
			}
			continue
		default:
		}

		// Provider.Stream publishes and closes events before exposing a terminal
		// error. Prefer every event that is already ready, and defer a terminal
		// error selected in the blocking select below until one final ready-event
		// pass. This avoids select's random choice dropping a terminal EventDone,
		// while still returning promptly for a broken provider that leaves events
		// open after reporting an error.
		if events != nil {
			select {
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if opts.OnEvent != nil {
					if err := opts.OnEvent(event); err != nil {
						return err
					}
				}
				continue
			default:
			}
		}
		if terminalErr != nil {
			return terminalErr
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flushC:
			if opts.OnFlush != nil {
				if err := opts.OnFlush(); err != nil {
					return err
				}
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if opts.OnEvent != nil {
				if err := opts.OnEvent(event); err != nil {
					return err
				}
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				terminalErr = err
				errs = nil
			}
		}
	}
	if terminalErr != nil {
		return terminalErr
	}
	return nil
}

func newStreamIdleTimer(timeout time.Duration) (*time.Timer, <-chan time.Time) {
	if timeout <= 0 {
		return nil, nil
	}
	timer := time.NewTimer(timeout)
	return timer, timer.C
}

func resetStreamIdleTimer(timer *time.Timer, timeout time.Duration) {
	if timer == nil || timeout <= 0 {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}

func stopStreamIdleTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
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
