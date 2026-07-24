package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

type webBackendKey struct {
	Value  string
	EnvVar string
	Source string
}

type webBackendMissingKeyError struct {
	Backend string
	EnvVar  string
}

func (e webBackendMissingKeyError) Error() string {
	return fmt.Sprintf("%s backend missing API key env %s", e.Backend, e.EnvVar)
}

func (r *Registry) webSearchBackend() string {
	if r == nil {
		return webtools.BackendNative
	}
	return normalizeToolWebBackend(r.toolPolicy.WebSearchBackend)
}

func (r *Registry) webSearchBackendForContext(ctx context.Context) string {
	if r == nil {
		return webtools.BackendNative
	}
	return normalizeToolWebBackend(r.toolPolicyForContext(ctx).WebSearchBackend)
}

func (r *Registry) webExtractBackend() string {
	if r == nil {
		return webtools.BackendNative
	}
	return normalizeToolWebBackend(r.toolPolicy.WebExtractBackend)
}

func (r *Registry) webExtractBackendForContext(ctx context.Context) string {
	if r == nil {
		return webtools.BackendNative
	}
	return normalizeToolWebBackend(r.toolPolicyForContext(ctx).WebExtractBackend)
}

func normalizeToolWebBackend(value string) string {
	switch config.NormalizeWebBackend(value) {
	case webtools.BackendExa:
		return webtools.BackendExa
	case webtools.BackendTavily:
		return webtools.BackendTavily
	default:
		return webtools.BackendNative
	}
}

func (r *Registry) resolveWebBackendKey(backend string) (webBackendKey, error) {
	backend = normalizeToolWebBackend(backend)
	envName := ""
	switch backend {
	case webtools.BackendTavily:
		envName = strings.TrimSpace(r.toolPolicy.WebTavilyAPIKeyEnv)
		if envName == "" {
			envName = "TAVILY_API_KEY"
		}
	case webtools.BackendExa:
		envName = strings.TrimSpace(r.toolPolicy.WebExaAPIKeyEnv)
		if envName == "" {
			envName = "EXA_API_KEY"
		}
	default:
		return webBackendKey{}, nil
	}
	value, source, ok := config.LookupEnvDotenvOrFiles(envName, r.toolPolicy.WebHermesEnvFiles)
	if !ok {
		return webBackendKey{}, webBackendMissingKeyError{Backend: backend, EnvVar: envName}
	}
	return webBackendKey{Value: value, EnvVar: envName, Source: source}, nil
}

func (r *Registry) webBackendSearch(ctx context.Context, backend string, req webtools.SearchRequest) (webtools.SearchResponse, webBackendKey, error) {
	key, err := r.resolveWebBackendKey(backend)
	if err != nil {
		return webtools.SearchResponse{}, webBackendKey{}, err
	}
	switch normalizeToolWebBackend(backend) {
	case webtools.BackendTavily:
		client := webtools.NewTavilyClient(webtools.BackendClientOptions{
			BaseURL:    r.tavilyBaseURL,
			APIKey:     key.Value,
			HTTPClient: r.webBackendHTTP,
			Sleep:      r.webBackendSleep,
		})
		resp, err := client.Search(ctx, req)
		return resp, key, err
	case webtools.BackendExa:
		client := webtools.NewExaClient(webtools.BackendClientOptions{
			BaseURL:    r.exaBaseURL,
			APIKey:     key.Value,
			HTTPClient: r.webBackendHTTP,
			Sleep:      r.webBackendSleep,
		})
		resp, err := client.Search(ctx, req)
		return resp, key, err
	default:
		return webtools.SearchResponse{}, key, fmt.Errorf("unsupported web search backend %q", backend)
	}
}

type nativeSearchResponse struct {
	Results             []searchResult
	ResultsBeforeFilter int
	ResultsAfterFilter  int
}

type webSearchMetadataStats struct {
	ResultCount         int
	ResultsBeforeFilter int
	ResultsAfterFilter  int
}

func (r *Registry) nativeSearch(ctx context.Context, req webtools.SearchRequest) (nativeSearchResponse, error) {
	query := strings.TrimSpace(req.Query)
	limit := normalizedNativeSearchLimit(req.Limit)
	parseLimit := limit
	if len(req.IncludeDomains) > 0 || len(req.ExcludeDomains) > 0 {
		parseLimit = 50
	}
	searchURL := duckDuckGoLiteSearchURL(query)
	body, _, _, err := httpGetWithClient(ctx, r.nativeWebHTTPClientForContext(ctx), searchURL, maxWebBytes)
	if err != nil {
		return nativeSearchResponse{}, err
	}
	results := parseSearchResults(searchURL, string(body), parseLimit)
	filtered := filterSearchResults(results, req, limit)
	return nativeSearchResponse{
		Results:             filtered,
		ResultsBeforeFilter: len(results),
		ResultsAfterFilter:  len(filtered),
	}, nil
}

