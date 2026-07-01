package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const defaultStreamLivenessInterval = 15 * time.Second

type streamLivenessWatchdog struct {
	emit     func(protocol.Event)
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}

	mu        sync.Mutex
	active    bool
	started   time.Time
	lastEvent time.Time
	phase     string
	runID     string
	turnID    string
	stepID    string
	callID    string
	attemptID string
	count     int
}

func (a *Agent) withStreamLiveness(emit func(protocol.Event)) (func(protocol.Event), func()) {
	interval := streamLivenessInterval(a.runtime)
	watchdog := newStreamLivenessWatchdog(interval, emit)
	if watchdog == nil {
		return emit, func() {}
	}
	return watchdog.Emit, watchdog.Close
}

func streamLivenessInterval(limits config.RuntimeLimits) time.Duration {
	if limits.StreamIdleTimeout <= 0 {
		return defaultStreamLivenessInterval
	}
	return limits.StreamIdleTimeout / 2
}

func newStreamLivenessWatchdog(interval time.Duration, emit func(protocol.Event)) *streamLivenessWatchdog {
	if interval <= 0 || emit == nil {
		return nil
	}
	w := &streamLivenessWatchdog{
		emit:     emit,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *streamLivenessWatchdog) Emit(event protocol.Event) {
	if w == nil {
		return
	}
	w.observe(event, time.Now())
	w.emit(event)
}

func (w *streamLivenessWatchdog) Close() {
	if w == nil {
		return
	}
	close(w.stop)
	<-w.done
}

func (w *streamLivenessWatchdog) run() {
	defer close(w.done)
	ticker := time.NewTicker(livenessTickInterval(w.interval))
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			if event, ok := w.heartbeat(now); ok {
				w.emit(event)
			}
		case <-w.stop:
			return
		}
	}
}

func livenessTickInterval(interval time.Duration) time.Duration {
	tick := interval / 2
	if tick < time.Millisecond {
		return time.Millisecond
	}
	return tick
}

func (w *streamLivenessWatchdog) observe(event protocol.Event, now time.Time) {
	event = protocol.EnrichEvent(event, protocol.EventEnvelope{})
	w.mu.Lock()
	defer w.mu.Unlock()
	switch event.Type {
	case protocol.EventRunStarted:
		w.active = true
		w.started = now
		w.runID = event.RunID
		w.phase = "run"
		w.count = 0
	case protocol.EventRunCompleted, protocol.EventRunFailed:
		w.active = false
	default:
		if phase := streamLivenessPhase(event); phase != "" {
			w.phase = phase
		}
	}
	if event.TurnID != "" {
		w.turnID = event.TurnID
	}
	if event.StepID != "" {
		w.stepID = event.StepID
	}
	if event.CallID != "" {
		w.callID = event.CallID
	}
	if event.AttemptID != "" {
		w.attemptID = event.AttemptID
	}
	if w.active {
		w.lastEvent = now
	}
}

func (w *streamLivenessWatchdog) heartbeat(now time.Time) (protocol.Event, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active || w.lastEvent.IsZero() || now.Sub(w.lastEvent) < w.interval {
		return protocol.Event{}, false
	}
	if w.started.IsZero() {
		w.started = w.lastEvent
	}
	w.count++
	idle := now.Sub(w.lastEvent)
	elapsed := now.Sub(w.started)
	w.lastEvent = now
	phase := strings.TrimSpace(w.phase)
	if phase == "" {
		phase = "run"
	}
	data := protocol.StreamStillRunningEvent{
		RunID:      w.runID,
		TurnID:     w.turnID,
		StepID:     w.stepID,
		CallID:     w.callID,
		AttemptID:  w.attemptID,
		Phase:      phase,
		ElapsedMS:  elapsed.Milliseconds(),
		IdleMS:     idle.Milliseconds(),
		IntervalMS: w.interval.Milliseconds(),
		Count:      w.count,
		Message:    fmt.Sprintf("still running: %s", phase),
	}
	return protocol.Event{
		Type:      protocol.EventStreamStillRunning,
		TurnID:    w.turnID,
		StepID:    w.stepID,
		CallID:    w.callID,
		AttemptID: w.attemptID,
		Data:      data,
	}, true
}

func streamLivenessPhase(event protocol.Event) string {
	switch event.Type {
	case protocol.EventModelCallStarted:
		return "model"
	case protocol.EventAssistantDelta:
		return "model_stream"
	case protocol.EventAssistantReasoning:
		return "reasoning_stream"
	case protocol.EventProviderUsageUpdate:
		return "provider_usage"
	case protocol.EventToolCallRequested:
		if call, ok := decodeLivenessToolCall(event.Data); ok && call.Name != "" {
			return "tool_requested:" + call.Name
		}
		return "tool_requested"
	case protocol.EventToolCallStarted:
		return "tool_started"
	case protocol.EventToolCallProgress:
		if progress, ok := decodeLivenessToolProgress(event.Data); ok {
			if progress.Phase != "" {
				return "tool:" + progress.Phase
			}
			if progress.Status != "" {
				return "tool:" + progress.Status
			}
		}
		return "tool"
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		return "tool_done"
	case protocol.EventStepStarted, protocol.EventStepCompleted:
		if step, ok := decodeLivenessStep(event.Data); ok && step.Kind != "" {
			return "step:" + string(step.Kind)
		}
		return "step"
	case protocol.EventHookStarted:
		return "hook"
	case protocol.EventUserInputRequested:
		return "waiting_for_user"
	default:
		return ""
	}
}

func decodeLivenessToolCall(value any) (protocol.ToolCall, bool) {
	var call protocol.ToolCall
	if decodeLivenessData(value, &call) != nil {
		return protocol.ToolCall{}, false
	}
	return call, call.Name != "" || call.ID != ""
}

func decodeLivenessToolProgress(value any) (protocol.ToolProgressEvent, bool) {
	var progress protocol.ToolProgressEvent
	if decodeLivenessData(value, &progress) != nil {
		return protocol.ToolProgressEvent{}, false
	}
	return progress, progress.Phase != "" || progress.Status != "" || progress.CallID != ""
}

func decodeLivenessStep(value any) (protocol.StepEvent, bool) {
	var step protocol.StepEvent
	if decodeLivenessData(value, &step) != nil {
		return protocol.StepEvent{}, false
	}
	return step, step.Kind != "" || step.StepID != ""
}

func decodeLivenessData(value any, out any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, out)
}
