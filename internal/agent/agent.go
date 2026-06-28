package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/instructions"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Agent struct {
	cfg      config.Config
	provider provider.Provider
	tools    *tools.Registry
}

func New(cfg config.Config, provider provider.Provider, registry *tools.Registry) *Agent {
	return &Agent{cfg: cfg, provider: provider, tools: registry}
}

func (a *Agent) Run(ctx context.Context, prompt string, emit func(protocol.Event)) error {
	messages := InitialMessages(a.cfg)
	messages = append(messages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	_, err := a.RunMessages(ctx, messages, emit)
	return err
}

func InitialMessages(cfgs ...config.Config) []protocol.Message {
	messages := []protocol.Message{{Role: protocol.RoleSystem, Content: systemPrompt()}}
	if len(cfgs) > 0 {
		if msg, ok := instructions.ProfileMessage(cfgs[0]); ok {
			messages = append(messages, msg)
		}
		if msg, ok := instructions.Message(cfgs[0]); ok {
			messages = append(messages, msg)
		}
	}
	return messages
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
	emit(protocol.Event{Type: protocol.EventRunStarted, Data: map[string]any{
		"submission_id": run.SubmissionID,
		"run_id":        run.ID,
		"status":        run.Status,
	}})
	messages = a.withMCPInstructions(messages)
	var lastPromptTokens int64
	for round := 0; round < a.cfg.MaxToolRounds; round++ {
		roundNum := round + 1
		turnID := agentTurnID(roundNum)
		turnStarted := time.Now()
		var compacted bool
		var compaction *compactionReport
		messages, compaction, compacted = compactMessages(messages, a.cfg, lastPromptTokens)
		if compacted {
			lastPromptTokens = 0
			emit(protocol.Event{Type: protocol.EventContextCompacted, Data: compaction})
		}
		toolSpecs := a.tools.Specs()
		turnSnapshot := runstate.NewSnapshot(a.cfg, messages, toolSpecs)
		emit(protocol.Event{Type: protocol.EventTurnStarted, Data: protocol.TurnEvent{
			TurnID:       turnID,
			Round:        roundNum,
			Status:       protocol.TurnStatusStarted,
			Model:        a.cfg.Model,
			MessageCount: len(messages),
			Metadata:     turnSnapshot.Metadata(),
		}})
		modelStepID := agentStepID(turnID, protocol.StepKindModelCall, 1)
		requestID := agentRequestID(turnID, roundNum)
		modelCallBase := a.modelCallMetadata(requestID, roundNum, len(messages), len(toolSpecs), turnSnapshot)
		modelStarted := time.Now()
		emit(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
			TurnID:       turnID,
			StepID:       modelStepID,
			Round:        roundNum,
			Kind:         protocol.StepKindModelCall,
			Status:       protocol.StepStatusStarted,
			Name:         a.cfg.Model,
			MessageCount: len(messages),
			Metadata:     copyMap(modelCallBase),
		}})
		emit(protocol.Event{
			Type:   protocol.EventModelCallStarted,
			TurnID: turnID,
			StepID: modelStepID,
			Data:   modelCallEventData(modelCallBase, protocol.StepStatusStarted, -1, -1, provider.Usage{}, provider.RequestMetadata{}, ""),
		})
		events, errs := a.provider.Stream(ctx, provider.Request{
			RequestID: requestID,
			Model:     a.cfg.Model,
			Messages:  messages,
			Tools:     toolSpecs,
		})
		var content string
		var reasoning string
		var firstDeltaAt time.Time
		var lastUsage provider.Usage
		var requestMeta provider.RequestMetadata
		var acc provider.ToolAccumulator
		for event := range events {
			switch event.Kind {
			case provider.EventContent:
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
				}
				content += event.Text
				emit(protocol.Event{
					Type:   protocol.EventAssistantDelta,
					TurnID: turnID,
					StepID: modelStepID,
					Data:   event.Text,
				})
			case provider.EventReasoning:
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
				}
				reasoning += event.Text
				emit(protocol.Event{
					Type:   protocol.EventAssistantReasoning,
					TurnID: turnID,
					StepID: modelStepID,
					Data:   event.Text,
				})
			case provider.EventToolCallDelta:
				acc.Push(event)
			case provider.EventUsage:
				if event.Usage.InputTokens > 0 {
					lastPromptTokens = event.Usage.InputTokens
				}
				lastUsage = event.Usage
				emit(protocol.Event{
					Type:   protocol.EventProviderUsageUpdate,
					TurnID: turnID,
					StepID: modelStepID,
					Data:   event.Usage,
				})
			case provider.EventRequestMetadata:
				requestMeta = event.Request
			case provider.EventDone:
			}
		}
		if err := <-errs; err != nil {
			emit(protocol.Event{
				Type:   protocol.EventModelCallFinished,
				TurnID: turnID,
				StepID: modelStepID,
				Data:   modelCallEventData(modelCallBase, protocol.StepStatusFailed, durationMS(modelStarted), firstDeltaLatencyMS(modelStarted, firstDeltaAt), lastUsage, requestMeta, err.Error()),
			})
			emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
				TurnID:     turnID,
				StepID:     modelStepID,
				Round:      roundNum,
				Kind:       protocol.StepKindModelCall,
				Status:     protocol.StepStatusFailed,
				Name:       a.cfg.Model,
				DurationMS: durationMS(modelStarted),
				Error:      err.Error(),
			}})
			emit(protocol.Event{Type: protocol.EventTurnCompleted, Data: protocol.TurnEvent{
				TurnID:     turnID,
				Round:      roundNum,
				Status:     protocol.TurnStatusFailed,
				StopReason: protocol.TurnStopError,
				Model:      a.cfg.Model,
				DurationMS: durationMS(turnStarted),
				Error:      err.Error(),
			}})
			emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
			return messages, err
		}
		emit(protocol.Event{
			Type:   protocol.EventModelCallFinished,
			TurnID: turnID,
			StepID: modelStepID,
			Data:   modelCallEventData(modelCallBase, protocol.StepStatusCompleted, durationMS(modelStarted), firstDeltaLatencyMS(modelStarted, firstDeltaAt), lastUsage, requestMeta, ""),
		})
		calls, err := acc.Finish()
		if err != nil {
			emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
				TurnID:     turnID,
				StepID:     modelStepID,
				Round:      roundNum,
				Kind:       protocol.StepKindModelCall,
				Status:     protocol.StepStatusFailed,
				Name:       a.cfg.Model,
				DurationMS: durationMS(modelStarted),
				Error:      err.Error(),
			}})
			emit(protocol.Event{Type: protocol.EventTurnCompleted, Data: protocol.TurnEvent{
				TurnID:     turnID,
				Round:      roundNum,
				Status:     protocol.TurnStatusFailed,
				StopReason: protocol.TurnStopError,
				Model:      a.cfg.Model,
				DurationMS: durationMS(turnStarted),
				Error:      err.Error(),
			}})
			emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
			return messages, err
		}
		modelMetadata := map[string]any{
			"content_chars":   len(content),
			"reasoning_chars": len(reasoning),
			"tool_call_count": len(calls),
		}
		for key, value := range modelCallEventData(modelCallBase, protocol.StepStatusCompleted, durationMS(modelStarted), firstDeltaLatencyMS(modelStarted, firstDeltaAt), lastUsage, requestMeta, "") {
			modelMetadata[key] = value
		}
		if !firstDeltaAt.IsZero() {
			modelMetadata["first_delta_ms"] = elapsedMS(modelStarted, firstDeltaAt)
		}
		emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
			TurnID:     turnID,
			StepID:     modelStepID,
			Round:      roundNum,
			Kind:       protocol.StepKindModelCall,
			Status:     protocol.StepStatusCompleted,
			Name:       a.cfg.Model,
			DurationMS: durationMS(modelStarted),
			Metadata:   modelMetadata,
		}})
		if len(calls) == 0 {
			messages = append(messages, protocol.Message{
				Role:             protocol.RoleAssistant,
				Content:          content,
				ReasoningContent: optionalReasoning(a.cfg, reasoning),
			})
			emit(protocol.Event{Type: protocol.EventTurnCompleted, Data: protocol.TurnEvent{
				TurnID:       turnID,
				Round:        roundNum,
				Status:       protocol.TurnStatusCompleted,
				StopReason:   protocol.TurnStopFinalAnswer,
				Model:        a.cfg.Model,
				MessageCount: len(messages),
				DurationMS:   durationMS(turnStarted),
			}})
			emit(protocol.Event{Type: protocol.EventRunCompleted})
			return messages, nil
		}
		messages = append(messages, protocol.Message{
			Role:             protocol.RoleAssistant,
			Content:          content,
			ReasoningContent: optionalReasoning(a.cfg, reasoning),
			ToolCalls:        calls,
		})
		results := a.executeToolCalls(ctx, turnID, roundNum, calls, emit)
		for _, result := range results {
			messages = append(messages, protocol.Message{
				Role:       protocol.RoleTool,
				Content:    result.Result.Content,
				ToolCallID: result.Call.ID,
				Name:       result.Call.Name,
			})
		}
		emit(protocol.Event{Type: protocol.EventTurnCompleted, Data: protocol.TurnEvent{
			TurnID:        turnID,
			Round:         roundNum,
			Status:        protocol.TurnStatusCompleted,
			StopReason:    protocol.TurnStopToolResults,
			Model:         a.cfg.Model,
			MessageCount:  len(messages),
			ToolCallCount: len(calls),
			DurationMS:    durationMS(turnStarted),
		}})
	}
	err := fmt.Errorf("exceeded max tool rounds: %d", a.cfg.MaxToolRounds)
	emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
	return messages, err
}

