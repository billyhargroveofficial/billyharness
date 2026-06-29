package tools

import (
	"context"
	"net/url"
	"strings"
)

type crawlItem struct {
	URL   string `json:"url"`
	Depth int    `json:"depth"`
}

type crawlPage struct {
	URL             string `json:"url"`
	Depth           int    `json:"depth"`
	Title           string `json:"title,omitempty"`
	Text            string `json:"text"`
	RawBytesFetched int    `json:"raw_bytes_fetched,omitempty"`
	MaxBytes        int    `json:"max_bytes,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	Error           string `json:"error,omitempty"`
}

func normalizedCrawlMaxPages(maxPages int) int {
	if maxPages <= 0 || maxPages > 10 {
		return 3
	}
	return maxPages
}

func normalizedCrawlMaxDepth(maxDepth int) int {
	if maxDepth < 0 || maxDepth > 2 {
		return 1
	}
	return maxDepth
}

func (r *Registry) crawl(ctx context.Context, rawURL string, maxPages, maxDepth int, sameHost bool, maxBytesPerPage int) ([]crawlPage, error) {
	maxPages = normalizedCrawlMaxPages(maxPages)
	maxDepth = normalizedCrawlMaxDepth(maxDepth)
	start, err := validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	startHost := strings.ToLower(start.Hostname())
	queue := []crawlItem{{URL: start.String(), Depth: 0}}
	seen := map[string]bool{}
	var pages []crawlPage
	for len(queue) > 0 && len(pages) < maxPages {
		item := queue[0]
		queue = queue[1:]
		if seen[item.URL] {
			continue
		}
		seen[item.URL] = true
		page, err := fetchPage(ctx, item.URL, maxBytesPerPage)
		out := crawlPage{URL: item.URL, Depth: item.Depth}
		if err != nil {
			out.Error = err.Error()
			pages = append(pages, out)
			continue
		}
		out.Title = page.Title
		out.Text = page.Text
		out.RawBytesFetched = page.RawBytesFetched
		out.MaxBytes = page.MaxBytes
		out.Truncated = page.Truncated
		pages = append(pages, out)
		if item.Depth >= maxDepth {
			continue
		}
		for _, link := range page.Links {
			u, err := url.Parse(link)
			if err != nil {
				continue
			}
			if sameHost && strings.ToLower(u.Hostname()) != startHost {
				continue
			}
			if !seen[u.String()] {
				queue = append(queue, crawlItem{URL: u.String(), Depth: item.Depth + 1})
			}
		}
	}
	return pages, nil
}
