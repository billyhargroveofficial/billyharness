package agent

import (
	"context"
	"strings"
	"time"

	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
)

const (
	modelDeltaCoalesceMaxBytes = 4 * 1024
	modelDeltaCoalesceMaxDelay = 50 * time.Millisecond
)

type modelCallStepInput struct {
	TurnID       string
	Round        int
	Messages     []protocol.Message
	ToolSpecs    []protocol.ToolSpec
	TurnSnapshot runstate.Snapshot
}

type modelCallStepResult struct {
	Content      string
	Reasoning    string
	ToolCalls    []protocol.ToolCall
	PromptTokens int64
	Err          error
}

func (a *Agent) runModelCallStep(ctx context.Context, hookRunner *runtimehooks.Runner, input modelCallStepInput, emit func(protocol.Event)) modelCallStepResult {
	stepID := agentStepID(input.TurnID, protocol.StepKindModelCall, 1)
	requestID := agentRequestID(input.TurnID, input.Round)
	modelCallBase := a.modelCallMetadata(requestID, input.Round, len(input.Messages), len(input.ToolSpecs), input.TurnSnapshot)
	started := time.Now()
	emit(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
		TurnID:       input.TurnID,
		StepID:       stepID,
		Round:        input.Round,
		Kind:         protocol.StepKindModelCall,
		Status:       protocol.StepStatusStarted,
		Name:         a.modelID(),
		MessageCount: len(input.Messages),
		Metadata:     copyMap(modelCallBase),
	}})
	emit(protocol.Event{
		Type:   protocol.EventModelCallStarted,
		TurnID: input.TurnID,
		StepID: stepID,
		Data:   modelCallEventData(modelCallBase, protocol.StepStatusStarted, -1, -1, provider.Usage{}, provider.RequestMetadata{}, ""),
	})
	stream := a.collectModelCallStream(ctx, hookRunner, provider.Request{
		RequestID: requestID,
		Model:     a.modelID(),
		Messages:  input.Messages,
		Tools:     input.ToolSpecs,
	}, input.TurnID, stepID, emit)
	result := modelCallStepResult{
		Content:      stream.Content,
		Reasoning:    stream.Reasoning,
		PromptTokens: stream.PromptTokens,
	}
	if err := stream.Err; err != nil {
		result.Err = err
		a.emitModelCallStepFailed(input, stepID, modelCallBase, started, stream, err, emit)
		return result
	}
	if stream.HookErr != nil {
		result.Err = stream.HookErr
		a.emitModelCallStepFailed(input, stepID, modelCallBase, started, stream, stream.HookErr, emit)
		return result
	}
	emit(protocol.Event{
		Type:   protocol.EventModelCallFinished,
		TurnID: input.TurnID,
		StepID: stepID,
		Data:   modelCallEventData(modelCallBase, protocol.StepStatusCompleted, durationMS(started), firstDeltaLatencyMS(started, stream.FirstDeltaAt), stream.Usage, stream.RequestMetadata, ""),
	})
	calls, err := stream.Accumulator.Finish()
	if err != nil {
		result.Err = err
		emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
			TurnID:     input.TurnID,
			StepID:     stepID,
			Round:      input.Round,
			Kind:       protocol.StepKindModelCall,
			Status:     protocol.StepStatusFailed,
			Name:       a.modelID(),
			DurationMS: durationMS(started),
			Error:      err.Error(),
		}})
		return result
	}
	result.ToolCalls = calls
	modelMetadata := map[string]any{
		"content_chars":   len(stream.Content),
		"reasoning_chars": len(stream.Reasoning),
		"tool_call_count": len(calls),
	}
	for key, value := range modelCallEventMetadata(modelCallEventData(modelCallBase, protocol.StepStatusCompleted, durationMS(started), firstDeltaLatencyMS(started, stream.FirstDeltaAt), stream.Usage, stream.RequestMetadata, "")) {
		modelMetadata[key] = value
	}
	if !stream.FirstDeltaAt.IsZero() {
		modelMetadata["first_delta_ms"] = elapsedMS(started, stream.FirstDeltaAt)
	}
	emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
		TurnID:     input.TurnID,
		StepID:     stepID,
		Round:      input.Round,
		Kind:       protocol.StepKindModelCall,
		Status:     protocol.StepStatusCompleted,
		Name:       a.modelID(),
		DurationMS: durationMS(started),
		Metadata:   modelMetadata,
	}})
	return result
}

