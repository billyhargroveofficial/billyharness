package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func TestValidatePublicHTTPURLRejectsLocalAndPrivateTargets(t *testing.T) {
	for _, rawURL := range []string{
		"http://localhost:8080",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"file:///etc/passwd",
	} {
		if _, err := webtools.ValidatePublicHTTPURL(context.Background(), rawURL, nil); err == nil {
			t.Fatalf("validatePublicHTTPURL(%q) returned nil error", rawURL)
		}
	}
}

func TestCompactFetchedPageLimitsDefaultOutput(t *testing.T) {
	text := strings.Repeat("Alpha beta gamma. ", 1000)
	page := fetchedPage{
		URL:         "https://example.com",
		Status:      200,
		ContentType: "text/plain",
		Title:       "Example",
		Text:        text,
		Links:       []string{"https://example.com/a", "https://example.com/b"},
	}
	out := compactFetchedPage(page, webFetchOptions{MaxChars: 1200, MaxLinks: 1})
	if out.OriginalTextChars <= 1200 {
		t.Fatalf("expected large original text, got %d", out.OriginalTextChars)
	}
	if !out.OutputTextTruncated || len([]rune(out.Text)) > 1400 {
		t.Fatalf("text was not compacted: truncated=%v len=%d", out.OutputTextTruncated, len([]rune(out.Text)))
	}
	if !strings.Contains(out.Summary, "Example") || len(out.Links) != 1 {
		t.Fatalf("compact page = %#v", out)
	}
}

func TestWebCompactionDefaultsStaySmall(t *testing.T) {
	page := fetchedPage{
		URL:   "https://example.com",
		Title: "Example",
		Text:  strings.Repeat("Alpha beta gamma delta. ", 2000),
	}
	fetched := compactFetchedPage(page, webFetchOptions{})
	if fetched.Text != "" || fetched.ReturnedTextChars != 0 || fetched.BudgetTextTokens != 0 {
		t.Fatalf("fetch default should not return raw text inline: %#v", fetched)
	}
	if fetched.Summary == "" || fetched.Extract == "" || len(fetched.KeyPoints) == 0 {
		t.Fatalf("fetch default should return digest/extract/key points: %#v", fetched)
	}
	if fetched.EstimatedTextTokens > 700 {
		t.Fatalf("fetch default returned too much: %#v", fetched)
	}

	crawled := compactCrawlPages([]crawlPage{{
		URL:   "https://example.com",
		Title: "Example",
		Text:  page.Text,
	}}, webFetchOptions{})
	if len(crawled) != 1 {
		t.Fatalf("crawl pages = %d", len(crawled))
	}
	if crawled[0].Text != "" || crawled[0].ReturnedTextChars != 0 || crawled[0].BudgetTextTokens != 0 {
		t.Fatalf("crawl default should not return raw text inline: %#v", crawled[0])
	}
	if crawled[0].Summary == "" || crawled[0].Extract == "" || len(crawled[0].KeyPoints) == 0 {
		t.Fatalf("crawl default should return digest/extract/key points: %#v", crawled[0])
	}
	if crawled[0].EstimatedTextTokens > 700 {
		t.Fatalf("crawl default returned too much: %#v", crawled[0])
	}
}

