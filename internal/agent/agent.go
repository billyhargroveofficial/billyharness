package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	runtimehooks "github.com/billyhargroveofficial/billyharness/internal/hooks"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type Agent struct {
	providerBinding config.ProviderBinding
	profile         config.ProfileSelection
	runtime         config.RuntimeLimits
	toolPolicy      config.ToolPolicySettings
	mcpSettings     config.MCPSettings
	hookSettings    config.HookSettings
	instructions    config.InstructionSettings
	provider        provider.Provider
	tools           *tools.Registry
	askUser         AskUserHandler
}

type Settings struct {
	ProviderBinding config.ProviderBinding
	Profile         config.ProfileSelection
	Runtime         config.RuntimeLimits
	ToolPolicy      config.ToolPolicySettings
	MCP             config.MCPSettings
	Hooks           config.HookSettings
	Instructions    config.InstructionSettings
	AskUser         AskUserHandler
}

type AskUserHandler func(context.Context, protocol.UserInputRequestEvent, func(protocol.Event)) (protocol.UserInputAnswerEvent, error)

type PromptSubmitOptions struct {
	Source   string
	Metadata map[string]string
}

func SettingsFromConfig(cfg config.Config) Settings {
	return Settings{
		ProviderBinding: cfg.ProviderBinding(),
		Profile:         cfg.ProfileSelection(),
		Runtime:         cfg.RuntimeLimits(),
		ToolPolicy:      cfg.ToolPolicySettings(),
		MCP:             cfg.MCPSettings(),
		Hooks:           cfg.HookSettings(),
		Instructions:    cfg.InstructionSettings(),
	}
}

func New(cfg config.Config, provider provider.Provider, registry *tools.Registry) *Agent {
	return NewFromSettings(SettingsFromConfig(cfg), provider, registry)
}

func NewFromSettings(settings Settings, provider provider.Provider, registry *tools.Registry) *Agent {
	return &Agent{
		providerBinding: settings.ProviderBinding,
		profile:         settings.Profile,
		runtime:         settings.Runtime,
		toolPolicy:      settings.ToolPolicy,
		mcpSettings:     settings.MCP,
		hookSettings:    settings.Hooks,
		instructions:    settings.Instructions,
		provider:        provider,
		tools:           registry,
		askUser:         settings.AskUser,
	}
}

func (a *Agent) snapshotInput() runstate.SnapshotInput {
	if a == nil {
		return runstate.SnapshotInput{}
	}
	return runstate.SnapshotInput{
		Provider:   a.providerBinding,
		Profile:    a.profile,
		Runtime:    a.runtime,
		ToolPolicy: a.toolPolicy,
		MCP:        a.mcpSettings,
	}
}

func (a *Agent) modelID() string {
	if a == nil {
		return ""
	}
	return a.providerBinding.Model.Model
}

func (a *Agent) providerID() string {
	if a == nil {
		return ""
	}
	return a.providerBinding.Provider.Provider
}

func (a *Agent) reasoningEffort() string {
	if a == nil {
		return ""
	}
	return a.providerBinding.Model.ReasoningEffort
}

func cleanProgressMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	clean := map[string]any{}
	for key, value := range metadata {
		if value == nil || value == "" {
			continue
		}
		clean[key] = value
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}

func toolResultProgressStatus(result protocol.ToolResult) string {
	if result.ErrorCode == "tool_aborted" {
		return toolProgressStatusAborted
	}
	if result.IsError {
		return protocol.StepStatusFailed
	}
	return protocol.StepStatusCompleted
}

func ensureToolMetadata(out *protocol.ToolResult) {
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
}

func summarizeToolCallArgs(call protocol.ToolCall) string {
	if call.InvalidArgumentError != "" {
		return compactText("invalid_json_args: "+call.InvalidArguments, 240)
	}
	return summarizeToolArgs(call.Arguments)
}

