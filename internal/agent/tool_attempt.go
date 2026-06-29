package agent

import (
	"context"
	"time"

	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
)

type toolOrchestrator struct {
	agent     *Agent
	emit      func(protocol.Event)
	hooks     *runtimehooks.Runner
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

const (
	toolPhasePrepare            = "prepare"
	toolPhasePermissionDecision = "permission_decision"
	toolPhaseAttemptStarted     = "attempt_started"
	toolPhaseExecuting          = "executing"
	toolPhaseAttemptFinished    = "attempt_finished"
	toolPhaseRetryDecision      = "retry_decision"
	toolPhaseFinalize           = "finalize"
	toolPhaseCancelAbort        = "cancel_abort"

	toolProgressStatusSkipped = "skipped"
	toolProgressStatusAborted = "aborted"
)

func (a *Agent) newToolOrchestrator(emit func(protocol.Event), hookRunner *runtimehooks.Runner) *toolOrchestrator {
	return &toolOrchestrator{
		agent:     a,
		emit:      emit,
		hooks:     hookRunner,
		decisions: map[string]toolPermissionDecision{},
	}
}

func (o *toolOrchestrator) Request(call protocol.ToolCall) {
	if o == nil || o.emit == nil {
		return
	}
	o.emit(protocol.Event{Type: protocol.EventToolCallRequested, CallID: call.ID, Data: call})
	o.EmitProgress(call, "", toolPhasePrepare, protocol.StepStatusStarted, map[string]any{
		"args_summary": summarizeToolCallArgs(call),
	})
	decision := o.permissionDecision(call)
	o.decisions[call.ID] = decision
	o.emit(protocol.Event{Type: protocol.EventToolPermissionRequested, Data: decision.requestEventData()})
	o.emit(protocol.Event{Type: protocol.EventToolPermissionDecided, Data: decision.decisionEventData()})
	o.EmitProgress(call, "", toolPhasePermissionDecision, decision.Decision, decision.progressMetadata())
	if o.agent != nil {
		o.agent.emitToolAudit(call, o.emit)
	}
}

func (o *toolOrchestrator) EmitAttemptStarted(call protocol.ToolCall, attemptID string) {
	if o == nil || o.emit == nil {
		return
	}
	o.EmitProgress(call, attemptID, toolPhaseAttemptStarted, protocol.StepStatusStarted, nil)
	o.emit(protocol.Event{
		Type:      protocol.EventToolCallStarted,
		CallID:    call.ID,
		AttemptID: attemptID,
		Data:      call.Name,
	})
}

func (o *toolOrchestrator) Execute(ctx context.Context, turnID string, index int, call protocol.ToolCall, attemptID string) toolExecutionResult {
	started := time.Now()
	decision := o.decision(call)
	executingStatus := protocol.StepStatusStarted
	if decision.Decision == "deny" {
		executingStatus = toolProgressStatusSkipped
	}
	o.EmitProgress(call, attemptID, toolPhaseExecuting, executingStatus, decision.progressMetadata())
	var out protocol.ToolResult
	stepID := agentStepID(turnID, protocol.StepKindToolCall, index+1)
	beforeHookErr := o.runHook(ctx, "before_tool", o.toolHookPayload(turnID, stepID, call, attemptID, map[string]any{
		"args_summary":        summarizeToolCallArgs(call),
		"permission_decision": decision.Decision,
		"permission_source":   decision.Source,
		"permission_reason":   decision.Reason,
		"requires_approval":   decision.RequiresApproval,
		"risk":                decision.Risk,
		"known_risk":          decision.KnownRisk,
		"started_at":          started.UTC().Format(time.RFC3339Nano),
	}))
	if beforeHookErr != nil {
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   beforeHookErr.Error(),
			IsError:   true,
			ErrorCode: "hook_failed",
			Metadata:  map[string]any{"hook_event": "before_tool"},
		}
	} else if decision.Decision == "deny" {
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   dangerousToolDisabledMessage(),
			IsError:   true,
			ErrorCode: "permission_denied",
			Metadata:  map[string]any{},
		}
	} else if call.InvalidArgumentError != "" {
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   invalidToolArgumentsMessage(call),
			IsError:   true,
			ErrorCode: "invalid_json_args",
			Metadata: map[string]any{
				"invalid_argument_error": call.InvalidArgumentError,
				"invalid_arguments":      compactText(call.InvalidArguments, 500),
			},
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
	ended := time.Now()
	duration := ended.Sub(started).Milliseconds()
	out.Metadata["tool_name"] = call.Name
	out.Metadata["args_summary"] = summarizeToolCallArgs(call)
	out.Metadata["attempt_id"] = attemptID
	out.Metadata["permission_decision"] = decision.Decision
	out.Metadata["permission_source"] = decision.Source
	out.Metadata["permission_reason"] = decision.Reason
	out.Metadata["started_at"] = started.UTC().Format(time.RFC3339Nano)
	out.Metadata["finished_at"] = ended.UTC().Format(time.RFC3339Nano)
	out.Metadata["duration_ms"] = duration
	if decision.KnownRisk {
		out.Metadata["risk"] = decision.Risk
	}
	out.Metadata["output_bytes"] = len(out.Content)
	out.Metadata["output_estimated_tokens"] = estimateMessagesTokens([]protocol.Message{{Role: protocol.RoleTool, Content: out.Content}})
	out.Metadata["truncated"] = out.Truncated
	if out.OutputRef != "" {
		annotateOutputRefMetadata(out.OutputRef, out.Metadata)
	}
	if beforeHookErr == nil {
		afterHookErr := o.runHook(ctx, "after_tool", o.toolHookPayload(turnID, stepID, call, attemptID, map[string]any{
			"status":                  toolResultProgressStatus(out),
			"error_code":              out.ErrorCode,
			"is_error":                out.IsError,
			"duration_ms":             duration,
			"output_bytes":            out.Metadata["output_bytes"],
			"output_estimated_tokens": out.Metadata["output_estimated_tokens"],
			"truncated":               out.Truncated,
			"output_ref":              out.OutputRef,
			"permission_decision":     decision.Decision,
			"permission_source":       decision.Source,
			"permission_reason":       decision.Reason,
		}))
		if afterHookErr != nil {
			out.IsError = true
			out.ErrorCode = "hook_failed"
			out.Content = afterHookErr.Error()
			out.Metadata["hook_event"] = "after_tool"
			out.Metadata["hook_error"] = afterHookErr.Error()
			out.Metadata["output_bytes"] = len(out.Content)
			out.Metadata["output_estimated_tokens"] = estimateMessagesTokens([]protocol.Message{{Role: protocol.RoleTool, Content: out.Content}})
		}
	}
	progressStatus := toolResultProgressStatus(out)
	if out.ErrorCode == "tool_aborted" {
		o.EmitProgress(call, attemptID, toolPhaseCancelAbort, toolProgressStatusAborted, map[string]any{
			"error_code":  out.ErrorCode,
			"duration_ms": duration,
		})
	}
	o.EmitProgress(call, attemptID, toolPhaseAttemptFinished, progressStatus, map[string]any{
		"duration_ms":             duration,
		"error_code":              out.ErrorCode,
		"output_bytes":            out.Metadata["output_bytes"],
		"output_estimated_tokens": out.Metadata["output_estimated_tokens"],
		"truncated":               out.Truncated,
		"permission_decision":     decision.Decision,
		"permission_source":       decision.Source,
		"permission_reason":       decision.Reason,
		"output_ref":              out.OutputRef,
		"rate_limit_wait_ms":      out.Metadata["rate_limit_wait_ms"],
	})
	return toolExecutionResult{Index: index, Call: call, Result: out, DurationMS: duration, AttemptID: attemptID}
}

