package fakeprovider

import (
	"context"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

type Step struct {
	Events        []provider.Event
	Err           error
	Delay         time.Duration
	WaitForCancel bool
	Started       chan<- struct{}
}

type Provider struct {
	mu       sync.Mutex
	steps    []Step
	repeat   *Step
	calls    int
	requests []provider.Request
}

func New(steps ...Step) *Provider {
	return &Provider{steps: append([]Step(nil), steps...)}
}

func (p *Provider) SetRepeat(step Step) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copy := step
	p.repeat = &copy
}

func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *Provider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.Request, len(p.requests))
	copy(out, p.requests)
	return out
}

func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, <-chan error) {
	step := p.next(req)
	events := make(chan provider.Event, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if step.Started != nil {
			close(step.Started)
		}
		if step.WaitForCancel {
			<-ctx.Done()
			errs <- ctx.Err()
			return
		}
		for _, event := range step.Events {
			if step.Delay > 0 {
				timer := time.NewTimer(step.Delay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					errs <- ctx.Err()
					return
				}
			}
			select {
			case events <- event:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if step.Err != nil {
			errs <- step.Err
		}
	}()
	return events, errs
}

func (p *Provider) next(req provider.Request) Step {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	call := p.calls
	if call-1 < len(p.steps) {
		return p.steps[call-1]
	}
	if p.repeat != nil {
		return *p.repeat
	}
	return Step{}
}