type toolExecutionResult struct {
	Index      int
	Call       protocol.ToolCall
	Result     protocol.ToolResult
	DurationMS int64
	AttemptID  string
}

func (a *Agent) executeToolCalls(ctx context.Context, turnID string, round int, calls []protocol.ToolCall, emit func(protocol.Event)) []toolExecutionResult {
	results := make([]toolExecutionResult, len(calls))
	orchestrator := a.newToolOrchestrator(emit)
	for _, call := range calls {
		orchestrator.Request(call)
	}
	for i := 0; i < len(calls); {
		if !a.canRunToolParallel(calls[i]) {
			results[i] = a.executeOneTool(ctx, orchestrator, turnID, round, i, calls[i], false, "", 0, 0, emit)
			i++
			continue
		}
		j := i + 1
		for j < len(calls) && a.canRunToolParallel(calls[j]) {
			j++
		}
		a.executeParallelToolBatch(ctx, orchestrator, turnID, round, calls, i, j, results, emit)
		i = j
	}
	return results
}

type toolOrchestrator struct {
	agent     *Agent
	emit      func(protocol.Event)
	decisions map[string]toolPermissionDecision
}

type toolPermissionDecision struct {
	CallID           string
	Name             string
	Risk             protocol.Risk
	KnownRisk        bool
	RequiresApproval bool
	Decision         string
	Source           string
	Reason           string
}