func normalizedNativeSearchLimit(value int) int {
	if value <= 0 {
		return 5
	}
	if value > 10 {
		return 10
	}
	return value
}

func filterSearchResults(results []searchResult, req webtools.SearchRequest, limit int) []searchResult {
	include := normalizedSearchDomains(req.IncludeDomains)
	exclude := normalizedSearchDomains(req.ExcludeDomains)
	out := make([]searchResult, 0, minInt(limit, len(results)))
	for _, result := range results {
		if !searchResultAllowedByDomains(result.URL, include, exclude) {
			continue
		}
		out = append(out, result)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func normalizedSearchDomains(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "https://")
		value = strings.TrimPrefix(value, "http://")
		if slash := strings.IndexByte(value, '/'); slash >= 0 {
			value = value[:slash]
		}
		value = strings.Trim(value, ". ")
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func searchResultAllowedByDomains(rawURL string, include, exclude map[string]bool) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.Trim(u.Hostname(), ". "))
	if host == "" {
		return false
	}
	for domain := range exclude {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for domain := range include {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func webSearchMetadata(backend string, req webtools.SearchRequest, key webBackendKey, stats webSearchMetadataStats) map[string]any {
	if stats.ResultCount == 0 {
		stats.ResultCount = stats.ResultsAfterFilter
	}
	freshnessRequested := req.FreshnessDays > 0
	domainFilterRequested := len(req.IncludeDomains) > 0 || len(req.ExcludeDomains) > 0
	freshnessSupported := backend == webtools.BackendTavily || backend == webtools.BackendExa
	domainFilterSupported := backend == webtools.BackendTavily || backend == webtools.BackendExa || backend == webtools.BackendNative
	var skippedFilters []string
	var postFilteredFilters []string
	domainEnforcement := "none"
	if domainFilterRequested {
		switch backend {
		case webtools.BackendNative:
			domainEnforcement = "native_post_filter"
			postFilteredFilters = append(postFilteredFilters, "domains")
		case webtools.BackendTavily, webtools.BackendExa:
			domainEnforcement = "provider"
		default:
			domainEnforcement = "unsupported"
			skippedFilters = append(skippedFilters, "domains")
		}
	}
	if freshnessRequested && !freshnessSupported {
		skippedFilters = append(skippedFilters, "freshness_days")
	}
	metadata := map[string]any{
		"web_backend":                   backend,
		"web_query":                     strings.TrimSpace(req.Query),
		"web_result_count":              stats.ResultCount,
		"web_results_after_filter":      stats.ResultsAfterFilter,
		"web_freshness_requested":       freshnessRequested,
		"web_freshness_supported":       freshnessSupported,
		"web_freshness_enforced":        freshnessRequested && freshnessSupported,
		"web_domain_filter_requested":   domainFilterRequested,
		"web_domain_filter_supported":   domainFilterSupported,
		"web_domain_filter_enforcement": domainEnforcement,
	}
	if stats.ResultsBeforeFilter > 0 {
		metadata["web_results_before_filter"] = stats.ResultsBeforeFilter
	}
	if len(skippedFilters) > 0 {
		metadata["web_skipped_filters"] = skippedFilters
	}
	if len(postFilteredFilters) > 0 {
		metadata["web_post_filtered_filters"] = postFilteredFilters
	}
	if key.EnvVar != "" {
		metadata["web_backend_key_env"] = key.EnvVar
	}
	if key.Source != "" {
		metadata["web_backend_key_src"] = key.Source
	}
	if req.FreshnessDays > 0 {
		metadata["web_freshness_days"] = req.FreshnessDays
	}
	if len(req.IncludeDomains) > 0 {
		metadata["web_include_domains"] = append([]string(nil), req.IncludeDomains...)
	}
	if len(req.ExcludeDomains) > 0 {
		metadata["web_exclude_domains"] = append([]string(nil), req.ExcludeDomains...)
	}
	return metadata
}

func shouldFallbackFromWebBackendSearch(err error) bool {
	if err == nil {
		return false
	}
	var missing webBackendMissingKeyError
	return !errors.As(err, &missing)
}

func (r *Registry) webBackendExtract(ctx context.Context, backend string, rawURL string, query string) (webtools.ExtractResponse, webBackendKey, error) {
	key, err := r.resolveWebBackendKey(backend)
	if err != nil {
		return webtools.ExtractResponse{}, webBackendKey{}, err
	}
	req := webtools.ExtractRequest{URLs: []string{rawURL}, Query: query}
	switch normalizeToolWebBackend(backend) {
	case webtools.BackendTavily:
		client := webtools.NewTavilyClient(webtools.BackendClientOptions{
			BaseURL:    r.tavilyBaseURL,
			APIKey:     key.Value,
			HTTPClient: r.webBackendHTTP,
			Sleep:      r.webBackendSleep,
		})
		resp, err := client.Extract(ctx, req)
		return resp, key, err
	case webtools.BackendExa:
		client := webtools.NewExaClient(webtools.BackendClientOptions{
			BaseURL:    r.exaBaseURL,
			APIKey:     key.Value,
			HTTPClient: r.webBackendHTTP,
			Sleep:      r.webBackendSleep,
		})
		resp, err := client.Extract(ctx, req)
		return resp, key, err
	default:
		return webtools.ExtractResponse{}, key, fmt.Errorf("unsupported web extract backend %q", backend)
	}
}

func (r *Registry) fetchProviderExtractPageResult(ctx context.Context, backend, rawURL string, opts webFetchOptions) (Result, error) {
	totalStart := time.Now()
	opts.MaxBytes = boundedBytes(opts.MaxBytes)
	u, err := r.validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return Result{}, err
	}
	backend = normalizeToolWebBackend(backend)
	cacheLookupStart := time.Now()
	cacheKey, cacheOK := r.webCacheKey(ctx, "web_extract", u.String(), opts, map[string]any{"backend": backend})
	cacheLookupMS := elapsedMillis(cacheLookupStart)
	if cacheOK {
		if compact, hit := r.loadWebPageCache(cacheKey); hit {
			compact.resetWebPhaseTimings()
			compact.WebCacheLookupMS = cacheLookupMS
			compact.WebTotalMS = elapsedMillis(totalStart)
			out, _ := json.MarshalIndent(compact, "", "  ")
			metadata := webPageMetadata(compact)
			metadata["web_backend"] = backend
			metadata["web_url"] = compact.URL
			return Result{Content: string(out), Metadata: metadata, Truncated: compact.OutputTextTruncated, OutputRef: compact.OutputRef}, nil
		}
	}
	fetchStart := time.Now()
	extracted, key, err := r.webBackendExtract(ctx, backend, u.String(), opts.Query)
	if err != nil {
		return Result{}, err
	}
	fetchMS := elapsedMillis(fetchStart)
	item, err := firstSuccessfulExtract(extracted, u.String())
	if err != nil {
		return Result{}, err
	}
	page := fetchedPage{
		URL:             firstNonEmpty(item.URL, u.String()),
		Status:          200,
		ContentType:     "text/plain; charset=utf-8",
		Title:           item.Title,
		Text:            strings.TrimSpace(item.Text),
		RawBytesFetched: len([]byte(item.Text)),
		MaxBytes:        opts.MaxBytes,
	}
	compactStart := time.Now()
	compact := compactFetchedPage(page, opts)
	compact.WebCacheLookupMS = cacheLookupMS
	compact.WebHTTPFetchMS = fetchMS
	compact.WebCompactMS = elapsedMillis(compactStart)
	summaryStart := time.Now()
	r.applyModelSummaryToPage(ctx, &compact, page, opts)
	compact.WebSummaryMS = elapsedMillis(summaryStart)
	outputRefStart := time.Now()
	ref, err := storeWebOutput("web_extract", page.URL, renderFetchedPageArtifact(page))
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
	metadata := webPageMetadata(compact)
	metadata["web_backend"] = backend
	metadata["web_url"] = compact.URL
	metadata["web_backend_key_env"] = key.EnvVar
	metadata["web_backend_key_src"] = key.Source
	metadata["helper_api_calls"] = extracted.Usage.APICalls
	metadata["helper_cost_usd"] = extracted.Usage.CostUSD
	return Result{Content: string(out), Metadata: metadata, Truncated: compact.OutputTextTruncated, OutputRef: compact.OutputRef}, nil
}

func firstSuccessfulExtract(resp webtools.ExtractResponse, fallbackURL string) (webtools.ExtractResult, error) {
	var firstErr string
	for _, item := range resp.Results {
		if strings.TrimSpace(item.Error) != "" && firstErr == "" {
			firstErr = item.Error
			continue
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		return item, nil
	}
	if firstErr != "" {
		return webtools.ExtractResult{}, fmt.Errorf("web extract backend failed for %s: %s", fallbackURL, firstErr)
	}
	return webtools.ExtractResult{}, fmt.Errorf("web extract backend returned no text for %s", fallbackURL)
}
