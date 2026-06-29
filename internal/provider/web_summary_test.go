package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func TestWebSummarizerUsesProviderAndRecordsUsage(t *testing.T) {
	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	cfg.WebSummaryMaxInputTokens = 300
	cfg.WebSummaryMaxOutputTokens = 80
	cfg.WebSummaryTimeout = time.Second
	summarizer := WebSummarizer{
		Binding:    cfg.ProviderBinding(),
		ToolPolicy: cfg.ToolPolicySettings(),
		NewProvider: func(got config.ProviderBinding) (Provider, error) {
			if got.Provider.Provider != "mock" || got.Model.Model != "mock-summarizer" || got.Model.Thinking != "disabled" || got.Model.MaxTokens != 80 {
				t.Fatalf("summary binding = provider:%q model:%q thinking:%q max:%d", got.Provider.Provider, got.Model.Model, got.Model.Thinking, got.Model.MaxTokens)
			}
			if got.Limits.RequestTimeout != time.Second || got.Limits.StreamIdleTimeout != time.Second {
				t.Fatalf("summary timeout limits = request:%s idle:%s", got.Limits.RequestTimeout, got.Limits.StreamIdleTimeout)
			}
			return summaryProvider{
				content: "Model summary keeps concrete facts.",
				usage:   Usage{InputTokens: 900, OutputTokens: 40, CacheHitTokens: 300, CacheMissTokens: 600},
			}, nil
		},
	}
	result, err := summarizer.SummarizeWeb(context.Background(), webtools.SummaryRequest{
		Source: webtools.SummarySource{
			URL:   "https://example.com/model",
			Title: "Model Summary",
			Query: "facts",
			Text:  strings.Repeat("Raw page evidence. ", 100),
		},
		Provider:        "mock",
		Model:           "mock-summarizer",
		MaxInputTokens:  300,
		MaxOutputTokens: 80,
		Timeout:         time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Model summary keeps concrete facts." ||
		result.Provider != "mock" ||
		result.Model != "mock-summarizer" ||
		result.InputTokens != 900 ||
		result.OutputTokens != 40 ||
		result.CacheHit != 300 ||
		result.CacheMiss != 600 {
		t.Fatalf("summary result = %#v", result)
	}
}

type summaryProvider struct {
	content string
	usage   Usage
}

func (p summaryProvider) Stream(ctx context.Context, req Request) (<-chan Event, <-chan error) {
	events := make(chan Event, 3)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if req.Model == "" || len(req.Messages) != 2 || req.Temperature == nil {
			errs <- context.Canceled
			return
		}
		select {
		case events <- Event{Kind: EventContent, Text: p.content}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		select {
		case events <- Event{Kind: EventUsage, Usage: p.usage}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		select {
		case events <- Event{Kind: EventDone}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
	}()
	return events, errs
}