func (a *Agent) newToolOrchestrator(emit func(protocol.Event)) *toolOrchestrator {
	return &toolOrchestrator{
		agent:     a,
		emit:      emit,
		decisions: map[string]toolPermissionDecision{},
	}
}

func (o *toolOrchestrator) Request(call protocol.ToolCall) {
	if o == nil || o.emit == nil {
		return
	}
	o.emit(protocol.Event{Type: protocol.EventToolCallRequested, CallID: call.ID, Data: call})
	decision := o.permissionDecision(call)
	o.decisions[call.ID] = decision
	o.emit(protocol.Event{Type: protocol.EventToolPermissionRequested, Data: decision.requestEventData()})
	o.emit(protocol.Event{Type: protocol.EventToolPermissionDecided, Data: decision.decisionEventData()})
	if o.agent != nil {
		o.agent.emitToolAudit(call, o.emit)
	}
}

func (o *toolOrchestrator) EmitAttemptStarted(call protocol.ToolCall, attemptID string) {
	if o == nil || o.emit == nil {
		return
	}
	o.emit(protocol.Event{
		Type:      protocol.EventToolCallStarted,
		CallID:    call.ID,
		AttemptID: attemptID,
		Data:      call.Name,
	})
}

func (o *toolOrchestrator) Execute(ctx context.Context, index int, call protocol.ToolCall, attemptID string) toolExecutionResult {
	started := time.Now()
	decision := o.decision(call)
	var out protocol.ToolResult
	if decision.Decision == "deny" {
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   dangerousToolDisabledMessage(),
			IsError:   true,
			ErrorCode: "permission_denied",
			Metadata:  map[string]any{},
		}
	} else if o == nil || o.agent == nil || o.agent.tools == nil {
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   "tool registry unavailable",
			IsError:   true,
			ErrorCode: "tool_registry_unavailable",
			Metadata:  map[string]any{},
		}
	} else {
		result, err := o.agent.tools.Call(ctx, call)
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   result.Content,
			IsError:   result.IsError,
			ErrorCode: result.ErrorCode,
			Metadata:  result.Metadata,
			Truncated: result.Truncated,
			OutputRef: result.OutputRef,
		}
		if err != nil {
			out.IsError = true
			if out.ErrorCode == "" {
				if ctx.Err() != nil {
					out.ErrorCode = "tool_aborted"
				} else {
					out.ErrorCode = "tool_error"
				}
			}
			if out.Content == "" {
				out.Content = err.Error()
			}
		}
		o.agent.compactToolResult(index, call, &out)
	}
	ensureToolMetadata(&out)
	out.Metadata["attempt_id"] = attemptID
	out.Metadata["permission_decision"] = decision.Decision
	out.Metadata["permission_source"] = decision.Source
	out.Metadata["permission_reason"] = decision.Reason
	if decision.KnownRisk {
		out.Metadata["risk"] = decision.Risk
	}
	out.Metadata["output_bytes"] = len(out.Content)
	out.Metadata["output_estimated_tokens"] = estimateMessagesTokens([]protocol.Message{{Role: protocol.RoleTool, Content: out.Content}})
	return toolExecutionResult{Index: index, Call: call, Result: out, DurationMS: durationMS(started), AttemptID: attemptID}
}

