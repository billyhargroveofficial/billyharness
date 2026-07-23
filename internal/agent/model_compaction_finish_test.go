package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestModelCompactionRejectsQwenLikeOutputLimitFinish(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.ContextCompactTokens = 10
	cfg.ContextCompactKeep = 1
	cfg.ContextCompactMaxChars = 2000
	cfg.ContextCompactStrategy = "model"
	cfg.ContextCompactSummaryProvider = "mock"
	cfg.ContextCompactSummaryModel = "mock-summary"

	oldSummaryProvider := newCompactionSummaryProvider
	newCompactionSummaryProvider = func(config.ProviderBinding) (provider.Provider, error) {
		return compactionFinishProvider{events: []provider.Event{
			{Kind: provider.EventContent, Text: "QWEN_PARTIAL_MUST_NOT_REPLACE_DETERMINISTIC_SUMMARY"},
			{Kind: provider.EventUsage, Usage: provider.Usage{OutputTokens: 8}},
			{Kind: provider.EventDone, Finish: provider.Finish{Kind: provider.FinishOutputLimit, RawReason: "length"}},
		}}, nil
	}
	t.Cleanup(func() { newCompactionSummaryProvider = oldSummaryProvider })

	a := New(cfg, provider.Mock{}, tools.NewRegistry(cfg))
	compacted, report, ok := a.compactMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: strings.Repeat("old context ", 100)},
		{Role: protocol.RoleAssistant, Content: "old answer"},
		{Role: protocol.RoleUser, Content: "latest task"},
	}, 100)
	if !ok || report == nil {
		t.Fatalf("compaction did not run: ok=%v report=%#v", ok, report)
	}
	if report.SummaryStrategy != "deterministic" || !strings.Contains(report.SummaryError, "output_limit") || !strings.Contains(report.SummaryError, "length") {
		t.Fatalf("report = %#v", report)
	}
	if strings.Contains(compacted[report.ReplacementIndex].Content, "QWEN_PARTIAL_MUST_NOT_REPLACE") {
		t.Fatalf("partial model output replaced deterministic summary: %q", compacted[report.ReplacementIndex].Content)
	}
}

type compactionFinishProvider struct {
	events []provider.Event
}

func (p compactionFinishProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, len(p.events))
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		for _, event := range p.events {
			select {
			case events <- event:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
	}()
	return events, errs
}
