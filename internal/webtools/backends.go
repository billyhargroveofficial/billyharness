package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	BackendNative = "native"
	BackendTavily = "tavily"
	BackendExa    = "exa"

	defaultTavilyBaseURL = "https://api.tavily.com"
	defaultExaBaseURL    = "https://api.exa.ai"
)

type SearchRequest struct {
	Query string
	Limit int
}

type SearchResponse struct {
	Backend string         `json:"backend"`
	Results []SearchResult `json:"results"`
	Usage   Usage          `json:"usage,omitempty"`
}

type SearchResult struct {
	Title         string  `json:"title,omitempty"`
	URL           string  `json:"url"`
	Content       string  `json:"content,omitempty"`
	Score         float64 `json:"score,omitempty"`
	PublishedDate string  `json:"published_date,omitempty"`
}

type ExtractRequest struct {
	URLs  []string
	Query string
}

type ExtractResponse struct {
	Backend string          `json:"backend"`
	Results []ExtractResult `json:"results"`
	Usage   Usage           `json:"usage,omitempty"`
}

type ExtractResult struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
	RawJSON string `json:"-"`
}

type Usage struct {
	APICalls int     `json:"api_calls,omitempty"`
	CostUSD  float64 `json:"cost_usd,omitempty"`
}

type BackendClientOptions struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	MaxRetries int
	Sleep      func(context.Context, time.Duration) error
}

type TavilyClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

type ExaClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

func NewTavilyClient(opts BackendClientOptions) TavilyClient {
	return TavilyClient{
		baseURL:    normalizedBaseURL(opts.BaseURL, defaultTavilyBaseURL),
		apiKey:     strings.TrimSpace(opts.APIKey),
		httpClient: backendHTTPClient(opts.HTTPClient),
		maxRetries: normalizedRetries(opts.MaxRetries),
		sleep:      opts.Sleep,
	}
}

func NewExaClient(opts BackendClientOptions) ExaClient {
	return ExaClient{
		baseURL:    normalizedBaseURL(opts.BaseURL, defaultExaBaseURL),
		apiKey:     strings.TrimSpace(opts.APIKey),
		httpClient: backendHTTPClient(opts.HTTPClient),
		maxRetries: normalizedRetries(opts.MaxRetries),
		sleep:      opts.Sleep,
	}
}

func (c TavilyClient) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return SearchResponse{}, fmt.Errorf("tavily api key is required")
	}
	limit := normalizedSearchLimit(req.Limit)
	payload := map[string]any{
		"query":               strings.TrimSpace(req.Query),
		"max_results":         limit,
		"search_depth":        "basic",
		"include_answer":      false,
		"include_raw_content": false,
	}
	var raw struct {
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Content       string  `json:"content"`
			Score         float64 `json:"score"`
			PublishedDate string  `json:"published_date"`
		} `json:"results"`
		ResponseTime float64 `json:"response_time"`
	}
	if err := c.post(ctx, "/search", payload, &raw); err != nil {
		return SearchResponse{}, err
	}
	out := SearchResponse{Backend: BackendTavily, Usage: Usage{APICalls: 1}}
	for _, item := range raw.Results {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		out.Results = append(out.Results, SearchResult{
			Title:         strings.TrimSpace(item.Title),
			URL:           strings.TrimSpace(item.URL),
			Content:       strings.TrimSpace(item.Content),
			Score:         item.Score,
			PublishedDate: strings.TrimSpace(item.PublishedDate),
		})
	}
	return out, nil
}