func (o *toolOrchestrator) EmitAttemptFinished(result toolExecutionResult) {
	if o == nil || o.emit == nil {
		return
	}
	if result.Result.OutputRef != "" {
		o.emit(protocol.Event{Type: protocol.EventToolOutputRefCreated, Data: map[string]any{
			"call_id":    result.Call.ID,
			"name":       result.Call.Name,
			"attempt_id": result.AttemptID,
			"output_ref": result.Result.OutputRef,
			"truncated":  result.Result.Truncated,
		}})
	}
	if result.Result.IsError {
		eventType := protocol.EventToolCallFailed
		if result.Result.ErrorCode == "tool_aborted" {
			eventType = protocol.EventToolCallAborted
		}
		o.emit(protocol.Event{
			Type:      eventType,
			CallID:    result.Call.ID,
			AttemptID: result.AttemptID,
			Data:      result.Result,
		})
	}
	o.emit(protocol.Event{
		Type:      protocol.EventToolCallFinished,
		CallID:    result.Call.ID,
		AttemptID: result.AttemptID,
		Data:      result.Result,
	})
}

func (o *toolOrchestrator) StepMetadata(call protocol.ToolCall, attemptID string, base map[string]any) map[string]any {
	metadata := map[string]any{}
	for key, value := range base {
		metadata[key] = value
	}
	decision := o.decision(call)
	metadata["attempt_id"] = attemptID
	metadata["permission_decision"] = decision.Decision
	metadata["permission_source"] = decision.Source
	metadata["permission_reason"] = decision.Reason
	if decision.KnownRisk {
		metadata["risk"] = decision.Risk
	}
	return metadata
}

func (o *toolOrchestrator) decision(call protocol.ToolCall) toolPermissionDecision {
	if o != nil {
		if decision, ok := o.decisions[call.ID]; ok {
			return decision
		}
		return o.permissionDecision(call)
	}
	return toolPermissionDecision{
		CallID:   call.ID,
		Name:     call.Name,
		Decision: "allow",
		Source:   "auto",
		Reason:   "no_orchestrator",
	}
}

func (o *toolOrchestrator) permissionDecision(call protocol.ToolCall) toolPermissionDecision {
	decision := toolPermissionDecision{
		CallID:   call.ID,
		Name:     call.Name,
		Decision: "allow",
		Source:   "auto",
		Reason:   "safe_or_existing_policy",
	}
	if o == nil || o.agent == nil || o.agent.tools == nil {
		decision.Reason = "tool_registry_unavailable"
		return decision
	}
	risk, ok := o.agent.tools.Risk(call.Name)
	if !ok {
		decision.Reason = "unknown_tool_checked_at_execution"
		return decision
	}
	decision.Risk = risk
	decision.KnownRisk = true
	switch risk {
	case protocol.RiskWrite, protocol.RiskExecute:
		decision.RequiresApproval = true
		if !o.agent.cfg.AutoApproveDangerous {
			decision.Decision = "deny"
			decision.Source = "config"
			decision.Reason = "dangerous_tools_disabled"
			return decision
		}
		decision.Source = "config"
		decision.Reason = "auto_approve_dangerous"
	case protocol.RiskExternal:
		decision.RequiresApproval = true
		decision.Reason = "external_tool_allowed_by_existing_policy"
	default:
		decision.Reason = "safe_tool"
	}
	return decision
}

