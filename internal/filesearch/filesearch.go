package filesearch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	DefaultCacheTTL = 2 * time.Second
	DefaultLimit    = 40
	MaxLimit        = 500

	maxCandidates     = 20000
	maxCommandOutput  = 4 * 1024 * 1024
	commandTimeout    = 2 * time.Second
	sourceGit         = "git"
	sourceRipgrep     = "rg"
	sourceWalk        = "walk"
	sourceMixed       = "mixed"
	candidateTypeFile = "file"
)

type Options struct {
	Roots []string
	Path  string
	Query string
	Limit int
	// Offset skips this many ranked matches before returning results.
	Offset int

	// Test hooks. Production callers should leave these false.
	DisableGit bool
	DisableRG  bool
}

type Match struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Score  int    `json:"score"`
	Source string `json:"source"`
}

type Result struct {
	Matches                 []Match `json:"matches"`
	Query                   string  `json:"query"`
	Path                    string  `json:"path"`
	Source                  string  `json:"source"`
	Total                   int     `json:"total"`
	Returned                int     `json:"returned"`
	Limit                   int     `json:"limit"`
	Offset                  int     `json:"offset"`
	NextOffset              int     `json:"next_offset"`
	Truncated               bool    `json:"truncated"`
	Candidates              int     `json:"candidates"`
	CandidatesTruncated     bool    `json:"candidates_truncated"`
	FilesSkippedSensitive   int     `json:"files_skipped_sensitive"`
	FilesSkippedOutsideRoot int     `json:"files_skipped_outside"`
}

type Resolver struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	cache map[string]cacheEntry
}

type cacheEntry struct {
	candidates []candidate
	stats      sourceStats
	source     string
	signature  string
	expires    time.Time
}

type candidate struct {
	Path   string
	Type   string
	Source string
}

type sourceStats struct {
	FilesSkippedSensitive   int
	FilesSkippedOutsideRoot int
	CandidatesTruncated     bool
}

type searchRoot struct {
	Root      string
	SearchRel string
	SearchAbs string
}

var defaultResolver = NewResolver(DefaultCacheTTL)

func NewResolver(ttl time.Duration) *Resolver {
	return &Resolver{ttl: ttl, now: time.Now, cache: map[string]cacheEntry{}}
}

func Find(ctx context.Context, opts Options) (Result, error) {
	return defaultResolver.Find(ctx, opts)
}

