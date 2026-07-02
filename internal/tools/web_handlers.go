package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func (r *Registry) addWebFetch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_fetch",
			Description: "Fetch a public HTTP(S) URL and return a compact digest, key points, short extract, links, and an output_ref to the full extracted text. By default it does not return raw page text to protect the agent context.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"query":{"type":"string","description":"Optional focus for extraction/snippets."},"max_bytes":{"type":"integer","default":65536},"max_tokens":{"type":"integer","default":0,"description":"Optional capped inline text token budget; default 0 returns no raw text."},"max_chars":{"type":"integer","description":"Optional capped inline text characters; ignored unless include_text/full_text is true."},"include_text":{"type":"boolean","default":false,"description":"Return capped raw extracted text inline. Prefer output_ref unless exact short quotes are needed."},"full_text":{"type":"boolean","default":false,"description":"Legacy alias for include_text; still capped hard."},"max_links":{"type":"integer","default":20}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL         string `json:"url"`
				Query       string `json:"query"`
				MaxBytes    int    `json:"max_bytes"`
				MaxTokens   int    `json:"max_tokens"`
				MaxChars    int    `json:"max_chars"`
				IncludeText bool   `json:"include_text"`
				FullText    bool   `json:"full_text"`
				MaxLinks    int    `json:"max_links"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			return r.fetchCompactPageResult(ctx, "web_fetch", in.URL, webFetchOptions{
				Query:       in.Query,
				MaxBytes:    in.MaxBytes,
				MaxChars:    in.MaxChars,
				MaxTokens:   in.MaxTokens,
				IncludeText: in.IncludeText,
				FullText:    in.FullText,
				MaxLinks:    in.MaxLinks,
			})
		},
	})
}

func (r *Registry) addWebExtract() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_extract",
			Description: "Fetch a public HTTP(S) URL and extract a focused digest for a query/topic. Returns compact out-of-band summary plus output_ref to full extracted text; never dumps raw page text unless include_text is true.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"query":{"type":"string","description":"Topic, question, or terms to focus extraction."},"max_bytes":{"type":"integer","default":65536},"max_tokens":{"type":"integer","default":0},"max_chars":{"type":"integer"},"include_text":{"type":"boolean","default":false},"max_links":{"type":"integer","default":20}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL         string `json:"url"`
				Query       string `json:"query"`
				MaxBytes    int    `json:"max_bytes"`
				MaxTokens   int    `json:"max_tokens"`
				MaxChars    int    `json:"max_chars"`
				IncludeText bool   `json:"include_text"`
				MaxLinks    int    `json:"max_links"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if backend := r.webExtractBackend(); backend != webtools.BackendNative {
				return r.fetchProviderExtractPageResult(ctx, backend, in.URL, webFetchOptions{
					Query:       in.Query,
					MaxBytes:    in.MaxBytes,
					MaxChars:    in.MaxChars,
					MaxTokens:   in.MaxTokens,
					IncludeText: in.IncludeText,
					MaxLinks:    in.MaxLinks,
				})
			}
			return r.fetchCompactPageResult(ctx, "web_extract", in.URL, webFetchOptions{
				Query:       in.Query,
				MaxBytes:    in.MaxBytes,
				MaxChars:    in.MaxChars,
				MaxTokens:   in.MaxTokens,
				IncludeText: in.IncludeText,
				MaxLinks:    in.MaxLinks,
			})
		},
	})
}

func (r *Registry) addWebSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_search",
			Description: "Search the web via DuckDuckGo Lite and return public result URLs. No API key required.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","default":5}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if strings.TrimSpace(in.Query) == "" {
				return Result{}, fmt.Errorf("query required")
			}
			if in.Limit <= 0 || in.Limit > 10 {
				in.Limit = 5
			}
			if backend := r.webSearchBackend(); backend != webtools.BackendNative {
				results, key, err := r.webBackendSearch(ctx, backend, in.Query, in.Limit)
				if err != nil {
					return Result{}, err
				}
				out, _ := json.MarshalIndent(results, "", "  ")
				return Result{Content: string(out), Metadata: map[string]any{
					"web_backend":         results.Backend,
					"web_query":           strings.TrimSpace(in.Query),
					"web_backend_key_env": key.EnvVar,
					"web_backend_key_src": key.Source,
					"helper_api_calls":    results.Usage.APICalls,
					"helper_cost_usd":     results.Usage.CostUSD,
				}}, nil
			}
			results, err := searchDuckDuckGoLite(ctx, in.Query, in.Limit)
			if err != nil {
				return Result{}, err
			}
			out, _ := json.MarshalIndent(results, "", "  ")
			return Result{Content: string(out)}, nil
		},
	})
}