func (d toolPermissionDecision) requestEventData() map[string]any {
	data := map[string]any{
		"call_id":           d.CallID,
		"name":              d.Name,
		"requires_approval": d.RequiresApproval,
	}
	if d.KnownRisk {
		data["risk"] = d.Risk
	}
	return data
}

func (d toolPermissionDecision) decisionEventData() map[string]any {
	data := d.requestEventData()
	data["decision"] = d.Decision
	data["source"] = d.Source
	data["reason"] = d.Reason
	return data
}

func ensureToolMetadata(out *protocol.ToolResult) {
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
}

func dangerousToolDisabledMessage() string {
	return "tool disabled; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=true or unset FAST_AGENT_AUTO_APPROVE_DANGEROUS to enable write/execute tools"
}

func (a *Agent) emitToolAudit(call protocol.ToolCall, emit func(protocol.Event)) {
	if a == nil || a.tools == nil || emit == nil {
		return
	}
	risk, ok := a.tools.Risk(call.Name)
	if !ok {
		return
	}
	switch risk {
	case protocol.RiskWrite, protocol.RiskExecute, protocol.RiskExternal:
	default:
		return
	}
	emit(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"call_id":       call.ID,
		"name":          call.Name,
		"risk":          risk,
		"auto_approved": a.cfg.AutoApproveDangerous,
	}})
}

func (a *Agent) executeParallelToolBatch(ctx context.Context, orchestrator *toolOrchestrator, turnID string, round int, calls []protocol.ToolCall, start, end int, results []toolExecutionResult, emit func(protocol.Event)) {
	limit := a.cfg.MaxParallelTools
	if limit <= 1 || end-start == 1 {
		for i := start; i < end; i++ {
			results[i] = a.executeOneTool(ctx, orchestrator, turnID, round, i, calls[i], false, "", 0, 0, emit)
		}
		return
	}
	if limit > end-start {
		limit = end - start
	}
	batchID := agentStepID(turnID, protocol.StepKindToolBatch, start+1)
	batchStarted := time.Now()
	batchSize := end - start
	emit(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
		TurnID:        turnID,
		StepID:        batchID,
		Round:         round,
		Index:         start,
		Kind:          protocol.StepKindToolBatch,
		Status:        protocol.StepStatusStarted,
		BatchID:       batchID,
		BatchSize:     batchSize,
		Parallel:      true,
		ParallelLimit: limit,
	}})
	for i := start; i < end; i++ {
		attemptID := agentAttemptID(turnID, i)
		emitToolStepStarted(emit, turnID, round, i, calls[i], true, batchID, batchSize, limit, orchestrator.StepMetadata(calls[i], attemptID, a.toolStepMetadata(calls[i], true, batchSize)))
		orchestrator.EmitAttemptStarted(calls[i], attemptID)
	}
	jobs := make(chan int)
	done := make(chan toolExecutionResult, end-start)
	var wg sync.WaitGroup
	for worker := 0; worker < limit; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				done <- orchestrator.Execute(ctx, idx, calls[idx], agentAttemptID(turnID, idx))
			}
		}()
	}
	go func() {
		for i := start; i < end; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(done)
	}()
	for result := range done {
		results[result.Index] = result
		emitToolStepCompleted(emit, turnID, round, result, true, batchID, batchSize, limit)
		orchestrator.EmitAttemptFinished(result)
	}
	emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
		TurnID:        turnID,
		StepID:        batchID,
		Round:         round,
		Index:         start,
		Kind:          protocol.StepKindToolBatch,
		Status:        protocol.StepStatusCompleted,
		BatchID:       batchID,
		BatchSize:     batchSize,
		Parallel:      true,
		ParallelLimit: limit,
		DurationMS:    durationMS(batchStarted),
	}})
}

