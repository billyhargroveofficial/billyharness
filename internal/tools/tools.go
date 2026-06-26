package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	defaultWebBytes = 64 * 1024
	maxWebBytes     = 512 * 1024
	maxWriteBytes   = 2 * 1024 * 1024
	maxExecOutput   = 512 * 1024
)

type Result struct {
	Content string
}

type Tool struct {
	Spec    protocol.ToolSpec
	Handler func(context.Context, json.RawMessage) (Result, error)
}

type Registry struct {
	cfg   config.Config
	tools map[string]Tool
}

func NewRegistry(cfg config.Config) *Registry {
	r := &Registry{cfg: cfg, tools: map[string]Tool{}}
	r.addTime()
	r.addFSRead()
	r.addFSList()
	r.addFSSearch()
	r.addFSWrite()
	r.addFSMkdir()
	r.addShellExec()
	r.addWebFetch()
	r.addWebSearch()
	r.addWebCrawl()
	return r
}

func (r *Registry) Specs() []protocol.ToolSpec {
	specs := make([]protocol.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, tool.Spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

func (r *Registry) Call(ctx context.Context, call protocol.ToolCall) (Result, error) {
	tool, ok := r.tools[call.Name]
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %s", call.Name)
	}
	return tool.Handler(ctx, call.Arguments)
}

func (r *Registry) add(tool Tool) {
	r.tools[tool.Spec.Name] = tool
}

func (r *Registry) addTime() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "time_now",
			Description: "Return the current UTC time.",
			Parameters:  raw(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Content: time.Now().UTC().Format(time.RFC3339Nano)}, nil
		},
	})
}

func (r *Registry) addFSRead() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_read_file",
			Description: "Read a UTF-8 file from the allowed workspace.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			bytes, err := os.ReadFile(path)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: truncate(string(bytes), r.cfg.MaxToolOutputBytes)}, nil
		},
	})
}

func (r *Registry) addFSList() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_list",
			Description: "List files under an allowed workspace directory.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer","default":100}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Path  string `json:"path"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 500 {
				in.Limit = 100
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return Result{}, err
			}
			var lines []string
			for i, entry := range entries {
				if i >= in.Limit {
					lines = append(lines, fmt.Sprintf("...[truncated at %d]", in.Limit))
					break
				}
				lines = append(lines, entry.Name())
			}
			return Result{Content: strings.Join(lines, "\n")}, nil
		},
	})
}

func (r *Registry) addFSSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_search",
			Description: "Search allowed workspace files for a literal query.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"},"path":{"type":"string","default":"."},"limit":{"type":"integer","default":100}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query string `json:"query"`
				Path  string `json:"path"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Path == "" {
				in.Path = "."
			}
			if in.Limit <= 0 || in.Limit > 500 {
				in.Limit = 100
			}
			base, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			var hits []string
			_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || len(hits) >= in.Limit {
					return nil
				}
				if sensitive(path) {
					return nil
				}
				bytes, err := os.ReadFile(path)
				if err != nil || len(bytes) > 2*1024*1024 {
					return nil
				}
				for n, line := range strings.Split(string(bytes), "\n") {
					if strings.Contains(strings.ToLower(line), strings.ToLower(in.Query)) {
						hits = append(hits, fmt.Sprintf("%s:%d: %s", path, n+1, truncate(strings.TrimSpace(line), 240)))
						if len(hits) >= in.Limit {
							return nil
						}
					}
				}
				return nil
			})
			return Result{Content: strings.Join(hits, "\n")}, nil
		},
	})
}

