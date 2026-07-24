package jobagent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/jobruntime"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

type observedCall struct {
	started   bool
	finished  bool
	usageSeen bool
	usage     provider.Usage
}

type usageCollector struct {
	mu                sync.Mutex
	route             jobs.ExecutionRoute
	limits            jobruntime.RemainingLimits
	cancel            context.CancelFunc
	calls             map[string]*observedCall
	err               error
	expectedToolCount int
	// usageInvalid means provider accounting was malformed or internally
	// contradictory. Policy violations such as exceeding a reservation do not
	// set it: the reported numbers remain factual and must be persisted.
	usageInvalid bool
}

func newUsageCollector(route jobs.ExecutionRoute, limits jobruntime.RemainingLimits, cancel context.CancelFunc, expectedToolCount int) *usageCollector {
	return &usageCollector{
		route:             route,
		limits:            limits,
		cancel:            cancel,
		calls:             map[string]*observedCall{},
		expectedToolCount: expectedToolCount,
	}
}

func (c *usageCollector) Emit(event protocol.Event) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.Type {
	case protocol.EventModelCallStarted:
		data, ok := event.Data.(protocol.ModelCallEvent)
		if !ok {
			c.failUsageLocked(errors.New("model.call_started has an unexpected payload"))
			return
		}
		key := modelCallKey(event, data)
		call := c.calls[key]
		if call == nil {
			call = &observedCall{}
			c.calls[key] = call
		}
		if call.started {
			c.failUsageLocked(fmt.Errorf("duplicate model.call_started for %q", key))
			return
		}
		call.started = true
		if uint64(c.startedCallsLocked()) > c.limits.ModelCalls {
			c.failLocked(fmt.Errorf("model calls exceed invocation limit %d", c.limits.ModelCalls))
		}
		c.validateRouteLocked(data)
		if data.ToolCount != c.expectedToolCount {
			c.failLocked(fmt.Errorf("provider request exposed %d tools, want exactly %d for the invocation capability", data.ToolCount, c.expectedToolCount))
		}
	case protocol.EventProviderUsageUpdate:
		usage, ok := event.Data.(provider.Usage)
		if !ok {
			c.failUsageLocked(errors.New("provider.usage has an unexpected payload"))
			return
		}
		if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheHitTokens < 0 || usage.CacheMissTokens < 0 || usage.ReasoningTokens < 0 {
			c.failUsageLocked(errors.New("provider reported negative token usage"))
			return
		}
		call := c.calls[event.StepID]
		if call == nil || !call.started {
			c.failUsageLocked(fmt.Errorf("provider usage preceded model call start for %q", event.StepID))
			return
		}
		call.usageSeen = true
		call.usage = usage
	case protocol.EventModelCallFinished:
		data, ok := event.Data.(protocol.ModelCallEvent)
		if !ok {
			c.failUsageLocked(errors.New("model.call_finished has an unexpected payload"))
			return
		}
		key := modelCallKey(event, data)
		call := c.calls[key]
		if call == nil || !call.started {
			c.failUsageLocked(fmt.Errorf("model.call_finished preceded start for %q", key))
			return
		}
		if call.finished {
			c.failUsageLocked(fmt.Errorf("duplicate model.call_finished for %q", key))
			return
		}
		call.finished = true
		c.validateRouteLocked(data)
		if data.Retries != 0 || data.Attempts > 1 {
			c.failLocked(fmt.Errorf("provider retried a bounded model call: attempts=%d retries=%d", data.Attempts, data.Retries))
		}
		if call.usageSeen && (data.InputTokens != call.usage.InputTokens || data.OutputTokens != call.usage.OutputTokens) {
			c.failUsageLocked(fmt.Errorf("finished model call usage differs from provider usage event for %q", key))
		}
		if call.usageSeen && uint64(call.usage.OutputTokens) > c.limits.MaxOutputTokens {
			c.failLocked(fmt.Errorf("model call output tokens %d exceed per-call limit %d", call.usage.OutputTokens, c.limits.MaxOutputTokens))
		}
		if usage, overflow := c.usageLocked(); overflow {
			c.failUsageLocked(errors.New("provider token usage overflows uint64"))
		} else if usage.TotalTokens() > c.limits.Tokens {
			c.failLocked(fmt.Errorf("provider token usage %d exceeds invocation limit %d", usage.TotalTokens(), c.limits.Tokens))
		}
	case protocol.EventProviderHelperUsage:
		c.failLocked(errors.New("isolated invocation emitted forbidden helper-provider usage"))
	}
}

