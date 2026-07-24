package webtools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientRejectsPublicThenPrivateRebinding(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("10.0.0.5"),
	}}
	dialer := &scriptedDialer{responses: []string{okResponse("should-not-run")}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	_, err := client.Get(context.Background(), "http://example.test/", 1024)
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected non-public rebinding error, got %v", err)
	}
	if got := dialer.CallCount(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
}

func TestClientRejectsPublicThenCGNATRebinding(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("100.100.100.200"),
	}}
	dialer := &scriptedDialer{responses: []string{okResponse("should-not-run")}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	_, err := client.Get(context.Background(), "http://example.test/", 1024)
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected CGNAT rebinding error, got %v", err)
	}
	if got := dialer.CallCount(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
}

func TestClientRejectsRedirectToPrivateIPBeforeSecondDial(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("93.184.216.34"),
	}}
	dialer := &scriptedDialer{responses: []string{
		redirectResponse("http://192.168.0.10/private"),
		okResponse("should-not-run"),
	}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	_, err := client.Get(context.Background(), "http://example.test/", 1024)
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected redirect non-public IP error, got %v", err)
	}
	if got := dialer.CallCount(); got != 1 {
		t.Fatalf("dial calls = %d, want only initial public request", got)
	}
}

func TestClientRejectsRedirectToCGNATBeforeSecondDial(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("93.184.216.34"),
	}}
	dialer := &scriptedDialer{responses: []string{
		redirectResponse("http://100.100.100.200/latest/meta-data"),
		okResponse("should-not-run"),
	}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	_, err := client.Get(context.Background(), "http://example.test/", 1024)
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected redirect CGNAT rejection, got %v", err)
	}
	if got := dialer.CallCount(); got != 1 {
		t.Fatalf("dial calls = %d, want only initial public request", got)
	}
}

func TestClientRejectsLocalhostAndRFC1918Targets(t *testing.T) {
	client := Client{DialContext: (&scriptedDialer{}).DialContext, Timeout: time.Second}
	for _, rawURL := range []string{
		"http://localhost:8080/",
		"http://service.localhost/",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://100.100.100.200/",
		"http://198.18.0.1/",
		"http://240.0.0.1/",
		"http://255.255.255.255/",
		"http://[2001:db8::1]/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			_, err := client.Get(context.Background(), rawURL, 1024)
			if err == nil {
				t.Fatal("expected private/local target rejection")
			}
		})
	}
}

func TestClientRejectsDNSNamesResolvingToSpecialUseAddresses(t *testing.T) {
	for _, rawIP := range []string{"100.100.100.200", "198.18.0.1", "240.0.0.1", "2001:db8::1"} {
		t.Run(rawIP, func(t *testing.T) {
			client := Client{
				Resolver: ResolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
					return ipAddrs(rawIP), nil
				}),
				DialContext: (&scriptedDialer{}).DialContext,
				Timeout:     time.Second,
			}
			if _, err := client.Get(context.Background(), "http://example.test/", 1024); err == nil ||
				!strings.Contains(err.Error(), "non-public IP") {
				t.Fatalf("special-use DNS result %s error = %v", rawIP, err)
			}
		})
	}
}

func TestClientRejectsNonASCIIHostBeforeDNSOrDial(t *testing.T) {
	resolverCalls := 0
	dialer := &scriptedDialer{responses: []string{okResponse("should-not-run")}}
	client := Client{
		Resolver: ResolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
			resolverCalls++
			return ipAddrs("93.184.216.34"), nil
		}),
		DialContext: dialer.DialContext,
		Timeout:     time.Second,
	}
	if _, err := client.Get(t.Context(), "http://İ.example/api", 1024); err == nil ||
		!strings.Contains(err.Error(), "non-ASCII URL hostnames") {
		t.Fatalf("non-ASCII host error = %v", err)
	}
	if resolverCalls != 0 || dialer.CallCount() != 0 {
		t.Fatalf("non-ASCII host reached resolver/dialer: resolver=%d dialer=%d", resolverCalls, dialer.CallCount())
	}
}

