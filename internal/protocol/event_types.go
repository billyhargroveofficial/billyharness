package protocol

type EventSourceSpec struct {
	Source EventSource
	Doc    string
}

type EventTypeSpec struct {
	Type        EventType
	RequiredIDs []string
	Payload     string
	Doc         string
}

var eventSourceSpecs = [...]EventSourceSpec{
	{Source: EventSourceAgent, Doc: "Agent runtime loop and tool orchestration"},
	{Source: EventSourceGateway, Doc: "Gateway session, replay, and transport layer"},
	{Source: EventSourceTUI, Doc: "Terminal UI client"},
	{Source: EventSourceTelegram, Doc: "Telegram adapter client"},
	{Source: EventSourceTool, Doc: "Native or MCP tool execution"},
	{Source: EventSourceProvider, Doc: "Model provider client"},
	{Source: EventSourceMCP, Doc: "MCP server or client integration"},
	{Source: EventSourceBench, Doc: "Benchmark and replay tooling"},
}

var eventTypeSpecs = [...]EventTypeSpec{
	{Type: EventRunStarted, RequiredIDs: []string{"run_id"}, Payload: "map[string]any", Doc: "Run lifecycle begins"},
	{Type: EventTurnStarted, RequiredIDs: []string{"run_id", "turn_id"}, Payload: "TurnEvent", Doc: "Conversation turn begins"},
	{Type: EventTurnCompleted, RequiredIDs: []string{"run_id", "turn_id"}, Payload: "TurnEvent", Doc: "Conversation turn reaches a terminal status"},
	{Type: EventTurnChangeRecorded, RequiredIDs: []string{"run_id", "turn_id"}, Payload: "TurnChangeEvent", Doc: "Filesystem changes were recorded for a turn"},
	{Type: EventTurnChangeReverted, RequiredIDs: []string{"run_id", "turn_id"}, Payload: "TurnChangeEvent", Doc: "Recorded turn changes were reverted or restored"},
	{Type: EventStepStarted, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "StepEvent", Doc: "Runtime step begins"},
	{Type: EventStepCompleted, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "StepEvent", Doc: "Runtime step completes"},
	{Type: EventModelCallStarted, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "ModelCallEvent", Doc: "Provider model call begins; isolated runs include capability attestation"},
	{Type: EventModelCallFinished, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "ModelCallEvent", Doc: "Provider model call finishes"},
	{Type: EventAssistantReasoning, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "string", Doc: "Assistant reasoning stream delta"},
	{Type: EventAssistantDelta, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "string", Doc: "Assistant content stream delta"},
	{Type: EventToolCallRequested, RequiredIDs: []string{"run_id", "call_id"}, Payload: "ToolCall", Doc: "Model requested a tool call"},
	{Type: EventToolPermissionRequested, RequiredIDs: []string{"run_id", "call_id"}, Payload: "ToolPermissionEvent", Doc: "Tool policy requested an approval decision"},
	{Type: EventToolPermissionDecided, RequiredIDs: []string{"run_id", "call_id"}, Payload: "ToolPermissionEvent", Doc: "Tool approval decision was recorded"},
	{Type: EventToolAudit, RequiredIDs: []string{"run_id", "call_id"}, Payload: "map[string]any", Doc: "Tool policy/audit metadata was emitted"},
	{Type: EventToolCallProgress, RequiredIDs: []string{"run_id", "call_id"}, Payload: "ToolProgressEvent", Doc: "Tool call emitted progress metadata"},
	{Type: EventToolCallStarted, RequiredIDs: []string{"run_id", "call_id", "attempt_id"}, Payload: "string", Doc: "Tool call attempt begins"},
	{Type: EventToolCallFinished, RequiredIDs: []string{"run_id", "call_id", "attempt_id"}, Payload: "ToolResult", Doc: "Tool call attempt completed successfully"},
	{Type: EventToolCallFailed, RequiredIDs: []string{"run_id", "call_id", "attempt_id"}, Payload: "ToolResult", Doc: "Tool call attempt failed"},
	{Type: EventToolCallAborted, RequiredIDs: []string{"run_id", "call_id", "attempt_id"}, Payload: "ToolResult", Doc: "Tool call attempt was aborted"},
	{Type: EventToolOutputRefCreated, RequiredIDs: []string{"run_id", "call_id", "attempt_id"}, Payload: "ToolOutputRefEvent", Doc: "Large tool output was stored behind an output ref"},
	{Type: EventContextThreshold, RequiredIDs: []string{"run_id"}, Payload: "ContextThresholdEvent", Doc: "Context budget threshold was crossed"},
	{Type: EventContextCompacted, RequiredIDs: []string{"run_id"}, Payload: "map[string]any", Doc: "Context compaction completed"},
	{Type: EventHookStarted, RequiredIDs: []string{"run_id"}, Payload: "HookEvent", Doc: "Hook execution begins"},
	{Type: EventHookFinished, RequiredIDs: []string{"run_id"}, Payload: "HookEvent", Doc: "Hook execution finished"},
	{Type: EventHookFailed, RequiredIDs: []string{"run_id"}, Payload: "HookEvent", Doc: "Hook execution failed"},
	{Type: EventRunCompleted, RequiredIDs: []string{"run_id"}, Payload: "", Doc: "Run completed successfully"},
	{Type: EventRunFailed, RequiredIDs: []string{"run_id"}, Payload: "string", Doc: "Run failed"},
	{Type: EventProviderUsageUpdate, RequiredIDs: []string{"run_id", "turn_id", "step_id"}, Payload: "map[string]any", Doc: "Provider token usage snapshot was updated"},
	{Type: EventProviderHelperUsage, RequiredIDs: []string{"run_id"}, Payload: "ProviderHelperUsageEvent", Doc: "Helper model/API usage was recorded"},
	{Type: EventSessionStatus, Payload: "gatewayapi.SessionStatus", Doc: "Gateway emitted a session status snapshot"},
	{Type: EventGatewayStreamGap, Payload: "GatewayStreamGapEvent", Doc: "Gateway live stream dropped events and replay is required"},
	{Type: EventStreamStillRunning, Payload: "StreamStillRunningEvent", Doc: "Long-running stream heartbeat"},
	{Type: EventSessionImported, Payload: "SessionImportedEvent", Doc: "External transcript import completed"},
	{Type: EventUserInputRequested, RequiredIDs: []string{"run_id", "turn_id", "step_id", "call_id", "attempt_id"}, Payload: "UserInputRequestEvent", Doc: "Tool requested user input"},
	{Type: EventUserInputAnswered, RequiredIDs: []string{"run_id", "turn_id", "step_id", "call_id", "attempt_id"}, Payload: "UserInputAnswerEvent", Doc: "User input request was answered"},
	{Type: EventUserInputRejected, RequiredIDs: []string{"run_id", "turn_id", "step_id", "call_id", "attempt_id"}, Payload: "UserInputRejectEvent", Doc: "User input request was rejected"},
}