func (a *Agent) executeOneTool(ctx context.Context, orchestrator *toolOrchestrator, turnID string, round, index int, call protocol.ToolCall, parallel bool, batchID string, batchSize, limit int, emit func(protocol.Event)) toolExecutionResult {
	attemptID := agentAttemptID(turnID, index)
	emitToolStepStarted(emit, turnID, round, index, call, parallel, batchID, batchSize, limit, orchestrator.StepMetadata(call, attemptID, a.toolStepMetadata(call, parallel, batchSize)))
	orchestrator.EmitAttemptStarted(call, attemptID)
	result := orchestrator.Execute(ctx, index, call, attemptID)
	emitToolStepCompleted(emit, turnID, round, result, parallel, batchID, batchSize, limit)
	orchestrator.EmitAttemptFinished(result)
	return result
}

func emitToolStepStarted(emit func(protocol.Event), turnID string, round, index int, call protocol.ToolCall, parallel bool, batchID string, batchSize, limit int, metadata map[string]any) {
	emit(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
		TurnID:        turnID,
		StepID:        agentStepID(turnID, protocol.StepKindToolCall, index+1),
		Round:         round,
		Index:         index,
		Kind:          protocol.StepKindToolCall,
		Status:        protocol.StepStatusStarted,
		Name:          call.Name,
		ToolCallID:    call.ID,
		BatchID:       batchID,
		BatchSize:     batchSize,
		Parallel:      parallel,
		ParallelLimit: limit,
		Metadata:      metadata,
	}})
}

func (a *Agent) toolStepMetadata(call protocol.ToolCall, parallel bool, batchSize int) map[string]any {
	metadata := map[string]any{}
	var risk protocol.Risk
	var ok bool
	if a != nil && a.tools != nil {
		risk, ok = a.tools.Risk(call.Name)
		if ok {
			metadata["risk"] = risk
		}
	}
	parallelSafe := risk == protocol.RiskReadOnly || risk == protocol.RiskNetwork
	if ok {
		metadata["parallel_safe"] = parallelSafe
	}
	switch {
	case parallel:
		metadata["parallel_policy"] = "parallel_batch"
	case a == nil || a.cfg.MaxParallelTools <= 1:
		metadata["parallel_policy"] = "parallel_disabled"
	case !ok:
		metadata["parallel_policy"] = "unknown_tool_serial"
	case !parallelSafe:
		metadata["parallel_policy"] = "serial_risk_" + string(risk)
	case batchSize <= 1:
		metadata["parallel_policy"] = "single_parallel_safe_tool"
	default:
		metadata["parallel_policy"] = "serial"
	}
	return metadata
}

func emitToolStepCompleted(emit func(protocol.Event), turnID string, round int, result toolExecutionResult, parallel bool, batchID string, batchSize, limit int) {
	status := protocol.StepStatusCompleted
	errorText := ""
	if result.Result.IsError {
		status = protocol.StepStatusFailed
		errorText = result.Result.Content
	}
	emit(protocol.Event{Type: protocol.EventStepCompleted, Data: protocol.StepEvent{
		TurnID:        turnID,
		StepID:        agentStepID(turnID, protocol.StepKindToolCall, result.Index+1),
		Round:         round,
		Index:         result.Index,
		Kind:          protocol.StepKindToolCall,
		Status:        status,
		Name:          result.Call.Name,
		ToolCallID:    result.Call.ID,
		BatchID:       batchID,
		BatchSize:     batchSize,
		Parallel:      parallel,
		ParallelLimit: limit,
		DurationMS:    result.DurationMS,
		Error:         errorText,
		Metadata: map[string]any{
			"attempt_id": result.AttemptID,
			"truncated":  result.Result.Truncated,
			"output_ref": result.Result.OutputRef,
		},
	}})
}

func agentTurnID(round int) string {
	return fmt.Sprintf("turn-%03d", round)
}

func agentStepID(turnID, kind string, index int) string {
	kind = strings.ReplaceAll(kind, "_", "-")
	return fmt.Sprintf("%s:%s-%03d", turnID, kind, index)
}

func agentAttemptID(turnID string, index int) string {
	return fmt.Sprintf("%s:attempt-001", agentStepID(turnID, protocol.StepKindToolCall, index+1))
}