func TestTinyDirectWebAnswerAvoidsSummaryBloatAndModelSummarizer(t *testing.T) {
	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	registry := NewRegistry(cfg, WithWebSummarizer(fatalSummarizer{t: t}))
	page := fetchedPage{
		URL:             "https://wttr.in/Moscow?format=3",
		Status:          200,
		ContentType:     "text/plain",
		Text:            "Moscow: +19C, partly cloudy",
		RawBytesFetched: 28,
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	if compact.OutputClass != "tiny_direct_answer" || compact.SummaryMode != "direct" ||
		compact.SummarizerModel != "direct" || compact.Text != page.Text || compact.Summary != page.Text {
		t.Fatalf("tiny direct compact = %#v", compact)
	}
	if compact.OutputTextTruncated || compact.EstimatedTextTokens != compact.OriginalTextTokens || compact.EstimatedTokensSaved != 0 {
		t.Fatalf("tiny direct metrics = %#v", compact)
	}
	meta := webPageMetadata(compact)
	if meta["output_class"] != "tiny_direct_answer" || meta["summary_mode"] != "direct" ||
		anyInt64(meta["tool_summary_api_total_tokens"]) != 0 {
		t.Fatalf("tiny direct metadata = %#v", meta)
	}

	crawl := compactCrawlPages([]crawlPage{{
		URL:             "https://wttr.in/Moscow?format=3",
		Depth:           0,
		Text:            page.Text,
		RawBytesFetched: page.RawBytesFetched,
		MaxBytes:        page.MaxBytes,
	}}, webFetchOptions{})
	if len(crawl) != 1 || crawl[0].OutputClass != "tiny_direct_answer" || crawl[0].Text != page.Text {
		t.Fatalf("tiny crawl page = %#v", crawl)
	}
}

func TestCompactFetchedPageHonorsTokenBudgetEvenForFullText(t *testing.T) {
	text := strings.Repeat("Alpha beta gamma. ", 2000)
	page := fetchedPage{
		URL:         "https://example.com",
		Status:      200,
		ContentType: "text/plain",
		Title:       "Example",
		Text:        text,
	}
	out := compactFetchedPage(page, webFetchOptions{FullText: true, MaxTokens: 200})
	if out.BudgetTextTokens != 200 || out.BudgetTextChars != 800 {
		t.Fatalf("budget = %d tokens / %d chars, want 200 / 800", out.BudgetTextTokens, out.BudgetTextChars)
	}
	if !out.OutputTextTruncated || !strings.Contains(out.CompactNote, "full_text") {
		t.Fatalf("full_text should still be capped: %#v", out)
	}
	if out.ReturnedTextChars > 1000 || out.EstimatedTextTokens > 800 {
		t.Fatalf("returned too much text: chars=%d tokens=%d", out.ReturnedTextChars, out.EstimatedTextTokens)
	}
}

func TestCompactCrawlPagesHonorsTotalTokenBudget(t *testing.T) {
	pages := []crawlPage{
		{URL: "https://example.com/a", Depth: 0, Title: "A", Text: strings.Repeat("A page sentence. ", 800)},
		{URL: "https://example.com/b", Depth: 1, Title: "B", Text: strings.Repeat("B page sentence. ", 800)},
		{URL: "https://example.com/c", Depth: 1, Title: "C", Text: strings.Repeat("C page sentence. ", 800)},
	}
	out := compactCrawlPages(pages, webFetchOptions{IncludeText: true, MaxTokens: 2000, MaxTotalTokens: 900})
	if len(out) != len(pages) {
		t.Fatalf("pages = %d, want %d", len(out), len(pages))
	}
	totalBudgetChars := 0
	for _, page := range out {
		totalBudgetChars += page.BudgetTextChars
		if page.BudgetTextChars != 1200 {
			t.Fatalf("per-page budget = %d, want 1200: %#v", page.BudgetTextChars, page)
		}
		if !page.OutputTextTruncated || page.EstimatedTextTokens > 900 {
			t.Fatalf("page was not compacted enough: %#v", page)
		}
	}
	if totalBudgetChars != 3600 {
		t.Fatalf("total budget chars = %d, want 3600", totalBudgetChars)
	}
}

func TestWebCompactionStoresFullTextOutOfBand(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	text := strings.Repeat("Full page sentence with important evidence. ", 300) + "TAIL_UNIQUE_EVIDENCE_THAT_SHOULD_ONLY_BE_IN_REF"
	page := fetchedPage{
		URL:             "https://example.com/article",
		Status:          200,
		ContentType:     "text/html",
		Title:           "Important Article",
		Text:            text,
		Links:           []string{"https://example.com/source"},
		RawBytesFetched: 12345,
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	ref, err := storeWebOutput("web_fetch", page.URL, renderFetchedPageArtifact(page))
	if err != nil {
		t.Fatal(err)
	}
	compact.OutputRef = ref
	out, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "TAIL_UNIQUE_EVIDENCE_THAT_SHOULD_ONLY_BE_IN_REF") {
		t.Fatalf("compact output leaked tail raw text: %s", string(out))
	}
	if compact.Text != "" || compact.OutputRef == "" {
		t.Fatalf("compact page should omit raw text and include output ref: %#v", compact)
	}
	if compact.OutputClass != "extractive_summary" || compact.SummaryMode != "extractive" ||
		compact.SummaryChars <= 0 || compact.RawBytesFetched != 12345 || compact.MaxBytes != 65536 || compact.EstimatedTokensSaved <= 0 {
		t.Fatalf("compact output contract missing metrics: %#v", compact)
	}
	meta := webPageMetadata(compact)
	if meta["tool_summary_kind"] != "extractive" ||
		anyInt64(meta["tool_summary_api_total_tokens"]) != 0 ||
		meta["tool_summary_external_model_used"] != false {
		t.Fatalf("summary metadata missing extractive zero-cost fields: %#v", meta)
	}
	if meta["output_class"] != "extractive_summary" || meta["summary_mode"] != "extractive" ||
		meta["summary_chars"].(int) <= 0 ||
		meta["summarizer_provider"] != "native" || meta["summarizer_model"] != "extractive" ||
		meta["raw_bytes_fetched"] != 12345 || meta["max_bytes"] != 65536 ||
		meta["estimated_tokens_saved"].(int) <= 0 {
		t.Fatalf("metadata missing web output contract metrics: %#v", meta)
	}
	if anyInt64(meta["tool_summary_input_tokens"]) <= anyInt64(meta["tool_summary_output_tokens"]) ||
		anyInt64(meta["tool_summary_saved_tokens"]) <= 0 {
		t.Fatalf("summary token metadata should show compression: %#v", meta)
	}
	if meta[tooloutput.MetadataOutputRef] != ref ||
		meta[tooloutput.MetadataOutputRefID] == "" ||
		anyInt64(meta[tooloutput.MetadataOutputRefBytes]) <= 0 ||
		meta[tooloutput.MetadataOutputRefSHA256] == "" ||
		meta[tooloutput.MetadataOutputRefPermissions] != "0600" ||
		meta[tooloutput.MetadataOutputRefPlaintext] != true {
		t.Fatalf("output ref metadata missing shared fields: %#v", meta)
	}
	bytes, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), text[:200]) {
		t.Fatalf("artifact missing full extracted text")
	}
	assertMode(t, filepath.Join(config.BillyHomeDir(), "tool-output"), 0o700)
	assertMode(t, filepath.Dir(ref), 0o700)
	assertMode(t, ref, 0o600)
}

