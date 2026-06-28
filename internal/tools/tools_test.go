package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

func TestParseSearchResultsUnwrapsDuckDuckGoRedirects(t *testing.T) {
	body := `
		<a rel="nofollow" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fdoc&amp;rut=abc">Example &amp; Docs</a>
		<a href="https://direct.example/path">Direct</a>
		<a href="https://duckduckgo.com/about">Skip engine link</a>
	`
	results := parseSearchResults("https://lite.duckduckgo.com/lite/?q=x", body, 10)
	if len(results) != 2 {
		t.Fatalf("results len = %d: %#v", len(results), results)
	}
	if results[0].Title != "Example & Docs" || results[0].URL != "https://example.com/doc" {
		t.Fatalf("first result = %#v", results[0])
	}
	if results[1].URL != "https://direct.example/path" {
		t.Fatalf("second result = %#v", results[1])
	}
}

func TestWriteToolCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(root, "out.txt"), "content": "hello"}),
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file should not exist, stat err = %v", statErr)
	}
}

func TestToolArgumentsValidatedAgainstSchema(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	for _, tc := range []struct {
		name string
		call protocol.ToolCall
		want string
	}{
		{
			name: "missing required",
			call: protocol.ToolCall{Name: "fs_read_file", Arguments: rawArgs(map[string]any{})},
			want: `missing required property "path"`,
		},
		{
			name: "wrong type",
			call: protocol.ToolCall{Name: "fs_list", Arguments: rawArgs(map[string]any{"path": ".", "limit": "ten"})},
			want: "$.limit must be integer",
		},
		{
			name: "extra property",
			call: protocol.ToolCall{Name: "time_now", Arguments: rawArgs(map[string]any{"unused": true})},
			want: `unknown property "unused"`,
		},
		{
			name: "array min items",
			call: protocol.ToolCall{Name: "shell_exec", Arguments: rawArgs(map[string]any{"argv": []string{}})},
			want: "$.argv must contain at least 1 items",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := registry.Call(context.Background(), tc.call)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !result.IsError || result.ErrorCode != "validation_error" || !strings.Contains(result.Content, tc.want) {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestDangerousToolsCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)
	for _, call := range []protocol.ToolCall{
		{Name: "fs_write_file", Arguments: rawArgs(map[string]any{"path": "x.txt", "content": "x"})},
		{Name: "fs_make_dir", Arguments: rawArgs(map[string]any{"path": "dir"})},
		{Name: "shell_exec", Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "true"}})},
	} {
		if _, err := registry.Call(context.Background(), call); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("%s expected disabled error, got %v", call.Name, err)
		}
	}
}

func TestWriteToolEnabledByDefault(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	target := filepath.Join(root, "default-on.txt")
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": target, "content": "enabled"}),
	}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "enabled" {
		t.Fatalf("content = %q", bytes)
	}
}

func TestWriteToolEnabledCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	target := filepath.Join(root, "nested", "out.txt")
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": target, "content": "hello"}),
	}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello" {
		t.Fatalf("content = %q", bytes)
	}
}

func TestFSMakeDirEnabledAndRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_make_dir",
		Arguments: rawArgs(map[string]any{"path": "nested/dir"}),
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "nested", "dir")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%v err=%v", info, err)
	}
	if _, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_make_dir",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(t.TempDir(), "outside")}),
	}); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func TestReadToolRejectsSensitivePath(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(root, ".env")}),
	})
	if err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("expected sensitive path error, got %v", err)
	}
}

func TestReadToolRejectsPathOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": outside}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func TestReadToolReturnsFullContentForAgentManagedOutput(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("full-content-", 200)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MaxToolOutputBytes = 64
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": "big.txt"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != content {
		t.Fatalf("read content len=%d, want full len=%d", len(result.Content), len(content))
	}
}

func TestSafePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(link, "outside.txt")}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape read error, got %v", err)
	}
	_, err = registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": filepath.Join(link, "new.txt"), "content": "escape"}),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape write error, got %v", err)
	}
}

func TestRelativePathUsesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("from workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_read_file",
		Arguments: rawArgs(map[string]any{"path": "note.txt"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "from workspace" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestFSListLimitAndSearch(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("needle secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	registry := NewRegistry(cfg)

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_list",
		Arguments: rawArgs(map[string]any{"path": ".", "limit": 2}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "...[truncated at 2]") {
		t.Fatalf("list output = %q", list.Content)
	}

	search, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_search",
		Arguments: rawArgs(map[string]any{"query": "NEEDLE", "path": ".", "limit": 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search.Content, "a.txt") || strings.Contains(search.Content, ".env") {
		t.Fatalf("search output = %q", search.Content)
	}
}

func TestShellExecGateAndWorkspaceCWD(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = false
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "pwd"}}),
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled shell error, got %v", err)
	}

	cfg.AutoApproveDangerous = true
	registry = NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: rawArgs(map[string]any{"argv": []string{"sh", "-c", "pwd"}, "cwd": ".", "max_output_bytes": 4096}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.TrimSpace(result.Content), root) {
		t.Fatalf("pwd output = %q, want workspace root %q", result.Content, root)
	}
}

func TestWriteRejectsOversizedContent(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	registry := NewRegistry(cfg)

	_, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "fs_write_file",
		Arguments: rawArgs(map[string]any{"path": "big.txt", "content": strings.Repeat("x", maxWriteBytes+1)}),
	})
	if err == nil || !strings.Contains(err.Error(), "content too large") {
		t.Fatalf("expected content too large, got %v", err)
	}
}

