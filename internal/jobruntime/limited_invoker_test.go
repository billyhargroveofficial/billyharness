package jobruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitedInvokerCapsConcurrencyAcrossSimultaneousCalls(t *testing.T) {
	delegate := &blockingLimitedInvoker{
		started: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
	limited, err := NewLimitedInvoker(delegate, 2)
	if err != nil {
		t.Fatal(err)
	}

	const calls = 8
	var wg sync.WaitGroup
	errCh := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, invokeErr := limited.Invoke(t.Context(), Invocation{})
			errCh <- invokeErr
		}()
	}
	for index := 0; index < 2; index++ {
		select {
		case <-delegate.started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for admitted invocation")
		}
	}
	waitForLimitedInvokerCount(t, time.Second, func() int { return limited.Queued() }, calls-2)
	if got := delegate.maxActive.Load(); got != 2 || limited.Active() != 2 || limited.Limit() != 2 {
		t.Fatalf("concurrency max=%d active=%d limit=%d queued=%d", got, limited.Active(), limited.Limit(), limited.Queued())
	}
	close(delegate.release)
	wg.Wait()
	close(errCh)
	for invokeErr := range errCh {
		if invokeErr != nil {
			t.Fatalf("limited invocation error = %v", invokeErr)
		}
	}
	if got := delegate.maxActive.Load(); got > 2 {
		t.Fatalf("observed concurrency %d exceeds cap 2", got)
	}
	if delegate.calls.Load() != calls || limited.Active() != 0 || limited.Queued() != 0 {
		t.Fatalf("final calls=%d active=%d queued=%d", delegate.calls.Load(), limited.Active(), limited.Queued())
	}
}

func TestLimitedInvokerQueuedCancellationIsUndispatchedUnknown(t *testing.T) {
	delegate := &blockingLimitedInvoker{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	limited, err := NewLimitedInvoker(delegate, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, invokeErr := limited.Invoke(t.Context(), Invocation{})
		firstDone <- invokeErr
	}()
	select {
	case <-delegate.started:
	case <-time.After(time.Second):
		t.Fatal("first invocation did not acquire limiter")
	}

	queuedCtx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, invokeErr := limited.Invoke(queuedCtx, Invocation{})
		secondDone <- invokeErr
	}()
	waitForLimitedInvokerCount(t, time.Second, func() int { return limited.Queued() }, 1)
	cancel()
	var queuedErr error
	select {
	case queuedErr = <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("queued invocation ignored cancellation")
	}
	if !errors.Is(queuedErr, context.Canceled) {
		t.Fatalf("queued error = %v", queuedErr)
	}
	dispatch, usage, ok := InvocationFailureFromError(queuedErr)
	if !ok || dispatch != DispatchNotDispatched || usage != UsageUnknown ||
		FatalPreflightFailureFromError(queuedErr) {
		t.Fatalf("queued provenance=%q/%q/%t fatal=%t", dispatch, usage, ok, FatalPreflightFailureFromError(queuedErr))
	}
	if delegate.calls.Load() != 1 {
		t.Fatalf("queued cancellation crossed delegate: calls=%d", delegate.calls.Load())
	}

	close(delegate.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first invocation error = %v", err)
	}
}

func TestNewLimitedInvokerValidatesConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		delegate Invoker
		limit    int
	}{
		{name: "nil delegate", limit: 1},
		{name: "zero", delegate: &blockingLimitedInvoker{}, limit: 0},
		{name: "too large", delegate: &blockingLimitedInvoker{}, limit: MaxLimitedInvokerConcurrency + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if limited, err := NewLimitedInvoker(test.delegate, test.limit); err == nil || limited != nil {
				t.Fatalf("invalid limiter accepted: %#v err=%v", limited, err)
			}
		})
	}
}

type blockingLimitedInvoker struct {
	started   chan struct{}
	release   chan struct{}
	active    atomic.Int64
	maxActive atomic.Int64
	calls     atomic.Int64
}

func (i *blockingLimitedInvoker) Invoke(ctx context.Context, _ Invocation) (InvocationResult, error) {
	i.calls.Add(1)
	active := i.active.Add(1)
	defer i.active.Add(-1)
	for {
		maximum := i.maxActive.Load()
		if active <= maximum || i.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	if i.started != nil {
		i.started <- struct{}{}
	}
	if i.release != nil {
		select {
		case <-i.release:
		case <-ctx.Done():
			return InvocationResult{}, ctx.Err()
		}
	}
	return InvocationResult{}, nil
}

func waitForLimitedInvokerCount(t *testing.T, timeout time.Duration, value func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if value() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for count %d; got %d", want, value())
}