func invalidToolArgumentsMessage(call protocol.ToolCall) string {
	detail := strings.TrimSpace(call.InvalidArgumentError)
	if detail == "" {
		detail = "invalid JSON arguments"
	}
	args := compactText(call.InvalidArguments, 500)
	if args == "" {
		args = "<empty>"
	}
	return fmt.Sprintf(
		"Tool call was not executed because its arguments were not valid JSON. %s. Original arguments: %s. Retry this tool call with a valid JSON object that matches the tool schema.",
		detail,
		args,
	)
}

func summarizeToolArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return compactText(string(raw), 160)
	}
	return compactText(summarizeArgValue(value, 0), 240)
}

func summarizeArgValue(value any, depth int) string {
	if depth > 2 {
		return "..."
	}
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, minInt(len(keys), 6))
		for i, key := range keys {
			if i >= 6 {
				parts = append(parts, fmt.Sprintf("...+%d", len(keys)-i))
				break
			}
			if sensitiveArgKey(key) {
				parts = append(parts, key+"=<redacted>")
				continue
			}
			if bulkyArgKey(key) {
				parts = append(parts, key+"=<omitted>")
				continue
			}
			parts = append(parts, key+"="+summarizeArgValue(v[key], depth+1))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		return fmt.Sprintf("[%d items]", len(v))
	case string:
		return strconvQuote(compactText(v, 64))
	case float64, bool, nil:
		body, _ := json.Marshal(v)
		return string(body)
	default:
		body, _ := json.Marshal(v)
		return compactText(string(body), 64)
	}
}

func sensitiveArgKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "api_key") ||
		strings.Contains(key, "apikey") ||
		strings.Contains(key, "authorization")
}

func bulkyArgKey(key string) bool {
	key = strings.ToLower(key)
	return key == "content" ||
		key == "text" ||
		key == "body" ||
		key == "input" ||
		key == "full_text"
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return "..."
	}
	return strings.TrimSpace(trimUTF8Bytes(text, limit-3)) + "..."
}

func strconvQuote(text string) string {
	body, _ := json.Marshal(text)
	return string(body)
}

func annotateOutputRefMetadata(ref string, metadata map[string]any) {
	_ = tooloutput.AddMetadataForPath(metadata, ref)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func joinHookError(primary, hookErr error) error {
	if hookErr == nil {
		return primary
	}
	if primary == nil {
		return hookErr
	}
	return errors.Join(primary, hookErr)
}

func (a *Agent) installMCPStatusHook(ctx context.Context, hookRunner *runtimehooks.Runner, run runstate.Run, emit func(protocol.Event)) func() {
	if a == nil || a.tools == nil || hookRunner == nil {
		return func() {}
	}
	deliver := func(status mcpclient.ServerStatus, phase string) {
		payload := mcpStatusHookPayload(run, status)
		payload["phase"] = phase
		_ = hookRunner.Run(ctx, "mcp_status_change", payload, emit)
	}
	cleanup := a.tools.AddMCPStatusListener(func(status mcpclient.ServerStatus) {
		deliver(status, "change")
	})
	for _, status := range a.tools.MCPStatuses() {
		deliver(status, "snapshot")
	}
	return cleanup
}

func mcpStatusHookPayload(run runstate.Run, status mcpclient.ServerStatus) map[string]any {
	payload := map[string]any{
		"submission_id": run.SubmissionID,
		"run_id":        run.ID,
		"server_name":   status.Name,
		"transport":     status.Transport,
		"enabled":       status.Enabled,
		"required":      status.Required,
		"connected":     status.Connected,
		"state":         status.State,
		"tool_count":    status.ToolCount,
		"retry_count":   status.RetryCount,
		"restart_count": status.RestartCount,
	}
	if status.Command != "" {
		payload["command"] = filepath.Base(status.Command)
	}
	if status.URL != "" {
		payload["url"] = status.URL
	}
	if status.UnsupportedReason != "" {
		payload["unsupported_reason"] = status.UnsupportedReason
	}
	if status.LastError != "" {
		payload["last_error"] = status.LastError
	}
	if status.Error != "" {
		payload["error"] = status.Error
	}
	if status.RetryBackoffMS > 0 {
		payload["retry_backoff_ms"] = status.RetryBackoffMS
	}
	if status.NextRetryAt != nil {
		payload["next_retry_at"] = status.NextRetryAt.Format(time.RFC3339Nano)
	}
	if status.LastEventAt != nil {
		payload["last_event_at"] = status.LastEventAt.Format(time.RFC3339Nano)
	}
	return payload
}

func dangerousToolDisabledMessage() string {
	return tools.DangerousToolDisabledMessage()
}

func (a *Agent) emitToolAudit(call protocol.ToolCall, toolSet tools.ToolSet, emit func(protocol.Event)) {
	if a == nil || emit == nil {
		return
	}
	risk, ok := toolSet.Risk(call.Name)
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
		"auto_approved": a.toolPolicy.AutoApproveDangerous,
	}})
}