func (a *Agent) emitModelCallStepFailed(input modelCallStepInput, stepID string, base map[string]any, started time.Time, stream modelCallStreamResult, err error, emit func(protocol.Event)) {
	emit(protocol.Event{
		Type:   protocol.EventModelCallFinished,
		TurnID: input.TurnID,
		StepID: stepID,
		Data:   modelCallEventData(base, protocol.StepStatusFailed, durationMS(started), firstDeltaLatencyMS(started, stream.FirstDeltaAt), stream.Usage, stream.RequestMetadata, err.Error()),
	})
	emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
		TurnID:     input.TurnID,
		StepID:     stepID,
		Round:      input.Round,
		Kind:       protocol.StepKindModelCall,
		Status:     protocol.StepStatusFailed,
		Name:       a.modelID(),
		DurationMS: durationMS(started),
		Error:      err.Error(),
	}})
}

type modelCallStreamResult struct {
	Content         string
	Reasoning       string
	FirstDeltaAt    time.Time
	Usage           provider.Usage
	RequestMetadata provider.RequestMetadata
	PromptTokens    int64
	Accumulator     provider.ToolAccumulator
	HookErr         error
	Err             error
}

func (a *Agent) collectModelCallStream(ctx context.Context, hookRunner *runtimehooks.Runner, req provider.Request, turnID, stepID string, emit func(protocol.Event)) modelCallStreamResult {
	events, errs := a.provider.Stream(ctx, req)
	var result modelCallStreamResult
	deltas := newModelDeltaCoalescer(turnID, stepID, emit)
	flushTimer := time.NewTimer(modelDeltaCoalesceMaxDelay)
	stopModelDeltaTimer(flushTimer)
	defer stopModelDeltaTimer(flushTimer)
	for events != nil {
		var flushC <-chan time.Time
		if deltas.Pending() {
			flushC = flushTimer.C
		}
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			wasPending := deltas.Pending()
			a.collectModelCallEvent(ctx, hookRunner, event, turnID, stepID, &result, deltas, emit)
			switch {
			case deltas.Pending() && !wasPending:
				resetModelDeltaTimer(flushTimer)
			case !deltas.Pending():
				stopModelDeltaTimer(flushTimer)
			}
		case <-flushC:
			deltas.FlushPending()
			stopModelDeltaTimer(flushTimer)
		}
	}
	deltas.FlushBoundary()
	result.Err = <-errs
	if result.Err != nil {
		if metadata, ok := provider.RequestMetadataFromError(result.Err); ok {
			result.RequestMetadata = mergeRequestMetadata(result.RequestMetadata, metadata)
		}
	}
	return result
}

func (a *Agent) collectModelCallEvent(ctx context.Context, hookRunner *runtimehooks.Runner, event provider.Event, turnID, stepID string, result *modelCallStreamResult, deltas *modelDeltaCoalescer, emit func(protocol.Event)) {
	if result == nil {
		return
	}
	switch event.Kind {
	case provider.EventContent:
		if result.FirstDeltaAt.IsZero() {
			result.FirstDeltaAt = time.Now()
		}
		result.Content += event.Text
		deltas.Add(protocol.EventAssistantDelta, event.Text)
	case provider.EventReasoning:
		if result.FirstDeltaAt.IsZero() {
			result.FirstDeltaAt = time.Now()
		}
		result.Reasoning += event.Text
		deltas.Add(protocol.EventAssistantReasoning, event.Text)
	case provider.EventToolCallDelta:
		deltas.FlushBoundary()
		result.Accumulator.Push(event)
	case provider.EventUsage:
		deltas.FlushBoundary()
		if event.Usage.InputTokens > 0 {
			result.PromptTokens = event.Usage.InputTokens
		}
		result.Usage = event.Usage
		emit(protocol.Event{
			Type:   protocol.EventProviderUsageUpdate,
			TurnID: turnID,
			StepID: stepID,
			Data:   event.Usage,
		})
	case provider.EventRequestMetadata:
		deltas.FlushBoundary()
		result.RequestMetadata = event.Request
		if event.Request.Retries > 0 {
			result.HookErr = joinHookError(result.HookErr, hookRunner.Run(ctx, "provider_retry", map[string]any{
				"turn_id":             turnID,
				"step_id":             stepID,
				"request_id":          event.Request.RequestID,
				"provider_id":         event.Request.ProviderID,
				"model_id":            event.Request.ModelID,
				"provider_request_id": event.Request.ProviderRequestID,
				"attempts":            event.Request.Attempts,
				"retries":             event.Request.Retries,
				"status_code":         event.Request.StatusCode,
			}, emit))
		}
	case provider.EventDone:
		deltas.FlushBoundary()
	}
}