func TestWebOutputMetadataReportsMissingArtifact(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.txt")
	meta := webPageMetadata(compactPage{
		URL:                  "https://example.com/missing",
		OutputClass:          "extractive_summary",
		SummaryMode:          "extractive",
		SummaryChars:         10,
		SummarizerProvider:   "native",
		SummarizerModel:      "extractive",
		EstimatedTextTokens:  10,
		EstimatedTokensSaved: 5,
		OutputRef:            missing,
	})
	if meta[tooloutput.MetadataOutputRef] != missing {
		t.Fatalf("metadata should preserve missing output_ref path: %#v", meta)
	}
	if meta[tooloutput.MetadataOutputRefHashError] == "" {
		t.Fatalf("metadata should report missing output_ref stat error: %#v", meta)
	}
}

func TestWebToolGoldenJSONContracts(t *testing.T) {
	note := "full extracted text is saved out-of-band in output_ref; inline response contains only digest/extract to protect context cost"
	assertGoldenJSON(t, "web_fetch", compactPage{
		URL:                  "https://example.com/fetch",
		OutputClass:          "extractive_summary",
		SummaryMode:          "extractive",
		Summary:              "Fetch summary",
		SummaryChars:         13,
		SummarizerProvider:   "native",
		SummarizerModel:      "extractive",
		KeyPoints:            []string{"Point one"},
		Extract:              "Evidence line.",
		OutputRef:            "/tmp/tool-output/fetch.txt",
		OriginalTextTokens:   42,
		EstimatedTextTokens:  8,
		EstimatedTokensSaved: 34,
		OutputTextTruncated:  true,
		CompactNote:          note,
	}, `{
  "url": "https://example.com/fetch",
  "output_class": "extractive_summary",
  "summary_mode": "extractive",
  "summary": "Fetch summary",
  "summary_chars": 13,
  "summarizer_provider": "native",
  "summarizer_model": "extractive",
  "web_cache_hit": false,
  "key_points": [
    "Point one"
  ],
  "extract": "Evidence line.",
  "output_ref": "/tmp/tool-output/fetch.txt",
  "original_text_tokens": 42,
  "estimated_text_tokens": 8,
  "estimated_tokens_saved": 34,
  "output_text_truncated": true,
  "compact_note": "full extracted text is saved out-of-band in output_ref; inline response contains only digest/extract to protect context cost"
}`)
	assertGoldenJSON(t, "web_extract", compactPage{
		URL:                  "https://example.com/extract",
		OutputClass:          "raw_excerpt",
		SummaryMode:          "extractive",
		Summary:              "Extract summary",
		SummaryChars:         15,
		SummarizerProvider:   "native",
		SummarizerModel:      "extractive",
		Extract:              "Focused evidence.",
		Text:                 "Short exact excerpt.",
		OutputRef:            "/tmp/tool-output/extract.txt",
		ReturnedTextChars:    20,
		EstimatedTextTokens:  6,
		EstimatedTokensSaved: 30,
		BudgetTextChars:      800,
		BudgetTextTokens:     200,
		OutputTextTruncated:  true,
		CompactNote:          "inline text is capped; use output_ref if exact source text is required",
	}, `{
  "url": "https://example.com/extract",
  "output_class": "raw_excerpt",
  "summary_mode": "extractive",
  "summary": "Extract summary",
  "summary_chars": 15,
  "summarizer_provider": "native",
  "summarizer_model": "extractive",
  "web_cache_hit": false,
  "extract": "Focused evidence.",
  "text": "Short exact excerpt.",
  "output_ref": "/tmp/tool-output/extract.txt",
  "returned_text_chars": 20,
  "estimated_text_tokens": 6,
  "estimated_tokens_saved": 30,
  "budget_text_chars": 800,
  "budget_text_tokens": 200,
  "output_text_truncated": true,
  "compact_note": "inline text is capped; use output_ref if exact source text is required"
}`)
	assertGoldenJSON(t, "web_crawl", compactCrawlOutput{
		Pages: []compactCrawlPage{{
			URL:                  "https://example.com/",
			OutputClass:          "extractive_summary",
			SummaryMode:          "extractive",
			Summary:              "Crawl summary",
			SummaryChars:         13,
			SummarizerProvider:   "native",
			SummarizerModel:      "extractive",
			Extract:              "Crawl evidence.",
			OutputRef:            "/tmp/tool-output/crawl.txt",
			OriginalTextTokens:   20,
			EstimatedTextTokens:  5,
			EstimatedTokensSaved: 15,
			OutputTextTruncated:  true,
		}},
		OutputClass:          "extractive_summary",
		SummaryMode:          "extractive",
		SummaryChars:         13,
		SummarizerProvider:   "native",
		SummarizerModel:      "extractive",
		OutputRef:            "/tmp/tool-output/crawl.txt",
		OriginalTextTokens:   20,
		EstimatedTextTokens:  5,
		EstimatedTokensSaved: 15,
		OutputTextTruncated:  true,
		CompactNote:          note,
	}, `{
  "pages": [
    {
      "url": "https://example.com/",
      "depth": 0,
      "output_class": "extractive_summary",
      "summary_mode": "extractive",
      "summary": "Crawl summary",
      "summary_chars": 13,
      "summarizer_provider": "native",
      "summarizer_model": "extractive",
      "extract": "Crawl evidence.",
      "output_ref": "/tmp/tool-output/crawl.txt",
      "original_text_tokens": 20,
      "estimated_text_tokens": 5,
      "estimated_tokens_saved": 15,
      "output_text_truncated": true
    }
  ],
  "output_class": "extractive_summary",
  "summary_mode": "extractive",
  "summary_chars": 13,
  "summarizer_provider": "native",
  "summarizer_model": "extractive",
  "web_cache_hit": false,
  "output_ref": "/tmp/tool-output/crawl.txt",
  "original_text_tokens": 20,
  "estimated_text_tokens": 5,
  "estimated_tokens_saved": 15,
  "output_text_truncated": true,
  "compact_note": "full extracted text is saved out-of-band in output_ref; inline response contains only digest/extract to protect context cost"
}`)
}