func agentRequestID(turnID string, round int) string {
	return fmt.Sprintf("%s:provider-request-%03d", turnID, round)
}

func newAgentSubmissionID() string {
	return fmt.Sprintf("submission-%s-%d", time.Now().UTC().Format("20060102T150405.000000000Z"), os.Getpid())
}

func newAgentRunID() string {
	return fmt.Sprintf("run-%s-%d", time.Now().UTC().Format("20060102T150405.000000000Z"), os.Getpid())
}

func (a *Agent) modelCallMetadata(requestID string, round, messageCount, toolCount int, snapshot runstate.Snapshot) map[string]any {
	metadata := snapshot.Metadata()
	if metadata["provider_id"] == nil {
		metadata["provider_id"] = modelinfo.ProviderForModel(a.cfg.Model, a.cfg.Provider)
	}
	if metadata["model_id"] == nil {
		metadata["model_id"] = a.cfg.Model
	}
	metadata["request_id"] = requestID
	metadata["round"] = round
	metadata["message_count"] = messageCount
	metadata["tool_count"] = toolCount
	if a.cfg.ReasoningEffort != "" {
		metadata["reasoning"] = a.cfg.ReasoningEffort
	}
	return metadata
}

func modelCallEventData(base map[string]any, status string, totalLatencyMS, firstDeltaMS int64, usage provider.Usage, meta provider.RequestMetadata, errText string) map[string]any {
	data := copyMap(base)
	data["status"] = status
	if meta.RequestID != "" {
		data["request_id"] = meta.RequestID
	}
	if meta.ProviderID != "" {
		data["provider_id"] = meta.ProviderID
	}
	if meta.ModelID != "" {
		data["model_id"] = meta.ModelID
	}
	if meta.ProviderRequestID != "" {
		data["provider_request_id"] = meta.ProviderRequestID
	}
	if meta.Attempts > 0 {
		data["attempts"] = meta.Attempts
	}
	if meta.Retries > 0 {
		data["retries"] = meta.Retries
	} else {
		data["retries"] = 0
	}
	if meta.StatusCode > 0 {
		data["status_code"] = meta.StatusCode
	}
	if totalLatencyMS >= 0 {
		data["total_latency_ms"] = totalLatencyMS
	}
	if firstDeltaMS >= 0 {
		data["first_delta_ms"] = firstDeltaMS
	}
	if usage.InputTokens > 0 {
		data["input_tokens"] = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		data["output_tokens"] = usage.OutputTokens
	}
	if usage.CacheHitTokens > 0 {
		data["cache_hit_tokens"] = usage.CacheHitTokens
	}
	if usage.CacheMissTokens > 0 {
		data["cache_miss_tokens"] = usage.CacheMissTokens
	}
	if usage.ReasoningTokens > 0 {
		data["reasoning_tokens"] = usage.ReasoningTokens
	}
	if errText != "" {
		data["error"] = errText
	}
	return data
}

func copyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func durationMS(started time.Time) int64 {
	if started.IsZero() {
		return 0
	}
	return time.Since(started).Milliseconds()
}

func elapsedMS(started, ended time.Time) int64 {
	if started.IsZero() || ended.IsZero() || ended.Before(started) {
		return 0
	}
	return ended.Sub(started).Milliseconds()
}

func firstDeltaLatencyMS(started, firstDeltaAt time.Time) int64 {
	if firstDeltaAt.IsZero() {
		return -1
	}
	return elapsedMS(started, firstDeltaAt)
}

func (a *Agent) compactToolResult(index int, call protocol.ToolCall, out *protocol.ToolResult) {
	if a == nil || out == nil || out.Content == "" || out.Truncated {
		return
	}
	limit := a.cfg.MaxToolOutputBytes
	if limit <= 0 || len(out.Content) <= limit {
		return
	}
	full := out.Content
	ref, err := storeManagedToolOutput(index, call, full)
	preview := trimUTF8Bytes(full, limit)
	if preview == "" {
		preview = "[tool output omitted]"
	}
	note := fmt.Sprintf("\n...[truncated %d bytes; full tool output saved as plaintext to %s with 0600 permissions. Use fs_read_file on output_ref if exact output is needed]", len(full)-len(preview), ref)
	if err != nil {
		ref = ""
		note = fmt.Sprintf("\n...[truncated %d bytes; failed to save full tool output: %v]", len(full)-len(preview), err)
	}
	out.Content = preview + note
	out.Truncated = true
	out.OutputRef = ref
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	out.Metadata["original_output_bytes"] = len(full)
	out.Metadata["returned_output_bytes"] = len(out.Content)
	if ref != "" {
		out.Metadata["output_ref"] = ref
		out.Metadata["output_ref_plaintext"] = true
		out.Metadata["output_ref_permissions"] = "0600"
	}
}

