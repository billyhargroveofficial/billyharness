package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const webCacheSchemaVersion = 1

type webCacheEntry struct {
	SchemaVersion int                 `json:"schema_version"`
	Key           string              `json:"key"`
	Tool          string              `json:"tool"`
	CreatedAt     time.Time           `json:"created_at"`
	ExpiresAt     time.Time           `json:"expires_at"`
	Page          *compactPage        `json:"page,omitempty"`
	Crawl         *compactCrawlOutput `json:"crawl,omitempty"`
}

type webCacheStatus struct {
	Enabled        bool   `json:"enabled"`
	CacheDir       string `json:"cache_dir"`
	TTLMS          int64  `json:"ttl_ms"`
	MaxBytes       int64  `json:"max_bytes"`
	Entries        int    `json:"entries"`
	Bytes          int64  `json:"bytes"`
	ExpiredEntries int    `json:"expired_entries"`
	ExpiredBytes   int64  `json:"expired_bytes"`
}

type webCacheFile struct {
	Path    string
	Size    int64
	ModTime time.Time
	Expired bool
}

func (r *Registry) webCacheKey(ctx context.Context, toolName, rawURL string, opts webFetchOptions, extra map[string]any) (string, bool) {
	if r == nil || !r.cfg.WebCacheEnabled || r.cfg.WebCacheTTL <= 0 || r.cfg.WebCacheMaxBytes <= 0 {
		return "", false
	}
	u, err := validatePublicHTTPURL(ctx, rawURL)
	if err != nil {
		return "", false
	}
	summaryCfg := r.webSummaryProviderConfig()
	payload := map[string]any{
		"version":                       webCacheSchemaVersion,
		"tool":                          toolName,
		"url":                           u.String(),
		"query":                         strings.TrimSpace(opts.Query),
		"max_bytes":                     boundedBytes(opts.MaxBytes),
		"max_chars":                     opts.MaxChars,
		"max_tokens":                    opts.MaxTokens,
		"max_total_tokens":              opts.MaxTotalTokens,
		"include_text":                  opts.IncludeText,
		"full_text":                     opts.FullText,
		"max_links":                     opts.MaxLinks,
		"web_summary_mode":              config.NormalizeWebSummaryMode(r.cfg.WebSummaryMode),
		"web_summary_provider":          summaryCfg.Provider,
		"web_summary_model":             summaryCfg.Model,
		"web_summary_max_input_tokens":  summaryCfg.WebSummaryMaxInputTokens,
		"web_summary_max_output_tokens": summaryCfg.WebSummaryMaxOutputTokens,
		"extra":                         extra,
	}
	bytes, _ := json.Marshal(payload)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), true
}

func (r *Registry) loadWebPageCache(key string) (compactPage, bool) {
	var entry webCacheEntry
	age, ok := r.loadWebCacheEntry(key, &entry)
	if !ok || entry.Page == nil || !webOutputRefExists(entry.Page.OutputRef) {
		return compactPage{}, false
	}
	page := *entry.Page
	page.applyWebCache(key, true, age, r.cfg.WebCacheTTL)
	return page, true
}

func (r *Registry) saveWebPageCache(key string, page compactPage) error {
	page.applyWebCache(key, false, 0, r.cfg.WebCacheTTL)
	return r.saveWebCacheEntry(key, webCacheEntry{Tool: "web_page", Page: &page})
}

func (r *Registry) loadWebCrawlCache(key string) (compactCrawlOutput, bool) {
	var entry webCacheEntry
	age, ok := r.loadWebCacheEntry(key, &entry)
	if !ok || entry.Crawl == nil || !webOutputRefExists(entry.Crawl.OutputRef) {
		return compactCrawlOutput{}, false
	}
	crawl := *entry.Crawl
	crawl.applyWebCache(key, true, age, r.cfg.WebCacheTTL)
	return crawl, true
}

func (r *Registry) saveWebCrawlCache(key string, crawl compactCrawlOutput) error {
	crawl.applyWebCache(key, false, 0, r.cfg.WebCacheTTL)
	return r.saveWebCacheEntry(key, webCacheEntry{Tool: "web_crawl", Crawl: &crawl})
}

func (r *Registry) loadWebCacheEntry(key string, out *webCacheEntry) (time.Duration, bool) {
	if key == "" || out == nil {
		return 0, false
	}
	bytes, err := os.ReadFile(r.webCachePath(key))
	if err != nil {
		return 0, false
	}
	if err := json.Unmarshal(bytes, out); err != nil {
		return 0, false
	}
	now := time.Now().UTC()
	if out.SchemaVersion != webCacheSchemaVersion || out.Key != key || !out.ExpiresAt.After(now) {
		_ = os.Remove(r.webCachePath(key))
		return 0, false
	}
	return now.Sub(out.CreatedAt), true
}

