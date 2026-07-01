package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/tooloutput"
)

func crawlMetadata(out compactCrawlOutput) map[string]any {
	savedTokens := maxInt(0, int(out.WebsumInputTokens-out.WebsumOutputTokens))
	apiInput, apiOutput, apiTotal := websumAPITokens(out.SummaryMode, out.WebsumInputTokens, out.WebsumOutputTokens)
	apiCacheHit, apiCacheMiss := websumAPICacheTokens(out.SummaryMode, out.WebsumCacheHit, out.WebsumCacheMiss)
	metadata := map[string]any{
		"output_class":                       out.OutputClass,
		"summary_mode":                       out.SummaryMode,
		"summary_chars":                      out.SummaryChars,
		"summarizer_provider":                out.SummarizerProvider,
		"summarizer_model":                   out.SummarizerModel,
		"websum_input_tokens":                out.WebsumInputTokens,
		"websum_output_tokens":               out.WebsumOutputTokens,
		"websum_cost":                        out.WebsumCost,
		"websum_cache_hit":                   out.WebsumCacheHit,
		"websum_cache_miss":                  out.WebsumCacheMiss,
		"websum_model":                       out.WebsumModel,
		"websum_error":                       out.WebsumError,
		"web_cache_hit":                      out.WebCacheHit,
		"web_cache_miss":                     out.WebCacheKey != "" && !out.WebCacheHit,
		"web_cache_key":                      out.WebCacheKey,
		"web_cache_age_ms":                   out.WebCacheAgeMS,
		"web_cache_ttl_ms":                   out.WebCacheTTLMS,
		"pages":                              len(out.Pages),
		"raw_bytes_fetched":                  out.RawBytesFetched,
		"max_bytes_per_page":                 out.MaxBytesPerPage,
		"original_text_chars":                out.OriginalTextChars,
		"original_text_tokens":               out.OriginalTextTokens,
		"returned_text_chars":                out.ReturnedTextChars,
		"estimated_text_tokens":              out.EstimatedTextTokens,
		"estimated_tokens_saved":             out.EstimatedTokensSaved,
		"output_text_truncated":              out.OutputTextTruncated,
		"output_ref":                         out.OutputRef,
		"tool_summary_kind":                  out.SummaryMode,
		"tool_summary_input_tokens":          out.WebsumInputTokens,
		"tool_summary_output_tokens":         out.WebsumOutputTokens,
		"tool_summary_saved_tokens":          savedTokens,
		"tool_summary_api_input_tokens":      apiInput,
		"tool_summary_api_output_tokens":     apiOutput,
		"tool_summary_api_total_tokens":      apiTotal,
		"tool_summary_api_cache_hit_tokens":  apiCacheHit,
		"tool_summary_api_cache_miss_tokens": apiCacheMiss,
		"tool_summary_estimated_cost_usd":    out.WebsumCost,
		"tool_summary_external_model_used":   out.SummaryMode == "model",
	}
	addOutputRefMetadata(metadata, out.OutputRef)
	return metadata
}

func (page *compactPage) resetWebPhaseTimings() {
	if page == nil {
		return
	}
	page.WebCacheLookupMS = 0
	page.WebHTTPFetchMS = 0
	page.WebCompactMS = 0
	page.WebSummaryMS = 0
	page.WebOutputRefMS = 0
	page.WebCacheSaveMS = 0
	page.WebTotalMS = 0
}

func elapsedMillis(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	return maxInt64(0, time.Since(start).Milliseconds())
}

func firstCrawlURL(pages []crawlPage) string {
	for _, page := range pages {
		if page.URL != "" {
			return page.URL
		}
	}
	return "crawl"
}

func renderFetchedPageArtifact(page fetchedPage) string {
	var b strings.Builder
	b.WriteString("url: " + page.URL + "\n")
	if page.Title != "" {
		b.WriteString("title: " + page.Title + "\n")
	}
	if page.ContentType != "" {
		b.WriteString("content_type: " + page.ContentType + "\n")
	}
	if page.Status != 0 {
		b.WriteString(fmt.Sprintf("status: %d\n", page.Status))
	}
	if page.Truncated {
		b.WriteString("response_body_truncated: true\n")
	}
	b.WriteString("\n--- extracted text ---\n")
	b.WriteString(strings.TrimSpace(page.Text))
	b.WriteString("\n")
	if len(page.Links) > 0 {
		b.WriteString("\n--- links ---\n")
		for _, link := range page.Links {
			b.WriteString(link + "\n")
		}
	}
	return b.String()
}