func (r *Registry) addFSWrite() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_write_file",
			Description: "Create, overwrite, or append to a UTF-8 file under the allowed workspace. Requires FAST_AGENT_AUTO_APPROVE_DANGEROUS=true.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"append":{"type":"boolean","default":false},"create_dirs":{"type":"boolean","default":true}},"required":["path","content"],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			if err := r.requireDangerous(); err != nil {
				return Result{}, err
			}
			var in struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				Append     bool   `json:"append"`
				CreateDirs *bool  `json:"create_dirs"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if len(in.Content) > maxWriteBytes {
				return Result{}, fmt.Errorf("content too large: %d bytes", len(in.Content))
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			createDirs := true
			if in.CreateDirs != nil {
				createDirs = *in.CreateDirs
			}
			if createDirs {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return Result{}, err
				}
			}
			flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			if in.Append {
				flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
			}
			file, err := os.OpenFile(path, flag, 0o644)
			if err != nil {
				return Result{}, err
			}
			defer file.Close()
			n, err := file.WriteString(in.Content)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: fmt.Sprintf("wrote %d bytes to %s", n, path)}, nil
		},
	})
}

func (r *Registry) addFSMkdir() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_make_dir",
			Description: "Create a directory under the allowed workspace. Requires FAST_AGENT_AUTO_APPROVE_DANGEROUS=true.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			if err := r.requireDangerous(); err != nil {
				return Result{}, err
			}
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			path, err := r.safePath(in.Path)
			if err != nil {
				return Result{}, err
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return Result{}, err
			}
			return Result{Content: "created " + path}, nil
		},
	})
}

func (r *Registry) addShellExec() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "shell_exec",
			Description: "Run a command by argv in an allowed workspace directory. Requires FAST_AGENT_AUTO_APPROVE_DANGEROUS=true.",
			Parameters:  raw(`{"type":"object","properties":{"argv":{"type":"array","items":{"type":"string"},"minItems":1},"cwd":{"type":"string","default":"."},"timeout_sec":{"type":"integer","default":20},"max_output_bytes":{"type":"integer","default":65536}},"required":["argv"],"additionalProperties":false}`),
			Risk:        protocol.RiskExecute,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			if err := r.requireDangerous(); err != nil {
				return Result{}, err
			}
			var in struct {
				Argv           []string `json:"argv"`
				CWD            string   `json:"cwd"`
				TimeoutSec     int      `json:"timeout_sec"`
				MaxOutputBytes int      `json:"max_output_bytes"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if len(in.Argv) == 0 || in.Argv[0] == "" {
				return Result{}, fmt.Errorf("argv required")
			}
			if in.CWD == "" {
				in.CWD = "."
			}
			if in.TimeoutSec <= 0 || in.TimeoutSec > 120 {
				in.TimeoutSec = 20
			}
			if in.MaxOutputBytes <= 0 || in.MaxOutputBytes > maxExecOutput {
				in.MaxOutputBytes = r.cfg.MaxToolOutputBytes
			}
			cwd, err := r.safePath(in.CWD)
			if err != nil {
				return Result{}, err
			}
			cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSec)*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cmdCtx, in.Argv[0], in.Argv[1:]...)
			cmd.Dir = cwd
			output, err := cmd.CombinedOutput()
			text := truncate(string(output), in.MaxOutputBytes)
			if cmdCtx.Err() != nil {
				return Result{Content: text}, cmdCtx.Err()
			}
			if err != nil {
				return Result{Content: text}, fmt.Errorf("command failed: %w", err)
			}
			return Result{Content: text}, nil
		},
	})
}

