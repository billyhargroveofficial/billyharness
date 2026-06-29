package tools

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

var (
	anchorRE   = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	brRE       = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>|</h[1-6]>`)
	scriptRE   = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRE    = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	noscriptRE = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	tagRE      = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRE    = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankRE    = regexp.MustCompile(`\n{3,}`)
	titleRE    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func parseSearchResults(baseURL, body string, limit int) []searchResult {
	matches := anchorRE.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var out []searchResult
	for _, match := range matches {
		if len(out) >= limit {
			break
		}
		title := cleanInlineText(match[2])
		link := normalizeLink(baseURL, html.UnescapeString(match[1]))
		link = unwrapDuckDuckGoURL(link)
		if title == "" || link == "" || seen[link] {
			continue
		}
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if strings.Contains(strings.ToLower(u.Hostname()), "duckduckgo.com") {
			continue
		}
		seen[link] = true
		out = append(out, searchResult{Title: title, URL: link})
	}
	return out
}

func normalizeLink(baseURL, rawLink string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(strings.TrimSpace(rawLink))
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func unwrapDuckDuckGoURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.ToLower(u.Hostname()), "duckduckgo.com") {
		if uddg := u.Query().Get("uddg"); uddg != "" {
			return uddg
		}
	}
	return rawURL
}

func extractLinks(baseURL, body string, limit int) []string {
	matches := anchorRE.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var out []string
	for _, match := range matches {
		if len(out) >= limit {
			break
		}
		link := normalizeLink(baseURL, html.UnescapeString(match[1]))
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		u.Fragment = ""
		link = u.String()
		if !seen[link] {
			seen[link] = true
			out = append(out, link)
		}
	}
	return out
}

func extractTitle(body string) string {
	match := titleRE.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return cleanInlineText(match[1])
}

func cleanHTMLText(body string) string {
	body = scriptRE.ReplaceAllString(body, " ")
	body = styleRE.ReplaceAllString(body, " ")
	body = noscriptRE.ReplaceAllString(body, " ")
	body = brRE.ReplaceAllString(body, "\n")
	body = tagRE.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)
	body = spaceRE.ReplaceAllString(body, " ")
	lines := strings.Split(body, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.TrimSpace(blankRE.ReplaceAllString(strings.Join(cleaned, "\n"), "\n\n"))
}

func cleanInlineText(body string) string {
	body = tagRE.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)
	body = spaceRE.ReplaceAllString(body, " ")
	return strings.TrimSpace(body)
}

func isHTML(contentType, body string) bool {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		return true
	}
	return strings.Contains(strings.ToLower(body[:min(len(body), 512)]), "<html")
}

func isTextual(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "csv")
}
