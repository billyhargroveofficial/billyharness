package agent

import (
	"context"
	"time"

	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func (a *Agent) Run(ctx context.Context, prompt string, emit func(protocol.Event)) error {
	messages := InitialMessagesFromSettings(a.instructions)
	messages = append(messages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	_, err := a.RunMessages(ctx, messages, emit)
	return err
}

func (a *Agent) RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
	if emit == nil {
		emit = func(protocol.Event) {}
	}
	runID := newAgentRunID()
	submission := runstate.Submission{ID: newAgentSubmissionID(), CreatedAt: time.Now().UTC()}
	run := runstate.Run{ID: runID, SubmissionID: submission.ID, Status: "started", StartedAt: submission.CreatedAt}
	emit = protocol.NewEventEnricherWithEnvelope(protocol.EventEnvelope{
		SubmissionID: submission.ID,
		RunID:        run.ID,
		Source:       protocol.EventSourceAgent,
	}, emit).Emit
	emitAgentRunStarted(run, emit)
	hookRunner := runtimehooks.New(a.hookSettings)
	if err := hookRunner.Run(ctx, "session_start", map[string]any{
		"submission_id": run.SubmissionID,
		"run_id":        run.ID,
		"status":        run.Status,
	}, emit); err != nil {
		emitAgentRunFailed(err, emit)
		return messages, err
	}
	cleanupMCPStatusHook := a.installMCPStatusHook(ctx, hookRunner, run, emit)
	defer cleanupMCPStatusHook()
	messages = a.withMCPInstructions(messages)
	var lastPromptTokens int64
	emittedContextThresholds := map[int]bool{}
	emitContextThresholdEvents(messages, a.runtime, 0, "initial", emittedContextThresholds, emit)
	for round := 0; round < a.runtime.MaxToolRounds; round++ {
		roundNum := round + 1
		turnID := agentTurnID(roundNum)
		turnStarted := time.Now()
		emitContextThresholdEvents(messages, a.runtime, roundNum, "before_turn", emittedContextThresholds, emit)
		var compacted bool
		var compaction *compactionReport
		messages, compaction, compacted = a.compactMessages(ctx, messages, lastPromptTokens)
		if compacted {
			lastPromptTokens = 0
			emit(protocol.Event{Type: protocol.EventContextCompacted, Data: compaction})
		}
		toolSet := a.snapshotToolSet(ctx)
		toolSpecs := toolSet.Specs()
		snapshotInput := a.snapshotInput()
		snapshotInput.MCPStatusSnapshotHash = toolSet.MCPStatusSnapshotHash()
		turnSnapshot := runstate.NewSnapshot(snapshotInput, messages, toolSpecs)
		a.emitTurnStarted(emit, turnID, roundNum, messages, turnSnapshot)
		if err := validateTranscriptPairing(messages); err != nil {
			err = a.failTurn(ctx, hookRunner, run, turnID, roundNum, turnStarted, err, emit)
			return messages, err
		}
		modelStep := a.runModelCallStep(ctx, hookRunner, modelCallStepInput{
			TurnID:       turnID,
			Round:        roundNum,
			Messages:     messages,
			ToolSpecs:    toolSpecs,
			TurnSnapshot: turnSnapshot,
		}, emit)
		if modelStep.PromptTokens > 0 {
			lastPromptTokens = modelStep.PromptTokens
		}
		if err := modelStep.Err; err != nil {
			err = a.failTurn(ctx, hookRunner, run, turnID, roundNum, turnStarted, err, emit)
			return messages, err
		}
		if len(modelStep.ToolCalls) == 0 {
			messages = a.appendModelResponse(messages, modelStep)
			emitContextThresholdEvents(messages, a.runtime, roundNum, "after_final_answer", emittedContextThresholds, emit)
			a.emitTurnCompleted(emit, turnCompletion{
				TurnID:       turnID,
				Round:        roundNum,
				Status:       protocol.TurnStatusCompleted,
				StopReason:   protocol.TurnStopFinalAnswer,
				MessageCount: len(messages),
				Started:      turnStarted,
			})
			if err := a.finishSuccessfulRun(ctx, hookRunner, run, turnID, len(messages), emit); err != nil {
				return messages, err
			}
			return messages, nil
		}
		messages = a.appendModelResponse(messages, modelStep)
		results := a.executeToolCalls(ctx, hookRunner, toolSet, turnID, roundNum, modelStep.ToolCalls, emit)
		messages = appendToolResultMessages(messages, results)
		emitContextThresholdEvents(messages, a.runtime, roundNum, "after_tool_results", emittedContextThresholds, emit)
		a.emitTurnCompleted(emit, turnCompletion{
			TurnID:        turnID,
			Round:         roundNum,
			Status:        protocol.TurnStatusCompleted,
			StopReason:    protocol.TurnStopToolResults,
			MessageCount:  len(messages),
			ToolCallCount: len(modelStep.ToolCalls),
			Started:       turnStarted,
		})
	}
	err := a.failMaxToolRounds(ctx, hookRunner, run, emit)
	return messages, err
}

type toolExecutionResult struct {
	Index      int
	Call       protocol.ToolCall
	Result     protocol.ToolResult
	DurationMS int64
	AttemptID  string
}

func (a *Agent) snapshotToolSet(ctx context.Context) tools.ToolSet {
	if a == nil || a.tools == nil {
		return tools.ToolSet{}
	}
	return a.tools.SnapshotWithToolPolicy(ctx, a.toolPolicy)
}

func (a *Agent) executeToolCalls(ctx context.Context, hookRunner *runtimehooks.Runner, toolSet tools.ToolSet, turnID string, round int, calls []protocol.ToolCall, emit func(protocol.Event)) []toolExecutionResult {
	results := make([]toolExecutionResult, len(calls))
	orchestrator := a.newToolOrchestrator(emit, hookRunner, toolSet)
	for _, call := range calls {
		orchestrator.Request(call)
	}
	for i := 0; i < len(calls); {
		if !a.canRunToolParallel(toolSet, calls[i]) {
			results[i] = a.executeOneTool(ctx, orchestrator, toolSet, turnID, round, i, calls[i], false, "", 0, 0, emit)
			i++
			continue
		}
		j := i + 1
		for j < len(calls) && a.canRunToolParallel(toolSet, calls[j]) {
			j++
		}
		a.executeParallelToolBatch(ctx, orchestrator, toolSet, turnID, round, calls, i, j, results, emit)
		i = j
	}
	return results
}