func TestWebIncludeTextMarksRawExcerptOutputClass(t *testing.T) {
	page := fetchedPage{
		URL:             "https://example.com/article",
		Status:          200,
		ContentType:     "text/plain",
		Text:            strings.Repeat("short raw excerpt sentence. ", 200),
		RawBytesFetched: 5600,
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{IncludeText: true, MaxTokens: 300})
	if compact.OutputClass != "raw_excerpt" || compact.SummaryMode != "extractive" || compact.SummaryChars <= 0 {
		t.Fatalf("compact output contract = %#v", compact)
	}
	if compact.Text == "" || compact.ReturnedTextChars == 0 || compact.BudgetTextTokens != 300 {
		t.Fatalf("include_text should return capped text with metrics: %#v", compact)
	}
}

func TestWebPageMetadataIncludesPhaseTimings(t *testing.T) {
	page := compactPage{
		URL:              "https://example.com/timing",
		OutputClass:      "extractive_summary",
		SummaryMode:      "extractive",
		WebCacheLookupMS: 1,
		WebHTTPFetchMS:   2,
		WebCompactMS:     3,
		WebSummaryMS:     4,
		WebOutputRefMS:   5,
		WebCacheSaveMS:   6,
		WebTotalMS:       21,
	}
	meta := webPageMetadata(page)
	for key, want := range map[string]int64{
		"web_cache_lookup_ms": 1,
		"web_http_fetch_ms":   2,
		"web_compact_ms":      3,
		"web_summary_ms":      4,
		"web_output_ref_ms":   5,
		"web_cache_save_ms":   6,
		"web_total_ms":        21,
	} {
		if got := anyInt64(meta[key]); got != want {
			t.Fatalf("%s = %d, want %d in %#v", key, got, want, meta)
		}
	}

	page.resetWebPhaseTimings()
	for _, got := range []int64{
		page.WebCacheLookupMS,
		page.WebHTTPFetchMS,
		page.WebCompactMS,
		page.WebSummaryMS,
		page.WebOutputRefMS,
		page.WebCacheSaveMS,
		page.WebTotalMS,
	} {
		if got != 0 {
			t.Fatalf("reset should clear web phase timings: %#v", page)
		}
	}
}