func (r *Resolver) Find(ctx context.Context, opts Options) (Result, error) {
	if r == nil {
		r = NewResolver(0)
	}
	opts.Query = strings.TrimSpace(opts.Query)
	opts.Path = strings.TrimSpace(opts.Path)
	opts.Limit = normalizeLimit(opts.Limit)
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	candidates, stats, source, err := r.candidates(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	matches := rankCandidates(candidates, opts.Query)
	total := len(matches)
	start := minInt(opts.Offset, total)
	end := minInt(start+opts.Limit, total)
	returned := append([]Match(nil), matches[start:end]...)
	truncated := end < total || stats.CandidatesTruncated
	nextOffset := 0
	if truncated {
		nextOffset = end
	}
	return Result{
		Matches:                 returned,
		Query:                   opts.Query,
		Path:                    defaultString(opts.Path, "."),
		Source:                  source,
		Total:                   total,
		Returned:                len(returned),
		Limit:                   opts.Limit,
		Offset:                  opts.Offset,
		NextOffset:              nextOffset,
		Truncated:               truncated,
		Candidates:              len(candidates),
		CandidatesTruncated:     stats.CandidatesTruncated,
		FilesSkippedSensitive:   stats.FilesSkippedSensitive,
		FilesSkippedOutsideRoot: stats.FilesSkippedOutsideRoot,
	}, nil
}

func (r *Resolver) candidates(ctx context.Context, opts Options) ([]candidate, sourceStats, string, error) {
	roots, err := normalizeRoots(opts.Roots)
	if err != nil {
		return nil, sourceStats{}, "", err
	}
	searchRoots, err := resolveSearchRoots(roots, opts.Path)
	if err != nil {
		return nil, sourceStats{}, "", err
	}
	if len(searchRoots) == 0 {
		return nil, sourceStats{}, "", fmt.Errorf("path outside workspace roots: %s", opts.Path)
	}
	key := cacheKey(searchRoots, opts)
	signature := cacheSignature(searchRoots)
	if entry, ok := r.cached(key, signature); ok {
		return append([]candidate(nil), entry.candidates...), entry.stats, entry.source, nil
	}
	candidates, stats, source, err := collectCandidates(ctx, searchRoots, opts)
	if err != nil {
		return nil, sourceStats{}, "", err
	}
	r.store(key, signature, candidates, stats, source)
	return candidates, stats, source, nil
}

func (r *Resolver) cached(key, signature string) (cacheEntry, bool) {
	if r == nil || r.ttl <= 0 {
		return cacheEntry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || entry.signature != signature || r.now().After(entry.expires) {
		return cacheEntry{}, false
	}
	return entry, true
}

func (r *Resolver) store(key, signature string, candidates []candidate, stats sourceStats, source string) {
	if r == nil || r.ttl <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = cacheEntry{
		candidates: append([]candidate(nil), candidates...),
		stats:      stats,
		source:     source,
		signature:  signature,
		expires:    r.now().Add(r.ttl),
	}
}

func collectCandidates(ctx context.Context, roots []searchRoot, opts Options) ([]candidate, sourceStats, string, error) {
	var all []candidate
	var stats sourceStats
	var sources []string
	seen := map[string]bool{}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, sourceStats{}, "", err
		}
		candidates, rootStats, source := collectCandidatesForRoot(ctx, root, opts)
		stats.FilesSkippedSensitive += rootStats.FilesSkippedSensitive
		stats.FilesSkippedOutsideRoot += rootStats.FilesSkippedOutsideRoot
		stats.CandidatesTruncated = stats.CandidatesTruncated || rootStats.CandidatesTruncated
		if len(candidates) == 0 {
			if source != "" {
				sources = append(sources, source)
			}
			continue
		}
		sources = append(sources, source)
		for _, item := range candidates {
			key := item.Type + "\x00" + item.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, item)
			if len(all) >= maxCandidates {
				stats.CandidatesTruncated = true
				break
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	return all, stats, combinedSource(sources), nil
}

func collectCandidatesForRoot(ctx context.Context, root searchRoot, opts Options) ([]candidate, sourceStats, string) {
	if !opts.DisableGit {
		if lines, ok := gitFiles(ctx, root); ok {
			candidates, stats := candidatesFromCommand(root, lines, sourceGit)
			if len(candidates) > 0 {
				return candidates, stats, sourceGit
			}
		}
	}
	if !opts.DisableRG {
		if lines, ok := rgFiles(ctx, root); ok {
			candidates, stats := candidatesFromCommand(root, lines, sourceRipgrep)
			if len(candidates) > 0 {
				return candidates, stats, sourceRipgrep
			}
		}
	}
	candidates, stats := walkFiles(ctx, root)
	return candidates, stats, sourceWalk
}

func gitFiles(ctx context.Context, root searchRoot) ([]string, bool) {
	args := []string{"-C", root.Root, "ls-files", "-co", "--exclude-standard", "-z"}
	if root.SearchRel != "." {
		args = append(args, "--", root.SearchRel)
	}
	out, ok := runBounded(ctx, "git", args, "")
	if !ok {
		return nil, false
	}
	return splitNUL(out), true
}

func rgFiles(ctx context.Context, root searchRoot) ([]string, bool) {
	out, ok := runBounded(ctx, "rg", []string{"--files", "--hidden", "--glob", "!.git"}, root.SearchAbs)
	if !ok {
		return nil, false
	}
	lines := splitLines(out)
	if root.SearchRel == "." {
		return lines, true
	}
	prefix := filepath.ToSlash(root.SearchRel)
	for i, line := range lines {
		lines[i] = prefix + "/" + line
	}
	return lines, true
}

func runBounded(parent context.Context, name string, args []string, dir string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxCommandOutput+1))
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil || ctx.Err() != nil || len(out) > maxCommandOutput {
		return nil, false
	}
	return out, true
}