func storeManagedToolOutput(index int, call protocol.ToolCall, content string) (string, error) {
	baseDir := filepath.Join(config.BillyHomeDir(), "tool-output")
	if err := ensurePrivateDir(baseDir); err != nil {
		return "", err
	}
	dir := filepath.Join(baseDir, time.Now().UTC().Format("20060102"))
	if err := ensurePrivateDir(dir); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(content))
	name := fmt.Sprintf("%s-%02d-%s-%s-%s.txt",
		time.Now().UTC().Format("150405.000000000"),
		index+1,
		safeOutputName(call.Name),
		safeOutputName(call.ID),
		hex.EncodeToString(sum[:4]),
	)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

var unsafeOutputNameRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeOutputName(value string) string {
	value = unsafeOutputNameRE.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return "tool"
	}
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func trimUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	text = text[:maxBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func (a *Agent) canRunToolParallel(call protocol.ToolCall) bool {
	return a != nil && a.tools != nil && a.cfg.MaxParallelTools > 1 && a.tools.CanRunParallel(call.Name)
}

func (a *Agent) withMCPInstructions(messages []protocol.Message) []protocol.Message {
	if a == nil || a.tools == nil {
		return messages
	}
	instructions := a.tools.Instructions()
	if len(instructions) == 0 || hasMCPInstructions(messages) {
		return messages
	}
	content := "# MCP server instructions\n\n" + strings.Join(instructions, "\n\n")
	insertAt := protectedPrefixEnd(messages)
	next := make([]protocol.Message, 0, len(messages)+1)
	next = append(next, messages[:insertAt]...)
	next = append(next, protocol.Message{Role: protocol.RoleUser, Content: content})
	next = append(next, messages[insertAt:]...)
	return next
}

func hasMCPInstructions(messages []protocol.Message) bool {
	for _, msg := range messages {
		if msg.Role == protocol.RoleUser && strings.HasPrefix(msg.Content, "# MCP server instructions") {
			return true
		}
	}
	return false
}

func systemPrompt() string {
	return strings.Join([]string{
		"You are a fast coding and research agent. Use tools when useful. Keep final answers concise. Never reveal secrets.",
		"",
		"Format final answers with simple Markdown that remains readable in a terminal TUI and Telegram rich messages.",
		"Supported Markdown: short paragraphs, headings, bullet lists, numbered lists, blockquotes, inline code, fenced code blocks, bold, italic, plain links, simple pipe tables, and LaTeX math.",
		"Use LaTeX for mathematical formulas: prefer inline $...$ for short formulas and display $$...$$ for important formulas. Do not put math formulas in code fences.",
		"Do not use HTML, images, Mermaid diagrams, footnotes, task-list checkboxes, or other Markdown extensions unless the user explicitly asks for them.",
		"Prefer fenced code blocks with a language tag for code, logs, and commands.",
		"Keep non-math formatting simple enough to remain readable when ANSI styling is unavailable.",
		"Connected MCP servers are exposed lazily through mcp_list_tools and mcp_call; use them only when the user asks for those external services.",
		"If the user mentions Parilka, парилка, парилке, or asks what is happening there, treat it as the Telegram Parilka chat. Use mcp_list_tools with server \"telegram-parilka\" and then mcp_call. Do not search the filesystem or run shell commands for Parilka chat context.",
		"Native web_fetch, web_extract, and web_crawl return compact digests plus output_ref files for full extracted text. Prefer the digest/extract fields. Read output_ref only when exact quotes, exact source text, or deeper evidence is necessary. Do not request include_text/full_text unless the user explicitly needs exact source text.",
	}, "\n")
}

func optionalReasoning(cfg config.Config, reasoning string) string {
	if cfg.StoreReasoningContent {
		return reasoning
	}
	return ""
}

func PrettyEvent(event protocol.Event) string {
	bytes, _ := json.Marshal(event)
	return string(bytes)
}