func TestModelWebSummarizerRunsOutsideMainLoopAndRecordsMetrics(t *testing.T) {
	summarizer := scriptedSummarizer{
		check: func(req webtools.SummaryRequest) {
			if req.Provider != "mock" || req.Model != "mock-summarizer" || req.MaxInputTokens != 300 || req.MaxOutputTokens != 80 {
				t.Fatalf("summary request = %#v", req)
			}
			if !strings.Contains(req.Source.Text, "RAW_TAIL_ONLY_IN_REF") || req.Source.URL != "https://example.com/model" {
				t.Fatalf("summary source = %#v", req.Source)
			}
		},
		result: webtools.SummaryResult{
			Text:         "Model summary keeps the important facts and leaves the raw tail out.",
			Provider:     "mock",
			Model:        "mock-summarizer",
			InputTokens:  900,
			OutputTokens: 40,
			CacheHit:     300,
		},
	}

	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	cfg.WebSummaryMaxInputTokens = 300
	cfg.WebSummaryMaxOutputTokens = 80
	registry := NewRegistry(cfg, WithWebSummarizer(summarizer))
	raw := strings.Repeat("Raw page evidence should stay outside context. ", 200) + "RAW_TAIL_ONLY_IN_REF"
	page := fetchedPage{
		URL:             "https://example.com/model",
		Status:          200,
		ContentType:     "text/html",
		Title:           "Model Summary",
		Text:            raw,
		RawBytesFetched: len(raw),
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	out, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "RAW_TAIL_ONLY_IN_REF") {
		t.Fatalf("model summary compact output leaked raw tail: %s", string(out))
	}
	if compact.OutputClass != "model_summary" || compact.SummaryMode != "model" ||
		compact.SummarizerProvider != "mock" || compact.SummarizerModel != "mock-summarizer" ||
		compact.WebsumInputTokens != 900 || compact.WebsumOutputTokens != 40 || compact.WebsumCacheHit != 300 ||
		compact.WebsumModel != "mock-summarizer" || compact.WebsumError != "" {
		t.Fatalf("model summary compact = %#v", compact)
	}
	meta := webPageMetadata(compact)
	if meta["tool_summary_kind"] != "model" || meta["tool_summary_external_model_used"] != true ||
		anyInt64(meta["tool_summary_api_input_tokens"]) != 900 ||
		anyInt64(meta["tool_summary_api_output_tokens"]) != 40 ||
		anyInt64(meta["tool_summary_api_total_tokens"]) != 940 ||
		meta["websum_model"] != "mock-summarizer" {
		t.Fatalf("model summary metadata = %#v", meta)
	}
}

