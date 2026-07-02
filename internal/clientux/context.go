package clientux

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type ContextReportOptions struct {
	Runtime  gatewayapi.ContextRuntime
	Usage    gatewayapi.ContextUsage
	Events   []protocol.Event
	Warnings []string
}

func BuildContextResponse(limits config.RuntimeLimits, id string, messages []protocol.Message) gatewayapi.SessionContextResponse {
	return BuildContextResponseWithOptions(limits, id, messages, ContextReportOptions{})
}

func BuildContextResponseWithOptions(limits config.RuntimeLimits, id string, messages []protocol.Message, opts ContextReportOptions) gatewayapi.SessionContextResponse {
	estimatedTokens := estimateMessagesTokens(messages)
	contextWindow := limits.ContextWindowTokens
	compactAt := int64(limits.ContextCompactTokens)
	var percentUsed float64
	if contextWindow > 0 {
		percentUsed = float64(estimatedTokens) / float64(contextWindow) * 100
	}
	var thresholdPercent float64
	if contextWindow > 0 && compactAt > 0 {
		thresholdPercent = float64(compactAt) / float64(contextWindow) * 100
	}
	sourceStats := map[string]*gatewayapi.ContextSource{}
	contributors := make([]gatewayapi.ContextContributor, 0, len(messages))
	for i, msg := range messages {
		chars := messageChars(msg)
		if chars == 0 {
			continue
		}
		source := contextSource(msg)
		tokens := messageTokens(msg)
		diagnostics := contextMessageDiagnostics(limits, msg)
		for _, contribution := range contextSourceContributions(msg) {
			stat := sourceStats[contribution.source]
			if stat == nil {
				stat = &gatewayapi.ContextSource{Source: contribution.source}
				sourceStats[contribution.source] = stat
			}
			stat.MessageCount++
			stat.Chars += contribution.chars
			stat.EstimatedTokens += int64((contribution.chars + 3) / 4)
			if contribution.source == source {
				if diagnostics.largeInline {
					stat.LargeInlineCount++
				}
				if diagnostics.hasOutputRef {
					stat.OutputRefCount++
				}
			}
		}
		contributors = append(contributors, gatewayapi.ContextContributor{
			Index:             i,
			Role:              string(msg.Role),
			Source:            source,
			Name:              msg.Name,
			Chars:             chars,
			EstimatedTokens:   tokens,
			Preview:           previewMessage(messagePreviewText(msg), 120),
			LargeInline:       diagnostics.largeInline,
			HasOutputRef:      diagnostics.hasOutputRef,
			InlineBudgetBytes: diagnostics.inlineBudgetBytes,
		})
	}
	sort.Slice(contributors, func(i, j int) bool {
		if contributors[i].EstimatedTokens == contributors[j].EstimatedTokens {
			return contributors[i].Index < contributors[j].Index
		}
		return contributors[i].EstimatedTokens > contributors[j].EstimatedTokens
	})
	if len(contributors) > 5 {
		contributors = contributors[:5]
	}
	sources := make([]gatewayapi.ContextSource, 0, len(sourceStats))
	for _, stat := range sourceStats {
		if estimatedTokens > 0 {
			stat.Percent = float64(stat.EstimatedTokens) / float64(estimatedTokens) * 100
		}
		sources = append(sources, *stat)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].EstimatedTokens == sources[j].EstimatedTokens {
			return sources[i].Source < sources[j].Source
		}
		return sources[i].EstimatedTokens > sources[j].EstimatedTokens
	})
	outputRefBuckets := 0
	largeInlineCount := 0
	outputRefCount := 0
	for _, source := range sources {
		if source.OutputRefCount > 0 {
			outputRefBuckets++
			outputRefCount += source.OutputRefCount
		}
		largeInlineCount += source.LargeInlineCount
	}
	eventReport := contextEventReport(opts.Events)
	runtime := mergeContextRuntime(opts.Runtime, eventReport.runtime)
	usage := mergeContextUsage(opts.Usage, eventReport.usage)
	prompt := eventReport.prompt
	if eventReport.outputRefs > outputRefCount {
		outputRefCount = eventReport.outputRefs
	}
	return gatewayapi.SessionContextResponse{
		ID:                      id,
		MessageCount:            len(messages),
		AttachmentCount:         protocol.MessageAttachmentCount(messages),
		ImageSubmissions:        protocol.MessageImageSubmissionCount(messages),
		EstimatedTokens:         estimatedTokens,
		ContextWindowTokens:     contextWindow,
		ContextCompactTokens:    compactAt,
		PercentUsed:             percentUsed,
		CompactThresholdPercent: thresholdPercent,
		OverCompactThreshold:    compactAt > 0 && estimatedTokens >= compactAt,
		Estimator:               "chars_div_4",
		Sources:                 sources,
		Thresholds:              contextThresholds(estimatedTokens, contextWindow),
		TopContributors:         contributors,
		Runtime:                 runtime,
		Usage:                   usage,
		Prompt:                  prompt,
		LastCompaction:          eventReport.compaction,
		OutputRefs: gatewayapi.ContextOutputRefs{
			Count:             outputRefCount,
			LargeInlineCount:  largeInlineCount,
			SourceBucketCount: outputRefBuckets,
		},
		Warnings: append([]string(nil), opts.Warnings...),
	}
}