func (o *toolOrchestrator) AbortBeforeExecute(index int, call protocol.ToolCall, attemptID string, err error) toolExecutionResult {
	if err == nil {
		err = context.Canceled
	}
	out := protocol.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Content:   err.Error(),
		IsError:   true,
		ErrorCode: "tool_aborted",
		Metadata:  map[string]any{},
	}
	decision := o.decision(call)
	out.Metadata["tool_name"] = call.Name
	out.Metadata["args_summary"] = summarizeToolCallArgs(call)
	out.Metadata["attempt_id"] = attemptID
	out.Metadata["permission_decision"] = decision.Decision
	out.Metadata["permission_source"] = decision.Source
	out.Metadata["permission_reason"] = decision.Reason
	if decision.KnownRisk {
		out.Metadata["risk"] = decision.Risk
	}
	out.Metadata["output_bytes"] = len(out.Content)
	out.Metadata["output_estimated_tokens"] = estimateMessagesTokens([]protocol.Message{{Role: protocol.RoleTool, Content: out.Content}})
	out.Metadata["truncated"] = false
	o.EmitProgress(call, attemptID, toolPhaseCancelAbort, toolProgressStatusAborted, map[string]any{
		"error_code":              out.ErrorCode,
		"output_bytes":            out.Metadata["output_bytes"],
		"output_estimated_tokens": out.Metadata["output_estimated_tokens"],
		"permission_decision":     decision.Decision,
		"permission_source":       decision.Source,
		"permission_reason":       decision.Reason,
	})
	o.EmitProgress(call, attemptID, toolPhaseAttemptFinished, toolProgressStatusAborted, map[string]any{
		"error_code":              out.ErrorCode,
		"output_bytes":            out.Metadata["output_bytes"],
		"output_estimated_tokens": out.Metadata["output_estimated_tokens"],
		"permission_decision":     decision.Decision,
		"permission_source":       decision.Source,
		"permission_reason":       decision.Reason,
	})
	return toolExecutionResult{Index: index, Call: call, Result: out, AttemptID: attemptID}
}

