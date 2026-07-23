package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
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
	Finish       provider.Finish
	FinishLegacy bool
	PromptTokens int64
	Err          error
}

// ModelFinishMismatchError reports a successful provider termination whose
// declared finish kind contradicts the assembled response. It is distinct from
// provider.FinishError, which classifies unsuccessful provider terminations.
type ModelFinishMismatchError struct {
	Finish        provider.Finish
	ToolCallCount int
}

func (e *ModelFinishMismatchError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("model finish %q contradicts %d parsed tool calls", e.Finish.Kind, e.ToolCallCount)
}

func (a *Agent) validateModelInputCapabilities(messages []protocol.Message) error {
	if protocol.MessageImageAttachmentCount(messages) == 0 {
		return nil
	}
	return modelinfo.ValidateCapabilityPolicy(modelinfo.CapabilityPolicyRequest{
		Provider:           a.providerID(),
		Model:              a.modelID(),
		RequireVisionInput: true,
	})
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
		Data:   modelCallEventData(modelCallBase, protocol.StepStatusStarted, -1, -1, provider.Usage{}, provider.RequestMetadata{}, provider.Finish{}, false, ""),
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
		Finish:       stream.Finish,
		FinishLegacy: stream.FinishLegacy,
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
	calls, err := stream.Accumulator.Finish()
	if err != nil {
		result.Err = err
		a.emitModelCallStepFailed(input, stepID, modelCallBase, started, stream, err, emit)
		return result
	}
	if err := validateExecutableToolCalls(calls); err != nil {
		result.Err = err
		a.emitModelCallStepFailed(input, stepID, modelCallBase, started, stream, err, emit)
		return result
	}
	finish, legacy, err := resolveModelFinish(stream.Finish, stream.FinishSeen, len(calls), a.allowLegacyModelFinish())
	stream.Finish = finish
	stream.FinishLegacy = legacy
	result.Finish = finish
	result.FinishLegacy = legacy
	if err != nil {
		result.Err = err
		a.emitModelCallStepFailed(input, stepID, modelCallBase, started, stream, err, emit)
		return result
	}
	result.ToolCalls = calls
	emit(protocol.Event{
		Type:   protocol.EventModelCallFinished,
		TurnID: input.TurnID,
		StepID: stepID,
		Data:   modelCallEventData(modelCallBase, protocol.StepStatusCompleted, durationMS(started), firstDeltaLatencyMS(started, stream.FirstDeltaAt), stream.Usage, stream.RequestMetadata, finish, legacy, ""),
	})
	modelMetadata := map[string]any{
		"content_chars":   len(stream.Content),
		"reasoning_chars": len(stream.Reasoning),
		"tool_call_count": len(calls),
	}
	for key, value := range modelCallEventMetadata(modelCallEventData(modelCallBase, protocol.StepStatusCompleted, durationMS(started), firstDeltaLatencyMS(started, stream.FirstDeltaAt), stream.Usage, stream.RequestMetadata, finish, legacy, "")) {
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

func validateExecutableToolCalls(calls []protocol.ToolCall) error {
	seen := map[string]int{}
	for i, call := range calls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		if previous, ok := seen[id]; ok {
			return fmt.Errorf("duplicate tool call id %q at indexes %d and %d", id, previous, i)
		}
		seen[id] = i
	}
	return nil
}

func (a *Agent) allowLegacyModelFinish() bool {
	if a == nil {
		return false
	}
	if modelinfo.NormalizeProvider(a.providerID()) == modelinfo.ProviderMock {
		return true
	}
	// Package-local test doubles may opt into the narrow compatibility path.
	// Real provider implementations cannot accidentally satisfy this marker.
	_, ok := a.provider.(legacyModelFinishProvider)
	return ok
}

type legacyModelFinishProvider interface {
	allowLegacyModelFinish()
}

func resolveModelFinish(finish provider.Finish, seen bool, toolCallCount int, allowLegacy bool) (provider.Finish, bool, error) {
	if !seen {
		finish = provider.Finish{Kind: provider.FinishUnknown, RawReason: "stream_closed_without_done"}
		return finish, false, provider.FinishErrorFor(finish)
	}
	finish = provider.NormalizeFinish(finish)
	legacy := finish == (provider.Finish{})
	if legacy {
		if !allowLegacy {
			finish = provider.Finish{Kind: provider.FinishUnknown, RawReason: "legacy_zero_not_allowed"}
			return finish, false, provider.FinishErrorFor(finish)
		}
		if toolCallCount > 0 {
			finish = provider.Finish{Kind: provider.FinishToolCalls, RawReason: "legacy_zero"}
		} else {
			finish = provider.FinishOrLegacyNatural(finish)
		}
	}
	if err := provider.FinishErrorFor(finish); err != nil {
		if normalized, ok := provider.FinishFromError(err); ok {
			finish = normalized
		}
		return finish, legacy, err
	}
	if finish.Kind == provider.FinishNatural && toolCallCount > 0 {
		return finish, legacy, &ModelFinishMismatchError{Finish: finish, ToolCallCount: toolCallCount}
	}
	if finish.Kind == provider.FinishToolCalls && toolCallCount == 0 {
		return finish, legacy, &ModelFinishMismatchError{Finish: finish, ToolCallCount: toolCallCount}
	}
	return finish, legacy, nil
}

func (a *Agent) emitModelCallStepFailed(input modelCallStepInput, stepID string, base map[string]any, started time.Time, stream modelCallStreamResult, err error, emit func(protocol.Event)) {
	emit(protocol.Event{
		Type:   protocol.EventModelCallFinished,
		TurnID: input.TurnID,
		StepID: stepID,
		Data:   modelCallEventData(base, protocol.StepStatusFailed, durationMS(started), firstDeltaLatencyMS(started, stream.FirstDeltaAt), stream.Usage, stream.RequestMetadata, stream.Finish, stream.FinishLegacy, err.Error()),
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
	Finish          provider.Finish
	FinishSeen      bool
	FinishLegacy    bool
	HookErr         error
	Err             error
}

func (a *Agent) collectModelCallStream(ctx context.Context, hookRunner *runtimehooks.Runner, req provider.Request, turnID, stepID string, emit func(protocol.Event)) modelCallStreamResult {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	events, errs := a.provider.Stream(streamCtx, req)
	var result modelCallStreamResult
	deltas := newModelDeltaCoalescer(turnID, stepID, emit)
	flushTimer := time.NewTimer(modelDeltaCoalesceMaxDelay)
	stopModelDeltaTimer(flushTimer)
	defer stopModelDeltaTimer(flushTimer)
	result.Err = provider.DrainStream(ctx, events, errs, provider.StreamDrainOptions{
		FlushC: func() <-chan time.Time {
			if deltas.Pending() {
				return flushTimer.C
			}
			return nil
		},
		OnFlush: func() error {
			deltas.FlushPending()
			stopModelDeltaTimer(flushTimer)
			return nil
		},
		OnEvent: func(event provider.Event) error {
			wasPending := deltas.Pending()
			if err := a.collectModelCallEvent(ctx, hookRunner, event, turnID, stepID, &result, deltas, emit); err != nil {
				return err
			}
			switch {
			case deltas.Pending() && !wasPending:
				resetModelDeltaTimer(flushTimer)
			case !deltas.Pending():
				stopModelDeltaTimer(flushTimer)
			}
			return nil
		},
	})
	deltas.FlushBoundary()
	if result.Err != nil {
		if metadata, ok := provider.RequestMetadataFromError(result.Err); ok {
			result.RequestMetadata = mergeRequestMetadata(result.RequestMetadata, metadata)
		}
	}
	return result
}

func (a *Agent) collectModelCallEvent(ctx context.Context, hookRunner *runtimehooks.Runner, event provider.Event, turnID, stepID string, result *modelCallStreamResult, deltas *modelDeltaCoalescer, emit func(protocol.Event)) error {
	if result == nil {
		return nil
	}
	if result.FinishSeen {
		if event.Kind == provider.EventDone {
			return fmt.Errorf("provider stream emitted multiple done events")
		}
		return fmt.Errorf("provider stream emitted event kind %d after done", event.Kind)
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
		result.Finish = provider.NormalizeFinish(event.Finish)
		result.FinishSeen = true
	}
	return nil
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
