package provider

import "fmt"

// NaturalFinishGuard validates helper-model streams that may return text but
// must never delegate work to tools. It requires exactly one explicit natural
// EventDone and rejects every event after it.
type NaturalFinishGuard struct {
	done bool
}

// Observe validates the control-flow meaning of one provider event. Callers
// should invoke it before consuming the event payload.
func (g *NaturalFinishGuard) Observe(event Event) error {
	if g == nil {
		return fmt.Errorf("nil natural finish guard")
	}
	if g.done {
		if event.Kind == EventDone {
			return fmt.Errorf("provider stream emitted multiple done events")
		}
		return fmt.Errorf("provider stream emitted event kind %d after done", event.Kind)
	}
	switch event.Kind {
	case EventToolCallDelta:
		return &FinishError{Finish: Finish{Kind: FinishToolCalls, RawReason: "unexpected_helper_tool_call"}}
	case EventDone:
		g.done = true
		finish := NormalizeFinish(event.Finish)
		if finish.Kind == FinishNatural {
			return nil
		}
		if err := FinishErrorFor(finish); err != nil {
			return err
		}
		return &FinishError{Finish: finish}
	default:
		return nil
	}
}

// Complete must be called after both provider channels close successfully.
func (g *NaturalFinishGuard) Complete() error {
	if g == nil || !g.done {
		return &FinishError{Finish: Finish{Kind: FinishUnknown, RawReason: "stream_closed_without_done"}}
	}
	return nil
}