func TestModelWebSummarizerFallsBackToExtractiveOnProviderError(t *testing.T) {
	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	registry := NewRegistry(cfg, WithWebSummarizer(failingSummarizer{}))
	page := fetchedPage{
		URL:   "https://example.com/fallback",
		Title: "Fallback",
		Text:  strings.Repeat("Fallback page sentence with useful details. ", 100),
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	originalSummary := compact.Summary
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	if compact.Summary != originalSummary || compact.SummaryMode != "extractive" || compact.OutputClass != "extractive_summary" {
		t.Fatalf("fallback should keep extractive compact output: %#v", compact)
	}
	if !strings.Contains(compact.WebsumError, "summary failed") {
		t.Fatalf("fallback missing error: %#v", compact)
	}
}

func TestModelWebSummaryServiceMetadataTimeoutAndConcurrency(t *testing.T) {
	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	cfg.WebSummaryTimeout = 500 * time.Millisecond
	summarizer := &blockingSummarizer{
		entered: make(chan webtools.SummaryRequest),
		release: make(chan struct{}),
		result:  webtools.SummaryResult{Text: "bounded summary", Provider: "mock", Model: "mock-summarizer", InputTokens: 10, OutputTokens: 2},
	}
	registry := NewRegistry(cfg, WithWebSummarizer(summarizer))
	registry.webSummarySlots = make(chan struct{}, 1)
	source := webtools.SummarySource{URL: "https://example.com/summary", Title: "Summary", Text: strings.Repeat("evidence ", 100)}

	firstDone := make(chan error, 1)
	go func() {
		_, err := registry.modelWebSummary(context.Background(), source)
		firstDone <- err
	}()
	req := <-summarizer.entered
	if req.RequestID == "" || req.ToolName != "web_summary" || req.AllowTools {
		t.Fatalf("summary request metadata/no-tools contract = %#v", req)
	}
	if req.Timeout != 500*time.Millisecond || req.Provider != "mock" || req.Model != "mock-summarizer" {
		t.Fatalf("summary request settings = %#v", req)
	}

	secondDone := make(chan error, 1)
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelSecond()
	go func() {
		_, err := registry.modelWebSummary(secondCtx, source)
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second summary err = %v, want deadline exceeded", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second summary did not time out while waiting for concurrency slot")
	}

	close(summarizer.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first summary err = %v", err)
	}
}