func renderCrawlArtifact(pages []crawlPage) string {
	var b strings.Builder
	for i, page := range pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(fmt.Sprintf("=== page %d depth=%d ===\n", i+1, page.Depth))
		b.WriteString("url: " + page.URL + "\n")
		if page.Title != "" {
			b.WriteString("title: " + page.Title + "\n")
		}
		if page.Error != "" {
			b.WriteString("error: " + page.Error + "\n")
			continue
		}
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(page.Text))
		b.WriteString("\n")
	}
	return b.String()
}

func storeWebOutput(toolName, source, content string) (string, error) {
	ref, err := tooloutput.Store(tooloutput.StoreRequest{
		Parts:                 []string{toolName, source},
		Content:               content,
		TrimSpace:             true,
		EnsureTrailingNewline: true,
	})
	return ref.Path, err
}

func webPageMetadata(page compactPage) map[string]any {
	savedTokens := maxInt(0, int(page.WebsumInputTokens-page.WebsumOutputTokens))
	apiInput, apiOutput, apiTotal := websumAPITokens(page.SummaryMode, page.WebsumInputTokens, page.WebsumOutputTokens)
	apiCacheHit, apiCacheMiss := websumAPICacheTokens(page.SummaryMode, page.WebsumCacheHit, page.WebsumCacheMiss)
	metadata := map[string]any{
		"output_class":                       page.OutputClass,
		"summary_mode":                       page.SummaryMode,
		"summary_chars":                      page.SummaryChars,
		"summarizer_provider":                page.SummarizerProvider,
		"summarizer_model":                   page.SummarizerModel,
		"websum_input_tokens":                page.WebsumInputTokens,
		"websum_output_tokens":               page.WebsumOutputTokens,
		"websum_cost":                        page.WebsumCost,
		"websum_cache_hit":                   page.WebsumCacheHit,
		"websum_cache_miss":                  page.WebsumCacheMiss,
		"websum_model":                       page.WebsumModel,
		"websum_error":                       page.WebsumError,
		"web_cache_hit":                      page.WebCacheHit,
		"web_cache_miss":                     page.WebCacheKey != "" && !page.WebCacheHit,
		"web_cache_key":                      page.WebCacheKey,
		"web_cache_age_ms":                   page.WebCacheAgeMS,
		"web_cache_ttl_ms":                   page.WebCacheTTLMS,
		"web_cache_lookup_ms":                page.WebCacheLookupMS,
		"web_http_fetch_ms":                  page.WebHTTPFetchMS,
		"web_compact_ms":                     page.WebCompactMS,
		"web_summary_ms":                     page.WebSummaryMS,
		"web_output_ref_ms":                  page.WebOutputRefMS,
		"web_cache_save_ms":                  page.WebCacheSaveMS,
		"web_total_ms":                       page.WebTotalMS,
		"raw_bytes_fetched":                  page.RawBytesFetched,
		"max_bytes":                          page.MaxBytes,
		"original_text_chars":                page.OriginalTextChars,
		"original_text_tokens":               page.OriginalTextTokens,
		"returned_text_chars":                page.ReturnedTextChars,
		"estimated_text_tokens":              page.EstimatedTextTokens,
		"estimated_tokens_saved":             page.EstimatedTokensSaved,
		"budget_text_chars":                  page.BudgetTextChars,
		"budget_text_tokens":                 page.BudgetTextTokens,
		"output_text_truncated":              page.OutputTextTruncated,
		"response_body_truncated":            page.Truncated,
		"output_ref":                         page.OutputRef,
		"tool_summary_kind":                  page.SummaryMode,
		"tool_summary_input_tokens":          page.WebsumInputTokens,
		"tool_summary_output_tokens":         page.WebsumOutputTokens,
		"tool_summary_saved_tokens":          savedTokens,
		"tool_summary_api_input_tokens":      apiInput,
		"tool_summary_api_output_tokens":     apiOutput,
		"tool_summary_api_total_tokens":      apiTotal,
		"tool_summary_api_cache_hit_tokens":  apiCacheHit,
		"tool_summary_api_cache_miss_tokens": apiCacheMiss,
		"tool_summary_estimated_cost_usd":    page.WebsumCost,
		"tool_summary_external_model_used":   page.SummaryMode == "model",
	}
	addOutputRefMetadata(metadata, page.OutputRef)
	return metadata
}

func addOutputRefMetadata(metadata map[string]any, ref string) {
	_ = tooloutput.AddMetadataForPath(metadata, ref)
}

func websumAPITokens(mode string, inputTokens, outputTokens int64) (int64, int64, int64) {
	if mode != "model" {
		return 0, 0, 0
	}
	return inputTokens, outputTokens, inputTokens + outputTokens
}

func websumAPICacheTokens(mode string, cacheHit, cacheMiss int64) (int64, int64) {
	if mode != "model" {
		return 0, 0
	}
	return cacheHit, cacheMiss
}