func TestValidatePublicHTTPURLRejectsLocalAndPrivateTargets(t *testing.T) {
	for _, rawURL := range []string{
		"http://localhost:8080",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"file:///etc/passwd",
	} {
		if _, err := validatePublicHTTPURL(context.Background(), rawURL); err == nil {
			t.Fatalf("validatePublicHTTPURL(%q) returned nil error", rawURL)
		}
	}
}

func TestCompactFetchedPageLimitsDefaultOutput(t *testing.T) {
	text := strings.Repeat("Alpha beta gamma. ", 1000)
	page := fetchedPage{
		URL:         "https://example.com",
		Status:      200,
		ContentType: "text/plain",
		Title:       "Example",
		Text:        text,
		Links:       []string{"https://example.com/a", "https://example.com/b"},
	}
	out := compactFetchedPage(page, webFetchOptions{MaxChars: 1200, MaxLinks: 1})
	if out.OriginalTextChars <= 1200 {
		t.Fatalf("expected large original text, got %d", out.OriginalTextChars)
	}
	if !out.OutputTextTruncated || len([]rune(out.Text)) > 1400 {
		t.Fatalf("text was not compacted: truncated=%v len=%d", out.OutputTextTruncated, len([]rune(out.Text)))
	}
	if !strings.Contains(out.Summary, "Example") || len(out.Links) != 1 {
		t.Fatalf("compact page = %#v", out)
	}
}

func TestWebCompactionDefaultsStaySmall(t *testing.T) {
	page := fetchedPage{
		URL:   "https://example.com",
		Title: "Example",
		Text:  strings.Repeat("Alpha beta gamma delta. ", 2000),
	}
	fetched := compactFetchedPage(page, webFetchOptions{})
	if fetched.Text != "" || fetched.ReturnedTextChars != 0 || fetched.BudgetTextTokens != 0 {
		t.Fatalf("fetch default should not return raw text inline: %#v", fetched)
	}
	if fetched.Summary == "" || fetched.Extract == "" || len(fetched.KeyPoints) == 0 {
		t.Fatalf("fetch default should return digest/extract/key points: %#v", fetched)
	}
	if fetched.EstimatedTextTokens > 700 {
		t.Fatalf("fetch default returned too much: %#v", fetched)
	}

	crawled := compactCrawlPages([]crawlPage{{
		URL:   "https://example.com",
		Title: "Example",
		Text:  page.Text,
	}}, webFetchOptions{})
	if len(crawled) != 1 {
		t.Fatalf("crawl pages = %d", len(crawled))
	}
	if crawled[0].Text != "" || crawled[0].ReturnedTextChars != 0 || crawled[0].BudgetTextTokens != 0 {
		t.Fatalf("crawl default should not return raw text inline: %#v", crawled[0])
	}
	if crawled[0].Summary == "" || crawled[0].Extract == "" || len(crawled[0].KeyPoints) == 0 {
		t.Fatalf("crawl default should return digest/extract/key points: %#v", crawled[0])
	}
	if crawled[0].EstimatedTextTokens > 700 {
		t.Fatalf("crawl default returned too much: %#v", crawled[0])
	}
}

