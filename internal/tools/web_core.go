package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func (r *Registry) fetchCompactPageResult(ctx context.Context, toolName, rawURL string, opts webFetchOptions) (Result, error) {
	totalStart := time.Now()
	opts.MaxBytes = boundedBytes(opts.MaxBytes)
	cacheLookupStart := time.Now()
	cacheKey, cacheOK := r.webCacheKey(ctx, toolName, rawURL, opts, nil)
	cacheLookupMS := elapsedMillis(cacheLookupStart)
	if cacheOK {
		if compact, hit := r.loadWebPageCache(cacheKey); hit {
			compact.resetWebPhaseTimings()
			compact.WebCacheLookupMS = cacheLookupMS
			compact.WebTotalMS = elapsedMillis(totalStart)
			out, _ := json.MarshalIndent(compact, "", "  ")
			return Result{
				Content:   string(out),
				Metadata:  webPageMetadata(compact),
				Truncated: compact.OutputTextTruncated,
				OutputRef: compact.OutputRef,
			}, nil
		}
	}
	fetchStart := time.Now()
	page, err := fetchPage(ctx, rawURL, opts.MaxBytes)
	if err != nil {
		return Result{}, err
	}
	fetchMS := elapsedMillis(fetchStart)
	compactStart := time.Now()
	compact := compactFetchedPage(page, opts)
	compact.WebCacheLookupMS = cacheLookupMS
	compact.WebHTTPFetchMS = fetchMS
	compact.WebCompactMS = elapsedMillis(compactStart)
	summaryStart := time.Now()
	r.applyModelSummaryToPage(ctx, &compact, page, opts)
	compact.WebSummaryMS = elapsedMillis(summaryStart)
	outputRefStart := time.Now()
	ref, err := storeWebOutput(toolName, page.URL, renderFetchedPageArtifact(page))
	compact.WebOutputRefMS = elapsedMillis(outputRefStart)
	if err != nil {
		compact.CompactNote = strings.TrimSpace(compact.CompactNote + " full extracted text save failed: " + err.Error())
	} else {
		compact.OutputRef = ref
	}
	if cacheOK {
		cacheSaveStart := time.Now()
		compact.applyWebCache(cacheKey, false, 0, r.toolPolicy.WebCacheTTL)
		_ = r.saveWebPageCache(cacheKey, compact)
		compact.WebCacheSaveMS = elapsedMillis(cacheSaveStart)
	}
	compact.WebTotalMS = elapsedMillis(totalStart)
	out, _ := json.MarshalIndent(compact, "", "  ")
	return Result{
		Content:   string(out),
		Metadata:  webPageMetadata(compact),
		Truncated: compact.OutputTextTruncated,
		OutputRef: compact.OutputRef,
	}, nil
}

func (r *Registry) applyModelSummaryToPage(ctx context.Context, compact *compactPage, page fetchedPage, opts webFetchOptions) {
	if compact == nil || r == nil || config.NormalizeWebSummaryMode(r.toolPolicy.WebSummaryMode) != "model" {
		return
	}
	if compact.OutputClass == "tiny_direct_answer" {
		return
	}
	summary, err := r.modelWebSummary(ctx, webtools.SummarySource{
		URL:   page.URL,
		Title: page.Title,
		Query: opts.Query,
		Text:  page.Text,
	})
	if err != nil {
		compact.WebsumError = truncateRunes(oneLine(err.Error()), 240)
		return
	}
	applyWebModelSummaryToPage(compact, summary)
}

func (r *Registry) applyModelSummaryToCrawlPage(ctx context.Context, compact *compactCrawlPage, page crawlPage, opts webFetchOptions) {
	if compact == nil || r == nil || config.NormalizeWebSummaryMode(r.toolPolicy.WebSummaryMode) != "model" {
		return
	}
	if compact.OutputClass == "tiny_direct_answer" {
		return
	}
	summary, err := r.modelWebSummary(ctx, webtools.SummarySource{
		URL:   page.URL,
		Title: page.Title,
		Query: opts.Query,
		Text:  page.Text,
	})
	if err != nil {
		compact.WebsumError = truncateRunes(oneLine(err.Error()), 240)
		return
	}
	applyWebModelSummaryToCrawlPage(compact, summary)
}

func applyWebModelSummaryToPage(page *compactPage, summary webModelSummary) {
	page.Summary = summary.Text
	page.SummaryMode = "model"
	page.SummarizerProvider = summary.Provider
	page.SummarizerModel = summary.Model
	page.WebsumInputTokens = summary.InputTokens
	page.WebsumOutputTokens = summary.OutputTokens
	page.WebsumCacheHit = summary.CacheHit
	page.WebsumCacheMiss = summary.CacheMiss
	page.WebsumCost = summary.CostUSD
	page.WebsumModel = summary.Model
	page.WebsumError = ""
	if strings.TrimSpace(page.Text) == "" {
		page.OutputClass = "model_summary"
	}
	page.SummaryChars = webSummaryChars(page.Summary, page.KeyPoints, page.Extract)
	page.EstimatedTextTokens = estimateTokens(compactPageInlineText(*page))
	page.EstimatedTokensSaved = maxInt(0, page.OriginalTextTokens-page.EstimatedTextTokens)
}

