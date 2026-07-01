package tools

import (
	"sort"
	"strings"
)

type fetchedPage struct {
	URL             string   `json:"url"`
	Status          int      `json:"status"`
	ContentType     string   `json:"content_type"`
	Title           string   `json:"title,omitempty"`
	Text            string   `json:"text"`
	Links           []string `json:"links,omitempty"`
	RawBytesFetched int      `json:"raw_bytes_fetched,omitempty"`
	MaxBytes        int      `json:"max_bytes,omitempty"`
	Truncated       bool     `json:"truncated,omitempty"`
}

type searchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type webFetchOptions struct {
	Query          string
	MaxBytes       int
	MaxChars       int
	MaxTokens      int
	MaxTotalTokens int
	IncludeText    bool
	FullText       bool
	MaxLinks       int
}

type compactPage struct {
	URL                  string   `json:"url"`
	Status               int      `json:"status,omitempty"`
	ContentType          string   `json:"content_type,omitempty"`
	OutputClass          string   `json:"output_class,omitempty"`
	SummaryMode          string   `json:"summary_mode,omitempty"`
	Title                string   `json:"title,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	SummaryChars         int      `json:"summary_chars,omitempty"`
	SummarizerProvider   string   `json:"summarizer_provider,omitempty"`
	SummarizerModel      string   `json:"summarizer_model,omitempty"`
	WebsumInputTokens    int64    `json:"websum_input_tokens,omitempty"`
	WebsumOutputTokens   int64    `json:"websum_output_tokens,omitempty"`
	WebsumCacheHit       int64    `json:"websum_cache_hit,omitempty"`
	WebsumCacheMiss      int64    `json:"websum_cache_miss,omitempty"`
	WebsumCost           float64  `json:"websum_cost,omitempty"`
	WebsumModel          string   `json:"websum_model,omitempty"`
	WebsumError          string   `json:"websum_error,omitempty"`
	WebCacheHit          bool     `json:"web_cache_hit"`
	WebCacheKey          string   `json:"web_cache_key,omitempty"`
	WebCacheAgeMS        int64    `json:"web_cache_age_ms,omitempty"`
	WebCacheTTLMS        int64    `json:"web_cache_ttl_ms,omitempty"`
	WebCacheLookupMS     int64    `json:"web_cache_lookup_ms,omitempty"`
	WebHTTPFetchMS       int64    `json:"web_http_fetch_ms,omitempty"`
	WebCompactMS         int64    `json:"web_compact_ms,omitempty"`
	WebSummaryMS         int64    `json:"web_summary_ms,omitempty"`
	WebOutputRefMS       int64    `json:"web_output_ref_ms,omitempty"`
	WebCacheSaveMS       int64    `json:"web_cache_save_ms,omitempty"`
	WebTotalMS           int64    `json:"web_total_ms,omitempty"`
	KeyPoints            []string `json:"key_points,omitempty"`
	Extract              string   `json:"extract,omitempty"`
	Text                 string   `json:"text,omitempty"`
	Links                []string `json:"links,omitempty"`
	OutputRef            string   `json:"output_ref,omitempty"`
	Truncated            bool     `json:"truncated,omitempty"`
	RawBytesFetched      int      `json:"raw_bytes_fetched,omitempty"`
	MaxBytes             int      `json:"max_bytes,omitempty"`
	OriginalTextChars    int      `json:"original_text_chars,omitempty"`
	OriginalTextTokens   int      `json:"original_text_tokens,omitempty"`
	ReturnedTextChars    int      `json:"returned_text_chars,omitempty"`
	EstimatedTextTokens  int      `json:"estimated_text_tokens,omitempty"`
	EstimatedTokensSaved int      `json:"estimated_tokens_saved,omitempty"`
	BudgetTextChars      int      `json:"budget_text_chars,omitempty"`
	BudgetTextTokens     int      `json:"budget_text_tokens,omitempty"`
	OutputTextTruncated  bool     `json:"output_text_truncated,omitempty"`
	CompactNote          string   `json:"compact_note,omitempty"`
}

type compactCrawlPage struct {
	URL                  string   `json:"url"`
	Depth                int      `json:"depth"`
	OutputClass          string   `json:"output_class,omitempty"`
	SummaryMode          string   `json:"summary_mode,omitempty"`
	Title                string   `json:"title,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	SummaryChars         int      `json:"summary_chars,omitempty"`
	SummarizerProvider   string   `json:"summarizer_provider,omitempty"`
	SummarizerModel      string   `json:"summarizer_model,omitempty"`
	WebsumInputTokens    int64    `json:"websum_input_tokens,omitempty"`
	WebsumOutputTokens   int64    `json:"websum_output_tokens,omitempty"`
	WebsumCacheHit       int64    `json:"websum_cache_hit,omitempty"`
	WebsumCacheMiss      int64    `json:"websum_cache_miss,omitempty"`
	WebsumCost           float64  `json:"websum_cost,omitempty"`
	WebsumModel          string   `json:"websum_model,omitempty"`
	WebsumError          string   `json:"websum_error,omitempty"`
	WebCacheHit          bool     `json:"web_cache_hit,omitempty"`
	WebCacheKey          string   `json:"web_cache_key,omitempty"`
	WebCacheAgeMS        int64    `json:"web_cache_age_ms,omitempty"`
	WebCacheTTLMS        int64    `json:"web_cache_ttl_ms,omitempty"`
	KeyPoints            []string `json:"key_points,omitempty"`
	Extract              string   `json:"extract,omitempty"`
	Text                 string   `json:"text,omitempty"`
	Error                string   `json:"error,omitempty"`
	OutputRef            string   `json:"output_ref,omitempty"`
	RawBytesFetched      int      `json:"raw_bytes_fetched,omitempty"`
	MaxBytes             int      `json:"max_bytes,omitempty"`
	Truncated            bool     `json:"truncated,omitempty"`
	OriginalTextChars    int      `json:"original_text_chars,omitempty"`
	OriginalTextTokens   int      `json:"original_text_tokens,omitempty"`
	ReturnedTextChars    int      `json:"returned_text_chars,omitempty"`
	EstimatedTextTokens  int      `json:"estimated_text_tokens,omitempty"`
	EstimatedTokensSaved int      `json:"estimated_tokens_saved,omitempty"`
	BudgetTextChars      int      `json:"budget_text_chars,omitempty"`
	BudgetTextTokens     int      `json:"budget_text_tokens,omitempty"`
	OutputTextTruncated  bool     `json:"output_text_truncated,omitempty"`
	CompactNote          string   `json:"compact_note,omitempty"`
}