func (r *Registry) addWebCrawl() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_crawl",
			Description: "Crawl public HTTP(S) pages breadth-first and return compact per-page digests plus one output_ref containing full extracted crawl text. Raw page text is not returned inline by default.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"query":{"type":"string","description":"Optional focus for extraction/snippets."},"max_pages":{"type":"integer","default":3},"max_depth":{"type":"integer","default":1},"same_host":{"type":"boolean","default":true},"max_bytes_per_page":{"type":"integer","default":65536},"max_tokens_per_page":{"type":"integer","default":0},"max_total_tokens":{"type":"integer","default":0},"max_chars_per_page":{"type":"integer","description":"Optional capped inline text characters per page; ignored unless include_text/full_text is true."},"include_text":{"type":"boolean","default":false},"full_text":{"type":"boolean","default":false}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL              string `json:"url"`
				Query            string `json:"query"`
				MaxPages         int    `json:"max_pages"`
				MaxDepth         int    `json:"max_depth"`
				SameHost         *bool  `json:"same_host"`
				MaxBytesPerPage  int    `json:"max_bytes_per_page"`
				MaxTokensPerPage int    `json:"max_tokens_per_page"`
				MaxTotalTokens   int    `json:"max_total_tokens"`
				MaxCharsPerPage  int    `json:"max_chars_per_page"`
				IncludeText      bool   `json:"include_text"`
				FullText         bool   `json:"full_text"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			sameHost := true
			if in.SameHost != nil {
				sameHost = *in.SameHost
			}
			opts := webFetchOptions{
				Query:          in.Query,
				MaxChars:       in.MaxCharsPerPage,
				MaxTokens:      in.MaxTokensPerPage,
				MaxTotalTokens: in.MaxTotalTokens,
				IncludeText:    in.IncludeText,
				FullText:       in.FullText,
				MaxLinks:       0,
			}
			cacheKey, cacheOK := r.webCacheKey(ctx, "web_crawl", in.URL, opts, map[string]any{
				"max_pages":          normalizedCrawlMaxPages(in.MaxPages),
				"max_depth":          normalizedCrawlMaxDepth(in.MaxDepth),
				"same_host":          sameHost,
				"max_bytes_per_page": boundedBytes(in.MaxBytesPerPage),
			})
			if cacheOK {
				if compact, hit := r.loadWebCrawlCache(cacheKey); hit {
					out, _ := json.MarshalIndent(compact, "", "  ")
					return Result{Content: string(out), Metadata: crawlMetadata(compact), Truncated: compact.OutputTextTruncated, OutputRef: compact.OutputRef}, nil
				}
			}
			pages, err := r.crawl(ctx, in.URL, in.MaxPages, in.MaxDepth, sameHost, boundedBytes(in.MaxBytesPerPage))
			if err != nil {
				return Result{}, err
			}
			compact, ref, storeErr := r.compactCrawlResult(ctx, pages, opts)
			if storeErr != nil {
				compact.CompactNote = strings.TrimSpace(compact.CompactNote + " full crawl text save failed: " + storeErr.Error())
			}
			if cacheOK {
				compact.applyWebCache(cacheKey, false, 0, r.toolPolicy.WebCacheTTL)
				_ = r.saveWebCrawlCache(cacheKey, compact)
			}
			out, _ := json.MarshalIndent(compact, "", "  ")
			return Result{Content: string(out), Metadata: crawlMetadata(compact), Truncated: compact.OutputTextTruncated, OutputRef: ref}, nil
		},
	})
}

func (r *Registry) addWebCache() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_cache_status",
			Description: "Inspect the native web fetch/extract/crawl cache. Shows entry count, total bytes, TTL, max size, and expired entries.",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			status := r.webCacheStatus()
			out, _ := json.MarshalIndent(status, "", "  ")
			return Result{Content: string(out), Metadata: map[string]any{
				"web_cache_enabled": status.Enabled,
				"web_cache_entries": status.Entries,
				"web_cache_bytes":   status.Bytes,
				"web_cache_expired": status.ExpiredEntries,
			}}, nil
		},
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_cache_clear",
			Description: "Clear the native web cache under $BILLYHARNESS_HOME/web-cache. This does not delete saved tool-output refs.",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			removed, bytes, err := r.clearWebCache()
			if err != nil {
				return Result{}, err
			}
			out, _ := json.MarshalIndent(map[string]any{
				"removed_entries": removed,
				"removed_bytes":   bytes,
				"cache_dir":       r.webCacheDir(),
			}, "", "  ")
			return Result{Content: string(out), Metadata: map[string]any{
				"web_cache_removed_entries": removed,
				"web_cache_removed_bytes":   bytes,
			}}, nil
		},
	})
}
