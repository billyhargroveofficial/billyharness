package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

func (r *Registry) webSearchBackend() string {
	if r == nil {
		return webtools.BackendNative
	}
	return normalizeToolWebBackend(r.toolPolicy.WebSearchBackend)
}

func (r *Registry) webExtractBackend() string {
	if r == nil {
		return webtools.BackendNative
	}
	return normalizeToolWebBackend(r.toolPolicy.WebExtractBackend)
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
		return webBackendKey{}, fmt.Errorf("%s backend missing API key env %s", backend, envName)
	}
	return webBackendKey{Value: value, EnvVar: envName, Source: source}, nil
}

func (r *Registry) webBackendSearch(ctx context.Context, backend string, query string, limit int) (webtools.SearchResponse, webBackendKey, error) {
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
		resp, err := client.Search(ctx, webtools.SearchRequest{Query: query, Limit: limit})
		return resp, key, err
	case webtools.BackendExa:
		client := webtools.NewExaClient(webtools.BackendClientOptions{
			BaseURL:    r.exaBaseURL,
			APIKey:     key.Value,
			HTTPClient: r.webBackendHTTP,
			Sleep:      r.webBackendSleep,
		})
		resp, err := client.Search(ctx, webtools.SearchRequest{Query: query, Limit: limit})
		return resp, key, err
	default:
		return webtools.SearchResponse{}, key, fmt.Errorf("unsupported web search backend %q", backend)
	}
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
	u, err := validatePublicHTTPURL(ctx, rawURL)
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
