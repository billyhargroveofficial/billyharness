package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

type WebSummarizer struct {
	Binding     config.ProviderBinding
	ToolPolicy  config.ToolPolicySettings
	NewProvider func(config.ProviderBinding) (Provider, error)
}

func NewWebSummarizerFromProjections(binding config.ProviderBinding, toolPolicy config.ToolPolicySettings) webtools.Summarizer {
	return WebSummarizer{Binding: binding, ToolPolicy: toolPolicy, NewProvider: NewFromBinding}
}

func (s WebSummarizer) SummarizeWeb(ctx context.Context, req webtools.SummaryRequest) (webtools.SummaryResult, error) {
	binding, settings := s.summaryBinding(req)
	input, inputTruncated := webtools.SummaryInput(req.Source.Text, settings.MaxInputTokens)
	prompt := webtools.SummaryPrompt(req.Source, input, inputTruncated)
	estimatedInputTokens := int64(webtools.EstimateTokens(prompt))
	if settings.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, settings.Timeout)
		defer cancel()
	}
	newProvider := s.NewProvider
	if newProvider == nil {
		newProvider = NewFromBinding
	}
	prov, err := newProvider(binding)
	if err != nil {
		return webtools.SummaryResult{}, err
	}
	temp := 0.1
	events, errs := prov.Stream(ctx, Request{
		Model: binding.Model.Model,
		Messages: []protocol.Message{
			{Role: protocol.RoleSystem, Content: webtools.SummarySystemPrompt},
			{Role: protocol.RoleUser, Content: prompt},
		},
		Temperature: &temp,
	})
	var content strings.Builder
	var usage Usage
	for event := range events {
		switch event.Kind {
		case EventContent:
			content.WriteString(event.Text)
		case EventUsage:
			usage = event.Usage
		}
	}
	if err := <-errs; err != nil {
		return webtools.SummaryResult{}, err
	}
	text := webtools.NormalizeSummaryOutput(content.String(), settings.MaxOutputTokens)
	if text == "" {
		return webtools.SummaryResult{}, fmt.Errorf("web summarizer returned empty summary")
	}
	inputTokens := usage.InputTokens
	if inputTokens == 0 {
		inputTokens = estimatedInputTokens
	}
	outputTokens := usage.OutputTokens
	if outputTokens == 0 {
		outputTokens = int64(webtools.EstimateTokens(text))
	}
	cacheHit := usage.CacheHitTokens
	cacheMiss := usage.CacheMissTokens
	return webtools.SummaryResult{
		Text:         text,
		Provider:     binding.Provider.Provider,
		Model:        binding.Model.Model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CacheHit:     cacheHit,
		CacheMiss:    cacheMiss,
		CostUSD:      webSummaryCostUSD(binding.Model.Model, inputTokens, outputTokens, cacheHit, cacheMiss),
	}, nil
}

type webSummarySettings struct {
	Provider        string
	Model           string
	MaxInputTokens  int
	MaxOutputTokens int
	Timeout         time.Duration
}

func (s WebSummarizer) summaryBinding(req webtools.SummaryRequest) (config.ProviderBinding, webSummarySettings) {
	binding := normalizeSummaryBaseBinding(s.Binding)
	settings := webSummarySettings{
		Provider:        modelinfo.NormalizeProvider(s.ToolPolicy.WebSummaryProvider),
		Model:           modelinfo.NormalizeAlias(s.ToolPolicy.WebSummaryModel),
		MaxInputTokens:  s.ToolPolicy.WebSummaryMaxInputTokens,
		MaxOutputTokens: s.ToolPolicy.WebSummaryMaxOutputTokens,
		Timeout:         s.ToolPolicy.WebSummaryTimeout,
	}
	if req.Provider != "" {
		settings.Provider = modelinfo.NormalizeProvider(req.Provider)
	}
	if req.Model != "" {
		settings.Model = modelinfo.NormalizeAlias(req.Model)
	}
	if binding.Model.DisableSpark && modelinfo.IsSparkModel(settings.Model) {
		settings.Model = "gpt-5.4-mini"
	}
	if settings.Model == "" {
		settings.Model = modelinfo.DefaultSummaryModel(binding.Model.Model, binding.Provider.Provider)
	}
	if settings.Provider == "" {
		settings.Provider = modelinfo.ProviderForModel(settings.Model, binding.Provider.Provider)
	}
	settings.Provider = modelinfo.ProviderForModel(settings.Model, settings.Provider)
	if req.MaxInputTokens > 0 {
		settings.MaxInputTokens = req.MaxInputTokens
	}
	if settings.MaxInputTokens <= 0 {
		settings.MaxInputTokens = 12_000
	}
	if req.MaxOutputTokens > 0 {
		settings.MaxOutputTokens = req.MaxOutputTokens
	}
	if settings.MaxOutputTokens <= 0 {
		settings.MaxOutputTokens = 700
	}
	if req.Timeout > 0 {
		settings.Timeout = req.Timeout
	}
	if settings.Timeout <= 0 {
		settings.Timeout = 60 * time.Second
	}
	binding.Provider.Provider = settings.Provider
	binding.Model.Model = settings.Model
	binding.Model.Thinking = "disabled"
	binding.Model.ReasoningEffort = ""
	binding.Model.MaxTokens = settings.MaxOutputTokens
	if settings.Timeout > 0 {
		binding.Limits.RequestTimeout = settings.Timeout
		if binding.Limits.StreamIdleTimeout <= 0 || binding.Limits.StreamIdleTimeout > settings.Timeout {
			binding.Limits.StreamIdleTimeout = settings.Timeout
		}
	}
	return binding, settings
}

func normalizeSummaryBaseBinding(binding config.ProviderBinding) config.ProviderBinding {
	binding.Model.Model = modelinfo.NormalizeAlias(binding.Model.Model)
	if binding.Model.DisableSpark && modelinfo.IsSparkModel(binding.Model.Model) {
		binding.Model.Model = "gpt-5.4-mini"
	}
	binding.Provider.Provider = modelinfo.ProviderForModel(binding.Model.Model, binding.Provider.Provider)
	return binding
}

func webSummaryCostUSD(model string, inputTokens, outputTokens, cacheHitTokens, cacheMissTokens int64) float64 {
	info := modelinfo.Lookup(model)
	if info.Subscription {
		return 0
	}
	pricing := info.Pricing
	inputCost := float64(inputTokens) * pricing.InputPer1M / 1_000_000
	if cacheHitTokens > 0 || cacheMissTokens > 0 {
		inputCost = (float64(cacheHitTokens)*pricing.CacheHitPer1M + float64(cacheMissTokens)*pricing.CacheMissPer1M) / 1_000_000
	}
	outputCost := float64(outputTokens) * pricing.OutputPer1M / 1_000_000
	return inputCost + outputCost
}