type contextEventMetrics struct {
	runtime    gatewayapi.ContextRuntime
	usage      gatewayapi.ContextUsage
	prompt     gatewayapi.ContextPrompt
	compaction *gatewayapi.ContextCompaction
	outputRefs int
	usageAcc   contextUsageAccumulator
	helperSeen map[string]bool
}

func contextEventReport(events []protocol.Event) contextEventMetrics {
	metrics := contextEventMetrics{helperSeen: map[string]bool{}}
	for _, event := range events {
		switch event.Type {
		case protocol.EventModelCallStarted:
			metrics.usage.ModelCalls++
			metrics.usageAcc.Reset()
			metrics.observeModelCall(event.Data)
		case protocol.EventModelCallFinished:
			metrics.observeModelCall(event.Data)
		case protocol.EventToolCallStarted:
			metrics.usage.ToolCalls++
		case protocol.EventProviderUsageUpdate:
			delta, current := metrics.usageAcc.Apply(contextUsageFromData(event.Data))
			metrics.usage.InputTokens += delta.InputTokens
			metrics.usage.OutputTokens += delta.OutputTokens
			metrics.usage.CacheHitTokens += delta.CacheHitTokens
			metrics.usage.CacheMissTokens += delta.CacheMissTokens
			metrics.usage.ReasoningTokens += delta.ReasoningTokens
			metrics.usage.LastInputTokens = current.InputTokens
			metrics.usage.LastOutputTokens = current.OutputTokens
			metrics.usage.LastCacheHitTokens = current.CacheHitTokens
			metrics.usage.LastCacheMissTokens = current.CacheMissTokens
		case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
			metrics.observeToolSummary(event)
		case protocol.EventProviderHelperUsage:
			metrics.observeHelperUsage(event)
		case protocol.EventToolOutputRefCreated:
			metrics.outputRefs++
		case protocol.EventContextCompacted:
			metrics.compaction = contextCompactionFromEvent(event)
		}
	}
	return metrics
}

func (m *contextEventMetrics) observeModelCall(data any) {
	model, ok := decodeContextData[protocol.ModelCallEvent](data)
	if !ok {
		return
	}
	if model.ProviderID != "" {
		m.runtime.Provider = model.ProviderID
	}
	if model.ModelID != "" {
		m.runtime.Model = model.ModelID
	}
	if model.ReasoningMode != "" {
		m.runtime.ReasoningMode = model.ReasoningMode
	} else if model.Reasoning != "" {
		m.runtime.ReasoningMode = model.Reasoning
	}
	if model.PromptInventory != nil {
		m.prompt = promptReportFromInventory(model.PromptInventory)
	}
	if model.PromptInventoryHash != "" {
		m.prompt.InventoryHash = model.PromptInventoryHash
	}
	if model.PromptCacheBreak != nil {
		m.prompt.CacheStatus = model.PromptCacheBreak.Status
		m.prompt.CacheReason = model.PromptCacheBreak.Reason
	}
}

