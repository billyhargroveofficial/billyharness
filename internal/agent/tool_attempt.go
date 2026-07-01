package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/checkpoint"
	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type toolOrchestrator struct {
	agent     *Agent
	toolSet   tools.ToolSet
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

func (a *Agent) newToolOrchestrator(emit func(protocol.Event), hookRunner *runtimehooks.Runner, toolSet tools.ToolSet) *toolOrchestrator {
	return &toolOrchestrator{
		agent:     a,
		toolSet:   toolSet,
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
		o.agent.emitToolAudit(call, o.toolSet, o.emit)
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

func (o *toolOrchestrator) Execute(ctx context.Context, runID, turnID string, index int, call protocol.ToolCall, attemptID string) toolExecutionResult {
	started := time.Now()
	decision := o.decision(call)
	executingStatus := protocol.StepStatusStarted
	if decision.Decision == "deny" {
		executingStatus = toolProgressStatusSkipped
	}
	o.EmitProgress(call, attemptID, toolPhaseExecuting, executingStatus, decision.progressMetadata())
	var out protocol.ToolResult
	stepID := agentStepID(turnID, protocol.StepKindToolCall, index+1)
	var turnChangeTracker *checkpoint.Tracker
	var turnChangeErr string
	if decision.Decision != "deny" && call.InvalidArgumentError == "" && o != nil && o.agent != nil {
		tracker, tracked, err := checkpoint.Begin(checkpoint.DefaultOptions(o.agent.toolPolicy.WorkspaceRoots), call.Name, call.Arguments)
		if err != nil {
			turnChangeErr = err.Error()
		} else if tracked {
			turnChangeTracker = tracker
		}
	}
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
	} else if o == nil || o.agent == nil {
		out = protocol.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Content:   "tool registry unavailable",
			IsError:   true,
			ErrorCode: "tool_registry_unavailable",
			Metadata:  map[string]any{},
		}
	} else if call.Name == tools.AskUserToolName {
		result, err := o.executeAskUser(ctx, runID, turnID, stepID, call, attemptID)
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
	} else {
		result, err := o.toolSet.Call(ctx, call)
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
	if turnChangeErr != "" {
		out.Metadata["turn_change_error"] = turnChangeErr
	}
	if turnChangeTracker != nil {
		record, changed, err := turnChangeTracker.Complete(turnID, stepID, call.ID, attemptID)
		if err != nil {
			out.Metadata["turn_change_error"] = err.Error()
		} else if changed {
			o.recordTurnChange(&out, record)
		}
	}
	progressStatus := toolResultProgressStatus(out)
	compact := toolResultCompact(call, attemptID, progressStatus, out)
	out.Compact = &compact
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
	return toolExecutionResult{Index: index, Call: call, Result: out, DurationMS: duration, AttemptID: attemptID, RunID: runID, TurnID: turnID, StepID: stepID}
}

func (o *toolOrchestrator) executeAskUser(ctx context.Context, runID, turnID, stepID string, call protocol.ToolCall, attemptID string) (tools.Result, error) {
	questions, err := tools.ParseAskUserQuestions(call.Arguments)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorCode: "validation_error"}, err
	}
	if o == nil || o.agent == nil || o.agent.askUser == nil {
		err := fmt.Errorf("ask_user is only available during a gateway session run")
		return tools.Result{Content: err.Error(), IsError: true, ErrorCode: "ask_user_unavailable"}, err
	}
	requestID := askUserRequestID(call, attemptID)
	request := protocol.UserInputRequestEvent{
		RequestID: requestID,
		RunID:     runID,
		TurnID:    turnID,
		StepID:    stepID,
		CallID:    call.ID,
		AttemptID: attemptID,
		Source:    "tool",
		Questions: questions,
	}
	answer, err := o.agent.askUser(ctx, request, o.emit)
	if err != nil {
		code := "user_input_rejected"
		if ctx.Err() != nil {
			code = "tool_aborted"
		}
		return tools.Result{
			Content:   err.Error(),
			IsError:   true,
			ErrorCode: code,
			Metadata: map[string]any{
				"request_id": requestID,
			},
		}, err
	}
	answer.Status = "answered"
	body, marshalErr := json.Marshal(answer)
	if marshalErr != nil {
		return tools.Result{Content: marshalErr.Error(), IsError: true, ErrorCode: "tool_error"}, marshalErr
	}
	return tools.Result{
		Content: string(body),
		Metadata: map[string]any{
			"request_id":   answer.RequestID,
			"answer_count": len(answer.Answers),
		},
	}, nil
}