func TestTinyDirectWebAnswerAvoidsSummaryBloatAndModelSummarizer(t *testing.T) {
	oldProvider := newWebSummaryProvider
	newWebSummaryProvider = func(config.Config) (provider.Provider, error) {
		t.Fatalf("tiny direct answer should not call model summarizer")
		return scriptedProvider{}, nil
	}
	t.Cleanup(func() { newWebSummaryProvider = oldProvider })

	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	registry := NewRegistry(cfg)
	page := fetchedPage{
		URL:             "https://wttr.in/Moscow?format=3",
		Status:          200,
		ContentType:     "text/plain",
		Text:            "Moscow: +19C, partly cloudy",
		RawBytesFetched: 28,
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	if compact.OutputClass != "tiny_direct_answer" || compact.SummaryMode != "direct" ||
		compact.SummarizerModel != "direct" || compact.Text != page.Text || compact.Summary != page.Text {
		t.Fatalf("tiny direct compact = %#v", compact)
	}
	if compact.OutputTextTruncated || compact.EstimatedTextTokens != compact.OriginalTextTokens || compact.EstimatedTokensSaved != 0 {
		t.Fatalf("tiny direct metrics = %#v", compact)
	}
	meta := webPageMetadata(compact)
	if meta["output_class"] != "tiny_direct_answer" || meta["summary_mode"] != "direct" ||
		anyInt64(meta["tool_summary_api_total_tokens"]) != 0 {
		t.Fatalf("tiny direct metadata = %#v", meta)
	}

	crawl := compactCrawlPages([]crawlPage{{
		URL:             "https://wttr.in/Moscow?format=3",
		Depth:           0,
		Text:            page.Text,
		RawBytesFetched: page.RawBytesFetched,
		MaxBytes:        page.MaxBytes,
	}}, webFetchOptions{})
	if len(crawl) != 1 || crawl[0].OutputClass != "tiny_direct_answer" || crawl[0].Text != page.Text {
		t.Fatalf("tiny crawl page = %#v", crawl)
	}
}

func TestCompactFetchedPageHonorsTokenBudgetEvenForFullText(t *testing.T) {
	text := strings.Repeat("Alpha beta gamma. ", 2000)
	page := fetchedPage{
		URL:         "https://example.com",
		Status:      200,
		ContentType: "text/plain",
		Title:       "Example",
		Text:        text,
	}
	out := compactFetchedPage(page, webFetchOptions{FullText: true, MaxTokens: 200})
	if out.BudgetTextTokens != 200 || out.BudgetTextChars != 800 {
		t.Fatalf("budget = %d tokens / %d chars, want 200 / 800", out.BudgetTextTokens, out.BudgetTextChars)
	}
	if !out.OutputTextTruncated || !strings.Contains(out.CompactNote, "full_text") {
		t.Fatalf("full_text should still be capped: %#v", out)
	}
	if out.ReturnedTextChars > 1000 || out.EstimatedTextTokens > 800 {
		t.Fatalf("returned too much text: chars=%d tokens=%d", out.ReturnedTextChars, out.EstimatedTextTokens)
	}
}

func TestCompactCrawlPagesHonorsTotalTokenBudget(t *testing.T) {
	pages := []crawlPage{
		{URL: "https://example.com/a", Depth: 0, Title: "A", Text: strings.Repeat("A page sentence. ", 800)},
		{URL: "https://example.com/b", Depth: 1, Title: "B", Text: strings.Repeat("B page sentence. ", 800)},
		{URL: "https://example.com/c", Depth: 1, Title: "C", Text: strings.Repeat("C page sentence. ", 800)},
	}
	out := compactCrawlPages(pages, webFetchOptions{IncludeText: true, MaxTokens: 2000, MaxTotalTokens: 900})
	if len(out) != len(pages) {
		t.Fatalf("pages = %d, want %d", len(out), len(pages))
	}
	totalBudgetChars := 0
	for _, page := range out {
		totalBudgetChars += page.BudgetTextChars
		if page.BudgetTextChars != 1200 {
			t.Fatalf("per-page budget = %d, want 1200: %#v", page.BudgetTextChars, page)
		}
		if !page.OutputTextTruncated || page.EstimatedTextTokens > 900 {
			t.Fatalf("page was not compacted enough: %#v", page)
		}
	}
	if totalBudgetChars != 3600 {
		t.Fatalf("total budget chars = %d, want 3600", totalBudgetChars)
	}
}

func TestWebCompactionStoresFullTextOutOfBand(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	text := strings.Repeat("Full page sentence with important evidence. ", 300) + "TAIL_UNIQUE_EVIDENCE_THAT_SHOULD_ONLY_BE_IN_REF"
	page := fetchedPage{
		URL:             "https://example.com/article",
		Status:          200,
		ContentType:     "text/html",
		Title:           "Important Article",
		Text:            text,
		Links:           []string{"https://example.com/source"},
		RawBytesFetched: 12345,
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	ref, err := storeWebOutput("web_fetch", page.URL, renderFetchedPageArtifact(page))
	if err != nil {
		t.Fatal(err)
	}
	compact.OutputRef = ref
	out, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "TAIL_UNIQUE_EVIDENCE_THAT_SHOULD_ONLY_BE_IN_REF") {
		t.Fatalf("compact output leaked tail raw text: %s", string(out))
	}
	if compact.Text != "" || compact.OutputRef == "" {
		t.Fatalf("compact page should omit raw text and include output ref: %#v", compact)
	}
	if compact.OutputClass != "extractive_summary" || compact.SummaryMode != "extractive" ||
		compact.SummaryChars <= 0 || compact.RawBytesFetched != 12345 || compact.MaxBytes != 65536 || compact.EstimatedTokensSaved <= 0 {
		t.Fatalf("compact output contract missing metrics: %#v", compact)
	}
	meta := webPageMetadata(compact)
	if meta["tool_summary_kind"] != "extractive" ||
		anyInt64(meta["tool_summary_api_total_tokens"]) != 0 ||
		meta["tool_summary_external_model_used"] != false {
		t.Fatalf("summary metadata missing extractive zero-cost fields: %#v", meta)
	}
	if meta["output_class"] != "extractive_summary" || meta["summary_mode"] != "extractive" ||
		meta["summary_chars"].(int) <= 0 ||
		meta["summarizer_provider"] != "native" || meta["summarizer_model"] != "extractive" ||
		meta["raw_bytes_fetched"] != 12345 || meta["max_bytes"] != 65536 ||
		meta["estimated_tokens_saved"].(int) <= 0 {
		t.Fatalf("metadata missing web output contract metrics: %#v", meta)
	}
	if anyInt64(meta["tool_summary_input_tokens"]) <= anyInt64(meta["tool_summary_output_tokens"]) ||
		anyInt64(meta["tool_summary_saved_tokens"]) <= 0 {
		t.Fatalf("summary token metadata should show compression: %#v", meta)
	}
	bytes, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), text[:200]) {
		t.Fatalf("artifact missing full extracted text")
	}
	assertMode(t, filepath.Join(config.BillyHomeDir(), "tool-output"), 0o700)
	assertMode(t, filepath.Dir(ref), 0o700)
	assertMode(t, ref, 0o600)
}