func (o *toolOrchestrator) EmitAttemptFinished(result toolExecutionResult) {
	if o == nil || o.emit == nil {
		return
	}
	progressStatus := toolResultProgressStatus(result.Result)
	if result.Result.OutputRef != "" {
		o.emit(protocol.Event{Type: protocol.EventToolOutputRefCreated, Data: toolOutputRefEvent(result)})
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
	o.EmitProgress(result.Call, result.AttemptID, toolPhaseRetryDecision, toolProgressStatusSkipped, map[string]any{
		"reason": "retries_not_configured",
	})
	o.emit(protocol.Event{
		Type:      protocol.EventToolCallFinished,
		CallID:    result.Call.ID,
		AttemptID: result.AttemptID,
		Data:      result.Result,
	})
	o.EmitProgress(result.Call, result.AttemptID, toolPhaseFinalize, progressStatus, map[string]any{
		"duration_ms": result.DurationMS,
		"error_code":  result.Result.ErrorCode,
		"truncated":   result.Result.Truncated,
		"output_ref":  result.Result.OutputRef,
	})
}

func (o *toolOrchestrator) EmitProgress(call protocol.ToolCall, attemptID, phase, status string, metadata map[string]any) {
	if o == nil || o.emit == nil {
		return
	}
	progress := protocol.ToolProgressEvent{
		CallID:    call.ID,
		Name:      call.Name,
		AttemptID: attemptID,
		Phase:     phase,
		Status:    status,
		Metadata:  cleanProgressMetadata(metadata),
	}
	o.emit(protocol.Event{
		Type:      protocol.EventToolCallProgress,
		CallID:    call.ID,
		AttemptID: attemptID,
		Data:      progress,
	})
}

func (o *toolOrchestrator) runHook(ctx context.Context, event string, payload map[string]any) error {
	if o == nil || o.hooks == nil {
		return nil
	}
	return o.hooks.Run(ctx, event, payload, o.emit)
}

func (o *toolOrchestrator) toolHookPayload(turnID, stepID string, call protocol.ToolCall, attemptID string, extra map[string]any) map[string]any {
	payload := map[string]any{
		"turn_id":    turnID,
		"step_id":    stepID,
		"call_id":    call.ID,
		"attempt_id": attemptID,
		"tool_name":  call.Name,
	}
	for key, value := range extra {
		if value != nil && value != "" {
			payload[key] = value
		}
	}
	return payload
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
	if o == nil || o.agent == nil || o.agent.tools == nil {
		return toolPermissionDecision{
			CallID:   call.ID,
			Name:     call.Name,
			Decision: "allow",
			Source:   "auto",
			Reason:   "tool_registry_unavailable",
		}
	}
	decision := o.agent.tools.PolicyDecision(call.Name)
	return toolPermissionDecision{
		CallID:           call.ID,
		Name:             call.Name,
		Risk:             decision.Risk,
		KnownRisk:        decision.KnownRisk,
		RequiresApproval: decision.RequiresApproval,
		Decision:         decision.Decision,
		Source:           decision.Source,
		Reason:           decision.Reason,
	}
}

func (d toolPermissionDecision) requestEventData() protocol.ToolPermissionEvent {
	data := protocol.ToolPermissionEvent{
		CallID:           d.CallID,
		Name:             d.Name,
		RequiresApproval: d.RequiresApproval,
	}
	if d.KnownRisk {
		data.Risk = d.Risk
	}
	return data
}

func (d toolPermissionDecision) decisionEventData() protocol.ToolPermissionEvent {
	data := d.requestEventData()
	data.Decision = d.Decision
	data.Source = d.Source
	data.Reason = d.Reason
	return data
}

func (d toolPermissionDecision) progressMetadata() map[string]any {
	data := map[string]any{
		"permission_decision": d.Decision,
		"permission_source":   d.Source,
		"permission_reason":   d.Reason,
		"requires_approval":   d.RequiresApproval,
	}
	if d.KnownRisk {
		data["risk"] = d.Risk
	}
	return data
}

func toolOutputRefEvent(result toolExecutionResult) protocol.ToolOutputRefEvent {
	metadata := result.Result.Metadata
	return protocol.ToolOutputRefEvent{
		CallID:               result.Call.ID,
		Name:                 result.Call.Name,
		AttemptID:            result.AttemptID,
		OutputRef:            result.Result.OutputRef,
		OutputRefID:          metadataString(metadata, tooloutput.MetadataOutputRefID),
		OutputRefBytes:       metadataInt64(metadata, tooloutput.MetadataOutputRefBytes),
		OutputRefSHA256:      metadataString(metadata, tooloutput.MetadataOutputRefSHA256),
		OutputRefPermissions: metadataString(metadata, tooloutput.MetadataOutputRefPermissions),
		OutputRefPlaintext:   metadataBool(metadata, tooloutput.MetadataOutputRefPlaintext),
		Truncated:            result.Result.Truncated,
	}
}

func metadataString(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
}

func metadataBool(metadata map[string]any, key string) bool {
	if value, ok := metadata[key].(bool); ok {
		return value
	}
	return false
}

func metadataInt64(metadata map[string]any, key string) int64 {
	switch value := metadata[key].(type) {
	case int:
		return int64(value)
	case int8:
		return int64(value)
	case int16:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case uint:
		return int64(value)
	case uint8:
		return int64(value)
	case uint16:
		return int64(value)
	case uint32:
		return int64(value)
	case uint64:
		if value <= uint64(^uint(0)>>1) {
			return int64(value)
		}
	case float64:
		return int64(value)
	case float32:
		return int64(value)
	}
	return 0
}