func (c TavilyClient) Extract(ctx context.Context, req ExtractRequest) (ExtractResponse, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return ExtractResponse{}, fmt.Errorf("tavily api key is required")
	}
	payload := map[string]any{
		"urls":          cleanURLs(req.URLs),
		"extract_depth": "basic",
		"format":        "text",
	}
	var raw struct {
		Results []struct {
			URL        string `json:"url"`
			RawContent string `json:"raw_content"`
			Content    string `json:"content"`
		} `json:"results"`
		FailedResults []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"failed_results"`
	}
	if err := c.post(ctx, "/extract", payload, &raw); err != nil {
		return ExtractResponse{}, err
	}
	out := ExtractResponse{Backend: BackendTavily, Usage: Usage{APICalls: 1}}
	for _, item := range raw.Results {
		text := item.RawContent
		if strings.TrimSpace(text) == "" {
			text = item.Content
		}
		out.Results = append(out.Results, ExtractResult{URL: strings.TrimSpace(item.URL), Text: strings.TrimSpace(text)})
	}
	for _, item := range raw.FailedResults {
		out.Results = append(out.Results, ExtractResult{URL: strings.TrimSpace(item.URL), Error: strings.TrimSpace(item.Error)})
	}
	return out, nil
}

func (c ExaClient) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return SearchResponse{}, fmt.Errorf("exa api key is required")
	}
	payload := map[string]any{
		"query":      strings.TrimSpace(req.Query),
		"numResults": normalizedSearchLimit(req.Limit),
	}
	var raw struct {
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Text          string  `json:"text"`
			Score         float64 `json:"score"`
			PublishedDate string  `json:"publishedDate"`
		} `json:"results"`
		CostDollars *struct {
			Total float64 `json:"total"`
		} `json:"costDollars"`
	}
	if err := c.post(ctx, "/search", payload, &raw); err != nil {
		return SearchResponse{}, err
	}
	out := SearchResponse{Backend: BackendExa, Usage: Usage{APICalls: 1}}
	if raw.CostDollars != nil {
		out.Usage.CostUSD = raw.CostDollars.Total
	}
	for _, item := range raw.Results {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		out.Results = append(out.Results, SearchResult{
			Title:         strings.TrimSpace(item.Title),
			URL:           strings.TrimSpace(item.URL),
			Score:         item.Score,
			PublishedDate: strings.TrimSpace(item.PublishedDate),
		})
	}
	return out, nil
}

func (c ExaClient) Extract(ctx context.Context, req ExtractRequest) (ExtractResponse, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return ExtractResponse{}, fmt.Errorf("exa api key is required")
	}
	payload := map[string]any{
		"urls": cleanURLs(req.URLs),
		"text": true,
	}
	var raw struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Text  string `json:"text"`
		} `json:"results"`
		Statuses []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"statuses"`
		CostDollars *struct {
			Total float64 `json:"total"`
		} `json:"costDollars"`
	}
	if err := c.post(ctx, "/contents", payload, &raw); err != nil {
		return ExtractResponse{}, err
	}
	out := ExtractResponse{Backend: BackendExa, Usage: Usage{APICalls: 1}}
	if raw.CostDollars != nil {
		out.Usage.CostUSD = raw.CostDollars.Total
	}
	for _, item := range raw.Results {
		out.Results = append(out.Results, ExtractResult{
			URL:   strings.TrimSpace(item.URL),
			Title: strings.TrimSpace(item.Title),
			Text:  strings.TrimSpace(item.Text),
		})
	}
	for _, status := range raw.Statuses {
		if strings.TrimSpace(status.Error) == "" {
			continue
		}
		out.Results = append(out.Results, ExtractResult{URL: strings.TrimSpace(status.ID), Error: strings.TrimSpace(status.Error)})
	}
	return out, nil
}

func (c TavilyClient) post(ctx context.Context, path string, payload any, out any) error {
	return postBackendJSON(ctx, c.httpClient, c.sleep, c.maxRetries, c.baseURL, path, payload, out, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	})
}

func (c ExaClient) post(ctx context.Context, path string, payload any, out any) error {
	return postBackendJSON(ctx, c.httpClient, c.sleep, c.maxRetries, c.baseURL, path, payload, out, func(req *http.Request) {
		req.Header.Set("x-api-key", c.apiKey)
	})
}

func postBackendJSON(ctx context.Context, client *http.Client, sleep func(context.Context, time.Duration) error, maxRetries int, baseURL, path string, payload any, out any, auth func(*http.Request)) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint, err := joinBackendURL(baseURL, path)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if auth != nil {
			auth(req)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			err = decodeBackendResponse(resp, out)
			if err == nil {
				return nil
			}
			lastErr = err
			if resp.StatusCode != http.StatusTooManyRequests || attempt == maxRetries {
				return err
			}
			delay := retryDelay(resp.Header.Get("Retry-After"), attempt)
			if sleep == nil {
				sleep = sleepContext
			}
			if sleepErr := sleep(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		if attempt == maxRetries {
			break
		}
		if sleep == nil {
			sleep = sleepContext
		}
		if sleepErr := sleep(ctx, retryDelay("", attempt)); sleepErr != nil {
			return sleepErr
		}
	}
	return lastErr
}

func decodeBackendResponse(resp *http.Response, out any) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("web backend HTTP %d: %s", resp.StatusCode, truncate(strings.TrimSpace(string(body)), 1000))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func joinBackendURL(baseURL, path string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid backend base URL %q", baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String(), nil
}

func normalizedBaseURL(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return strings.TrimRight(value, "/")
}

func backendHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func normalizedRetries(value int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return 2
	}
	if value > 5 {
		return 5
	}
	return value
}

func normalizedSearchLimit(value int) int {
	if value <= 0 {
		return 5
	}
	if value > 10 {
		return 10
	}
	return value
}

func cleanURLs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func retryDelay(header string, attempt int) time.Duration {
	header = strings.TrimSpace(header)
	if header != "" {
		if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(header); err == nil {
			if delay := time.Until(when); delay > 0 {
				if delay > 10*time.Second {
					return 10 * time.Second
				}
				return delay
			}
		}
	}
	delay := time.Duration(250*(1<<max(0, attempt))) * time.Millisecond
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