func (r *Registry) addWebFetch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "web_fetch",
			Description: "Fetch a public HTTP(S) URL and return extracted text plus discovered links.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"max_bytes":{"type":"integer","default":65536}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL      string `json:"url"`
				MaxBytes int    `json:"max_bytes"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			page, err := fetchPage(ctx, in.URL, boundedBytes(in.MaxBytes))
			if err != nil {
				return Result{}, err
			}
			out, _ := json.MarshalIndent(page, "", "  ")
			return Result{Content: string(out)}, nil
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
			Description: "Crawl public HTTP(S) pages breadth-first, optionally restricted to the start host.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"},"max_pages":{"type":"integer","default":3},"max_depth":{"type":"integer","default":1},"same_host":{"type":"boolean","default":true},"max_bytes_per_page":{"type":"integer","default":65536}},"required":["url"],"additionalProperties":false}`),
			Risk:        protocol.RiskNetwork,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				URL             string `json:"url"`
				MaxPages        int    `json:"max_pages"`
				MaxDepth        int    `json:"max_depth"`
				SameHost        *bool  `json:"same_host"`
				MaxBytesPerPage int    `json:"max_bytes_per_page"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			sameHost := true
			if in.SameHost != nil {
				sameHost = *in.SameHost
			}
			pages, err := crawl(ctx, in.URL, in.MaxPages, in.MaxDepth, sameHost, boundedBytes(in.MaxBytesPerPage))
			if err != nil {
				return Result{}, err
			}
			out, _ := json.MarshalIndent(pages, "", "  ")
			return Result{Content: string(out)}, nil
		},
	})
}

func (r *Registry) requireDangerous() error {
	if r.cfg.AutoApproveDangerous {
		return nil
	}
	return fmt.Errorf("tool disabled; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=true to enable write/execute tools")
}

func (r *Registry) safePath(input string) (string, error) {
	if input == "" {
		input = "."
	}
	if !filepath.IsAbs(input) {
		input = filepath.Join(r.relativeBase(), input)
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	if sensitive(path) {
		return "", fmt.Errorf("refusing sensitive path %s", path)
	}
	for _, root := range r.cfg.WorkspaceRoots {
		absRoot, _ := filepath.Abs(root)
		if path == absRoot || strings.HasPrefix(path, absRoot+string(os.PathSeparator)) {
			return path, nil
		}
	}
	return "", fmt.Errorf("path outside workspace roots: %s", path)
}

func (r *Registry) relativeBase() string {
	if len(r.cfg.WorkspaceRoots) > 0 && r.cfg.WorkspaceRoots[0] != "" {
		return r.cfg.WorkspaceRoots[0]
	}
	cwd, _ := os.Getwd()
	return cwd
}

func sensitive(path string) bool {
	lower := strings.ToLower(path)
	for _, needle := range []string{".env", ".ssh", "id_rsa", "id_ed25519", "auth.json", "token", "secret", ".aws", ".kube", ".docker"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

type fetchedPage struct {
	URL         string   `json:"url"`
	Status      int      `json:"status"`
	ContentType string   `json:"content_type"`
	Title       string   `json:"title,omitempty"`
	Text        string   `json:"text"`
	Links       []string `json:"links,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
}

type searchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type crawlItem struct {
	URL   string `json:"url"`
	Depth int    `json:"depth"`
}

type crawlPage struct {
	URL   string `json:"url"`
	Depth int    `json:"depth"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

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
	truncated := false
	if len(body) > maxBytes {
		truncated = true
		body = body[:maxBytes]
	}
	textBody := string(body)
	page := fetchedPage{
		URL:         finalURL,
		Status:      http.StatusOK,
		ContentType: contentType,
		Truncated:   truncated,
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

func crawl(ctx context.Context, rawURL string, maxPages, maxDepth int, sameHost bool, maxBytesPerPage int) ([]crawlPage, error) {
	if maxPages <= 0 || maxPages > 10 {
		maxPages = 3
	}
	if maxDepth < 0 || maxDepth > 2 {
		maxDepth = 1
	}
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

func httpGet(ctx context.Context, rawURL string, maxBytes int) ([]byte, string, string, error) {
	u, err := validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return nil, "", "", err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			_, err := validatePublicHTTPURL(req.Context(), req.URL.String())
			return err
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", "fast-agent-harness-go/0.1 (+https://localhost)")
	req.Header.Set("Accept", "text/html,text/plain,application/json,application/xml;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(limited), 1000))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return nil, "", "", err
	}
	return body, resp.Request.URL.String(), resp.Header.Get("Content-Type"), nil
}

func validatePublicHTTPURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL host required")
	}
	if err := validatePublicHost(ctx, host); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePublicHost(ctx context.Context, host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("refusing localhost URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("refusing non-public IP %s", ip)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("host resolved to no addresses")
	}
	for _, addr := range addrs {
		if !isPublicIP(addr.IP) {
			return fmt.Errorf("refusing host %s resolved to non-public IP %s", host, addr.IP)
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

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

func boundedBytes(n int) int {
	if n <= 0 {
		return defaultWebBytes
	}
	if n > maxWebBytes {
		return maxWebBytes
	}
	return n
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("...[truncated %d bytes]", len(s)-n)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