var eventSourceSpecBySource = func() map[EventSource]EventSourceSpec {
	out := make(map[EventSource]EventSourceSpec, len(eventSourceSpecs))
	for _, spec := range eventSourceSpecs {
		out[spec.Source] = spec
	}
	return out
}()

var eventTypeSpecByType = func() map[EventType]EventTypeSpec {
	out := make(map[EventType]EventTypeSpec, len(eventTypeSpecs))
	for _, spec := range eventTypeSpecs {
		out[spec.Type] = spec
	}
	return out
}()

func EventSourceDocs() []EventSourceSpec {
	out := make([]EventSourceSpec, len(eventSourceSpecs))
	copy(out, eventSourceSpecs[:])
	return out
}

func EventTypeDocs() []EventTypeSpec {
	out := make([]EventTypeSpec, len(eventTypeSpecs))
	for i, spec := range eventTypeSpecs {
		spec.RequiredIDs = copyEventTypeStrings(spec.RequiredIDs)
		out[i] = spec
	}
	return out
}

func eventTypeSpec(eventType EventType) (EventTypeSpec, bool) {
	spec, ok := eventTypeSpecByType[eventType]
	if !ok {
		return EventTypeSpec{}, false
	}
	spec.RequiredIDs = copyEventTypeStrings(spec.RequiredIDs)
	return spec, true
}

func copyEventTypeStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
