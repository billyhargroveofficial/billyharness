package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/projectcontext"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type PromptBlockedError struct {
	Reason string
}

func (e PromptBlockedError) Error() string {
	if e.Reason == "" {
		return "prompt blocked by user_prompt_submit hook"
	}
	return "prompt blocked by user_prompt_submit hook: " + e.Reason
}

func (e PromptBlockedError) DiscardPromptHistory() bool {
	return true
}

func (a *Agent) Run(ctx context.Context, prompt string, emit func(protocol.Event)) error {
	return a.RunWithPromptOptions(ctx, prompt, PromptSubmitOptions{Source: "direct"}, emit)
}

func (a *Agent) RunWithPromptOptions(ctx context.Context, prompt string, opts PromptSubmitOptions, emit func(protocol.Event)) error {
	messages := InitialMessagesFromSettings(a.instructions)
	messages = append(messages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	_, err := a.RunMessagesWithPromptOptions(ctx, messages, opts, emit)
	return err
}

func (a *Agent) RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
	return a.RunMessagesWithPromptOptions(ctx, messages, PromptSubmitOptions{Source: "direct"}, emit)
}

func (a *Agent) RunMessagesWithPromptOptions(ctx context.Context, messages []protocol.Message, opts PromptSubmitOptions, emit func(protocol.Event)) ([]protocol.Message, error) {
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
	var stopLiveness func()
	emit, stopLiveness = a.withStreamLiveness(emit)
	defer stopLiveness()
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
	messages, err := a.applyPromptSubmitHook(ctx, hookRunner, run, messages, opts, emit)
	if err != nil {
		emitAgentRunFailed(err, emit)
		return messages, err
	}
	messages, _ = projectcontext.ReconcileMessages(a.instructions, messages)
	cleanupMCPStatusHook := a.installMCPStatusHook(ctx, hookRunner, run, emit)
	defer cleanupMCPStatusHook()
	messages = a.withMCPInstructions(messages)
	var lastPromptTokens int64
	var previousTurnSnapshot *runstate.Snapshot
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
			if usage, ok := compaction.helperUsageEvent(run.ID); ok {
				emit(protocol.Event{Type: protocol.EventProviderHelperUsage, Data: usage})
			}
		}
		toolSet := a.snapshotToolSet(ctx)
		toolSpecs := toolSet.Specs()
		snapshotInput := a.snapshotInput()
		snapshotInput.MCPStatusSnapshotHash = toolSet.MCPStatusSnapshotHash()
		turnSnapshot := runstate.NewSnapshot(snapshotInput, messages, toolSpecs).WithPromptCacheBreak(previousTurnSnapshot)
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
		previous := turnSnapshot
		previousTurnSnapshot = &previous
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
		results := a.executeToolCalls(ctx, hookRunner, toolSet, run.ID, turnID, roundNum, modelStep.ToolCalls, emit)
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
	err = a.failMaxToolRounds(ctx, hookRunner, run, emit)
	return messages, err
}

func (a *Agent) applyPromptSubmitHook(ctx context.Context, hookRunner *runtimehooks.Runner, run runstate.Run, messages []protocol.Message, opts PromptSubmitOptions, emit func(protocol.Event)) ([]protocol.Message, error) {
	index := lastUserMessageIndex(messages)
	if index < 0 {
		return messages, nil
	}
	cwd, _ := os.Getwd()
	source := opts.Source
	if source == "" {
		source = "direct"
	}
	result, err := hookRunner.RunPromptSubmit(ctx, runtimehooks.PromptSubmitInput{
		Prompt:       messages[index].Content,
		CWD:          cwd,
		ModelID:      a.modelID(),
		Profile:      a.profile.Profile,
		SubmissionID: run.SubmissionID,
		RunID:        run.ID,
		Source:       source,
		AccessMode:   a.toolPolicy.AccessMode,
		Metadata:     clonePromptSubmitMetadata(opts.Metadata),
	}, emit)
	if err != nil {
		return removeMessageAt(messages, index), err
	}
	if result.Blocked {
		return removeMessageAt(messages, index), PromptBlockedError{Reason: result.Reason}
	}
	next := cloneProtocolMessages(messages)
	if result.Prompt != "" {
		next[index].Content = result.Prompt
	}
	if result.AdditionalContext != "" {
		contextMessage := protocol.Message{
			Role:    protocol.RoleUser,
			Content: fmt.Sprintf("# user_prompt_submit hook context\n\n%s", result.AdditionalContext),
		}
		next = insertMessageAt(next, index, contextMessage)
	}
	return next, nil
}

func lastUserMessageIndex(messages []protocol.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == protocol.RoleUser {
			return i
		}
	}
	return -1
}

func removeMessageAt(messages []protocol.Message, index int) []protocol.Message {
	if index < 0 || index >= len(messages) {
		return cloneProtocolMessages(messages)
	}
	out := make([]protocol.Message, 0, len(messages)-1)
	out = append(out, messages[:index]...)
	out = append(out, messages[index+1:]...)
	return cloneProtocolMessages(out)
}

func insertMessageAt(messages []protocol.Message, index int, message protocol.Message) []protocol.Message {
	if index < 0 || index > len(messages) {
		index = len(messages)
	}
	out := make([]protocol.Message, 0, len(messages)+1)
	out = append(out, messages[:index]...)
	out = append(out, message)
	out = append(out, messages[index:]...)
	return out
}

func cloneProtocolMessages(messages []protocol.Message) []protocol.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]protocol.Message, len(messages))
	copy(out, messages)
	return out
}

func clonePromptSubmitMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

type toolExecutionResult struct {
	Index      int
	Call       protocol.ToolCall
	Result     protocol.ToolResult
	DurationMS int64
	AttemptID  string
	RunID      string
	TurnID     string
	StepID     string
}

func (a *Agent) snapshotToolSet(ctx context.Context) tools.ToolSet {
	if a == nil || a.tools == nil {
		return tools.ToolSet{}
	}
	return a.tools.SnapshotWithToolPolicy(ctx, a.toolPolicy)
}

func (a *Agent) executeToolCalls(ctx context.Context, hookRunner *runtimehooks.Runner, toolSet tools.ToolSet, runID, turnID string, round int, calls []protocol.ToolCall, emit func(protocol.Event)) []toolExecutionResult {
	results := make([]toolExecutionResult, len(calls))
	orchestrator := a.newToolOrchestrator(emit, hookRunner, toolSet)
	for _, call := range calls {
		orchestrator.Request(call)
	}
	for i := 0; i < len(calls); {
		if !a.canRunToolParallel(toolSet, calls[i]) {
			results[i] = a.executeOneTool(ctx, orchestrator, toolSet, runID, turnID, round, i, calls[i], false, "", 0, 0, emit)
			i++
			continue
		}
		j := i + 1
		for j < len(calls) && a.canRunToolParallel(toolSet, calls[j]) {
			j++
		}
		a.executeParallelToolBatch(ctx, orchestrator, toolSet, runID, turnID, round, calls, i, j, results, emit)
		i = j
	}
	return results
}
