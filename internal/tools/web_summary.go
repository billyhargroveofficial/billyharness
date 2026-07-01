package tools

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

const defaultWebSummaryConcurrency = 2

type webModelSummary struct {
	Text         string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CacheHit     int64
	CacheMiss    int64
	CostUSD      float64
}

type webSummaryRequestSettings struct {
	Provider        string
	Model           string
	MaxInputTokens  int
	MaxOutputTokens int
	Timeout         time.Duration
}

func (r *Registry) modelWebSummary(ctx context.Context, src webtools.SummarySource) (webModelSummary, error) {
	if r.webSummarizer == nil {
		return webModelSummary{}, fmt.Errorf("web model summarizer unavailable")
	}
	settings := r.webSummarySettings()
	summaryCtx := ctx
	var cancel context.CancelFunc
	if settings.Timeout > 0 {
		summaryCtx, cancel = context.WithTimeout(ctx, settings.Timeout)
		defer cancel()
	}
	release, err := r.acquireWebSummarySlot(summaryCtx)
	if err != nil {
		return webModelSummary{}, err
	}
	defer release()
	summary, err := r.webSummarizer.SummarizeWeb(summaryCtx, webtools.SummaryRequest{
		RequestID:       r.nextWebSummaryRequestID(),
		ToolName:        "web_summary",
		Source:          src,
		Provider:        settings.Provider,
		Model:           settings.Model,
		MaxInputTokens:  settings.MaxInputTokens,
		MaxOutputTokens: settings.MaxOutputTokens,
		Timeout:         settings.Timeout,
		AllowTools:      false,
	})
	if err != nil {
		return webModelSummary{}, err
	}
	text := webtools.NormalizeSummaryOutput(summary.Text, settings.MaxOutputTokens)
	if text == "" {
		return webModelSummary{}, fmt.Errorf("web summarizer returned empty summary")
	}
	inputTokens := summary.InputTokens
	if inputTokens == 0 {
		inputTokens = int64(webtools.EstimateTokens(src.Text))
	}
	outputTokens := summary.OutputTokens
	if outputTokens == 0 {
		outputTokens = int64(estimateTokens(text))
	}
	return webModelSummary{
		Text:         text,
		Provider:     firstNonEmpty(summary.Provider, settings.Provider),
		Model:        firstNonEmpty(summary.Model, settings.Model),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CacheHit:     summary.CacheHit,
		CacheMiss:    summary.CacheMiss,
		CostUSD:      summary.CostUSD,
	}, nil
}

func (r *Registry) acquireWebSummarySlot(ctx context.Context) (func(), error) {
	if r == nil {
		return func() {}, nil
	}
	slots := r.webSummarySlots
	if slots == nil {
		slots = make(chan struct{}, defaultWebSummaryConcurrency)
	}
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Registry) nextWebSummaryRequestID() string {
	if r == nil {
		return "websum-0"
	}
	id := atomic.AddInt64(&r.webSummarySeq, 1)
	return fmt.Sprintf("websum-%d", id)
}

func (r *Registry) webSummarySettings() webSummaryRequestSettings {
	if r == nil {
		return webSummaryRequestSettings{}
	}
	settings := r.toolPolicy
	return webSummaryRequestSettings{
		Provider:        settings.WebSummaryProvider,
		Model:           settings.WebSummaryModel,
		MaxInputTokens:  settings.WebSummaryMaxInputTokens,
		MaxOutputTokens: settings.WebSummaryMaxOutputTokens,
		Timeout:         settings.WebSummaryTimeout,
	}
}