func promptReportFromInventory(inventory *protocol.PromptInventory) gatewayapi.ContextPrompt {
	if inventory == nil {
		return gatewayapi.ContextPrompt{}
	}
	return gatewayapi.ContextPrompt{
		InventoryHash: inventory.Hash,
		SectionCount:  len(inventory.Sections),
		TotalBytes:    inventory.TotalBytes,
		ApproxTokens:  inventory.ApproxTokens,
		ToolSchemas:   inventory.ToolSchemaCount,
		Sections:      append([]protocol.PromptSection(nil), inventory.Sections...),
	}
}

func (m *contextEventMetrics) observeToolSummary(event protocol.Event) {
	result, ok := decodeContextData[protocol.ToolResult](event.Data)
	if !ok || len(result.Metadata) == 0 {
		return
	}
	in := metadataInt64(result.Metadata, "tool_summary_input_tokens")
	out := metadataInt64(result.Metadata, "tool_summary_output_tokens")
	m.usage.WebSummaryInputTokens += in
	m.usage.WebSummaryOutputTokens += out
	callID := firstContextString(result.CallID, event.CallID)
	if callID != "" && m.helperSeen[callID] {
		return
	}
	apiInput := metadataInt64(result.Metadata, "tool_summary_api_input_tokens")
	apiOutput := metadataInt64(result.Metadata, "tool_summary_api_output_tokens")
	api := metadataInt64(result.Metadata, "tool_summary_api_total_tokens")
	if api == 0 {
		api = metadataInt64(result.Metadata, "tool_summary_api_tokens")
	}
	if api == 0 {
		api = apiInput + apiOutput
	}
	cacheHit := metadataInt64(result.Metadata, "tool_summary_api_cache_hit_tokens")
	cacheMiss := metadataInt64(result.Metadata, "tool_summary_api_cache_miss_tokens")
	if api <= 0 && apiInput <= 0 && apiOutput <= 0 && cacheHit <= 0 && cacheMiss <= 0 && !metadataBool(result.Metadata, "tool_summary_external_model_used") {
		return
	}
	m.usage.HelperModelCalls++
	m.usage.HelperModelInputTokens += apiInput
	m.usage.HelperModelOutputTokens += apiOutput
	m.usage.HelperModelAPITokens += api
	m.usage.HelperModelCacheHit += cacheHit
	m.usage.HelperModelCacheMiss += cacheMiss
}

func (m *contextEventMetrics) observeHelperUsage(event protocol.Event) {
	usage, ok := decodeContextData[protocol.ProviderHelperUsageEvent](event.Data)
	if !ok {
		return
	}
	api := usage.APITokens
	if api == 0 {
		api = usage.InputTokens + usage.OutputTokens
	}
	kind := strings.TrimSpace(usage.Kind)
	if kind == "web_summary" || api > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheHitTokens > 0 || usage.CacheMissTokens > 0 {
		m.usage.HelperModelCalls++
		m.usage.HelperModelInputTokens += usage.InputTokens
		m.usage.HelperModelOutputTokens += usage.OutputTokens
		m.usage.HelperModelAPITokens += api
		m.usage.HelperModelCacheHit += usage.CacheHitTokens
		m.usage.HelperModelCacheMiss += usage.CacheMissTokens
	}
	m.usage.HelperAPICalls += usage.APICalls
	m.usage.HelperCostUSD += usage.CostUSD
	if kind == "web_summary" {
		callID := firstContextString(usage.CallID, event.CallID)
		if callID != "" {
			m.helperSeen[callID] = true
		}
	}
}

func contextCompactionFromEvent(event protocol.Event) *gatewayapi.ContextCompaction {
	m := mapFromContextData(event.Data)
	if len(m) == 0 {
		return nil
	}
	return &gatewayapi.ContextCompaction{
		Seq:          event.Seq,
		CompactionID: metadataString(m, "compaction_id"),
		Strategy:     metadataString(m, "summary_strategy"),
		BeforeTokens: metadataInt64(m, "before_estimated_tokens"),
		AfterTokens:  metadataInt64(m, "after_estimated_tokens"),
		Reason:       metadataString(m, "reason"),
	}
}