func (c *usageCollector) Result() (jobs.Usage, error) {
	if c == nil {
		return jobs.Usage{}, fmt.Errorf("%w: collector is nil", ErrUsageAccounting)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	usage, overflow := c.usageLocked()
	if overflow {
		c.failUsageLocked(errors.New("provider token usage overflows uint64"))
	}
	if len(c.calls) == 0 {
		c.failLocked(errors.New("provider made no model calls"))
	}
	for key, call := range c.calls {
		if !call.started || !call.finished {
			c.failUsageLocked(fmt.Errorf("model call %q did not emit a complete start/finish pair", key))
		}
		if !call.usageSeen {
			c.failUsageLocked(fmt.Errorf("model call %q did not report provider usage", key))
		}
	}
	if usage.ModelCalls > c.limits.ModelCalls {
		c.failLocked(fmt.Errorf("model calls %d exceed invocation limit %d", usage.ModelCalls, c.limits.ModelCalls))
	}
	if usage.TotalTokens() == 0 {
		c.failUsageLocked(errors.New("provider reported zero token usage"))
	}
	if usage.TotalTokens() > c.limits.Tokens {
		c.failLocked(fmt.Errorf("provider token usage %d exceeds invocation limit %d", usage.TotalTokens(), c.limits.Tokens))
	}
	if c.err != nil {
		return usage, fmt.Errorf("%w: %v", ErrUsageAccounting, c.err)
	}
	return usage, nil
}

// Provenance reports whether the provider boundary was attempted and whether
// the returned usage is complete and internally consistent. Limit violations
// deliberately remain factual: the runtime must account for the real excess.
func (c *usageCollector) Provenance() (jobruntime.DispatchProvenance, jobruntime.UsageProvenance) {
	if c == nil {
		return jobruntime.DispatchNotDispatched, jobruntime.UsageUnknown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.startedCallsLocked() == 0 {
		return jobruntime.DispatchNotDispatched, jobruntime.UsageUnknown
	}
	if c.usageInvalid {
		return jobruntime.DispatchDispatched, jobruntime.UsageUnknown
	}
	usage, overflow := c.usageLocked()
	if overflow || usage.TotalTokens() == 0 {
		return jobruntime.DispatchDispatched, jobruntime.UsageUnknown
	}
	for _, call := range c.calls {
		if !call.started || !call.finished || !call.usageSeen {
			return jobruntime.DispatchDispatched, jobruntime.UsageUnknown
		}
	}
	return jobruntime.DispatchDispatched, jobruntime.UsageFactual
}

// RejectionPrecededNoGeneration reports whether an HTTP rejection on the
// current provider call can truthfully describe the whole invocation as
// having generated no output. A rejection on a later agent round is not
// enough: an earlier round may already have produced billable tokens and tool
// side effects, including a non-idempotent writer mutation.
func (c *usageCollector) RejectionPrecededNoGeneration() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) != 1 || c.startedCallsLocked() != 1 {
		return false
	}
	for _, call := range c.calls {
		// The agent emits model.call_finished even for a provider error. The
		// typed HTTP rejection, rather than the telemetry finish marker, proves
		// that this sole request produced no response stream.
		return call.started && !call.usageSeen
	}
	return false
}

func (c *usageCollector) validateRouteLocked(data protocol.ModelCallEvent) {
	if data.ProviderID != c.route.ProviderID || data.ModelID != c.route.ModelID || data.Reasoning != c.route.ReasoningEffort {
		c.failLocked(fmt.Errorf(
			"model call route provider=%q model=%q reasoning=%q differs from persisted provider=%q model=%q reasoning=%q",
			data.ProviderID,
			data.ModelID,
			data.Reasoning,
			c.route.ProviderID,
			c.route.ModelID,
			c.route.ReasoningEffort,
		))
	}
}

func (c *usageCollector) startedCallsLocked() int {
	total := 0
	for _, call := range c.calls {
		if call.started {
			total++
		}
	}
	return total
}

func (c *usageCollector) usageLocked() (jobs.Usage, bool) {
	usage := jobs.Usage{ModelCalls: uint64(c.startedCallsLocked())}
	for _, call := range c.calls {
		if !call.usageSeen {
			continue
		}
		input := uint64(call.usage.InputTokens)
		output := uint64(call.usage.OutputTokens)
		if ^uint64(0)-usage.InputTokens < input || ^uint64(0)-usage.OutputTokens < output {
			return usage, true
		}
		usage.InputTokens += input
		usage.OutputTokens += output
	}
	return usage, false
}

func (c *usageCollector) failLocked(err error) {
	if err == nil {
		return
	}
	if c.err == nil {
		c.err = err
	} else {
		c.err = errors.Join(c.err, err)
	}
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *usageCollector) failUsageLocked(err error) {
	c.usageInvalid = true
	c.failLocked(err)
}

func modelCallKey(event protocol.Event, data protocol.ModelCallEvent) string {
	if event.StepID != "" {
		return event.StepID
	}
	return data.RequestID
}