type modelDeltaCoalescer struct {
	turnID      string
	stepID      string
	emit        func(protocol.Event)
	currentType protocol.EventType
	pending     strings.Builder
	lastFlush   time.Time
}

func newModelDeltaCoalescer(turnID, stepID string, emit func(protocol.Event)) *modelDeltaCoalescer {
	return &modelDeltaCoalescer{turnID: turnID, stepID: stepID, emit: emit}
}

func (c *modelDeltaCoalescer) Add(eventType protocol.EventType, text string) {
	if c == nil || text == "" {
		return
	}
	now := time.Now()
	if c.currentType != "" && c.currentType != eventType {
		c.flushAt(now)
		c.currentType = ""
	}
	if c.currentType == "" {
		c.currentType = eventType
		c.emitDelta(eventType, text)
		c.lastFlush = now
		return
	}
	c.pending.WriteString(text)
	if c.pending.Len() >= modelDeltaCoalesceMaxBytes || (!c.lastFlush.IsZero() && now.Sub(c.lastFlush) >= modelDeltaCoalesceMaxDelay) {
		c.flushAt(now)
	}
}

func (c *modelDeltaCoalescer) FlushBoundary() {
	if c == nil {
		return
	}
	c.flushAt(time.Now())
	c.currentType = ""
}

func (c *modelDeltaCoalescer) FlushPending() {
	if c == nil {
		return
	}
	c.flushAt(time.Now())
}

func (c *modelDeltaCoalescer) Pending() bool {
	return c != nil && c.pending.Len() > 0
}

func (c *modelDeltaCoalescer) flushAt(now time.Time) {
	if c == nil || c.pending.Len() == 0 || c.currentType == "" {
		return
	}
	c.emitDelta(c.currentType, c.pending.String())
	c.pending.Reset()
	c.lastFlush = now
}

func (c *modelDeltaCoalescer) emitDelta(eventType protocol.EventType, text string) {
	if c == nil || c.emit == nil || text == "" {
		return
	}
	c.emit(protocol.Event{
		Type:   eventType,
		TurnID: c.turnID,
		StepID: c.stepID,
		Data:   text,
	})
}

func resetModelDeltaTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	stopModelDeltaTimer(timer)
	timer.Reset(modelDeltaCoalesceMaxDelay)
}

func stopModelDeltaTimer(timer *time.Timer) {
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

func mergeRequestMetadata(current, fallback provider.RequestMetadata) provider.RequestMetadata {
	if current.RequestID == "" {
		current.RequestID = fallback.RequestID
	}
	if current.ProviderID == "" {
		current.ProviderID = fallback.ProviderID
	}
	if current.ModelID == "" {
		current.ModelID = fallback.ModelID
	}
	if current.ProviderRequestID == "" {
		current.ProviderRequestID = fallback.ProviderRequestID
	}
	if current.Attempts == 0 {
		current.Attempts = fallback.Attempts
	}
	if current.Retries == 0 {
		current.Retries = fallback.Retries
	}
	if current.StatusCode == 0 {
		current.StatusCode = fallback.StatusCode
	}
	return current
}