func candidatesFromCommand(root searchRoot, lines []string, source string) ([]candidate, sourceStats) {
	var out []candidate
	var stats sourceStats
	seen := map[string]bool{}
	for _, line := range lines {
		rel := cleanRel(line)
		if rel == "" {
			continue
		}
		path := filepath.Join(root.Root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() {
			continue
		}
		addCandidate(&out, seen, &stats, root.Root, path, source)
		if len(out) >= maxCandidates {
			stats.CandidatesTruncated = true
			break
		}
	}
	return out, stats
}

func walkFiles(ctx context.Context, root searchRoot) ([]candidate, sourceStats) {
	var out []candidate
	var stats sourceStats
	seen := map[string]bool{}
	err := filepath.WalkDir(root.SearchAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path != root.SearchAbs && isSensitivePath(path) {
			stats.FilesSkippedSensitive++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		addCandidate(&out, seen, &stats, root.Root, path, sourceWalk)
		if len(out) >= maxCandidates {
			stats.CandidatesTruncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		return out, stats
	}
	return out, stats
}

func addCandidate(out *[]candidate, seen map[string]bool, stats *sourceStats, root, path, source string) {
	if isSensitivePath(path) {
		stats.FilesSkippedSensitive++
		return
	}
	if !insideRoot(root, path) {
		stats.FilesSkippedOutsideRoot++
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		stats.FilesSkippedOutsideRoot++
		return
	}
	rel = cleanRel(rel)
	if rel == "" || isSensitivePath(rel) {
		stats.FilesSkippedSensitive++
		return
	}
	key := candidateTypeFile + "\x00" + rel
	if seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, candidate{Path: rel, Type: candidateTypeFile, Source: source})
}

func rankCandidates(candidates []candidate, query string) []Match {
	query = normalizeQuery(query)
	matches := make([]Match, 0, len(candidates))
	for _, item := range candidates {
		score, ok := scorePath(item.Path, query)
		if !ok {
			continue
		}
		matches = append(matches, Match{Path: item.Path, Type: item.Type, Score: score, Source: item.Source})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if len(matches[i].Path) != len(matches[j].Path) {
			return len(matches[i].Path) < len(matches[j].Path)
		}
		return matches[i].Path < matches[j].Path
	})
	return matches
}

func scorePath(path, query string) (int, bool) {
	if query == "" {
		return 1, true
	}
	pathLower := strings.ToLower(filepath.ToSlash(path))
	baseLower := strings.ToLower(filepath.Base(pathLower))
	stemLower := strings.TrimSuffix(baseLower, filepath.Ext(baseLower))
	switch {
	case pathLower == query:
		return 120000 - len(pathLower), true
	case baseLower == query:
		return 110000 - len(pathLower), true
	case stemLower == query:
		return 100000 - len(pathLower), true
	case strings.HasPrefix(baseLower, query):
		return 90000 - len(pathLower), true
	case strings.Contains(baseLower, query):
		return 80000 - len(pathLower), true
	case strings.HasPrefix(pathLower, query):
		return 70000 - len(pathLower), true
	case strings.Contains(pathLower, query):
		return 60000 - len(pathLower), true
	default:
		if fuzzy := fuzzyScore(pathLower, query); fuzzy > 0 {
			return 30000 + fuzzy - len(pathLower), true
		}
		return 0, false
	}
}

func fuzzyScore(path, query string) int {
	if query == "" {
		return 1
	}
	score := 0
	last := -1
	for _, qr := range query {
		found := -1
		for i, pr := range path {
			if i <= last {
				continue
			}
			if unicode.ToLower(pr) == unicode.ToLower(qr) {
				found = i
				break
			}
		}
		if found < 0 {
			return 0
		}
		gap := found - last - 1
		score += maxInt(1, 40-gap)
		last = found
	}
	return score
}

func normalizeRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		roots = []string{cwd}
	}
	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no workspace roots configured")
	}
	return out, nil
}

