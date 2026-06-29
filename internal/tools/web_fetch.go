package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func searchDuckDuckGoLite(ctx context.Context, query string, limit int) ([]searchResult, error) {
	values := url.Values{"q": []string{query}}
	searchURL := "https://lite.duckduckgo.com/lite/?" + values.Encode()
	body, _, _, err := httpGet(ctx, searchURL, maxWebBytes)
	if err != nil {
		return nil, err
	}
	return parseSearchResults(searchURL, string(body), limit), nil
}

func fetchPage(ctx context.Context, rawURL string, maxBytes int) (fetchedPage, error) {
	body, finalURL, contentType, err := httpGet(ctx, rawURL, maxBytes+1)
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
	resp, err := webtools.DefaultClient().Get(ctx, rawURL, maxBytes)
	if err != nil {
		return nil, "", "", err
	}
	return resp.Body, resp.URL, resp.ContentType, nil
}

func validatePublicHTTPURL(ctx context.Context, rawURL string) (*url.URL, error) {
	return webtools.ValidatePublicHTTPURL(ctx, rawURL, nil)
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