func (a *Agent) executeParallelToolBatch(ctx context.Context, orchestrator *toolOrchestrator, toolSet tools.ToolSet, runID, turnID string, round int, calls []protocol.ToolCall, start, end int, results []toolExecutionResult, emit func(protocol.Event)) {
	limit := a.runtime.MaxParallelTools
	if limit <= 1 || end-start == 1 {
		for i := start; i < end; i++ {
			results[i] = a.executeOneTool(ctx, orchestrator, toolSet, runID, turnID, round, i, calls[i], false, "", 0, 0, emit)
		}
		return
	}
	if limit > end-start {
		limit = end - start
	}
	batchID := agentStepID(turnID, protocol.StepKindToolBatch, start+1)
	batchStarted := time.Now()
	batchSize := end - start
	rateBuckets := a.toolRateBuckets(toolSet, calls, start, end)
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
		emitToolStepStarted(emit, turnID, round, i, calls[i], true, batchID, batchSize, limit, orchestrator.StepMetadata(calls[i], attemptID, a.toolStepMetadata(toolSet, calls[i], true, batchSize)))
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
				call := calls[idx]
				release, waitMS, acquired := a.acquireToolRateBucket(ctx, toolSet, rateBuckets, call)
				if !acquired {
					done <- orchestrator.AbortBeforeExecute(idx, call, agentAttemptID(turnID, idx), ctx.Err())
					continue
				}
				result := orchestrator.Execute(ctx, runID, turnID, idx, call, agentAttemptID(turnID, idx))
				if release != nil {
					release()
				}
				if waitMS > 0 {
					ensureToolMetadata(&result.Result)
					result.Result.Metadata["rate_limit_wait_ms"] = waitMS
				}
				done <- result
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

func (a *Agent) executeOneTool(ctx context.Context, orchestrator *toolOrchestrator, toolSet tools.ToolSet, runID, turnID string, round, index int, call protocol.ToolCall, parallel bool, batchID string, batchSize, limit int, emit func(protocol.Event)) toolExecutionResult {
	attemptID := agentAttemptID(turnID, index)
	emitToolStepStarted(emit, turnID, round, index, call, parallel, batchID, batchSize, limit, orchestrator.StepMetadata(call, attemptID, a.toolStepMetadata(toolSet, call, parallel, batchSize)))
	orchestrator.EmitAttemptStarted(call, attemptID)
	result := orchestrator.Execute(ctx, runID, turnID, index, call, attemptID)
	emitToolStepCompleted(emit, turnID, round, result, parallel, batchID, batchSize, limit)
	orchestrator.EmitAttemptFinished(result)
	return result
}

func (a *Agent) toolRateBuckets(toolSet tools.ToolSet, calls []protocol.ToolCall, start, end int) map[string]chan struct{} {
	if a == nil {
		return nil
	}
	limits := map[string]int{}
	for i := start; i < end; i++ {
		meta, ok := toolSet.ParallelMetadata(calls[i].Name)
		if !ok || meta.RateLimitKey == "" || meta.MaxConcurrency <= 0 {
			continue
		}
		if existing, ok := limits[meta.RateLimitKey]; !ok || meta.MaxConcurrency < existing {
			limits[meta.RateLimitKey] = meta.MaxConcurrency
		}
	}
	if len(limits) == 0 {
		return nil
	}
	buckets := make(map[string]chan struct{}, len(limits))
	for key, limit := range limits {
		if limit < 1 {
			limit = 1
		}
		buckets[key] = make(chan struct{}, limit)
	}
	return buckets
}

func (a *Agent) acquireToolRateBucket(ctx context.Context, toolSet tools.ToolSet, buckets map[string]chan struct{}, call protocol.ToolCall) (func(), int64, bool) {
	if len(buckets) == 0 || a == nil {
		return nil, 0, true
	}
	meta, ok := toolSet.ParallelMetadata(call.Name)
	if !ok || meta.RateLimitKey == "" {
		return nil, 0, true
	}
	bucket := buckets[meta.RateLimitKey]
	if bucket == nil {
		return nil, 0, true
	}
	started := time.Now()
	select {
	case bucket <- struct{}{}:
		return func() { <-bucket }, elapsedMS(started, time.Now()), true
	case <-ctx.Done():
		return nil, elapsedMS(started, time.Now()), false
	}
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

func (a *Agent) toolStepMetadata(toolSet tools.ToolSet, call protocol.ToolCall, parallel bool, batchSize int) map[string]any {
	metadata := map[string]any{}
	if call.InvalidArgumentError != "" {
		metadata["invalid_argument_error"] = call.InvalidArgumentError
		metadata["args_summary"] = summarizeToolCallArgs(call)
	}
	var risk protocol.Risk
	var ok bool
	var parallelMeta tools.ParallelMetadata
	var hasParallelMeta bool
	risk, ok = toolSet.Risk(call.Name)
	if ok {
		metadata["risk"] = risk
	}
	parallelMeta, hasParallelMeta = toolSet.ParallelMetadata(call.Name)
	parallelSafe := hasParallelMeta && parallelMeta.CanRunParallel()
	if hasParallelMeta {
		metadata["parallel_safe"] = parallelSafe
		metadata["parallel_policy"] = parallelMeta.Policy
		metadata["idempotent"] = parallelMeta.Idempotent
		metadata["requires_exclusive_workspace"] = parallelMeta.RequiresExclusiveWorkspace
		metadata["cancellable"] = parallelMeta.Cancellable
		if parallelMeta.RateLimitKey != "" {
			metadata["rate_limit_key"] = parallelMeta.RateLimitKey
		}
		if parallelMeta.MaxConcurrency > 0 {
			metadata["max_concurrency"] = parallelMeta.MaxConcurrency
		}
	}
	switch {
	case parallel:
		metadata["parallel_decision"] = "parallel_batch"
	case a == nil || a.runtime.MaxParallelTools <= 1:
		metadata["parallel_decision"] = "parallel_disabled"
	case !hasParallelMeta:
		metadata["parallel_decision"] = "unknown_tool_serial"
	case !parallelSafe:
		metadata["parallel_decision"] = "serial_policy_" + parallelMeta.Policy
	case batchSize <= 1:
		metadata["parallel_decision"] = "single_parallel_safe_tool"
	default:
		metadata["parallel_decision"] = "serial"
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
		metadata["provider_id"] = a.providerID()
	}
	if metadata["model_id"] == nil {
		metadata["model_id"] = a.modelID()
	}
	metadata["request_id"] = requestID
	metadata["round"] = round
	metadata["message_count"] = messageCount
	metadata["tool_count"] = toolCount
	if reasoning := a.reasoningEffort(); reasoning != "" {
		metadata["reasoning"] = reasoning
	}
	return metadata
}

func modelCallEventData(base map[string]any, status string, totalLatencyMS, firstDeltaMS int64, usage provider.Usage, meta provider.RequestMetadata, errText string) protocol.ModelCallEvent {
	data := protocol.ModelCallEvent{
		RequestID:               metadataString(base, "request_id"),
		Round:                   int(metadataInt64(base, "round")),
		MessageCount:            int(metadataInt64(base, "message_count")),
		ToolCount:               int(metadataInt64(base, "tool_count")),
		ProviderID:              metadataString(base, "provider_id"),
		ModelID:                 metadataString(base, "model_id"),
		Reasoning:               metadataString(base, "reasoning"),
		ReasoningMode:           metadataString(base, "reasoning_mode"),
		ContextBudgetTokens:     metadataInt64(base, "context_budget_tokens"),
		ToolSnapshotHash:        metadataString(base, "tool_snapshot_hash"),
		MCPStatusSnapshotHash:   metadataString(base, "mcp_status_snapshot_hash"),
		ProfileInstructionHash:  metadataString(base, "profile_instruction_hash"),
		DangerousPermissionMode: metadataString(base, "dangerous_permission_mode"),
		AccessMode:              metadataString(base, "access_mode"),
		Status:                  status,
		Retries:                 meta.Retries,
	}
	if meta.RequestID != "" {
		data.RequestID = meta.RequestID
	}
	if meta.ProviderID != "" {
		data.ProviderID = meta.ProviderID
	}
	if meta.ModelID != "" {
		data.ModelID = meta.ModelID
	}
	if meta.ProviderRequestID != "" {
		data.ProviderRequestID = meta.ProviderRequestID
	}
	if meta.Attempts > 0 {
		data.Attempts = meta.Attempts
	}
	if meta.StatusCode > 0 {
		data.StatusCode = meta.StatusCode
	}
	if totalLatencyMS >= 0 {
		data.TotalLatencyMS = int64Ptr(totalLatencyMS)
	}
	if firstDeltaMS >= 0 {
		data.FirstDeltaMS = int64Ptr(firstDeltaMS)
	}
	if usage.InputTokens > 0 {
		data.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		data.OutputTokens = usage.OutputTokens
	}
	if usage.CacheHitTokens > 0 {
		data.CacheHitTokens = usage.CacheHitTokens
	}
	if usage.CacheMissTokens > 0 {
		data.CacheMissTokens = usage.CacheMissTokens
	}
	if usage.ReasoningTokens > 0 {
		data.ReasoningTokens = usage.ReasoningTokens
	}
	if errText != "" {
		data.Error = errText
	}
	return data
}

func modelCallEventMetadata(data protocol.ModelCallEvent) map[string]any {
	metadata := map[string]any{
		"request_id":                data.RequestID,
		"round":                     data.Round,
		"message_count":             data.MessageCount,
		"tool_count":                data.ToolCount,
		"provider_id":               data.ProviderID,
		"model_id":                  data.ModelID,
		"dangerous_permission_mode": data.DangerousPermissionMode,
		"access_mode":               data.AccessMode,
		"status":                    data.Status,
		"retries":                   data.Retries,
	}
	addStringMetadata(metadata, "reasoning", data.Reasoning)
	addStringMetadata(metadata, "reasoning_mode", data.ReasoningMode)
	addInt64Metadata(metadata, "context_budget_tokens", data.ContextBudgetTokens)
	addStringMetadata(metadata, "tool_snapshot_hash", data.ToolSnapshotHash)
	addStringMetadata(metadata, "mcp_status_snapshot_hash", data.MCPStatusSnapshotHash)
	addStringMetadata(metadata, "profile_instruction_hash", data.ProfileInstructionHash)
	addStringMetadata(metadata, "provider_request_id", data.ProviderRequestID)
	addIntMetadata(metadata, "attempts", data.Attempts)
	addIntMetadata(metadata, "status_code", data.StatusCode)
	addOptionalInt64Metadata(metadata, "total_latency_ms", data.TotalLatencyMS)
	addOptionalInt64Metadata(metadata, "first_delta_ms", data.FirstDeltaMS)
	addInt64Metadata(metadata, "input_tokens", data.InputTokens)
	addInt64Metadata(metadata, "output_tokens", data.OutputTokens)
	addInt64Metadata(metadata, "cache_hit_tokens", data.CacheHitTokens)
	addInt64Metadata(metadata, "cache_miss_tokens", data.CacheMissTokens)
	addInt64Metadata(metadata, "reasoning_tokens", data.ReasoningTokens)
	addStringMetadata(metadata, "error", data.Error)
	return metadata
}

func addStringMetadata(metadata map[string]any, key, value string) {
	if value != "" {
		metadata[key] = value
	}
}

func addIntMetadata(metadata map[string]any, key string, value int) {
	if value > 0 {
		metadata[key] = value
	}
}

func addInt64Metadata(metadata map[string]any, key string, value int64) {
	if value > 0 {
		metadata[key] = value
	}
}

func addOptionalInt64Metadata(metadata map[string]any, key string, value *int64) {
	if value != nil {
		metadata[key] = *value
	}
}

func int64Ptr(value int64) *int64 {
	return &value
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
	if a == nil || out == nil || out.Content == "" {
		return
	}
	limit := a.toolPolicy.MaxToolOutputBytes
	if limit <= 0 || len(out.Content) <= limit {
		return
	}
	full := out.Content
	ref := out.OutputRef
	reusedRef := ref != ""
	var refInfo tooloutput.Ref
	var err error
	if ref == "" {
		refInfo, err = storeManagedToolOutput(index, call, full)
		ref = refInfo.Path
	}
	out.Content = boundedToolOutputPreview(full, limit, ref, reusedRef, err)
	out.Truncated = true
	out.OutputRef = ref
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	out.Metadata["original_output_bytes"] = len(full)
	out.Metadata["returned_output_bytes"] = len(out.Content)
	out.Metadata["inline_budget_bytes"] = limit
	out.Metadata["inline_budget_enforced"] = true
	if ref != "" {
		if refInfo.Path != "" {
			refInfo.AddMetadata(out.Metadata)
		} else {
			_ = tooloutput.AddMetadataForPath(out.Metadata, ref)
		}
	}
}

func boundedToolOutputPreview(full string, limit int, ref string, reusedRef bool, saveErr error) string {
	if limit <= 0 {
		return ""
	}
	noteFor := func(omitted int) string {
		if saveErr != nil {
			return fmt.Sprintf("\n...[truncated %d bytes; failed to save full tool output: %v]", omitted, saveErr)
		}
		if reusedRef && ref != "" {
			return fmt.Sprintf("\n...[truncated %d bytes to fit inline budget; existing output_ref remains %s. Use fs_read_file on output_ref if exact output is needed]", omitted, ref)
		}
		if ref != "" {
			return fmt.Sprintf("\n...[truncated %d bytes; full tool output saved as plaintext to %s with 0600 permissions. Use fs_read_file on output_ref if exact output is needed]", omitted, ref)
		}
		return fmt.Sprintf("\n...[truncated %d bytes]", omitted)
	}
	note := noteFor(len(full))
	if len(note) >= limit {
		return trimUTF8Bytes(strings.TrimSpace(note), limit)
	}
	for {
		previewLimit := limit - len(note)
		preview := trimUTF8Bytes(full, previewLimit)
		omitted := len(full) - len(preview)
		nextNote := noteFor(omitted)
		if len(preview)+len(nextNote) <= limit {
			if preview == "" {
				return trimUTF8Bytes(strings.TrimSpace(nextNote), limit)
			}
			return preview + nextNote
		}
		if nextNote == note {
			return trimUTF8Bytes(preview+nextNote, limit)
		}
		note = nextNote
		if len(note) >= limit {
			return trimUTF8Bytes(strings.TrimSpace(note), limit)
		}
	}
}

func storeManagedToolOutput(index int, call protocol.ToolCall, content string) (tooloutput.Ref, error) {
	return tooloutput.Store(tooloutput.StoreRequest{
		Parts:   []string{fmt.Sprintf("%02d", index+1), call.Name, call.ID},
		Content: content,
	})
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

func (a *Agent) canRunToolParallel(toolSet tools.ToolSet, call protocol.ToolCall) bool {
	return a != nil && a.runtime.MaxParallelTools > 1 && toolSet.CanRunParallel(call.Name)
}

func PrettyEvent(event protocol.Event) string {
	bytes, _ := json.Marshal(event)
	return string(bytes)
}