func TestWebIncludeTextMarksRawExcerptOutputClass(t *testing.T) {
	page := fetchedPage{
		URL:             "https://example.com/article",
		Status:          200,
		ContentType:     "text/plain",
		Text:            strings.Repeat("short raw excerpt sentence. ", 200),
		RawBytesFetched: 5600,
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{IncludeText: true, MaxTokens: 300})
	if compact.OutputClass != "raw_excerpt" || compact.SummaryMode != "extractive" || compact.SummaryChars <= 0 {
		t.Fatalf("compact output contract = %#v", compact)
	}
	if compact.Text == "" || compact.ReturnedTextChars == 0 || compact.BudgetTextTokens != 300 {
		t.Fatalf("include_text should return capped text with metrics: %#v", compact)
	}
}

func TestWebPageMetadataIncludesPhaseTimings(t *testing.T) {
	page := compactPage{
		URL:              "https://example.com/timing",
		OutputClass:      "extractive_summary",
		SummaryMode:      "extractive",
		WebCacheLookupMS: 1,
		WebHTTPFetchMS:   2,
		WebCompactMS:     3,
		WebSummaryMS:     4,
		WebOutputRefMS:   5,
		WebCacheSaveMS:   6,
		WebTotalMS:       21,
	}
	meta := webPageMetadata(page)
	for key, want := range map[string]int64{
		"web_cache_lookup_ms": 1,
		"web_http_fetch_ms":   2,
		"web_compact_ms":      3,
		"web_summary_ms":      4,
		"web_output_ref_ms":   5,
		"web_cache_save_ms":   6,
		"web_total_ms":        21,
	} {
		if got := anyInt64(meta[key]); got != want {
			t.Fatalf("%s = %d, want %d in %#v", key, got, want, meta)
		}
	}

	page.resetWebPhaseTimings()
	for _, got := range []int64{
		page.WebCacheLookupMS,
		page.WebHTTPFetchMS,
		page.WebCompactMS,
		page.WebSummaryMS,
		page.WebOutputRefMS,
		page.WebCacheSaveMS,
		page.WebTotalMS,
	} {
		if got != 0 {
			t.Fatalf("reset should clear web phase timings: %#v", page)
		}
	}
}

func TestModelWebSummarizerRunsOutsideMainLoopAndRecordsMetrics(t *testing.T) {
	oldProvider := newWebSummaryProvider
	newWebSummaryProvider = func(cfg config.Config) (provider.Provider, error) {
		if cfg.Provider != "mock" || cfg.Model != "mock-summarizer" || cfg.Thinking != "disabled" {
			t.Fatalf("summary cfg = provider:%q model:%q thinking:%q", cfg.Provider, cfg.Model, cfg.Thinking)
		}
		return scriptedProvider{
			content: "Model summary keeps the important facts and leaves the raw tail out.",
			usage:   provider.Usage{InputTokens: 900, OutputTokens: 40, CacheHitTokens: 300, CacheMissTokens: 600},
		}, nil
	}
	t.Cleanup(func() { newWebSummaryProvider = oldProvider })

	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	cfg.WebSummaryMaxInputTokens = 300
	cfg.WebSummaryMaxOutputTokens = 80
	registry := NewRegistry(cfg)
	raw := strings.Repeat("Raw page evidence should stay outside context. ", 200) + "RAW_TAIL_ONLY_IN_REF"
	page := fetchedPage{
		URL:             "https://example.com/model",
		Status:          200,
		ContentType:     "text/html",
		Title:           "Model Summary",
		Text:            raw,
		RawBytesFetched: len(raw),
		MaxBytes:        65536,
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	out, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "RAW_TAIL_ONLY_IN_REF") {
		t.Fatalf("model summary compact output leaked raw tail: %s", string(out))
	}
	if compact.OutputClass != "model_summary" || compact.SummaryMode != "model" ||
		compact.SummarizerProvider != "mock" || compact.SummarizerModel != "mock-summarizer" ||
		compact.WebsumInputTokens != 900 || compact.WebsumOutputTokens != 40 || compact.WebsumCacheHit != 300 ||
		compact.WebsumModel != "mock-summarizer" || compact.WebsumError != "" {
		t.Fatalf("model summary compact = %#v", compact)
	}
	meta := webPageMetadata(compact)
	if meta["tool_summary_kind"] != "model" || meta["tool_summary_external_model_used"] != true ||
		anyInt64(meta["tool_summary_api_input_tokens"]) != 900 ||
		anyInt64(meta["tool_summary_api_output_tokens"]) != 40 ||
		anyInt64(meta["tool_summary_api_total_tokens"]) != 940 ||
		meta["websum_model"] != "mock-summarizer" {
		t.Fatalf("model summary metadata = %#v", meta)
	}
}

func TestModelWebSummarizerFallsBackToExtractiveOnProviderError(t *testing.T) {
	oldProvider := newWebSummaryProvider
	newWebSummaryProvider = func(config.Config) (provider.Provider, error) {
		return failingProvider{}, nil
	}
	t.Cleanup(func() { newWebSummaryProvider = oldProvider })

	cfg := config.Default()
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	registry := NewRegistry(cfg)
	page := fetchedPage{
		URL:   "https://example.com/fallback",
		Title: "Fallback",
		Text:  strings.Repeat("Fallback page sentence with useful details. ", 100),
	}
	compact := compactFetchedPage(page, webFetchOptions{})
	originalSummary := compact.Summary
	registry.applyModelSummaryToPage(context.Background(), &compact, page, webFetchOptions{})
	if compact.Summary != originalSummary || compact.SummaryMode != "extractive" || compact.OutputClass != "extractive_summary" {
		t.Fatalf("fallback should keep extractive compact output: %#v", compact)
	}
	if !strings.Contains(compact.WebsumError, "summary failed") {
		t.Fatalf("fallback missing error: %#v", compact)
	}
}

