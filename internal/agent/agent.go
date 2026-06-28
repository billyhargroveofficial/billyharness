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
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
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
	emit(protocol.Event{Type: protocol.EventRunStarted})
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
		emit(protocol.Event{Type: protocol.EventTurnStarted, Data: protocol.TurnEvent{
			TurnID:       turnID,
			Round:        roundNum,
			Status:       protocol.TurnStatusStarted,
			Model:        a.cfg.Model,
			MessageCount: len(messages),
		}})
		modelStepID := agentStepID(turnID, protocol.StepKindModelCall, 1)
		modelStarted := time.Now()
		emit(protocol.Event{Type: protocol.EventStepStarted, Data: protocol.StepEvent{
			TurnID:       turnID,
			StepID:       modelStepID,
			Round:        roundNum,
			Kind:         protocol.StepKindModelCall,
			Status:       protocol.StepStatusStarted,
			Name:         a.cfg.Model,
			MessageCount: len(messages),
		}})
		emit(protocol.Event{Type: protocol.EventModelCallStarted, Data: map[string]any{"round": roundNum}})
		events, errs := a.provider.Stream(ctx, provider.Request{
			Model:    a.cfg.Model,
			Messages: messages,
			Tools:    a.tools.Specs(),
		})
		var content string
		var reasoning string
		var firstDeltaAt time.Time
		var acc provider.ToolAccumulator
		for event := range events {
			switch event.Kind {
			case provider.EventContent:
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
				}
				content += event.Text
				emit(protocol.Event{Type: protocol.EventAssistantDelta, Data: event.Text})
			case provider.EventReasoning:
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
				}
				reasoning += event.Text
				emit(protocol.Event{Type: protocol.EventAssistantReasoning, Data: event.Text})
			case provider.EventToolCallDelta:
				acc.Push(event)
			case provider.EventUsage:
				if event.Usage.InputTokens > 0 {
					lastPromptTokens = event.Usage.InputTokens
				}
				emit(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: event.Usage})
			case provider.EventDone:
			}
		}
		if err := <-errs; err != nil {
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
		emit(protocol.Event{Type: protocol.EventModelCallFinished, Data: map[string]any{"round": roundNum}})
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
}

func (a *Agent) executeToolCalls(ctx context.Context, turnID string, round int, calls []protocol.ToolCall, emit func(protocol.Event)) []toolExecutionResult {
	results := make([]toolExecutionResult, len(calls))
	for _, call := range calls {
		emit(protocol.Event{Type: protocol.EventToolCallRequested, Data: call})
		a.emitToolAudit(call, emit)
	}
	for i := 0; i < len(calls); {
		if !a.canRunToolParallel(calls[i]) {
			results[i] = a.executeOneTool(ctx, turnID, round, i, calls[i], false, "", 0, 0, emit)
			i++
			continue
		}
		j := i + 1
		for j < len(calls) && a.canRunToolParallel(calls[j]) {
			j++
		}
		a.executeParallelToolBatch(ctx, turnID, round, calls, i, j, results, emit)
		i = j
	}
	return results
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

func (a *Agent) executeParallelToolBatch(ctx context.Context, turnID string, round int, calls []protocol.ToolCall, start, end int, results []toolExecutionResult, emit func(protocol.Event)) {
	limit := a.cfg.MaxParallelTools
	if limit <= 1 || end-start == 1 {
		for i := start; i < end; i++ {
			results[i] = a.executeOneTool(ctx, turnID, round, i, calls[i], false, "", 0, 0, emit)
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
		emitToolStepStarted(emit, turnID, round, i, calls[i], true, batchID, batchSize, limit, a.toolStepMetadata(calls[i], true, batchSize))
		emit(protocol.Event{Type: protocol.EventToolCallStarted, Data: calls[i].Name})
	}
	jobs := make(chan int)
	done := make(chan toolExecutionResult, end-start)
	var wg sync.WaitGroup
	for worker := 0; worker < limit; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				done <- a.callTool(ctx, idx, calls[idx])
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
		emit(protocol.Event{Type: protocol.EventToolCallFinished, Data: result.Result})
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

func (a *Agent) executeOneTool(ctx context.Context, turnID string, round, index int, call protocol.ToolCall, parallel bool, batchID string, batchSize, limit int, emit func(protocol.Event)) toolExecutionResult {
	emitToolStepStarted(emit, turnID, round, index, call, parallel, batchID, batchSize, limit, a.toolStepMetadata(call, parallel, batchSize))
	emit(protocol.Event{Type: protocol.EventToolCallStarted, Data: call.Name})
	result := a.callTool(ctx, index, call)
	emitToolStepCompleted(emit, turnID, round, result, parallel, batchID, batchSize, limit)
	emit(protocol.Event{Type: protocol.EventToolCallFinished, Data: result.Result})
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

func (a *Agent) callTool(ctx context.Context, index int, call protocol.ToolCall) toolExecutionResult {
	started := time.Now()
	result, err := a.tools.Call(ctx, call)
	out := protocol.ToolResult{
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
			out.ErrorCode = "tool_error"
		}
		if out.Content == "" {
			out.Content = err.Error()
		}
	}
	a.compactToolResult(index, call, &out)
	return toolExecutionResult{Index: index, Call: call, Result: out, DurationMS: durationMS(started)}
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