func mergeContextRuntime(primary, fallback gatewayapi.ContextRuntime) gatewayapi.ContextRuntime {
	out := primary
	if out.Provider == "" {
		out.Provider = fallback.Provider
	}
	if out.Model == "" {
		out.Model = fallback.Model
	}
	if out.Profile == "" {
		out.Profile = fallback.Profile
	}
	if out.ReasoningMode == "" {
		out.ReasoningMode = fallback.ReasoningMode
	}
	if out.AccessMode == "" {
		out.AccessMode = fallback.AccessMode
	}
	return out
}

func mergeContextUsage(primary, fallback gatewayapi.ContextUsage) gatewayapi.ContextUsage {
	if !contextUsageEmpty(primary) {
		return primary
	}
	return fallback
}

func contextUsageEmpty(usage gatewayapi.ContextUsage) bool {
	return usage == (gatewayapi.ContextUsage{})
}

type contextUsage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64
}

type contextUsageAccumulator struct {
	last contextUsage
}

func (a *contextUsageAccumulator) Reset() {
	a.last = contextUsage{}
}

func (a *contextUsageAccumulator) Apply(update contextUsage) (contextUsage, contextUsage) {
	if update == (contextUsage{}) {
		return contextUsage{}, a.last
	}
	if update.atLeast(a.last) {
		delta := update.minus(a.last)
		a.last = update
		return delta, update
	}
	a.last = update
	return update, update
}

func (u contextUsage) atLeast(other contextUsage) bool {
	return u.InputTokens >= other.InputTokens &&
		u.OutputTokens >= other.OutputTokens &&
		u.CacheHitTokens >= other.CacheHitTokens &&
		u.CacheMissTokens >= other.CacheMissTokens &&
		u.ReasoningTokens >= other.ReasoningTokens
}

func (u contextUsage) minus(other contextUsage) contextUsage {
	return contextUsage{
		InputTokens:     u.InputTokens - other.InputTokens,
		OutputTokens:    u.OutputTokens - other.OutputTokens,
		CacheHitTokens:  u.CacheHitTokens - other.CacheHitTokens,
		CacheMissTokens: u.CacheMissTokens - other.CacheMissTokens,
		ReasoningTokens: u.ReasoningTokens - other.ReasoningTokens,
	}
}

func contextUsageFromData(data any) contextUsage {
	m := mapFromContextData(data)
	return contextUsage{
		InputTokens:     metadataInt64(m, "input_tokens"),
		OutputTokens:    metadataInt64(m, "output_tokens"),
		CacheHitTokens:  metadataInt64(m, "cache_hit_tokens"),
		CacheMissTokens: metadataInt64(m, "cache_miss_tokens"),
		ReasoningTokens: metadataInt64(m, "reasoning_tokens"),
	}
}

type contextDiagnostics struct {
	largeInline       bool
	hasOutputRef      bool
	inlineBudgetBytes int
}

func contextMessageDiagnostics(limits config.RuntimeLimits, msg protocol.Message) contextDiagnostics {
	if msg.Role != protocol.RoleTool {
		return contextDiagnostics{}
	}
	budget := limits.MaxToolOutputBytes
	diag := contextDiagnostics{
		hasOutputRef: messageHasOutputRef(msg),
	}
	if budget > 0 && len(msg.Content) > budget {
		diag.largeInline = true
		diag.inlineBudgetBytes = budget
	}
	return diag
}

func messageHasOutputRef(msg protocol.Message) bool {
	content := strings.ToLower(msg.Content)
	return strings.Contains(content, "output_ref") || strings.Contains(content, "tool-output")
}

func contextThresholds(estimatedTokens, contextWindow int64) []gatewayapi.ContextThreshold {
	if contextWindow <= 0 {
		return nil
	}
	thresholds := []int{50, 70, 85, 95}
	out := make([]gatewayapi.ContextThreshold, 0, len(thresholds))
	for _, percent := range thresholds {
		tokens := (contextWindow*int64(percent) + 99) / 100
		remaining := tokens - estimatedTokens
		if remaining < 0 {
			remaining = 0
		}
		out = append(out, gatewayapi.ContextThreshold{
			Percent:         percent,
			Tokens:          tokens,
			Crossed:         estimatedTokens >= tokens,
			RemainingTokens: remaining,
		})
	}
	return out
}

