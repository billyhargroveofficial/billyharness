package agent

import (
	"context"
	"fmt"
	"time"

	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
)

type turnCompletion struct {
	TurnID        string
	Round         int
	Status        string
	StopReason    string
	Started       time.Time
	MessageCount  int
	ToolCallCount int
	Err           error
}

func emitAgentRunStarted(run runstate.Run, emit func(protocol.Event)) {
	emit(protocol.Event{Type: protocol.EventRunStarted, Data: map[string]any{
		"submission_id": run.SubmissionID,
		"run_id":        run.ID,
		"status":        run.Status,
	}})
}

func emitAgentRunCompleted(emit func(protocol.Event)) {
	emit(protocol.Event{Type: protocol.EventRunCompleted})
}

func emitAgentRunFailed(err error, emit func(protocol.Event)) {
	emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
}

func (a *Agent) emitTurnStarted(emit func(protocol.Event), turnID string, round int, messages []protocol.Message, snapshot runstate.Snapshot) {
	emit(protocol.Event{Type: protocol.EventTurnStarted, Data: protocol.TurnEvent{
		TurnID:       turnID,
		Round:        round,
		Status:       protocol.TurnStatusStarted,
		Model:        a.modelID(),
		MessageCount: len(messages),
		Metadata:     snapshot.Metadata(),
	}})
}

func (a *Agent) emitTurnCompleted(emit func(protocol.Event), completion turnCompletion) {
	event := protocol.TurnEvent{
		TurnID:        completion.TurnID,
		Round:         completion.Round,
		Status:        completion.Status,
		StopReason:    completion.StopReason,
		Model:         a.modelID(),
		MessageCount:  completion.MessageCount,
		ToolCallCount: completion.ToolCallCount,
		DurationMS:    durationMS(completion.Started),
	}
	if completion.Err != nil {
		event.Error = completion.Err.Error()
	}
	emit(protocol.Event{Type: protocol.EventTurnCompleted, Data: event})
}

func (a *Agent) finishSuccessfulRun(ctx context.Context, hookRunner *runtimehooks.Runner, run runstate.Run, turnID string, messageCount int, emit func(protocol.Event)) error {
	if err := hookRunner.Run(ctx, "session_done", map[string]any{
		"run_id":        run.ID,
		"status":        protocol.StepStatusCompleted,
		"turn_id":       turnID,
		"message_count": messageCount,
	}, emit); err != nil {
		emitAgentRunFailed(err, emit)
		return err
	}
	emitAgentRunCompleted(emit)
	return nil
}

func (a *Agent) failTurn(ctx context.Context, hookRunner *runtimehooks.Runner, run runstate.Run, turnID string, round int, started time.Time, err error, emit func(protocol.Event)) error {
	a.emitTurnCompleted(emit, turnCompletion{
		TurnID:     turnID,
		Round:      round,
		Status:     protocol.TurnStatusFailed,
		StopReason: protocol.TurnStopError,
		Started:    started,
		Err:        err,
	})
	return a.finishFailedRun(ctx, hookRunner, run, turnID, err, emit)
}

func (a *Agent) failMaxToolRounds(ctx context.Context, hookRunner *runtimehooks.Runner, run runstate.Run, emit func(protocol.Event)) error {
	err := fmt.Errorf("exceeded max tool rounds: %d", a.runtime.MaxToolRounds)
	return a.finishFailedRun(ctx, hookRunner, run, "", err, emit)
}

func (a *Agent) finishFailedRun(ctx context.Context, hookRunner *runtimehooks.Runner, run runstate.Run, turnID string, err error, emit func(protocol.Event)) error {
	payload := map[string]any{
		"run_id": run.ID,
		"status": protocol.StepStatusFailed,
		"error":  err.Error(),
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}
	err = joinHookError(err, hookRunner.Run(ctx, "session_done", payload, emit))
	emitAgentRunFailed(err, emit)
	return err
}