func applyWebModelSummaryToCrawlPage(page *compactCrawlPage, summary webModelSummary) {
	page.Summary = summary.Text
	page.SummaryMode = "model"
	page.SummarizerProvider = summary.Provider
	page.SummarizerModel = summary.Model
	page.WebsumInputTokens = summary.InputTokens
	page.WebsumOutputTokens = summary.OutputTokens
	page.WebsumCacheHit = summary.CacheHit
	page.WebsumCacheMiss = summary.CacheMiss
	page.WebsumCost = summary.CostUSD
	page.WebsumModel = summary.Model
	page.WebsumError = ""
	if strings.TrimSpace(page.Text) == "" {
		page.OutputClass = "model_summary"
	}
	page.SummaryChars = webSummaryChars(page.Summary, page.KeyPoints, page.Extract)
	page.EstimatedTextTokens = estimateTokens(compactCrawlPageInlineText(*page))
	page.EstimatedTokensSaved = maxInt(0, page.OriginalTextTokens-page.EstimatedTextTokens)
}

func (r *Registry) compactCrawlResult(ctx context.Context, pages []crawlPage, opts webFetchOptions) (compactCrawlOutput, string, error) {
	compactPages := compactCrawlPages(pages, opts)
	for i := range compactPages {
		if i < len(pages) && pages[i].Error == "" {
			r.applyModelSummaryToCrawlPage(ctx, &compactPages[i], pages[i], opts)
		}
	}
	artifact := renderCrawlArtifact(pages)
	ref, err := storeWebOutput("web_crawl", firstCrawlURL(pages), artifact)
	var out compactCrawlOutput
	out.Pages = compactPages
	out.OutputClass = webOutputClass(opts.IncludeText || opts.FullText)
	out.SummaryMode = "extractive"
	out.OutputRef = ref
	if err == nil && ref != "" {
		for i := range out.Pages {
			if out.Pages[i].Error == "" {
				out.Pages[i].OutputRef = ref
			}
		}
	}
	allTinyDirect := len(out.Pages) > 0 && !opts.IncludeText && !opts.FullText
	for _, page := range out.Pages {
		if page.Error != "" || page.OutputClass != "tiny_direct_answer" {
			allTinyDirect = false
		}
		out.RawBytesFetched += page.RawBytesFetched
		out.SummaryChars += page.SummaryChars
		out.WebsumInputTokens += page.WebsumInputTokens
		out.WebsumOutputTokens += page.WebsumOutputTokens
		out.WebsumCacheHit += page.WebsumCacheHit
		out.WebsumCacheMiss += page.WebsumCacheMiss
		out.WebsumCost += page.WebsumCost
		if out.SummarizerProvider == "" && page.SummarizerProvider != "" {
			out.SummarizerProvider = page.SummarizerProvider
		}
		if out.SummarizerModel == "" && page.SummarizerModel != "" {
			out.SummarizerModel = page.SummarizerModel
			out.WebsumModel = page.WebsumModel
		}
		if out.WebsumError == "" && page.WebsumError != "" {
			out.WebsumError = page.WebsumError
		}
		if page.SummaryMode == "model" {
			out.SummaryMode = "model"
			if !opts.IncludeText && !opts.FullText {
				out.OutputClass = "model_summary"
			}
		}
		if page.MaxBytes > out.MaxBytesPerPage {
			out.MaxBytesPerPage = page.MaxBytes
		}
		out.OriginalTextChars += page.OriginalTextChars
		out.OriginalTextTokens += page.OriginalTextTokens
		out.ReturnedTextChars += page.ReturnedTextChars
		out.EstimatedTextTokens += page.EstimatedTextTokens
		out.EstimatedTokensSaved += page.EstimatedTokensSaved
		out.OutputTextTruncated = out.OutputTextTruncated || page.OutputTextTruncated
	}
	if allTinyDirect {
		out.OutputClass = "tiny_direct_answer"
		out.SummaryMode = "direct"
		out.SummarizerProvider = "native"
		out.SummarizerModel = "direct"
		out.WebsumModel = "direct"
	}
	if out.OutputTextTruncated {
		out.CompactNote = "full extracted crawl text is saved out-of-band in output_ref; inline response contains only digest/extract to protect context cost"
	}
	return out, ref, err
}