type compactCrawlOutput struct {
	Pages                []compactCrawlPage `json:"pages"`
	OutputClass          string             `json:"output_class,omitempty"`
	SummaryMode          string             `json:"summary_mode,omitempty"`
	SummaryChars         int                `json:"summary_chars,omitempty"`
	SummarizerProvider   string             `json:"summarizer_provider,omitempty"`
	SummarizerModel      string             `json:"summarizer_model,omitempty"`
	WebsumInputTokens    int64              `json:"websum_input_tokens,omitempty"`
	WebsumOutputTokens   int64              `json:"websum_output_tokens,omitempty"`
	WebsumCacheHit       int64              `json:"websum_cache_hit,omitempty"`
	WebsumCacheMiss      int64              `json:"websum_cache_miss,omitempty"`
	WebsumCost           float64            `json:"websum_cost,omitempty"`
	WebsumModel          string             `json:"websum_model,omitempty"`
	WebsumError          string             `json:"websum_error,omitempty"`
	WebCacheHit          bool               `json:"web_cache_hit"`
	WebCacheKey          string             `json:"web_cache_key,omitempty"`
	WebCacheAgeMS        int64              `json:"web_cache_age_ms,omitempty"`
	WebCacheTTLMS        int64              `json:"web_cache_ttl_ms,omitempty"`
	OutputRef            string             `json:"output_ref,omitempty"`
	RawBytesFetched      int                `json:"raw_bytes_fetched,omitempty"`
	MaxBytesPerPage      int                `json:"max_bytes_per_page,omitempty"`
	OriginalTextChars    int                `json:"original_text_chars,omitempty"`
	OriginalTextTokens   int                `json:"original_text_tokens,omitempty"`
	ReturnedTextChars    int                `json:"returned_text_chars,omitempty"`
	EstimatedTextTokens  int                `json:"estimated_text_tokens,omitempty"`
	EstimatedTokensSaved int                `json:"estimated_tokens_saved,omitempty"`
	OutputTextTruncated  bool               `json:"output_text_truncated,omitempty"`
	CompactNote          string             `json:"compact_note,omitempty"`
}