func TestIsPublicIPRequiresGloballyRoutableAddress(t *testing.T) {
	for _, rawIP := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		if !IsPublicIP(net.ParseIP(rawIP)) {
			t.Fatalf("globally routable address rejected: %s", rawIP)
		}
	}
	for _, rawIP := range []string{
		"0.0.0.0",
		"10.0.0.1",
		"100.64.0.1",
		"100.100.100.200",
		"127.0.0.1",
		"169.254.169.254",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"255.255.255.255",
		"::1",
		"64:ff9b::1",
		"2001:db8::1",
		"fc00::1",
		"fe80::1",
		"ff00::1",
	} {
		if IsPublicIP(net.ParseIP(rawIP)) {
			t.Fatalf("non-global address accepted: %s", rawIP)
		}
	}
}

func TestBackendDefaultHTTPClientRejectsPrivateBaseURL(t *testing.T) {
	client := NewTavilyClient(BackendClientOptions{
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "tvly-test",
		MaxRetries: -1,
	})

	_, err := client.Search(context.Background(), SearchRequest{Query: "agent docs", Limit: 1})
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected private backend base URL rejection, got %v", err)
	}
}

func TestClientFetchesNormalPublicHost(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("93.184.216.34"),
	}}
	dialer := &scriptedDialer{responses: []string{okResponse("ok")}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	resp, err := client.Get(context.Background(), "http://example.test/page", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "ok" || resp.ContentType != "text/plain" || resp.URL != "http://example.test/page" || resp.StatusCode != 200 {
		t.Fatalf("response = %#v body=%q", resp, resp.Body)
	}
	calls := dialer.Calls()
	if len(calls) != 1 || calls[0] != "93.184.216.34:80" {
		t.Fatalf("dial calls = %#v", calls)
	}
}

func TestAllowedHTTPSURLPrefixesMatchExactOriginAndPath(t *testing.T) {
	resolver := ResolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.test" {
			return nil, fmt.Errorf("unexpected host %q", host)
		}
		return ipAddrs("93.184.216.34"), nil
	})
	client := Client{
		Resolver:           resolver,
		AllowedURLPrefixes: []string{"https://example.test/api/v2/content"},
	}
	for _, rawURL := range []string{
		"https://example.test/api/v2/content",
		"https://example.test/api/v2/content?limit=10",
		"https://example.test:443/api/v2/content?cursor=next",
		"https://example.test:0443/api/v2/content?cursor=next",
	} {
		if _, err := client.ValidatePublicHTTPURL(context.Background(), rawURL); err != nil {
			t.Fatalf("allowed URL %q rejected: %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{
		"http://example.test/api/v2/content",
		"https://other.example/api/v2/content",
		"https://example.test/api/v2/content/items",
		"https://example.test/api/v2/content-private",
		"https://example.test/api/v2/other",
		"https://example.test/api/v2/content/blocks/id/progress",
		"https://example.test/api/v2/content/%69tem",
		"https://example.test/api/v2/content/%2e%2e/private",
		"https://example.test/api/v2/content/%252e%252e/private",
		"https://example.test/api/v2/content/item%20name",
		"https://İ.example/api/v2/content",
		"https://[fe80::1%25eth0]/api/v2/content",
		"https://example.test//api/v2/content",
	} {
		if _, err := client.ValidatePublicHTTPURL(context.Background(), rawURL); err == nil {
			t.Fatalf("disallowed URL %q was accepted", rawURL)
		}
	}
}

func TestAllowedHTTPSURLPathPrefixesMatchOnlyOriginAndPathBoundary(t *testing.T) {
	resolver := ResolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.test" {
			return nil, fmt.Errorf("unexpected host %q", host)
		}
		return ipAddrs("93.184.216.34"), nil
	})
	client := Client{
		Resolver:               resolver,
		AllowedURLPathPrefixes: []string{"https://example.test/api"},
		RequireHTTPS:           true,
	}
	for _, rawURL := range []string{
		"https://example.test/api",
		"https://example.test/api/items",
		"https://example.test:443/api/items?cursor=next",
	} {
		if _, err := client.ValidatePublicHTTPURL(t.Context(), rawURL); err != nil {
			t.Fatalf("allowed path-prefix URL %q rejected: %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{
		"http://example.test/api",
		"https://example.test/api-private",
		"https://other.example/api/items",
		"https://example.test/api/%2e%2e/private",
	} {
		if _, err := client.ValidatePublicHTTPURL(t.Context(), rawURL); err == nil {
			t.Fatalf("disallowed path-prefix URL %q was accepted", rawURL)
		}
	}
	redirectClient := client.publicHTTPClient()
	start, err := http.NewRequest(http.MethodGet, "https://example.test/api/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := http.NewRequest(http.MethodGet, "https://example.test/api/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := redirectClient.CheckRedirect(inside, []*http.Request{start}); err != nil {
		t.Fatalf("inside-prefix redirect rejected: %v", err)
	}
	outside, err := http.NewRequest(http.MethodGet, "https://evil.example/api/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := redirectClient.CheckRedirect(outside, []*http.Request{start}); err == nil ||
		!strings.Contains(err.Error(), "outside allowed HTTPS path prefixes") {
		t.Fatalf("outside-host redirect error = %v", err)
	}
}

func TestRequireHTTPSWithoutPrefixesRejectsHTTP(t *testing.T) {
	client := Client{
		Resolver: ResolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return ipAddrs("93.184.216.34"), nil
		}),
		RequireHTTPS: true,
	}
	if _, err := client.ValidatePublicHTTPURL(t.Context(), "https://example.test/anything"); err != nil {
		t.Fatalf("unrestricted HTTPS rejected: %v", err)
	}
	if _, err := client.ValidatePublicHTTPURL(t.Context(), "http://example.test/anything"); err == nil {
		t.Fatal("RequireHTTPS accepted HTTP")
	}
}

