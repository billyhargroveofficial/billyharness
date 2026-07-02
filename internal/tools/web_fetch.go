package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

var duckDuckGoLiteBaseURL = "https://lite.duckduckgo.com/lite/"

func duckDuckGoLiteSearchURL(query string) string {
	values := url.Values{"q": []string{query}}
	return duckDuckGoLiteBaseURL + "?" + values.Encode()
}

func searchDuckDuckGoLite(ctx context.Context, query string, limit int) ([]searchResult, error) {
	searchURL := duckDuckGoLiteSearchURL(query)
	body, _, _, err := httpGet(ctx, searchURL, maxWebBytes)
	if err != nil {
		return nil, err
	}
	return parseSearchResults(searchURL, string(body), limit), nil
}

func fetchPage(ctx context.Context, rawURL string, maxBytes int) (fetchedPage, error) {
	return fetchPageWithClient(ctx, webtools.DefaultClient(), rawURL, maxBytes)
}

func (r *Registry) fetchPage(ctx context.Context, rawURL string, maxBytes int) (fetchedPage, error) {
	return fetchPageWithClient(ctx, r.nativeWebHTTPClient(), rawURL, maxBytes)
}

func fetchPageWithClient(ctx context.Context, client webtools.Client, rawURL string, maxBytes int) (fetchedPage, error) {
	body, finalURL, contentType, err := httpGetWithClient(ctx, client, rawURL, maxBytes+1)
	if err != nil {
		return fetchedPage{}, err
	}
	rawBytesFetched := len(body)
	truncated := false
	if len(body) > maxBytes {
		truncated = true
		body = body[:maxBytes]
	}
	textBody := string(body)
	page := fetchedPage{
		URL:             finalURL,
		Status:          http.StatusOK,
		ContentType:     contentType,
		RawBytesFetched: rawBytesFetched,
		MaxBytes:        maxBytes,
		Truncated:       truncated,
	}
	if isHTML(contentType, textBody) {
		page.Title = extractTitle(textBody)
		page.Text = truncate(cleanHTMLText(textBody), maxBytes)
		page.Links = extractLinks(finalURL, textBody, 50)
		return page, nil
	}
	if !isTextual(contentType) {
		return fetchedPage{}, fmt.Errorf("refusing non-text response content-type %q", contentType)
	}
	page.Text = truncate(textBody, maxBytes)
	return page, nil
}

func httpGet(ctx context.Context, rawURL string, maxBytes int) ([]byte, string, string, error) {
	return httpGetWithClient(ctx, webtools.DefaultClient(), rawURL, maxBytes)
}

func httpGetWithClient(ctx context.Context, client webtools.Client, rawURL string, maxBytes int) ([]byte, string, string, error) {
	resp, err := client.Get(ctx, rawURL, maxBytes)
	if err != nil {
		return nil, "", "", err
	}
	return resp.Body, resp.URL, resp.ContentType, nil
}

func validatePublicHTTPURL(ctx context.Context, rawURL string) (*url.URL, error) {
	return webtools.ValidatePublicHTTPURL(ctx, rawURL, nil)
}

func (r *Registry) validatePublicHTTPURL(ctx context.Context, rawURL string) (*url.URL, error) {
	client := r.nativeWebHTTPClient()
	return webtools.ValidatePublicHTTPURL(ctx, rawURL, client.Resolver)
}

func (r *Registry) nativeWebHTTPClient() webtools.Client {
	if r != nil && r.nativeWebClient != nil {
		return *r.nativeWebClient
	}
	return webtools.DefaultClient()
}

func boundedBytes(n int) int {
	if n <= 0 {
		return defaultWebBytes
	}
	if n > maxWebBytes {
		return maxWebBytes
	}
	return n
}