func compactFetchedPage(page fetchedPage, opts webFetchOptions) compactPage {
	maxChars, budgetTokens := webTextBudget(opts, webTextChars, webDefaultTextTokens, webMaxTextTokens)
	text := ""
	outputTruncated := false
	includeText := opts.IncludeText || opts.FullText
	if includeText {
		text, outputTruncated = compactWebText(page.Text, maxChars, opts.FullText)
	} else {
		outputTruncated = strings.TrimSpace(page.Text) != ""
	}
	maxLinks := opts.MaxLinks
	if maxLinks <= 0 || maxLinks > 50 {
		maxLinks = webMaxLinks
	}
	links := page.Links
	if len(links) > maxLinks {
		links = links[:maxLinks]
	}
	summary := summarizeText(page.Title, page.Text, webDigestChars)
	points := keyPoints(page.Text, opts.Query, 5, webKeyPointChars)
	extract := extractSnippets(page.Text, opts.Query, webExtractChars)
	originalTokens := estimateTokens(page.Text)
	outputClass := webOutputClass(includeText)
	summaryMode := "extractive"
	summarizerModel := "extractive"
	if tinyDirectWebAnswer(page.Text, page.Links, page.Truncated, includeText, originalTokens) {
		text = strings.TrimSpace(page.Text)
		outputTruncated = false
		outputClass = "tiny_direct_answer"
		summaryMode = "direct"
		summarizerModel = "direct"
		summary = text
		points = nil
		extract = ""
	}
	estimatedTokens := estimateTokens(compactInlineText(text, page.Title, page.Text, opts.Query))
	if outputClass == "tiny_direct_answer" {
		estimatedTokens = originalTokens
	}
	estimatedSaved := maxInt(0, originalTokens-estimatedTokens)
	return compactPage{
		URL:                  page.URL,
		Status:               page.Status,
		ContentType:          page.ContentType,
		OutputClass:          outputClass,
		SummaryMode:          summaryMode,
		Title:                page.Title,
		Summary:              summary,
		SummaryChars:         webSummaryChars(summary, points, extract),
		SummarizerProvider:   "native",
		SummarizerModel:      summarizerModel,
		WebsumInputTokens:    int64(originalTokens),
		WebsumOutputTokens:   int64(estimatedTokens),
		WebsumModel:          summarizerModel,
		KeyPoints:            points,
		Extract:              extract,
		Text:                 text,
		Links:                links,
		Truncated:            page.Truncated,
		RawBytesFetched:      page.RawBytesFetched,
		MaxBytes:             page.MaxBytes,
		OriginalTextChars:    len([]rune(page.Text)),
		OriginalTextTokens:   originalTokens,
		ReturnedTextChars:    len([]rune(text)),
		EstimatedTextTokens:  estimatedTokens,
		EstimatedTokensSaved: estimatedSaved,
		BudgetTextChars:      maxChars,
		BudgetTextTokens:     budgetTokens,
		OutputTextTruncated:  outputTruncated || len(page.Links) > len(links),
		CompactNote:          webCompactNote(outputTruncated || len(page.Links) > len(links), includeText, opts.FullText),
	}
}

func compactCrawlPages(pages []crawlPage, opts webFetchOptions) []compactCrawlPage {
	maxChars, budgetTokens := webTextBudget(opts, webCrawlChars, webCrawlDefaultTokens, webMaxTextTokens)
	totalTextChars, totalTokens := webTotalTextBudget(opts.MaxTotalTokens, len(pages))
	if totalTextChars > 0 && len(pages) > 0 {
		perPageTotalCap := max(800, totalTextChars/len(pages))
		if maxChars > perPageTotalCap {
			maxChars = perPageTotalCap
			budgetTokens = estimateTokensByChars(maxChars)
		}
	}
	out := make([]compactCrawlPage, 0, len(pages))
	for _, page := range pages {
		text := ""
		outputTruncated := false
		includeText := opts.IncludeText || opts.FullText
		if includeText {
			text, outputTruncated = compactWebText(page.Text, maxChars, opts.FullText)
		} else {
			outputTruncated = strings.TrimSpace(page.Text) != ""
		}
		summary := summarizeText(page.Title, page.Text, webDigestChars)
		points := keyPoints(page.Text, opts.Query, 4, webKeyPointChars)
		extract := extractSnippets(page.Text, opts.Query, webExtractChars)
		originalTokens := estimateTokens(page.Text)
		outputClass := webOutputClass(includeText)
		summaryMode := "extractive"
		summarizerModel := "extractive"
		if tinyDirectWebAnswer(page.Text, nil, page.Truncated, includeText, originalTokens) {
			text = strings.TrimSpace(page.Text)
			outputTruncated = false
			outputClass = "tiny_direct_answer"
			summaryMode = "direct"
			summarizerModel = "direct"
			summary = text
			points = nil
			extract = ""
		}
		estimatedTokens := estimateTokens(compactInlineText(text, page.Title, page.Text, opts.Query))
		if outputClass == "tiny_direct_answer" {
			estimatedTokens = originalTokens
		}
		out = append(out, compactCrawlPage{
			URL:                  page.URL,
			Depth:                page.Depth,
			OutputClass:          outputClass,
			SummaryMode:          summaryMode,
			Title:                page.Title,
			Summary:              summary,
			SummaryChars:         webSummaryChars(summary, points, extract),
			SummarizerProvider:   "native",
			SummarizerModel:      summarizerModel,
			WebsumInputTokens:    int64(originalTokens),
			WebsumOutputTokens:   int64(estimatedTokens),
			WebsumModel:          summarizerModel,
			KeyPoints:            points,
			Extract:              extract,
			Text:                 text,
			Error:                page.Error,
			RawBytesFetched:      page.RawBytesFetched,
			MaxBytes:             page.MaxBytes,
			Truncated:            page.Truncated,
			OriginalTextChars:    len([]rune(page.Text)),
			OriginalTextTokens:   originalTokens,
			ReturnedTextChars:    len([]rune(text)),
			EstimatedTextTokens:  estimatedTokens,
			EstimatedTokensSaved: maxInt(0, originalTokens-estimatedTokens),
			BudgetTextChars:      maxChars,
			BudgetTextTokens:     min(budgetTokens, totalTokens),
			OutputTextTruncated:  outputTruncated,
			CompactNote:          webCompactNote(outputTruncated, includeText, opts.FullText),
		})
	}
	return out
}

