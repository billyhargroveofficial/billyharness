package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

func TestWebSearchTavilyBackendForwardsQueryOptions(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("TEST_TAVILY_WEB_KEY", "tvly-secret-for-test")
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tvly-secret-for-test" {
			t.Fatalf("authorization = %q", got)
		}
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Fresh","url":"https://docs.example.com/fresh","content":"Useful evidence.","score":0.9,"published_date":"2026-07-02"}]}`))
	}))
	t.Cleanup(server.Close)
	cfg := config.Default()
	cfg.WebSearchBackend = "tavily"
	cfg.WebTavilyAPIKeyEnv = "TEST_TAVILY_WEB_KEY"
	registry := NewRegistry(cfg, WithWebBackendBaseURLs(server.URL, ""), WithWebBackendHTTPClient(server.Client()))

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "web_search",
		Arguments: rawArgs(map[string]any{
			"query":           "agent docs",
			"limit":           2,
			"freshness_days":  7,
			"include_domains": []string{"https://Docs.Example.com/path", "api.example.com"},
			"exclude_domains": []string{"spam.example.com"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload["query"] != "agent docs" || anyInt64(payload["max_results"]) != 2 ||
		payload["topic"] != "news" || anyInt64(payload["days"]) != 7 {
		t.Fatalf("search payload missing query/freshness options: %#v", payload)
	}
	if got := fmt.Sprint(payload["include_domains"]); got != "[docs.example.com api.example.com]" {
		t.Fatalf("include domains = %s payload=%#v", got, payload)
	}
	if got := fmt.Sprint(payload["exclude_domains"]); got != "[spam.example.com]" {
		t.Fatalf("exclude domains = %s payload=%#v", got, payload)
	}
	if result.Metadata["web_backend"] != "tavily" ||
		result.Metadata["web_freshness_days"] != 7 ||
		result.Metadata["web_freshness_requested"] != true ||
		result.Metadata["web_freshness_supported"] != true ||
		result.Metadata["web_freshness_enforced"] != true ||
		result.Metadata["web_domain_filter_requested"] != true ||
		result.Metadata["web_domain_filter_supported"] != true ||
		result.Metadata["web_domain_filter_enforcement"] != "provider" ||
		anyInt64(result.Metadata["web_result_count"]) != 1 ||
		anyInt64(result.Metadata["web_results_after_filter"]) != 1 ||
		!strings.Contains(result.Content, `"published_date": "2026-07-02"`) ||
		!strings.Contains(result.Content, `"content": "Useful evidence."`) {
		t.Fatalf("search result/metadata = %#v\n%s", result.Metadata, result.Content)
	}
}

func TestWebSearchBackendFailureFallsBackToNativeWithMetadata(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("TEST_EXA_WEB_KEY", "exa-secret-for-test")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(backend.Close)
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lite/" {
			t.Fatalf("native path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
			<a rel="nofollow" href="/l/?uddg=https%3A%2F%2Fdocs.example.com%2Fguide">Docs Result</a>
			<a rel="nofollow" href="/l/?uddg=https%3A%2F%2Fother.example.net%2Fskip">Other Result</a>
		`))
	}))
	t.Cleanup(native.Close)
	nativeAddr := native.Listener.Addr().String()
	oldDDGBase := duckDuckGoLiteBaseURL
	duckDuckGoLiteBaseURL = "http://lite.duckduckgo.com/lite/"
	t.Cleanup(func() { duckDuckGoLiteBaseURL = oldDDGBase })
	dialer := net.Dialer{}
	nativeClient := webtools.Client{
		Resolver: webtools.ResolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "lite.duckduckgo.com" {
				return nil, fmt.Errorf("unexpected host lookup %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, nativeAddr)
		},
	}
	cfg := config.Default()
	cfg.WebSearchBackend = "exa"
	cfg.WebExaAPIKeyEnv = "TEST_EXA_WEB_KEY"
	registry := NewRegistry(cfg,
		WithWebBackendBaseURLs("", backend.URL),
		WithWebBackendHTTPClient(backend.Client()),
		WithNativeWebClient(nativeClient),
	)

	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "web_search",
		Arguments: rawArgs(map[string]any{
			"query":           "agent docs",
			"limit":           5,
			"freshness_days":  3,
			"include_domains": []string{"docs.example.com"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata["web_backend"] != "native" ||
		result.Metadata["web_backend_attempted"] != "exa" ||
		result.Metadata["web_backend_failed"] != true ||
		result.Metadata["web_failover_policy"] != "configured_backend_then_native" ||
		result.Metadata["web_freshness_requested"] != true ||
		result.Metadata["web_freshness_supported"] != false ||
		result.Metadata["web_freshness_enforced"] != false ||
		result.Metadata["web_domain_filter_requested"] != true ||
		result.Metadata["web_domain_filter_supported"] != true ||
		result.Metadata["web_domain_filter_enforcement"] != "native_post_filter" ||
		anyInt64(result.Metadata["web_result_count"]) != 1 ||
		anyInt64(result.Metadata["web_results_before_filter"]) != 2 ||
		anyInt64(result.Metadata["web_results_after_filter"]) != 1 ||
		!strings.Contains(fmt.Sprint(result.Metadata["web_skipped_filters"]), "freshness_days") ||
		!strings.Contains(fmt.Sprint(result.Metadata["web_post_filtered_filters"]), "domains") {
		t.Fatalf("fallback metadata = %#v", result.Metadata)
	}
	if !strings.Contains(result.Content, "https://docs.example.com/guide") ||
		strings.Contains(result.Content, "other.example.net") {
		t.Fatalf("fallback content/domain filter = %s", result.Content)
	}
}

func TestCleanHTMLTextPreservesReadableTablesListsAndCode(t *testing.T) {
	text := cleanHTMLText(`
		<html><body>
		<table><tr><th>Name</th><th>Value</th></tr><tr><td>Alpha</td><td>42</td></tr></table>
		<ul><li>First item</li><li>Second item</li></ul>
		<pre><code>go test ./internal/tools</code></pre>
		</body></html>
	`)
	for _, want := range []string{"Name | Value |", "Alpha | 42 |", "First item\nSecond item", "go test ./internal/tools"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cleaned HTML missing %q:\n%s", want, text)
		}
	}
}