func contextSource(msg protocol.Message) string {
	switch msg.Role {
	case protocol.RoleSystem:
		if strings.HasPrefix(strings.TrimSpace(msg.Content), "Conversation summary (auto-compact)") {
			return "compaction_summary"
		}
		return "system_instructions"
	case protocol.RoleUser:
		if strings.HasPrefix(strings.TrimSpace(msg.Content), "# Memory context") || strings.Contains(msg.Content, "<MEMORY_CONTEXT>") {
			return "memory_context"
		}
		if strings.HasPrefix(strings.TrimSpace(msg.Content), "# Project context") || strings.Contains(msg.Content, "<PROJECT_CONTEXT>") {
			return "project_context"
		}
		return "user_messages"
	case protocol.RoleAssistant:
		if len(msg.ToolCalls) > 0 {
			return "assistant_tool_calls"
		}
		return "assistant_messages"
	case protocol.RoleTool:
		name := strings.ToLower(strings.TrimSpace(msg.Name))
		switch {
		case strings.HasPrefix(name, "web_"):
			return "web_summaries"
		case strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp "):
			return "mcp_outputs"
		default:
			return "tool_outputs"
		}
	default:
		return string(msg.Role)
	}
}

type contextSourceContribution struct {
	source string
	chars  int
}

func contextSourceContributions(msg protocol.Message) []contextSourceContribution {
	var out []contextSourceContribution
	baseChars := messageCharsWithoutReasoning(msg)
	if baseChars > 0 {
		out = append(out, contextSourceContribution{source: contextSource(msg), chars: baseChars})
	}
	if msg.ReasoningContent != "" {
		out = append(out, contextSourceContribution{source: "reasoning_summaries", chars: len(msg.ReasoningContent)})
	}
	return out
}

func messagePreviewText(msg protocol.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}
	if count := msg.AttachmentCount(); count > 0 {
		if count == 1 {
			return "[1 attachment]"
		}
		return fmt.Sprintf("[%d attachments]", count)
	}
	if len(msg.ToolCalls) == 0 {
		return ""
	}
	var calls []string
	for _, call := range msg.ToolCalls {
		args := strings.TrimSpace(string(call.Arguments))
		if args != "" {
			calls = append(calls, call.Name+" "+args)
		} else {
			calls = append(calls, call.Name)
		}
	}
	return strings.Join(calls, "; ")
}

func estimateMessagesTokens(messages []protocol.Message) int64 {
	var tokens int64
	for _, msg := range messages {
		tokens += messageTokens(msg)
	}
	return tokens
}

func messageTokens(msg protocol.Message) int64 {
	var tokens int64
	for _, contribution := range contextSourceContributions(msg) {
		tokens += int64((contribution.chars + 3) / 4)
	}
	return tokens
}

func messageChars(msg protocol.Message) int {
	chars := messageCharsWithoutReasoning(msg)
	if msg.ReasoningContent != "" {
		chars += len(msg.ReasoningContent)
	}
	return chars
}

func messageCharsWithoutReasoning(msg protocol.Message) int {
	chars := len(msg.Content) + len(msg.Name) + len(msg.ToolCallID) + len(string(msg.Role))
	for _, call := range msg.ToolCalls {
		chars += len(call.ID) + len(call.Name) + len(call.Arguments)
	}
	return chars
}

func previewMessage(content string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	if maxChars <= 3 {
		return content[:maxChars]
	}
	return content[:maxChars-3] + "..."
}

func decodeContextData[T any](data any) (T, bool) {
	var out T
	if data == nil {
		return out, false
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return out, false
	}
	if err := json.Unmarshal(bytes, &out); err != nil {
		return out, false
	}
	return out, true
}

func mapFromContextData(data any) map[string]any {
	if data == nil {
		return nil
	}
	if m, ok := data.(map[string]any); ok {
		return m
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil
	}
	return out
}

func metadataString(metadata map[string]any, key string) string {
	switch value := metadata[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func metadataBool(metadata map[string]any, key string) bool {
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func metadataInt64(metadata map[string]any, key string) int64 {
	switch value := metadata[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func firstContextString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
