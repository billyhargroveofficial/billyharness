package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	defaultFSSearchLimit = 100
	maxFSSearchLimit     = 500
	maxFSGrepContext     = 5
	maxFSGrepFileBytes   = 2 * 1024 * 1024
	maxFSGrepLineRunes   = 240
	maxFSGlobCandidates  = 10000
)

var errStopFSSearch = fmt.Errorf("stop filesystem search")

type fsGrepInput struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	Include         string `json:"include"`
	Limit           int    `json:"limit"`
	Offset          int    `json:"offset"`
	Context         int    `json:"context"`
	Before          int    `json:"before"`
	After           int    `json:"after"`
	CaseInsensitive bool   `json:"case_insensitive"`
	OutputMode      string `json:"output_mode"`
}

type fsGlobInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Type    string `json:"type"`
	Sort    string `json:"sort"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

type fsSearchStats struct {
	FilesScanned          int
	FilesMatched          int
	FilesSkippedSensitive int
	FilesSkippedOutside   int
	FilesSkippedLarge     int
	FilesSkippedBinary    int
	CandidatesTruncated   bool
}

type fsSearchFile struct {
	Path string
	Rel  string
	Info os.FileInfo
}

type fsGlobEntry struct {
	Rel     string
	Kind    string
	ModTime time.Time
	Size    int64
}

func (r *Registry) addFSGrep() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_grep",
			Description: "Search allowed workspace files with a bounded regular expression grep. Supports include glob, context lines, offset, limit, binary/large/sensitive skips, and deterministic truncation.",
			Parameters:  raw(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regular expression to search for."},"path":{"type":"string","default":".","description":"Workspace file or directory to search."},"include":{"type":"string","description":"Optional glob filter such as *.go or **/*.md."},"case_insensitive":{"type":"boolean","default":false},"output_mode":{"type":"string","enum":["content","files_with_matches","count"],"default":"content"},"context":{"type":"integer","default":0,"description":"Context lines before and after each returned match; clamped to 5."},"before":{"type":"integer","default":0},"after":{"type":"integer","default":0},"limit":{"type":"integer","default":100,"description":"Maximum returned matches or files, depending on output_mode; clamped to 500."},"offset":{"type":"integer","default":0,"description":"Number of matches or files to skip before returning results."}},"required":["pattern"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: r.handleFSGrep,
	})
}

func (r *Registry) addFSGlob() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_glob",
			Description: "Find allowed workspace paths with a bounded recursive glob. Supports file/dir/both filters, name or modified-time sorting, offset, limit, and sensitive-path skips.",
			Parameters:  raw(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern such as *.go or **/*.md."},"path":{"type":"string","default":".","description":"Workspace directory to search."},"type":{"type":"string","enum":["file","dir","both"],"default":"file"},"sort":{"type":"string","enum":["name","modified"],"default":"name"},"limit":{"type":"integer","default":100,"description":"Maximum returned paths; clamped to 500."},"offset":{"type":"integer","default":0,"description":"Number of matching paths to skip before returning results."}},"required":["pattern"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: r.handleFSGlob,
	})
}

func (r *Registry) handleFSGrep(ctx context.Context, args json.RawMessage) (Result, error) {
	var in fsGrepInput
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	in.Path = defaultString(in.Path, ".")
	in.OutputMode = defaultString(in.OutputMode, "content")
	if in.OutputMode != "content" && in.OutputMode != "files_with_matches" && in.OutputMode != "count" {
		err := fmt.Errorf("invalid output_mode %q", in.OutputMode)
		return errorResult("fs_grep_invalid_output_mode", err.Error()), err
	}
	in.Limit = clampPositive(in.Limit, defaultFSSearchLimit, maxFSSearchLimit)
	if in.Offset < 0 {
		in.Offset = 0
	}
	if in.Context > 0 {
		in.Before = in.Context
		in.After = in.Context
	}
	in.Before = clampRange(in.Before, 0, maxFSGrepContext)
	in.After = clampRange(in.After, 0, maxFSGrepContext)

	expr := in.Pattern
	if in.CaseInsensitive {
		expr = "(?i:" + expr + ")"
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		result := errorResult("fs_grep_invalid_regex", err.Error())
		return result, err
	}
	base, err := r.safePath(in.Path)
	if err != nil {
		return Result{}, err
	}
	var stats fsSearchStats
	files, err := r.collectSearchFiles(base, in.Include, &stats)
	if err != nil {
		return Result{}, err
	}
	if in.OutputMode != "content" {
		return runFSGrepSummaryMode(ctx, files, re, in, &stats)
	}
	var lines []string
	totalMatches := 0
	returnedMatches := 0
	truncated := false
	err = walkFSGrepFiles(ctx, files, re, in, &stats, func(file fsSearchFile, fileLines []string, lineIndex int, loc []int) error {
		totalMatches++
		if totalMatches <= in.Offset {
			return nil
		}
		if returnedMatches >= in.Limit {
			truncated = true
			return errStopFSSearch
		}
		returnedMatches++
		start := maxInt(0, lineIndex-in.Before)
		for i := start; i < lineIndex; i++ {
			lines = append(lines, formatFSGrepContextLine(file.Rel, i+1, fileLines[i]))
		}
		col := utf8.RuneCountInString(fileLines[lineIndex][:loc[0]]) + 1
		lines = append(lines, formatFSGrepMatchLine(file.Rel, lineIndex+1, col, fileLines[lineIndex]))
		end := minInt(len(fileLines)-1, lineIndex+in.After)
		for i := lineIndex + 1; i <= end; i++ {
			lines = append(lines, formatFSGrepContextLine(file.Rel, i+1, fileLines[i]))
		}
		return nil
	})
	if err != nil && err != errStopFSSearch {
		return Result{}, err
	}
	if len(lines) == 0 {
		lines = append(lines, "no matches")
	}
	nextOffset := 0
	if truncated {
		nextOffset = in.Offset + returnedMatches
		lines = append(lines, fmt.Sprintf("...[truncated; next_offset=%d]", nextOffset))
	}
	metadata := map[string]any{
		"tool":                    "fs_grep",
		"path":                    in.Path,
		"pattern":                 in.Pattern,
		"include":                 in.Include,
		"case_insensitive":        in.CaseInsensitive,
		"output_mode":             in.OutputMode,
		"limit":                   in.Limit,
		"offset":                  in.Offset,
		"next_offset":             nextOffset,
		"matches":                 totalMatches,
		"returned_matches":        returnedMatches,
		"files_scanned":           stats.FilesScanned,
		"files_matched":           stats.FilesMatched,
		"files_skipped_sensitive": stats.FilesSkippedSensitive,
		"files_skipped_outside":   stats.FilesSkippedOutside,
		"files_skipped_large":     stats.FilesSkippedLarge,
		"files_skipped_binary":    stats.FilesSkippedBinary,
		"truncated":               truncated,
		"display_summary":         fsGrepSummary(returnedMatches, stats.FilesMatched, truncated),
		"display_target":          in.Pattern,
	}
	return Result{Content: strings.Join(lines, "\n"), Metadata: metadata, Truncated: truncated}, nil
}

func (r *Registry) handleFSGlob(ctx context.Context, args json.RawMessage) (Result, error) {
	var in fsGlobInput
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	in.Path = defaultString(in.Path, ".")
	in.Type = defaultString(in.Type, "file")
	in.Sort = defaultString(in.Sort, "name")
	if in.Type != "file" && in.Type != "dir" && in.Type != "both" {
		err := fmt.Errorf("invalid type %q", in.Type)
		return errorResult("fs_glob_invalid_type", err.Error()), err
	}
	if in.Sort != "name" && in.Sort != "modified" {
		err := fmt.Errorf("invalid sort %q", in.Sort)
		return errorResult("fs_glob_invalid_sort", err.Error()), err
	}
	in.Limit = clampPositive(in.Limit, defaultFSSearchLimit, maxFSSearchLimit)
	if in.Offset < 0 {
		in.Offset = 0
	}
	base, err := r.safePath(in.Path)
	if err != nil {
		return Result{}, err
	}
	matcher, err := newFSGlobMatcher(in.Pattern)
	if err != nil {
		result := errorResult("fs_glob_invalid_pattern", err.Error())
		return result, err
	}
	var stats fsSearchStats
	entries, err := r.collectGlobEntries(ctx, base, matcher, in.Type, &stats)
	if err != nil {
		return Result{}, err
	}
	sortFSGlobEntries(entries, in.Sort)
	total := len(entries)
	start := minInt(in.Offset, total)
	end := minInt(start+in.Limit, total)
	returned := entries[start:end]
	truncated := end < total || stats.CandidatesTruncated
	nextOffset := 0
	if truncated {
		nextOffset = end
	}
	lines := make([]string, 0, len(returned)+1)
	for _, entry := range returned {
		line := entry.Rel
		if entry.Kind == "dir" {
			line += "/"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "no matches")
	}
	if truncated {
		lines = append(lines, fmt.Sprintf("...[truncated; next_offset=%d]", nextOffset))
	}
	metadata := map[string]any{
		"tool":                    "fs_glob",
		"path":                    in.Path,
		"pattern":                 in.Pattern,
		"type":                    in.Type,
		"sort":                    in.Sort,
		"limit":                   in.Limit,
		"offset":                  in.Offset,
		"next_offset":             nextOffset,
		"matches":                 total,
		"returned_matches":        len(returned),
		"files_skipped_sensitive": stats.FilesSkippedSensitive,
		"files_skipped_outside":   stats.FilesSkippedOutside,
		"truncated":               truncated,
		"display_summary":         fsGlobSummary(len(returned), total, truncated),
		"display_target":          in.Pattern,
	}
	return Result{Content: strings.Join(lines, "\n"), Metadata: metadata, Truncated: truncated}, nil
}

func runFSGrepSummaryMode(ctx context.Context, files []fsSearchFile, re *regexp.Regexp, in fsGrepInput, stats *fsSearchStats) (Result, error) {
	var lines []string
	totalMatches := 0
	matchedFiles := 0
	returned := 0
	truncated := false
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		fileLines, skipped, err := readFSGrepFile(file.Path, file.Info)
		if err != nil {
			return Result{}, err
		}
		switch skipped {
		case "large":
			stats.FilesSkippedLarge++
			continue
		case "binary":
			stats.FilesSkippedBinary++
			continue
		}
		stats.FilesScanned++
		count := 0
		for _, line := range fileLines {
			if re.FindStringIndex(line) != nil {
				count++
			}
		}
		if count == 0 {
			continue
		}
		matchedFiles++
		stats.FilesMatched++
		totalMatches += count
		if matchedFiles <= in.Offset {
			continue
		}
		if returned >= in.Limit {
			truncated = true
			break
		}
		returned++
		if in.OutputMode == "count" {
			lines = append(lines, fmt.Sprintf("%s: %d", file.Rel, count))
		} else {
			lines = append(lines, file.Rel)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "no matches")
	}
	nextOffset := 0
	if truncated {
		nextOffset = in.Offset + returned
		lines = append(lines, fmt.Sprintf("...[truncated; next_offset=%d]", nextOffset))
	}
	metadata := map[string]any{
		"tool":                    "fs_grep",
		"path":                    in.Path,
		"pattern":                 in.Pattern,
		"include":                 in.Include,
		"case_insensitive":        in.CaseInsensitive,
		"output_mode":             in.OutputMode,
		"limit":                   in.Limit,
		"offset":                  in.Offset,
		"next_offset":             nextOffset,
		"matches":                 totalMatches,
		"returned_matches":        returned,
		"files_scanned":           stats.FilesScanned,
		"files_matched":           stats.FilesMatched,
		"files_skipped_sensitive": stats.FilesSkippedSensitive,
		"files_skipped_outside":   stats.FilesSkippedOutside,
		"files_skipped_large":     stats.FilesSkippedLarge,
		"files_skipped_binary":    stats.FilesSkippedBinary,
		"truncated":               truncated,
		"display_summary":         fsGrepSummary(returned, stats.FilesMatched, truncated),
		"display_target":          in.Pattern,
	}
	return Result{Content: strings.Join(lines, "\n"), Metadata: metadata, Truncated: truncated}, nil
}

func walkFSGrepFiles(ctx context.Context, files []fsSearchFile, re *regexp.Regexp, in fsGrepInput, stats *fsSearchStats, visit func(fsSearchFile, []string, int, []int) error) error {
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		lines, skipped, err := readFSGrepFile(file.Path, file.Info)
		if err != nil {
			return err
		}
		switch skipped {
		case "large":
			stats.FilesSkippedLarge++
			continue
		case "binary":
			stats.FilesSkippedBinary++
			continue
		}
		stats.FilesScanned++
		fileMatched := false
		for i, line := range lines {
			loc := re.FindStringIndex(line)
			if loc == nil {
				continue
			}
			if !fileMatched {
				stats.FilesMatched++
				fileMatched = true
			}
			if err := visit(file, lines, i, loc); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Registry) collectSearchFiles(base, include string, stats *fsSearchStats) ([]fsSearchFile, error) {
	info, err := os.Stat(base)
	if err != nil {
		return nil, err
	}
	var files []fsSearchFile
	addFile := func(path string, info os.FileInfo) {
		rel := r.displayRelPath(path)
		if include != "" && !fsGlobPatternMatches(include, rel, false) {
			return
		}
		if _, err := r.safePath(path); err != nil {
			stats.FilesSkippedOutside++
			return
		}
		files = append(files, fsSearchFile{Path: path, Rel: rel, Info: info})
	}
	if !info.IsDir() {
		addFile(base, info)
		return files, nil
	}
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != base && sensitive(path) {
			stats.FilesSkippedSensitive++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.IsDir() {
			return nil
		}
		addFile(path, info)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, err
}

func (r *Registry) collectGlobEntries(ctx context.Context, base string, matcher *regexp.Regexp, typ string, stats *fsSearchStats) ([]fsGlobEntry, error) {
	var entries []fsGlobEntry
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == base {
			return nil
		}
		if sensitive(path) {
			stats.FilesSkippedSensitive++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if _, err := r.safePath(path); err != nil {
			stats.FilesSkippedOutside++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		kind := "file"
		if info.IsDir() {
			kind = "dir"
		}
		if typ != "both" && typ != kind {
			return nil
		}
		rel := r.displayRelPath(path)
		if !matcher.MatchString(rel) {
			return nil
		}
		if len(entries) >= maxFSGlobCandidates {
			stats.CandidatesTruncated = true
			return errStopFSSearch
		}
		entries = append(entries, fsGlobEntry{Rel: rel, Kind: kind, ModTime: info.ModTime(), Size: info.Size()})
		return nil
	})
	if err == errStopFSSearch {
		err = nil
	}
	return entries, err
}

func readFSGrepFile(path string, info os.FileInfo) ([]string, string, error) {
	if stat, err := os.Stat(path); err == nil {
		info = stat
	}
	if info.Size() > maxFSGrepFileBytes {
		return nil, "large", nil
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil
	}
	if strings.ContainsRune(string(bytes), '\x00') || !utf8.Valid(bytes) {
		return nil, "binary", nil
	}
	text := strings.ReplaceAll(string(bytes), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, "", nil
	}
	return strings.Split(text, "\n"), "", nil
}

func formatFSGrepMatchLine(rel string, lineNo, col int, line string) string {
	return fmt.Sprintf("%s:%d:%d: %s", rel, lineNo, col, compactFSGrepLine(line, col))
}

func formatFSGrepContextLine(rel string, lineNo int, line string) string {
	return fmt.Sprintf("%s-%d- %s", rel, lineNo, compactFSGrepLine(line, 0))
}

func compactFSGrepLine(line string, col int) string {
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) <= maxFSGrepLineRunes {
		return line
	}
	start := 0
	if col > maxFSGrepLineRunes/2 {
		start = col - maxFSGrepLineRunes/2
	}
	if start > len(runes)-maxFSGrepLineRunes {
		start = len(runes) - maxFSGrepLineRunes
	}
	if start < 0 {
		start = 0
	}
	end := minInt(len(runes), start+maxFSGrepLineRunes)
	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(runes) {
		suffix = "..."
	}
	return prefix + string(runes[start:end]) + suffix
}

func sortFSGlobEntries(entries []fsGlobEntry, sortMode string) {
	sort.Slice(entries, func(i, j int) bool {
		if sortMode == "modified" && !entries[i].ModTime.Equal(entries[j].ModTime) {
			return entries[i].ModTime.After(entries[j].ModTime)
		}
		return entries[i].Rel < entries[j].Rel
	})
}

func newFSGlobMatcher(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(filepath.ToSlash(pattern))
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	expr := fsGlobToRegexp(pattern)
	if !strings.Contains(pattern, "/") {
		expr = "(?:.*/)?" + expr
	}
	return regexp.Compile("^" + expr + "$")
}

func fsGlobPatternMatches(pattern, rel string, isDir bool) bool {
	matcher, err := newFSGlobMatcher(pattern)
	if err != nil {
		return false
	}
	if matcher.MatchString(filepath.ToSlash(rel)) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		return matcher.MatchString(filepath.Base(rel))
	}
	if isDir {
		return matcher.MatchString(strings.TrimSuffix(filepath.ToSlash(rel), "/"))
	}
	return false
}

func fsGlobToRegexp(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
			continue
		}
		if ch == '?' {
			b.WriteString("[^/]")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(ch)))
	}
	return b.String()
}

func (r *Registry) displayRelPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	for _, root := range r.toolPolicy.WorkspaceRoots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(absRoot, abs); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(abs))
}

func fsGrepSummary(matches, files int, truncated bool) string {
	summary := fmt.Sprintf("fs_grep %d match%s in %d file%s", matches, pluralSuffix(matches), files, pluralSuffix(files))
	if truncated {
		summary += " (truncated)"
	}
	return summary
}

func fsGlobSummary(returned, total int, truncated bool) string {
	summary := fmt.Sprintf("fs_glob %d path%s", returned, pluralSuffix(returned))
	if total != returned {
		summary += fmt.Sprintf(" of %d", total)
	}
	if truncated {
		summary += " (truncated)"
	}
	return summary
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func clampPositive(value, fallback, maxValue int) int {
	if value <= 0 {
		return fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampRange(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