func TestWebSummaryExtractiveModeDoesNotNeedSummarizer(t *testing.T) {
	cfg := config.Default()
	cfg.WebSummaryMode = "extractive"
	registry := NewRegistry(cfg, WithWebSummarizer(fatalSummarizer{t: t}))
	page := fetchedPage{
		URL:   "https://example.com/extractive",
		Title: "Extractive",
		Text:  strings.Repeat("Extractive page sentence with useful details. ", 100),
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	originalSummary := compact.Summary
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	if compact.Summary != originalSummary || compact.SummaryMode != "extractive" || compact.WebsumError != "" {
		t.Fatalf("extractive summary should not need summarizer: %#v", compact)
	}

	cfg.WebSummaryMode = "model"
	registry = NewRegistry(cfg)
	compact = compactFetchedPage(page, webFetchOptions{})
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	if compact.SummaryMode != "extractive" || !strings.Contains(compact.WebsumError, "web model summarizer unavailable") {
		t.Fatalf("model mode without summarizer should fall back: %#v", compact)
	}
}

func TestWebCacheStoresAndClearsCompactOutputs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	cfg := config.Default()
	cfg.WebCacheEnabled = true
	cfg.WebCacheTTL = time.Hour
	cfg.WebCacheMaxBytes = 1 << 20
	registry := NewRegistry(cfg)
	ctx := context.Background()
	opts := webFetchOptions{Query: "weather", MaxBytes: 65536}
	key, ok := registry.webCacheKey(ctx, "web_fetch", "http://93.184.216.34/article", opts, nil)
	if !ok || key == "" {
		t.Fatalf("web cache key missing")
	}
	otherQuery, _ := registry.webCacheKey(ctx, "web_fetch", "http://93.184.216.34/article", webFetchOptions{Query: "news", MaxBytes: 65536}, nil)
	if otherQuery == key {
		t.Fatalf("web cache key should include query")
	}
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	modelRegistry := NewRegistry(cfg)
	modelKey, _ := modelRegistry.webCacheKey(ctx, "web_fetch", "http://93.184.216.34/article", opts, nil)
	if modelKey == key {
		t.Fatalf("web cache key should include summarizer config")
	}

	page := compactPage{
		URL:                 "http://93.184.216.34/article",
		OutputClass:         "extractive_summary",
		SummaryMode:         "extractive",
		Summary:             "cached summary",
		EstimatedTextTokens: 17,
	}
	pageRef, err := storeWebOutput("web_fetch", page.URL, "cached full page")
	if err != nil {
		t.Fatal(err)
	}
	page.OutputRef = pageRef
	if err := registry.saveWebPageCache(key, page); err != nil {
		t.Fatal(err)
	}
	cached, hit := registry.loadWebPageCache(key)
	if !hit || !cached.WebCacheHit || cached.WebCacheKey != key || cached.WebCacheTTLMS != int64(time.Hour/time.Millisecond) {
		t.Fatalf("cached page = %#v hit=%v", cached, hit)
	}
	if err := os.Remove(pageRef); err != nil {
		t.Fatal(err)
	}
	if _, hit := registry.loadWebPageCache(key); hit {
		t.Fatal("page cache should miss after output ref is deleted")
	}

	crawlKey, ok := registry.webCacheKey(ctx, "web_crawl", "http://93.184.216.34/", webFetchOptions{}, map[string]any{"max_pages": 1})
	if !ok {
		t.Fatalf("crawl cache key missing")
	}
	crawlRef, err := storeWebOutput("web_crawl", "http://93.184.216.34/", "cached full crawl")
	if err != nil {
		t.Fatal(err)
	}
	crawl := compactCrawlOutput{
		Pages:       []compactCrawlPage{{URL: "http://93.184.216.34/", Summary: "cached crawl"}},
		OutputClass: "extractive_summary",
		SummaryMode: "extractive",
		OutputRef:   crawlRef,
	}
	if err := registry.saveWebCrawlCache(crawlKey, crawl); err != nil {
		t.Fatal(err)
	}
	cachedCrawl, hit := registry.loadWebCrawlCache(crawlKey)
	if !hit || !cachedCrawl.WebCacheHit || len(cachedCrawl.Pages) != 1 || !cachedCrawl.Pages[0].WebCacheHit {
		t.Fatalf("cached crawl = %#v hit=%v", cachedCrawl, hit)
	}
	if err := os.Remove(crawlRef); err != nil {
		t.Fatal(err)
	}
	if _, hit := registry.loadWebCrawlCache(crawlKey); hit {
		t.Fatal("crawl cache should miss after output ref is deleted")
	}

	status, err := registry.Call(ctx, protocol.ToolCall{Name: "web_cache_status", Arguments: rawArgs(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"enabled": true`, `"entries":`} {
		if !strings.Contains(status.Content, want) {
			t.Fatalf("status missing %q in:\n%s", want, status.Content)
		}
	}
	cleared, err := registry.Call(ctx, protocol.ToolCall{Name: "web_cache_clear", Arguments: rawArgs(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cleared.Content, `"removed_entries":`) || anyInt64(cleared.Metadata["web_cache_removed_entries"]) == 0 {
		t.Fatalf("clear result = %#v content=\n%s", cleared, cleared.Content)
	}
	after := registry.webCacheStatus()
	if after.Entries != 0 {
		t.Fatalf("cache should be empty after clear: %#v", after)
	}
}

func TestCompactCrawlResultReturnsSingleOutputRef(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	pages := []crawlPage{
		{URL: "https://example.com/a", Depth: 0, Title: "A", Text: strings.Repeat("A page sentence. ", 400), RawBytesFetched: 7000, MaxBytes: 65536},
		{URL: "https://example.com/b", Depth: 1, Title: "B", Text: strings.Repeat("B page sentence. ", 400), RawBytesFetched: 8000, MaxBytes: 65536},
	}
	registry := NewRegistry(config.Default())
	out, ref, err := registry.compactCrawlResult(context.Background(), pages, webFetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ref == "" || out.OutputRef != ref || len(out.Pages) != 2 {
		t.Fatalf("crawl compact output/ref = %#v ref=%q", out, ref)
	}
	if out.OutputClass != "extractive_summary" || out.SummaryMode != "extractive" ||
		out.SummaryChars <= 0 || out.RawBytesFetched != 15000 || out.MaxBytesPerPage != 65536 || out.EstimatedTokensSaved <= 0 {
		t.Fatalf("crawl output contract missing metrics: %#v", out)
	}
	meta := crawlMetadata(out)
	if meta["output_class"] != "extractive_summary" || meta["summary_mode"] != "extractive" ||
		meta["summary_chars"].(int) <= 0 ||
		meta["summarizer_provider"] != "native" || meta["summarizer_model"] != "extractive" ||
		meta["raw_bytes_fetched"] != 15000 || meta["max_bytes_per_page"] != 65536 ||
		meta["estimated_tokens_saved"].(int) <= 0 {
		t.Fatalf("crawl metadata missing web output contract metrics: %#v", meta)
	}
	for _, page := range out.Pages {
		if page.Text != "" || page.OutputRef != ref || page.Extract == "" {
			t.Fatalf("page should be compact with shared ref: %#v", page)
		}
	}
	bytes, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), "=== page 2") || !strings.Contains(string(bytes), "B page sentence") {
		t.Fatalf("crawl artifact missing page text:\n%s", string(bytes))
	}
}