func TestAllowedContentBlocksPathDoesNotAuthorizeProgressDescendant(t *testing.T) {
	client := Client{
		Resolver: ResolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return ipAddrs("93.184.216.34"), nil
		}),
		AllowedURLPrefixes: []string{"https://nareshka.example/api/v2/content/blocks"},
	}
	if _, err := client.ValidatePublicHTTPURL(t.Context(), "https://nareshka.example/api/v2/content/blocks?limit=10"); err != nil {
		t.Fatalf("exact blocks path rejected: %v", err)
	}
	if _, err := client.ValidatePublicHTTPURL(t.Context(), "https://nareshka.example/api/v2/content/blocks/id/progress"); err == nil {
		t.Fatal("blocks path authorized an unlisted progress descendant")
	}
}

func TestAllowedHTTPSURLPrefixesRejectRedirectOutsideScope(t *testing.T) {
	client := Client{
		Resolver: ResolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return ipAddrs("93.184.216.34"), nil
		}),
		AllowedURLPrefixes: []string{"https://example.test/api"},
	}
	httpClient := client.publicHTTPClient()
	allowed, err := http.NewRequest(http.MethodGet, "https://example.test/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := http.NewRequest(http.MethodGet, "https://example.test/api?cursor=next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpClient.CheckRedirect(inside, []*http.Request{allowed}); err != nil {
		t.Fatalf("same exact-path redirect rejected: %v", err)
	}
	outside, err := http.NewRequest(http.MethodGet, "https://example.test/admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpClient.CheckRedirect(outside, []*http.Request{allowed}); err == nil ||
		!strings.Contains(err.Error(), "outside allowed_url_prefixes") {
		t.Fatalf("outside redirect error = %v", err)
	}
	for _, test := range []struct {
		rawURL string
		want   string
	}{
		{rawURL: "https://example.test/api/%69tem", want: "unescaped canonical path"},
		{rawURL: "https://example.test/api/%252e%252e/private", want: "unescaped canonical path"},
		{rawURL: "https://example.test/api/%252fprivate", want: "unescaped canonical path"},
		{rawURL: "https://İ.example/api", want: "non-ASCII URL hostnames"},
		{rawURL: "https://example.test//api", want: "empty path segments"},
	} {
		escaped, err := http.NewRequest(http.MethodGet, test.rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := httpClient.CheckRedirect(escaped, []*http.Request{allowed}); err == nil ||
			!strings.Contains(err.Error(), test.want) {
			t.Fatalf("redirect %q error = %v, want %q", test.rawURL, err, test.want)
		}
	}
}

func TestNormalizeAllowedHTTPSURLPrefixesIsStrictAndDeterministic(t *testing.T) {
	got, err := NormalizeAllowedHTTPSURLPrefixes([]string{
		"https://b.example:0443/api/",
		"https://a.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "https://a.example/,https://b.example/api" {
		t.Fatalf("normalized prefixes = %#v", got)
	}
	for _, values := range [][]string{
		{" https://example.com/api"},
		{"http://example.com/api"},
		{"https://user@example.com/api"},
		{"https://example.com/api?tenant=1"},
		{"https://example.com/%61pi"},
		{"https://İ.example/api"},
		{"https://[fe80::1%25eth0]/api"},
		{"https://example.com//api"},
		{"https://example.com/api//child"},
		{"https://example.com/api", "https://example.com/api/"},
	} {
		if _, err := NormalizeAllowedHTTPSURLPrefixes(values); err == nil {
			t.Fatalf("invalid prefixes accepted: %#v", values)
		}
	}
}

func TestTavilySearchAndExtract(t *testing.T) {
	var seenAuth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"One","url":"https://example.com/1","content":"short snippet","score":0.9,"raw_content":"must not parse"}]}`))
		case "/extract":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/1","raw_content":"full extracted text"}],"failed_results":[{"url":"https://bad.example","error":"blocked"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client := NewTavilyClient(BackendClientOptions{BaseURL: server.URL, APIKey: "tvly-secret", HTTPClient: server.Client()})

	search, err := client.Search(context.Background(), SearchRequest{Query: "hello", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if search.Backend != BackendTavily || search.Usage.APICalls != 1 || len(search.Results) != 1 ||
		search.Results[0].Content != "short snippet" || strings.Contains(search.Results[0].Content, "must not parse") {
		t.Fatalf("search = %#v", search)
	}
	extract, err := client.Extract(context.Background(), ExtractRequest{URLs: []string{"https://example.com/1", "https://bad.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(extract.Results) != 2 || extract.Results[0].Text != "full extracted text" || extract.Results[1].Error != "blocked" {
		t.Fatalf("extract = %#v", extract)
	}
	for _, auth := range seenAuth {
		if auth != "Bearer tvly-secret" {
			t.Fatalf("auth header = %q", auth)
		}
	}
}

func TestExaSearchRetryAfterAndContents(t *testing.T) {
	var calls int
	var slept []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "exa-secret" {
			t.Fatalf("missing x-api-key: %q", r.Header.Get("x-api-key"))
		}
		switch r.URL.Path {
		case "/search":
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"slow down"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"Two","url":"https://example.com/2","text":"raw page text should not leak","score":0.8}],"costDollars":{"total":0.001}}`))
		case "/contents":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"Two","url":"https://example.com/2","text":"exa extracted text"}],"costDollars":{"total":0.002}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client := NewExaClient(BackendClientOptions{
		BaseURL:    server.URL,
		APIKey:     "exa-secret",
		HTTPClient: server.Client(),
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	})

	search, err := client.Search(context.Background(), SearchRequest{Query: "hello", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("retry calls=%d slept=%v", calls, slept)
	}
	if len(search.Results) != 1 || search.Results[0].Content != "" || search.Usage.CostUSD != 0.001 {
		t.Fatalf("search = %#v", search)
	}
	extract, err := client.Extract(context.Background(), ExtractRequest{URLs: []string{"https://example.com/2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(extract.Results) != 1 || extract.Results[0].Text != "exa extracted text" || extract.Usage.CostUSD != 0.002 {
		t.Fatalf("extract = %#v", extract)
	}
}

type scriptedResolver struct {
	mu      sync.Mutex
	answers [][]net.IPAddr
}

func (r *scriptedResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.answers) == 0 {
		return nil, fmt.Errorf("no resolver answer for %s", host)
	}
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return answer, nil
}

type scriptedDialer struct {
	mu        sync.Mutex
	calls     []string
	responses []string
}

func (d *scriptedDialer) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, address)
	if len(d.responses) == 0 {
		d.mu.Unlock()
		return nil, fmt.Errorf("no response for %s", address)
	}
	response := d.responses[0]
	d.responses = d.responses[1:]
	d.mu.Unlock()

	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		var request string
		buf := make([]byte, 256)
		for !strings.Contains(request, "\r\n\r\n") && len(request) < 8192 {
			n, err := server.Read(buf)
			if n > 0 {
				request += string(buf[:n])
			}
			if err != nil {
				return
			}
		}
		_, _ = io.WriteString(server, response)
	}()
	return client, nil
}

func (d *scriptedDialer) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *scriptedDialer) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func ipAddrs(values ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		out = append(out, net.IPAddr{IP: net.ParseIP(value)})
	}
	return out
}

func okResponse(body string) string {
	return fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}

func redirectResponse(location string) string {
	return "HTTP/1.1 302 Found\r\nLocation: " + location + "\r\nContent-Length: 0\r\n\r\n"
}