func compactInlineText(text, title, fullText, query string) string {
	return strings.Join(nonEmptyStrings(
		title,
		summarizeText(title, fullText, webDigestChars),
		strings.Join(keyPoints(fullText, query, 5, webKeyPointChars), "\n"),
		extractSnippets(fullText, query, webExtractChars),
		text,
	), "\n")
}

func keyPoints(text, query string, maxPoints, maxChars int) []string {
	if maxPoints <= 0 {
		return nil
	}
	sentences := candidateSentences(text)
	if len(sentences) == 0 {
		return nil
	}
	ranked := rankSentences(sentences, query)
	points := make([]string, 0, maxPoints)
	seen := map[string]bool{}
	for _, sentence := range ranked {
		sentence = truncateRunes(oneLine(sentence), maxChars)
		if sentence == "" || seen[strings.ToLower(sentence)] {
			continue
		}
		seen[strings.ToLower(sentence)] = true
		points = append(points, sentence)
		if len(points) >= maxPoints {
			break
		}
	}
	return points
}

func extractSnippets(text, query string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxChars <= 0 {
		return ""
	}
	var selected []string
	terms := queryTerms(query)
	for _, sentence := range candidateSentences(text) {
		if len(selected) >= 6 {
			break
		}
		clean := oneLine(sentence)
		if clean == "" {
			continue
		}
		if len(terms) == 0 || textScore(clean, terms) > 0 {
			selected = append(selected, clean)
		}
	}
	if len(selected) == 0 {
		for _, sentence := range candidateSentences(text) {
			selected = append(selected, sentence)
			if len(selected) >= 6 {
				break
			}
		}
	}
	return truncateRunes(strings.Join(selected, "\n"), maxChars)
}

func candidateSentences(text string) []string {
	var out []string
	for _, paragraph := range splitParagraphs(text) {
		for _, sentence := range splitSentences(paragraph) {
			sentence = oneLine(sentence)
			if len([]rune(sentence)) < 40 {
				continue
			}
			out = append(out, sentence)
			if len(out) >= 32 {
				return out
			}
		}
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, truncateRunes(oneLine(text), 320))
	}
	return out
}

func rankSentences(sentences []string, query string) []string {
	type scored struct {
		text  string
		score int
		index int
	}
	terms := queryTerms(query)
	items := make([]scored, 0, len(sentences))
	for i, sentence := range sentences {
		items = append(items, scored{text: sentence, score: textScore(sentence, terms), index: i})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].index < items[j].index
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.text)
	}
	return out
}

func queryTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	terms := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, term := range fields {
		term = strings.Trim(term, `"'.,:;!?()[]{}<>`)
		if len([]rune(term)) < 3 || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func textScore(text string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(text)
	score := 0
	for _, term := range terms {
		score += strings.Count(lower, term)
	}
	return score
}

func splitParagraphs(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for _, paragraph := range raw {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph != "" {
			out = append(out, paragraph)
		}
	}
	return out
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func webTextBudget(opts webFetchOptions, fallbackChars, fallbackTokens, maxTokens int) (int, int) {
	if !opts.IncludeText && !opts.FullText {
		return 0, webInlineDefaultTokens
	}
	fallback := fallbackChars
	if opts.FullText && opts.MaxChars <= 0 && opts.MaxTokens <= 0 {
		fallback = webFullTextChars
	}
	charBudget := normalizedWebChars(opts.MaxChars, fallback)
	tokenBudget := normalizedWebTokens(opts.MaxTokens, fallbackTokens, min(maxTokens, webInlineMaxTokens))
	tokenChars := tokenBudget * 4
	if tokenChars > 0 && tokenChars < charBudget {
		charBudget = tokenChars
	}
	if charBudget > webHardTextChars {
		charBudget = webHardTextChars
	}
	return charBudget, estimateTokensByChars(charBudget)
}

func webTotalTextBudget(maxTotalTokens, pageCount int) (int, int) {
	if pageCount <= 0 {
		return 0, 0
	}
	tokens := normalizedWebTokens(maxTotalTokens, webCrawlDefaultTotalToks, min(webCrawlMaxTotalToks, webInlineMaxTokens))
	return tokens * 4, tokens
}

func normalizedWebChars(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value < 800 {
		value = 800
	}
	if value > webHardTextChars {
		value = webHardTextChars
	}
	return value
}

func normalizedWebTokens(value, fallback, maxTokens int) int {
	if value <= 0 {
		value = fallback
	}
	if value < 200 {
		value = 200
	}
	if value > maxTokens {
		value = maxTokens
	}
	return value
}

func compactWebText(text string, maxChars int, fullText bool) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return truncateRunesWithMarker(text, maxChars)
}

func webCompactNote(truncated, includeText, fullText bool) string {
	if !truncated {
		return ""
	}
	if !includeText {
		return "full extracted text is saved out-of-band in output_ref; inline response contains only digest/extract to protect context cost"
	}
	if fullText {
		return "full_text was requested, but output is still capped by max_tokens/max_chars to protect context cost"
	}
	return "inline text is capped; use output_ref if exact source text is required"
}

func webOutputClass(inlineText bool) string {
	if inlineText {
		return "raw_excerpt"
	}
	return "extractive_summary"
}

func tinyDirectWebAnswer(text string, links []string, truncated, includeText bool, originalTokens int) bool {
	if includeText || truncated || len(links) > 0 {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	return originalTokens > 0 && originalTokens <= webTinyDirectTokens && len([]rune(text)) <= webTinyDirectTokens*4
}

func webSummaryChars(summary string, keyPoints []string, extract string) int {
	return len([]rune(strings.Join(nonEmptyStrings(summary, strings.Join(keyPoints, "\n"), extract), "\n")))
}

func compactPageInlineText(page compactPage) string {
	return strings.Join(nonEmptyStrings(
		page.Title,
		page.Summary,
		strings.Join(page.KeyPoints, "\n"),
		page.Extract,
		page.Text,
	), "\n")
}

func compactCrawlPageInlineText(page compactCrawlPage) string {
	return strings.Join(nonEmptyStrings(
		page.Title,
		page.Summary,
		strings.Join(page.KeyPoints, "\n"),
		page.Extract,
		page.Text,
	), "\n")
}

func estimateTokens(text string) int {
	return estimateTokensByChars(len([]rune(text)))
}

func estimateTokensByChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func summarizeText(title, text string, maxChars int) string {
	text = oneLine(text)
	if text == "" {
		return strings.TrimSpace(title)
	}
	var parts []string
	if title = strings.TrimSpace(title); title != "" {
		parts = append(parts, title)
	}
	sentences := splitSentences(text)
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		candidate := strings.Join(append(append([]string{}, parts...), sentence), " — ")
		if len([]rune(candidate)) > maxChars {
			break
		}
		parts = append(parts, sentence)
		if len(parts) >= 4 {
			break
		}
	}
	if len(parts) == 0 {
		return truncate(text, maxChars)
	}
	return truncate(strings.Join(parts, " — "), maxChars)
}

func splitSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' && r != '。' && r != '…' {
			continue
		}
		if i+1 <= start {
			continue
		}
		out = append(out, strings.TrimSpace(text[start:i+len(string(r))]))
		start = i + len(string(r))
		if len(out) >= 8 {
			break
		}
	}
	if len(out) == 0 {
		return []string{truncate(text, 320)}
	}
	return out
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func truncateRunesWithMarker(text string, maxRunes int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text, false
	}
	if maxRunes < 32 {
		maxRunes = 32
	}
	return string(runes[:maxRunes]) + "\n...[truncated; call web_fetch with full_text=true only if exact full page text is required]", true
}

func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}