func TestWebCacheStoresAndClearsCompactOutputs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	cfg := config.Default()
	cfg.WebCacheEnabled = true
	cfg.WebCacheTTL = time.Hour
	cfg.WebCacheMaxBytes = 1 << 20
	registry := NewRegistry(cfg)
	ctx := context.Background()
	opts := webFetchOptions{Query: "weather", MaxBytes: 65536}
	key, ok := registry.webCacheKey(ctx, "web_fetch", "http://93.184.216.34/article", opts, nil)
	if !ok || key == "" {
		t.Fatalf("web cache key missing")
	}
	otherQuery, _ := registry.webCacheKey(ctx, "web_fetch", "http://93.184.216.34/article", webFetchOptions{Query: "news", MaxBytes: 65536}, nil)
	if otherQuery == key {
		t.Fatalf("web cache key should include query")
	}
	cfg.WebSummaryMode = "model"
	cfg.WebSummaryProvider = "mock"
	cfg.WebSummaryModel = "mock-summarizer"
	modelRegistry := NewRegistry(cfg)
	modelKey, _ := modelRegistry.webCacheKey(ctx, "web_fetch", "http://93.184.216.34/article", opts, nil)
	if modelKey == key {
		t.Fatalf("web cache key should include summarizer config")
	}

	page := compactPage{
		URL:                 "http://93.184.216.34/article",
		OutputClass:         "extractive_summary",
		SummaryMode:         "extractive",
		Summary:             "cached summary",
		EstimatedTextTokens: 17,
	}
	if err := registry.saveWebPageCache(key, page); err != nil {
		t.Fatal(err)
	}
	cached, hit := registry.loadWebPageCache(key)
	if !hit || !cached.WebCacheHit || cached.WebCacheKey != key || cached.WebCacheTTLMS != int64(time.Hour/time.Millisecond) {
		t.Fatalf("cached page = %#v hit=%v", cached, hit)
	}

	crawlKey, ok := registry.webCacheKey(ctx, "web_crawl", "http://93.184.216.34/", webFetchOptions{}, map[string]any{"max_pages": 1})
	if !ok {
		t.Fatalf("crawl cache key missing")
	}
	crawl := compactCrawlOutput{
		Pages:       []compactCrawlPage{{URL: "http://93.184.216.34/", Summary: "cached crawl"}},
		OutputClass: "extractive_summary",
		SummaryMode: "extractive",
	}
	if err := registry.saveWebCrawlCache(crawlKey, crawl); err != nil {
		t.Fatal(err)
	}
	cachedCrawl, hit := registry.loadWebCrawlCache(crawlKey)
	if !hit || !cachedCrawl.WebCacheHit || len(cachedCrawl.Pages) != 1 || !cachedCrawl.Pages[0].WebCacheHit {
		t.Fatalf("cached crawl = %#v hit=%v", cachedCrawl, hit)
	}

	status, err := registry.Call(ctx, protocol.ToolCall{Name: "web_cache_status", Arguments: rawArgs(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"enabled": true`, `"entries":`} {
		if !strings.Contains(status.Content, want) {
			t.Fatalf("status missing %q in:\n%s", want, status.Content)
		}
	}
	cleared, err := registry.Call(ctx, protocol.ToolCall{Name: "web_cache_clear", Arguments: rawArgs(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cleared.Content, `"removed_entries":`) || anyInt64(cleared.Metadata["web_cache_removed_entries"]) == 0 {
		t.Fatalf("clear result = %#v content=\n%s", cleared, cleared.Content)
	}
	after := registry.webCacheStatus()
	if after.Entries != 0 {
		t.Fatalf("cache should be empty after clear: %#v", after)
	}
}

func TestCompactCrawlResultReturnsSingleOutputRef(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	pages := []crawlPage{
		{URL: "https://example.com/a", Depth: 0, Title: "A", Text: strings.Repeat("A page sentence. ", 400), RawBytesFetched: 7000, MaxBytes: 65536},
		{URL: "https://example.com/b", Depth: 1, Title: "B", Text: strings.Repeat("B page sentence. ", 400), RawBytesFetched: 8000, MaxBytes: 65536},
	}
	registry := NewRegistry(config.Default())
	out, ref, err := registry.compactCrawlResult(context.Background(), pages, webFetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ref == "" || out.OutputRef != ref || len(out.Pages) != 2 {
		t.Fatalf("crawl compact output/ref = %#v ref=%q", out, ref)
	}
	if out.OutputClass != "extractive_summary" || out.SummaryMode != "extractive" ||
		out.SummaryChars <= 0 || out.RawBytesFetched != 15000 || out.MaxBytesPerPage != 65536 || out.EstimatedTokensSaved <= 0 {
		t.Fatalf("crawl output contract missing metrics: %#v", out)
	}
	meta := crawlMetadata(out)
	if meta["output_class"] != "extractive_summary" || meta["summary_mode"] != "extractive" ||
		meta["summary_chars"].(int) <= 0 ||
		meta["summarizer_provider"] != "native" || meta["summarizer_model"] != "extractive" ||
		meta["raw_bytes_fetched"] != 15000 || meta["max_bytes_per_page"] != 65536 ||
		meta["estimated_tokens_saved"].(int) <= 0 {
		t.Fatalf("crawl metadata missing web output contract metrics: %#v", meta)
	}
	for _, page := range out.Pages {
		if page.Text != "" || page.OutputRef != ref || page.Extract == "" {
			t.Fatalf("page should be compact with shared ref: %#v", page)
		}
	}
	bytes, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), "=== page 2") || !strings.Contains(string(bytes), "B page sentence") {
		t.Fatalf("crawl artifact missing page text:\n%s", string(bytes))
	}
}

func TestLazyMCPGatewayHidesRawSpecsAndCanCallTool(t *testing.T) {
	registry := NewRegistry(config.Default())
	registry.mcpTools["mcp__fake__echo"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__fake__echo",
			Description: "MCP fake/echo. Echo text",
			Parameters:  raw(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			return Result{Content: in.Text}, nil
		},
	}
	registry.mcpTools["mcp__telegram_parilka__read_history"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__telegram_parilka__read_history",
			Description: "Read messages from the local SQLite cache.",
			Parameters:  raw(`{"type":"object","properties":{"limit":{"type":"integer"}}}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: "history"}, nil
		},
	}
	registry.addMCPGateway()

	for _, spec := range registry.Specs() {
		if spec.Name == "mcp__fake__echo" {
			t.Fatalf("raw MCP tool leaked into model specs: %#v", registry.Specs())
		}
	}
	if !hasSpec(registry.Specs(), "mcp_list_tools") || !hasSpec(registry.Specs(), "mcp_call") || !hasSpec(registry.Specs(), "tool_search") {
		t.Fatalf("lazy MCP tools missing: %#v", registry.Specs())
	}

	direct, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp__fake__echo",
		Arguments: rawArgs(map[string]any{"text": "bypass"}),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("raw MCP tool should not be directly callable, got result=%#v err=%v", direct, err)
	}
	if direct.ErrorCode != "unknown_tool" {
		t.Fatalf("direct raw MCP call result = %#v", direct)
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"query": "echo"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "mcp__fake__echo") || strings.Contains(list.Content, "input_schema") {
		t.Fatalf("unexpected list output: %s", list.Content)
	}

	parilkaByServer, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "telegram-parilka"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parilkaByServer.Content, "mcp__telegram_parilka__read_history") ||
		!strings.Contains(parilkaByServer.Content, `"server": "telegram-parilka"`) {
		t.Fatalf("telegram-parilka filter failed: %s", parilkaByServer.Content)
	}

	parilkaByRussianAlias, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"query": "парилка"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parilkaByRussianAlias.Content, "mcp__telegram_parilka__read_history") {
		t.Fatalf("russian parilka alias failed: %s", parilkaByRussianAlias.Content)
	}

	called, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "ok"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if called.Content != "ok" {
		t.Fatalf("mcp_call result = %q", called.Content)
	}

	rejected, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "ok", "extra": true},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), `unknown property "extra"`) {
		t.Fatalf("expected target schema validation error, got result=%#v err=%v", rejected, err)
	}
	if !rejected.IsError || rejected.ErrorCode != "validation_error" {
		t.Fatalf("expected validation error result, got %#v", rejected)
	}

	nullArgs, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__telegram_parilka__read_history",
			"arguments": nil,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if nullArgs.Content != "history" {
		t.Fatalf("null mcp_call arguments result = %q", nullArgs.Content)
	}
}

