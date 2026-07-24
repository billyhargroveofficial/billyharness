package jobruntime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// MaxLimitedInvokerConcurrency bounds semaphore allocation and protects an
// operator typo from turning one gateway into an effectively unbounded
// provider dispatcher.
const MaxLimitedInvokerConcurrency = 1024

// LimitedInvoker applies one process-local concurrency budget to a shared
// Invoker. Durable-job composition creates exactly one wrapper for the runner,
// so independent jobs compete for the same slots while each job's persisted
// worker topology remains unchanged.
type LimitedInvoker struct {
	delegate Invoker
	slots    chan struct{}
	active   atomic.Int64
	queued   atomic.Int64
}

var _ Invoker = (*LimitedInvoker)(nil)

func NewLimitedInvoker(delegate Invoker, maxConcurrent int) (*LimitedInvoker, error) {
	if delegate == nil {
		return nil, errors.New("limited invoker delegate is required")
	}
	if maxConcurrent < 1 || maxConcurrent > MaxLimitedInvokerConcurrency {
		return nil, fmt.Errorf("limited invoker concurrency must be between 1 and %d", MaxLimitedInvokerConcurrency)
	}
	return &LimitedInvoker{delegate: delegate, slots: make(chan struct{}, maxConcurrent)}, nil
}

func (l *LimitedInvoker) Invoke(ctx context.Context, invocation Invocation) (InvocationResult, error) {
	if l == nil || l.delegate == nil || l.slots == nil {
		return InvocationResult{}, NewFatalPreflightFailure(errors.New("limited invoker is not initialized"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return InvocationResult{}, NewInvocationFailure(err, DispatchNotDispatched, UsageUnknown)
	}

	l.queued.Add(1)
	select {
	case l.slots <- struct{}{}:
		l.queued.Add(-1)
	case <-ctx.Done():
		l.queued.Add(-1)
		return InvocationResult{}, NewInvocationFailure(ctx.Err(), DispatchNotDispatched, UsageUnknown)
	}
	// If cancellation won concurrently with slot admission, do not cross the
	// delegate/provider boundary. The acquired slot is still released below.
	if err := ctx.Err(); err != nil {
		<-l.slots
		return InvocationResult{}, NewInvocationFailure(err, DispatchNotDispatched, UsageUnknown)
	}

	l.active.Add(1)
	defer func() {
		l.active.Add(-1)
		<-l.slots
	}()
	return l.delegate.Invoke(ctx, invocation)
}

func (l *LimitedInvoker) Limit() int {
	if l == nil {
		return 0
	}
	return cap(l.slots)
}

func (l *LimitedInvoker) Active() int {
	if l == nil {
		return 0
	}
	return int(l.active.Load())
}

func (l *LimitedInvoker) Queued() int {
	if l == nil {
		return 0
	}
	return int(l.queued.Load())
}
