package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

var newCompactionSummaryProvider = provider.NewFromBinding

func (a *Agent) compactMessages(ctx context.Context, messages []protocol.Message, observedPromptTokens int64) ([]protocol.Message, *compactionReport, bool) {
	compactedMessages, report, compacted := compactMessages(messages, a.runtime, observedPromptTokens)
	if !compacted || report == nil || config.NormalizeContextCompactStrategy(a.runtime.ContextCompactStrategy) != "model" {
		return compactedMessages, report, compacted
	}
	if err := a.applyModelCompactionSummary(ctx, compactedMessages, report); err != nil {
		report.SummaryError = truncateForCompaction(err.Error(), 240)
		return compactedMessages, report, compacted
	}
	return compactedMessages, report, compacted
}

func (a *Agent) applyModelCompactionSummary(ctx context.Context, messages []protocol.Message, report *compactionReport) error {
	if a == nil || report == nil || report.ReplacementIndex < 0 || report.ReplacementIndex >= len(messages) {
		return fmt.Errorf("invalid compaction replacement index")
	}
	source := strings.TrimSpace(messages[report.ReplacementIndex].Content)
	if source == "" {
		return fmt.Errorf("empty deterministic compaction source")
	}
	binding, maxOutputTokens, timeout := a.compactionSummaryBinding()
	prov, err := newCompactionSummaryProvider(binding)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	temp := 0.1
	events, errs := prov.Stream(ctx, provider.Request{
		Model: binding.Model.Model,
		Messages: []protocol.Message{
			{
				Role:    protocol.RoleSystem,
				Content: "You compress earlier agent conversation context for a future coding-agent turn. Return a compact factual summary only. Preserve current task state, file paths, tool outputs, decisions, failures, IDs, dates, and user constraints. Do not include XML, markdown tables, or raw transcript dumps.",
			},
			{Role: protocol.RoleUser, Content: source},
		},
		Temperature: &temp,
	})
	var b strings.Builder
	var usage provider.Usage
	for event := range events {
		switch event.Kind {
		case provider.EventContent:
			b.WriteString(event.Text)
		case provider.EventUsage:
			usage = event.Usage
		}
	}
	if err := <-errs; err != nil {
		return err
	}
	text := compactModelSummaryText(b.String(), maxOutputTokens)
	if text == "" {
		return fmt.Errorf("model compaction summary was empty")
	}
	messages[report.ReplacementIndex].Content = buildModelCompactionSummary(report.CompactionID, text)
	report.SummaryStrategy = "model"
	report.SummaryProvider = binding.Provider.Provider
	report.SummaryModel = binding.Model.Model
	report.SummaryChars = len(messages[report.ReplacementIndex].Content)
	report.SummaryEstimatedTokens = estimateMessagesTokens([]protocol.Message{messages[report.ReplacementIndex]})
	report.AfterEstimatedTokens = estimateMessagesTokens(messages)
	report.ActiveEstimatedTokens = report.AfterEstimatedTokens
	report.ModelSummaryInputTokens = usage.InputTokens
	report.ModelSummaryOutputTokens = usage.OutputTokens
	return nil
}

func (a *Agent) compactionSummaryBinding() (config.ProviderBinding, int, time.Duration) {
	binding := a.providerBinding
	model := modelinfo.NormalizeAlias(a.runtime.ContextCompactSummaryModel)
	if model == "" {
		model = modelinfo.NormalizeAlias(a.toolPolicy.WebSummaryModel)
	}
	if binding.Model.DisableSpark && modelinfo.IsSparkModel(model) {
		model = "gpt-5.4-mini"
	}
	providerID := modelinfo.NormalizeProvider(a.runtime.ContextCompactSummaryProvider)
	if providerID == "" {
		providerID = modelinfo.NormalizeProvider(a.toolPolicy.WebSummaryProvider)
	}
	if model == "" {
		model = modelinfo.DefaultSummaryModel(binding.Model.Model, binding.Provider.Provider)
	}
	binding.Model.Model = model
	binding.Provider.Provider = modelinfo.ProviderForModel(model, providerID)
	binding.Model.Thinking = "disabled"
	binding.Model.ReasoningEffort = "off"
	maxOutputTokens := a.toolPolicy.WebSummaryMaxOutputTokens
	if maxOutputTokens <= 0 {
		maxOutputTokens = 700
	}
	timeout := a.toolPolicy.WebSummaryTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	binding.Limits.RequestTimeout = timeout
	if binding.Limits.StreamIdleTimeout <= 0 || binding.Limits.StreamIdleTimeout > timeout {
		binding.Limits.StreamIdleTimeout = timeout
	}
	return binding, maxOutputTokens, timeout
}

func compactModelSummaryText(text string, maxTokens int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	maxChars := maxTokens * 4
	if maxChars <= 0 {
		maxChars = 2800
	}
	return truncateForCompaction(text, maxChars)
}

func buildModelCompactionSummary(id, text string) string {
	return strings.TrimSpace(fmt.Sprintf("%s\nEarlier conversation context was compacted before the next model call.\nCompaction id: %s.\nModel summary:\n%s", compactionMarker, id, strings.TrimSpace(text)))
}