func TestRegistryExposesExplicitParallelMetadata(t *testing.T) {
	registry := NewRegistry(config.Config{})
	registry.mcpTools["mcp__fake__echo"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__fake__echo",
			Description: "Echo.",
			Parameters:  raw(`{"type":"object","properties":{}}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: "ok"}, nil
		},
	}
	registry.addMCPGateway()
	readMeta, ok := registry.ParallelMetadata("fs_read_file")
	if !ok || readMeta.Policy != ParallelPolicyReadOnly || !readMeta.Idempotent || readMeta.RequiresExclusiveWorkspace || !readMeta.CanRunParallel() {
		t.Fatalf("read metadata = %#v ok=%v", readMeta, ok)
	}
	webMeta, ok := registry.ParallelMetadata("web_fetch")
	if !ok || webMeta.Policy != ParallelPolicyNetworkRateLimited || webMeta.RateLimitKey != "web" || webMeta.MaxConcurrency != 3 || !webMeta.CanRunParallel() {
		t.Fatalf("web metadata = %#v ok=%v", webMeta, ok)
	}
	writeMeta, ok := registry.ParallelMetadata("fs_write_file")
	if !ok || writeMeta.Policy != ParallelPolicyExclusiveWorkspace || !writeMeta.RequiresExclusiveWorkspace || writeMeta.CanRunParallel() {
		t.Fatalf("write metadata = %#v ok=%v", writeMeta, ok)
	}
	mcpMeta, ok := registry.ParallelMetadata("mcp_call")
	if !ok || mcpMeta.Policy != ParallelPolicyUnknownExternal || !mcpMeta.RequiresExclusiveWorkspace || mcpMeta.CanRunParallel() {
		t.Fatalf("mcp metadata = %#v ok=%v", mcpMeta, ok)
	}
}

func TestToolSearchFindsNativeAndMCPTools(t *testing.T) {
	registry := NewRegistry(config.Default())
	registry.mcpTools["mcp__github__search_repositories"] = Tool{
		Spec: protocol.ToolSpec{
			Name:        "mcp__github__search_repositories",
			Description: "Search GitHub repositories by query.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskExternal,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: "ok"}, nil
		},
	}
	registry.addMCPGateway()

	native, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "tool_search",
		Arguments: rawArgs(map[string]any{"query": "read file", "limit": 5}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var nativeResp struct {
		Tools []struct {
			Name      string `json:"name"`
			Source    string `json:"source"`
			Namespace string `json:"namespace"`
			Risk      string `json:"risk"`
			CallTool  string `json:"call_tool"`
		} `json:"tools"`
		Metrics struct {
			DiscoveryCalls int `json:"discovery_calls"`
			Returned       int `json:"returned"`
			ScannedNative  int `json:"scanned_native"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(native.Content), &nativeResp); err != nil {
		t.Fatalf("native tool_search json = %v\n%s", err, native.Content)
	}
	for _, want := range []string{
		`"name": "fs_read_file"`,
		`"source": "native"`,
		`"namespace": "fs"`,
		`"risk": "read_only"`,
		`"call_tool": "fs_read_file"`,
		`"metrics"`,
	} {
		if !strings.Contains(native.Content, want) {
			t.Fatalf("native tool_search missing %q in:\n%s", want, native.Content)
		}
	}
	if nativeResp.Metrics.DiscoveryCalls != 1 || nativeResp.Metrics.Returned == 0 || nativeResp.Metrics.ScannedNative == 0 {
		t.Fatalf("native metrics = %#v", nativeResp.Metrics)
	}

	filteredNative, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":     "file",
			"namespace": "fs",
			"risk":      "read_only",
			"limit":     10,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filteredNative.Content, `"name": "fs_read_file"`) ||
		strings.Contains(filteredNative.Content, `"name": "web_fetch"`) {
		t.Fatalf("native namespace/risk filter failed:\n%s", filteredNative.Content)
	}

	mcp, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":             "repositories",
			"server":            "github",
			"namespace":         "mcp.github",
			"risk":              "external",
			"include_schema":    true,
			"max_schema_tokens": 200,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var mcpResp struct {
		Tools []struct {
			Name        string          `json:"name"`
			Source      string          `json:"source"`
			Namespace   string          `json:"namespace"`
			Server      string          `json:"server"`
			Risk        string          `json:"risk"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		Metrics struct {
			DiscoveryCalls     int `json:"discovery_calls"`
			Returned           int `json:"returned"`
			SchemaIncluded     int `json:"schema_included"`
			SchemaTokens       int `json:"schema_tokens"`
			SchemaBudgetTokens int `json:"schema_budget_tokens"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(mcp.Content), &mcpResp); err != nil {
		t.Fatalf("mcp tool_search json = %v\n%s", err, mcp.Content)
	}
	for _, want := range []string{
		`"name": "mcp__github__search_repositories"`,
		`"source": "mcp"`,
		`"namespace": "mcp.github"`,
		`"server": "github"`,
		`"risk": "external"`,
		`"call_tool": "mcp_call"`,
		`"call_name": "mcp__github__search_repositories"`,
		`"input_schema"`,
	} {
		if !strings.Contains(mcp.Content, want) {
			t.Fatalf("mcp tool_search missing %q in:\n%s", want, mcp.Content)
		}
	}
	if mcpResp.Metrics.DiscoveryCalls != 1 || mcpResp.Metrics.Returned != 1 ||
		mcpResp.Metrics.SchemaIncluded != 1 || mcpResp.Metrics.SchemaTokens == 0 ||
		mcpResp.Metrics.SchemaBudgetTokens != 200 {
		t.Fatalf("mcp metrics = %#v", mcpResp.Metrics)
	}

	overBudget, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "tool_search",
		Arguments: rawArgs(map[string]any{
			"query":             "repositories",
			"server":            "github",
			"include_schema":    true,
			"max_schema_tokens": 1,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overBudget.Content, `"schema_omitted"`) ||
		!strings.Contains(overBudget.Content, `"schema_truncated": true`) ||
		!strings.Contains(overBudget.Content, `"truncated": true`) {
		t.Fatalf("schema budget omission missing:\n%s", overBudget.Content)
	}
}

func TestSkillsListAndReadAreOnDemandBoundedAndCompatOptional(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	writeSkill(t, filepath.Join(home, "skills", "teacher", "SKILL.md"), "# Teacher\nHome skill body")
	writeSkill(t, filepath.Join(project, ".billyharness", "skills", "review", "SKILL.md"), "# Review\nProject skill body with more text")
	writeSkill(t, filepath.Join(project, ".claude", "skills", "legacy", "SKILL.md"), "# Legacy\nClaude compat body")

	cfg := config.Default()
	cfg.WorkspaceRoots = []string{project}
	registry := NewRegistry(cfg)
	if !hasSpec(registry.Specs(), "skill_list") || !hasSpec(registry.Specs(), "skill_read") {
		t.Fatalf("skill tools missing from specs: %#v", registry.Specs())
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "skill_list",
		Arguments: rawArgs(map[string]any{"limit": 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name": "teacher"`, `"source": "home"`, `"name": "review"`, `"source": "project"`, `"content_injected": false`} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("skill_list missing %q in:\n%s", want, list.Content)
		}
	}
	if strings.Contains(list.Content, `"name": "legacy"`) {
		t.Fatalf("compat skill leaked without include_compat:\n%s", list.Content)
	}

	compat, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "skill_list",
		Arguments: rawArgs(map[string]any{"include_compat": true, "query": "legacy"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compat.Content, `"name": "legacy"`) || !strings.Contains(compat.Content, `"source": "claude_compat"`) {
		t.Fatalf("compat skill missing:\n%s", compat.Content)
	}

	read, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "skill_read",
		Arguments: rawArgs(map[string]any{
			"name":      "review",
			"source":    "project",
			"max_chars": 8,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !read.Truncated || read.Metadata["skill_name"] != "review" || read.Metadata["skill_source"] != "project" ||
		!strings.Contains(read.Content, `...[truncated]`) {
		t.Fatalf("bounded skill_read result=%#v content=\n%s", read, read.Content)
	}

	legacy, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "skill_read",
		Arguments: rawArgs(map[string]any{
			"name":           "legacy",
			"include_compat": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(legacy.Content, "Claude compat body") {
		t.Fatalf("legacy skill read = %s", legacy.Content)
	}
}

func TestMCPGatewayListsServerStatusesAndValidatesStdioCalls(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1"},
		CWD:            root,
		Enabled:        true,
		Required:       true,
		StartupTimeout: 5 * time.Second,
		ToolTimeout:    5 * time.Second,
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_list_tools",
		Arguments: rawArgs(map[string]any{
			"server":         "fake",
			"include_schema": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"servers"`,
		`"name": "fake"`,
		`"connected": true`,
		`"state": "connected"`,
		`"tool_count": 1`,
		`"mcp__fake__echo"`,
		`"namespace": "mcp.fake"`,
		`"risk": "external"`,
		`"input_schema"`,
		`"metrics"`,
		`"schema_included": 1`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing %q in:\n%s", want, list.Content)
		}
	}

	invalid, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"extra": "nope"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), `missing required property "text"`) {
		t.Fatalf("expected target schema validation error, got result=%#v err=%v", invalid, err)
	}
	if !invalid.IsError || invalid.ErrorCode != "validation_error" {
		t.Fatalf("expected validation error result, got %#v", invalid)
	}

	valid, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "hello"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if valid.Content != "hello" {
		t.Fatalf("mcp_call content = %q", valid.Content)
	}
}

func TestMCPGatewayReconnectsCrashedStdioServer(t *testing.T) {
	root := t.TempDir()
	phaseFile := filepath.Join(root, "tools-reconnect.phase")
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServer{{
		Name:           "fake",
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestToolsFakeStdioMCPServer"},
		Env:            map[string]string{"BILLYHARNESS_TOOLS_MCP_HELPER": "1", "BILLYHARNESS_TOOLS_MCP_MODE": "close_once_then_echo", "BILLYHARNESS_TOOLS_MCP_PHASE_FILE": phaseFile},
		CWD:            root,
		Enabled:        true,
		Required:       true,
		StartupTimeout: 2 * time.Second,
		ToolTimeout:    2 * time.Second,
	}}
	registry, err := NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	first, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "first"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("first mcp_call result=%#v err=%v", first, err)
	}

	second, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "mcp_call",
		Arguments: rawArgs(map[string]any{
			"name":      "mcp__fake__echo",
			"arguments": map[string]any{"text": "second"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "second" {
		t.Fatalf("second mcp_call content = %q", second.Content)
	}

	list, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "mcp_list_tools",
		Arguments: rawArgs(map[string]any{"server": "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"state": "reconnected"`,
		`"retry_count": 1`,
		`"restart_count": 1`,
		`"connected": true`,
	} {
		if !strings.Contains(list.Content, want) {
			t.Fatalf("mcp_list_tools missing %q in:\n%s", want, list.Content)
		}
	}
}

func TestToolsFakeStdioMCPServer(t *testing.T) {
	if os.Getenv("BILLYHARNESS_TOOLS_MCP_HELPER") != "1" {
		return
	}
	mode := os.Getenv("BILLYHARNESS_TOOLS_MCP_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0.0"},
				"instructions":    "Use echo for MCP gateway tests.",
			}})
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo text",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false},
			}}}})
		case "tools/call":
			var call struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &call)
			if call.Name != "echo" {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				continue
			}
			if mode == "close_once_then_echo" && !toolsMCPPhaseExists() {
				writeToolsMCPPhase()
				os.Exit(0)
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprint(call.Arguments["text"])}},
				"isError": false,
			}})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}

func toolsMCPPhaseExists() bool {
	path := os.Getenv("BILLYHARNESS_TOOLS_MCP_PHASE_FILE")
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeToolsMCPPhase() {
	path := os.Getenv("BILLYHARNESS_TOOLS_MCP_PHASE_FILE")
	if path != "" {
		_ = os.WriteFile(path, []byte("closed"), 0o600)
	}
}

func hasSpec(specs []protocol.ToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rawArgs(value any) json.RawMessage {
	bytes, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return bytes
}

type scriptedProvider struct {
	content string
	usage   provider.Usage
}

func (p scriptedProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, 3)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		select {
		case events <- provider.Event{Kind: provider.EventContent, Text: p.content}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		if p.usage.InputTokens > 0 || p.usage.OutputTokens > 0 {
			select {
			case events <- provider.Event{Kind: provider.EventUsage, Usage: p.usage}:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		select {
		case events <- provider.Event{Kind: provider.EventDone}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
	}()
	return events, errs
}

type failingProvider struct{}

func (failingProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		select {
		case errs <- fmt.Errorf("summary failed"):
		case <-ctx.Done():
			errs <- ctx.Err()
		}
	}()
	return events, errs
}

func anyInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