func askUserRequestID(call protocol.ToolCall, attemptID string) string {
	if id := strings.TrimSpace(call.ID); id != "" {
		return id
	}
	return strings.TrimSpace(attemptID)
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
	compact := toolResultCompact(call, attemptID, toolProgressStatusAborted, out)
	out.Compact = &compact
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
	if usage, ok := providerHelperUsageFromToolResult(result); ok {
		o.emit(protocol.Event{Type: protocol.EventProviderHelperUsage, Data: usage})
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

func providerHelperUsageFromToolResult(result toolExecutionResult) (protocol.ProviderHelperUsageEvent, bool) {
	metadata := result.Result.Metadata
	if len(metadata) == 0 {
		return protocol.ProviderHelperUsageEvent{}, false
	}
	inputTokens := metadataInt64(metadata, "tool_summary_api_input_tokens")
	outputTokens := metadataInt64(metadata, "tool_summary_api_output_tokens")
	apiTokens := metadataInt64(metadata, "tool_summary_api_total_tokens")
	if apiTokens == 0 {
		apiTokens = inputTokens + outputTokens
	}
	cacheHit := metadataInt64(metadata, "tool_summary_api_cache_hit_tokens")
	cacheMiss := metadataInt64(metadata, "tool_summary_api_cache_miss_tokens")
	externalModel := metadataBool(metadata, "tool_summary_external_model_used")
	if !externalModel && apiTokens <= 0 && cacheHit <= 0 && cacheMiss <= 0 {
		return protocol.ProviderHelperUsageEvent{}, false
	}
	return protocol.ProviderHelperUsageEvent{
		Kind:            "web_summary",
		Provider:        metadataString(metadata, "summarizer_provider"),
		Model:           firstNonEmptyString(metadataString(metadata, "summarizer_model"), metadataString(metadata, "websum_model")),
		RunID:           result.RunID,
		TurnID:          result.TurnID,
		StepID:          result.StepID,
		CallID:          result.Call.ID,
		AttemptID:       result.AttemptID,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		CacheHitTokens:  cacheHit,
		CacheMissTokens: cacheMiss,
		APITokens:       apiTokens,
		CostUSD:         metadataFloat64(metadata, "tool_summary_estimated_cost_usd"),
	}, true
}

func (o *toolOrchestrator) EmitProgress(call protocol.ToolCall, attemptID, phase, status string, metadata map[string]any) {
	if o == nil || o.emit == nil {
		return
	}
	cleanMetadata := cleanProgressMetadata(metadata)
	compact := toolProgressCompact(call, attemptID, phase, status, cleanMetadata)
	progress := protocol.ToolProgressEvent{
		CallID:    call.ID,
		Name:      call.Name,
		AttemptID: attemptID,
		Phase:     phase,
		Status:    status,
		Metadata:  cleanMetadata,
		Compact:   &compact,
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
	if o == nil || o.agent == nil {
		return toolPermissionDecision{
			CallID:   call.ID,
			Name:     call.Name,
			Decision: "allow",
			Source:   "auto",
			Reason:   "tool_registry_unavailable",
		}
	}
	decision := o.toolSet.PolicyDecision(call.Name)
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
	compact := toolResultCompact(result.Call, result.AttemptID, "output_ref", result.Result)
	compact.Lifecycle = "output_ref"
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
		Compact:              &compact,
	}
}

func (o *toolOrchestrator) recordTurnChange(out *protocol.ToolResult, record checkpoint.PatchRecord) {
	if o == nil || out == nil {
		return
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		out.Metadata["turn_change_error"] = err.Error()
		return
	}
	ref, err := tooloutput.Store(tooloutput.StoreRequest{
		Parts:                 []string{"checkpoint", record.ToolName, record.ChangeID},
		Content:               string(body),
		EnsureTrailingNewline: true,
	})
	if err != nil {
		out.Metadata["turn_change_error"] = err.Error()
		return
	}
	out.Metadata["turn_change_id"] = record.ChangeID
	out.Metadata["turn_change_files"] = record.Stats.FileCount
	out.Metadata["turn_change_added"] = record.Stats.Added
	out.Metadata["turn_change_modified"] = record.Stats.Modified
	out.Metadata["turn_change_deleted"] = record.Stats.Deleted
	out.Metadata["turn_change_additions"] = record.Stats.Additions
	out.Metadata["turn_change_deletions"] = record.Stats.Deletions
	out.Metadata["turn_change_reversible"] = recordReversible(record)
	out.Metadata["turn_change_output_ref"] = ref.Path
	out.Metadata["turn_change_output_ref_id"] = ref.ID
	data := turnChangeEvent(record, ref)
	if o.emit != nil {
		o.emit(protocol.Event{Type: protocol.EventTurnChangeRecorded, Data: data})
	}
}

func turnChangeEvent(record checkpoint.PatchRecord, ref tooloutput.Ref) protocol.TurnChangeEvent {
	event := protocol.TurnChangeEvent{
		ChangeID:                  record.ChangeID,
		TurnID:                    record.TurnID,
		StepID:                    record.StepID,
		CallID:                    record.CallID,
		AttemptID:                 record.AttemptID,
		ToolName:                  record.ToolName,
		Status:                    "recorded",
		Summary:                   turnChangeSummary(record),
		FileCount:                 record.Stats.FileCount,
		Added:                     record.Stats.Added,
		Modified:                  record.Stats.Modified,
		Deleted:                   record.Stats.Deleted,
		Directories:               record.Stats.Directories,
		Additions:                 record.Stats.Additions,
		Deletions:                 record.Stats.Deletions,
		BinaryFiles:               record.Stats.BinaryFiles,
		LargeFiles:                record.Stats.LargeFiles,
		Reversible:                recordReversible(record),
		PatchOutputRef:            ref.Path,
		PatchOutputRefID:          ref.ID,
		PatchOutputRefBytes:       ref.Bytes,
		PatchOutputRefSHA256:      ref.SHA256,
		PatchOutputRefPermissions: ref.Permissions,
		PatchOutputRefPlaintext:   ref.Plaintext,
	}
	for i, file := range record.Files {
		if i >= 80 {
			break
		}
		item := protocol.TurnChangeFile{
			Path:       file.Path,
			RelPath:    file.RelPath,
			Change:     file.Change,
			Kind:       file.Kind,
			Additions:  file.Additions,
			Deletions:  file.Deletions,
			Binary:     file.Binary,
			Large:      file.Large,
			Reversible: file.Reversible,
		}
		if file.Before != nil {
			item.BeforeSHA256 = file.Before.SHA256
		}
		if file.After != nil {
			item.AfterSHA256 = file.After.SHA256
		}
		event.Files = append(event.Files, item)
	}
	return event
}

func turnChangeSummary(record checkpoint.PatchRecord) string {
	return "changed " + metadataIntString(record.Stats.FileCount) +
		" files (+" + metadataIntString(record.Stats.Additions) +
		" -" + metadataIntString(record.Stats.Deletions) + ")"
}

func metadataIntString(value int) string {
	return strconv.Itoa(value)
}

func recordReversible(record checkpoint.PatchRecord) bool {
	for _, file := range record.Files {
		if !file.Reversible {
			return false
		}
	}
	return true
}

func toolProgressCompact(call protocol.ToolCall, attemptID, phase, status string, metadata map[string]any) protocol.ToolCompact {
	target := metadataString(metadata, "args_summary")
	if target == "" {
		target = summarizeToolCallArgs(call)
	}
	compact := protocol.ToolCompact{
		CallID:    call.ID,
		AttemptID: attemptID,
		Name:      call.Name,
		Lifecycle: phase,
		Status:    status,
		Title:     call.Name,
		Summary:   compactToolSummary(call.Name, target, status),
		Detail:    phase,
		Category:  metadataText(metadata, "risk"),
		Verb:      phase,
		Target:    target,
	}
	applyToolCompactMetadata(&compact, metadata)
	return compact
}

func toolResultCompact(call protocol.ToolCall, attemptID, status string, result protocol.ToolResult) protocol.ToolCompact {
	metadata := result.Metadata
	compact := toolProgressCompact(call, attemptID, "result", status, metadata)
	compact.OutputRef = result.OutputRef
	compact.Truncated = result.Truncated
	compact.IsError = result.IsError
	if result.ErrorCode != "" {
		compact.Error = result.ErrorCode
	} else if result.IsError {
		compact.Error = compactText(result.Content, 120)
	}
	if result.OutputRef != "" {
		compact.Hints = append(compact.Hints, "output_ref")
	}
	if result.Truncated {
		compact.Hints = append(compact.Hints, "truncated")
	}
	if result.IsError {
		compact.Status = protocol.StepStatusFailed
		compact.Summary = compactToolSummary(call.Name, compact.Target, compact.Status)
	}
	return compact
}

func applyToolCompactMetadata(compact *protocol.ToolCompact, metadata map[string]any) {
	if compact == nil || len(metadata) == 0 {
		return
	}
	compact.OutputRef = firstNonEmptyString(compact.OutputRef, metadataString(metadata, "output_ref"))
	compact.OutputRefID = firstNonEmptyString(compact.OutputRefID, metadataString(metadata, tooloutput.MetadataOutputRefID))
	compact.Summary = firstNonEmptyString(metadataString(metadata, "display_summary"), compact.Summary)
	compact.Target = firstNonEmptyString(metadataString(metadata, "display_target"), compact.Target)
	compact.DurationMS = firstNonZeroInt64(compact.DurationMS, metadataInt64(metadata, "duration_ms"))
	compact.EstimatedTokens = firstNonZeroInt64(compact.EstimatedTokens, metadataInt64(metadata, "output_estimated_tokens"))
	compact.EstimatedTokens = firstNonZeroInt64(compact.EstimatedTokens, metadataInt64(metadata, "estimated_text_tokens"))
	compact.OriginalBytes = firstNonZeroInt64(compact.OriginalBytes, metadataInt64(metadata, "original_output_bytes"))
	compact.OriginalBytes = firstNonZeroInt64(compact.OriginalBytes, metadataInt64(metadata, "output_bytes"))
	compact.OriginalBytes = firstNonZeroInt64(compact.OriginalBytes, metadataInt64(metadata, tooloutput.MetadataOutputRefBytes))
	if metadataBool(metadata, "truncated") {
		compact.Truncated = true
	}
	if compact.OutputRef != "" {
		compact.Hints = appendMissingHint(compact.Hints, "output_ref")
	}
	if compact.Truncated {
		compact.Hints = appendMissingHint(compact.Hints, "truncated")
	}
}

func compactToolSummary(name, target, status string) string {
	var parts []string
	if status != "" {
		parts = append(parts, status)
	}
	if name != "" {
		parts = append(parts, name)
	}
	if target != "" && target != "{}" {
		parts = append(parts, target)
	}
	return compactText(strings.Join(parts, " "), 180)
}

func appendMissingHint(hints []string, hint string) []string {
	for _, existing := range hints {
		if existing == hint {
			return hints
		}
	}
	return append(hints, hint)
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func metadataText(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
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

func metadataFloat64(metadata map[string]any, key string) float64 {
	switch value := metadata[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}
