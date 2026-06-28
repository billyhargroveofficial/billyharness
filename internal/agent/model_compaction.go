package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

var newCompactionSummaryProvider = provider.New

func (a *Agent) compactMessages(ctx context.Context, messages []protocol.Message, observedPromptTokens int64) ([]protocol.Message, *compactionReport, bool) {
	compactedMessages, report, compacted := compactMessages(messages, a.cfg, observedPromptTokens)
	if !compacted || report == nil || config.NormalizeContextCompactStrategy(a.cfg.ContextCompactStrategy) != "model" {
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
	cfg := a.compactionSummaryConfig()
	prov, err := newCompactionSummaryProvider(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.WebSummaryTimeout)
	defer cancel()
	temp := 0.1
	events, errs := prov.Stream(ctx, provider.Request{
		Model: cfg.Model,
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
	text := compactModelSummaryText(b.String(), cfg.WebSummaryMaxOutputTokens)
	if text == "" {
		return fmt.Errorf("model compaction summary was empty")
	}
	messages[report.ReplacementIndex].Content = buildModelCompactionSummary(report.CompactionID, text)
	report.SummaryStrategy = "model"
	report.SummaryProvider = cfg.Provider
	report.SummaryModel = cfg.Model
	report.SummaryChars = len(messages[report.ReplacementIndex].Content)
	report.SummaryEstimatedTokens = estimateMessagesTokens([]protocol.Message{messages[report.ReplacementIndex]})
	report.AfterEstimatedTokens = estimateMessagesTokens(messages)
	report.ActiveEstimatedTokens = report.AfterEstimatedTokens
	report.ModelSummaryInputTokens = usage.InputTokens
	report.ModelSummaryOutputTokens = usage.OutputTokens
	return nil
}

func (a *Agent) compactionSummaryConfig() config.Config {
	cfg := a.cfg
	cfg.Provider = cfg.ContextCompactSummaryProvider
	cfg.Model = cfg.ContextCompactSummaryModel
	if cfg.Model == "" {
		cfg.Model = cfg.WebSummaryModel
	}
	if cfg.Provider == "" {
		cfg.Provider = cfg.WebSummaryProvider
	}
	cfg.Thinking = "disabled"
	cfg.ReasoningEffort = "off"
	cfg.MaxToolRounds = 0
	cfg.ApplyModelProviderDefaults()
	cfg.ApplyWebSummaryDefaults()
	return cfg
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