func resolveSearchRoots(roots []string, input string) ([]searchRoot, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		input = "."
	}
	var out []searchRoot
	if filepath.IsAbs(input) {
		abs, err := filepath.Abs(input)
		if err != nil {
			return nil, err
		}
		for _, root := range roots {
			if insideRoot(root, abs) {
				rel, _ := filepath.Rel(root, abs)
				out = append(out, searchRoot{Root: root, SearchRel: cleanRel(defaultString(rel, ".")), SearchAbs: abs})
			}
		}
		return out, nil
	}
	rel := cleanRel(input)
	if rel == "" {
		rel = "."
	}
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return nil, fmt.Errorf("path outside workspace roots: %s", input)
	}
	for _, root := range roots {
		searchAbs := filepath.Join(root, filepath.FromSlash(rel))
		if !insideRoot(root, searchAbs) {
			continue
		}
		out = append(out, searchRoot{Root: root, SearchRel: rel, SearchAbs: searchAbs})
	}
	return out, nil
}

func insideRoot(root, path string) bool {
	policyRoot := resolvedPolicyPath(root)
	policyPath := resolvedPolicyPath(path)
	return policyPath == policyRoot || strings.HasPrefix(policyPath, policyRoot+string(os.PathSeparator))
}

func resolvedPolicyPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs, _ = filepath.Abs(abs)
	return filepath.Clean(abs)
}

func cacheKey(roots []searchRoot, opts Options) string {
	parts := []string{opts.Path}
	for _, root := range roots {
		parts = append(parts, root.Root+"="+root.SearchRel)
	}
	if opts.DisableGit {
		parts = append(parts, "no-git")
	}
	if opts.DisableRG {
		parts = append(parts, "no-rg")
	}
	return strings.Join(parts, "\x00")
}

func cacheSignature(roots []searchRoot) string {
	parts := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		for _, path := range []string{root.Root, root.SearchAbs} {
			info, err := os.Stat(path)
			if err != nil {
				parts = append(parts, path+"=missing")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%d:%d", path, info.ModTime().UnixNano(), info.Size()))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "|")
}

func normalizeQuery(query string) string {
	query = strings.ToLower(strings.TrimSpace(filepath.ToSlash(query)))
	query = strings.TrimPrefix(query, "./")
	return query
}

func cleanRel(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "." || value == "/" || strings.HasPrefix(value, "../") || value == ".." || filepath.IsAbs(value) {
		return ""
	}
	return value
}

func isSensitivePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, part := range strings.Split(lower, "/") {
		if part == ".git" || part == ".ssh" || part == ".aws" || part == ".kube" || part == ".docker" {
			return true
		}
	}
	for _, needle := range []string{".env", "id_rsa", "id_ed25519", "auth.json", "token", "secret"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func combinedSource(sources []string) string {
	var nonEmpty []string
	seen := map[string]bool{}
	for _, source := range sources {
		if source == "" || seen[source] {
			continue
		}
		seen[source] = true
		nonEmpty = append(nonEmpty, source)
	}
	if len(nonEmpty) == 0 {
		return sourceWalk
	}
	if len(nonEmpty) == 1 {
		return nonEmpty[0]
	}
	return sourceMixed
}

func splitNUL(out []byte) []string {
	parts := bytes.Split(out, []byte{0})
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			lines = append(lines, string(part))
		}
	}
	return lines
}

func splitLines(out []byte) []string {
	raw := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