func (r *Registry) saveWebCacheEntry(key string, entry webCacheEntry) error {
	if key == "" || r == nil || !r.cfg.WebCacheEnabled || r.cfg.WebCacheTTL <= 0 || r.cfg.WebCacheMaxBytes <= 0 {
		return nil
	}
	now := time.Now().UTC()
	entry.SchemaVersion = webCacheSchemaVersion
	entry.Key = key
	entry.CreatedAt = now
	entry.ExpiresAt = now.Add(r.cfg.WebCacheTTL)
	path := r.webCachePath(key)
	if err := ensurePrivateToolDir(r.webCacheDir()); err != nil {
		return err
	}
	if err := ensurePrivateToolDir(filepath.Dir(path)); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, bytes, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return r.cleanupWebCache()
}

func (r *Registry) webCacheStatus() webCacheStatus {
	status := webCacheStatus{
		Enabled:  r != nil && r.cfg.WebCacheEnabled,
		CacheDir: r.webCacheDir(),
	}
	if r != nil {
		status.TTLMS = r.cfg.WebCacheTTL.Milliseconds()
		status.MaxBytes = r.cfg.WebCacheMaxBytes
	}
	files := r.webCacheFiles(time.Now().UTC())
	for _, file := range files {
		status.Entries++
		status.Bytes += file.Size
		if file.Expired {
			status.ExpiredEntries++
			status.ExpiredBytes += file.Size
		}
	}
	return status
}

func (r *Registry) clearWebCache() (int, int64, error) {
	var removed int
	var removedBytes int64
	for _, file := range r.webCacheFiles(time.Now().UTC()) {
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return removed, removedBytes, err
		}
		removed++
		removedBytes += file.Size
	}
	_ = removeEmptyDirs(r.webCacheDir())
	return removed, removedBytes, nil
}

func (r *Registry) cleanupWebCache() error {
	if r == nil || r.cfg.WebCacheMaxBytes <= 0 {
		return nil
	}
	now := time.Now().UTC()
	files := r.webCacheFiles(now)
	var kept []webCacheFile
	var total int64
	for _, file := range files {
		if file.Expired {
			_ = os.Remove(file.Path)
			continue
		}
		kept = append(kept, file)
		total += file.Size
	}
	if total <= r.cfg.WebCacheMaxBytes {
		_ = removeEmptyDirs(r.webCacheDir())
		return nil
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ModTime.Before(kept[j].ModTime) })
	for _, file := range kept {
		if total <= r.cfg.WebCacheMaxBytes {
			break
		}
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= file.Size
	}
	_ = removeEmptyDirs(r.webCacheDir())
	return nil
}

func (r *Registry) webCacheFiles(now time.Time) []webCacheFile {
	dir := r.webCacheDir()
	var files []webCacheFile
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, webCacheFile{
			Path:    path,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Expired: webCacheFileExpired(path, now),
		})
		return nil
	})
	return files
}

func webCacheFileExpired(path string, now time.Time) bool {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var entry webCacheEntry
	if err := json.Unmarshal(bytes, &entry); err != nil {
		return false
	}
	return entry.SchemaVersion != webCacheSchemaVersion || !entry.ExpiresAt.After(now)
}

func (r *Registry) webCacheDir() string {
	return filepath.Join(config.BillyHomeDir(), "web-cache")
}

func (r *Registry) webCachePath(key string) string {
	key = strings.TrimSpace(key)
	if len(key) < 2 {
		key = fmt.Sprintf("%064x", 0)
	}
	return filepath.Join(r.webCacheDir(), key[:2], key+".json")
}

func (page *compactPage) applyWebCache(key string, hit bool, age time.Duration, ttl time.Duration) {
	if page == nil {
		return
	}
	page.WebCacheHit = hit
	page.WebCacheKey = key
	page.WebCacheAgeMS = maxInt64(0, age.Milliseconds())
	page.WebCacheTTLMS = maxInt64(0, ttl.Milliseconds())
}

func (crawl *compactCrawlOutput) applyWebCache(key string, hit bool, age time.Duration, ttl time.Duration) {
	if crawl == nil {
		return
	}
	crawl.WebCacheHit = hit
	crawl.WebCacheKey = key
	crawl.WebCacheAgeMS = maxInt64(0, age.Milliseconds())
	crawl.WebCacheTTLMS = maxInt64(0, ttl.Milliseconds())
	for i := range crawl.Pages {
		crawl.Pages[i].WebCacheHit = hit
		crawl.Pages[i].WebCacheKey = key
		crawl.Pages[i].WebCacheAgeMS = crawl.WebCacheAgeMS
		crawl.Pages[i].WebCacheTTLMS = crawl.WebCacheTTLMS
	}
}

func webOutputRefExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func removeEmptyDirs(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			_ = removeEmptyDirs(filepath.Join(root, entry.Name()))
		}
	}
	return os.Remove(root)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
